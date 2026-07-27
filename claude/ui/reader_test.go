package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupFixtureDir creates a temp registry data dir containing a registry.db
// seeded with the store.go schema (projects/plans/audit/deploy_checks/issues,
// each with a JSON `data` column), and sets REGISTRY_DATA_DIR to point at it.
func setupFixtureDir(t *testing.T) (dataDir string, cleanup func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "registry-ui-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	db := openFixtureDB(t, dir)
	db.Close()
	t.Setenv("REGISTRY_DATA_DIR", dir)
	return dir, func() { os.RemoveAll(dir) }
}

// openFixtureDB opens (creating if needed) the registry.db under dir and
// ensures the schema mirrors claude/mcp/server/store.go's createSchema.
func openFixtureDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "registry.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
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
		`CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			date TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			reported_at TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS deploy_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			recorded_at TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return db
}

func marshalFixture(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

// seedProject inserts a row into the projects table.
func seedProject(t *testing.T, dir, name string, data map[string]any) {
	t.Helper()
	db := openFixtureDB(t, dir)
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO projects (name, data) VALUES (?, ?)
		 ON CONFLICT(name) DO UPDATE SET data=excluded.data`,
		name, marshalFixture(t, data),
	); err != nil {
		t.Fatalf("seedProject: %v", err)
	}
}

