package main

import (
	"database/sql"
	"sort"
	"time"
)

// ── aggregate: cross-project readers (DOTFILES-32) ───────────────────────────
//
// Every reader in reader.go is project-scoped. The global dashboard tabs
// (Kanban/Reviews/Audits/Issues/Deploy Checks) need the same data merged
// across every project instead. These functions loop ReadProjects() and
// delegate to the existing per-project readers rather than querying the
// registry.db schema directly, so they can never drift from — or regress —
// the per-project reads those readers already serve.

// KanbanCard is a single plan step flattened for the global Kanban board,
// tagged with the project it belongs to.
type KanbanCard struct {
	Project string `json:"project"`
	Ticket  string `json:"ticket"`
	Title   string `json:"title,omitempty"`
	Step    int    `json:"step"`
	Status  string `json:"status"`
	Why     string `json:"why,omitempty"`
	DoneAt  string `json:"done_at,omitempty"`
}

// AggregateKanban walks every project's plans and flattens their plan steps
// into cards for the global Kanban board (bucketed by Status once rendered).
// A "done" step whose done_at predates doneCutoff is excluded from the
// returned cards — kept out of the visible Done column — but is still
// tallied and reported via hiddenOlder, so callers can render a
// "+N older, hidden" indicator instead of silently dropping it. A done step
// with no done_at (recorded before this field existed) is never excluded,
// since its age can't be determined.
func AggregateKanban(doneCutoff time.Time) (cards []KanbanCard, hiddenOlder int) {
	projects, err := ReadProjects()
	if err != nil {
		return nil, 0
	}
	for _, p := range projects {
		metas, err := ReadPlans(p.Name)
		if err != nil {
			continue
		}
		for _, m := range metas {
			plan, err := ReadPlan(p.Name, m.Ticket)
			if err != nil {
				continue
			}
			for i, s := range plan.PlanSteps {
				if s.Status == "done" && s.DoneAt != "" {
					if t, err := time.Parse(time.RFC3339, s.DoneAt); err == nil && t.Before(doneCutoff) {
						hiddenOlder++
						continue
					}
				}
				cards = append(cards, KanbanCard{
					Project: p.Name,
					Ticket:  m.Ticket,
					Title:   s.Title,
					Step:    i + 1,
					Status:  s.Status,
					Why:     s.Why,
					DoneAt:  s.DoneAt,
				})
			}
		}
	}
	return cards, hiddenOlder
}

// PlanCard is a single plan flattened for the global Kanban board (one card
// per plan, not per step — DOTFILES-39), tagged with the project it belongs
// to and its overall progress/status derived from its steps.
type PlanCard struct {
	Project      string `json:"project"`
	Ticket       string `json:"ticket"`
	Title        string `json:"title,omitempty"`
	DoneSteps    int    `json:"done_steps"`
	TotalSteps   int    `json:"total_steps"`
	Status       string `json:"status"`
	LatestDoneAt string `json:"latest_done_at,omitempty"`
}

// AggregatePlanKanban walks every project's plans and produces one PlanCard
// per plan (rather than AggregateKanban's one card per step) for the global
// Kanban board. A fully-done plan whose latest step done_at predates
// doneCutoff is excluded from the returned cards — kept out of the visible
// Done column — but is still tallied and reported via hiddenOlder, so
// callers can render a "+N older, hidden" indicator, mirroring
// AggregateKanban's per-step cutoff exactly but applied once per plan using
// its latest done_at. A fully-done plan whose steps carry no done_at
// (recorded before this field existed) is never excluded, since its age
// can't be determined.
func AggregatePlanKanban(doneCutoff time.Time) (cards []PlanCard, hiddenOlder int) {
	projects, err := ReadProjects()
	if err != nil {
		return nil, 0
	}
	for _, p := range projects {
		metas, err := ReadPlans(p.Name)
		if err != nil {
			continue
		}
		for _, m := range metas {
			plan, err := ReadPlan(p.Name, m.Ticket)
			if err != nil {
				continue
			}
			// Status is persisted on the plan row by the registry MCP
			// server's ComputePlanStatus at each mutation point (UpdateStep,
			// WriteAudit, SetPlanPhase) — read directly rather than
			// re-derived here (DOTFILES-48). A plan written before that
			// migration and not yet backfilled falls back to "pending"
			// rather than rendering with an empty Kanban column.
			status := plan.Status
			if status == "" {
				status = "pending"
			}
			doneSteps := 0
			var latestDoneAt string
			var latestDoneAtParsed time.Time
			for _, s := range plan.PlanSteps {
				if s.Status != "done" {
					continue
				}
				doneSteps++
				if s.DoneAt == "" {
					continue
				}
				if t, err := time.Parse(time.RFC3339, s.DoneAt); err == nil {
					if t.After(latestDoneAtParsed) {
						latestDoneAtParsed = t
						latestDoneAt = s.DoneAt
					}
				}
			}
			if status == "done" && latestDoneAt != "" && latestDoneAtParsed.Before(doneCutoff) {
				hiddenOlder++
				continue
			}
			cards = append(cards, PlanCard{
				Project:      p.Name,
				Ticket:       m.Ticket,
				Title:        plan.Summary,
				DoneSteps:    doneSteps,
				TotalSteps:   len(plan.PlanSteps),
				Status:       status,
				LatestDoneAt: latestDoneAt,
			})
		}
	}
	return cards, hiddenOlder
}

