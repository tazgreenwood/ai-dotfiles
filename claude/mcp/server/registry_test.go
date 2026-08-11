package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
