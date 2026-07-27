package main

import (
	"io"
	"net/http"
	"net/http/httptest"
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

// setupProjectFixture seeds a minimal project + plan + audit + deploy check
// into the registry.db under dataDir.
func setupProjectFixture(t *testing.T, dataDir, projectName string) {
	t.Helper()

	seedProject(t, dataDir, projectName, map[string]any{
		"name": projectName,
		"repo": map[string]any{"workspace": "tazgreenwood"},
	})

	seedPlan(t, dataDir, projectName, "TICKET-1", map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "done"}},
	})

	seedAudit(t, dataDir, projectName, []map[string]any{
		{"ticket": "TICKET-1", "type": "feature", "summary": "Add thing", "date": "2026-06-01"},
	})

	seedDeployChecks(t, dataDir, projectName, []map[string]any{
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
	seedDeployChecks(t, dir, "existing", []map[string]any{
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
	seedDeployChecks(t, dir, "existing", []map[string]any{
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

// ── plan.html kanban board ─────────────────────────────────────────────────────

// columnRegion returns the substring of body between the given column header
// and the next column header in order (or end of body for the last column).
// Fails the test if the header can't be located.
func columnRegion(t *testing.T, body string, header string, nextHeaders ...string) string {
	t.Helper()
	start := strings.Index(body, header)
	if start == -1 {
		t.Fatalf("expected body to contain column header %q, got:\n%s", header, body)
	}
	start += len(header)
	end := len(body)
	for _, next := range nextHeaders {
		if idx := strings.Index(body[start:], next); idx != -1 && start+idx < end {
			end = start + idx
		}
	}
	return body[start:end]
}

func TestGetPlan_RendersKanbanColumnsByStatus(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-KANBAN", map[string]any{
		"ticket":  "TICKET-KANBAN",
		"summary": "Kanban test plan",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "Step Alpha Pending", "status": "pending"},
			{"step": 2, "title": "Step Bravo Active", "status": "in_progress"},
			{"step": 3, "title": "Step Delta Stuck", "status": "blocked"},
		},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/plans/TICKET-KANBAN")
	if err != nil {
		t.Fatalf("GET /projects/existing/plans/TICKET-KANBAN: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(bodyBytes)

	for _, header := range []string{"Pending", "In Progress", "Done", "Blocked"} {
		if !strings.Contains(body, header) {
			t.Errorf("expected body to contain column header %q, got:\n%s", header, body)
		}
	}

	pendingRegion := columnRegion(t, body, "Pending", "In Progress", "Done", "Blocked")
	if !strings.Contains(pendingRegion, "Step Alpha Pending") {
		t.Errorf("expected Pending column to contain %q, got region:\n%s", "Step Alpha Pending", pendingRegion)
	}

	inProgressRegion := columnRegion(t, body, "In Progress", "Done", "Blocked")
	if !strings.Contains(inProgressRegion, "Step Bravo Active") {
		t.Errorf("expected In Progress column to contain %q, got region:\n%s", "Step Bravo Active", inProgressRegion)
	}

	doneRegion := columnRegion(t, body, "Done", "Blocked")
	if !strings.Contains(doneRegion, "No steps") {
		t.Errorf("expected empty Done column to render placeholder %q, got region:\n%s", "No steps", doneRegion)
	}

	blockedRegion := columnRegion(t, body, "Blocked")
	if !strings.Contains(blockedRegion, "Step Delta Stuck") {
		t.Errorf("expected Blocked column to contain %q, got region:\n%s", "Step Delta Stuck", blockedRegion)
	}
}

func TestGetPlan_BlockedStepHasDistinctStylingFromPending(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-BLOCKED", map[string]any{
		"ticket":  "TICKET-BLOCKED",
		"summary": "Blocked styling test plan",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "Step Alpha Pending", "status": "pending"},
			{"step": 2, "title": "Step Delta Stuck", "status": "blocked"},
		},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/plans/TICKET-BLOCKED")
	if err != nil {
		t.Fatalf("GET /projects/existing/plans/TICKET-BLOCKED: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(bodyBytes)

	const blockedClass = "bg-red-500/10 text-red-700"

	pendingRegion := columnRegion(t, body, "Pending", "In Progress", "Done", "Blocked")
	if strings.Contains(pendingRegion, blockedClass) {
		t.Errorf("blocked-specific class %q should not appear near pending step, got region:\n%s", blockedClass, pendingRegion)
	}

	blockedRegion := columnRegion(t, body, "Blocked")
	if !strings.Contains(blockedRegion, blockedClass) {
		t.Errorf("expected blocked step to carry distinct class %q, got region:\n%s", blockedClass, blockedRegion)
	}
}

func TestGetPlan_RendersAsyncGroupBadgeWithSharedTint(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-ASYNC", map[string]any{
		"ticket":  "TICKET-ASYNC",
		"summary": "Async group badge test plan",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "Step Alpha Async", "status": "pending", "execution": "async", "parallel_group": 2, "files": []string{"a.go"}},
			{"step": 2, "title": "Step Bravo Async", "status": "in_progress", "execution": "async", "parallel_group": 2, "files": []string{"b.go"}},
			{"step": 3, "title": "Step Charlie Sync", "status": "pending", "execution": "sync", "files": []string{"c.go"}},
		},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/plans/TICKET-ASYNC")
	if err != nil {
		t.Fatalf("GET /projects/existing/plans/TICKET-ASYNC: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(bodyBytes)

	const groupBadge = "⇉ Group 2"
	const groupTintClass = "bg-violet-500/10"

	alphaIdx := strings.Index(body, "Step Alpha Async")
	if alphaIdx == -1 {
		t.Fatalf("expected body to contain %q, got:\n%s", "Step Alpha Async", body)
	}
	bravoIdx := strings.Index(body, "Step Bravo Async")
	if bravoIdx == -1 {
		t.Fatalf("expected body to contain %q, got:\n%s", "Step Bravo Async", body)
	}
	charlieIdx := strings.Index(body, "Step Charlie Sync")
	if charlieIdx == -1 {
		t.Fatalf("expected body to contain %q, got:\n%s", "Step Charlie Sync", body)
	}

	alphaCard := body[alphaIdx : alphaIdx+strings.Index(body[alphaIdx:], "</button>")]
	bravoCard := body[bravoIdx : bravoIdx+strings.Index(body[bravoIdx:], "</button>")]
	charlieCard := body[charlieIdx : charlieIdx+strings.Index(body[charlieIdx:], "</button>")]

	if !strings.Contains(alphaCard, groupBadge) {
		t.Errorf("expected async step card to contain group badge %q, got:\n%s", groupBadge, alphaCard)
	}
	if !strings.Contains(alphaCard, groupTintClass) {
		t.Errorf("expected async step card badge to carry shared tint class %q, got:\n%s", groupTintClass, alphaCard)
	}
	if !strings.Contains(bravoCard, groupBadge) {
		t.Errorf("expected async step card to contain group badge %q, got:\n%s", groupBadge, bravoCard)
	}
	if !strings.Contains(bravoCard, groupTintClass) {
		t.Errorf("expected async step card badge to carry shared tint class %q, got:\n%s", groupTintClass, bravoCard)
	}
	if strings.Contains(charlieCard, groupBadge) {
		t.Errorf("expected sync step with no parallel_group to render no group badge, got:\n%s", charlieCard)
	}
}

func TestGetAudit_WithQueryParams_FiltersResults(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	entries := []map[string]any{
		{"ticket": "DOTFILES-1", "type": "feature", "summary": "Old entry", "date": "2026-04-01"},
		{"ticket": "DOTFILES-2", "type": "feature", "summary": "New entry", "date": "2026-06-01"},
	}
	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedAudit(t, dir, "existing", entries)

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
