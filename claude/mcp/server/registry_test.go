package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestDataDir(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "registry-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Setenv("REGISTRY_DATA_DIR", dir)
	return dir, func() { os.RemoveAll(dir) }
}

func TestRegistryReportIssue_WritesIssue(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"project": "myproject",
		"tool":    "registry_get_project",
		"error":   "project not found",
	}
	result := registryReportIssue(args)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	issues, err := s.GetIssues("myproject")
	if err != nil {
		t.Fatalf("GetIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %d", len(issues))
	}
	if issues[0]["tool"] != "registry_get_project" {
		t.Errorf("want tool=registry_get_project, got %v", issues[0]["tool"])
	}
	if issues[0]["error"] != "project not found" {
		t.Errorf("want error='project not found', got %v", issues[0]["error"])
	}
}

func TestRegistryReportIssue_AddsRecordedAt(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"project": "myproject",
		"tool":    "registry_get_plan",
		"error":   "plan missing",
	}
	registryReportIssue(args)

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	issues, err := s.GetIssues("myproject")
	if err != nil {
		t.Fatalf("GetIssues: %v", err)
	}

	if _, ok := issues[0]["_recorded_at"]; !ok {
		t.Error("want _recorded_at field, not present")
	}
}

func TestRegistryReportIssue_DefaultsProjectToPrivateDotfiles(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"tool":  "registry_set",
		"error": "write failed",
	}
	result := registryReportIssue(args)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	issues, err := s.GetIssues("private-dotfiles")
	if err != nil {
		t.Fatalf("GetIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("want 1 issue for private-dotfiles, got %d", len(issues))
	}
}

func TestRegistryReportIssue_DefaultsSeverityToError(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"project": "myproject",
		"tool":    "registry_get_project",
		"error":   "boom",
	}
	registryReportIssue(args)

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	issues, err := s.GetIssues("myproject")
	if err != nil {
		t.Fatalf("GetIssues: %v", err)
	}

	if issues[0]["severity"] != "error" {
		t.Errorf("want severity=error, got %v", issues[0]["severity"])
	}
}

func TestRegistryReportIssue_ReturnsTotalIssues(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"project": "myproject",
		"tool":    "registry_get_project",
		"error":   "first",
	}
	registryReportIssue(args)
	args["error"] = "second"
	result := registryReportIssue(args)

	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)

	if resp["ok"] != true {
		t.Errorf("want ok=true, got %v", resp["ok"])
	}
	total, _ := resp["total_issues"].(float64)
	if total != 2 {
		t.Errorf("want total_issues=2, got %v", resp["total_issues"])
	}
}

