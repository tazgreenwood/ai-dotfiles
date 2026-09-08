package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *store {
	t.Helper()
	s, err := newStore(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleProposal(summary string) *proposal {
	return &proposal{
		Project:         "private-dotfiles",
		Source:          "slack",
		SourceChannel:   "C0STUART",
		SourcePermalink: "https://example.slack.com/archives/C0STUART/p1756600000000100",
		SourceRef:       "1756600000.000100",
		Kind:            "plan",
		Summary:         summary,
		Payload:         map[string]any{"ticket": "DOTFILES-99", "plan_steps": []any{}},
	}
}

// ── proposals: create + get ──────────────────────────────────────────────────

func TestCreateProposal_GetRoundtrip(t *testing.T) {
	s := newTestStore(t)

	in := sampleProposal("plan a thing")
	id, err := s.CreateProposal(in)
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if id == 0 {
		t.Fatal("want non-zero id")
	}

	got, err := s.GetProposal(id)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.ID != id {
		t.Errorf("id: want %d, got %d", id, got.ID)
	}
	if got.Project != in.Project {
		t.Errorf("project: want %q, got %q", in.Project, got.Project)
	}
	if got.SourceChannel != in.SourceChannel {
		t.Errorf("source_channel: want %q, got %q", in.SourceChannel, got.SourceChannel)
	}
	if got.SourcePermalink != in.SourcePermalink {
		t.Errorf("source_permalink: want %q, got %q", in.SourcePermalink, got.SourcePermalink)
	}
	if got.SourceRef != in.SourceRef {
		t.Errorf("source_ref: want %q, got %q", in.SourceRef, got.SourceRef)
	}
	if got.Kind != "plan" {
		t.Errorf("kind: want plan, got %q", got.Kind)
	}
	if got.Summary != "plan a thing" {
		t.Errorf("summary: want 'plan a thing', got %q", got.Summary)
	}
	if got.Status != "pending" {
		t.Errorf("status: want pending (default), got %q", got.Status)
	}
	if got.CreatedAt == "" {
		t.Error("created_at: want a stamped RFC3339 value, got empty")
	}
	if got.Payload["ticket"] != "DOTFILES-99" {
		t.Errorf("payload not round-tripped: got %v", got.Payload)
	}
}

func TestGetProposal_UnknownID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetProposal(4242); err == nil {
		t.Fatal("want error for unknown proposal id, got nil")
	}
}

func TestCreateProposal_RejectsBadKindAndStatus(t *testing.T) {
	s := newTestStore(t)

	bad := sampleProposal("bad kind")
	bad.Kind = "sudo"
	if _, err := s.CreateProposal(bad); err == nil {
		t.Error("want error for invalid kind, got nil")
	}

	bad2 := sampleProposal("bad status")
	bad2.SourceRef = "1756600000.000999"
	bad2.Status = "shipped"
	if _, err := s.CreateProposal(bad2); err == nil {
		t.Error("want error for invalid status, got nil")
	}
}

// TestCreateProposal_ValidKinds pins the kind enum. "registration" joins the
// original four so Stuart can propose registering an unregistered repo through
// the same approval gate a plan goes through; the enum stays closed so a typo
// is still an error rather than a silently unroutable row.
func TestCreateProposal_ValidKinds(t *testing.T) {
	s := newTestStore(t)

	for i, kind := range []string{"plan", "fix", "review", "improvement", "registration"} {
		p := sampleProposal("kind " + kind)
		p.Kind = kind
		p.SourceRef = fmt.Sprintf("1756600001.0001%02d", i)
		id, err := s.CreateProposal(p)
		if err != nil {
			t.Fatalf("CreateProposal(kind=%q): %v", kind, err)
		}
		got, err := s.GetProposal(id)
		if err != nil {
			t.Fatalf("GetProposal(kind=%q): %v", kind, err)
		}
		if got.Kind != kind {
			t.Errorf("kind: want %q, got %q", kind, got.Kind)
		}
	}
}

// ── proposals: nullable timestamps ───────────────────────────────────────────

func TestCreateProposal_NullableColumnsStayNull(t *testing.T) {
	s := newTestStore(t)

	id, err := s.CreateProposal(sampleProposal("unnotified"))
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}

	// Assert at the SQL layer that these are real SQL NULL, not empty strings.
	var notifiedNull, decidedNull, supersededNull int
	err = s.db.QueryRow(
		`SELECT notified_at IS NULL, decided_at IS NULL, superseded_by IS NULL
		 FROM proposals WHERE id = ?`, id,
	).Scan(&notifiedNull, &decidedNull, &supersededNull)
	if err != nil {
		t.Fatalf("null check query: %v", err)
	}
	if notifiedNull != 1 {
		t.Error("notified_at: want NULL when unset")
	}
	if decidedNull != 1 {
		t.Error("decided_at: want NULL when unset")
	}
	if supersededNull != 1 {
		t.Error("superseded_by: want NULL when unset")
	}

	got, err := s.GetProposal(id)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.NotifiedAt != nil {
		t.Errorf("NotifiedAt: want nil, got %q", *got.NotifiedAt)
	}
	if got.DecidedAt != nil {
		t.Errorf("DecidedAt: want nil, got %q", *got.DecidedAt)
	}
	if got.SupersededBy != nil {
		t.Errorf("SupersededBy: want nil, got %d", *got.SupersededBy)
	}
}

