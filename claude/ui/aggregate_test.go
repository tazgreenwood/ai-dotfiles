package main

import (
	"sort"
	"testing"
	"time"
)

// seedEvents inserts rows into the events table (created on demand — the
// shared reader_test.go fixture schema predates the events table, so we
// create it here with IF NOT EXISTS, mirroring store.go's createSchema).
func seedEvents(t *testing.T, dir, project, eventType string, entries []map[string]any) {
	t.Helper()
	db := openFixtureDB(t, dir)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project TEXT NOT NULL,
		type TEXT NOT NULL,
		occurred_at TEXT NOT NULL,
		data TEXT NOT NULL,
		tags TEXT
	)`); err != nil {
		t.Fatalf("create events table: %v", err)
	}
	for _, e := range entries {
		occurredAt, _ := e["occurred_at"].(string)
		if _, err := db.Exec(
			`INSERT INTO events (project, type, occurred_at, data) VALUES (?, ?, ?, ?)`,
			project, eventType, occurredAt, marshalFixture(t, e),
		); err != nil {
			t.Fatalf("seedEvents: %v", err)
		}
	}
}

// ── AggregateKanban ──────────────────────────────────────────────────────────

func TestAggregateKanban_DoneCutoff_ExcludesButCountsOlderDoneSteps(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	now := time.Now().UTC()
	oldDoneAt := now.AddDate(0, 0, -20).Format(time.RFC3339)
	recentDoneAt := now.Add(-1 * time.Hour).Format(time.RFC3339)

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedPlan(t, dir, "alpha", "DOTFILES-1", map[string]any{
		"ticket":  "DOTFILES-1",
		"summary": "test plan",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "old done", "status": "done", "done_at": oldDoneAt},
			{"step": 2, "title": "recent done", "status": "done", "done_at": recentDoneAt},
			{"step": 3, "title": "pending step", "status": "pending"},
			{"step": 4, "title": "blocked step", "status": "blocked"},
		},
	})

	cutoff := now.AddDate(0, 0, -14)
	cards, hiddenOlder := AggregateKanban(cutoff)

	if hiddenOlder != 1 {
		t.Fatalf("want hiddenOlder=1, got %d", hiddenOlder)
	}
	if len(cards) != 3 {
		t.Fatalf("want 3 visible cards, got %d: %+v", len(cards), cards)
	}
	for _, c := range cards {
		if c.Title == "old done" {
			t.Fatalf("old done-cutoff step leaked into visible cards: %+v", c)
		}
		if c.Project != "alpha" {
			t.Errorf("want Project=alpha, got %q", c.Project)
		}
	}
}

func TestAggregateKanban_NoDoneAt_NotExcluded(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedPlan(t, dir, "alpha", "DOTFILES-1", map[string]any{
		"ticket":  "DOTFILES-1",
		"summary": "test plan",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "done no timestamp", "status": "done"},
		},
	})

	cards, hiddenOlder := AggregateKanban(time.Now().AddDate(0, 0, -14))
	if hiddenOlder != 0 {
		t.Fatalf("want hiddenOlder=0, got %d", hiddenOlder)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 card (done step w/o done_at should stay visible), got %d", len(cards))
	}
}

// ── derivePlanStatus precedence (DOTFILES-44) ───────────────────────────────
//
// New precedence: blocked > in_review > in_progress > pr_ready > done > pending.
// pr_ready/done is audit-entry-gated: a plan whose steps are all "done" is
// "pr_ready" until an audit entry exists for its ticket, at which point it
// becomes "done". hasAuditEntry stands in for that check here; the real
// audit lookup is wired up in a later step.
func TestDerivePlanStatus_Precedence(t *testing.T) {
	tests := []struct {
		name          string
		steps         []PlanStep
		hasAuditEntry bool
		want          string
	}{
		{
			name:  "no steps",
			steps: nil,
			want:  "pending",
		},
		{
			name: "all pending",
			steps: []PlanStep{
				{Status: "pending"},
				{Status: "pending"},
			},
			want: "pending",
		},
		{
			name: "any in_progress",
			steps: []PlanStep{
				{Status: "pending"},
				{Status: "in_progress"},
			},
			want: "in_progress",
		},
		{
			name: "any blocked beats in_progress",
			steps: []PlanStep{
				{Status: "in_progress"},
				{Status: "blocked"},
			},
			want: "blocked",
		},
		{
			name: "any blocked beats in_review",
			steps: []PlanStep{
				{Status: "in_review"},
				{Status: "blocked"},
			},
			want: "blocked",
		},
		{
			name: "any in_review beats in_progress",
			steps: []PlanStep{
				{Status: "in_progress"},
				{Status: "in_review"},
			},
			want: "in_review",
		},
		{
			name: "any in_review beats done",
			steps: []PlanStep{
				{Status: "done"},
				{Status: "in_review"},
			},
			want: "in_review",
		},
		{
			name: "all done, no audit entry => pr_ready",
			steps: []PlanStep{
				{Status: "done"},
				{Status: "done"},
			},
			hasAuditEntry: false,
			want:          "pr_ready",
		},
		{
			name: "all done, with audit entry => done",
			steps: []PlanStep{
				{Status: "done"},
				{Status: "done"},
			},
			hasAuditEntry: true,
			want:          "done",
		},
		{
			name: "partial done (not all) with no in_progress/in_review/blocked => in_progress",
			steps: []PlanStep{
				{Status: "done"},
				{Status: "pending"},
			},
			want: "in_progress",
		},
		{
			name: "pr_ready beats pending even with audit entry true but not all done (should not happen but pin behavior)",
			steps: []PlanStep{
				{Status: "done"},
				{Status: "pending"},
			},
			hasAuditEntry: true,
			want:          "in_progress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := derivePlanStatus(tt.steps, tt.hasAuditEntry)
			if got != tt.want {
				t.Errorf("derivePlanStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── AggregatePlanKanban ──────────────────────────────────────────────────────

func TestAggregatePlanKanban_StatusDerivation(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})

	seedPlan(t, dir, "alpha", "DOTFILES-1", map[string]any{
		"ticket":  "DOTFILES-1",
		"summary": "all pending",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "a", "status": "pending"},
			{"step": 2, "title": "b", "status": "pending"},
		},
	})
	seedPlan(t, dir, "alpha", "DOTFILES-2", map[string]any{
		"ticket":  "DOTFILES-2",
		"summary": "partial",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "a", "status": "done", "done_at": time.Now().UTC().Format(time.RFC3339)},
			{"step": 2, "title": "b", "status": "pending"},
		},
	})
	seedPlan(t, dir, "alpha", "DOTFILES-3", map[string]any{
		"ticket":  "DOTFILES-3",
		"summary": "all done",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "a", "status": "done", "done_at": time.Now().UTC().Format(time.RFC3339)},
			{"step": 2, "title": "b", "status": "done", "done_at": time.Now().UTC().Format(time.RFC3339)},
		},
	})
	seedPlan(t, dir, "alpha", "DOTFILES-4", map[string]any{
		"ticket":  "DOTFILES-4",
		"summary": "has blocked",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "a", "status": "done", "done_at": time.Now().UTC().Format(time.RFC3339)},
			{"step": 2, "title": "b", "status": "blocked"},
			{"step": 3, "title": "c", "status": "in_progress"},
		},
	})
	seedPlan(t, dir, "alpha", "DOTFILES-5", map[string]any{
		"ticket":  "DOTFILES-5",
		"summary": "has in_progress",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "a", "status": "pending"},
			{"step": 2, "title": "b", "status": "in_progress"},
		},
	})
	// DOTFILES-3's audit entry is what distinguishes it from a merely
	// all-done plan (which would be pr_ready, not done — DOTFILES-44).
	seedAudit(t, dir, "alpha", []map[string]any{
		{"ticket": "DOTFILES-3", "date": "2026-08-01"},
	})

	cards, _ := AggregatePlanKanban(time.Now().AddDate(0, 0, -14))

	byTicket := map[string]PlanCard{}
	for _, c := range cards {
		byTicket[c.Ticket] = c
	}

	tests := []struct {
		ticket     string
		wantStatus string
		wantDone   int
		wantTotal  int
	}{
		{"DOTFILES-1", "pending", 0, 2},
		{"DOTFILES-2", "in_progress", 1, 2},
		{"DOTFILES-3", "done", 2, 2},
		{"DOTFILES-4", "blocked", 1, 3},
		{"DOTFILES-5", "in_progress", 0, 2},
	}
	for _, tt := range tests {
		c, ok := byTicket[tt.ticket]
		if !ok {
			t.Fatalf("missing card for %s", tt.ticket)
		}
		if c.Status != tt.wantStatus {
			t.Errorf("%s: want Status=%s, got %s", tt.ticket, tt.wantStatus, c.Status)
		}
		if c.DoneSteps != tt.wantDone || c.TotalSteps != tt.wantTotal {
			t.Errorf("%s: want %d/%d, got %d/%d", tt.ticket, tt.wantDone, tt.wantTotal, c.DoneSteps, c.TotalSteps)
		}
		if c.Project != "alpha" {
			t.Errorf("%s: want Project=alpha, got %s", tt.ticket, c.Project)
		}
	}
}

func TestAggregatePlanKanban_DoneCutoff_ExcludesFullyDonePlan(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	now := time.Now().UTC()
	oldDoneAt := now.AddDate(0, 0, -20).Format(time.RFC3339)
	recentDoneAt := now.Add(-1 * time.Hour).Format(time.RFC3339)

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedPlan(t, dir, "alpha", "DOTFILES-OLD", map[string]any{
		"ticket":  "DOTFILES-OLD",
		"summary": "old fully done plan",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "a", "status": "done", "done_at": oldDoneAt},
			{"step": 2, "title": "b", "status": "done", "done_at": oldDoneAt},
		},
	})
	seedPlan(t, dir, "alpha", "DOTFILES-NEW", map[string]any{
		"ticket":  "DOTFILES-NEW",
		"summary": "recent fully done plan",
		"plan_steps": []map[string]any{
			{"step": 1, "title": "a", "status": "done", "done_at": oldDoneAt},
			{"step": 2, "title": "b", "status": "done", "done_at": recentDoneAt},
		},
	})
	// Both plans need audit entries to be "done" (rather than pr_ready)
	// so the cutoff logic under test — which only ever applies to "done"
	// plans — actually exercises them (DOTFILES-44).
	seedAudit(t, dir, "alpha", []map[string]any{
		{"ticket": "DOTFILES-OLD", "date": "2026-01-01"},
		{"ticket": "DOTFILES-NEW", "date": "2026-08-01"},
	})

	cutoff := now.AddDate(0, 0, -14)
	cards, hiddenOlder := AggregatePlanKanban(cutoff)

	if hiddenOlder != 1 {
		t.Fatalf("want hiddenOlder=1, got %d", hiddenOlder)
	}
	for _, c := range cards {
		if c.Ticket == "DOTFILES-OLD" {
			t.Fatalf("old fully-done plan leaked into visible cards: %+v", c)
		}
	}
	found := false
	for _, c := range cards {
		if c.Ticket == "DOTFILES-NEW" {
			found = true
		}
	}
	if !found {
		t.Fatalf("recent fully-done plan missing from visible cards")
	}
}

// ── AggregateEvents ──────────────────────────────────────────────────────────

func TestAggregateEvents_ProjectFilterAndSort(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProject(t, dir, "beta", map[string]any{"name": "beta"})

	seedEvents(t, dir, "alpha", "pr_review", []map[string]any{
		{"occurred_at": "2026-08-01T00:00:00Z", "summary": "alpha older"},
		{"occurred_at": "2026-08-20T00:00:00Z", "summary": "alpha newer"},
	})
	seedEvents(t, dir, "beta", "pr_review", []map[string]any{
		{"occurred_at": "2026-08-15T00:00:00Z", "summary": "beta middle"},
	})

	all := AggregateEvents("pr_review", "")
	if len(all) != 3 {
		t.Fatalf("want 3 events across projects, got %d", len(all))
	}
	if !sort.SliceIsSorted(all, func(i, j int) bool { return all[i].OccurredAt > all[j].OccurredAt }) {
		t.Errorf("want newest-first order, got %+v", all)
	}
	if all[0].Summary != "alpha newer" {
		t.Errorf("want newest event first, got %q", all[0].Summary)
	}

	filtered := AggregateEvents("pr_review", "alpha")
	if len(filtered) != 2 {
		t.Fatalf("want 2 events for alpha filter, got %d", len(filtered))
	}
	for _, e := range filtered {
		if e.Project != "alpha" {
			t.Errorf("projectFilter leaked project %q", e.Project)
		}
	}
}

// ── AggregateAudit ───────────────────────────────────────────────────────────

func TestAggregateAudit_ProjectFilterAndSort(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProject(t, dir, "beta", map[string]any{"name": "beta"})

	seedAudit(t, dir, "alpha", []map[string]any{
		{"ticket": "DOTFILES-1", "date": "2026-08-01"},
		{"ticket": "DOTFILES-2", "date": "2026-08-20"},
	})
	seedAudit(t, dir, "beta", []map[string]any{
		{"ticket": "ONE-1", "date": "2026-08-15"},
	})

	all := AggregateAudit("")
	if len(all) != 3 {
		t.Fatalf("want 3 audit entries, got %d", len(all))
	}
	if all[0].Ticket != "DOTFILES-2" {
		t.Errorf("want newest-first (DOTFILES-2 first), got %q", all[0].Ticket)
	}

	filtered := AggregateAudit("beta")
	if len(filtered) != 1 || filtered[0].Ticket != "ONE-1" {
		t.Fatalf("want 1 beta entry (ONE-1), got %+v", filtered)
	}
}

// ── AggregateIssues ──────────────────────────────────────────────────────────

func TestAggregateIssues_ProjectFilter(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProject(t, dir, "beta", map[string]any{"name": "beta"})

	seedIssues(t, dir, "alpha", []map[string]any{
		{"tool": "build", "error": "boom", "severity": "high", "_recorded_at": "2026-08-01T00:00:00Z"},
	})
	seedIssues(t, dir, "beta", []map[string]any{
		{"tool": "ship", "error": "bang", "severity": "low", "_recorded_at": "2026-08-10T00:00:00Z"},
	})

	all := AggregateIssues("")
	if len(all) != 2 {
		t.Fatalf("want 2 issues, got %d", len(all))
	}

	filtered := AggregateIssues("alpha")
	if len(filtered) != 1 || filtered[0].Project != "alpha" {
		t.Fatalf("want 1 alpha issue, got %+v", filtered)
	}
}

// ── AggregateDeployChecks ────────────────────────────────────────────────────

func TestAggregateDeployChecks_ProjectFilter(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()

	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProject(t, dir, "beta", map[string]any{"name": "beta"})

	seedDeployChecks(t, dir, "alpha", []map[string]any{
		{"app": "svc-a", "status": "ok", "date": "2026-08-01T00:00:00Z"},
	})
	seedDeployChecks(t, dir, "beta", []map[string]any{
		{"app": "svc-b", "status": "ok", "date": "2026-08-10T00:00:00Z"},
	})

	all := AggregateDeployChecks("")
	if len(all) != 2 {
		t.Fatalf("want 2 deploy checks, got %d", len(all))
	}

	filtered := AggregateDeployChecks("beta")
	if len(filtered) != 1 || filtered[0].Project != "beta" {
		t.Fatalf("want 1 beta deploy check, got %+v", filtered)
	}
}
