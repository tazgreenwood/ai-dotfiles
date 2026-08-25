package main

import (
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
			for _, s := range plan.PlanSteps {
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
					Step:    s.Step,
					Status:  s.Status,
					Why:     s.Why,
					DoneAt:  s.DoneAt,
				})
			}
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