// ── proposals: list ──────────────────────────────────────────────────────────

func TestListProposals_FiltersByStatus(t *testing.T) {
	s := newTestStore(t)

	pendingA := sampleProposal("pending a")
	pendingA.SourceRef = "1756600000.000001"
	idA, err := s.CreateProposal(pendingA)
	if err != nil {
		t.Fatalf("CreateProposal A: %v", err)
	}

	pendingB := sampleProposal("pending b")
	pendingB.SourceRef = "1756600000.000002"
	if _, err := s.CreateProposal(pendingB); err != nil {
		t.Fatalf("CreateProposal B: %v", err)
	}

	otherProject := sampleProposal("other project")
	otherProject.Project = "emily"
	otherProject.SourceRef = "1756600000.000003"
	if _, err := s.CreateProposal(otherProject); err != nil {
		t.Fatalf("CreateProposal C: %v", err)
	}

	if err := s.UpdateProposalStatus(idA, "approved", "lgtm"); err != nil {
		t.Fatalf("UpdateProposalStatus: %v", err)
	}

	all, err := s.ListProposals("private-dotfiles", "")
	if err != nil {
		t.Fatalf("ListProposals all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all for project: want 2, got %d", len(all))
	}

	pending, err := s.ListProposals("private-dotfiles", "pending")
	if err != nil {
		t.Fatalf("ListProposals pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending: want 1, got %d", len(pending))
	}
	if pending[0].Summary != "pending b" {
		t.Errorf("pending: want 'pending b', got %q", pending[0].Summary)
	}

	approved, err := s.ListProposals("private-dotfiles", "approved")
	if err != nil {
		t.Fatalf("ListProposals approved: %v", err)
	}
	if len(approved) != 1 || approved[0].ID != idA {
		t.Fatalf("approved: want [%d], got %+v", idA, approved)
	}
	if approved[0].DecisionNote != "lgtm" {
		t.Errorf("decision_note: want lgtm, got %q", approved[0].DecisionNote)
	}
	if approved[0].DecidedAt == nil {
		t.Error("decided_at: want stamped after a decision, got NULL")
	}
}

// ── proposals: dedup ─────────────────────────────────────────────────────────

func TestCreateProposal_DuplicateSourceRefRejected(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateProposal(sampleProposal("first")); err != nil {
		t.Fatalf("first CreateProposal: %v", err)
	}
	// Same (source, source_ref) — the Slack-message dedup key.
	if _, err := s.CreateProposal(sampleProposal("duplicate")); err == nil {
		t.Fatal("want UNIQUE(source, source_ref) violation, got nil")
	}

	all, err := s.ListProposals("private-dotfiles", "")
	if err != nil {
		t.Fatalf("ListProposals: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 row after rejected duplicate, got %d", len(all))
	}
}

// ── proposals: supersede chain ───────────────────────────────────────────────

