// Command migrate is a one-shot tool for DOTFILES-23: it copies the legacy
// per-project JSON files under the registry data dir into the new SQLite
// registry.db, then verifies the migration is lossless by re-reading the
// original JSON files and diffing them against what landed in the DB.
//
// Design note: this is deliberately its OWN standalone `package main` under
// cmd/migrate, duplicating the (small) schema + insert SQL from ../../store.go
// rather than importing the sibling `registry` package's unexported `store`
// type. store.go/registry.go are already shipped + reviewed for DOTFILES-23
// steps 1-4; refactoring them into an importable internal package just to
// share this one-shot tool's code would touch already-QA'd files for no
// lasting benefit. The schema/INSERT statements here are kept in exact sync
// with store.go's createSchema()/SetProject()/WritePlan()/WriteAudit()/
// WriteDeployCheck() by hand.
//
// Idempotency note: running this tool twice against the same data dir is
// safe (won't crash / corrupt data) but is NOT exactly-once for the
// append-only tables (audit, deploy_checks) — a second run will duplicate
// those rows, same limitation store.go's own WriteAudit/WriteDeployCheck
// have. projects and plans are upserted (ON CONFLICT), so re-running those
// is fully idempotent. This is a one-shot migration tool, so that's
// acceptable.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

func dataDir() string {
	if d := os.Getenv("REGISTRY_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "registry", "data")
}

func main() {
	dir := dataDir()

	backupDir, err := backupDataDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: backup of %s failed, aborting before touching anything: %v\n", dir, err)
		os.Exit(1)
	}
	fmt.Printf("Backed up %s -> %s\n", dir, backupDir)

	dbPath := filepath.Join(dir, "registry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: enable WAL: %v\n", err)
		os.Exit(1)
	}
	if err := createSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: create schema: %v\n", err)
		os.Exit(1)
	}

	projects, err := discoverProjects(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: discover projects in %s: %v\n", dir, err)
		os.Exit(1)
	}

	allPassed := true
	for _, project := range projects {
		if err := migrateProject(db, dir, project); err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: migrate project %q: %v\n", project, err)
			os.Exit(1)
		}
	}

	for _, project := range projects {
		passed, report := verifyProject(db, dir, project)
		fmt.Println(report)
		if !passed {
			allPassed = false
		}
	}

	if !allPassed {
		fmt.Println("\nRESULT: FAIL — see diffs above")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: PASS — all projects migrated with zero diffs")
}

// ── backup ───────────────────────────────────────────────────────────────────

