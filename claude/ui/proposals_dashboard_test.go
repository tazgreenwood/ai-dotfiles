package main

import (
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ── Proposals queue tab (DOTFILES-34 step 7) ─────────────────────────────────

// proposalFixture is the seed shape for one proposals row. Zero values are
// filled with valid defaults so a test only states the fields it cares about;
// NotifiedAt and SupersededBy are pointers so a test can assert on the NULL
// cases the render states hinge on.
type proposalFixture struct {
	Project      string
	Source       string
	SourceRef    string
	Channel      string
	Permalink    string
	Kind         string
	Summary      string
	Status       string
	CreatedAt    string
	NotifiedAt   *string
	SupersededBy *int64
}

func strp(s string) *string { return &s }

// seedProposals inserts rows into the proposals table and returns their ids,
// in the order given.
func seedProposals(t *testing.T, dir string, rows []proposalFixture) []int64 {
	t.Helper()
	db := openFixtureDB(t, dir)
	defer db.Close()

	var ids []int64
	for i, r := range rows {
		if r.Source == "" {
			r.Source = "slack"
		}
		if r.SourceRef == "" {
			r.SourceRef = "ts-" + r.Summary + itoa(i)
		}
		if r.Channel == "" {
			r.Channel = "C0BTZPUJ0E6"
		}
		if r.Kind == "" {
			r.Kind = "plan"
		}
		if r.Status == "" {
			r.Status = "pending"
		}
		if r.CreatedAt == "" {
			r.CreatedAt = "2026-08-01T00:00:00Z"
		}
		var notified any
		if r.NotifiedAt != nil {
			notified = *r.NotifiedAt
		}
		var superseded any
		if r.SupersededBy != nil {
			superseded = *r.SupersededBy
		}
		res, err := db.Exec(
			`INSERT INTO proposals
				(project, source, source_channel, source_permalink, source_ref,
				 kind, summary, payload, status, superseded_by, decision_note,
				 notified_at, decided_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, '{}', ?, ?, '', ?, NULL, ?)`,
			r.Project, r.Source, r.Channel, r.Permalink, r.SourceRef,
			r.Kind, r.Summary, r.Status, superseded, notified, r.CreatedAt,
		)
		if err != nil {
			t.Fatalf("seedProposals: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seedProposals LastInsertId: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func itoa(i int) string {
	return string(rune('a' + i%26))
}

// supersede marks oldID superseded by newID, the same pair of writes
// SupersedeProposal performs in the MCP store.
func supersede(t *testing.T, dir string, oldID, newID int64) {
	t.Helper()
	db := openFixtureDB(t, dir)
	defer db.Close()
	if _, err := db.Exec(
		`UPDATE proposals SET status = 'superseded', superseded_by = ? WHERE id = ?`,
		newID, oldID,
	); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	var check sql.NullInt64
	if err := db.QueryRow(`SELECT superseded_by FROM proposals WHERE id = ?`, oldID).Scan(&check); err != nil {
		t.Fatalf("supersede verify: %v", err)
	}
	if !check.Valid {
		t.Fatalf("supersede: superseded_by still NULL on %d", oldID)
	}
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// The queue inverts the newest-first house convention on purpose: the
// longest-waiting pending decision is the one most at risk of being dropped.
func TestDashboardProposals_QueueSortPutsOldestPendingFirst(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProposals(t, dir, []proposalFixture{
		{Project: "alpha", Summary: "NEWEST-PENDING", CreatedAt: "2026-08-20T00:00:00Z"},
		{Project: "alpha", Summary: "OLDEST-PENDING", CreatedAt: "2026-08-01T00:00:00Z"},
		{Project: "alpha", Summary: "MIDDLE-PENDING", CreatedAt: "2026-08-10T00:00:00Z"},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	body := getBody(t, ts.URL+"/proposals")
	oldest := strings.Index(body, "OLDEST-PENDING")
	middle := strings.Index(body, "MIDDLE-PENDING")
	newest := strings.Index(body, "NEWEST-PENDING")
	if oldest < 0 || middle < 0 || newest < 0 {
		t.Fatalf("expected all three pending rows rendered, got:\n%s", body)
	}
	if !(oldest < middle && middle < newest) {
		t.Errorf("want oldest-first ordering (oldest=%d < middle=%d < newest=%d)", oldest, middle, newest)
	}
}

// Pending outranks decided rows regardless of age: the queue is grouped
// before it is sorted.
func TestDashboardProposals_PendingGroupSortsAboveDecided(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProposals(t, dir, []proposalFixture{
		{Project: "alpha", Summary: "RECENT-APPROVED", Status: "approved", CreatedAt: "2026-08-25T00:00:00Z"},
		{Project: "alpha", Summary: "OLD-PENDING", CreatedAt: "2026-08-02T00:00:00Z"},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	body := getBody(t, ts.URL+"/proposals?status=")
	pending := strings.Index(body, "OLD-PENDING")
	approved := strings.Index(body, "RECENT-APPROVED")
	if pending < 0 || approved < 0 {
		t.Fatalf("expected both rows rendered, got:\n%s", body)
	}
	if pending > approved {
		t.Errorf("want pending row above approved row (pending=%d, approved=%d)", pending, approved)
	}
}

func TestDashboardProposals_StatusFilter(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProposals(t, dir, []proposalFixture{
		{Project: "alpha", Summary: "ROW-PENDING"},
		{Project: "alpha", Summary: "ROW-APPROVED", Status: "approved"},
		{Project: "alpha", Summary: "ROW-REJECTED", Status: "rejected"},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	cases := []struct {
		query   string
		want    []string
		notWant []string
	}{
		// No status param at all: defaults to the pending queue.
		{"/proposals", []string{"ROW-PENDING"}, []string{"ROW-APPROVED", "ROW-REJECTED"}},
		{"/proposals?status=pending", []string{"ROW-PENDING"}, []string{"ROW-APPROVED"}},
		{"/proposals?status=approved", []string{"ROW-APPROVED"}, []string{"ROW-PENDING", "ROW-REJECTED"}},
		{"/proposals?status=rejected", []string{"ROW-REJECTED"}, []string{"ROW-PENDING", "ROW-APPROVED"}},
		// Explicitly empty: all statuses.
		{"/proposals?status=", []string{"ROW-PENDING", "ROW-APPROVED", "ROW-REJECTED"}, nil},
	}
	for _, c := range cases {
		body := getBody(t, ts.URL+c.query)
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: want body to contain %q", c.query, w)
			}
		}
		for _, n := range c.notWant {
			if strings.Contains(body, n) {
				t.Errorf("%s: want body NOT to contain %q", c.query, n)
			}
		}
	}
}

func TestDashboardProposals_ProjectFilter(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProject(t, dir, "beta", map[string]any{"name": "beta"})
	seedProposals(t, dir, []proposalFixture{
		{Project: "alpha", Summary: "ALPHA-ROW"},
		{Project: "beta", Summary: "BETA-ROW"},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	body := getBody(t, ts.URL+"/proposals?project=alpha")
	if !strings.Contains(body, "ALPHA-ROW") {
		t.Errorf("want ALPHA-ROW in filtered body")
	}
	if strings.Contains(body, "BETA-ROW") {
		t.Errorf("want BETA-ROW excluded when ?project=alpha")
	}
}

// The permalink render states. Two invariants:
//   1. The queue never renders an anchor it cannot honour — an empty href would
//      be a dead exit on the only actionable control on the row.
//   2. Link availability and delivery status are INDEPENDENT facts. Gating the
//      anchor on notified_at hid a working permalink behind a "Not sent" badge,
//      which is the bug this suite now pins against (DOTFILES-39).
func TestDashboardProposals_PermalinkRenderStates(t *testing.T) {
	t.Run("permalink present but never notified renders BOTH the link and Not sent", func(t *testing.T) {
		dir, cleanup := setupFixtureDir(t)
		defer cleanup()
		seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
		seedProposals(t, dir, []proposalFixture{
			{Project: "alpha", Summary: "UNSENT-ROW", Permalink: "https://slack.example/archives/C1/p1", NotifiedAt: nil},
		})
		ts := newTestServer(t, dir)
		defer ts.Close()

		body := getBody(t, ts.URL+"/proposals")
		// The row is actionable and the exit exists in the data — render it.
		if !strings.Contains(body, `href="https://slack.example/archives/C1/p1"`) {
			t.Errorf("want the permalink anchor even when notified_at is NULL, got:\n%s", body)
		}
		if !strings.Contains(body, "Open in Slack") {
			t.Errorf("want the 'Open in Slack' label")
		}
		// Delivery status is still reported, independently of the link.
		if !strings.Contains(body, "Not sent") {
			t.Errorf("want 'Not sent' to remain as a delivery signal")
		}
		if !strings.Contains(body, "Recorded but never delivered to Slack.") {
			t.Errorf("want the explanatory line under 'Not sent'")
		}
	})

	t.Run("empty permalink renders no anchor and no empty href", func(t *testing.T) {
		dir, cleanup := setupFixtureDir(t)
		defer cleanup()
		seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
		seedProposals(t, dir, []proposalFixture{
			{Project: "alpha", Summary: "NOLINK-ROW", Permalink: "", NotifiedAt: strp("2026-08-02T00:00:00Z")},
		})
		ts := newTestServer(t, dir)
		defer ts.Close()

		body := getBody(t, ts.URL+"/proposals")
		if strings.Contains(body, "Open in Slack") {
			t.Errorf("want NO anchor when the permalink is empty, got:\n%s", body)
		}
		if strings.Contains(body, `href=""`) {
			t.Errorf("want NO empty href anywhere in the page")
		}
		// The old 'Sent — open your Slack DMs' branch was unreachable in
		// practice (store.go rejects an empty source_permalink at create time)
		// and has been removed; it must not come back.
		if strings.Contains(body, "open your Slack DMs") {
			t.Errorf("the unreachable 'open your Slack DMs' branch must stay deleted")
		}
	})

	t.Run("notified with permalink renders the link in a new tab", func(t *testing.T) {
		dir, cleanup := setupFixtureDir(t)
		defer cleanup()
		seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
		seedProposals(t, dir, []proposalFixture{
			{Project: "alpha", Summary: "LINKED-ROW", Permalink: "https://slack.example/archives/C1/p2", NotifiedAt: strp("2026-08-02T00:00:00Z")},
		})
		ts := newTestServer(t, dir)
		defer ts.Close()

		body := getBody(t, ts.URL+"/proposals")
		if !strings.Contains(body, `href="https://slack.example/archives/C1/p2"`) {
			t.Errorf("want the permalink anchor, got:\n%s", body)
		}
		// Without target="_blank" the rel attributes are inert and the click
		// navigates the queue away, losing the filter state.
		if !strings.Contains(body, `target="_blank"`) {
			t.Errorf("want target=\"_blank\" so rel=noopener noreferrer is meaningful")
		}
		if !strings.Contains(body, `rel="noopener noreferrer"`) {
			t.Errorf("want rel=noopener noreferrer retained alongside target=_blank")
		}
		// A delivered row has nothing to warn about.
		if strings.Contains(body, "Not sent") {
			t.Errorf("want NO 'Not sent' badge on a delivered row")
		}
	})
}

func TestDashboardProposals_EmptyStates(t *testing.T) {
	t.Run("pending queue empty state", func(t *testing.T) {
		dir, cleanup := setupFixtureDir(t)
		defer cleanup()
		seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
		ts := newTestServer(t, dir)
		defer ts.Close()

		body := getBody(t, ts.URL+"/proposals")
		if !strings.Contains(body, "Nothing awaiting your approval.") {
			t.Errorf("want the pending-queue empty state, got:\n%s", body)
		}
		if strings.Contains(body, "No proposals yet") {
			t.Errorf("want only ONE empty state rendered")
		}
	})

	t.Run("all-statuses empty state", func(t *testing.T) {
		dir, cleanup := setupFixtureDir(t)
		defer cleanup()
		seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
		ts := newTestServer(t, dir)
		defer ts.Close()

		body := getBody(t, ts.URL+"/proposals?status=")
		if !strings.Contains(body, "No proposals yet") || !strings.Contains(body, "the agent will DM you when it has one.") {
			t.Errorf("want the never-had-any empty state, got:\n%s", body)
		}
		if strings.Contains(body, "Nothing awaiting your approval.") {
			t.Errorf("want only ONE empty state rendered")
		}
	})

	// Both empty states must clear the contrast floor: text-zinc-500 on light,
	// text-zinc-400 on dark. dashboard_issues.html's inverted pair
	// (text-zinc-400 dark:text-zinc-500) is ~2.5:1 and fails WCAG 1.4.3.
	t.Run("empty state clears the contrast floor", func(t *testing.T) {
		dir, cleanup := setupFixtureDir(t)
		defer cleanup()
		seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
		ts := newTestServer(t, dir)
		defer ts.Close()

		// Scoped to the table: base.html's sidebar (out of scope for this
		// step, and not table text) uses the inverted pair for its section
		// labels.
		table := tableRegion(t, getBody(t, ts.URL+"/proposals"))
		if strings.Contains(table, "text-zinc-400 dark:text-zinc-500") {
			t.Errorf("proposals table must not use the low-contrast zinc-400/zinc-500 pair:\n%s", table)
		}
		if !strings.Contains(table, "text-zinc-500 dark:text-zinc-400") {
			t.Errorf("want the zinc-500/zinc-400 contrast-floor pair on the empty state:\n%s", table)
		}
	})
}

// One row per revision chain: the superseded revisions are preserved in the
// table and readable in the Slack thread, but listing each as its own queue
// entry would overstate how many decisions are waiting.
func TestDashboardProposals_SupersededRowsHiddenAndHeadShowsRevBadge(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	ids := seedProposals(t, dir, []proposalFixture{
		{Project: "alpha", Summary: "REV-ONE", SourceRef: "ts-1", CreatedAt: "2026-08-01T00:00:00Z"},
		{Project: "alpha", Summary: "REV-TWO", SourceRef: "ts-2", CreatedAt: "2026-08-02T00:00:00Z"},
		{Project: "alpha", Summary: "REV-THREE", SourceRef: "ts-3", CreatedAt: "2026-08-03T00:00:00Z"},
	})
	supersede(t, dir, ids[0], ids[1])
	supersede(t, dir, ids[1], ids[2])

	ts := newTestServer(t, dir)
	defer ts.Close()

	for _, path := range []string{"/proposals", "/proposals?status="} {
		body := getBody(t, ts.URL+path)
		if !strings.Contains(body, "REV-THREE") {
			t.Errorf("%s: want the chain head rendered, got:\n%s", path, body)
		}
		if strings.Contains(body, "REV-ONE") || strings.Contains(body, "REV-TWO") {
			t.Errorf("%s: want superseded revisions absent from the list", path)
		}
		if !strings.Contains(body, "rev 3") {
			t.Errorf("%s: want a static 'rev 3' badge on the chain head", path)
		}
		if !strings.Contains(body, "earlier revisions in the Slack thread") {
			t.Errorf("%s: want the sr-only revision explanation", path)
		}
	}
}

// A first-cut proposal is revision 1 and carries no badge — the badge exists
// to signal "there is history here", so it must not appear when there isn't.
func TestDashboardProposals_FirstRevisionHasNoBadge(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProposals(t, dir, []proposalFixture{{Project: "alpha", Summary: "SOLO-ROW"}})

	ts := newTestServer(t, dir)
	defer ts.Close()

	body := getBody(t, ts.URL+"/proposals")
	if strings.Contains(body, "rev 1") || strings.Contains(body, "earlier revisions in the Slack thread") {
		t.Errorf("want no revision badge on a first-cut proposal, got:\n%s", body)
	}
}

func TestGlobalTabs_ProposalsIsFirstAndRootStaysKanban(t *testing.T) {
	tabs := globalTabs()
	if len(tabs) != 6 {
		t.Fatalf("want 6 global tabs, got %d", len(tabs))
	}
	if tabs[0].Label != "Proposals" || tabs[0].URL != "/proposals" {
		t.Errorf("want Proposals first at /proposals, got %+v", tabs[0])
	}
	if tabs[1].Label != "Kanban" || tabs[1].URL != "/" {
		t.Errorf(`want "/" to still be Kanban, got %+v`, tabs[1])
	}
}

// tableRegion returns just the <table>…</table> slice of a rendered page, so a
// contrast assertion about table text isn't answered by markup elsewhere on
// the page.
func tableRegion(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "<table")
	end := strings.Index(body, "</table>")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("no <table> in rendered page:\n%s", body)
	}
	return body[start : end+len("</table>")]
}

// Every populated row's text must clear the contrast floor too, not just the
// empty state.
func TestDashboardProposals_PopulatedRowsClearContrastFloor(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	seedProject(t, dir, "alpha", map[string]any{"name": "alpha"})
	seedProposals(t, dir, []proposalFixture{
		{Project: "alpha", Summary: "ROW", Permalink: "https://slack.example/x", NotifiedAt: strp("2026-08-02T00:00:00Z")},
	})

	ts := newTestServer(t, dir)
	defer ts.Close()

	table := tableRegion(t, getBody(t, ts.URL+"/proposals"))
	if strings.Contains(table, "text-zinc-400 dark:text-zinc-500") {
		t.Errorf("proposals table row must not use the low-contrast zinc-400/zinc-500 pair:\n%s", table)
	}
}

// relAge exists because dateOnly cannot answer the question this queue asks.
func TestRelAge_DistinguishesMinutesFromHours(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct{ ts, want string }{
		{"2026-09-01T11:50:00Z", "10m ago"},
		{"2026-09-01T06:00:00Z", "6h ago"},
		{"2026-08-31T16:00:00Z", "20h ago"},
		{"2026-08-29T12:00:00Z", "3d ago"},
		{"2026-09-01T11:59:59Z", "just now"},
		{"", ""},
		{"not-a-timestamp", "not-a-timestamp"},
	}
	for _, c := range cases {
		if got := relAgeAt(c.ts, now); got != c.want {
			t.Errorf("relAgeAt(%q): want %q, got %q", c.ts, c.want, got)
		}
	}
	// The whole point: two timestamps dateOnly renders identically must render
	// differently here.
	tenMin, twentyHours := relAgeAt("2026-09-01T11:50:00Z", now), relAgeAt("2026-08-31T16:00:00Z", now)
	if dateOnly("2026-09-01T11:50:00Z") == dateOnly("2026-09-01T11:50:00Z") && tenMin == twentyHours {
		t.Errorf("relAge must distinguish 10 minutes from 20 hours, both gave %q", tenMin)
	}
}
