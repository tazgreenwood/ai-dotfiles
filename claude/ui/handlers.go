package main

import (
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"slices"
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

// relAge renders an RFC3339 timestamp as a coarse relative age ("6h ago").
//
// Deliberately NOT dateOnly: the Proposals queue is about how long something
// has been waiting on a human, and a date string cannot distinguish a proposal
// raised 10 minutes ago from one raised 20 hours ago — both render as today.
// An unparseable or empty timestamp falls back to the raw string rather than
// inventing an age.
func relAge(ts string) string {
	return relAgeAt(ts, time.Now())
}

// relAgeAt is relAge with an injectable "now", so the rendering is testable
// without sleeping.
func relAgeAt(ts string, now time.Time) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := now.Sub(t)
	if d < 0 {
		return "just now"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
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
		"percent":         func(n, d int) int { return n * 100 / d },
		"groupColorClass": groupColorClass,
		"dateOnly":        dateOnly,
		"relAge":          relAge,
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

// globalTabs returns the 6 fixed global-dashboard sidebar links, in display
// order, rendered above the per-project list.
//
// Proposals is first, above Kanban, on purpose: it is the only tab holding
// work that is blocked on the user, and a queue belongs above the records of
// what already happened. "/" remains Kanban — the ordering here changes what
// the eye lands on first, not what the root route serves.
func globalTabs() []navLink {
	return []navLink{
		{Label: "Proposals", URL: "/proposals"},
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
	Cards   []PlanCard
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
// project name, with cards inside each group sorted by ticket.
func groupCardsByProject(cards []PlanCard) []kanbanProjectGroup {
	byProject := map[string][]PlanCard{}
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
			return cs[i].Ticket < cs[j].Ticket
		})
		groups = append(groups, kanbanProjectGroup{Project: p, Cards: cs})
	}
	return groups
}

func handleDashboardKanban(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cutoff := time.Now().Add(-doneCutoffWindow)
	cards, hiddenOlder := AggregatePlanKanban(cutoff)

	columns := []kanbanColumn{
		{Header: "Pending", Status: "pending"},
		{Header: "In Progress", Status: "in_progress"},
		{Header: "In Review/QA", Status: "in_review"},
		{Header: "PR Ready", Status: "pr_ready"},
		{Header: "Done (14d)", Status: "done"},
		{Header: "Blocked", Status: "blocked"},
	}
	byStatus := map[string][]PlanCard{}
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
	Breadcrumbs     []breadcrumb
	Plan            Plan
	Columns         []planColumn
	EffectiveStatus string
	PhaseOptions    []phaseOption
}

// phaseOption is one entry in the plan-detail phase_override <select>.
type phaseOption struct {
	Value    string
	Label    string
	Selected bool
}

// planPhases are the 6 valid phase_override values, in the order they're
// offered in the plan-detail override <select> — must match validPlanPhases
// in claude/mcp/server/registry.go.
var planPhases = []struct{ Value, Label string }{
	{"pending", "Pending"},
	{"in_progress", "In Progress"},
	{"in_review", "In Review/QA"},
	{"pr_ready", "PR Ready"},
	{"done", "Done"},
	{"blocked", "Blocked"},
}

// buildPhaseOptions returns the "No override — automatic" option plus the 6
// phase options, with whichever matches selected (the plan's current
// effective status — its override if set, else the computed status) marked
// Selected.
func buildPhaseOptions(selected string) []phaseOption {
	opts := make([]phaseOption, 0, len(planPhases)+1)
	opts = append(opts, phaseOption{Value: "", Label: "No override — automatic", Selected: selected == ""})
	for _, p := range planPhases {
		opts = append(opts, phaseOption{Value: p.Value, Label: p.Label, Selected: selected == p.Value})
	}
	return opts
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

// proposalStatuses are the status values offered in the Proposals tab filter.
// "superseded" is absent on purpose: a superseded revision is history, not a
// queue entry, and the list is head-only.
var proposalFilterStatuses = []string{"pending", "approved", "rejected"}

type dashboardProposalsData struct {
	Breadcrumbs []breadcrumb
	Projects    []string
	Selected    string
	Status      string
	Statuses    []string
	Entries     []ProposalRow
}

// handleDashboardProposals renders the read-only global Proposals queue:
// proposals awaiting (or having received) a human decision, aggregated across
// every project, optionally narrowed by ?project= and ?status=.
//
// The status filter defaults to "pending" when the parameter is ABSENT — the
// default view is the queue, not the archive. An explicitly empty
// ?status= means "all statuses", which is how the "All statuses" option in
// the filter form clears it.
func handleDashboardProposals(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	selected := q.Get("project")
	status := "pending"
	if q.Has("status") {
		status = q.Get("status")
	}
	if status != "" && !slices.Contains(proposalFilterStatuses, status) {
		status = "pending"
	}
	data := dashboardProposalsData{
		Breadcrumbs: []breadcrumb{{Label: "Proposals"}},
		Projects:    projectNames(),
		Selected:    selected,
		Status:      status,
		Statuses:    proposalFilterStatuses,
		Entries:     AggregateProposals(selected, status),
	}
	render(w, r, "dashboard_proposals.html", data)
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
		{Header: "In Review/QA", Status: "in_review"},
		{Header: "Done", Status: "done"},
		{Header: "Blocked", Status: "blocked"},
	}
	pendingCol := 0
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
			// An unrecognized step status (including "pr_ready", which is a
			// plan-level derived status, never a step status) falls back to
			// Pending rather than being silently dropped from the board.
			columns[pendingCol].Steps = append(columns[pendingCol].Steps, s)
		}
	}

	auditTickets, _ := AuditTicketSet(name)
	effectiveStatus := derivePlanStatus(plan.PlanSteps, auditTickets[ticket], plan.PhaseOverride)

	data := planData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name, URL: "/projects/" + name},
			{Label: ticket},
		},
		Plan:            plan,
		Columns:         columns,
		EffectiveStatus: effectiveStatus,
		PhaseOptions:    buildPhaseOptions(effectiveStatus),
	}
	render(w, r, "plan.html", data)
}

