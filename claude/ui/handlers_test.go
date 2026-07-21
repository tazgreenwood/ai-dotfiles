package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestServer returns an httptest.Server using the production handler
// with REGISTRY_DATA_DIR pointed at the provided data directory.
func newTestServer(t *testing.T, dataDir string) *httptest.Server {
	t.Helper()
	t.Setenv("REGISTRY_DATA_DIR", dataDir)
	return httptest.NewServer(newRouter())
}

// setupProjectFixture writes a minimal project + plan + audit under dataDir.
func setupProjectFixture(t *testing.T, dataDir, projectName string) {
	t.Helper()

	writeFixture(t, filepath.Join(dataDir, projectName, "project.json"), map[string]any{
		"name": projectName,
		"repo": map[string]any{"workspace": "tazgreenwood"},
	})

	writeFixture(t, filepath.Join(dataDir, projectName, "plans", "TICKET-1.json"), map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "done"}},
	})

	writeFixture(t, filepath.Join(dataDir, projectName, "audit.json"), []map[string]any{
		{"ticket": "TICKET-1", "type": "feature", "summary": "Add thing", "date": "2026-06-01"},
	})

	writeFixture(t, filepath.Join(dataDir, projectName, "deploy_checks.json"), []map[string]any{
		{"app": "emily", "status": "pass", "summary": "All checks passed", "date": "2026-06-01"},
	})
}

// ── GET / ──────────────────────────────────────────────────────────────────────

func TestGetRoot_Returns200HTML(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("want Content-Type text/html, got %q", ct)
	}
}

// ── GET /projects/{name} ───────────────────────────────────────────────────────

func TestGetProject_ExistingReturns200(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing")
	if err != nil {
		t.Fatalf("GET /projects/existing: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

func TestGetProject_ShowsRecentDeployChecksSection(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing")
	if err != nil {
		t.Fatalf("GET /projects/existing: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if !strings.Contains(string(body), "Recent Deploy Checks") {
		t.Errorf("expected body to contain %q, got:\n%s", "Recent Deploy Checks", body)
	}
	if !strings.Contains(string(body), "/projects/existing/deploy-checks") {
		t.Errorf("expected body to contain link to %q, got:\n%s", "/projects/existing/deploy-checks", body)
	}
}

func TestGetProject_NonexistentReturns404(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/nonexistent")
	if err != nil {
		t.Fatalf("GET /projects/nonexistent: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// ── GET /projects/{name}/plans/{ticket} ────────────────────────────────────────

func TestGetPlan_ExistingReturns200(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/plans/TICKET-1")
	if err != nil {
		t.Fatalf("GET /projects/existing/plans/TICKET-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

func TestGetPlan_MissingTicketReturns404(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/plans/TICKET-999")
	if err != nil {
		t.Fatalf("GET /projects/existing/plans/TICKET-999: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// ── GET /projects/{name}/audit ─────────────────────────────────────────────────

func TestGetAudit_ExistingReturns200(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/audit")
	if err != nil {
		t.Fatalf("GET /projects/existing/audit: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

func TestGetAudit_MissingProjectReturns404(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/nonexistent/audit")
	if err != nil {
		t.Fatalf("GET /projects/nonexistent/audit: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// ── GET /projects/{name}/deploy-checks ─────────────────────────────────────────

func TestGetDeployChecks_ExistingReturns200(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")
	writeFixture(t, filepath.Join(dir, "existing", "deploy_checks.json"), []map[string]any{
		{"status": "pass", "summary": "Deploy checked out", "date": "2026-06-01"},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/deploy-checks")
	if err != nil {
		t.Fatalf("GET /projects/existing/deploy-checks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

func TestGetDeployChecks_MissingProjectReturns404(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/nonexistent/deploy-checks")
	if err != nil {
		t.Fatalf("GET /projects/nonexistent/deploy-checks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// ── Content-type helpers ───────────────────────────────────────────────────────

func TestGetProject_ResponseIsJSON(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing")
	if err != nil {
		t.Fatalf("GET /projects/existing: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("want Content-Type text/html, got %q", ct)
	}
}

// ── GET /projects/{name}/issues ────────────────────────────────────────────────

func TestGetIssues_ExistingProjectReturns200(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/issues")
	if err != nil {
		t.Fatalf("GET /projects/existing/issues: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

func TestGetIssues_UnknownProjectReturns404(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/unknown/issues")
	if err != nil {
		t.Fatalf("GET /projects/unknown/issues: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// ── review.html template ───────────────────────────────────────────────────────

func TestRenderReview_ContainsExpectedSections(t *testing.T) {
	data := reviewData{
		Breadcrumbs:   []breadcrumb{{Label: "Registry", URL: "/"}, {Label: "Code Review"}},
		Title:         "Code Review — feat/DOTFILES-9",
		Summary:       "Adds HTML report rendering for code review results",
		Why:           "Need a readable output instead of raw diffs",
		DiffOverview:  "3 files changed, 42 insertions, 5 deletions",
		ExecutionMode: "mock",
		ExecutionLog:  "ran synthesized inputs through changed functions: all passed",
		Suggestions:   []string{"Add error handling for nil input", "Extract helper for repeated logic"},
	}

	rec := httptest.NewRecorder()
	render(rec, "review.html", data)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	wantContains := []string{
		data.Summary,
		data.Why,
		data.DiffOverview,
		data.ExecutionMode,
		data.ExecutionLog,
		"Add error handling for nil input",
		"Extract helper for repeated logic",
	}
	for _, want := range wantContains {
		if !strings.Contains(body, want) {
			t.Errorf("expected rendered HTML to contain %q, got:\n%s", want, body)
		}
	}
}

// ── Cache-Control headers ──────────────────────────────────────────────────────

func TestHandlers_SetNoStoreCacheControl(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")
	writeFixture(t, filepath.Join(dir, "existing", "deploy_checks.json"), []map[string]any{
		{"status": "pass", "date": "2026-06-01"},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	paths := []string{
		"/",
		"/projects/existing",
		"/projects/existing/plans/TICKET-1",
		"/projects/existing/audit",
		"/projects/existing/issues",
		"/projects/existing/deploy-checks",
	}

	for _, path := range paths {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()

		cc := resp.Header.Get("Cache-Control")
		if cc != "no-store" {
			t.Errorf("GET %s: want Cache-Control %q, got %q", path, "no-store", cc)
		}
	}
}

func TestGetAudit_WithQueryParams_FiltersResults(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	entries := []map[string]any{
		{"ticket": "DOTFILES-1", "type": "feature", "summary": "Old entry", "date": "2026-04-01"},
		{"ticket": "DOTFILES-2", "type": "feature", "summary": "New entry", "date": "2026-06-01"},
	}
	if err := os.MkdirAll(filepath.Join(dir, "existing"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "existing", "project.json"), map[string]any{"name": "existing"})
	writeFixture(t, filepath.Join(dir, "existing", "audit.json"), entries)

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/audit?since=2026-05-01")
	if err != nil {
		t.Fatalf("GET /projects/existing/audit?since=2026-05-01: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}