func TestSupersedeProposal_SetsBothFieldsAtomically(t *testing.T) {
	s := newTestStore(t)

	oldP := sampleProposal("revision 1")
	oldP.SourceRef = "1756600000.000010"
	oldID, err := s.CreateProposal(oldP)
	if err != nil {
		t.Fatalf("CreateProposal old: %v", err)
	}
	newP := sampleProposal("revision 2")
	newP.SourceRef = "1756600000.000011"
	newID, err := s.CreateProposal(newP)
	if err != nil {
		t.Fatalf("CreateProposal new: %v", err)
	}

	if err := s.SupersedeProposal(oldID, newID, "use sqlite not postgres"); err != nil {
		t.Fatalf("SupersedeProposal: %v", err)
	}

	got, err := s.GetProposal(oldID)
	if err != nil {
		t.Fatalf("GetProposal old: %v", err)
	}
	if got.Status != "superseded" {
		t.Errorf("status: want superseded, got %q", got.Status)
	}
	if got.SupersededBy == nil || *got.SupersededBy != newID {
		t.Errorf("superseded_by: want %d, got %v", newID, got.SupersededBy)
	}
	// The reason for the revision moves in the same transaction as the status.
	if got.DecisionNote != "use sqlite not postgres" {
		t.Errorf("decision_note: want the pushback note, got %q", got.DecisionNote)
	}

	// Both revisions are preserved.
	all, err := s.ListProposals("private-dotfiles", "")
	if err != nil {
		t.Fatalf("ListProposals: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want both revisions preserved, got %d rows", len(all))
	}

	stillPending, err := s.GetProposal(newID)
	if err != nil {
		t.Fatalf("GetProposal new: %v", err)
	}
	if stillPending.Status != "pending" {
		t.Errorf("new revision status: want pending, got %q", stillPending.Status)
	}
}

func TestSupersedeProposal_MidTransactionFailureAppliesNeither(t *testing.T) {
	s := newTestStore(t)

	oldP := sampleProposal("revision 1")
	oldP.SourceRef = "1756600000.000020"
	oldID, err := s.CreateProposal(oldP)
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}

	// Superseding by a non-existent id fails the in-transaction existence check.
	// If the write weren't transactional, status/superseded_by/decision_note
	// could be left mutated.
	if err := s.SupersedeProposal(oldID, 999999, "note that must not land"); err == nil {
		t.Fatal("want error superseding by a non-existent proposal, got nil")
	}

	got, err := s.GetProposal(oldID)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("status: want pending after rollback, got %q", got.Status)
	}
	if got.SupersededBy != nil {
		t.Errorf("superseded_by: want NULL after rollback, got %d", *got.SupersededBy)
	}
}

func TestSupersedeProposal_UnknownOldID(t *testing.T) {
	s := newTestStore(t)

	newID, err := s.CreateProposal(sampleProposal("only revision"))
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if err := s.SupersedeProposal(999999, newID, ""); err == nil {
		t.Fatal("want error for unknown old proposal id, got nil")
	}
}

// A row that already carries a human's decision must not have it rewritten by a
// supersede — approval is the containment of the whole propose-only design.
func TestSupersedeProposal_RefusesAlreadyDecidedRow(t *testing.T) {
	s := newTestStore(t)

	oldP := sampleProposal("revision 1")
	oldP.SourceRef = "1756600000.000030"
	oldID, err := s.CreateProposal(oldP)
	if err != nil {
		t.Fatalf("CreateProposal old: %v", err)
	}
	newP := sampleProposal("revision 2")
	newP.SourceRef = "1756600000.000031"
	newID, err := s.CreateProposal(newP)
	if err != nil {
		t.Fatalf("CreateProposal new: %v", err)
	}
	if err := s.UpdateProposalStatus(oldID, "approved", "lgtm"); err != nil {
		t.Fatalf("UpdateProposalStatus: %v", err)
	}

	if err := s.SupersedeProposal(oldID, newID, "actually change it"); err == nil {
		t.Fatal("want error superseding an approved proposal, got nil")
	}

	got, err := s.GetProposal(oldID)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("status: want approved preserved, got %q", got.Status)
	}
	if got.DecisionNote != "lgtm" {
		t.Errorf("decision_note: want the human's note preserved, got %q", got.DecisionNote)
	}
}

// A caller scoped to one project must not be able to point its revision chain
// at another project's row.
func TestSupersedeProposal_RefusesCrossProjectSuccessor(t *testing.T) {
	s := newTestStore(t)

	oldP := sampleProposal("revision 1")
	oldP.SourceRef = "1756600000.000040"
	oldID, err := s.CreateProposal(oldP)
	if err != nil {
		t.Fatalf("CreateProposal old: %v", err)
	}
	otherP := sampleProposal("other project's proposal")
	otherP.Project = "emily"
	otherP.SourceRef = "1756600000.000041"
	otherID, err := s.CreateProposal(otherP)
	if err != nil {
		t.Fatalf("CreateProposal other: %v", err)
	}

	if err := s.SupersedeProposal(oldID, otherID, "cross-project"); err == nil {
		t.Fatal("want error superseding across projects, got nil")
	}

	got, err := s.GetProposal(oldID)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("status: want pending after refusal, got %q", got.Status)
	}
}