// validPlanPhases mirrors claude/mcp/server/registry.go's allowlist of the
// same name — must stay in sync (the plan-detail <select> only ever offers
// these 6 values, but this handler is reachable by a direct POST too, so it
// enforces the same allowlist rather than trusting the form).
var validPlanPhases = func() map[string]bool {
	m := make(map[string]bool, len(planPhases))
	for _, p := range planPhases {
		m[p.Value] = true
	}
	return m
}()

// handleSetPlanPhase sets or clears a plan's phase_override via a single
// atomic json_set/json_remove UPDATE (never a read-modify-write — that would
// reintroduce the exact lost-update race SetPlanPhase's server-side
// counterpart in claude/mcp/server/store.go was written to avoid: a
// concurrent registry_update_step call landing between a read and a write
// here would have its step-status change silently reverted). An empty
// "phase" form value clears the override; any other value must be one of
// the 6 valid phases or the request is rejected — phase_override silently
// overrides planIsShipped's shipped/done determination with no other trace,
// so a bad value must never be allowed to persist, and every successful set
// is recorded as an event for that reason too.
func handleSetPlanPhase(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ticket := r.PathValue("ticket")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	phase := r.FormValue("phase")
	if phase != "" && !validPlanPhases[phase] {
		http.Error(w, "invalid phase: must be empty or one of pending, in_progress, in_review, pr_ready, blocked, done", http.StatusBadRequest)
		return
	}

	db, err := openDB()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	var exists int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM plans WHERE project = ? AND ticket = ?`, name, ticket,
	).Scan(&exists); err != nil || exists == 0 {
		http.NotFound(w, r)
		return
	}

	if phase == "" {
		_, err = db.Exec(
			`UPDATE plans SET data = json_remove(data, '$.phase_override') WHERE project = ? AND ticket = ?`,
			name, ticket,
		)
	} else {
		_, err = db.Exec(
			`UPDATE plans SET data = json_set(data, '$.phase_override', ?) WHERE project = ? AND ticket = ?`,
			phase, name, ticket,
		)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Best-effort: a failed event write must never fail the phase set, which
	// already committed.
	if eventData, err := json.Marshal(map[string]any{"ticket": ticket, "phase": phase}); err == nil {
		_, _ = db.Exec(
			`INSERT INTO events (project, type, occurred_at, data, tags) VALUES (?, ?, ?, ?, ?)`,
			name, "plan_phase_override_set", time.Now().UTC().Format(time.RFC3339), string(eventData), "phase-override",
		)
	}

	http.Redirect(w, r, "/projects/"+name+"/plans/"+ticket, http.StatusSeeOther)
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