// EventWithProject tags an EventEntry with its owning project, for the
// global Reviews (and any future event-type) tab.
type EventWithProject struct {
	Project string `json:"project"`
	EventEntry
}

// AggregateEvents loops every project, reads its events (optionally
// filtered by eventType, same as ReadEvents), tags each with its project,
// and returns them merged and sorted newest-first by OccurredAt.
// projectFilter narrows the result to a single project when non-empty.
func AggregateEvents(eventType, projectFilter string) []EventWithProject {
	projects, err := ReadProjects()
	if err != nil {
		return nil
	}
	var out []EventWithProject
	for _, p := range projects {
		if projectFilter != "" && p.Name != projectFilter {
			continue
		}
		events, err := ReadEvents(p.Name, eventType, "", "")
		if err != nil {
			continue
		}
		for _, e := range events {
			out = append(out, EventWithProject{Project: p.Name, EventEntry: e})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].OccurredAt > out[j].OccurredAt
	})
	return out
}

// AuditWithProject tags an AuditEntry with its owning project, for the
// global Audits tab.
type AuditWithProject struct {
	Project string `json:"project"`
	AuditEntry
}

func auditSortKey(e AuditEntry) string {
	if e.Date != "" {
		return e.Date
	}
	return e.RecordedAt
}

// AggregateAudit loops every project's audit log, tags each entry with its
// project, and returns them merged and sorted newest-first (by Date, falling
// back to RecordedAt when Date is unset). projectFilter narrows the result
// to a single project when non-empty.
func AggregateAudit(projectFilter string) []AuditWithProject {
	projects, err := ReadProjects()
	if err != nil {
		return nil
	}
	var out []AuditWithProject
	for _, p := range projects {
		if projectFilter != "" && p.Name != projectFilter {
			continue
		}
		entries, err := ReadAudit(p.Name, "", "")
		if err != nil {
			continue
		}
		for _, e := range entries {
			out = append(out, AuditWithProject{Project: p.Name, AuditEntry: e})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return auditSortKey(out[i].AuditEntry) > auditSortKey(out[j].AuditEntry)
	})
	return out
}

// IssueWithProject tags an IssueEntry with its owning project, for the
// global Issues tab.
type IssueWithProject struct {
	Project string `json:"project"`
	IssueEntry
}

// AggregateIssues loops every project's issue log, tags each entry with its
// project, and returns them merged and sorted newest-first by RecordedAt.
// projectFilter narrows the result to a single project when non-empty.
func AggregateIssues(projectFilter string) []IssueWithProject {
	projects, err := ReadProjects()
	if err != nil {
		return nil
	}
	var out []IssueWithProject
	for _, p := range projects {
		if projectFilter != "" && p.Name != projectFilter {
			continue
		}
		entries, err := ReadIssues(p.Name, "")
		if err != nil {
			continue
		}
		for _, e := range entries {
			out = append(out, IssueWithProject{Project: p.Name, IssueEntry: e})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].RecordedAt > out[j].RecordedAt
	})
	return out
}

// DeployCheckWithProject tags a DeployCheckEntry with its owning project,
// for the global Deploy Checks tab.
type DeployCheckWithProject struct {
	Project string `json:"project"`
	DeployCheckEntry
}

func deployCheckSortKey(e DeployCheckEntry) string {
	if e.Date != "" {
		return e.Date
	}
	return e.RecordedAt
}

// AggregateDeployChecks loops every project's deploy checks, tags each entry
// with its project, and returns them merged and sorted newest-first (by
// Date, falling back to RecordedAt when Date is unset). projectFilter
// narrows the result to a single project when non-empty.
func AggregateDeployChecks(projectFilter string) []DeployCheckWithProject {
	projects, err := ReadProjects()
	if err != nil {
		return nil
	}
	var out []DeployCheckWithProject
	for _, p := range projects {
		if projectFilter != "" && p.Name != projectFilter {
			continue
		}
		entries, err := ReadDeployChecks(p.Name, "", "")
		if err != nil {
			continue
		}
		for _, e := range entries {
			out = append(out, DeployCheckWithProject{Project: p.Name, DeployCheckEntry: e})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return deployCheckSortKey(out[i].DeployCheckEntry) > deployCheckSortKey(out[j].DeployCheckEntry)
	})
	return out
}