// ── proposals: status update ─────────────────────────────────────────────────

func TestUpdateProposalStatus_RejectsBadInput(t *testing.T) {
	s := newTestStore(t)

	id, err := s.CreateProposal(sampleProposal("to decide"))
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if err := s.UpdateProposalStatus(id, "merged", ""); err == nil {
		t.Error("want error for invalid status, got nil")
	}
	// Superseding goes through SupersedeProposal so the chain pointer is never
	// left dangling.
	if err := s.UpdateProposalStatus(id, "superseded", ""); err == nil {
		t.Error("want error setting superseded via UpdateProposalStatus, got nil")
	}
	if err := s.UpdateProposalStatus(999999, "approved", ""); err == nil {
		t.Error("want error for unknown id, got nil")
	}
}

func TestUpdateProposalStatus_RejectedKeepsNote(t *testing.T) {
	s := newTestStore(t)

	id, err := s.CreateProposal(sampleProposal("to reject"))
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if err := s.UpdateProposalStatus(id, "rejected", "not now"); err != nil {
		t.Fatalf("UpdateProposalStatus: %v", err)
	}
	got, err := s.GetProposal(id)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.Status != "rejected" {
		t.Errorf("status: want rejected, got %q", got.Status)
	}
	if got.DecisionNote != "not now" {
		t.Errorf("decision_note: want 'not now', got %q", got.DecisionNote)
	}
	if got.DecidedAt == nil {
		t.Error("decided_at: want stamped, got NULL")
	}
}

// ── project index (DOTFILES-36) ──────────────────────────────────────────────

// seedProject writes a project plus optional plans. Plans are given newest-last.
func seedProject(t *testing.T, s *store, name, purpose string, plans []map[string]any) {
	t.Helper()
	data := map[string]any{
		"name": name,
		"repo": map[string]any{"workspace": "clearlinkit", "localPath": "/tmp/" + name},
		// A large subtree that must NOT appear in the index payload.
		"resources": map[string]any{"slack": map[string]any{"channel": "C0NOISE"}},
	}
	if purpose != "" {
		data["purpose"] = purpose
	}
	if err := s.SetProject(name, data); err != nil {
		t.Fatalf("SetProject %s: %v", name, err)
	}
	for i, p := range plans {
		ticket, _ := p["ticket"].(string)
		if err := s.WritePlan(name, ticket, p); err != nil {
			t.Fatalf("WritePlan %s #%d: %v", name, i, err)
		}
	}
}

func planWith(ticket, summary string, statuses ...string) map[string]any {
	steps := make([]any, 0, len(statuses))
	for i, st := range statuses {
		steps = append(steps, map[string]any{"id": i + 1, "status": st})
	}
	return map[string]any{"ticket": ticket, "summary": summary, "plan_steps": steps}
}

func indexByName(t *testing.T, rows []projectIndexEntry, name string) projectIndexEntry {
	t.Helper()
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("project %q not present in index", name)
	return projectIndexEntry{}
}

func TestListIndex_OneRowPerProjectWithActivePlan(t *testing.T) {
	s := newTestStore(t)

	seedProject(t, s, "alpha", "Alpha does the alpha thing", []map[string]any{
		planWith("ALPHA-1", "old shipped work", "done", "done"),
		planWith("ALPHA-2", "the live one", "done", "pending"),
	})
	seedProject(t, s, "beta", "Beta does the beta thing", nil)

	rows, err := s.ListIndex()
	if err != nil {
		t.Fatalf("ListIndex: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want one row per project (2), got %d", len(rows))
	}

	alpha := indexByName(t, rows, "alpha")
	if alpha.Purpose != "Alpha does the alpha thing" {
		t.Errorf("purpose: got %q", alpha.Purpose)
	}
	if alpha.LocalPath != "/tmp/alpha" {
		t.Errorf("local_path: got %q", alpha.LocalPath)
	}
	if alpha.ActivePlan == nil {
		t.Fatal("active_plan: want the newest non-shipped plan, got nil")
	}
	if alpha.ActivePlan.Ticket != "ALPHA-2" {
		t.Errorf("active_plan.ticket: want ALPHA-2 (newest non-shipped), got %q", alpha.ActivePlan.Ticket)
	}
	if alpha.ActivePlan.Summary != "the live one" {
		t.Errorf("active_plan.summary: got %q", alpha.ActivePlan.Summary)
	}
}

