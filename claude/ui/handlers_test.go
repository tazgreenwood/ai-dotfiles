package main

import (
	"encoding/json"
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

	var body struct {
		Entries []any `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Entries) != 1 {
		t.Errorf("want 1 filtered entry, got %d", len(body.Entries))
	}
}
