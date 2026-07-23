package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ── store: SQLite-backed persistence (DOTFILES-23) ──────────────────────────────
//
// Single SQLite DB (WAL mode) replacing per-project JSON files under
// data/{project}/*.json. Tables: projects, plans, audit, issues, deploy_checks.

type store struct {
	db *sql.DB
}

func newStore(dbPath string) (*store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	s := &store{db: db}
	if err := s.createSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) Close() error {
	return s.db.Close()
}

func (s *store) createSchema() error {
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
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// ── projects ─────────────────────────────────────────────────────────────────

func (s *store) SetProject(name string, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO projects (name, data) VALUES (?, ?)
		 ON CONFLICT(name) DO UPDATE SET data=excluded.data`,
		name, string(b),
	)
	return err
}

func (s *store) GetProject(name string) (map[string]any, error) {
	var raw string
	err := s.db.QueryRow(`SELECT data FROM projects WHERE name = ?`, name).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *store) ListProjects() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

// ── plans ────────────────────────────────────────────────────────────────────

func (s *store) WritePlan(project, ticket string, plan map[string]any) error {
	b, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	status, _ := plan["status"].(string)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(
		`INSERT INTO plans (project, ticket, status, created_at, data) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project, ticket) DO UPDATE SET status=excluded.status, data=excluded.data`,
		project, ticket, status, now, string(b),
	)
	return err
}

func (s *store) GetPlan(project, ticket string) (map[string]any, error) {
	var id int64
	var createdAt, raw string
	err := s.db.QueryRow(
		`SELECT id, created_at, data FROM plans WHERE project = ? AND ticket = ?`,
		project, ticket,
	).Scan(&id, &createdAt, &raw)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, err
	}
	data["id"] = id
	data["created_at"] = createdAt
	return data, nil
}

func (s *store) ListPlans(project string) ([]map[string]any, error) {
	rows, err := s.db.Query(
		`SELECT id, ticket, created_at, data FROM plans WHERE project = ? ORDER BY id`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []map[string]any
	for rows.Next() {
		var id int64
		var ticket, createdAt, raw string
		if err := rows.Scan(&id, &ticket, &createdAt, &raw); err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return nil, err
		}
		data["id"] = id
		data["created_at"] = createdAt
		if t, _ := data["ticket"].(string); t == "" {
			data["ticket"] = ticket
		}
		plans = append(plans, data)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return plans, nil
}

func (s *store) UpdateStep(project, ticket string, stepIndex int, status string) error {
	var raw string
	err := s.db.QueryRow(
		`SELECT data FROM plans WHERE project = ? AND ticket = ?`,
		project, ticket,
	).Scan(&raw)
	if err != nil {
		return err
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return err
	}
	steps, ok := plan["plan_steps"].([]any)
	if !ok || stepIndex < 0 || stepIndex >= len(steps) {
		return fmt.Errorf("step index %d out of range", stepIndex)
	}
	step, ok := steps[stepIndex].(map[string]any)
	if !ok {
		return fmt.Errorf("step %d is not an object", stepIndex)
	}
	step["status"] = status

	b, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE plans SET data = ?, status = ? WHERE project = ? AND ticket = ?`,
		string(b), status, project, ticket,
	)
	return err
}

// ── audit ────────────────────────────────────────────────────────────────────

func (s *store) WriteAudit(project string, entry map[string]any) (int, error) {
	date, _ := entry["date"].(string)
	b, err := json.Marshal(entry)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(
		`INSERT INTO audit (project, date, data) VALUES (?, ?, ?)`,
		project, date, string(b),
	); err != nil {
		return 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE project = ?`, project).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) GetAudit(project, since, until string) ([]map[string]any, int, error) {
	query := `SELECT data FROM audit WHERE project = ?`
	args := []any{project}
	if since != "" {
		query += ` AND date >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND date <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY date ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return entries, len(entries), nil
}

// ── issues ───────────────────────────────────────────────────────────────────

func (s *store) WriteIssue(project string, issue map[string]any) (int, error) {
	reportedAt := time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(issue)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(
		`INSERT INTO issues (project, reported_at, data) VALUES (?, ?, ?)`,
		project, reportedAt, string(b),
	); err != nil {
		return 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE project = ?`, project).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) GetIssues(project string) ([]map[string]any, error) {
	rows, err := s.db.Query(
		`SELECT data FROM issues WHERE project = ? ORDER BY reported_at ASC`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var issues []map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var issue map[string]any
		if err := json.Unmarshal([]byte(raw), &issue); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return issues, nil
}

// ── deploy checks ────────────────────────────────────────────────────────────

func (s *store) WriteDeployCheck(project string, check map[string]any) (int, error) {
	// Prefer the entry's own "date" field (so since/until filtering matches the
	// deploy check's reported date, not the write time), fall back to now.
	recordedAt, _ := check["date"].(string)
	if recordedAt == "" {
		recordedAt = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(check)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(
		`INSERT INTO deploy_checks (project, recorded_at, data) VALUES (?, ?, ?)`,
		project, recordedAt, string(b),
	); err != nil {
		return 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM deploy_checks WHERE project = ?`, project).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) GetDeployChecks(project, since, until string) ([]map[string]any, int, error) {
	query := `SELECT data FROM deploy_checks WHERE project = ?`
	args := []any{project}
	if since != "" {
		query += ` AND recorded_at >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND recorded_at <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY recorded_at ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return entries, len(entries), nil
}
