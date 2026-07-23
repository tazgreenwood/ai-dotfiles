package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ── Data dir ───────────────────────────────────────────────────────────────────

func dataDir() string {
	if d := os.Getenv("REGISTRY_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "registry", "data")
}

// ── SQLite-backed store (DOTFILES-23) ───────────────────────────────────────────
//
// Lazily-initialized, cached per dataDir() path so tests that override
// REGISTRY_DATA_DIR per-run each get an isolated DB, while production usage
// (stable dataDir() across the process lifetime) reuses a single connection.

var (
	storesMu sync.Mutex
	stores   = map[string]*store{}
)

func getStore() (*store, error) {
	path := filepath.Join(dataDir(), "registry.db")
	storesMu.Lock()
	defer storesMu.Unlock()
	if s, ok := stores[path]; ok {
		return s, nil
	}
	s, err := newStore(path)
	if err != nil {
		return nil, err
	}
	stores[path] = s
	return s, nil
}

func dotGet(m map[string]any, path string) any {
	parts := strings.Split(path, ".")
	cur := any(m)
	for _, k := range parts {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

func dotSet(m map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	cur := m
	for _, k := range parts[:len(parts)-1] {
		if cur[k] == nil {
			cur[k] = map[string]any{}
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			cur[k] = map[string]any{}
			next = cur[k].(map[string]any)
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = value
}

func str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// ── Tool handlers ──────────────────────────────────────────────────────────────

func registryGetProject(args map[string]any) ToolResult {
	name := str(args, "name")
	if name == "" {
		return toolErr("name required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	data, err := s.GetProject(name)
	if err != nil {
		return toolErr(fmt.Sprintf("project '%s' not found in registry", name))
	}
	if p := str(args, "path"); p != "" {
		return toolOK(map[string]any{"value": dotGet(data, p)})
	}
	return toolOK(data)
}

func registrySet(args map[string]any) ToolResult {
	name := str(args, "name")
	path := str(args, "path")
	value := args["value"]
	if name == "" || path == "" {
		return toolErr("name and path required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	data, err := s.GetProject(name)
	if err != nil {
		data = map[string]any{}
	}
	dotSet(data, path, value)
	if err := s.SetProject(name, data); err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "path": path, "value": value})
}

func registryInitProject(args map[string]any) ToolResult {
	name := str(args, "name")
	if name == "" {
		return toolErr("name required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	if _, err := s.GetProject(name); err == nil {
		return toolErr(fmt.Sprintf("project '%s' already exists — use registry_set to update fields", name))
	}
	ws := str(args, "workspace")
	if ws == "" {
		ws = os.Getenv("BITBUCKET_WORKSPACE")
	}
	if ws == "" {
		ws = "clearlinkit"
	}
	data := map[string]any{
		"name": name,
		"repo": map[string]any{
			"workspace": ws,
			"localPath": str(args, "localPath"),
			"base":      strOr(args, "base", "production"),
			"prTarget":  strOr(args, "prTarget", "staging"),
		},
		"deploy": map[string]any{
			"profile":  strOr(args, "profile", "martech"),
			"cluster":  strOr(args, "cluster", "general-production"),
			"logGroup": strOr(args, "logGroup", name+"-production"),
			"env":      strOr(args, "env", "production"),
		},
	}
	if err := s.SetProject(name, data); err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "created": filepath.Join(dataDir(), "registry.db"), "data": data})
}

func strOr(args map[string]any, key, def string) string {
	if v := str(args, key); v != "" {
		return v
	}
	return def
}

func registryListProjects(args map[string]any) ToolResult {
	s, err := getStore()
	if err != nil {
		return toolOK(map[string]any{"projects": []string{}})
	}
	projects, err := s.ListProjects()
	if err != nil {
		return toolOK(map[string]any{"projects": []string{}})
	}
	return toolOK(map[string]any{"projects": projects})
}

func registryListPlans(args map[string]any) ToolResult {
	name := str(args, "name")
	if name == "" {
		return toolErr("name required")
	}
	s, err := getStore()
	if err != nil {
		return toolOK(map[string]any{"plans": []any{}})
	}
	rows, err := s.ListPlans(name)
	if err != nil {
		return toolOK(map[string]any{"plans": []any{}})
	}
	var plans []map[string]any
	for _, data := range rows {
		ticket, _ := data["ticket"].(string)
		status := "active"
		if steps, ok := data["plan_steps"].([]any); ok {
			allDone := true
			for _, s := range steps {
				if step, ok := s.(map[string]any); ok {
					if step["status"] != "done" {
						allDone = false
						break
					}
				}
			}
			if allDone {
				status = "shipped"
			}
		}
		plans = append(plans, map[string]any{
			"ticket":  ticket,
			"summary": data["summary"],
			"status":  status,
		})
	}
	return toolOK(map[string]any{"plans": plans})
}

func registryGetPlan(args map[string]any) ToolResult {
	name := str(args, "name")
	ticket := str(args, "ticket")
	if name == "" || ticket == "" {
		return toolErr("name and ticket required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	data, err := s.GetPlan(name, ticket)
	if err != nil {
		return toolErr(fmt.Sprintf("plan '%s' not found for project '%s'", ticket, name))
	}
	return toolOK(data)
}

func registryWritePlan(args map[string]any) ToolResult {
	name := str(args, "name")
	ticket := str(args, "ticket")
	data, ok := args["data"].(map[string]any)
	if name == "" || ticket == "" || !ok {
		return toolErr("name, ticket, and data required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	if err := s.WritePlan(name, ticket, data); err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "file": filepath.Join(dataDir(), "registry.db")})
}

func registryWriteAudit(args map[string]any) ToolResult {
	name := str(args, "name")
	entry, ok := args["entry"].(map[string]any)
	if name == "" || !ok {
		return toolErr("name and entry required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	entry["_recorded_at"] = time.Now().UTC().Format(time.RFC3339)
	total, err := s.WriteAudit(name, entry)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "total_entries": total})
}

func registryWriteDeployCheck(args map[string]any) ToolResult {
	name := str(args, "name")
	entry, ok := args["entry"].(map[string]any)
	if name == "" || !ok {
		return toolErr("name and entry required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	entry["_recorded_at"] = time.Now().UTC().Format(time.RFC3339)
	total, err := s.WriteDeployCheck(name, entry)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "total_entries": total})
}

func registryReportIssue(args map[string]any) ToolResult {
	project := str(args, "project")
	if project == "" {
		project = "private-dotfiles"
	}
	tool := str(args, "tool")
	errMsg := str(args, "error")
	if tool == "" || errMsg == "" {
		return toolErr("tool and error required")
	}
	severity := str(args, "severity")
	if severity == "" {
		severity = "error"
	}
	entry := map[string]any{
		"tool":         tool,
		"error":        errMsg,
		"severity":     severity,
		"_recorded_at": time.Now().UTC().Format(time.RFC3339),
	}
	if ctx := str(args, "context"); ctx != "" {
		entry["context"] = ctx
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	total, err := s.WriteIssue(project, entry)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "total_issues": total})
}

func registryUpdateStep(args map[string]any) ToolResult {
	name := str(args, "name")
	ticket := str(args, "ticket")
	status := str(args, "status")
	idxRaw, ok := args["step_index"]
	if name == "" || ticket == "" || status == "" || !ok {
		return toolErr("name, ticket, step_index, and status required")
	}
	var idx int
	switch v := idxRaw.(type) {
	case float64:
		idx = int(v)
	case int:
		idx = v
	default:
		return toolErr("step_index must be a number")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	if _, err := s.GetPlan(name, ticket); err != nil {
		return toolErr(fmt.Sprintf("plan '%s' not found for project '%s'", ticket, name))
	}
	if err := s.UpdateStep(name, ticket, idx, status); err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "step_index": idx, "status": status})
}

func registryGetResources(args map[string]any) ToolResult {
	name := str(args, "name")
	if name == "" {
		return toolErr("name required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	data, err := s.GetProject(name)
	if err != nil {
		return toolErr(fmt.Sprintf("project '%s' not found in registry", name))
	}
	resources, _ := data["resources"].(map[string]any)
	if resources == nil {
		resources = map[string]any{}
	}
	if category := str(args, "category"); category != "" {
		if v, ok := resources[category]; ok {
			resources = map[string]any{category: v}
		} else {
			resources = map[string]any{}
		}
	}
	return toolOK(map[string]any{"resources": resources})
}

func registryGetAudit(args map[string]any) ToolResult {
	name := str(args, "name")
	if name == "" {
		return toolErr("name required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	entries, total, err := s.GetAudit(name, str(args, "since"), str(args, "until"))
	if err != nil {
		return toolErr(err.Error())
	}
	if entries == nil {
		entries = []map[string]any{}
	}
	return toolOK(map[string]any{"entries": entries, "total": total})
}

func registryGetDeployChecks(args map[string]any) ToolResult {
	name := str(args, "name")
	if name == "" {
		return toolErr("name required")
	}
	s, err := getStore()
	if err != nil {
		return toolErr(err.Error())
	}
	entries, total, err := s.GetDeployChecks(name, str(args, "since"), str(args, "until"))
	if err != nil {
		return toolErr(err.Error())
	}
	if entries == nil {
		entries = []map[string]any{}
	}
	return toolOK(map[string]any{"entries": entries, "total": total})
}

func allTools() []Tool {
	return registryTools()
}

// ── Tool schemas ───────────────────────────────────────────────────────────────

func registryTools() []Tool {
	return []Tool{
		{
			Name:        "registry_get_project",
			Description: "Get project metadata (deploy config, repo config, etc)",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Project name e.g. emily"},
					"path": map[string]any{"type": "string", "description": "Optional dot-path e.g. deploy.cluster"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "registry_set",
			Description: "Set a value in project metadata using dot-path",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"path":  map[string]any{"type": "string", "description": "Dot-path e.g. deploy.lastDeployAt"},
					"value": map[string]any{"description": "Value to set (any JSON type)"},
				},
				"required": []string{"name", "path", "value"},
			},
		},
		{
			Name:        "registry_init_project",
			Description: "Create a new project entry in the registry",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":      map[string]any{"type": "string"},
					"workspace": map[string]any{"type": "string"},
					"localPath": map[string]any{"type": "string"},
					"base":      map[string]any{"type": "string", "description": "Base branch e.g. production"},
					"prTarget":  map[string]any{"type": "string", "description": "PR target branch e.g. staging"},
					"profile":   map[string]any{"type": "string", "description": "AWS profile"},
					"cluster":   map[string]any{"type": "string", "description": "ECS cluster"},
					"logGroup":  map[string]any{"type": "string", "description": "CloudWatch log group"},
					"env":       map[string]any{"type": "string"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "registry_list_projects",
			Description: "List all projects in the registry",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "registry_list_plans",
			Description: "List all plans for a project",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
				"required":   []string{"name"},
			},
		},
		{
			Name:        "registry_get_plan",
			Description: "Get a specific plan by ticket number",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"ticket": map[string]any{"type": "string", "description": "e.g. ONE-24416"},
				},
				"required": []string{"name", "ticket"},
			},
		},
		{
			Name:        "registry_update_step",
			Description: "Update the status of a single plan step by index. Preferred over registry_write_plan for status-only changes.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":       map[string]any{"type": "string"},
					"ticket":     map[string]any{"type": "string", "description": "e.g. ONE-24416"},
					"step_index": map[string]any{"type": "integer", "description": "Zero-based index into plan_steps"},
					"status":     map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done", "blocked"}},
				},
				"required": []string{"name", "ticket", "step_index", "status"},
			},
		},
		{
			Name:        "registry_write_plan",
			Description: "Write or update a plan for a project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"ticket": map[string]any{"type": "string"},
					"data":   map[string]any{"type": "object"},
				},
				"required": []string{"name", "ticket", "data"},
			},
		},
		{
			Name:        "registry_write_audit",
			Description: "Append an entry to the project audit trail",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"entry": map[string]any{"type": "object", "description": "Include: action, ticket, actor, details"},
				},
				"required": []string{"name", "entry"},
			},
		},
		{
			Name:        "registry_write_deploy_check",
			Description: "Append an entry to the project deploy-check log",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"entry": map[string]any{"type": "object", "description": "Include: status, date, and other deploy-check metadata"},
				},
				"required": []string{"name", "entry"},
			},
		},
		{
			Name:        "registry_get_deploy_checks",
			Description: "Query deploy-check log entries by date range",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"since": map[string]any{"type": "string", "description": "ISO date YYYY-MM-DD inclusive"},
					"until": map[string]any{"type": "string", "description": "ISO date YYYY-MM-DD inclusive"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "registry_report_issue",
			Description: "ALWAYS CALL THIS TOOL when any registry MCP tool call fails, returns an error, or produces an unexpected result. Do not skip — call it immediately on failure. Provide: project name, tool that failed, error message, and optionally what you were trying to do.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project":  map[string]any{"type": "string", "description": "Project name (default: 'registry')"},
					"tool":     map[string]any{"type": "string", "description": "Name of the tool that failed"},
					"error":    map[string]any{"type": "string", "description": "Error message"},
					"context":  map[string]any{"type": "string", "description": "What you were trying to do (optional)"},
					"severity": map[string]any{"type": "string", "enum": []string{"error", "warning"}, "description": "Default: error"},
				},
				"required": []string{"tool", "error"},
			},
		},
		{
			Name:        "registry_get_resources",
			Description: "Get cached project resources (Grafana dashboards, Slack channels, Bitbucket repos, etc), optionally filtered by category",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":     map[string]any{"type": "string", "description": "Project name"},
					"category": map[string]any{"type": "string", "description": "Optional: filter to one category e.g. grafana, slack, aws, bitbucket"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "registry_get_audit",
			Description: "Query audit trail entries by date range",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"since": map[string]any{"type": "string", "description": "ISO date YYYY-MM-DD inclusive"},
					"until": map[string]any{"type": "string", "description": "ISO date YYYY-MM-DD inclusive"},
				},
				"required": []string{"name"},
			},
		},
	}
}
