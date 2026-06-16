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

func TestRegistryReportIssue_DefaultsProjectToRegistry(t *testing.T) {
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

	issuesFile := filepath.Join(dir, "registry", "issues.json")
	if _, err := os.Stat(issuesFile); err != nil {
		t.Fatalf("want issues.json at registry/issues.json, not found: %v", err)
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