// ── proposals (DOTFILES-34) ──────────────────────────────────────────────────
//
// The Proposals tab is a QUEUE, not a record: it answers "what is waiting on
// me?" rather than "what happened recently?". That inverts two conventions the
// tabs above deliberately share, and both inversions are intentional:
//
//  1. Sort. Every Aggregate* above returns newest-first. Here pending
//     proposals come first and OLDEST-first inside that group, because the
//     thing that has been waiting on a human the longest is the thing most at
//     risk of being forgotten. Non-pending rows keep the newest-first house
//     convention, since for them the queue framing no longer applies.
//  2. Row count. One row per revision CHAIN, not per proposal. A pushback
//     re-plan supersedes the prior revision and both are preserved in the
//     table, but a queue that lists five revisions of one request as five
//     things to decide is lying about how much work is waiting. Superseded
//     revisions stay readable in the Slack thread the row links to.

// ProposalRow is one proposal flattened for the read-only Proposals queue,
// tagged with its project and its position in its revision chain.
type ProposalRow struct {
	Project         string `json:"project"`
	ID              int64  `json:"id"`
	Kind            string `json:"kind"`
	Summary         string `json:"summary"`
	Status          string `json:"status"`
	SourceChannel   string `json:"source_channel"`
	SourcePermalink string `json:"source_permalink"`
	// NotifiedAt is empty when the DB column is NULL — i.e. the proposal was
	// recorded but never actually delivered to Slack.
	//
	// Delivery status and link availability are INDEPENDENT (DOTFILES-39). The
	// template gates the "Open in Slack" anchor on SourcePermalink alone and
	// shows a "Not sent" badge alongside it when NotifiedAt is empty. Do NOT
	// re-gate the anchor on NotifiedAt: a row whose Slack post failed still
	// carries a valid permalink, and hiding it was the bug.
	NotifiedAt   string `json:"notified_at,omitempty"`
	DecisionNote string `json:"decision_note,omitempty"`
	CreatedAt    string `json:"created_at"`
	// Revision is 1 for a first-cut proposal, N for the head of a chain of N
	// revisions. Rendered as a static "rev N" badge.
	Revision int `json:"revision"`
}

// ReadProposals reads every proposal row across all projects (or one project
// when projectFilter is non-empty), unfiltered by status. Callers filter and
// sort; the chain walk in AggregateProposals needs the superseded rows even
// when it won't render them.
func ReadProposals(projectFilter string) ([]ProposalRow, map[int64]int64, error) {
	db, err := openDB()
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	query := `SELECT id, project, kind, summary, status, source_channel,
		source_permalink, notified_at, decision_note, created_at, superseded_by
		FROM proposals`
	var args []any
	if projectFilter != "" {
		query += ` WHERE project = ?`
		args = append(args, projectFilter)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var out []ProposalRow
	// predecessor maps a proposal id to the revision it superseded, so a
	// chain can be walked backwards from its head to count revisions.
	predecessor := map[int64]int64{}
	for rows.Next() {
		var p ProposalRow
		var notifiedAt, decisionNote sql.NullString
		var supersededBy sql.NullInt64
		if err := rows.Scan(
			&p.ID, &p.Project, &p.Kind, &p.Summary, &p.Status, &p.SourceChannel,
			&p.SourcePermalink, &notifiedAt, &decisionNote, &p.CreatedAt, &supersededBy,
		); err != nil {
			return nil, nil, err
		}
		p.NotifiedAt = notifiedAt.String
		p.DecisionNote = decisionNote.String
		if supersededBy.Valid {
			predecessor[supersededBy.Int64] = p.ID
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return out, predecessor, nil
}

// AggregateProposals returns the Proposals queue: one row per revision chain
// (superseded revisions are never listed), optionally narrowed to one project
// and/or one status, sorted pending-first / oldest-first within pending.
//
// An empty status means "every non-superseded status". Passing status
// "superseded" explicitly is honoured, for completeness — it is not offered in
// the UI filter.
func AggregateProposals(projectFilter, status string) []ProposalRow {
	all, predecessor, err := ReadProposals(projectFilter)
	if err != nil {
		return nil
	}

	var out []ProposalRow
	for _, p := range all {
		if status == "" {
			if p.Status == "superseded" {
				continue
			}
		} else if p.Status != status {
			continue
		}
		p.Revision = revisionDepth(p.ID, predecessor)
		out = append(out, p)
	}

	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := out[i].Status == "pending", out[j].Status == "pending"
		if pi != pj {
			return pi
		}
		if pi {
			// Oldest first: longest-waiting decision at the top.
			if out[i].CreatedAt != out[j].CreatedAt {
				return out[i].CreatedAt < out[j].CreatedAt
			}
			return out[i].ID < out[j].ID
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// revisionDepth counts how many revisions precede id in its chain, returning
// 1 for a proposal that superseded nothing. The visited set guards against a
// cycle in the chain (which the transactional SupersedeProposal writer should
// make impossible) rather than hanging the request.
func revisionDepth(id int64, predecessor map[int64]int64) int {
	depth := 1
	visited := map[int64]bool{id: true}
	for {
		prev, ok := predecessor[id]
		if !ok || visited[prev] {
			return depth
		}
		visited[prev] = true
		id = prev
		depth++
	}
}
