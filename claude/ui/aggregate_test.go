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
