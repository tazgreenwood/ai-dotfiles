package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
	dir, cleanup := setupTestDataDir(t)
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

	issuesFile := filepath.Join(dir, "myproject", "issues.json")
	b, err := os.ReadFile(issuesFile)
	if err != nil {
		t.Fatalf("issues.json not created: %v", err)
	}
	var issues []map[string]any
	if err := json.Unmarshal(b, &issues); err != nil {
		t.Fatalf("invalid json: %v", err)
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

	issuesFile := filepath.Join(dataDir(), "myproject", "issues.json")
	b, _ := os.ReadFile(issuesFile)
	var issues []map[string]any
	json.Unmarshal(b, &issues)

	if _, ok := issues[0]["_recorded_at"]; !ok {
		t.Error("want _recorded_at field, not present")
	}
}

func TestRegistryReportIssue_DefaultsProjectToPrivateDotfiles(t *testing.T) {
	dir, cleanup := setupTestDataDir(t)
	defer cleanup()

	args := map[string]any{
		"tool":  "registry_set",
		"error": "write failed",
	}
	result := registryReportIssue(args)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	issuesFile := filepath.Join(dir, "private-dotfiles", "issues.json")
	if _, err := os.Stat(issuesFile); err != nil {
		t.Fatalf("want issues.json at private-dotfiles/issues.json, not found: %v", err)
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

	issuesFile := filepath.Join(dataDir(), "myproject", "issues.json")
	b, _ := os.ReadFile(issuesFile)
	var issues []map[string]any
	json.Unmarshal(b, &issues)

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

	issuesFile := filepath.Join(dataDir(), "myproject", "issues.json")
	b, _ := os.ReadFile(issuesFile)
	var issues []map[string]any
	json.Unmarshal(b, &issues)

	if issues[0]["severity"] != "warning" {
		t.Errorf("want severity=warning, got %v", issues[0]["severity"])
	}
}

func writeProjectJSON(t *testing.T, dir, name string, data map[string]any) {
	t.Helper()
	projectPath := filepath.Join(dir, name)
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	b, _ := json.Marshal(data)
	if err := os.WriteFile(filepath.Join(projectPath, "project.json"), b, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestGetResources_ReturnsAll(t *testing.T) {
	dir, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectJSON(t, dir, "myproject", map[string]any{
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
	dir, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectJSON(t, dir, "myproject", map[string]any{
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

func TestGetResources_EmptyWhenNone(t *testing.T) {
	dir, cleanup := setupTestDataDir(t)
	defer cleanup()

	writeProjectJSON(t, dir, "myproject", map[string]any{
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