// seedPlan inserts a row into the plans table.
func seedPlan(t *testing.T, dir, project, ticket string, data map[string]any) {
	t.Helper()
	db := openFixtureDB(t, dir)
	defer db.Close()
	status, _ := data["status"].(string)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO plans (project, ticket, status, created_at, data) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project, ticket) DO UPDATE SET status=excluded.status, data=excluded.data`,
		project, ticket, status, now, marshalFixture(t, data),
	); err != nil {
		t.Fatalf("seedPlan: %v", err)
	}
}

// seedAudit inserts a row into the audit table.
func seedAudit(t *testing.T, dir, project string, entries []map[string]any) {
	t.Helper()
	db := openFixtureDB(t, dir)
	defer db.Close()
	for _, e := range entries {
		date, _ := e["date"].(string)
		if _, err := db.Exec(
			`INSERT INTO audit (project, date, data) VALUES (?, ?, ?)`,
			project, date, marshalFixture(t, e),
		); err != nil {
			t.Fatalf("seedAudit: %v", err)
		}
	}
}

// seedDeployChecks inserts rows into the deploy_checks table.
func seedDeployChecks(t *testing.T, dir, project string, entries []map[string]any) {
	t.Helper()
	db := openFixtureDB(t, dir)
	defer db.Close()
	for _, e := range entries {
		recordedAt, _ := e["date"].(string)
		if recordedAt == "" {
			recordedAt, _ = e["_recorded_at"].(string)
		}
		if _, err := db.Exec(
			`INSERT INTO deploy_checks (project, recorded_at, data) VALUES (?, ?, ?)`,
			project, recordedAt, marshalFixture(t, e),
		); err != nil {
			t.Fatalf("seedDeployChecks: %v", err)
		}
	}
}

// seedIssues inserts rows into the issues table.
func seedIssues(t *testing.T, dir, project string, entries []map[string]any) {
	t.Helper()
	db := openFixtureDB(t, dir)
	defer db.Close()
	for _, e := range entries {
		reportedAt, _ := e["_recorded_at"].(string)
		if _, err := db.Exec(
			`INSERT INTO issues (project, reported_at, data) VALUES (?, ?, ?)`,
			project, reportedAt, marshalFixture(t, e),
		); err != nil {
			t.Fatalf("seedIssues: %v", err)
		}
	}
}

// ── ReadProjects ───────────────────────────────────────────────────────────────

func TestReadProjects_ReturnsList(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProject(t, dir, "beta", map[string]any{"name": "beta"})

	projects, err := ReadProjects()
	if err != nil {
		t.Fatalf("ReadProjects() error: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("want 2 projects, got %d: %v", len(projects), projects)
	}
}

func TestReadProjects_EmptyDir(t *testing.T) {
	_, cleanup := setupFixtureDir(t)
	defer cleanup()

	projects, err := ReadProjects()
	if err != nil {
		t.Fatalf("ReadProjects() error: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("want 0 projects, got %d", len(projects))
	}
}

// ── ReadProject ────────────────────────────────────────────────────────────────

func TestReadProject_ReturnsProject(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "myproject", map[string]any{
		"name": "myproject",
		"repo": map[string]any{"workspace": "tazgreenwood"},
	})

	proj, err := ReadProject("myproject")
	if err != nil {
		t.Fatalf("ReadProject() error: %v", err)
	}
	if proj.Name != "myproject" {
		t.Errorf("want Name=myproject, got %q", proj.Name)
	}
}

func TestReadProject_MissingReturnsError(t *testing.T) {
	_, cleanup := setupFixtureDir(t)
	defer cleanup()

	_, err := ReadProject("nonexistent")
	if err == nil {
		t.Fatal("want error for missing project, got nil")
	}
}

// ── ReadPlans ──────────────────────────────────────────────────────────────────

func TestReadPlans_ReturnsPlanMeta(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedPlan(t, dir, "myproject", "DOTFILES-1", map[string]any{
		"ticket":  "DOTFILES-1",
		"summary": "First plan",
		"plan_steps": []map[string]any{
			{"step": 1, "status": "done"},
		},
	})
	seedPlan(t, dir, "myproject", "DOTFILES-2", map[string]any{
		"ticket":  "DOTFILES-2",
		"summary": "Second plan",
		"plan_steps": []map[string]any{
			{"step": 1, "status": "active"},
		},
	})

	plans, err := ReadPlans("myproject")
	if err != nil {
		t.Fatalf("ReadPlans() error: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("want 2 plans, got %d", len(plans))
	}
}

func TestReadPlans_StatusShippedWhenAllStepsDone(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedPlan(t, dir, "myproject", "DOTFILES-1", map[string]any{
		"ticket":  "DOTFILES-1",
		"summary": "All done",
		"plan_steps": []map[string]any{
			{"step": 1, "status": "done"},
			{"step": 2, "status": "done"},
		},
	})

	plans, err := ReadPlans("myproject")
	if err != nil {
		t.Fatalf("ReadPlans() error: %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("want 1 plan, got 0")
	}
	if plans[0].Status != "shipped" {
		t.Errorf("want Status=shipped, got %q", plans[0].Status)
	}
}

func TestReadPlans_StatusActiveWhenStepPending(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedPlan(t, dir, "myproject", "DOTFILES-3", map[string]any{
		"ticket":  "DOTFILES-3",
		"summary": "In progress",
		"plan_steps": []map[string]any{
			{"step": 1, "status": "done"},
			{"step": 2, "status": "active"},
		},
	})

	plans, err := ReadPlans("myproject")
	if err != nil {
		t.Fatalf("ReadPlans() error: %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("want 1 plan, got 0")
	}
	if plans[0].Status != "active" {
		t.Errorf("want Status=active, got %q", plans[0].Status)
	}
}

// ── ReadPlan ───────────────────────────────────────────────────────────────────

func TestReadPlan_ReturnsPlan(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedPlan(t, dir, "myproject", "TICKET-1", map[string]any{
		"ticket":  "TICKET-1",
		"summary": "Test plan",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "Do the thing", "status": "active"},
		},
	})

	plan, err := ReadPlan("myproject", "TICKET-1")
	if err != nil {
		t.Fatalf("ReadPlan() error: %v", err)
	}
	if plan.Ticket != "TICKET-1" {
		t.Errorf("want Ticket=TICKET-1, got %q", plan.Ticket)
	}
	if plan.Summary != "Test plan" {
		t.Errorf("want Summary='Test plan', got %q", plan.Summary)
	}
}

func TestReadPlan_MissingReturnsError(t *testing.T) {
	_, cleanup := setupFixtureDir(t)
	defer cleanup()

	_, err := ReadPlan("myproject", "TICKET-999")
	if err == nil {
		t.Fatal("want error for missing plan, got nil")
	}
}

// ── ReadAudit ──────────────────────────────────────────────────────────────────

func TestReadAudit_ReturnsAllEntries(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedAudit(t, dir, "myproject", []map[string]any{
		{"ticket": "DOTFILES-1", "type": "feature", "summary": "Add thing", "date": "2026-05-01"},
		{"ticket": "DOTFILES-2", "type": "bugfix", "summary": "Fix bug", "date": "2026-06-01"},
	})

	got, err := ReadAudit("myproject", "", "")
	if err != nil {
		t.Fatalf("ReadAudit() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
}

func TestReadAudit_FiltersBySince(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedAudit(t, dir, "myproject", []map[string]any{
		{"ticket": "DOTFILES-1", "date": "2026-04-15"},
		{"ticket": "DOTFILES-2", "date": "2026-05-01"},
		{"ticket": "DOTFILES-3", "date": "2026-06-10"},
	})

	got, err := ReadAudit("myproject", "2026-05-01", "")
	if err != nil {
		t.Fatalf("ReadAudit() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries (since 2026-05-01), got %d", len(got))
	}
}

func TestReadAudit_FiltersByUntil(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedAudit(t, dir, "myproject", []map[string]any{
		{"ticket": "DOTFILES-1", "date": "2026-04-15"},
		{"ticket": "DOTFILES-2", "date": "2026-05-01"},
		{"ticket": "DOTFILES-3", "date": "2026-06-10"},
	})

	got, err := ReadAudit("myproject", "", "2026-05-01")
	if err != nil {
		t.Fatalf("ReadAudit() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries (until 2026-05-01), got %d", len(got))
	}
}

func TestReadAudit_FiltersBySinceAndUntil(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedAudit(t, dir, "myproject", []map[string]any{
		{"ticket": "DOTFILES-1", "date": "2026-04-15"},
		{"ticket": "DOTFILES-2", "date": "2026-05-01"},
		{"ticket": "DOTFILES-3", "date": "2026-06-10"},
	})

	got, err := ReadAudit("myproject", "2026-05-01", "2026-05-31")
	if err != nil {
		t.Fatalf("ReadAudit() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry (2026-05-01 to 2026-05-31), got %d", len(got))
	}
	if got[0].Ticket != "DOTFILES-2" {
		t.Errorf("want Ticket=DOTFILES-2, got %q", got[0].Ticket)
	}
}

func TestReadAudit_MissingFileReturnsEmpty(t *testing.T) {
	_, cleanup := setupFixtureDir(t)
	defer cleanup()

	got, err := ReadAudit("nonexistent", "", "")
	if err != nil {
		t.Fatalf("ReadAudit() for missing project should not error, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 entries for missing project, got %d", len(got))
	}
}

// ── ReadDeployChecks ───────────────────────────────────────────────────────────

func TestReadDeployChecks_ReturnsAllEntries(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedDeployChecks(t, dir, "myproject", []map[string]any{
		{"status": "pass", "summary": "Deploy checked out", "date": "2026-05-01"},
		{"status": "fail", "summary": "Deploy failed health check", "date": "2026-06-01"},
	})

	got, err := ReadDeployChecks("myproject", "", "")
	if err != nil {
		t.Fatalf("ReadDeployChecks() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
}

func TestReadDeployChecks_FiltersBySince(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedDeployChecks(t, dir, "myproject", []map[string]any{
		{"status": "pass", "date": "2026-04-15"},
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-10"},
	})

	got, err := ReadDeployChecks("myproject", "2026-05-01", "")
	if err != nil {
		t.Fatalf("ReadDeployChecks() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries (since 2026-05-01), got %d", len(got))
	}
}

func TestReadDeployChecks_FiltersByUntil(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedDeployChecks(t, dir, "myproject", []map[string]any{
		{"status": "pass", "date": "2026-04-15"},
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-10"},
	})

	got, err := ReadDeployChecks("myproject", "", "2026-05-01")
	if err != nil {
		t.Fatalf("ReadDeployChecks() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries (until 2026-05-01), got %d", len(got))
	}
}

func TestReadDeployChecks_FiltersBySinceAndUntil(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedDeployChecks(t, dir, "myproject", []map[string]any{
		{"status": "pass", "date": "2026-04-15"},
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-10"},
	})

	got, err := ReadDeployChecks("myproject", "2026-05-01", "2026-05-31")
	if err != nil {
		t.Fatalf("ReadDeployChecks() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry (2026-05-01 to 2026-05-31), got %d", len(got))
	}
	if got[0].Status != "pass" {
		t.Errorf("want Status=pass, got %q", got[0].Status)
	}
}

func TestReadDeployChecks_MissingFileReturnsEmpty(t *testing.T) {
	_, cleanup := setupFixtureDir(t)
	defer cleanup()

	got, err := ReadDeployChecks("nonexistent", "", "")
	if err != nil {
		t.Fatalf("ReadDeployChecks() for missing project should not error, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 entries for missing project, got %d", len(got))
	}
}

// ── ReadIssues ─────────────────────────────────────────────────────────────────

func TestReadIssues_ReturnsEntries(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedIssues(t, dir, "myproject", []map[string]any{
		{"tool": "registry_get_project", "error": "not found", "severity": "error", "_recorded_at": "2026-06-16T10:00:00Z"},
		{"tool": "registry_set", "error": "write failed", "severity": "warning", "_recorded_at": "2026-06-16T11:00:00Z"},
	})

	got, err := ReadIssues("myproject", "")
	if err != nil {
		t.Fatalf("ReadIssues() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 issues, got %d", len(got))
	}
}

func TestReadIssues_MissingFileReturnsEmpty(t *testing.T) {
	_, cleanup := setupFixtureDir(t)
	defer cleanup()

	got, err := ReadIssues("nonexistent", "")
	if err != nil {
		t.Fatalf("ReadIssues() for missing project should not error, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 issues, got %d", len(got))
	}
}

func TestReadIssues_FiltersBySeverity(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedIssues(t, dir, "myproject", []map[string]any{
		{"tool": "registry_get_project", "error": "not found", "severity": "error", "_recorded_at": "2026-06-16T10:00:00Z"},
		{"tool": "registry_set", "error": "write failed", "severity": "warning", "_recorded_at": "2026-06-16T11:00:00Z"},
		{"tool": "registry_get_plan", "error": "missing plan", "severity": "error", "_recorded_at": "2026-06-16T12:00:00Z"},
	})

	got, err := ReadIssues("myproject", "error")
	if err != nil {
		t.Fatalf("ReadIssues() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 error-severity issues, got %d", len(got))
	}
	for _, e := range got {
		if e.Severity != "error" {
			t.Errorf("want severity=error, got %q", e.Severity)
		}
	}
}
