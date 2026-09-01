package main

import (
	"path/filepath"
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