func backupDataDir(dir string) (string, error) {
	ts := time.Now().Format("20060102-150405")
	backupDir := dir + ".bak-" + ts
	if err := copyDir(dir, backupDir); err != nil {
		return "", err
	}
	return backupDir, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// ── schema (duplicated from store.go createSchema) ──────────────────────────

func createSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			name TEXT PRIMARY KEY,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS plans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			ticket TEXT NOT NULL,
			status TEXT,
			created_at TEXT NOT NULL,
			data TEXT NOT NULL,
			UNIQUE(project, ticket)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_plans_project_ticket ON plans(project, ticket)`,
		`CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			date TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_project_date ON audit(project, date)`,
		`CREATE TABLE IF NOT EXISTS issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			reported_at TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_project_reported_at ON issues(project, reported_at)`,
		`CREATE TABLE IF NOT EXISTS deploy_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			recorded_at TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deploy_checks_project_recorded_at ON deploy_checks(project, recorded_at)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// ── discovery ────────────────────────────────────────────────────────────────

func discoverProjects(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var projects []string
	for _, e := range entries {
		if e.IsDir() {
			projects = append(projects, e.Name())
		}
	}
	sort.Strings(projects)
	return projects, nil
}

// ── migration ────────────────────────────────────────────────────────────────

func migrateProject(db *sql.DB, dataDir, project string) error {
	dir := filepath.Join(dataDir, project)

	if raw, ok, err := readJSONFile(filepath.Join(dir, "project.json")); err != nil {
		return fmt.Errorf("read project.json: %w", err)
	} else if ok {
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return fmt.Errorf("unmarshal project.json: %w", err)
		}
		if _, err := db.Exec(
			`INSERT INTO projects (name, data) VALUES (?, ?)
			 ON CONFLICT(name) DO UPDATE SET data=excluded.data`,
			project, string(raw),
		); err != nil {
			return fmt.Errorf("insert project: %w", err)
		}
	}

	planFiles, err := filepath.Glob(filepath.Join(dir, "plans", "*.json"))
	if err != nil {
		return fmt.Errorf("glob plans: %w", err)
	}
	sort.Strings(planFiles)
	for _, pf := range planFiles {
		raw, err := os.ReadFile(pf)
		if err != nil {
			return fmt.Errorf("read %s: %w", pf, err)
		}
		var plan map[string]any
		if err := json.Unmarshal(raw, &plan); err != nil {
			return fmt.Errorf("unmarshal %s: %w", pf, err)
		}
		ticket := plan["ticket"]
		ticketStr, _ := ticket.(string)
		if ticketStr == "" {
			base := filepath.Base(pf)
			ticketStr = base[:len(base)-len(filepath.Ext(base))]
		}
		status, _ := plan["status"].(string)
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := db.Exec(
			`INSERT INTO plans (project, ticket, status, created_at, data) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(project, ticket) DO UPDATE SET status=excluded.status, data=excluded.data`,
			project, ticketStr, status, now, string(raw),
		); err != nil {
			return fmt.Errorf("insert plan %s: %w", pf, err)
		}
	}

	if raw, ok, err := readJSONFile(filepath.Join(dir, "audit.json")); err != nil {
		return fmt.Errorf("read audit.json: %w", err)
	} else if ok {
		var entries []map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("unmarshal audit.json: %w", err)
		}
		for _, entry := range entries {
			date, _ := entry["date"].(string)
			b, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("marshal audit entry: %w", err)
			}
			if _, err := db.Exec(
				`INSERT INTO audit (project, date, data) VALUES (?, ?, ?)`,
				project, date, string(b),
			); err != nil {
				return fmt.Errorf("insert audit entry: %w", err)
			}
		}
	}

	if raw, ok, err := readJSONFile(filepath.Join(dir, "issues.json")); err != nil {
		return fmt.Errorf("read issues.json: %w", err)
	} else if ok {
		var entries []map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("unmarshal issues.json: %w", err)
		}
		for _, entry := range entries {
			reportedAt, _ := entry["_recorded_at"].(string)
			if reportedAt == "" {
				reportedAt = time.Now().UTC().Format(time.RFC3339)
			}
			b, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("marshal issue entry: %w", err)
			}
			if _, err := db.Exec(
				`INSERT INTO issues (project, reported_at, data) VALUES (?, ?, ?)`,
				project, reportedAt, string(b),
			); err != nil {
				return fmt.Errorf("insert issue entry: %w", err)
			}
		}
	}

	if raw, ok, err := readJSONFile(filepath.Join(dir, "deploy_checks.json")); err != nil {
		return fmt.Errorf("read deploy_checks.json: %w", err)
	} else if ok {
		var entries []map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("unmarshal deploy_checks.json: %w", err)
		}
		for _, entry := range entries {
			recordedAt, _ := entry["date"].(string)
			if recordedAt == "" {
				recordedAt = time.Now().UTC().Format(time.RFC3339)
			}
			b, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("marshal deploy_check entry: %w", err)
			}
			if _, err := db.Exec(
				`INSERT INTO deploy_checks (project, recorded_at, data) VALUES (?, ?, ?)`,
				project, recordedAt, string(b),
			); err != nil {
				return fmt.Errorf("insert deploy_check entry: %w", err)
			}
		}
	}

	return nil
}

