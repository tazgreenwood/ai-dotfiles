package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// ── reader: SQLite-backed reads (DOTFILES-25) ────────────────────────────────
//
// Mirrors claude/mcp/server/store.go's dbPath resolution and query pattern.
// Registry-ui is read-only against the same registry.db the MCP server
// writes to: single source of truth, no separate JSON files.

type Project struct {
	Name  string         `json:"name"`
	Repo  map[string]any `json:"repo,omitempty"`
	Extra map[string]any `json:"-"`
}

type PlanStep struct {
	Step         int      `json:"step"`
	Title        string   `json:"title,omitempty"`
	Status       string   `json:"status"`
	Why          string   `json:"why,omitempty"`
	How          string   `json:"how,omitempty"`
	Files        []string `json:"files,omitempty"`
	Verification string   `json:"verification,omitempty"`
	Risk         string   `json:"risk,omitempty"`
	ID           int      `json:"id,omitempty"`
}

type PlanMeta struct {
	Ticket  string `json:"ticket"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
}

type Plan struct {
	Ticket             string     `json:"ticket"`
	Summary            string     `json:"summary"`
	Branch             string     `json:"branch,omitempty"`
	BaseBranch         string     `json:"base_branch,omitempty"`
	PRUrl              string     `json:"pr_url,omitempty"`
	AcceptanceCriteria []string   `json:"acceptance_criteria,omitempty"`
	PlanSteps          []PlanStep `json:"plan_steps,omitempty"`
}

type AuditEntry struct {
	Ticket     string `json:"ticket"`
	Type       string `json:"type,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Impact     string `json:"impact,omitempty"`
	Branch     string `json:"branch,omitempty"`
	PRUrl      string `json:"pr_url,omitempty"`
	Date       string `json:"date,omitempty"`
	RecordedAt string `json:"_recorded_at,omitempty"`
}

type IssueEntry struct {
	Tool       string `json:"tool"`
	Error      string `json:"error"`
	Context    string `json:"context,omitempty"`
	Severity   string `json:"severity"`
	RecordedAt string `json:"_recorded_at,omitempty"`
}

type DeployCheckEntry struct {
	App          string `json:"app,omitempty"`
	Env          string `json:"env,omitempty"`
	Cluster      string `json:"cluster,omitempty"`
	Profile      string `json:"profile,omitempty"`
	Status       string `json:"status,omitempty"`
	Summary      string `json:"summary,omitempty"`
	Services     any    `json:"services,omitempty"`
	ErrorsBefore any    `json:"errors_before,omitempty"`
	ErrorsAfter  any    `json:"errors_after,omitempty"`
	ReportPath   string `json:"report_path,omitempty"`
	Date         string `json:"date,omitempty"`
	RecordedAt   string `json:"_recorded_at,omitempty"`
}

func dataDir() string {
	if d := os.Getenv("REGISTRY_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "registry", "data")
}

func dbPath() string {
	return filepath.Join(dataDir(), "registry.db")
}

func openDB() (*sql.DB, error) {
	return sql.Open("sqlite", dbPath())
}

func ReadProjects() ([]Project, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name, data FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		var p Project
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			continue
		}
		p.Name = name
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if projects == nil {
		projects = []Project{}
	}
	return projects, nil
}

func ReadProject(name string) (Project, error) {
	db, err := openDB()
	if err != nil {
		return Project{}, err
	}
	defer db.Close()

	var raw string
	if err := db.QueryRow(`SELECT data FROM projects WHERE name = ?`, name).Scan(&raw); err != nil {
		return Project{}, err
	}
	var p Project
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Project{}, err
	}
	p.Name = name
	return p, nil
}

func ReadPlans(name string) ([]PlanMeta, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT ticket, data FROM plans WHERE project = ? ORDER BY id`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metas []PlanMeta
	for rows.Next() {
		var ticket, raw string
		if err := rows.Scan(&ticket, &raw); err != nil {
			return nil, err
		}
		var plan Plan
		if err := json.Unmarshal([]byte(raw), &plan); err != nil {
			continue
		}
		plan.Ticket = ticket
		status := "shipped"
		if len(plan.PlanSteps) == 0 {
			status = "active"
		}
		for _, s := range plan.PlanSteps {
			if s.Status != "done" {
				status = "active"
				break
			}
		}
		metas = append(metas, PlanMeta{
			Ticket:  plan.Ticket,
			Summary: plan.Summary,
			Status:  status,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if metas == nil {
		metas = []PlanMeta{}
	}
	return metas, nil
}

func ReadPlan(name, ticket string) (Plan, error) {
	db, err := openDB()
	if err != nil {
		return Plan{}, err
	}
	defer db.Close()

	var raw string
	if err := db.QueryRow(
		`SELECT data FROM plans WHERE project = ? AND ticket = ?`, name, ticket,
	).Scan(&raw); err != nil {
		return Plan{}, err
	}
	var plan Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return Plan{}, err
	}
	plan.Ticket = ticket
	return plan, nil
}

func ReadIssues(name, severity string) ([]IssueEntry, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT data FROM issues WHERE project = ? ORDER BY reported_at ASC`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []IssueEntry
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var e IssueEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if severity == "" {
		if entries == nil {
			entries = []IssueEntry{}
		}
		return entries, nil
	}
	var filtered []IssueEntry
	for _, e := range entries {
		if e.Severity == severity {
			filtered = append(filtered, e)
		}
	}
	if filtered == nil {
		filtered = []IssueEntry{}
	}
	return filtered, nil
}

func ReadDeployChecks(name, since, until string) ([]DeployCheckEntry, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `SELECT data FROM deploy_checks WHERE project = ?`
	args := []any{name}
	if since != "" {
		query += ` AND recorded_at >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND recorded_at <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY recorded_at ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []DeployCheckEntry
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var e DeployCheckEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []DeployCheckEntry{}
	}
	return entries, nil
}

func ReadAudit(name, since, until string) ([]AuditEntry, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `SELECT data FROM audit WHERE project = ?`
	args := []any{name}
	if since != "" {
		query += ` AND date >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND date <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY date ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []AuditEntry{}
	}
	return entries, nil
}
