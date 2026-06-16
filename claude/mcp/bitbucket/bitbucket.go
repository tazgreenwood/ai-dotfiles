package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const bbBase = "https://api.bitbucket.org/2.0"

func bbWorkspace() string {
	if ws := os.Getenv("BITBUCKET_WORKSPACE"); ws != "" {
		return ws
	}
	return "clearlinkit"
}

func bbAuth() (string, error) {
	email := os.Getenv("BITBUCKET_EMAIL")
	token := os.Getenv("BITBUCKET_TOKEN")
	if email == "" || token == "" {
		return "", fmt.Errorf("BITBUCKET_EMAIL and BITBUCKET_TOKEN env vars required")
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token)), nil
}

func bbGet(endpoint string) (map[string]any, error) {
	auth, err := bbAuth()
	if err != nil {
		return nil, err
	}
	u := endpoint
	if !strings.HasPrefix(endpoint, "http") {
		u = bbBase + endpoint
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("bitbucket %d: %s", resp.StatusCode, string(body))
	}
	var m map[string]any
	return m, json.Unmarshal(body, &m)
}

func bbPost(endpoint string, payload any) (map[string]any, error) {
	auth, err := bbAuth()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", bbBase+endpoint, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("bitbucket %d: %s", resp.StatusCode, string(body))
	}
	var m map[string]any
	return m, json.Unmarshal(body, &m)
}

func bbGetText(endpoint string) (string, error) {
	auth, err := bbAuth()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("GET", bbBase+endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("bitbucket %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

func bbPaginate(endpoint string, max int) ([]any, error) {
	var results []any
	u := bbBase + endpoint
	for u != "" && len(results) < max {
		data, err := bbGet(u)
		if err != nil {
			return results, err
		}
		if values, ok := data["values"].([]any); ok {
			results = append(results, values...)
		}
		if next, ok := data["next"].(string); ok {
			u = next
		} else {
			u = ""
		}
	}
	if len(results) > max {
		results = results[:max]
	}
	return results, nil
}

func repoPath(repo string) string {
	return fmt.Sprintf("/repositories/%s/%s", bbWorkspace(), repo)
}

func str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func strOr(args map[string]any, key, def string) string {
	if v := str(args, key); v != "" {
		return v
	}
	return def
}

func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func intArgOr(args map[string]any, key string, def int) int {
	if v := intArg(args, key); v > 0 {
		return v
	}
	return def
}

// ── Tool handlers ──────────────────────────────────────────────────────────────

func bbListPRs(args map[string]any) ToolResult {
	repo := str(args, "repo")
	state := strOr(args, "state", "OPEN")
	prs, err := bbPaginate(repoPath(repo)+"/pullrequests?state="+state, 50)
	if err != nil {
		return toolErr(err.Error())
	}
	var result []map[string]any
	for _, p := range prs {
		pr, _ := p.(map[string]any)
		result = append(result, map[string]any{
			"id":          pr["id"],
			"title":       pr["title"],
			"author":      nestedStr(pr, "author", "display_name"),
			"source":      nestedStr(pr, "source", "branch", "name"),
			"destination": nestedStr(pr, "destination", "branch", "name"),
			"state":       pr["state"],
			"created_on":  pr["created_on"],
			"url":         nestedStr(pr, "links", "html", "href"),
		})
	}
	return toolOK(map[string]any{"prs": result})
}

func bbGetPR(args map[string]any) ToolResult {
	repo := str(args, "repo")
	id := intArg(args, "pr_id")
	data, err := bbGet(fmt.Sprintf("%s/pullrequests/%d", repoPath(repo), id))
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{
		"id":           data["id"],
		"title":        data["title"],
		"description":  data["description"],
		"author":       nestedStr(data, "author", "display_name"),
		"source":       nestedStr(data, "source", "branch", "name"),
		"destination":  nestedStr(data, "destination", "branch", "name"),
		"state":        data["state"],
		"created_on":   data["created_on"],
		"updated_on":   data["updated_on"],
		"reviewers":    extractNames(data, "reviewers"),
		"participants": extractParticipants(data),
		"url":          nestedStr(data, "links", "html", "href"),
	})
}

func bbCreatePR(args map[string]any) ToolResult {
	repo := str(args, "repo")
	reviewers := []map[string]any{}
	if rv, ok := args["reviewers"].([]any); ok {
		for _, r := range rv {
			if s, ok := r.(string); ok {
				if len(s) == 38 && strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
					reviewers = append(reviewers, map[string]any{"uuid": s})
				} else {
					reviewers = append(reviewers, map[string]any{"username": s})
				}
			}
		}
	}
	payload := map[string]any{
		"title":       str(args, "title"),
		"description": str(args, "description"),
		"source":      map[string]any{"branch": map[string]any{"name": str(args, "source")}},
		"destination": map[string]any{"branch": map[string]any{"name": str(args, "destination")}},
		"reviewers":   reviewers,
	}
	data, err := bbPost(repoPath(repo)+"/pullrequests", payload)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{
		"id":  data["id"],
		"url": nestedStr(data, "links", "html", "href"),
	})
}

func bbGetCommits(args map[string]any) ToolResult {
	repo := str(args, "repo")
	limit := intArgOr(args, "limit", 20)
	branch := str(args, "branch")
	endpoint := repoPath(repo) + "/commits"
	if branch != "" {
		endpoint += "?include=" + url.QueryEscape(branch)
	}
	commits, err := bbPaginate(endpoint, limit)
	if err != nil {
		return toolErr(err.Error())
	}
	var result []map[string]any
	for _, c := range commits {
		commit, _ := c.(map[string]any)
		msg := ""
		if m, ok := commit["message"].(string); ok {
			if idx := strings.Index(m, "\n"); idx >= 0 {
				msg = m[:idx]
			} else {
				msg = m
			}
		}
		result = append(result, map[string]any{
			"hash":    truncHash(commit),
			"message": msg,
			"author":  commitAuthor(commit),
			"date":    commit["date"],
		})
	}
	return toolOK(map[string]any{"commits": result})
}

