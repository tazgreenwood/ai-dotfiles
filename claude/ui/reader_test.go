package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setupFixtureDir creates a temp registry data dir and sets REGISTRY_DATA_DIR.
// Returns a cleanup function.
func setupFixtureDir(t *testing.T) (dataDir string, cleanup func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "registry-ui-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Setenv("REGISTRY_DATA_DIR", dir)
	return dir, func() { os.RemoveAll(dir) }
}

func writeFixture(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// ── ReadProjects ───────────────────────────────────────────────────────────────

func TestReadProjects_ReturnsList(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	writeFixture(t, filepath.Join(dir, "alpha", "project.json"), map[string]any{"name": "alpha"})
	writeFixture(t, filepath.Join(dir, "beta", "project.json"), map[string]any{"name": "beta"})
	// dir with no project.json should be excluded
	if err := os.MkdirAll(filepath.Join(dir, "empty-dir"), 0755); err != nil {
		t.Fatal(err)
	}

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

	writeFixture(t, filepath.Join(dir, "myproject", "project.json"), map[string]any{
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

	plansDir := filepath.Join(dir, "myproject", "plans")
	writeFixture(t, filepath.Join(plansDir, "DOTFILES-1.json"), map[string]any{
		"ticket":  "DOTFILES-1",
		"summary": "First plan",
		"plan_steps": []map[string]any{
			{"step": 1, "status": "done"},
		},
	})
	writeFixture(t, filepath.Join(plansDir, "DOTFILES-2.json"), map[string]any{
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

	plansDir := filepath.Join(dir, "myproject", "plans")
	writeFixture(t, filepath.Join(plansDir, "DOTFILES-1.json"), map[string]any{
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

	plansDir := filepath.Join(dir, "myproject", "plans")
	writeFixture(t, filepath.Join(plansDir, "DOTFILES-3.json"), map[string]any{
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

	writeFixture(t, filepath.Join(dir, "myproject", "plans", "TICKET-1.json"), map[string]any{
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

	entries := []map[string]any{
		{"ticket": "DOTFILES-1", "type": "feature", "summary": "Add thing", "date": "2026-05-01"},
		{"ticket": "DOTFILES-2", "type": "bugfix", "summary": "Fix bug", "date": "2026-06-01"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "audit.json"), entries)

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

	entries := []map[string]any{
		{"ticket": "DOTFILES-1", "date": "2026-04-15"},
		{"ticket": "DOTFILES-2", "date": "2026-05-01"},
		{"ticket": "DOTFILES-3", "date": "2026-06-10"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "audit.json"), entries)

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

	entries := []map[string]any{
		{"ticket": "DOTFILES-1", "date": "2026-04-15"},
		{"ticket": "DOTFILES-2", "date": "2026-05-01"},
		{"ticket": "DOTFILES-3", "date": "2026-06-10"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "audit.json"), entries)

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

	entries := []map[string]any{
		{"ticket": "DOTFILES-1", "date": "2026-04-15"},
		{"ticket": "DOTFILES-2", "date": "2026-05-01"},
		{"ticket": "DOTFILES-3", "date": "2026-06-10"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "audit.json"), entries)

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

	entries := []map[string]any{
		{"status": "pass", "summary": "Deploy checked out", "date": "2026-05-01"},
		{"status": "fail", "summary": "Deploy failed health check", "date": "2026-06-01"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "deploy_checks.json"), entries)

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

	entries := []map[string]any{
		{"status": "pass", "date": "2026-04-15"},
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-10"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "deploy_checks.json"), entries)

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

	entries := []map[string]any{
		{"status": "pass", "date": "2026-04-15"},
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-10"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "deploy_checks.json"), entries)

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

	entries := []map[string]any{
		{"status": "pass", "date": "2026-04-15"},
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-10"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "deploy_checks.json"), entries)

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

	issues := []map[string]any{
		{"tool": "registry_get_project", "error": "not found", "severity": "error", "_recorded_at": "2026-06-16T10:00:00Z"},
		{"tool": "registry_set", "error": "write failed", "severity": "warning", "_recorded_at": "2026-06-16T11:00:00Z"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "issues.json"), issues)

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

	issues := []map[string]any{
		{"tool": "registry_get_project", "error": "not found", "severity": "error"},
		{"tool": "registry_set", "error": "write failed", "severity": "warning"},
		{"tool": "registry_get_plan", "error": "missing plan", "severity": "error"},
	}
	writeFixture(t, filepath.Join(dir, "myproject", "issues.json"), issues)

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
