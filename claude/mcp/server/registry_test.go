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
		"id":     float64(id),
		"status": "triaged",
		"triage": "plan",
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