func bbAddPRComment(args map[string]any) ToolResult {
	repo := str(args, "repo")
	id := intArg(args, "pr_id")
	payload := map[string]any{"content": map[string]any{"raw": str(args, "comment")}}
	_, err := bbPost(fmt.Sprintf("%s/pullrequests/%d/comments", repoPath(repo), id), payload)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true})
}

func bbGetRepo(args map[string]any) ToolResult {
	data, err := bbGet(repoPath(str(args, "repo")))
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{
		"name":        data["name"],
		"full_name":   data["full_name"],
		"description": data["description"],
		"mainbranch":  nestedStr(data, "mainbranch", "name"),
		"size":        data["size"],
		"updated_on":  data["updated_on"],
		"url":         nestedStr(data, "links", "html", "href"),
	})
}

func bbListBranches(args map[string]any) ToolResult {
	limit := intArgOr(args, "limit", 30)
	branches, err := bbPaginate(repoPath(str(args, "repo"))+"/refs/branches", limit)
	if err != nil {
		return toolErr(err.Error())
	}
	var result []map[string]any
	for _, b := range branches {
		branch, _ := b.(map[string]any)
		result = append(result, map[string]any{
			"name":   branch["name"],
			"target": truncHash(nestedMap(branch, "target")),
			"date":   nestedStr(branch, "target", "date"),
		})
	}
	return toolOK(map[string]any{"branches": result})
}

func bbGetDiff(args map[string]any) ToolResult {
	repo := str(args, "repo")
	id := intArg(args, "pr_id")
	diff, err := bbGetText(fmt.Sprintf("%s/pullrequests/%d/diff", repoPath(repo), id))
	if err != nil {
		return toolErr(err.Error())
	}
	if len(diff) > 50000 {
		diff = diff[:50000] + "\n... (truncated)"
	}
	return toolOK(map[string]any{"diff": diff})
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func nestedStr(m map[string]any, keys ...string) string {
	cur := any(m)
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

func nestedMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func extractNames(m map[string]any, key string) []string {
	items, _ := m[key].([]any)
	var names []string
	for _, item := range items {
		if im, ok := item.(map[string]any); ok {
			if name := nestedStr(im, "display_name"); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func extractParticipants(m map[string]any) []map[string]any {
	items, _ := m["participants"].([]any)
	var result []map[string]any
	for _, item := range items {
		if p, ok := item.(map[string]any); ok {
			result = append(result, map[string]any{
				"user":     nestedStr(p, "user", "display_name"),
				"approved": p["approved"],
				"state":    p["state"],
			})
		}
	}
	return result
}

func truncHash(m map[string]any) string {
	if m == nil {
		return ""
	}
	h, _ := m["hash"].(string)
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func commitAuthor(m map[string]any) string {
	if name := nestedStr(m, "author", "user", "display_name"); name != "" {
		return name
	}
	return nestedStr(m, "author", "raw")
}

// ── Tool schemas ───────────────────────────────────────────────────────────────

func bitbucketTools() []Tool {
	return []Tool{
		{
			Name:        "bitbucket_list_prs",
			Description: "List pull requests for a repo",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":  map[string]any{"type": "string", "description": "Repo slug e.g. emily"},
					"state": map[string]any{"type": "string", "enum": []string{"OPEN", "MERGED", "DECLINED"}, "description": "Default: OPEN"},
				},
				"required": []string{"repo"},
			},
		},
		{
			Name:        "bitbucket_get_pr",
			Description: "Get a pull request by ID",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":  map[string]any{"type": "string"},
					"pr_id": map[string]any{"type": "number"},
				},
				"required": []string{"repo", "pr_id"},
			},
		},
		{
			Name:        "bitbucket_create_pr",
			Description: "Create a pull request",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":        map[string]any{"type": "string"},
					"title":       map[string]any{"type": "string"},
					"description": map[string]any{"type": "string"},
					"source":      map[string]any{"type": "string", "description": "Source branch"},
					"destination": map[string]any{"type": "string", "description": "Target branch"},
					"reviewers":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"repo", "title", "source", "destination"},
			},
		},
		{
			Name:        "bitbucket_get_commits",
			Description: "Get recent commits for a repo branch",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":   map[string]any{"type": "string"},
					"branch": map[string]any{"type": "string"},
					"limit":  map[string]any{"type": "number", "description": "Default 20"},
				},
				"required": []string{"repo"},
			},
		},
		{
			Name:        "bitbucket_add_pr_comment",
			Description: "Add a comment to a pull request",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":    map[string]any{"type": "string"},
					"pr_id":   map[string]any{"type": "number"},
					"comment": map[string]any{"type": "string"},
				},
				"required": []string{"repo", "pr_id", "comment"},
			},
		},
		{
			Name:        "bitbucket_get_repo",
			Description: "Get repository info",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"repo": map[string]any{"type": "string"}},
				"required":   []string{"repo"},
			},
		},
		{
			Name:        "bitbucket_list_branches",
			Description: "List branches for a repo",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":  map[string]any{"type": "string"},
					"limit": map[string]any{"type": "number", "description": "Default 30"},
				},
				"required": []string{"repo"},
			},
		},
		{
			Name:        "bitbucket_get_diff",
			Description: "Get the diff for a pull request",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":  map[string]any{"type": "string"},
					"pr_id": map[string]any{"type": "number"},
				},
				"required": []string{"repo", "pr_id"},
			},
		},
	}
}