func readJSONFile(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return raw, true, nil
}

// ── verification ─────────────────────────────────────────────────────────────

func verifyProject(db *sql.DB, dataDir, project string) (bool, string) {
	dir := filepath.Join(dataDir, project)
	passed := true
	lines := []string{fmt.Sprintf("=== %s ===", project)}

	// project.json
	if raw, ok, err := readJSONFile(filepath.Join(dir, "project.json")); err != nil {
		passed, lines = false, append(lines, fmt.Sprintf("  project.json: FAIL (read error: %v)", err))
	} else if ok {
		var want map[string]any
		if err := json.Unmarshal(raw, &want); err != nil {
			passed, lines = false, append(lines, fmt.Sprintf("  project.json: FAIL (unmarshal error: %v)", err))
		} else {
			var gotRaw string
			if err := db.QueryRow(`SELECT data FROM projects WHERE name = ?`, project).Scan(&gotRaw); err != nil {
				passed, lines = false, append(lines, fmt.Sprintf("  project.json: FAIL (db query error: %v)", err))
			} else {
				var got map[string]any
				json.Unmarshal([]byte(gotRaw), &got)
				if reflect.DeepEqual(want, got) {
					lines = append(lines, "  project.json: PASS")
				} else {
					passed = false
					lines = append(lines, fmt.Sprintf("  project.json: FAIL\n    want: %v\n    got:  %v", want, got))
				}
			}
		}
	}

	// plans/*.json
	planFiles, _ := filepath.Glob(filepath.Join(dir, "plans", "*.json"))
	sort.Strings(planFiles)
	for _, pf := range planFiles {
		raw, err := os.ReadFile(pf)
		if err != nil {
			passed, lines = false, append(lines, fmt.Sprintf("  %s: FAIL (read error: %v)", filepath.Base(pf), err))
			continue
		}
		var want map[string]any
		if err := json.Unmarshal(raw, &want); err != nil {
			passed, lines = false, append(lines, fmt.Sprintf("  %s: FAIL (unmarshal error: %v)", filepath.Base(pf), err))
			continue
		}
		ticketStr, _ := want["ticket"].(string)
		if ticketStr == "" {
			base := filepath.Base(pf)
			ticketStr = base[:len(base)-len(filepath.Ext(base))]
		}
		var gotRaw string
		if err := db.QueryRow(`SELECT data FROM plans WHERE project = ? AND ticket = ?`, project, ticketStr).Scan(&gotRaw); err != nil {
			passed = false
			lines = append(lines, fmt.Sprintf("  plans/%s.json: FAIL (db query error: %v)", ticketStr, err))
			continue
		}
		var got map[string]any
		json.Unmarshal([]byte(gotRaw), &got)
		if reflect.DeepEqual(want, got) {
			lines = append(lines, fmt.Sprintf("  plans/%s.json: PASS", ticketStr))
		} else {
			passed = false
			lines = append(lines, fmt.Sprintf("  plans/%s.json: FAIL\n    want: %v\n    got:  %v", ticketStr, want, got))
		}
	}

	// audit.json
	if raw, ok, err := readJSONFile(filepath.Join(dir, "audit.json")); err != nil {
		passed, lines = false, append(lines, fmt.Sprintf("  audit.json: FAIL (read error: %v)", err))
	} else if ok {
		var want []map[string]any
		json.Unmarshal(raw, &want)
		rows, err := db.Query(`SELECT data FROM audit WHERE project = ? ORDER BY id ASC`, project)
		if err != nil {
			passed, lines = false, append(lines, fmt.Sprintf("  audit.json: FAIL (db query error: %v)", err))
		} else {
			var got []map[string]any
			for rows.Next() {
				var gr string
				rows.Scan(&gr)
				var e map[string]any
				json.Unmarshal([]byte(gr), &e)
				got = append(got, e)
			}
			rows.Close()
			if len(want) != len(got) {
				passed = false
				lines = append(lines, fmt.Sprintf("  audit.json: FAIL (count mismatch: want %d, got %d)", len(want), len(got)))
			} else {
				ok := true
				for i := range want {
					if !reflect.DeepEqual(want[i], got[i]) {
						ok = false
						lines = append(lines, fmt.Sprintf("  audit.json[%d]: FAIL\n    want: %v\n    got:  %v", i, want[i], got[i]))
					}
				}
				if ok {
					lines = append(lines, fmt.Sprintf("  audit.json: PASS (%d entries)", len(want)))
				} else {
					passed = false
				}
			}
		}
	}

	// issues.json
	if raw, ok, err := readJSONFile(filepath.Join(dir, "issues.json")); err != nil {
		passed, lines = false, append(lines, fmt.Sprintf("  issues.json: FAIL (read error: %v)", err))
	} else if ok {
		var want []map[string]any
		json.Unmarshal(raw, &want)
		rows, err := db.Query(`SELECT data FROM issues WHERE project = ? ORDER BY id ASC`, project)
		if err != nil {
			passed, lines = false, append(lines, fmt.Sprintf("  issues.json: FAIL (db query error: %v)", err))
		} else {
			var got []map[string]any
			for rows.Next() {
				var gr string
				rows.Scan(&gr)
				var e map[string]any
				json.Unmarshal([]byte(gr), &e)
				got = append(got, e)
			}
			rows.Close()
			if len(want) != len(got) {
				passed = false
				lines = append(lines, fmt.Sprintf("  issues.json: FAIL (count mismatch: want %d, got %d)", len(want), len(got)))
			} else {
				ok := true
				for i := range want {
					if !reflect.DeepEqual(want[i], got[i]) {
						ok = false
						lines = append(lines, fmt.Sprintf("  issues.json[%d]: FAIL\n    want: %v\n    got:  %v", i, want[i], got[i]))
					}
				}
				if ok {
					lines = append(lines, fmt.Sprintf("  issues.json: PASS (%d entries)", len(want)))
				} else {
					passed = false
				}
			}
		}
	}

	// deploy_checks.json
	if raw, ok, err := readJSONFile(filepath.Join(dir, "deploy_checks.json")); err != nil {
		passed, lines = false, append(lines, fmt.Sprintf("  deploy_checks.json: FAIL (read error: %v)", err))
	} else if ok {
		var want []map[string]any
		json.Unmarshal(raw, &want)
		rows, err := db.Query(`SELECT data FROM deploy_checks WHERE project = ? ORDER BY id ASC`, project)
		if err != nil {
			passed, lines = false, append(lines, fmt.Sprintf("  deploy_checks.json: FAIL (db query error: %v)", err))
		} else {
			var got []map[string]any
			for rows.Next() {
				var gr string
				rows.Scan(&gr)
				var e map[string]any
				json.Unmarshal([]byte(gr), &e)
				got = append(got, e)
			}
			rows.Close()
			if len(want) != len(got) {
				passed = false
				lines = append(lines, fmt.Sprintf("  deploy_checks.json: FAIL (count mismatch: want %d, got %d)", len(want), len(got)))
			} else {
				ok := true
				for i := range want {
					if !reflect.DeepEqual(want[i], got[i]) {
						ok = false
						lines = append(lines, fmt.Sprintf("  deploy_checks.json[%d]: FAIL\n    want: %v\n    got:  %v", i, want[i], got[i]))
					}
				}
				if ok {
					lines = append(lines, fmt.Sprintf("  deploy_checks.json: PASS (%d entries)", len(want)))
				} else {
					passed = false
				}
			}
		}
	}

	status := "PASS"
	if !passed {
		status = "FAIL"
	}
	lines = append(lines, fmt.Sprintf("  -> %s: %s", project, status))
	return passed, joinLines(lines)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