func TestListIndex_NoPlansAndAllShippedReportNoActivePlan(t *testing.T) {
	s := newTestStore(t)

	seedProject(t, s, "noplans", "has no plans at all", nil)
	seedProject(t, s, "allshipped", "everything is done", []map[string]any{
		planWith("SHIP-1", "done work", "done"),
		planWith("SHIP-2", "also done", "done", "done"),
	})
	// All steps done is necessary but not sufficient: a plan is only actually
	// shipped once /ship has written a matching audit entry.
	if _, err := s.WriteAudit("allshipped", map[string]any{"ticket": "SHIP-1", "type": "feature"}); err != nil {
		t.Fatalf("WriteAudit SHIP-1: %v", err)
	}
	if _, err := s.WriteAudit("allshipped", map[string]any{"ticket": "SHIP-2", "type": "feature"}); err != nil {
		t.Fatalf("WriteAudit SHIP-2: %v", err)
	}

	rows, err := s.ListIndex()
	if err != nil {
		t.Fatalf("ListIndex: %v", err)
	}
	if ap := indexByName(t, rows, "noplans").ActivePlan; ap != nil {
		t.Errorf("noplans: want nil active_plan, got %+v", ap)
	}
	if ap := indexByName(t, rows, "allshipped").ActivePlan; ap != nil {
		t.Errorf("allshipped: want nil active_plan, got %+v", ap)
	}
}

func TestListIndex_MissingPurposeIsEmptyNotError(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "nopurpose", "", nil)

	rows, err := s.ListIndex()
	if err != nil {
		t.Fatalf("ListIndex must not error on a missing purpose: %v", err)
	}
	if got := indexByName(t, rows, "nopurpose").Purpose; got != "" {
		t.Errorf("purpose: want empty string, got %q", got)
	}
}

// The whole point of the index is that it is cheap. A routing decision must not
// drag plan bodies, audit entries or resource subtrees into context.
func TestListIndex_PayloadCarriesNoHeavySubtrees(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "alpha", "Alpha does the alpha thing", []map[string]any{
		planWith("ALPHA-2", "the live one", "pending"),
	})

	rows, err := s.ListIndex()
	if err != nil {
		t.Fatalf("ListIndex: %v", err)
	}
	blob, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"plan_steps", "resources", "C0NOISE"} {
		if strings.Contains(string(blob), forbidden) {
			t.Errorf("index payload leaks %q: %s", forbidden, blob)
		}
	}
}

// ── agent_runs (DOTFILES-38) ─────────────────────────────────────────────────

func TestCreateProposal_SessionSourceAllowsEmptyPermalink(t *testing.T) {
	s := newTestStore(t)

	p := sampleProposal("planned in session")
	p.Source = "session"
	p.SourceChannel = ""
	p.SourcePermalink = ""
	p.SourceRef = "2026-09-01T22:00:00Z"

	id, err := s.CreateProposal(p)
	if err != nil {
		t.Fatalf("session proposal with no permalink must be accepted: %v", err)
	}
	got, err := s.GetProposal(id)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.Source != "session" || got.SourcePermalink != "" {
		t.Errorf("round-trip: source=%q permalink=%q", got.Source, got.SourcePermalink)
	}
}

func TestCreateProposal_SlackSourceStillRequiresPermalink(t *testing.T) {
	s := newTestStore(t)

	noLink := sampleProposal("slack with no permalink")
	noLink.SourceRef = "1756600000.000501"
	noLink.SourcePermalink = ""
	if _, err := s.CreateProposal(noLink); err == nil {
		t.Error("want error: a slack proposal with no permalink is a queue row with no way back")
	}

	noChannel := sampleProposal("slack with no channel")
	noChannel.SourceRef = "1756600000.000502"
	noChannel.SourceChannel = ""
	if _, err := s.CreateProposal(noChannel); err == nil {
		t.Error("want error: a slack proposal with no channel")
	}
}