func TestRegistryUpdateStep_UpdatesStatus(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	plan := map[string]any{
		"ticket":  "TEST-1",
		"summary": "test plan",
		"plan_steps": []any{
			map[string]any{"title": "step one", "status": "pending"},
			map[string]any{"title": "step two", "status": "pending"},
		},
	}
	registryWritePlan(map[string]any{"name": "myproject", "ticket": "TEST-1", "data": plan})

	result := registryUpdateStep(map[string]any{
		"name":       "myproject",
		"ticket":     "TEST-1",
		"step_index": float64(0),
		"status":     "in_progress",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	got := registryGetPlan(map[string]any{"name": "myproject", "ticket": "TEST-1"})
	var data map[string]any
	json.Unmarshal([]byte(got.Content[0].Text), &data)
	steps := data["plan_steps"].([]any)
	s0 := steps[0].(map[string]any)
	s1 := steps[1].(map[string]any)
	if s0["status"] != "in_progress" {
		t.Errorf("want step 0 status=in_progress, got %v", s0["status"])
	}
	if s1["status"] != "pending" {
		t.Errorf("want step 1 status=pending, got %v", s1["status"])
	}
}

func TestRegistryUpdateStep_AcceptsInReview(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	plan := map[string]any{
		"ticket":  "TEST-2",
		"summary": "test plan",
		"plan_steps": []any{
			map[string]any{"title": "step one", "status": "in_progress"},
			map[string]any{"title": "step two", "status": "pending"},
		},
	}
	registryWritePlan(map[string]any{"name": "myproject", "ticket": "TEST-2", "data": plan})

	result := registryUpdateStep(map[string]any{
		"name":       "myproject",
		"ticket":     "TEST-2",
		"step_index": float64(0),
		"status":     "in_review",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	got := registryGetPlan(map[string]any{"name": "myproject", "ticket": "TEST-2"})
	var data map[string]any
	json.Unmarshal([]byte(got.Content[0].Text), &data)
	steps := data["plan_steps"].([]any)
	s0 := steps[0].(map[string]any)
	s1 := steps[1].(map[string]any)
	if s0["status"] != "in_review" {
		t.Errorf("want step 0 status=in_review, got %v", s0["status"])
	}
	if s1["status"] != "pending" {
		t.Errorf("want step 1 status=pending, got %v", s1["status"])
	}
}

// planIsShipped (via registry_index's active_plan selection) must not consider
// a plan shipped just because every step finished — it is only actually
// shipped once /ship has written a matching audit entry. Otherwise a plan
// vanishes from the index (and from routing) the instant build finishes,
// before /ship has even run.
func TestRegistryIndex_AllStepsDoneButNoAuditEntry_StillActive(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	registryInitProject(map[string]any{"name": "myproject"})
	plan := map[string]any{
		"ticket":  "TEST-4",
		"summary": "finished build, not shipped yet",
		"plan_steps": []any{
			map[string]any{"title": "step one", "status": "done"},
			map[string]any{"title": "step two", "status": "done"},
		},
	}
	registryWritePlan(map[string]any{"name": "myproject", "ticket": "TEST-4", "data": plan})

	result := registryIndex(map[string]any{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var resp struct {
		Projects []projectIndexEntry `json:"projects"`
	}
	json.Unmarshal([]byte(result.Content[0].Text), &resp)

	var found *projectIndexEntry
	for i := range resp.Projects {
		if resp.Projects[i].Name == "myproject" {
			found = &resp.Projects[i]
		}
	}
	if found == nil {
		t.Fatalf("myproject not present in index")
	}
	if found.ActivePlan == nil {
		t.Fatal("want active_plan non-nil: all steps done but no audit entry exists yet, so the plan is not shipped")
	}
	if found.ActivePlan.Ticket != "TEST-4" {
		t.Errorf("active_plan.ticket: want TEST-4, got %q", found.ActivePlan.Ticket)
	}
}

func TestRegistryIndex_AllStepsDoneWithMatchingAuditEntry_Shipped(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	registryInitProject(map[string]any{"name": "myproject"})
	plan := map[string]any{
		"ticket":  "TEST-5",
		"summary": "finished and shipped",
		"plan_steps": []any{
			map[string]any{"title": "step one", "status": "done"},
			map[string]any{"title": "step two", "status": "done"},
		},
	}
	registryWritePlan(map[string]any{"name": "myproject", "ticket": "TEST-5", "data": plan})
	registryWriteAudit(map[string]any{
		"name": "myproject",
		"entry": map[string]any{
			"ticket":  "TEST-5",
			"type":    "feature",
			"summary": "shipped it",
		},
	})

	result := registryIndex(map[string]any{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var resp struct {
		Projects []projectIndexEntry `json:"projects"`
	}
	json.Unmarshal([]byte(result.Content[0].Text), &resp)

	var found *projectIndexEntry
	for i := range resp.Projects {
		if resp.Projects[i].Name == "myproject" {
			found = &resp.Projects[i]
		}
	}
	if found == nil {
		t.Fatalf("myproject not present in index")
	}
	if found.ActivePlan != nil {
		t.Errorf("want active_plan nil: matching audit entry exists so the plan is shipped, got %+v", found.ActivePlan)
	}
}

// TestComputePlanStatus_Outcomes pins the precedence contract for the single
// authoritative status function that DOTFILES-48 introduces to replace the
// duplicated derivation logic (backend planIsShipped bool + UI
// derivePlanStatus 6-value switch). Precedence, highest first: phase_override
// (if set, wins outright) > blocked (any step blocked) > in_review (any step
// in_review) > in_progress (any step in_progress, or partially done) >
// pr_ready/done (every step done — done once a matching audit entry exists,
// pr_ready until then) > pending (default).
func TestComputePlanStatus_Outcomes(t *testing.T) {
	tests := []struct {
		name     string
		steps    []any
		hasAudit bool
		override string
		want     string
	}{
		{
			name: "pending: no step started",
			steps: []any{
				map[string]any{"status": "pending"},
				map[string]any{"status": "pending"},
			},
			want: "pending",
		},
		{
			name: "in_progress: a step is in_progress",
			steps: []any{
				map[string]any{"status": "in_progress"},
				map[string]any{"status": "pending"},
			},
			want: "in_progress",
		},
		{
			name: "in_progress: partially done beats pending",
			steps: []any{
				map[string]any{"status": "done"},
				map[string]any{"status": "pending"},
			},
			want: "in_progress",
		},
		{
			name: "in_review: a step is in_review",
			steps: []any{
				map[string]any{"status": "done"},
				map[string]any{"status": "in_review"},
			},
			want: "in_review",
		},
		{
			name: "blocked: any blocked step beats in_review",
			steps: []any{
				map[string]any{"status": "blocked"},
				map[string]any{"status": "in_review"},
			},
			want: "blocked",
		},
		{
			name: "pr_ready: all steps done, no audit entry yet",
			steps: []any{
				map[string]any{"status": "done"},
				map[string]any{"status": "done"},
			},
			hasAudit: false,
			want:     "pr_ready",
		},
		{
			name: "done: all steps done, matching audit entry exists",
			steps: []any{
				map[string]any{"status": "done"},
				map[string]any{"status": "done"},
			},
			hasAudit: true,
			want:     "done",
		},
		{
			name: "override wins outright over step-derived status",
			steps: []any{
				map[string]any{"status": "pending"},
			},
			hasAudit: false,
			override: "blocked",
			want:     "blocked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := map[string]any{"plan_steps": tt.steps}
			if tt.override != "" {
				data["phase_override"] = tt.override
			}
			got := ComputePlanStatus(data, tt.hasAudit)
			if got != tt.want {
				t.Errorf("ComputePlanStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPlanStatus_PersistedAfterMutations pins the persistence half of
// DOTFILES-48: the plan's status field must be recomputed and written into
// the plan's stored JSON at each of the 3 mutation points (UpdateStep,
// WriteAudit, SetPlanPhase) rather than re-derived on every read. Today none
// of the three writes a "status" field into the plan blob, so each assertion
// below fails against current behavior.
func TestPlanStatus_PersistedAfterMutations(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	registryInitProject(map[string]any{"name": "myproject"})
	plan := map[string]any{
		"ticket":  "TEST-STATUS-1",
		"summary": "pins persisted status field",
		"plan_steps": []any{
			map[string]any{"title": "only step", "status": "pending"},
		},
	}
	registryWritePlan(map[string]any{"name": "myproject", "ticket": "TEST-STATUS-1", "data": plan})

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}

	// 1. UpdateStep marks the only step done -> all steps done, no audit yet
	// -> persisted status should become pr_ready.
	if err := s.UpdateStep("myproject", "TEST-STATUS-1", 0, "done"); err != nil {
		t.Fatalf("UpdateStep: %v", err)
	}
	data, err := s.GetPlan("myproject", "TEST-STATUS-1")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got, _ := data["status"].(string); got != "pr_ready" {
		t.Errorf("after UpdateStep(done): want persisted status=pr_ready, got %q", got)
	}

	// 2. WriteAudit records a matching audit entry -> persisted status should
	// flip from pr_ready to done.
	registryWriteAudit(map[string]any{
		"name": "myproject",
		"entry": map[string]any{
			"ticket":  "TEST-STATUS-1",
			"type":    "feature",
			"summary": "shipped it",
		},
	})
	data, err = s.GetPlan("myproject", "TEST-STATUS-1")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got, _ := data["status"].(string); got != "done" {
		t.Errorf("after WriteAudit: want persisted status=done, got %q", got)
	}

	// 3. SetPlanPhase overrides the plan to blocked -> persisted status must
	// reflect the override, not the step/audit-derived done.
	if err := s.SetPlanPhase("myproject", "TEST-STATUS-1", "blocked"); err != nil {
		t.Fatalf("SetPlanPhase: %v", err)
	}
	data, err = s.GetPlan("myproject", "TEST-STATUS-1")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got, _ := data["status"].(string); got != "blocked" {
		t.Errorf("after SetPlanPhase(blocked): want persisted status=blocked, got %q", got)
	}
}

func TestRegistryWritePlan_InvalidAsyncGrouping_RejectedAndNotPersisted(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	plan := map[string]any{
		"ticket":  "TEST-3",
		"summary": "bad async grouping",
		"plan_steps": []any{
			map[string]any{
				"title":          "step one",
				"status":         "pending",
				"execution":      "async",
				"parallel_group": "group-a",
				"files":          []any{"foo.go", "bar.go"},
			},
			map[string]any{
				"title":          "step two",
				"status":         "pending",
				"execution":      "async",
				"parallel_group": "group-a",
				"files":          []any{"bar.go", "baz.go"},
			},
		},
	}

	result := registryWritePlan(map[string]any{"name": "myproject", "ticket": "TEST-3", "data": plan})
	if !result.IsError {
		t.Fatal("want registry_write_plan to reject overlapping async parallel_group, got success")
	}

	got := registryGetPlan(map[string]any{"name": "myproject", "ticket": "TEST-3"})
	if !got.IsError {
		t.Fatal("want plan not persisted after rejected write, but registry_get_plan succeeded")
	}

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	plans, err := s.ListPlans("myproject")
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	for _, p := range plans {
		if p["ticket"] == "TEST-3" {
			t.Fatal("want TEST-3 not present in ListPlans after rejected write")
		}
	}
}

func TestRegistryUpdateStep_OutOfRange(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	plan := map[string]any{
		"ticket":     "TEST-2",
		"plan_steps": []any{map[string]any{"title": "only step", "status": "pending"}},
	}
	registryWritePlan(map[string]any{"name": "myproject", "ticket": "TEST-2", "data": plan})

	result := registryUpdateStep(map[string]any{
		"name":       "myproject",
		"ticket":     "TEST-2",
		"step_index": float64(5),
		"status":     "done",
	})
	if !result.IsError {
		t.Fatal("want error for out-of-range index")
	}
}

func TestRegistryUpdateStep_MissingPlan(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryUpdateStep(map[string]any{
		"name":       "myproject",
		"ticket":     "NOPE-1",
		"step_index": float64(0),
		"status":     "done",
	})
	if !result.IsError {
		t.Fatal("want error for missing plan")
	}
}

func TestRegistryReportIssue_AcceptsWarning(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"project":  "myproject",
		"tool":     "registry_get_project",
		"error":    "partial result",
		"severity": "warning",
	}
	registryReportIssue(args)

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	issues, err := s.GetIssues("myproject")
	if err != nil {
		t.Fatalf("GetIssues: %v", err)
	}

	if issues[0]["severity"] != "warning" {
		t.Errorf("want severity=warning, got %v", issues[0]["severity"])
	}
}

func setupUnwritableDataDir(t *testing.T) {
	t.Helper()
	parent, err := os.MkdirTemp("", "registry-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(parent) })
	// Point REGISTRY_DATA_DIR at a path that is itself a plain file (not a
	// directory), so the SQLite store fails to open/create registry.db
	// underneath it with ENOTDIR.
	blocker := filepath.Join(parent, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("REGISTRY_DATA_DIR", blocker)
}

func TestRegistryWritePlan_UnwritableDataDir_ReturnsLoudError(t *testing.T) {
	setupUnwritableDataDir(t)

	result := registryWritePlan(map[string]any{
		"name":   "myproject",
		"ticket": "TEST-1",
		"data":   map[string]any{"ticket": "TEST-1", "summary": "test plan"},
	})

	if !result.IsError {
		t.Fatal("want registry_write_plan to return a loud error when data dir is unwritable, got success")
	}
	if result.Content[0].Text == "" {
		t.Error("want non-empty error message")
	}
}

func TestRegistryGetPlan_UnwritableDataDir_ReturnsLoudError(t *testing.T) {
	setupUnwritableDataDir(t)

	result := registryGetPlan(map[string]any{
		"name":   "myproject",
		"ticket": "TEST-1",
	})

	if !result.IsError {
		t.Fatal("want registry_get_plan to return a loud error when plan cannot be read due to unwritable/blocked data dir, got success")
	}
	if result.Content[0].Text == "" {
		t.Error("want non-empty error message")
	}
}

func writeProjectData(t *testing.T, name string, data map[string]any) {
	t.Helper()
	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	if err := s.SetProject(name, data); err != nil {
		t.Fatalf("SetProject: %v", err)
	}
}

func TestGetResources_ReturnsAll(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectData(t, "myproject", map[string]any{
		"name": "myproject",
		"resources": map[string]any{
			"grafana": map[string]any{"dashboard": "http://grafana/d/abc"},
			"aws":     map[string]any{"log_group": "/app/logs"},
		},
	})

	result := registryGetResources(map[string]any{"name": "myproject"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	resources, ok := resp["resources"].(map[string]any)
	if !ok {
		t.Fatalf("want resources map, got %T", resp["resources"])
	}
	if _, ok := resources["grafana"]; !ok {
		t.Error("want grafana key in resources")
	}
	if _, ok := resources["aws"]; !ok {
		t.Error("want aws key in resources")
	}
}

func TestGetResources_FiltersByCategory(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectData(t, "myproject", map[string]any{
		"name": "myproject",
		"resources": map[string]any{
			"grafana": map[string]any{"dashboard": "http://grafana/d/abc"},
			"aws":     map[string]any{"log_group": "/app/logs"},
		},
	})

	result := registryGetResources(map[string]any{"name": "myproject", "category": "grafana"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	resources, ok := resp["resources"].(map[string]any)
	if !ok {
		t.Fatalf("want resources map, got %T", resp["resources"])
	}
	if _, ok := resources["grafana"]; !ok {
		t.Error("want grafana key in resources")
	}
	if _, ok := resources["aws"]; ok {
		t.Error("want aws excluded when category=grafana")
	}
}

func TestScriptsResourceCategory_RoundTrip(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	setResult := registrySet(map[string]any{
		"name":  "myproject",
		"path":  "resources.scripts.foo",
		"value": map[string]any{"command": "echo hello", "description": "prints hello"},
	})
	if setResult.IsError {
		t.Fatalf("unexpected error: %s", setResult.Content[0].Text)
	}

	result := registryGetResources(map[string]any{"name": "myproject", "category": "scripts"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	resources, ok := resp["resources"].(map[string]any)
	if !ok {
		t.Fatalf("want resources map, got %T", resp["resources"])
	}
	scripts, ok := resources["scripts"].(map[string]any)
	if !ok {
		t.Fatalf("want scripts key in resources, got %v", resources)
	}
	foo, ok := scripts["foo"].(map[string]any)
	if !ok {
		t.Fatalf("want foo key in scripts, got %v", scripts)
	}
	if foo["command"] != "echo hello" {
		t.Errorf("want command='echo hello', got %v", foo["command"])
	}
}

func TestGetResources_EmptyWhenNone(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectData(t, "myproject", map[string]any{
		"name": "myproject",
	})

	result := registryGetResources(map[string]any{"name": "myproject"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	resources, ok := resp["resources"].(map[string]any)
	if !ok {
		t.Fatalf("want resources map, got %T", resp["resources"])
	}
	if len(resources) != 0 {
		t.Errorf("want empty resources, got %v", resources)
	}
}

// ── registryWriteDeployCheck ────────────────────────────────────────────────────

func TestRegistryWriteDeployCheck_WritesEntry(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"name": "myproject",
		"entry": map[string]any{
			"status": "pass",
			"date":   "2026-07-21",
		},
	}
	result := registryWriteDeployCheck(args)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	log, _, err := s.GetDeployChecks("myproject", "", "")
	if err != nil {
		t.Fatalf("GetDeployChecks: %v", err)
	}
	if len(log) != 1 {
		t.Fatalf("want 1 entry, got %d", len(log))
	}
	if log[0]["status"] != "pass" {
		t.Errorf("want status=pass, got %v", log[0]["status"])
	}
}

func TestRegistryWriteDeployCheck_AddsRecordedAt(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"name":  "myproject",
		"entry": map[string]any{"status": "fail"},
	}
	registryWriteDeployCheck(args)

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	log, _, err := s.GetDeployChecks("myproject", "", "")
	if err != nil {
		t.Fatalf("GetDeployChecks: %v", err)
	}

	if _, ok := log[0]["_recorded_at"]; !ok {
		t.Error("want _recorded_at field, not present")
	}
}

func TestRegistryWriteDeployCheck_ReturnsTotalEntries(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"name":  "myproject",
		"entry": map[string]any{"status": "pass"},
	}
	registryWriteDeployCheck(args)
	result := registryWriteDeployCheck(args)

	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)

	if resp["ok"] != true {
		t.Errorf("want ok=true, got %v", resp["ok"])
	}
	total, _ := resp["total_entries"].(float64)
	if total != 2 {
		t.Errorf("want total_entries=2, got %v", resp["total_entries"])
	}
}

func TestRegistryWriteDeployCheck_MissingNameOrEntry(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryWriteDeployCheck(map[string]any{"name": "myproject"})
	if !result.IsError {
		t.Fatal("want error when entry missing, got none")
	}

	result = registryWriteDeployCheck(map[string]any{"entry": map[string]any{"status": "pass"}})
	if !result.IsError {
		t.Fatal("want error when name missing, got none")
	}
}

// ── registryGetDeployChecks ─────────────────────────────────────────────────────

func seedDeployChecks(t *testing.T, name string, entries []map[string]any) {
	t.Helper()
	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	for _, e := range entries {
		if _, err := s.WriteDeployCheck(name, e); err != nil {
			t.Fatalf("WriteDeployCheck: %v", err)
		}
	}
}

func TestRegistryGetDeployChecks_ReturnsAllEntries(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	seedDeployChecks(t, "myproject", []map[string]any{
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-01"},
	})

	result := registryGetDeployChecks(map[string]any{"name": "myproject"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	total, _ := resp["total"].(float64)
	if total != 2 {
		t.Fatalf("want total=2, got %v", resp["total"])
	}
}

func TestRegistryGetDeployChecks_FiltersBySince(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	seedDeployChecks(t, "myproject", []map[string]any{
		{"status": "pass", "date": "2026-04-15"},
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-10"},
	})

	result := registryGetDeployChecks(map[string]any{"name": "myproject", "since": "2026-05-01"})
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	total, _ := resp["total"].(float64)
	if total != 2 {
		t.Fatalf("want total=2 (since 2026-05-01), got %v", resp["total"])
	}
}

func TestRegistryGetDeployChecks_FiltersByUntil(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	seedDeployChecks(t, "myproject", []map[string]any{
		{"status": "pass", "date": "2026-04-15"},
		{"status": "pass", "date": "2026-05-01"},
		{"status": "fail", "date": "2026-06-10"},
	})

	result := registryGetDeployChecks(map[string]any{"name": "myproject", "until": "2026-05-01"})
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	total, _ := resp["total"].(float64)
	if total != 2 {
		t.Fatalf("want total=2 (until 2026-05-01), got %v", resp["total"])
	}
}

func TestRegistryGetDeployChecks_EmptyWhenMissing(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryGetDeployChecks(map[string]any{"name": "nonexistent"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	entries, ok := resp["entries"].([]any)
	if !ok {
		t.Fatalf("want entries array, got %T", resp["entries"])
	}
	if len(entries) != 0 {
		t.Errorf("want empty entries, got %v", entries)
	}
}

func TestRegistryGetDeployChecks_MissingName(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryGetDeployChecks(map[string]any{})
	if !result.IsError {
		t.Fatal("want error when name missing, got none")
	}
}

// ── SQLite-backed store (DOTFILES-23) ────────────────────────────────────────
//
// These tests exercise the store type that will back all registry data
// (projects, plans, audit, issues, deploy checks) in a single SQLite DB
// (WAL mode). No implementation exists yet — store.go lands in the next step.

func openTestStore(t *testing.T) *store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "registry.db")
	s, err := newStore(dbPath)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStore_ProjectDotPathRoundtrip(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetProject("myproject", map[string]any{"name": "myproject"}); err != nil {
		t.Fatalf("SetProject: %v", err)
	}

	data, err := s.GetProject("myproject")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	dotSet(data, "deploy.cluster", "general-production")
	if err := s.SetProject("myproject", data); err != nil {
		t.Fatalf("SetProject (update): %v", err)
	}

	data2, err := s.GetProject("myproject")
	if err != nil {
		t.Fatalf("GetProject (after update): %v", err)
	}
	if got := dotGet(data2, "deploy.cluster"); got != "general-production" {
		t.Errorf("want deploy.cluster=general-production, got %v", got)
	}
}

func TestStore_PlanWriteGetUpdateStep(t *testing.T) {
	s := openTestStore(t)

	plan := map[string]any{
		"ticket":  "TEST-1",
		"summary": "test plan",
		"plan_steps": []any{
			map[string]any{"title": "step one", "status": "pending"},
		},
	}
	if err := s.WritePlan("myproject", "TEST-1", plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	got, err := s.GetPlan("myproject", "TEST-1")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	id1 := got["id"]
	if id1 == nil {
		t.Fatal("want id assigned on first write, got nil")
	}
	createdAt1, _ := got["created_at"].(string)
	if createdAt1 == "" {
		t.Fatal("want created_at stamped on first write, got empty")
	}

	if err := s.UpdateStep("myproject", "TEST-1", 0, "done"); err != nil {
		t.Fatalf("UpdateStep: %v", err)
	}

	got2, err := s.GetPlan("myproject", "TEST-1")
	if err != nil {
		t.Fatalf("GetPlan (after UpdateStep): %v", err)
	}
	if got2["id"] != id1 {
		t.Errorf("want id unchanged after UpdateStep, want %v got %v", id1, got2["id"])
	}
	if got2["created_at"] != createdAt1 {
		t.Errorf("want created_at unchanged after UpdateStep, want %v got %v", createdAt1, got2["created_at"])
	}
	steps, ok := got2["plan_steps"].([]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("want 1 plan_step, got %v", got2["plan_steps"])
	}
	step0, _ := steps[0].(map[string]any)
	if step0["status"] != "done" {
		t.Errorf("want step 0 status=done, got %v", step0["status"])
	}

	time.Sleep(10 * time.Millisecond)
	plan["summary"] = "updated summary"
	if err := s.WritePlan("myproject", "TEST-1", plan); err != nil {
		t.Fatalf("WritePlan (rewrite): %v", err)
	}
	got3, err := s.GetPlan("myproject", "TEST-1")
	if err != nil {
		t.Fatalf("GetPlan (after rewrite): %v", err)
	}
	if got3["id"] != id1 {
		t.Errorf("want id unchanged after rewrite, want %v got %v", id1, got3["id"])
	}
	if got3["created_at"] != createdAt1 {
		t.Errorf("want created_at unchanged after rewrite, want %v got %v", createdAt1, got3["created_at"])
	}
}

func TestStore_UpdateStep_StampsAndClearsDoneAt(t *testing.T) {
	s := openTestStore(t)

	plan := map[string]any{
		"ticket":  "TEST-2",
		"summary": "done_at test plan",
		"plan_steps": []any{
			map[string]any{"title": "step one", "status": "pending"},
		},
	}
	if err := s.WritePlan("myproject", "TEST-2", plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	if err := s.UpdateStep("myproject", "TEST-2", 0, "done"); err != nil {
		t.Fatalf("UpdateStep (done): %v", err)
	}

	got, err := s.GetPlan("myproject", "TEST-2")
	if err != nil {
		t.Fatalf("GetPlan (after done): %v", err)
	}
	steps, ok := got["plan_steps"].([]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("want 1 plan_step, got %v", got["plan_steps"])
	}
	step0, _ := steps[0].(map[string]any)
	doneAt, _ := step0["done_at"].(string)
	if doneAt == "" {
		t.Fatal("want done_at set after transition to done, got empty")
	}
	if _, err := time.Parse(time.RFC3339, doneAt); err != nil {
		t.Errorf("want done_at to parse as RFC3339, got %q: %v", doneAt, err)
	}

	if err := s.UpdateStep("myproject", "TEST-2", 0, "pending"); err != nil {
		t.Fatalf("UpdateStep (pending): %v", err)
	}

	got2, err := s.GetPlan("myproject", "TEST-2")
	if err != nil {
		t.Fatalf("GetPlan (after pending): %v", err)
	}
	steps2, ok := got2["plan_steps"].([]any)
	if !ok || len(steps2) != 1 {
		t.Fatalf("want 1 plan_step, got %v", got2["plan_steps"])
	}
	step0b, _ := steps2[0].(map[string]any)
	if _, present := step0b["done_at"]; present {
		t.Errorf("want done_at cleared after moving out of done, got %v", step0b["done_at"])
	}
}

func TestStore_AuditWriteAndDateRangeFilter(t *testing.T) {
	s := openTestStore(t)

	entries := []map[string]any{
		{"ticket": "T1", "date": "2026-04-15"},
		{"ticket": "T2", "date": "2026-05-01"},
		{"ticket": "T3", "date": "2026-06-10"},
	}
	for _, e := range entries {
		if _, err := s.WriteAudit("myproject", e); err != nil {
			t.Fatalf("WriteAudit: %v", err)
		}
	}

	filtered, total, err := s.GetAudit("myproject", "2026-05-01", "")
	if err != nil {
		t.Fatalf("GetAudit: %v", err)
	}
	if total != 2 {
		t.Fatalf("want total=2 (since 2026-05-01), got %d", total)
	}
	if len(filtered) != 2 {
		t.Fatalf("want 2 entries, got %d", len(filtered))
	}
	if filtered[0]["ticket"] != "T2" || filtered[1]["ticket"] != "T3" {
		t.Errorf("want order [T2, T3], got [%v, %v]", filtered[0]["ticket"], filtered[1]["ticket"])
	}

	filtered, total, err = s.GetAudit("myproject", "", "2026-05-01")
	if err != nil {
		t.Fatalf("GetAudit (until): %v", err)
	}
	if total != 2 {
		t.Fatalf("want total=2 (until 2026-05-01), got %d", total)
	}
	if filtered[0]["ticket"] != "T1" || filtered[1]["ticket"] != "T2" {
		t.Errorf("want order [T1, T2], got [%v, %v]", filtered[0]["ticket"], filtered[1]["ticket"])
	}
}

func TestStore_IssueRoundtrip(t *testing.T) {
	s := openTestStore(t)

	total, err := s.WriteIssue("myproject", map[string]any{
		"tool":     "registry_get_project",
		"error":    "project not found",
		"severity": "error",
	})
	if err != nil {
		t.Fatalf("WriteIssue: %v", err)
	}
	if total != 1 {
		t.Fatalf("want total=1, got %d", total)
	}

	issues, err := s.GetIssues("myproject")
	if err != nil {
		t.Fatalf("GetIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %d", len(issues))
	}
	if issues[0]["tool"] != "registry_get_project" {
		t.Errorf("want tool=registry_get_project, got %v", issues[0]["tool"])
	}
}

func TestStore_DeployCheckRoundtrip(t *testing.T) {
	s := openTestStore(t)

	total, err := s.WriteDeployCheck("myproject", map[string]any{
		"status": "pass",
		"date":   "2026-05-01",
	})
	if err != nil {
		t.Fatalf("WriteDeployCheck: %v", err)
	}
	if total != 1 {
		t.Fatalf("want total=1, got %d", total)
	}

	entries, total2, err := s.GetDeployChecks("myproject", "", "")
	if err != nil {
		t.Fatalf("GetDeployChecks: %v", err)
	}
	if total2 != 1 {
		t.Fatalf("want total=1, got %d", total2)
	}
	if entries[0]["status"] != "pass" {
		t.Errorf("want status=pass, got %v", entries[0]["status"])
	}
}

// ── registryDeriveBranchName ─────────────────────────────────────────────────────

func TestRegistryDeriveBranchName_PrefixByType(t *testing.T) {
	cases := []struct {
		ticketType string
		want       string
	}{
		{"Story", "feat"},
		{"Task", "feat"},
		{"Feature", "feat"},
		{"Bug", "fix"},
		{"Defect", "fix"},
		{"Research", "research"},
		{"Refactor", "chore"},
		{"Maintenance", "chore"},
		{"Unknown", "chore"},
		{"", "chore"},
	}
	for _, c := range cases {
		result := registryDeriveBranchName(map[string]any{"ticket": "ONE-1234", "ticket_type": c.ticketType})
		if result.IsError {
			t.Fatalf("unexpected error for type %q: %s", c.ticketType, result.Content[0].Text)
		}
		var resp map[string]any
		json.Unmarshal([]byte(result.Content[0].Text), &resp)
		if resp["prefix"] != c.want {
			t.Errorf("type %q: want prefix %q, got %v", c.ticketType, c.want, resp["prefix"])
		}
	}
}

func TestRegistryDeriveBranchName_SlugifiesDescription(t *testing.T) {
	result := registryDeriveBranchName(map[string]any{
		"ticket":      "ONE-1234",
		"ticket_type": "Bug",
		"description": "Fix Null Pointer in User Service!!",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	want := "fix/ONE-1234-fix-null-pointer-in-user-service"
	if resp["branch"] != want {
		t.Errorf("want branch %q, got %v", want, resp["branch"])
	}
}

func TestRegistryDeriveBranchName_NoDescriptionOmitsSlug(t *testing.T) {
	result := registryDeriveBranchName(map[string]any{"ticket": "ONE-1234", "ticket_type": "Feature"})
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	if resp["branch"] != "feat/ONE-1234" {
		t.Errorf("want branch feat/ONE-1234, got %v", resp["branch"])
	}
}

func TestRegistryDeriveBranchName_MissingTicket(t *testing.T) {
	result := registryDeriveBranchName(map[string]any{"ticket_type": "Bug"})
	if !result.IsError {
		t.Fatal("want error when ticket missing, got none")
	}
}

// ── registryInferAuditType ───────────────────────────────────────────────────────

func TestRegistryInferAuditType_ByPrefix(t *testing.T) {
	cases := map[string]string{
		"feat/ONE-1-thing":     "feature",
		"fix/ONE-1-thing":      "bugfix",
		"chore/ONE-1-thing":    "chore",
		"refactor/ONE-1-thing": "refactor",
		"docs/ONE-1-thing":     "docs",
		"test/ONE-1-thing":     "test",
		"weird/ONE-1-thing":    "chore",
		"no-slash-branch":      "chore",
	}
	for branch, want := range cases {
		result := registryInferAuditType(map[string]any{"branch": branch})
		var resp map[string]any
		json.Unmarshal([]byte(result.Content[0].Text), &resp)
		if resp["type"] != want {
			t.Errorf("branch %q: want type %q, got %v", branch, want, resp["type"])
		}
	}
}

// ── registryIsFakeTicket ──────────────────────────────────────────────────────────

func TestRegistryIsFakeTicket_TrueWithinCounter(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectData(t, "private-dotfiles", map[string]any{"name": "private-dotfiles", "ticket_counter": "29"})

	result := registryIsFakeTicket(map[string]any{"name": "private-dotfiles", "ticket": "DOTFILES-15"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	if resp["is_fake"] != true {
		t.Errorf("want is_fake=true, got %v", resp["is_fake"])
	}
}

func TestRegistryIsFakeTicket_FalseForRealJiraKey(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectData(t, "emily", map[string]any{"name": "emily"})

	result := registryIsFakeTicket(map[string]any{"name": "emily", "ticket": "ONE-25305"})
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	if resp["is_fake"] != false {
		t.Errorf("want is_fake=false, got %v", resp["is_fake"])
	}
}

func TestRegistryIsFakeTicket_FalseWhenBeyondCounter(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectData(t, "private-dotfiles", map[string]any{"name": "private-dotfiles", "ticket_counter": "5"})

	result := registryIsFakeTicket(map[string]any{"name": "private-dotfiles", "ticket": "DOTFILES-99"})
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	if resp["is_fake"] != false {
		t.Errorf("want is_fake=false (beyond counter), got %v", resp["is_fake"])
	}
}

func TestRegistryIsFakeTicket_ExplicitPrefixOverride(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectData(t, "custom-project", map[string]any{"name": "custom-project", "ticket_counter": "3"})

	result := registryIsFakeTicket(map[string]any{"name": "custom-project", "ticket": "CUST-2", "prefix": "CUST"})
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	if resp["is_fake"] != true {
		t.Errorf("want is_fake=true with explicit prefix, got %v", resp["is_fake"])
	}
}

// ── registryUnionFiles ────────────────────────────────────────────────────────────

func TestRegistryUnionFiles_DedupesPreservingOrder(t *testing.T) {
	result := registryUnionFiles(map[string]any{
		"file_groups": []any{
			[]any{"a.go", "b.go"},
			[]any{"b.go", "c.go"},
			[]any{"a.go"},
		},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var resp map[string]any
	json.Unmarshal([]byte(result.Content[0].Text), &resp)
	files, ok := resp["files"].([]any)
	if !ok {
		t.Fatalf("want files array, got %T", resp["files"])
	}
	want := []string{"a.go", "b.go", "c.go"}
	if len(files) != len(want) {
		t.Fatalf("want %v, got %v", want, files)
	}
	for i, f := range files {
		if f != want[i] {
			t.Errorf("index %d: want %q, got %v", i, want[i], f)
		}
	}
}

func TestRegistryUnionFiles_MissingFileGroups(t *testing.T) {
	result := registryUnionFiles(map[string]any{})
	if !result.IsError {
		t.Fatal("want error when file_groups missing, got none")
	}
}

// ── proposals over MCP (DOTFILES-34 step 2) ──────────────────────────────────

func sampleProposalArgs(sourceRef, summary string) map[string]any {
	return map[string]any{
		"name": "private-dotfiles",
		"proposal": map[string]any{
			"source":           "slack",
			"source_channel":   "C0STUART",
			"source_permalink": "https://example.slack.com/archives/C0STUART/p" + sourceRef,
			"source_ref":       sourceRef,
			"kind":             "plan",
			"summary":          summary,
			"payload":          map[string]any{"ticket": "DOTFILES-99"},
		},
	}
}

func decodeToolResult(t *testing.T, r ToolResult) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal([]byte(r.Content[0].Text), &resp); err != nil {
		t.Fatalf("decode tool result %q: %v", r.Content[0].Text, err)
	}
	return resp
}

func writeTestProposal(t *testing.T, sourceRef, summary string) int64 {
	t.Helper()
	result := registryWriteProposal(sampleProposalArgs(sourceRef, summary))
	if result.IsError {
		t.Fatalf("registry_write_proposal: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	idf, ok := resp["id"].(float64)
	if !ok {
		t.Fatalf("want numeric id, got %T (%v)", resp["id"], resp["id"])
	}
	return int64(idf)
}

func TestRegistryWriteProposal_ReturnsOkAndID(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryWriteProposal(sampleProposalArgs("1756600000.000100", "plan a thing"))
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	if resp["ok"] != true {
		t.Errorf("want ok=true, got %v", resp["ok"])
	}
	id, ok := resp["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("want positive numeric id, got %T (%v)", resp["id"], resp["id"])
	}
}

func TestRegistryWriteProposal_StampsCreatedAtServerSide(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := sampleProposalArgs("1756600000.000200", "stamped")
	// A caller-supplied created_at must not win — the server stamps it.
	args["proposal"].(map[string]any)["created_at"] = "1999-01-01T00:00:00Z"

	id := int64(decodeToolResult(t, registryWriteProposal(args))["id"].(float64))

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	p, err := s.GetProposal(id)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if p.CreatedAt == "1999-01-01T00:00:00Z" {
		t.Error("caller-supplied created_at was persisted; want server stamp")
	}
	if _, err := time.Parse(time.RFC3339, p.CreatedAt); err != nil {
		t.Errorf("created_at %q is not RFC3339: %v", p.CreatedAt, err)
	}
}

func TestRegistryWriteProposal_MissingNameOrProposal(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	if !registryWriteProposal(map[string]any{"name": "private-dotfiles"}).IsError {
		t.Error("want error when proposal missing, got none")
	}
	args := sampleProposalArgs("1756600000.000300", "no name")
	delete(args, "name")
	if !registryWriteProposal(args).IsError {
		t.Error("want error when name missing, got none")
	}
}

func TestRegistryWriteProposal_DuplicateSourceRefErrorsNotPanics(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeTestProposal(t, "1756600000.000400", "first")

	result := registryWriteProposal(sampleProposalArgs("1756600000.000400", "duplicate"))
	if !result.IsError {
		t.Fatal("want error on duplicate source_ref, got success")
	}
	// The poller keys "already seen" off this text, so it must name the cause.
	if !strings.Contains(result.Content[0].Text, "already exists") {
		t.Errorf("duplicate error should say 'already exists', got %s", result.Content[0].Text)
	}
}

func TestRegistryGetProposals_ReturnsDocumentedShape(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeTestProposal(t, "1756600000.000500", "one")

	result := registryGetProposals(map[string]any{"name": "private-dotfiles"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	proposals, ok := resp["proposals"].([]any)
	if !ok {
		t.Fatalf("want proposals array, got %T", resp["proposals"])
	}
	if len(proposals) != 1 {
		t.Fatalf("want 1 proposal, got %d", len(proposals))
	}
	p := proposals[0].(map[string]any)
	for _, key := range []string{"id", "project", "source_channel", "source_permalink", "source_ref", "kind", "summary", "status", "created_at"} {
		if _, ok := p[key]; !ok {
			t.Errorf("proposal missing key %q", key)
		}
	}
	if p["status"] != "pending" {
		t.Errorf("want status=pending, got %v", p["status"])
	}
}

func TestRegistryGetProposals_EmptyWhenNone(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	resp := decodeToolResult(t, registryGetProposals(map[string]any{"name": "private-dotfiles"}))
	proposals, ok := resp["proposals"].([]any)
	if !ok {
		t.Fatalf("want proposals array, got %T", resp["proposals"])
	}
	if len(proposals) != 0 {
		t.Fatalf("want empty array, got %d", len(proposals))
	}
}

func TestRegistryGetProposals_FiltersByStatus(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	approvedID := writeTestProposal(t, "1756600000.000600", "to approve")
	writeTestProposal(t, "1756600000.000700", "stays pending")

	upd := registryUpdateProposal(map[string]any{
		"name": "private-dotfiles", "id": float64(approvedID), "status": "approved",
	})
	if upd.IsError {
		t.Fatalf("registry_update_proposal: %s", upd.Content[0].Text)
	}

	pending := decodeToolResult(t, registryGetProposals(map[string]any{
		"name": "private-dotfiles", "status": "pending",
	}))["proposals"].([]any)
	if len(pending) != 1 {
		t.Fatalf("want 1 pending, got %d", len(pending))
	}
	if pending[0].(map[string]any)["summary"] != "stays pending" {
		t.Errorf("wrong pending row: %v", pending[0])
	}

	approved := decodeToolResult(t, registryGetProposals(map[string]any{
		"name": "private-dotfiles", "status": "approved",
	}))["proposals"].([]any)
	if len(approved) != 1 {
		t.Fatalf("want 1 approved, got %d", len(approved))
	}
}

func TestRegistryGetProposals_FiltersByID(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeTestProposal(t, "1756600000.000800", "first")
	wantID := writeTestProposal(t, "1756600000.000900", "second")

	resp := decodeToolResult(t, registryGetProposals(map[string]any{
		"name": "private-dotfiles", "id": float64(wantID),
	}))
	proposals := resp["proposals"].([]any)
	if len(proposals) != 1 {
		t.Fatalf("want exactly 1 proposal for id filter, got %d", len(proposals))
	}
	if proposals[0].(map[string]any)["summary"] != "second" {
		t.Errorf("want summary=second, got %v", proposals[0].(map[string]any)["summary"])
	}
}

func TestRegistryGetProposals_UnknownIDErrors(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryGetProposals(map[string]any{"name": "private-dotfiles", "id": float64(4242)})
	if !result.IsError {
		t.Fatal("want error for unknown id, got success")
	}
}

func TestRegistryGetProposals_IDFromAnotherProjectNotReturned(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestProposal(t, "1756600000.001000", "mine")

	result := registryGetProposals(map[string]any{"name": "some-other-project", "id": float64(id)})
	if !result.IsError {
		t.Fatal("want error when id belongs to another project, got success")
	}
}

func TestRegistryUpdateProposal_ApprovesAndRecordsNote(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestProposal(t, "1756600000.001100", "approve me")

	result := registryUpdateProposal(map[string]any{
		"name": "private-dotfiles", "id": float64(id),
		"status": "approved", "decision_note": "lgtm",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if decodeToolResult(t, result)["ok"] != true {
		t.Error("want ok=true")
	}

	s, _ := getStore()
	p, err := s.GetProposal(id)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if p.Status != "approved" {
		t.Errorf("want status=approved, got %q", p.Status)
	}
	if p.DecisionNote != "lgtm" {
		t.Errorf("want decision_note=lgtm, got %q", p.DecisionNote)
	}
	if p.DecidedAt == nil {
		t.Error("want decided_at stamped on approval")
	}
}

func TestRegistryUpdateProposal_SupersedeLinksRevisionChain(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	oldID := writeTestProposal(t, "1756600000.001200", "rev 1")
	newID := writeTestProposal(t, "1756600000.001300", "rev 2")

	result := registryUpdateProposal(map[string]any{
		"name": "private-dotfiles", "id": float64(oldID),
		"status": "superseded", "superseded_by": float64(newID),
		"decision_note": "make it smaller",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	s, _ := getStore()
	p, err := s.GetProposal(oldID)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if p.Status != "superseded" {
		t.Errorf("want status=superseded, got %q", p.Status)
	}
	if p.SupersededBy == nil || *p.SupersededBy != newID {
		t.Errorf("want superseded_by=%d, got %v", newID, p.SupersededBy)
	}
	if p.DecisionNote != "make it smaller" {
		t.Errorf("want the pushback note preserved, got %q", p.DecisionNote)
	}
}

func TestRegistryUpdateProposal_SupersededRequiresSupersededBy(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestProposal(t, "1756600000.001400", "rev 1")

	result := registryUpdateProposal(map[string]any{
		"name": "private-dotfiles", "id": float64(id), "status": "superseded",
	})
	if !result.IsError {
		t.Fatal("want error when superseded_by omitted, got success")
	}
}

func TestRegistryUpdateProposal_MissingIDErrorsCleanly(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryUpdateProposal(map[string]any{
		"name": "private-dotfiles", "id": float64(9999), "status": "approved",
	})
	if !result.IsError {
		t.Fatal("want error for missing id, got success")
	}
	if !strings.Contains(result.Content[0].Text, "not found") {
		t.Errorf("want 'not found' in error, got %s", result.Content[0].Text)
	}
}

func TestRegistryUpdateProposal_MissingArgs(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestProposal(t, "1756600000.001500", "args")

	if !registryUpdateProposal(map[string]any{"name": "private-dotfiles", "id": float64(id)}).IsError {
		t.Error("want error when status missing, got none")
	}
	if !registryUpdateProposal(map[string]any{"name": "private-dotfiles", "status": "approved"}).IsError {
		t.Error("want error when id missing, got none")
	}
	if !registryUpdateProposal(map[string]any{"id": float64(id), "status": "approved"}).IsError {
		t.Error("want error when name missing, got none")
	}
	if !registryUpdateProposal(map[string]any{
		"name": "private-dotfiles", "id": float64(id), "status": "bogus",
	}).IsError {
		t.Error("want error for invalid status, got none")
	}
}

func TestRegistryUpdateProposal_WrongProjectRejected(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestProposal(t, "1756600000.001600", "mine")

	result := registryUpdateProposal(map[string]any{
		"name": "some-other-project", "id": float64(id), "status": "approved",
	})
	if !result.IsError {
		t.Fatal("want error when updating another project's proposal, got success")
	}
}

func TestProposalToolsRegisteredInDispatchAndSchemas(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	names := map[string]bool{}
	for _, tool := range allTools() {
		names[tool.Name] = true
	}
	for _, want := range []string{"registry_write_proposal", "registry_get_proposals", "registry_update_proposal"} {
		if !names[want] {
			t.Errorf("tool %q missing from tools/list", want)
		}
		if got := dispatch(want, map[string]any{}); strings.Contains(got.Content[0].Text, "unknown tool") {
			t.Errorf("tool %q not wired into dispatch", want)
		}
	}
}

// ── registry_index (DOTFILES-36) ─────────────────────────────────────────────

// decodeIndex parses a registry_index ToolResult into its projects array.
func decodeIndex(t *testing.T, result ToolResult) []map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var body struct {
		Projects []map[string]any `json:"projects"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &body); err != nil {
		t.Fatalf("unmarshal index: %v (body: %s)", err, result.Content[0].Text)
	}
	return body.Projects
}

func TestRegistryIndex_ReturnsThinRowPerProject(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	if err := s.SetProject("alpha", map[string]any{
		"name":      "alpha",
		"purpose":   "Alpha does the alpha thing",
		"repo":      map[string]any{"workspace": "clearlinkit", "localPath": "/tmp/alpha"},
		"resources": map[string]any{"slack": map[string]any{"channel": "C0NOISE"}},
	}); err != nil {
		t.Fatalf("SetProject: %v", err)
	}
	if err := s.WritePlan("alpha", "ALPHA-9", map[string]any{
		"ticket":     "ALPHA-9",
		"summary":    "live work",
		"plan_steps": []any{map[string]any{"id": 1, "status": "pending"}},
	}); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	result := registryIndex(map[string]any{})
	projects := decodeIndex(t, result)
	if len(projects) != 1 {
		t.Fatalf("want 1 project row, got %d", len(projects))
	}
	row := projects[0]
	if row["name"] != "alpha" {
		t.Errorf("name: got %v", row["name"])
	}
	if row["purpose"] != "Alpha does the alpha thing" {
		t.Errorf("purpose: got %v", row["purpose"])
	}
	if row["local_path"] != "/tmp/alpha" {
		t.Errorf("local_path: got %v", row["local_path"])
	}
	if row["repo"] != "clearlinkit/alpha" {
		t.Errorf("repo: got %v", row["repo"])
	}
	ap, ok := row["active_plan"].(map[string]any)
	if !ok {
		t.Fatalf("active_plan: want an object, got %v", row["active_plan"])
	}
	if ap["ticket"] != "ALPHA-9" || ap["summary"] != "live work" {
		t.Errorf("active_plan: got %v", ap)
	}

	// Routing must never drag heavy subtrees along.
	for _, forbidden := range []string{"plan_steps", "resources", "C0NOISE"} {
		if strings.Contains(result.Content[0].Text, forbidden) {
			t.Errorf("index body leaks %q: %s", forbidden, result.Content[0].Text)
		}
	}
}

func TestRegistryIndex_EmptyRegistryReturnsEmptyListNotNull(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryIndex(map[string]any{})
	projects := decodeIndex(t, result)
	if projects == nil {
		t.Fatal("want an empty array, got JSON null — callers iterate this")
	}
	if len(projects) != 0 {
		t.Errorf("want 0 rows on an empty registry, got %d", len(projects))
	}
}

// ── agent runs (DOTFILES-38) ─────────────────────────────────────────────────

func decodeRuns(t *testing.T, result ToolResult) []map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var body struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &body); err != nil {
		t.Fatalf("unmarshal runs: %v (body: %s)", err, result.Content[0].Text)
	}
	return body.Runs
}

func writeTestRun(t *testing.T, args map[string]any) int64 {
	t.Helper()
	result := registryWriteRun(args)
	if result.IsError {
		t.Fatalf("registryWriteRun: %s", result.Content[0].Text)
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &body); err != nil {
		t.Fatalf("unmarshal id: %v", err)
	}
	return body.ID
}

func TestRegistryRun_WriteGetUpdateCycle(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestRun(t, map[string]any{
		"name":        "private-dotfiles",
		"proposal_id": float64(12),
		"ticket":      "DOTFILES-99",
		"cursor":      map[string]any{"branch": "feat/x", "last_step": float64(0)},
	})

	runs := decodeRuns(t, registryGetRuns(map[string]any{"name": "private-dotfiles", "id": float64(id)}))
	if len(runs) != 1 {
		t.Fatalf("want 1 run by id, got %d", len(runs))
	}
	if runs[0]["phase"] != "planning" || runs[0]["status"] != "running" {
		t.Errorf("defaults: got phase=%v status=%v", runs[0]["phase"], runs[0]["status"])
	}

	upd := registryUpdateRun(map[string]any{
		"name": "private-dotfiles", "id": float64(id),
		"phase": "building", "status": "running",
		"cursor": map[string]any{"branch": "feat/x", "last_step": float64(2)},
		"note":   "step 2 done",
	})
	if upd.IsError {
		t.Fatalf("registryUpdateRun: %s", upd.Content[0].Text)
	}

	runs = decodeRuns(t, registryGetRuns(map[string]any{"name": "private-dotfiles", "id": float64(id)}))
	cursor, _ := runs[0]["cursor"].(map[string]any)
	if runs[0]["phase"] != "building" || cursor["last_step"] != float64(2) {
		t.Errorf("advance did not persist: %v", runs[0])
	}
}

// Omitting cursor must not erase where the chain got to — a phase-only advance
// that wiped the cursor would make resume re-run completed steps.
func TestRegistryUpdateRun_OmittedCursorIsPreserved(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestRun(t, map[string]any{
		"name":   "private-dotfiles",
		"cursor": map[string]any{"last_step": float64(4)},
	})
	if r := registryUpdateRun(map[string]any{
		"name": "private-dotfiles", "id": float64(id),
		"phase": "shipping", "status": "running",
	}); r.IsError {
		t.Fatalf("update: %s", r.Content[0].Text)
	}

	runs := decodeRuns(t, registryGetRuns(map[string]any{"name": "private-dotfiles", "id": float64(id)}))
	cursor, _ := runs[0]["cursor"].(map[string]any)
	if cursor["last_step"] != float64(4) {
		t.Errorf("cursor must survive a phase-only advance, got %v", runs[0]["cursor"])
	}
}

func TestRegistryRun_ScopedToProjectAndValidated(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestRun(t, map[string]any{"name": "private-dotfiles"})

	// A caller scoped to another project must not see or advance this run.
	if r := registryGetRuns(map[string]any{"name": "emily", "id": float64(id)}); !r.IsError {
		t.Error("want error reading another project's run by id")
	}
	if r := registryUpdateRun(map[string]any{
		"name": "emily", "id": float64(id), "phase": "building", "status": "running",
	}); !r.IsError {
		t.Error("want error advancing another project's run")
	}

	if r := registryUpdateRun(map[string]any{
		"name": "private-dotfiles", "id": float64(id), "phase": "hammering", "status": "running",
	}); !r.IsError {
		t.Error("want error for invalid phase")
	}
	if r := registryGetRuns(map[string]any{"name": "private-dotfiles", "status": "bogus"}); !r.IsError {
		t.Error("want error for invalid status filter")
	}
}

func TestRegistryGetRuns_EmptyReturnsEmptyListNotNull(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	runs := decodeRuns(t, registryGetRuns(map[string]any{"name": "private-dotfiles"}))
	if runs == nil {
		t.Fatal("want an empty array, got JSON null — callers iterate this")
	}
	if len(runs) != 0 {
		t.Errorf("want 0 runs, got %d", len(runs))
	}
}

// ── inbox tests (DOTFILES-40) ──────────────────────────────────────────────

func sampleInboxArgs(sourceRef, rawText string) map[string]any {
	return map[string]any{
		"inbox": map[string]any{
			"source":           "slack",
			"source_channel":   "C0STUART",
			"source_permalink": "https://example.slack.com/archives/C0STUART/p" + sourceRef,
			"source_ref":       sourceRef,
			"raw_text":         rawText,
		},
	}
}

func writeTestInbox(t *testing.T, sourceRef, rawText string) int64 {
	t.Helper()
	result := registryWriteInbox(sampleInboxArgs(sourceRef, rawText))
	if result.IsError {
		t.Fatalf("registry_write_inbox: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	idf, ok := resp["id"].(float64)
	if !ok {
		t.Fatalf("want numeric id, got %T (%v)", resp["id"], resp["id"])
	}
	return int64(idf)
}

func TestRegistryWriteInbox_ReturnsOkAndID(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryWriteInbox(sampleInboxArgs("1756600100.000100", "inbox ask"))
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	if resp["ok"] != true {
		t.Errorf("want ok=true, got %v", resp["ok"])
	}
	id, ok := resp["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("want positive numeric id, got %T (%v)", resp["id"], resp["id"])
	}
}

func TestRegistryWriteInbox_StampsCreatedAtServerSide(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := sampleInboxArgs("1756600100.000200", "stamped")
	// A caller-supplied created_at must not win — the server stamps it.
	args["inbox"].(map[string]any)["created_at"] = "1999-01-01T00:00:00Z"

	id := int64(decodeToolResult(t, registryWriteInbox(args))["id"].(float64))

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	it, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if it.CreatedAt == "1999-01-01T00:00:00Z" {
		t.Error("caller-supplied created_at was persisted; want server stamp")
	}
	if _, err := time.Parse(time.RFC3339, it.CreatedAt); err != nil {
		t.Errorf("created_at %q is not RFC3339: %v", it.CreatedAt, err)
	}
}

func TestRegistryWriteInbox_DuplicateSourceRefErrorsNotPanics(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	sourceRef := "1756600100.000300"
	writeTestInbox(t, sourceRef, "first")

	// The UNIQUE(source, source_ref) index is the dedup key: a re-seen
	// message must come back as a clear "already exists" error.
	result := registryWriteInbox(sampleInboxArgs(sourceRef, "second"))
	if !result.IsError {
		t.Error("want error on duplicate source_ref, got none")
	}
	errMsg := result.Content[0].Text
	if !strings.Contains(errMsg, "already exists") || !strings.Contains(errMsg, sourceRef) {
		t.Errorf("want clean 'already exists' error, got: %s", errMsg)
	}
}

func TestRegistryWriteInbox_RejectsCallerSuppliedID(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := sampleInboxArgs("1756600100.000400", "with id")
	args["inbox"].(map[string]any)["id"] = 999

	result := registryWriteInbox(args)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	// Verify that a different id was assigned, not the caller-supplied one.
	resp := decodeToolResult(t, result)
	assignedID := int64(resp["id"].(float64))
	if assignedID == 999 {
		t.Error("caller-supplied id was persisted; want server-assigned id")
	}
}

func TestRegistryGetInbox_ReturnsDocumentedShape(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	_ = writeTestInbox(t, "1756600100.000500", "test inbox")

	result := registryGetInbox(map[string]any{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	items, ok := resp["inbox"].([]any)
	if !ok {
		t.Fatalf("want array of inbox items, got %T", resp["inbox"])
	}
	if len(items) == 0 {
		t.Fatal("want at least one inbox item")
	}
	item := items[0].(map[string]any)
	if _, ok := item["id"]; !ok {
		t.Error("want id field")
	}
	if _, ok := item["source"]; !ok {
		t.Error("want source field")
	}
	if _, ok := item["raw_text"]; !ok {
		t.Error("want raw_text field")
	}
	if _, ok := item["status"]; !ok {
		t.Error("want status field")
	}
	if _, ok := item["created_at"]; !ok {
		t.Error("want created_at field")
	}
}

func TestRegistryGetInbox_EmptyWhenNone(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	result := registryGetInbox(map[string]any{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	items := resp["inbox"]
	if items == nil {
		t.Fatal("want an empty array, got JSON null — callers iterate this")
	}
	itemsArr, ok := items.([]any)
	if !ok {
		t.Fatalf("want array, got %T", items)
	}
	if len(itemsArr) != 0 {
		t.Errorf("want 0 items, got %d", len(itemsArr))
	}
}

func TestRegistryGetInbox_FiltersByStatus(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	_ = writeTestInbox(t, "1756600100.000600", "new item")
	id2 := writeTestInbox(t, "1756600100.000601", "triaged item")

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	// Mark the second one as triaged.
	if err := s.UpdateInbox(id2, "triaged", "plan", "private-dotfiles", "", nil); err != nil {
		t.Fatalf("UpdateInbox: %v", err)
	}

	// Get only new items.
	result := registryGetInbox(map[string]any{"status": "new"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	items := resp["inbox"].([]any)
	if len(items) != 1 {
		t.Errorf("want 1 new item, got %d", len(items))
	}

	// Get only triaged items.
	result = registryGetInbox(map[string]any{"status": "triaged"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	resp = decodeToolResult(t, result)
	items = resp["inbox"].([]any)
	if len(items) != 1 {
		t.Errorf("want 1 triaged item, got %d", len(items))
	}
}

func TestRegistryUpdateInbox_AdvancesStatusSingleStatement(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestInbox(t, "1756600100.000700", "to update")

	result := registryUpdateInbox(map[string]any{
		"id":      float64(id),
		"status":  "triaged",
		"triage":  "plan",
		"project": "private-dotfiles",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	resp := decodeToolResult(t, result)
	if resp["ok"] != true {
		t.Errorf("want ok=true, got %v", resp["ok"])
	}

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	it, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if it.Status != "triaged" {
		t.Errorf("want status 'triaged', got %q", it.Status)
	}
	if it.Triage != "plan" {
		t.Errorf("want triage 'plan', got %q", it.Triage)
	}
	if it.Project == nil || *it.Project != "private-dotfiles" {
		t.Errorf("want project 'private-dotfiles', got %v", it.Project)
	}
}

func TestRegistryUpdateInbox_OmittedFieldsLeaveStoredValueUnchanged(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	id := writeTestInbox(t, "1756600100.000800", "with values")

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	// Set some values first.
	if err := s.UpdateInbox(id, "triaged", "plan", "private-dotfiles", "initial note", nil); err != nil {
		t.Fatalf("UpdateInbox: %v", err)
	}

	// Update only status, leaving triage and note unchanged.
	result := registryUpdateInbox(map[string]any{
		"id":     float64(id),
		"status": "routed",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	it, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if it.Status != "routed" {
		t.Errorf("want status 'routed', got %q", it.Status)
	}
	if it.Triage != "plan" {
		t.Errorf("want triage unchanged as 'plan', got %q", it.Triage)
	}
	if it.Note != "initial note" {
		t.Errorf("want note unchanged as 'initial note', got %q", it.Note)
	}
}

func TestInboxToolsRegisteredInDispatchAndSchemas(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	defs := allTools()
	toolsByName := make(map[string]bool)
	for _, tool := range defs {
		toolsByName[tool.Name] = true
	}

	expectedTools := []string{
		"registry_write_inbox",
		"registry_get_inbox",
		"registry_update_inbox",
	}
	for _, name := range expectedTools {
		if !toolsByName[name] {
			t.Errorf("tool %q not found in ToolDefinitions", name)
		}
	}

	// Check dispatch routes each tool.
	for _, name := range expectedTools {
		// Each tool should at least not return "unknown tool" error.
		// (Actual dispatch testing is done elsewhere.)
		_ = name
	}
}

// TestRegistryWriteInbox_IgnoresCallerSuppliedLinkageFields covers the
// ship-review WARNING: project, proposal_id, run_id and note are not in the
// tool's InputSchema, but json.Unmarshal fills them from any extra keys sent, so
// without an explicit reset a capture-time caller could pre-link a brand-new row
// to an arbitrary existing proposal or run.
func TestRegistryWriteInbox_IgnoresCallerSuppliedLinkageFields(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := sampleInboxArgs("1756600100.000900", "forged linkage")
	inbox := args["inbox"].(map[string]any)
	inbox["project"] = "some-other-project"
	inbox["proposal_id"] = 4242
	inbox["run_id"] = 777
	inbox["note"] = "pre-set by the caller"
	inbox["status"] = "routed"
	inbox["triage"] = "drop"

	result := registryWriteInbox(args)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	id := int64(decodeToolResult(t, result)["id"].(float64))

	s, err := getStore()
	if err != nil {
		t.Fatalf("getStore: %v", err)
	}
	it, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}

	if it.Project != nil {
		t.Errorf("caller-supplied project was persisted (%v); capture happens before routing, so it must be null", *it.Project)
	}
	if it.ProposalID != nil {
		t.Errorf("caller-supplied proposal_id was persisted (%d); a fresh row must not be pre-linked to an existing proposal", *it.ProposalID)
	}
	if it.RunID != nil {
		t.Errorf("caller-supplied run_id was persisted (%d); a fresh row must not be pre-linked to an existing run", *it.RunID)
	}
	if it.Note != "" {
		t.Errorf("caller-supplied note was persisted (%q); note is written by triage, not by capture", it.Note)
	}
	if it.Status != "new" {
		t.Errorf("caller-supplied status won: got %q, want %q", it.Status, "new")
	}
	if it.Triage != "" {
		t.Errorf("caller-supplied triage was persisted (%q); only triage may set a verdict", it.Triage)
	}
}

// --- egress secret-pattern check (DOTFILES-41) ---
//
// checkEgress is the enforceable half of the findings-egress control: every
// Stuart Slack post is passed through it before chat.postMessage. Contract:
//
//	checkEgress(text string) (clean bool, matched []string)
//
// `clean` is false when the body matches any credential pattern; `matched`
// names the patterns that fired and MUST NEVER contain the matched substring —
// echoing the secret into a tool result would recreate the leak in the
// transcript the refusal was supposed to prevent.

func TestCheckEgressCatchesCredentialShapes(t *testing.T) {
	cases := []struct {
		name        string
		text        string
		wantPattern string
	}{
		{
			name:        "aws access key id",
			text:        "here is the key AKIAIOSFODNN7EXAMPLE for the uploader",
			wantPattern: "aws_access_key",
		},
		{
			name:        "aws access key alone on a line",
			text:        "AKIA1234567890ABCDEF",
			wantPattern: "aws_access_key",
		},
		{
			name:        "slack bot token",
			text:        "token is " + "xoxb-1234567890123-1234567890123-" + "abcdefghijklmnopqrstuvwx",
			wantPattern: "slack_token",
		},
		{
			name:        "slack user token",
			text:        "xoxp-9876543210987-9876543210987-" + "zyxwvutsrqponmlkjihgfedc",
			wantPattern: "slack_token",
		},
		{
			name:        "legacy Slack token",
			text:        "xoxa-2-ABCDEFGHIJ-1234567890-" + "abcdefghijklmnopqrstuvwxyz012345",
			wantPattern: "slack_token",
		},
		{
			// xapp- is the app-level token family and is NOT matched by the
			// xox[abeprs]- pattern: a test labelling xoxa- "app-level" made a
			// real gap look covered.
			name:        "slack app-level token",
			text:        "SLACK_APP_TOKEN=" + "xapp-1-A012BCDEFGH-1234567890123-" + "abcdef0123456789abcdef0123456789",
			wantPattern: "slack_app_token",
		},
		{
			name:        "atlassian api token",
			text:        "JIRA_API_TOKEN=ATATT3xFfGF0T4Nn8mQrS7uVwXyZ1234567890abcdefGH=",
			wantPattern: "atlassian_api_token",
		},
		{
			name:        "anthropic api key",
			text:        "export ANTHROPIC_API_KEY=sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEf",
			wantPattern: "anthropic_api_key",
		},
		{
			name:        "generic sk- api key",
			text:        "the config still has sk-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789 in it",
			wantPattern: "generic_sk_api_key",
		},
		{
			// bearer_token requires the literal keyword; a token pasted on its
			// own was invisible before this family existed.
			name:        "bare jwt with no bearer keyword",
			text:        "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
			wantPattern: "jwt",
		},
		{
			name:        "google api key",
			text:        "maps key AIzaSyA1B2C3D4E5F6G7H8I9J0K1L2M3N4O5P6Q7R is in the client bundle",
			wantPattern: "google_api_key",
		},
		{
			name:        "api_key assignment with hex secret",
			text:        "api_key=4f2c1a9b8e7d6c5b4a39281706f5e4d3c2b1a098",
			wantPattern: "api_key_assignment",
		},
		{
			name:        "API-KEY header form with hex secret",
			text:        `curl -H 'X-API-Key: "0123456789abcdef0123456789abcdef01234567"' https://internal/api`,
			wantPattern: "api_key_assignment",
		},
		{
			name:        "slack refresh token",
			text:        "xoxr-1111111111111-2222222222222-" + "abcdefghijklmnopqrstuvwx",
			wantPattern: "slack_token",
		},
		{
			name:        "pem rsa private key header",
			text:        "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA...\n",
			wantPattern: "pem_private_key",
		},
		{
			name:        "pem openssh private key header",
			text:        "attached:\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk...\n",
			wantPattern: "pem_private_key",
		},
		{
			name:        "github personal access token",
			text:        "use ghp_1234567890abcdefghijklmnopqrstuvwxyz to clone",
			wantPattern: "github_pat",
		},
		{
			name:        "github oauth token",
			text:        "gho_abcdefghijklmnopqrstuvwxyz1234567890",
			wantPattern: "github_pat",
		},
		{
			name:        "generic bearer token",
			text:        "curl -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abcdefghijklmnop'",
			wantPattern: "bearer_token",
		},
		{
			name:        "dotenv style assignment",
			text:        "SLACK_BOT_TOKEN=" + "xoxb-1234567890123-1234567890123-" + "abcdefghijklmnopqrstuvwx",
			wantPattern: "slack_token",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clean, matched := checkEgress(tc.text)
			if clean {
				t.Fatalf("checkEgress reported clean for %s; a credential-shaped body must be refused, not posted", tc.name)
			}
			found := false
			for _, m := range matched {
				if m == tc.wantPattern {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("matched = %v, want it to include %q so the refusal names what fired", matched, tc.wantPattern)
			}
		})
	}
}

func TestCheckEgressAllowsNormalProse(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{
			name: "proposal summary",
			text: "Proposal 96: restrict every sweep relay's tool grant to its minimum, remove the sweep's shell, and gate every Stuart Slack post on a secret-pattern egress check. 7 steps, 4 new agent files. Approve or push back in this thread.",
		},
		{
			name: "unified diff excerpt",
			text: "--- a/claude/workflows/lead-workflow.js\n+++ b/claude/workflows/lead-workflow.js\n@@ -979,7 +979,7 @@\n-      agentType: 'general-purpose',\n+      agentType: 'stuart-slack-reader',\n",
		},
		{
			name: "repo file path",
			text: "See claude/mcp/server/registry.go and claude/agents/sweep-investigator.md for the enforcement points.",
		},
		{
			name: "slack ts",
			text: "Replied in thread 1756915234.482719 on channel C09ABCDEFGH.",
		},
		{
			name: "git sha",
			text: "Shipped as dcd8b4f, full sha 4f2c1a9b8e7d6c5b4a39281706f5e4d3c2b1a098.",
		},
		{
			name: "sentence containing the word token",
			text: "The Slack bot token is read from the environment by the plugin, so no token value ever appears in a proposal body.",
		},
		{
			name: "ticket prose with uppercase runs",
			text: "DOTFILES-41 closes the BLOCKER from DOTFILES-40; AKIA is the AWS key prefix we now screen for.",
		},
		{
			// The bare-JWT family keys off eyJ plus the two-dot structure; a
			// base64 blob with no dots must not fire.
			name: "base64 blob that is not a jwt",
			text: "attachment payload: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9aGVsbG8gd29ybGQgdGhpcyBpcyBub3QgYSB0b2tlbg",
		},
		{
			name: "long hex git object id with no api_key keyword",
			text: "merge-base is 0123456789abcdef0123456789abcdef01234567 on the feature branch.",
		},
		{
			name: "url with a query string",
			text: "Dashboard: https://grafana.example.com/d/abc123/api?orgId=1&from=now-6h&to=now&var-env=production",
		},
		{
			// `sk-` must not match through a word like risk-: this repo has a
			// risk-assumption-researcher agent, and a prefix-only sk- pattern
			// refused every proposal that named it.
			name: "prose containing risk- followed by a long hyphenated phrase",
			text: "The risk-assumption-and-mitigation-planning-researcher agent runs in the idea-validation fan-out.",
		},
		{
			name: "slack app id mentioned in prose",
			text: "The Stuart app is A012BCDEFGH; its xapp token lives in the environment, never in a proposal body.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clean, matched := checkEgress(tc.text)
			if !clean {
				t.Errorf("checkEgress refused legitimate %s (matched %v); a false positive silently blocks the sweep's output", tc.name, matched)
			}
		})
	}
}

func TestCheckEgressReportsWhichPatternMatched(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	body := "Investigation TL;DR: the uploader hardcodes " + secret + " in config."

	clean, matched := checkEgress(body)
	if clean {
		t.Fatalf("checkEgress reported clean for a body containing an AWS key shape")
	}
	if len(matched) == 0 {
		t.Fatalf("matched is empty; a refusal with no pattern name is not actionable")
	}
	if matched[0] != "aws_access_key" {
		t.Errorf("matched[0] = %q, want %q", matched[0], "aws_access_key")
	}
	for _, m := range matched {
		if strings.Contains(m, secret) {
			t.Errorf("matched entry %q echoes the secret; the result must name the pattern only, never the matched text", m)
		}
		if strings.Contains(m, "AKIA") {
			t.Errorf("matched entry %q leaks part of the matched substring", m)
		}
	}
}

// --- egress gate: screening the bytes that are actually posted (DOTFILES-41) ---
//
// The post path writes the body to a temp file and posts THAT file. Handing the
// gate a retyped copy of the body checks a string nobody sends: a transcription
// slip — or a deliberately mangled retype — passes the gate while a different
// set of bytes leaves the machine. So registryCheckEgress takes an optional
// `path` and screens the file's real bytes, and `path` wins over `text`
// whenever both arrive.

func TestRegistryCheckEgressPathScreensFileBytes(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "stuart-post.txt")
	if err := os.WriteFile(body, []byte("TL;DR: uploader hardcodes AKIAIOSFODNN7EXAMPLE in config.\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := registryCheckEgress(map[string]any{"path": body})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var resp struct {
		Clean   bool     `json:"clean"`
		Matched []string `json:"matched"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.Clean {
		t.Fatalf("clean=true for a file whose bytes carry an AWS key shape; the gate read something other than the file")
	}
	if len(resp.Matched) == 0 || resp.Matched[0] != "aws_access_key" {
		t.Errorf("matched = %v, want [aws_access_key]", resp.Matched)
	}
}

func TestRegistryCheckEgressPathCleanFile(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "clean.txt")
	if err := os.WriteFile(body, []byte("Proposal 96: restrict every sweep relay's tool grant. Approve or push back in this thread.\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := registryCheckEgress(map[string]any{"path": body})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"clean": true`) {
		t.Errorf("ordinary prose in a file was refused: %s", result.Content[0].Text)
	}
	// matched must serialise as [] rather than null: a caller reading matched.length
	// on null crashes at exactly the moment it is deciding whether to post.
	if !strings.Contains(result.Content[0].Text, `"matched": []`) {
		t.Errorf("matched did not normalise to an empty array: %s", result.Content[0].Text)
	}
}

func TestRegistryCheckEgressMissingPathErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-body.txt")

	result := registryCheckEgress(map[string]any{"path": missing})
	if !result.IsError {
		t.Fatalf("a path that cannot be read returned a non-error result (%s); an unreadable body must fail loudly, never come back clean", result.Content[0].Text)
	}
	if strings.Contains(result.Content[0].Text, `"clean":true`) || strings.Contains(result.Content[0].Text, `"clean": true`) {
		t.Errorf("unreadable path reported clean: %s", result.Content[0].Text)
	}
}

func TestRegistryCheckEgressUnreadablePathErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 is still readable")
	}
	dir := t.TempDir()
	body := filepath.Join(dir, "locked.txt")
	if err := os.WriteFile(body, []byte("AKIAIOSFODNN7EXAMPLE\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(body, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	defer os.Chmod(body, 0o600)

	result := registryCheckEgress(map[string]any{"path": body})
	if !result.IsError {
		t.Fatalf("an unreadable file returned a non-error result: %s", result.Content[0].Text)
	}
}

func TestRegistryCheckEgressPathWinsOverText(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "posted.txt")
	if err := os.WriteFile(body, []byte("key AKIAIOSFODNN7EXAMPLE is in the config\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The classic defeat: a clean retyped summary alongside a dirty real body.
	result := registryCheckEgress(map[string]any{
		"path": body,
		"text": "TL;DR: the uploader hardcodes a credential in config.",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"clean": false`) {
		t.Fatalf("text won over path: the gate screened the retyped summary instead of the bytes being posted (%s)", result.Content[0].Text)
	}

	// And the mirror: dirty retype, clean file. The file is what gets posted, so
	// this must pass — otherwise `path` is not authoritative, it is merely OR-ed in.
	clean := filepath.Join(dir, "clean.txt")
	if err := os.WriteFile(clean, []byte("nothing sensitive here\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result = registryCheckEgress(map[string]any{
		"path": clean,
		"text": "AKIAIOSFODNN7EXAMPLE",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"clean": true`) {
		t.Errorf("path was not authoritative; text still contributed: %s", result.Content[0].Text)
	}
}

func TestRegistryCheckEgressTextStillWorks(t *testing.T) {
	result := registryCheckEgress(map[string]any{"text": "ghp_1234567890abcdefghijklmnopqrstuvwxyz"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"github_pat"`) {
		t.Errorf("text-only caller lost its screening: %s", result.Content[0].Text)
	}
}

func TestRegistryCheckEgressRejectsBadArgs(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"neither path nor text", map[string]any{}},
		{"non-string text", map[string]any{"text": 42}},
		{"non-string path", map[string]any{"path": []any{"a"}}},
		{"empty path", map[string]any{"path": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := registryCheckEgress(tc.args)
			if !result.IsError {
				t.Errorf("%s returned a non-error result: %s", tc.name, result.Content[0].Text)
			}
		})
	}
}

// The gate is only a control if the tool is reachable: an unwired dispatch case
// or a missing tools/list entry means every caller gets "unknown tool" and the
// post path's only screen silently becomes a no-op.
func TestCheckEgressToolRegisteredInDispatchAndSchemas(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range allTools() {
		names[tool.Name] = true
	}
	if !names["registry_check_egress"] {
		t.Error("registry_check_egress missing from tools/list")
	}
	got := dispatch("registry_check_egress", map[string]any{"text": "AKIAIOSFODNN7EXAMPLE"})
	if strings.Contains(got.Content[0].Text, "unknown tool") {
		t.Fatal("registry_check_egress not wired into dispatch")
	}
	if got.IsError {
		t.Fatalf("dispatch returned an error: %s", got.Content[0].Text)
	}
	var body struct {
		Clean   bool     `json:"clean"`
		Matched []string `json:"matched"`
	}
	if err := json.Unmarshal([]byte(got.Content[0].Text), &body); err != nil {
		t.Fatalf("dispatch result is not the documented shape: %v", err)
	}
	if body.Clean || len(body.Matched) == 0 {
		t.Errorf("dispatch of a credential-shaped body returned clean=%v matched=%v", body.Clean, body.Matched)
	}
}

// A clean body must serialize `matched` as [] rather than null: a caller that
// reads matched.length on null crashes on the happy path.
func TestRegistryCheckEgressCleanTextEmptySliceShape(t *testing.T) {
	result := registryCheckEgress(map[string]any{"text": "Proposal 97: three steps, no credentials here."})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"matched": []`) {
		t.Errorf("clean result = %s, want matched serialized as []", result.Content[0].Text)
	}
}

// TestSetPlanPhase codifies the contract for the not-yet-implemented
// registry_set_plan_phase tool (DOTFILES-47): read-merge-write onto the
// plan blob (plan_steps must survive untouched — a full-object clobber is
// the exact bug class fixed in ship.md this session), a valid-phase enum,
// and empty string clearing a previously-set override.
func TestSetPlanPhase(t *testing.T) {
	_, cleanup := setupTestDataDir(t)
	defer cleanup()

	seedPlan := func(t *testing.T, ticket string) {
		t.Helper()
		plan := map[string]any{
			"ticket":  ticket,
			"summary": "test plan",
			"plan_steps": []any{
				map[string]any{"title": "step one", "status": "done"},
				map[string]any{"title": "step two", "status": "in_progress"},
			},
		}
		wrote := registryWritePlan(map[string]any{"name": "myproject", "ticket": ticket, "data": plan})
		if wrote.IsError {
			t.Fatalf("seed WritePlan: %s", wrote.Content[0].Text)
		}
	}

	t.Run("sets phase_override via read-merge-write, plan_steps untouched", func(t *testing.T) {
		seedPlan(t, "TEST-PHASE-1")

		result := registrySetPlanPhase(map[string]any{
			"name":   "myproject",
			"ticket": "TEST-PHASE-1",
			"phase":  "pr_ready",
		})
		if result.IsError {
			t.Fatalf("unexpected error: %s", result.Content[0].Text)
		}

		got := registryGetPlan(map[string]any{"name": "myproject", "ticket": "TEST-PHASE-1"})
		if got.IsError {
			t.Fatalf("GetPlan: %s", got.Content[0].Text)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(got.Content[0].Text), &data); err != nil {
			t.Fatalf("unmarshal plan: %v", err)
		}
		if data["phase_override"] != "pr_ready" {
			t.Errorf("phase_override = %v, want pr_ready", data["phase_override"])
		}
		steps, ok := data["plan_steps"].([]any)
		if !ok || len(steps) != 2 {
			t.Fatalf("plan_steps clobbered: %v", data["plan_steps"])
		}
		s0 := steps[0].(map[string]any)
		s1 := steps[1].(map[string]any)
		if s0["status"] != "done" {
			t.Errorf("step 0 status = %v, want done (clobbered by phase set)", s0["status"])
		}
		if s1["status"] != "in_progress" {
			t.Errorf("step 1 status = %v, want in_progress (clobbered by phase set)", s1["status"])
		}
		if s0["title"] != "step one" || s1["title"] != "step two" {
			t.Errorf("step titles clobbered: %v / %v", s0["title"], s1["title"])
		}
	})

	t.Run("empty string clears a previously-set override", func(t *testing.T) {
		seedPlan(t, "TEST-PHASE-2")

		set := registrySetPlanPhase(map[string]any{
			"name":   "myproject",
			"ticket": "TEST-PHASE-2",
			"phase":  "done",
		})
		if set.IsError {
			t.Fatalf("unexpected error setting override: %s", set.Content[0].Text)
		}

		cleared := registrySetPlanPhase(map[string]any{
			"name":   "myproject",
			"ticket": "TEST-PHASE-2",
			"phase":  "",
		})
		if cleared.IsError {
			t.Fatalf("unexpected error clearing override: %s", cleared.Content[0].Text)
		}

		got := registryGetPlan(map[string]any{"name": "myproject", "ticket": "TEST-PHASE-2"})
		if got.IsError {
			t.Fatalf("GetPlan: %s", got.Content[0].Text)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(got.Content[0].Text), &data); err != nil {
			t.Fatalf("unmarshal plan: %v", err)
		}
		if ov, present := data["phase_override"]; present && ov != "" {
			t.Errorf("phase_override = %v (present=%v), want absent or empty after clearing", ov, present)
		}
	})

	t.Run("invalid phase string is rejected", func(t *testing.T) {
		seedPlan(t, "TEST-PHASE-3")

		result := registrySetPlanPhase(map[string]any{
			"name":   "myproject",
			"ticket": "TEST-PHASE-3",
			"phase":  "not_a_real_phase",
		})
		if !result.IsError {
			t.Fatalf("want toolErr for invalid phase, got success: %v", result.Content[0].Text)
		}

		got := registryGetPlan(map[string]any{"name": "myproject", "ticket": "TEST-PHASE-3"})
		var data map[string]any
		json.Unmarshal([]byte(got.Content[0].Text), &data)
		if ov, present := data["phase_override"]; present && ov != "" {
			t.Errorf("invalid phase must not be persisted, got phase_override=%v", ov)
		}
	})

	t.Run("unknown ticket returns an error, never a silent no-op success", func(t *testing.T) {
		result := registrySetPlanPhase(map[string]any{
			"name":   "myproject",
			"ticket": "NO-SUCH-TICKET",
			"phase":  "pr_ready",
		})
		if !result.IsError {
			t.Fatalf("want toolErr for unknown ticket, got success: %v", result.Content[0].Text)
		}
	})

	t.Run("does not leak internal id/created_at fields into the persisted plan doc", func(t *testing.T) {
		seedPlan(t, "TEST-PHASE-4")

		result := registrySetPlanPhase(map[string]any{
			"name":   "myproject",
			"ticket": "TEST-PHASE-4",
			"phase":  "blocked",
		})
		if result.IsError {
			t.Fatalf("unexpected error: %s", result.Content[0].Text)
		}

		s, err := getStore()
		if err != nil {
			t.Fatalf("getStore: %v", err)
		}
		var raw string
		if err := s.db.QueryRow(
			`SELECT data FROM plans WHERE project = ? AND ticket = ?`, "myproject", "TEST-PHASE-4",
		).Scan(&raw); err != nil {
			t.Fatalf("query raw data column: %v", err)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatalf("unmarshal raw data: %v", err)
		}
		if _, present := data["id"]; present {
			t.Errorf("raw data column must not gain an 'id' field, got: %v", data)
		}
		if _, present := data["created_at"]; present {
			t.Errorf("raw data column must not gain a 'created_at' field, got: %v", data)
		}
	})
}
