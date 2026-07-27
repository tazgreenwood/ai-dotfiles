package main

import (
	"embed"
	"html/template"
	"net/http"
	"sort"
	"strconv"
)

const plansPerPage = 10

//go:embed templates
var templateFS embed.FS

type sidebarItem struct {
	Name      string
	PlanCount int
}

func sidebarProjects() []sidebarItem {
	projects, err := ReadProjects()
	if err != nil {
		return nil
	}
	items := make([]sidebarItem, 0, len(projects))
	for _, p := range projects {
		plans, _ := ReadPlans(p.Name)
		items = append(items, sidebarItem{Name: p.Name, PlanCount: len(plans)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// groupColorClass cycles a fixed small Tailwind palette by group number, so
// steps sharing a parallel_group get a matching tint regardless of which
// kanban column they land in.
func groupColorClass(group int) string {
	palette := []string{
		"bg-sky-500/10 text-sky-700 dark:text-sky-400 ring-1 ring-inset ring-sky-500/20",
		"bg-fuchsia-500/10 text-fuchsia-700 dark:text-fuchsia-400 ring-1 ring-inset ring-fuchsia-500/20",
		"bg-violet-500/10 text-violet-700 dark:text-violet-400 ring-1 ring-inset ring-violet-500/20",
		"bg-teal-500/10 text-teal-700 dark:text-teal-400 ring-1 ring-inset ring-teal-500/20",
		"bg-rose-500/10 text-rose-700 dark:text-rose-400 ring-1 ring-inset ring-rose-500/20",
	}
	return palette[group%len(palette)]
}

func render(w http.ResponseWriter, page string, data any) {
	t, err := template.New("base.html").Funcs(template.FuncMap{
		"sidebarProjects": sidebarProjects,
		"add":             func(a, b int) int { return a + b },
		"sub":             func(a, b int) int { return a - b },
		"groupColorClass": groupColorClass,
	}).ParseFS(templateFS, "templates/base.html", "templates/"+page)
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
	Stats       dashboardStats
}

type dashboardStats struct {
	TotalProjects int
	ActivePlans   int
	ShippedPlans  int
	OpenIssues    int
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
	Stats              projectStats
	Page               int
	TotalPages         int
	HasPrev            bool
	HasNext            bool
}

type projectStats struct {
	TotalPlans        int
	ShippedPlans      int
	TotalSteps        int
	DoneSteps         int
	OpenIssues        int
	LatestDeployApp   string
	LatestDeployState string
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
	stats := dashboardStats{TotalProjects: len(projects)}
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
		for _, pl := range plans {
			if pl.Status == "shipped" {
				stats.ShippedPlans++
			} else {
				stats.ActivePlans++
			}
		}
		stats.OpenIssues += len(issues)
	}

	data := indexData{
		Breadcrumbs: []breadcrumb{{Label: "Registry", URL: "/"}},
		Projects:    summaries,
		Stats:       stats,
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
	stats := projectStats{TotalPlans: len(metas)}
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
		if m.Status == "shipped" {
			stats.ShippedPlans++
		}
		stats.TotalSteps += total
		stats.DoneSteps += done
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
	if len(allDeployChecks) > 0 {
		latest := allDeployChecks[len(allDeployChecks)-1]
		stats.LatestDeployApp = latest.App
		stats.LatestDeployState = latest.Status
	}

	issues, _ := ReadIssues(name, "")
	stats.OpenIssues = len(issues)

	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	totalPages := max((len(plans)+plansPerPage-1)/plansPerPage, 1)
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * plansPerPage
	end := start + plansPerPage
	if start > len(plans) {
		start = len(plans)
	}
	if end > len(plans) {
		end = len(plans)
	}
	pagedPlans := plans[start:end]

	data := projectData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name},
		},
		Project:            proj,
		Plans:              pagedPlans,
		RecentAudit:        recent,
		RecentDeployChecks: recentDeployChecks,
		IssueCount:         len(issues),
		Stats:              stats,
		Page:               page,
		TotalPages:         totalPages,
		HasPrev:            page > 1,
		HasNext:            page < totalPages,
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