// Dedup must keep working across sources — two session proposals stamped at the
// same instant are the same request claimed twice.
func TestCreateProposal_SessionSourceStillDedups(t *testing.T) {
	s := newTestStore(t)

	mk := func() *proposal {
		p := sampleProposal("session dup")
		p.Source = "session"
		p.SourceChannel = ""
		p.SourcePermalink = ""
		p.SourceRef = "2026-09-01T22:05:00Z"
		return p
	}
	if _, err := s.CreateProposal(mk()); err != nil {
		t.Fatalf("first session proposal: %v", err)
	}
	if _, err := s.CreateProposal(mk()); err == nil {
		t.Fatal("want UNIQUE(source, source_ref) to still reject a duplicate session proposal")
	}
}

// ── atomic build claim (DOTFILES-35 step 1) ──────────────────────────────────

func TestUpdateStep_ConcurrentUpdatesAllPersist(t *testing.T) {
	s := newTestStore(t)

	const n = 12
	steps := make([]any, n)
	for i := 0; i < n; i++ {
		steps[i] = map[string]any{"id": i + 1, "status": "pending"}
	}
	if err := s.WritePlan("private-dotfiles", "RACE-1", map[string]any{
		"ticket": "RACE-1", "plan_steps": steps,
	}); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start // release together, maximising interleave
			errs[idx] = s.UpdateStep("private-dotfiles", "RACE-1", idx, "done")
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("step %d update errored (a blocked writer must wait, not fail): %v", i, err)
		}
	}
	plan, err := s.GetPlan("private-dotfiles", "RACE-1")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	got := plan["plan_steps"].([]any)
	if len(got) != n {
		t.Fatalf("want %d steps, got %d", n, len(got))
	}
	lost := 0
	for _, raw := range got {
		if raw.(map[string]any)["status"] != "done" {
			lost++
		}
	}
	if lost != 0 {
		t.Errorf("%d of %d concurrent step updates were lost — the blob read-modify-write is not serialized", lost, n)
	}
}

// The step's status was being written into the PLAN's status column: marking
// step 3 in_progress set the whole plan's status. Invisible only because
// nothing reads that column (plan status is derived from step statuses).
func sampleCall(project string, cost float64) *agentCall {
	rid := int64(1)
	return &agentCall{
		RunID: &rid, Project: project, Workflow: "ship-review", AgentLabel: "security",
		Model: "claude-opus-5", Status: "ok", StartedAt: "2026-09-01T10:00:00Z",
		EndedAt: "2026-09-01T10:01:00Z", InputTokens: 1200, OutputTokens: 340,
		CostUSD: cost, Verdict: "GO WITH WARNINGS",
		Trajectory: map[string]any{"tool_calls": []any{"Read", "Grep"}},
	}
}

func TestCreateAgentCall_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAgentCall(sampleCall("private-dotfiles", 0.42))
	if err != nil {
		t.Fatalf("CreateAgentCall: %v", err)
	}
	calls, err := s.ListAgentCalls("private-dotfiles", "", "")
	if err != nil {
		t.Fatalf("ListAgentCalls: %v", err)
	}
	if len(calls) != 1 || calls[0].ID != id {
		t.Fatalf("want the created call back, got %+v", calls)
	}
	c := calls[0]
	if c.CostUSD != 0.42 {
		t.Errorf("cost_usd must survive as REAL, got %v", c.CostUSD)
	}
	if c.Trajectory == nil {
		t.Error("trajectory must round-trip as JSON")
	}
	if c.Verdict != "GO WITH WARNINGS" || c.Model != "claude-opus-5" {
		t.Errorf("verdict/model: %q / %q", c.Verdict, c.Model)
	}
}

// A call outside a /lead build chain (a bare /ship) has no run to belong to.
func TestCreateAgentCall_NullRunIDIsValid(t *testing.T) {
	s := newTestStore(t)
	c := sampleCall("private-dotfiles", 0.1)
	c.RunID = nil
	if _, err := s.CreateAgentCall(c); err != nil {
		t.Fatalf("a call with no run must be valid: %v", err)
	}
	calls, _ := s.ListAgentCalls("private-dotfiles", "", "")
	if len(calls) != 1 || calls[0].RunID != nil {
		t.Errorf("run_id must persist as NULL, got %+v", calls[0].RunID)
	}
}

