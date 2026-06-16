package main

import (
	"testing"
)

func TestDispatch_UnknownToolReturnsError(t *testing.T) {
	result := dispatch("unknown_tool", map[string]any{})
	if !result.IsError {
		t.Error("want IsError=true for unknown tool, got false")
	}
}

func TestDispatch_KnownToolsRouted(t *testing.T) {
	knownTools := []string{
		"bitbucket_list_prs",
		"bitbucket_get_pr",
		"bitbucket_create_pr",
		"bitbucket_get_commits",
		"bitbucket_add_pr_comment",
		"bitbucket_get_repo",
		"bitbucket_list_branches",
		"bitbucket_get_diff",
	}
	for _, tool := range knownTools {
		result := dispatch(tool, map[string]any{})
		// With no env vars set, auth will fail — but the tool was routed (not "unknown tool")
		if result.Content[0].Text == `{"error":"unknown tool: `+tool+`"}` {
			t.Errorf("tool %q not routed — got unknown tool error", tool)
		}
	}
}

func TestBbAuth_ReturnsErrorWhenEnvMissing(t *testing.T) {
	t.Setenv("BITBUCKET_EMAIL", "")
	t.Setenv("BITBUCKET_TOKEN", "")

	_, err := bbAuth()
	if err == nil {
		t.Error("want error when BITBUCKET_EMAIL and BITBUCKET_TOKEN unset, got nil")
	}
}

func TestAllTools_ReturnsBitbucketTools(t *testing.T) {
	tools := allTools()
	if len(tools) == 0 {
		t.Fatal("want at least 1 tool, got 0")
	}
	for _, tool := range tools {
		if tool.Name == "" {
			t.Error("tool with empty name found")
		}
	}
}
