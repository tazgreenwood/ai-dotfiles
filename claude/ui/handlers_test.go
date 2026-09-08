package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestGetRoot_RendersOnePlanCardLinkedToPlanPage verifies the DOTFILES-39
// redesign: the global Kanban board renders one card per PLAN (not one per
// step), showing the ticket, plan summary, and an N/M steps-done progress
// indicator, linked to the existing per-plan detail page rather than opening
// a step-detail modal.
func TestGetRoot_RendersOnePlanCardLinkedToPlanPage(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{
		"name": "existing",
		"repo": map[string]any{"workspace": "tazgreenwood"},
	})
	seedPlan(t, dir, "existing", "TICKET-MULTI", map[string]any{
		"ticket":  "TICKET-MULTI",
		"summary": "Multi-step plan",
		"plan_steps": []map[string]any{
			{"step": 1, "status": "done"},
			{"step": 2, "status": "pending"},
			{"step": 3, "status": "pending"},
		},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	html := string(body)

	if !strings.Contains(html, `href="/projects/existing/plans/TICKET-MULTI"`) {
		t.Errorf("want plan card linking to /projects/existing/plans/TICKET-MULTI, got:\n%s", html)
	}
	if !strings.Contains(html, "Multi-step plan") {
		t.Errorf("want plan summary %q rendered, got:\n%s", "Multi-step plan", html)
	}
	if !strings.Contains(html, "1/3") {
		t.Errorf("want steps-done progress indicator %q, got:\n%s", "1/3", html)
	}
	if strings.Contains(html, "data-step-modal") {
		t.Errorf("want no step-detail modal trigger left in the page, found data-step-modal")
	}
	if strings.Contains(html, "Step 1") || strings.Contains(html, "Step 2") {
		t.Errorf("want no per-step card text on the plan-level board, got:\n%s", html)
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
		Suggestions: []Suggestion{
			{
				Severity:     "MAJOR",
				Title:        "Missing nil check",
				File:         "pkg/handler.go",
				Line:         "42",
				WhatsWrong:   "Add error handling for nil input",
				Evidence:     "handler.go:42 dereferences req without a nil check",
				SuggestedFix: "Add a nil guard before dereferencing req",
				Confidence:   "high",
			},
			{Severity: "NIT", Title: "Duplicated logic", WhatsWrong: "Extract helper for repeated logic"},
			{Severity: "MINOR", Title: "Overly broad catch", WhatsWrong: "Swallows all errors", DemotedFrom: "MAJOR"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/example/reviews/1", nil)
	render(rec, req, "review.html", data)

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
		"pkg/handler.go",
		"handler.go:42 dereferences req without a nil check",
		"Add a nil guard before dereferencing req",
		"Confidence: high",
		"demoted from MAJOR",
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

// ── POST /projects/{name}/plans/{ticket}/phase (DOTFILES-47) ────────────────

func TestGetPlan_RendersPhaseOverrideControl_DefaultsToComputedStatus(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-1", map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "done"}},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/plans/TICKET-1")
	if err != nil {
		t.Fatalf("GET plan: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	if !strings.Contains(body, `action="/projects/existing/plans/TICKET-1/phase"`) {
		t.Fatalf("expected phase override form action, got:\n%s", body)
	}
	if !strings.Contains(body, `<label for="phase-override"`) {
		t.Errorf("expected labeled phase-override control, got:\n%s", body)
	}
	if !strings.Contains(body, `id="phase-override"`) || !strings.Contains(body, `name="phase"`) {
		t.Errorf("expected select#phase-override[name=phase], got:\n%s", body)
	}
	// No override set — plan has all steps done and no audit entry, so the
	// computed effective status is pr_ready, and the state text must say so
	// rather than "Automatic" alone leaving the value ambiguous.
	if !strings.Contains(body, "Automatic") {
		t.Errorf("expected 'Automatic' state text when no override is set, got:\n%s", body)
	}
	if !strings.Contains(body, `value="pr_ready" selected`) {
		t.Errorf("expected pr_ready option selected as the computed default, got:\n%s", body)
	}
}

func TestSetPlanPhase_SetsOverride_RedirectsAndPersists(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-1", map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "done"}},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.PostForm(ts.URL+"/projects/existing/plans/TICKET-1/phase", url.Values{
		"phase": {"blocked"},
	})
	if err != nil {
		t.Fatalf("POST phase: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("want 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/projects/existing/plans/TICKET-1" {
		t.Errorf("want redirect to plan detail page, got %q", loc)
	}

	plan, err := ReadPlan("existing", "TICKET-1")
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.PhaseOverride != "blocked" {
		t.Errorf("want phase_override 'blocked', got %q", plan.PhaseOverride)
	}

	// The plan page itself reflects the override applied, not the computed
	// status, and shows it as text rather than only a color.
	page, err := http.Get(ts.URL + "/projects/existing/plans/TICKET-1")
	if err != nil {
		t.Fatalf("GET plan after set: %v", err)
	}
	defer page.Body.Close()
	b, _ := io.ReadAll(page.Body)
	body := string(b)
	if !strings.Contains(body, "Override: blocked") {
		t.Errorf("expected 'Override: blocked' state text, got:\n%s", body)
	}
	if !strings.Contains(body, `value="blocked" selected`) {
		t.Errorf("expected blocked option selected, got:\n%s", body)
	}
}

func TestSetPlanPhase_EmptyValueClearsOverride(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-1", map[string]any{
		"ticket":         "TICKET-1",
		"summary":        "Test plan",
		"plan_steps":     []map[string]any{{"step": 1, "status": "pending"}},
		"phase_override": "blocked",
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.PostForm(ts.URL+"/projects/existing/plans/TICKET-1/phase", url.Values{
		"phase": {""},
	})
	if err != nil {
		t.Fatalf("POST phase clear: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("want 303, got %d", resp.StatusCode)
	}

	plan, err := ReadPlan("existing", "TICKET-1")
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.PhaseOverride != "" {
		t.Errorf("want phase_override cleared, got %q", plan.PhaseOverride)
	}
	// Steps are all pending, so the computed status reverts to "pending".
	page, err := http.Get(ts.URL + "/projects/existing/plans/TICKET-1")
	if err != nil {
		t.Fatalf("GET plan after clear: %v", err)
	}
	defer page.Body.Close()
	b, _ := io.ReadAll(page.Body)
	body := string(b)
	if !strings.Contains(body, "Automatic") {
		t.Errorf("expected 'Automatic' state text after clearing, got:\n%s", body)
	}
}

// TestSetPlanPhase_InvalidPhase_RejectedNotSilentlyStored guards the
// BLOCKER a code review caught: a phase_override value outside the 6 valid
// phases isn't recognized by any Kanban column and makes the plan's card
// vanish from the board with no error. A direct POST (bypassing the
// <select>, which only ever offers valid values) must be rejected, not
// silently persisted.
func TestSetPlanPhase_InvalidPhase_RejectedNotSilentlyStored(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-1", map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "pending"}},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.PostForm(ts.URL+"/projects/existing/plans/TICKET-1/phase", url.Values{
		"phase": {"not_a_real_phase"},
	})
	if err != nil {
		t.Fatalf("POST phase: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid phase, got %d", resp.StatusCode)
	}

	plan, err := ReadPlan("existing", "TICKET-1")
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.PhaseOverride != "" {
		t.Errorf("invalid phase must not be persisted, got phase_override=%q", plan.PhaseOverride)
	}
}

// TestSetPlanPhase_RejectsNonLoopbackRequest guards the security finding a
// review pass caught: this is registry-ui's first state-mutating route (every
// other route is GET), can force any plan to look "shipped", and the server
// otherwise binds all interfaces with zero auth. A request whose RemoteAddr
// isn't loopback must be refused outright. httptest.Server always dials from
// 127.0.0.1, so this calls the handler directly with a forged RemoteAddr
// rather than going through newTestServer.
func TestSetPlanPhase_RejectsNonLoopbackRequest(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-1", map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "pending"}},
	})

	form := url.Values{"phase": {"blocked"}}
	req := httptest.NewRequest(http.MethodPost, "/projects/existing/plans/TICKET-1/phase", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("name", "existing")
	req.SetPathValue("ticket", "TICKET-1")
	req.RemoteAddr = "203.0.113.7:54321" // TEST-NET-3, definitely not loopback

	rec := httptest.NewRecorder()
	handleSetPlanPhase(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for non-loopback request, got %d", rec.Code)
	}

	plan, err := ReadPlan("existing", "TICKET-1")
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.PhaseOverride != "" {
		t.Errorf("non-loopback request must not persist a phase_override, got %q", plan.PhaseOverride)
	}
}

// TestSetPlanPhase_RejectsCrossOriginRequest guards the security finding a
// review pass caught: RemoteAddr alone can't distinguish the operator's own
// dashboard from a malicious page loaded in a browser running on the same
// machine, since the browser IS the loopback client either way — the
// isLoopbackRequest check on its own does not stop that. A same-machine
// request whose Origin (or Referer) header names a different host must be
// rejected even though it satisfies the loopback check.
func TestSetPlanPhase_RejectsCrossOriginRequest(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-1", map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "pending"}},
	})

	form := url.Values{"phase": {"blocked"}}
	req := httptest.NewRequest(http.MethodPost, "/projects/existing/plans/TICKET-1/phase", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example.com")
	req.SetPathValue("name", "existing")
	req.SetPathValue("ticket", "TICKET-1")
	req.RemoteAddr = "127.0.0.1:54321" // genuinely loopback -- the browser is the local client
	req.Host = "localhost:7432"

	rec := httptest.NewRecorder()
	handleSetPlanPhase(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for cross-origin request, got %d", rec.Code)
	}

	plan, err := ReadPlan("existing", "TICKET-1")
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.PhaseOverride != "" {
		t.Errorf("cross-origin request must not persist a phase_override, got %q", plan.PhaseOverride)
	}
}

// TestSetPlanPhase_AllowsSameOriginRequest confirms the same-origin check
// doesn't false-positive on the legitimate case: a real form submission from
// the plan page itself, where Origin matches r.Host.
func TestSetPlanPhase_AllowsSameOriginRequest(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "existing", map[string]any{"name": "existing"})
	seedPlan(t, dir, "existing", "TICKET-1", map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "pending"}},
	})

	form := url.Values{"phase": {"blocked"}}
	req := httptest.NewRequest(http.MethodPost, "/projects/existing/plans/TICKET-1/phase", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://localhost:7432")
	req.SetPathValue("name", "existing")
	req.SetPathValue("ticket", "TICKET-1")
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "localhost:7432"

	rec := httptest.NewRecorder()
	handleSetPlanPhase(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("want 303 for same-origin request, got %d", rec.Code)
	}

	plan, err := ReadPlan("existing", "TICKET-1")
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.PhaseOverride != "blocked" {
		t.Errorf("want phase_override 'blocked' for same-origin request, got %q", plan.PhaseOverride)
	}
}