func TestListAgentCalls_RespectsWindowInclusively(t *testing.T) {
	s := newTestStore(t)
	for _, ts := range []string{"2026-08-30T00:00:00Z", "2026-08-31T00:00:00Z", "2026-09-01T00:00:00Z"} {
		c := sampleCall("private-dotfiles", 0.1)
		c.StartedAt = ts
		if _, err := s.CreateAgentCall(c); err != nil {
			t.Fatalf("CreateAgentCall: %v", err)
		}
	}
	got, err := s.ListAgentCalls("private-dotfiles", "2026-08-31", "2026-08-31")
	if err != nil {
		t.Fatalf("ListAgentCalls: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("window must be inclusive on both ends: want 1, got %d", len(got))
	}
}

// SumCostSince is deliberately GLOBAL: a future daily ceiling is machine-wide,
// not per project.
func TestSumCostSince_IsGlobalAndZeroWhenEmpty(t *testing.T) {
	s := newTestStore(t)
	total, n, err := s.SumCostSince("2026-01-01")
	if err != nil {
		t.Fatalf("SumCostSince on an empty table must not error: %v", err)
	}
	if total != 0 || n != 0 {
		t.Errorf("want 0/0 on empty, got %v/%d", total, n)
	}
	if _, err := s.CreateAgentCall(sampleCall("private-dotfiles", 0.25)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAgentCall(sampleCall("emily", 0.75)); err != nil {
		t.Fatal(err)
	}
	total, n, err = s.SumCostSince("2026-01-01")
	if err != nil {
		t.Fatalf("SumCostSince: %v", err)
	}
	if total != 1.0 || n != 2 {
		t.Errorf("want a cross-project total of 1.0 over 2 calls, got %v over %d", total, n)
	}
}

// TestClaimProposalForBuild_RefusesNonPlanKind pins the kind guard. Adding the
// "registration" kind made approved non-plan proposals routine, and /lead build
// with no id takes the newest approved proposal — which right after a
// registration approval is the registration row, whose payload is not a plan.
// Refusing in the store keeps the guard where the other claim guards live,
// rather than in prompt prose that can drift.
func TestPlanIsShipped_PhaseOverridePrecedence(t *testing.T) {
	doneStep := map[string]any{"status": "done"}
	pendingStep := map[string]any{"status": "pending"}

	tests := []struct {
		name     string
		hasAudit bool
		data     map[string]any
		wantShip bool
	}{
		{
			name:     "override done wins even with no steps and no audit",
			hasAudit: false,
			data: map[string]any{
				"phase_override": "done",
				"plan_steps":     []any{},
			},
			wantShip: true,
		},
		{
			name:     "override done wins even with pending steps and no audit",
			hasAudit: false,
			data: map[string]any{
				"phase_override": "done",
				"plan_steps":     []any{pendingStep},
			},
			wantShip: true,
		},
		{
			name:     "override pr_ready wins even with all steps done and audit present",
			hasAudit: true,
			data: map[string]any{
				"phase_override": "pr_ready",
				"plan_steps":     []any{doneStep, doneStep},
			},
			wantShip: false,
		},
		{
			name:     "override pending wins over done steps and audit",
			hasAudit: true,
			data: map[string]any{
				"phase_override": "pending",
				"plan_steps":     []any{doneStep},
			},
			wantShip: false,
		},
		{
			name:     "empty override falls back to steps+audit logic (all done, audit present -> shipped)",
			hasAudit: true,
			data: map[string]any{
				"phase_override": "",
				"plan_steps":     []any{doneStep, doneStep},
			},
			wantShip: true,
		},
		{
			name:     "empty override falls back to steps+audit logic (pending step -> not shipped)",
			hasAudit: true,
			data: map[string]any{
				"phase_override": "",
				"plan_steps":     []any{doneStep, pendingStep},
			},
			wantShip: false,
		},
		{
			name:     "absent override falls back to steps+audit logic (all done, no audit -> not shipped)",
			hasAudit: false,
			data: map[string]any{
				"plan_steps": []any{doneStep, doneStep},
			},
			wantShip: false,
		},
		{
			name:     "absent override falls back to steps+audit logic (all done, audit present -> shipped)",
			hasAudit: true,
			data: map[string]any{
				"plan_steps": []any{doneStep, doneStep},
			},
			wantShip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planIsShipped(tt.hasAudit, tt.data)
			if got != tt.wantShip {
				t.Errorf("planIsShipped(hasAudit=%v, data=%+v) = %v, want %v", tt.hasAudit, tt.data, got, tt.wantShip)
			}
		})
	}
}
