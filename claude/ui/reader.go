package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

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

func dataDir() string {
	if d := os.Getenv("REGISTRY_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "registry", "data")
}

func ReadProjects() ([]Project, error) {
	base := dataDir()
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Project{}, nil
		}
		return nil, err
	}
	var projects []Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pf := filepath.Join(base, e.Name(), "project.json")
		if _, err := os.Stat(pf); err != nil {
			continue
		}
		p, err := readProjectFile(pf)
		if err != nil {
			continue
		}
		projects = append(projects, p)
	}
	if projects == nil {
		projects = []Project{}
	}
	return projects, nil
}

func ReadProject(name string) (Project, error) {
	pf := filepath.Join(dataDir(), name, "project.json")
	return readProjectFile(pf)
}

func readProjectFile(path string) (Project, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Project{}, err
	}
	var p Project
	if err := json.Unmarshal(b, &p); err != nil {
		return Project{}, err
	}
	return p, nil
}

func ReadPlans(name string) ([]PlanMeta, error) {
	plansDir := filepath.Join(dataDir(), name, "plans")
	entries, err := os.ReadDir(plansDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []PlanMeta{}, nil
		}
		return nil, err
	}
	var metas []PlanMeta
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(plansDir, e.Name()))
		if err != nil {
			continue
		}
		var plan Plan
		if err := json.Unmarshal(b, &plan); err != nil {
			continue
		}
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
	if metas == nil {
		metas = []PlanMeta{}
	}
	return metas, nil
}

func ReadPlan(name, ticket string) (Plan, error) {
	pf := filepath.Join(dataDir(), name, "plans", ticket+".json")
	b, err := os.ReadFile(pf)
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	if err := json.Unmarshal(b, &plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func ReadAudit(name, since, until string) ([]AuditEntry, error) {
	af := filepath.Join(dataDir(), name, "audit.json")
	b, err := os.ReadFile(af)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []AuditEntry{}, nil
		}
		return nil, err
	}
	var entries []AuditEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	var filtered []AuditEntry
	for _, e := range entries {
		d := e.Date
		if len(d) > 10 {
			d = d[:10]
		}
		if since != "" && d < since {
			continue
		}
		if until != "" && d > until {
			continue
		}
		filtered = append(filtered, e)
	}
	if filtered == nil {
		filtered = []AuditEntry{}
	}
	return filtered, nil
}
