package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── Data dir ───────────────────────────────────────────────────────────────────

func dataDir() string {
	if d := os.Getenv("REGISTRY_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "clearlink-registry", "data")
}

func projectDir(name string) string { return filepath.Join(dataDir(), name) }
func plansDir(name string) string   { return filepath.Join(dataDir(), name, "plans") }

// ── JSON helpers ───────────────────────────────────────────────────────────────

func readJSON(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

func writeJSON(path string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
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
	data, err := readJSON(filepath.Join(projectDir(name), "project.json"))
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
	file := filepath.Join(projectDir(name), "project.json")
	data, err := readJSON(file)
	if err != nil {
		data = map[string]any{}
	}
	dotSet(data, path, value)
	if err := writeJSON(file, data); err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "path": path, "value": value})
}

func registryInitProject(args map[string]any) ToolResult {
	name := str(args, "name")
	if name == "" {
		return toolErr("name required")
	}
	file := filepath.Join(projectDir(name), "project.json")
	if _, err := os.Stat(file); err == nil {
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
	if err := writeJSON(file, data); err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "created": file, "data": data})
}

func strOr(args map[string]any, key, def string) string {
	if v := str(args, key); v != "" {
		return v
	}
	return def
}

func registryListProjects(args map[string]any) ToolResult {
	entries, err := os.ReadDir(dataDir())
	if err != nil {
		return toolOK(map[string]any{"projects": []string{}})
	}
	var projects []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dataDir(), e.Name(), "project.json")); err == nil {
				projects = append(projects, e.Name())
			}
		}
	}
	return toolOK(map[string]any{"projects": projects})
}

func registryListPlans(args map[string]any) ToolResult {
	name := str(args, "name")
	if name == "" {
		return toolErr("name required")
	}
	entries, err := os.ReadDir(plansDir(name))
	if err != nil {
		return toolOK(map[string]any{"plans": []any{}})
	}
	var plans []map[string]any
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := readJSON(filepath.Join(plansDir(name), e.Name()))
		if err != nil {
			continue
		}
		ticket, _ := data["ticket"].(string)
		if ticket == "" {
			ticket = strings.TrimSuffix(e.Name(), ".json")
		}
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
	data, err := readJSON(filepath.Join(plansDir(name), ticket+".json"))
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
	file := filepath.Join(plansDir(name), ticket+".json")
	if err := writeJSON(file, data); err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "file": file})
}

func registryWriteAudit(args map[string]any) ToolResult {
	name := str(args, "name")
	entry, ok := args["entry"].(map[string]any)
	if name == "" || !ok {
		return toolErr("name and entry required")
	}
	file := filepath.Join(projectDir(name), "audit.json")
	var log []any
	if data, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(data, &log)
	}
	entry["_recorded_at"] = time.Now().UTC().Format(time.RFC3339)
	log = append(log, entry)
	if err := writeJSON(file, log); err != nil {
		return toolErr(err.Error())
	}
	return toolOK(map[string]any{"ok": true, "total_entries": len(log)})
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
	}
}
