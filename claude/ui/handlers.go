package main

import (
	"embed"
	"html/template"
	"net/http"
	"sort"
)

//go:embed templates
var templateFS embed.FS

func render(w http.ResponseWriter, page string, data any) {
	t, err := template.ParseFS(templateFS, "templates/base.html", "templates/"+page)
	if err != nil {
		http.Error(w, "template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		// Headers already sent — log only
		_ = err
	}
}

type breadcrumb struct {
	Label string
	URL   string
}

type indexData struct {
	Breadcrumbs []breadcrumb
	Projects    []projectSummary
}

type projectSummary struct {
	Name       string
	PlanCount  int
	AuditCount int
	IssueCount int
}

type issueData struct {
	Breadcrumbs []breadcrumb
	ProjectName string
	Entries     []IssueEntry
	Severity    string
}

type projectData struct {
	Breadcrumbs        []breadcrumb
	Project            Project
	Plans              []planSummary
	RecentAudit        []AuditEntry
	RecentDeployChecks []DeployCheckEntry
	IssueCount         int
}

type planSummary struct {
	PlanMeta
	DoneSteps  int
	TotalSteps int
}

type planData struct {
	Breadcrumbs []breadcrumb
	Plan        Plan
	Columns     []planColumn
}

type planColumn struct {
	Header string
	Status string
	Steps  []PlanStep
}

type auditData struct {
	Breadcrumbs []breadcrumb
	ProjectName string
	Entries     []AuditEntry
	Since       string
	Until       string
}

type deployChecksData struct {
	Breadcrumbs []breadcrumb
	ProjectName string
	Entries     []DeployCheckEntry
	Since       string
	Until       string
}

type reviewData struct {
	Breadcrumbs   []breadcrumb
	Title         string
	Summary       string
	Why           string
	DiffOverview  string
	ExecutionMode string
	ExecutionLog  string
	Suggestions   []string
}

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	projects, err := ReadProjects()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	summaries := make([]projectSummary, 0, len(projects))
	for _, p := range projects {
		plans, _ := ReadPlans(p.Name)
		audit, _ := ReadAudit(p.Name, "", "")
		issues, _ := ReadIssues(p.Name, "")
		summaries = append(summaries, projectSummary{
			Name:       p.Name,
			PlanCount:  len(plans),
			AuditCount: len(audit),
			IssueCount: len(issues),
		})
	}

	data := indexData{
		Breadcrumbs: []breadcrumb{{Label: "Registry", URL: "/"}},
		Projects:    summaries,
	}

	render(w, "index.html", data)
}

func handlePlan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	ticket := r.PathValue("ticket")
	plan, err := ReadPlan(name, ticket)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	columns := []planColumn{
		{Header: "Pending", Status: "pending"},
		{Header: "In Progress", Status: "in_progress"},
		{Header: "Done", Status: "done"},
		{Header: "Blocked", Status: "blocked"},
	}
	for _, s := range plan.PlanSteps {
		matched := false
		for i := range columns {
			if columns[i].Status == s.Status {
				columns[i].Steps = append(columns[i].Steps, s)
				matched = true
				break
			}
		}
		if !matched {
			columns[0].Steps = append(columns[0].Steps, s)
		}
	}

	data := planData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name, URL: "/projects/" + name},
			{Label: ticket},
		},
		Plan:    plan,
		Columns: columns,
	}
	render(w, "plan.html", data)
}

func handleAudit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	if _, err := ReadProject(name); err != nil {
		http.NotFound(w, r)
		return
	}
	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")
	entries, err := ReadAudit(name, since, until)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Date > entries[j].Date
	})
	data := auditData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name, URL: "/projects/" + name},
			{Label: "Audit"},
		},
		ProjectName: name,
		Entries:     entries,
		Since:       since,
		Until:       until,
	}
	render(w, "audit.html", data)
}

func handleDeployChecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	if _, err := ReadProject(name); err != nil {
		http.NotFound(w, r)
		return
	}
	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")
	entries, err := ReadDeployChecks(name, since, until)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Date > entries[j].Date
	})
	data := deployChecksData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name, URL: "/projects/" + name},
			{Label: "Deploy Checks"},
		},
		ProjectName: name,
		Entries:     entries,
		Since:       since,
		Until:       until,
	}
	render(w, "deploy_checks.html", data)
}

func handleProject(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	proj, err := ReadProject(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	metas, _ := ReadPlans(name)
	plans := make([]planSummary, 0, len(metas))
	for _, m := range metas {
		full, err := ReadPlan(name, m.Ticket)
		done := 0
		total := 0
		if err == nil {
			total = len(full.PlanSteps)
			for _, s := range full.PlanSteps {
				if s.Status == "done" {
					done++
				}
			}
		}
		plans = append(plans, planSummary{
			PlanMeta:   m,
			DoneSteps:  done,
			TotalSteps: total,
		})
	}

	allAudit, _ := ReadAudit(name, "", "")
	recent := allAudit
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}

	allDeployChecks, _ := ReadDeployChecks(name, "", "")
	recentDeployChecks := allDeployChecks
	if len(recentDeployChecks) > 5 {
		recentDeployChecks = recentDeployChecks[len(recentDeployChecks)-5:]
	}

	issues, _ := ReadIssues(name, "")

	data := projectData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name},
		},
		Project:            proj,
		Plans:              plans,
		RecentAudit:        recent,
		RecentDeployChecks: recentDeployChecks,
		IssueCount:         len(issues),
	}

	render(w, "project.html", data)
}

func handleIssues(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	if _, err := ReadProject(name); err != nil {
		http.NotFound(w, r)
		return
	}
	severity := r.URL.Query().Get("severity")
	entries, err := ReadIssues(name, severity)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := issueData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name, URL: "/projects/" + name},
			{Label: "Issues"},
		},
		ProjectName: name,
		Entries:     entries,
		Severity:    severity,
	}
	render(w, "issues.html", data)
}
