package main

import (
	"embed"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// doneCutoffWindow is how far back a "done" plan step is still shown in the
// global Kanban board's Done column before it's counted as hidden instead.
const doneCutoffWindow = 14 * 24 * time.Hour

const plansPerPage = 10

//go:embed templates
var templateFS embed.FS

func dateOnly(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

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

// render parses and executes the named content template inside base.html.
// currentPath is exposed to templates as a func (rather than a data field)
// so every page — dashboard tabs and per-project pages alike — can compute
// aria-current on the sidebar without every handler's data struct needing a
// CurrentPath field.
func render(w http.ResponseWriter, r *http.Request, page string, data any) {
	currentPath := r.URL.Path
	t, err := template.New("base.html").Funcs(template.FuncMap{
		"sidebarProjects": sidebarProjects,
		"globalTabs":      globalTabs,
		"add":             func(a, b int) int { return a + b },
		"sub":             func(a, b int) int { return a - b },
		"groupColorClass": groupColorClass,
		"dateOnly":        dateOnly,
		"currentPath":     func() string { return currentPath },
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

// navLink is one sidebar entry — either a global tab (fixed URL, no count)
// or a per-project link (rendered separately by sidebarProjects).
type navLink struct {
	Label string
	URL   string
}

// globalTabs returns the 5 fixed global-dashboard sidebar links, in display
// order, rendered above the per-project list.
func globalTabs() []navLink {
	return []navLink{
		{Label: "Kanban", URL: "/"},
		{Label: "Reviews", URL: "/reviews"},
		{Label: "Audits", URL: "/audits"},
		{Label: "Issues", URL: "/issues"},
		{Label: "Deploy Checks", URL: "/deploy-checks"},
	}
}

// kanbanProjectGroup buckets a global Kanban column's cards by project, so
// the template can render a real sub-heading per project within the column.
type kanbanProjectGroup struct {
	Project string
	Cards   []KanbanCard
}

// kanbanColumn is one of the 4 fixed Kanban columns (Pending/In
// Progress/Done/Blocked), holding its cards grouped by project.
// HiddenOlder is only ever set on the Done column: the count of "done"
// steps excluded from Groups because their done_at predates the 14-day
// cutoff.
type kanbanColumn struct {
	Header      string
	Status      string
	Groups      []kanbanProjectGroup
	CardCount   int
	HiddenOlder int
}

type dashboardKanbanData struct {
	Breadcrumbs []breadcrumb
	Columns     []kanbanColumn
}

// groupCardsByProject buckets cards by Project, sorted alphabetically by
// project name, with cards inside each group sorted by ticket then step.
func groupCardsByProject(cards []KanbanCard) []kanbanProjectGroup {
	byProject := map[string][]KanbanCard{}
	var projects []string
	for _, c := range cards {
		if _, ok := byProject[c.Project]; !ok {
			projects = append(projects, c.Project)
		}
		byProject[c.Project] = append(byProject[c.Project], c)
	}
	sort.Strings(projects)
	groups := make([]kanbanProjectGroup, 0, len(projects))
	for _, p := range projects {
		cs := byProject[p]
		sort.SliceStable(cs, func(i, j int) bool {
			if cs[i].Ticket != cs[j].Ticket {
				return cs[i].Ticket < cs[j].Ticket
			}
			return cs[i].Step < cs[j].Step
		})
		groups = append(groups, kanbanProjectGroup{Project: p, Cards: cs})
	}
	return groups
}

func handleDashboardKanban(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cutoff := time.Now().Add(-doneCutoffWindow)
	cards, hiddenOlder := AggregateKanban(cutoff)

	columns := []kanbanColumn{
		{Header: "Pending", Status: "pending"},
		{Header: "In Progress", Status: "in_progress"},
		{Header: "Done (14d)", Status: "done"},
		{Header: "Blocked", Status: "blocked"},
	}
	byStatus := map[string][]KanbanCard{}
	for _, c := range cards {
		byStatus[c.Status] = append(byStatus[c.Status], c)
	}
	for i := range columns {
		columns[i].Groups = groupCardsByProject(byStatus[columns[i].Status])
		columns[i].CardCount = len(byStatus[columns[i].Status])
		if columns[i].Status == "done" {
			columns[i].HiddenOlder = hiddenOlder
		}
	}

	data := dashboardKanbanData{
		Breadcrumbs: []breadcrumb{{Label: "Registry", URL: "/"}},
		Columns:     columns,
	}
	render(w, r, "dashboard_kanban.html", data)
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
	RecentReviews      []EventEntry
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
	Suggestions   []Suggestion
}

type reviewsListData struct {
	Breadcrumbs []breadcrumb
	ProjectName string
	Entries     []EventEntry
}

type dashboardReviewsData struct {
	Breadcrumbs []breadcrumb
	Projects    []string
	Selected    string
	Entries     []EventWithProject
}

type dashboardAuditsData struct {
	Breadcrumbs []breadcrumb
	Projects    []string
	Selected    string
	Entries     []AuditWithProject
}

type dashboardIssuesData struct {
	Breadcrumbs []breadcrumb
	Projects    []string
	Selected    string
	Entries     []IssueWithProject
}

type dashboardDeployChecksData struct {
	Breadcrumbs []breadcrumb
	Projects    []string
	Selected    string
	Entries     []DeployCheckWithProject
}

// projectNames returns every project's name, sorted alphabetically, for
// populating a global tab's project-filter <select>.
func projectNames() []string {
	projects, err := ReadProjects()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}

// handleDashboardReviews renders the global Reviews tab: pr_review events
// aggregated across every project, optionally narrowed by ?project=.
func handleDashboardReviews(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	selected := r.URL.Query().Get("project")
	data := dashboardReviewsData{
		Breadcrumbs: []breadcrumb{{Label: "Reviews"}},
		Projects:    projectNames(),
		Selected:    selected,
		Entries:     AggregateEvents("pr_review", selected),
	}
	render(w, r, "dashboard_reviews.html", data)
}

// handleDashboardAudits renders the global Audits tab: audit entries
// aggregated across every project, optionally narrowed by ?project=.
func handleDashboardAudits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	selected := r.URL.Query().Get("project")
	data := dashboardAuditsData{
		Breadcrumbs: []breadcrumb{{Label: "Audits"}},
		Projects:    projectNames(),
		Selected:    selected,
		Entries:     AggregateAudit(selected),
	}
	render(w, r, "dashboard_audits.html", data)
}

// handleDashboardIssues renders the global Issues tab: issue-log entries
// aggregated across every project, optionally narrowed by ?project=.
func handleDashboardIssues(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	selected := r.URL.Query().Get("project")
	data := dashboardIssuesData{
		Breadcrumbs: []breadcrumb{{Label: "Issues"}},
		Projects:    projectNames(),
		Selected:    selected,
		Entries:     AggregateIssues(selected),
	}
	render(w, r, "dashboard_issues.html", data)
}

// handleDashboardDeployChecks renders the global Deploy Checks tab: deploy
// check entries aggregated across every project, optionally narrowed by
// ?project=.
func handleDashboardDeployChecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	selected := r.URL.Query().Get("project")
	data := dashboardDeployChecksData{
		Breadcrumbs: []breadcrumb{{Label: "Deploy Checks"}},
		Projects:    projectNames(),
		Selected:    selected,
		Entries:     AggregateDeployChecks(selected),
	}
	render(w, r, "dashboard_deploy_checks.html", data)
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
	render(w, r, "plan.html", data)
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
	render(w, r, "audit.html", data)
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
	render(w, r, "deploy_checks.html", data)
}

func handleReviewDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	entry, err := ReadEvent(name, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := reviewData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name, URL: "/projects/" + name},
			{Label: "Reviews", URL: "/projects/" + name + "/reviews"},
			{Label: idStr},
		},
		Title:         "Review — " + dateOnly(entry.OccurredAt) + " — " + entry.Verdict,
		Summary:       entry.Summary,
		Why:           entry.Why,
		DiffOverview:  entry.DiffOverview,
		ExecutionMode: entry.ExecutionMode,
		ExecutionLog:  entry.ExecutionLog,
		Suggestions:   entry.Suggestions,
	}
	render(w, r, "review.html", data)
}

// handleReviewsList renders the list of code reviews for a project, querying the registry event log for pr_review entries.
func handleReviewsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	if _, err := ReadProject(name); err != nil {
		http.NotFound(w, r)
		return
	}
	entries, err := ReadEvents(name, "pr_review", "", "")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := reviewsListData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name, URL: "/projects/" + name},
			{Label: "Reviews"},
		},
		ProjectName: name,
		Entries:     entries,
	}
	render(w, r, "reviews.html", data)
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

	recentReviews, _ := ReadEvents(name, "pr_review", "", "")
	if len(recentReviews) > 5 {
		recentReviews = recentReviews[:5]
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
		RecentReviews:      recentReviews,
		IssueCount:         len(issues),
		Stats:              stats,
		Page:               page,
		TotalPages:         totalPages,
		HasPrev:            page > 1,
		HasNext:            page < totalPages,
	}

	render(w, r, "project.html", data)
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
	render(w, r, "issues.html", data)
}
