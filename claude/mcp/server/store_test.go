package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
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

func sampleRun() *agentRun {
	pid := int64(7)
	return &agentRun{
		Project:    "private-dotfiles",
		ProposalID: &pid,
		Ticket:     "DOTFILES-99",
		Phase:      "planning",
		Status:     "running",
		Cursor:     map[string]any{"branch": "feat/x", "last_step": float64(0)},
	}
}

func TestCreateRun_GetRoundtrip(t *testing.T) {
	s := newTestStore(t)

	in := sampleRun()
	id, err := s.CreateRun(in)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if id == 0 {
		t.Fatal("want non-zero id")
	}

	got, err := s.GetRun(id)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Project != "private-dotfiles" || got.Ticket != "DOTFILES-99" {
		t.Errorf("project/ticket: got %q/%q", got.Project, got.Ticket)
	}
	if got.ProposalID == nil || *got.ProposalID != 7 {
		t.Errorf("proposal_id: want 7, got %v", got.ProposalID)
	}
	if got.Phase != "planning" || got.Status != "running" {
		t.Errorf("phase/status: got %q/%q", got.Phase, got.Status)
	}
	if got.Cursor["branch"] != "feat/x" {
		t.Errorf("cursor did not round-trip: %v", got.Cursor)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Error("want created_at and updated_at stamped")
	}
}

// A hand-started run (not from a proposal) is legitimate.
func TestCreateRun_WithoutProposalID(t *testing.T) {
	s := newTestStore(t)
	r := sampleRun()
	r.ProposalID = nil
	r.Ticket = ""
	id, err := s.CreateRun(r)
	if err != nil {
		t.Fatalf("CreateRun without proposal: %v", err)
	}
	got, err := s.GetRun(id)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.ProposalID != nil {
		t.Errorf("proposal_id: want NULL, got %d", *got.ProposalID)
	}
}

func TestCreateRun_RejectsBadPhaseAndStatus(t *testing.T) {
	s := newTestStore(t)

	bad := sampleRun()
	bad.Phase = "hammering"
	if _, err := s.CreateRun(bad); err == nil {
		t.Error("want error for invalid phase, got nil")
	}

	bad2 := sampleRun()
	bad2.Status = "vibing"
	if _, err := s.CreateRun(bad2); err == nil {
		t.Error("want error for invalid status, got nil")
	}
}

func TestGetRun_UnknownID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetRun(4242); err == nil {
		t.Fatal("want error for unknown run id, got nil")
	}
}

// UpdateRun is the resume spine: it must advance phase, status, cursor and note
// in ONE statement and restamp updated_at. Deliberately not the UpdateStep
// read-modify-write shape.
func TestUpdateRun_AdvancesCursorAtomically(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateRun(sampleRun())
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	before, err := s.GetRun(id)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	newCursor := map[string]any{"branch": "feat/x", "last_step": float64(3), "pr_url": ""}
	if err := s.UpdateRun(id, "building", "running", newCursor, "step 3 done", "DOTFILES-77"); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	got, err := s.GetRun(id)
	if err != nil {
		t.Fatalf("GetRun after update: %v", err)
	}
	if got.Phase != "building" || got.Status != "running" {
		t.Errorf("phase/status: got %q/%q", got.Phase, got.Status)
	}
	if got.Cursor["last_step"] != float64(3) {
		t.Errorf("cursor.last_step: want 3, got %v", got.Cursor["last_step"])
	}
	if got.Note != "step 3 done" {
		t.Errorf("note: got %q", got.Note)
	}
	if got.CreatedAt != before.CreatedAt {
		t.Error("created_at must not move on update")
	}
	if got.UpdatedAt == "" {
		t.Error("updated_at must be restamped")
	}
}

// The run opens before the ticket key exists, so the ticket can only arrive on
// a later advance. The first real /lead build finished with an empty ticket
// column because UpdateRun had no way to set it.
func TestUpdateRun_SetsTicketLaterAndPreservesItWhenOmitted(t *testing.T) {
	s := newTestStore(t)
	r := sampleRun()
	r.Ticket = "" // opened before allocation, as /lead build STEP 8b does
	id, err := s.CreateRun(r)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if err := s.UpdateRun(id, "building", "running", nil, "plan written", "DOTFILES-42"); err != nil {
		t.Fatalf("UpdateRun with ticket: %v", err)
	}
	got, err := s.GetRun(id)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Ticket != "DOTFILES-42" {
		t.Fatalf("ticket: want DOTFILES-42 set on advance, got %q", got.Ticket)
	}

	// A later advance that omits the ticket must not blank it.
	if err := s.UpdateRun(id, "shipping", "running", nil, "entering ship", ""); err != nil {
		t.Fatalf("UpdateRun without ticket: %v", err)
	}
	got, err = s.GetRun(id)
	if err != nil {
		t.Fatalf("GetRun after second advance: %v", err)
	}
	if got.Ticket != "DOTFILES-42" {
		t.Errorf("ticket must survive an advance that omits it, got %q", got.Ticket)
	}
}

func TestUpdateRun_RejectsBadInputAndUnknownID(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateRun(sampleRun())
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := s.UpdateRun(id, "hammering", "running", nil, "", ""); err == nil {
		t.Error("want error for invalid phase")
	}
	if err := s.UpdateRun(id, "building", "vibing", nil, "", ""); err == nil {
		t.Error("want error for invalid status")
	}
	if err := s.UpdateRun(999999, "building", "running", nil, "", ""); err == nil {
		t.Error("want error for unknown run id")
	}
}

func TestListRuns_FiltersByStatusAndProject(t *testing.T) {
	s := newTestStore(t)

	running, err := s.CreateRun(sampleRun())
	if err != nil {
		t.Fatalf("CreateRun running: %v", err)
	}
	paused := sampleRun()
	pausedID, err := s.CreateRun(paused)
	if err != nil {
		t.Fatalf("CreateRun paused: %v", err)
	}
	if err := s.UpdateRun(pausedID, "building", "paused", nil, "needs an answer", ""); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	other := sampleRun()
	other.Project = "emily"
	if _, err := s.CreateRun(other); err != nil {
		t.Fatalf("CreateRun other: %v", err)
	}

	all, err := s.ListRuns("private-dotfiles", "")
	if err != nil {
		t.Fatalf("ListRuns all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all for project: want 2, got %d", len(all))
	}

	onlyPaused, err := s.ListRuns("private-dotfiles", "paused")
	if err != nil {
		t.Fatalf("ListRuns paused: %v", err)
	}
	if len(onlyPaused) != 1 || onlyPaused[0].ID != pausedID {
		t.Fatalf("paused: want [%d], got %+v", pausedID, onlyPaused)
	}
	if onlyPaused[0].Note != "needs an answer" {
		t.Errorf("note: got %q", onlyPaused[0].Note)
	}

	onlyRunning, err := s.ListRuns("private-dotfiles", "running")
	if err != nil {
		t.Fatalf("ListRuns running: %v", err)
	}
	if len(onlyRunning) != 1 || onlyRunning[0].ID != running {
		t.Fatalf("running: want [%d], got %+v", running, onlyRunning)
	}

	if _, err := s.ListRuns("private-dotfiles", "bogus"); err == nil {
		t.Error("want error for invalid status filter")
	}
}

// The double-build guard depends on finding an existing run for a proposal.
func TestListRuns_FindsRunByProposal(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRun(sampleRun()); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	runs, err := s.ListRuns("private-dotfiles", "")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	found := false
	for _, r := range runs {
		if r.ProposalID != nil && *r.ProposalID == 7 {
			found = true
		}
	}
	if !found {
		t.Error("want a run discoverable by its proposal_id — the double-build guard depends on it")
	}
}

// ── session-sourced proposals (DOTFILES-38 step 5) ───────────────────────────

// An in-session proposal has no originating Slack message, so it has no
// permalink. The NOT-EMPTY check must relax for source='session' ONLY —
// widening it to slack would reintroduce queue rows with no way back to the
// conversation, which is the reason the check exists.
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

func approvedProposal(t *testing.T, s *store, sourceRef string) int64 {
	t.Helper()
	p := sampleProposal("claimable")
	p.SourceRef = sourceRef
	id, err := s.CreateProposal(p)
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if err := s.UpdateProposalStatus(id, "approved", "approve"); err != nil {
		t.Fatalf("UpdateProposalStatus: %v", err)
	}
	return id
}

func TestClaimProposalForBuild_HappyPathOpensARun(t *testing.T) {
	s := newTestStore(t)
	pid := approvedProposal(t, s, "1756600000.000700")

	runID, err := s.ClaimProposalForBuild("private-dotfiles", pid)
	if err != nil {
		t.Fatalf("ClaimProposalForBuild: %v", err)
	}
	r, err := s.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if r.Phase != "planning" || r.Status != "running" {
		t.Errorf("want a fresh planning/running run, got %s/%s", r.Phase, r.Status)
	}
	if r.ProposalID == nil || *r.ProposalID != pid {
		t.Errorf("run must carry the proposal id, got %v", r.ProposalID)
	}
}

func TestClaimProposalForBuild_RefusesNonApproved(t *testing.T) {
	s := newTestStore(t)
	for i, status := range []string{"pending", "rejected"} {
		p := sampleProposal("not approved")
		p.SourceRef = "1756600000.00071" + string(rune('0'+i))
		id, err := s.CreateProposal(p)
		if err != nil {
			t.Fatalf("CreateProposal: %v", err)
		}
		if status != "pending" {
			if err := s.UpdateProposalStatus(id, status, "no"); err != nil {
				t.Fatalf("UpdateProposalStatus: %v", err)
			}
		}
		_, err = s.ClaimProposalForBuild("private-dotfiles", id)
		if err == nil {
			t.Errorf("%s proposal must not be claimable", status)
			continue
		}
		if !strings.Contains(err.Error(), status) {
			t.Errorf("refusal must name the blocking status %q, got: %v", status, err)
		}
	}
}

func TestClaimProposalForBuild_RefusesWhenARunAlreadyExists(t *testing.T) {
	s := newTestStore(t)
	pid := approvedProposal(t, s, "1756600000.000720")

	if _, err := s.ClaimProposalForBuild("private-dotfiles", pid); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := s.ClaimProposalForBuild("private-dotfiles", pid); err == nil {
		t.Fatal("second claim must refuse — the human wants --resume, not a second build")
	}
}

// The pre-agent_runs case: DOTFILES-36 was converted by hand, so proposal 2 has
// a plan and no run. The run check alone would miss it.
func TestClaimProposalForBuild_RefusesWhenAPlanAlreadyCarriesIt(t *testing.T) {
	s := newTestStore(t)
	pid := approvedProposal(t, s, "1756600000.000730")

	if err := s.WritePlan("private-dotfiles", "DOTFILES-LEGACY", map[string]any{
		"ticket": "DOTFILES-LEGACY", "from_proposal": pid, "plan_steps": []any{},
	}); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	if _, err := s.ClaimProposalForBuild("private-dotfiles", pid); err == nil {
		t.Fatal("must refuse a proposal a plan already carries, even with no run")
	}
}

func TestClaimProposalForBuild_RefusesCrossProjectAndUnknown(t *testing.T) {
	s := newTestStore(t)
	pid := approvedProposal(t, s, "1756600000.000740")

	if _, err := s.ClaimProposalForBuild("emily", pid); err == nil {
		t.Error("a caller scoped to another project must not claim this proposal")
	}
	if _, err := s.ClaimProposalForBuild("private-dotfiles", 999999); err == nil {
		t.Error("must refuse an unknown proposal id")
	}
}

// The race the separate check-then-create sequence allowed: two concurrent
// claims both passed their checks and both built.
func TestClaimProposalForBuild_ConcurrentClaimsProduceExactlyOneRun(t *testing.T) {
	s := newTestStore(t)
	pid := approvedProposal(t, s, "1756600000.000750")

	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won []int64
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if runID, err := s.ClaimProposalForBuild("private-dotfiles", pid); err == nil {
				mu.Lock()
				won = append(won, runID)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(won) != 1 {
		t.Fatalf("exactly one claim must win, got %d: %v", len(won), won)
	}
	runs, err := s.ListRuns("private-dotfiles", "")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want exactly 1 run row after %d concurrent claims, got %d", n, len(runs))
	}
}

// ── UpdateStep concurrency (DOTFILES-35 step 2) ──────────────────────────────

// build-workflow.js runs async steps concurrently and each spawned subagent
// calls registry_update_step independently. UpdateStep SELECTs the whole plan
// blob, mutates it in Go, and UPDATEs it back — so without serialization the
// last writer wins on the ENTIRE blob and other steps' statuses vanish.
//
// Demonstrated failing against the pre-fix code on 2026-09-01: 9 of 12 statuses
// lost and 7 of 12 calls errored outright (no busy_timeout, so blocked writers
// error instead of waiting). A test that passes against the broken code proves
// nothing, so this one was verified to reproduce before the fix landed.
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
func TestUpdateStep_DoesNotWriteStepStatusIntoPlanColumn(t *testing.T) {
	s := newTestStore(t)
	if err := s.WritePlan("private-dotfiles", "COL-1", map[string]any{
		"ticket": "COL-1", "plan_steps": []any{map[string]any{"id": 1, "status": "pending"}},
	}); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	if err := s.UpdateStep("private-dotfiles", "COL-1", 0, "in_progress"); err != nil {
		t.Fatalf("UpdateStep: %v", err)
	}
	var col string
	if err := s.db.QueryRow(
		`SELECT COALESCE(status, '') FROM plans WHERE project = ? AND ticket = ?`,
		"private-dotfiles", "COL-1",
	).Scan(&col); err != nil {
		t.Fatalf("status query: %v", err)
	}
	if col == "in_progress" {
		t.Errorf("plans.status was set to the STEP's status %q — a persisted lie", col)
	}
}

// ── agent_calls (DOTFILES-35 step 3) ─────────────────────────────────────────

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
func TestClaimProposalForBuild_RefusesNonPlanKind(t *testing.T) {
	s := newTestStore(t)

	p := sampleProposal("register some-repo")
	p.Kind = "registration"
	p.SourceRef = "1756600009.000901"
	id, err := s.CreateProposal(p)
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if err := s.UpdateProposalStatus(id, "approved", "approve"); err != nil {
		t.Fatalf("UpdateProposalStatus: %v", err)
	}

	if _, err := s.ClaimProposalForBuild(p.Project, id); err == nil {
		t.Fatal("claim succeeded for kind=registration; want refusal")
	} else if !strings.Contains(err.Error(), "not \"plan\"") {
		t.Errorf("error should name the kind guard, got: %v", err)
	}

	// A plan proposal in the same state still claims cleanly.
	q := sampleProposal("do the work")
	q.Kind = "plan"
	q.SourceRef = "1756600009.000902"
	qid, err := s.CreateProposal(q)
	if err != nil {
		t.Fatalf("CreateProposal(plan): %v", err)
	}
	if err := s.UpdateProposalStatus(qid, "approved", "approve"); err != nil {
		t.Fatalf("UpdateProposalStatus(plan): %v", err)
	}
	if _, err := s.ClaimProposalForBuild(q.Project, qid); err != nil {
		t.Fatalf("claim refused a plan proposal: %v", err)
	}
}

// ── inbox (DOTFILES-40) ──────────────────────────────────────────────────────
//
// The inbox is the capture half of the Stuart sweep, split off from proposals
// on purpose: capture happens BEFORE routing, so `project` is nullable and no
// LLM is involved in writing a row. These tests pin the three things callers
// depend on — required source identity, the (source, source_ref) dedup key that
// is the sweep's cursor, and an update that leaves omitted fields alone (a
// triage pass that blanked `project` would make the next sweep re-route a row
// it had already routed).

func sampleInbox(ref string) *inboxItem {
	return &inboxItem{
		Source:          "slack",
		SourceChannel:   "C0STUART",
		SourcePermalink: "https://example.slack.com/archives/C0STUART/p" + strings.ReplaceAll(ref, ".", ""),
		SourceRef:       ref,
		RawText:         "can you look at the flaky deploy check",
	}
}

func TestCreateInboxRequiresSourceAndRef(t *testing.T) {
	s := newTestStore(t)

	noSource := sampleInbox("1756700000.000100")
	noSource.Source = ""
	if _, err := s.CreateInbox(noSource); err == nil {
		t.Error("want error when source is empty, got nil")
	}

	noRef := sampleInbox("1756700000.000101")
	noRef.SourceRef = ""
	if _, err := s.CreateInbox(noRef); err == nil {
		t.Error("want error when source_ref is empty, got nil")
	}

	// A row with both still writes — the guard is on identity, not on project.
	if _, err := s.CreateInbox(sampleInbox("1756700000.000102")); err != nil {
		t.Fatalf("CreateInbox with source+source_ref: %v", err)
	}
}

// TestCreateInboxAllowsNullProject is the capture-before-routing case: a Slack
// message is captured with no idea yet which project it belongs to. The row
// must be writable and readable with project absent, not defaulted to the cwd
// project — a defaulted project is an invisible mis-route.
func TestCreateInboxAllowsNullProject(t *testing.T) {
	s := newTestStore(t)

	in := sampleInbox("1756700001.000100")
	if in.Project != nil {
		t.Fatal("sampleInbox should start with no project")
	}
	id, err := s.CreateInbox(in)
	if err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}
	if id == 0 {
		t.Fatal("want non-zero id")
	}

	got, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if got.Project != nil {
		t.Errorf("project: want absent (NULL), got %q", *got.Project)
	}
	if got.SourceRef != in.SourceRef {
		t.Errorf("source_ref: want %q, got %q", in.SourceRef, got.SourceRef)
	}
	if got.RawText != in.RawText {
		t.Errorf("raw_text: want %q, got %q", in.RawText, got.RawText)
	}
	if got.Status != "new" {
		t.Errorf("status: want new (default), got %q", got.Status)
	}
	if got.CreatedAt == "" {
		t.Error("created_at: want a stamped RFC3339 value, got empty")
	}
}

func TestCreateInboxRejectsInvalidStatus(t *testing.T) {
	s := newTestStore(t)

	bad := sampleInbox("1756700002.000100")
	bad.Status = "pending"
	if _, err := s.CreateInbox(bad); err == nil {
		t.Error("want error for status outside new|triaged|routed|closed, got nil")
	}

	for i, status := range []string{"new", "triaged", "routed", "closed"} {
		ok := sampleInbox(fmt.Sprintf("1756700002.0002%02d", i))
		ok.Status = status
		id, err := s.CreateInbox(ok)
		if err != nil {
			t.Fatalf("CreateInbox(status=%q): %v", status, err)
		}
		got, err := s.GetInbox(id)
		if err != nil {
			t.Fatalf("GetInbox(status=%q): %v", status, err)
		}
		if got.Status != status {
			t.Errorf("status: want %q, got %q", status, got.Status)
		}
	}
}

// TestCreateInboxDuplicateSourceRefErrors pins the dedup cursor. The sweep
// re-reads the same channel window every run, so a re-seen message must come
// back as a clean "already exists" error the caller treats as "already seen" —
// not a raw sqlite constraint string and not a second row.
func TestCreateInboxDuplicateSourceRefErrors(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateInbox(sampleInbox("1756700003.000100")); err != nil {
		t.Fatalf("first CreateInbox: %v", err)
	}

	_, err := s.CreateInbox(sampleInbox("1756700003.000100"))
	if err == nil {
		t.Fatal("want UNIQUE(source, source_ref) violation, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should say 'already exists' so callers can match it, got: %v", err)
	}
	if !strings.Contains(err.Error(), "1756700003.000100") {
		t.Errorf("error should name the source_ref, got: %v", err)
	}

	all, err := s.ListInbox("")
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 row after rejected duplicate, got %d", len(all))
	}
}

// TestListInboxFiltersByStatus — the sweep asks for open rows only, and it asks
// across every project (including rows with no project yet), so the listing is
// deliberately not project-scoped.
func TestListInboxFiltersByStatus(t *testing.T) {
	s := newTestStore(t)

	newRow := sampleInbox("1756700004.000100")
	newID, err := s.CreateInbox(newRow)
	if err != nil {
		t.Fatalf("CreateInbox new: %v", err)
	}

	routedRow := sampleInbox("1756700004.000101")
	routedRow.Status = "routed"
	project := "private-dotfiles"
	routedRow.Project = &project
	if _, err := s.CreateInbox(routedRow); err != nil {
		t.Fatalf("CreateInbox routed: %v", err)
	}

	closedRow := sampleInbox("1756700004.000102")
	closedRow.Status = "closed"
	other := "emily"
	closedRow.Project = &other
	if _, err := s.CreateInbox(closedRow); err != nil {
		t.Fatalf("CreateInbox closed: %v", err)
	}

	all, err := s.ListInbox("")
	if err != nil {
		t.Fatalf("ListInbox all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 rows unfiltered across projects, got %d", len(all))
	}

	newOnly, err := s.ListInbox("new")
	if err != nil {
		t.Fatalf("ListInbox new: %v", err)
	}
	if len(newOnly) != 1 {
		t.Fatalf("want 1 new row, got %d", len(newOnly))
	}
	if newOnly[0].ID != newID {
		t.Errorf("want the new row (id %d), got id %d", newID, newOnly[0].ID)
	}

	routed, err := s.ListInbox("routed")
	if err != nil {
		t.Fatalf("ListInbox routed: %v", err)
	}
	if len(routed) != 1 {
		t.Fatalf("want 1 routed row, got %d", len(routed))
	}

	if _, err := s.ListInbox("nonsense"); err == nil {
		t.Error("want error for an invalid status filter, got nil")
	}
}

// TestUpdateInboxOmittedFieldsUnchanged is the non-clobbering guarantee, the
// same one registry_update_run carries: a status-only advance must leave
// triage, project, note and proposal_id exactly as they were. Blanking them
// would strand a routed row — the next sweep would see no project and re-plan
// work that already has a proposal.
func TestUpdateInboxOmittedFieldsUnchanged(t *testing.T) {
	s := newTestStore(t)

	in := sampleInbox("1756700005.000100")
	project := "private-dotfiles"
	in.Project = &project
	in.Triage = "plan"
	in.Note = "matched by purpose line, not by name"
	id, err := s.CreateInbox(in)
	if err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	proposalID := int64(7)
	if err := s.UpdateInbox(id, "triaged", "", "", "", &proposalID); err != nil {
		t.Fatalf("UpdateInbox (attach proposal): %v", err)
	}

	// Status-only advance: everything else must survive.
	if err := s.UpdateInbox(id, "routed", "", "", "", nil); err != nil {
		t.Fatalf("UpdateInbox (status only): %v", err)
	}

	got, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if got.Status != "routed" {
		t.Errorf("status: want routed, got %q", got.Status)
	}
	if got.Triage != "plan" {
		t.Errorf("triage clobbered: want plan, got %q", got.Triage)
	}
	if got.Project == nil || *got.Project != project {
		t.Errorf("project clobbered: want %q, got %v", project, got.Project)
	}
	if got.Note != in.Note {
		t.Errorf("note clobbered: want %q, got %q", in.Note, got.Note)
	}
	if got.ProposalID == nil || *got.ProposalID != proposalID {
		t.Errorf("proposal_id clobbered: want %d, got %v", proposalID, got.ProposalID)
	}
	if got.UpdatedAt == "" {
		t.Error("updated_at: want a stamped value after an update, got empty")
	}

	if err := s.UpdateInbox(999999, "closed", "", "", "", nil); err == nil {
		t.Error("want error updating an unknown inbox id, got nil")
	}
	if err := s.UpdateInbox(id, "nonsense", "", "", "", nil); err == nil {
		t.Error("want error for an invalid status, got nil")
	}
}

// TestUpdateInboxSetsProjectLater is the whole reason project is nullable:
// capture writes the row with no project, and triage fills it in on a later
// pass once routing has run.
func TestUpdateInboxSetsProjectLater(t *testing.T) {
	s := newTestStore(t)

	id, err := s.CreateInbox(sampleInbox("1756700006.000100"))
	if err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}
	before, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox before: %v", err)
	}
	if before.Project != nil {
		t.Fatalf("want NULL project at capture, got %q", *before.Project)
	}

	if err := s.UpdateInbox(id, "triaged", "investigate", "private-dotfiles", "read-only, runs now", nil); err != nil {
		t.Fatalf("UpdateInbox: %v", err)
	}

	got, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox after: %v", err)
	}
	if got.Project == nil || *got.Project != "private-dotfiles" {
		t.Errorf("project: want private-dotfiles after routing, got %v", got.Project)
	}
	if got.Triage != "investigate" {
		t.Errorf("triage: want investigate, got %q", got.Triage)
	}
	if got.Status != "triaged" {
		t.Errorf("status: want triaged, got %q", got.Status)
	}
	if got.Note != "read-only, runs now" {
		t.Errorf("note: want the triage note, got %q", got.Note)
	}
}

// ── worklist (DOTFILES-40) ───────────────────────────────────────────────────
//
// ListWorklist is the cross-project sibling of ListIndex: "what is in flight
// everywhere, and what is each thing waiting on". It answers from three tables
// — open inbox rows, pending proposals, running/paused runs — and its entire
// value is being cheap enough to call on every sweep, so these tests pin the
// query count as hard as they pin the contents.

// worklistOfKind returns just the rows of one kind, so an assertion about
// proposals is not perturbed by inbox or run rows.
func worklistOfKind(rows []worklistItem, kind string) []worklistItem {
	var out []worklistItem
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func worklistHas(rows []worklistItem, kind, project string, id int64) bool {
	for _, r := range rows {
		if r.Kind == kind && r.Project == project && r.ID == id {
			return true
		}
	}
	return false
}

// seedWorklistProject gives one project the full triple: an open inbox row, a
// pending proposal and a paused run. refSeed keeps (source, source_ref) unique
// across projects, since that UNIQUE index is global, not per-project.
func seedWorklistProject(t *testing.T, s *store, project, refSeed string) (inboxID, proposalID, runID int64) {
	t.Helper()

	seedProject(t, s, project, project+" does a thing", []map[string]any{
		planWith(strings.ToUpper(project)+"-1", "the live one", "pending"),
	})

	it := sampleInbox(refSeed + ".000100")
	it.Project = &project
	it.RawText = "open ask for " + project
	inboxID, err := s.CreateInbox(it)
	if err != nil {
		t.Fatalf("CreateInbox %s: %v", project, err)
	}

	p := sampleProposal("pending proposal for " + project)
	p.Project = project
	p.SourceRef = refSeed + ".000200"
	p.SourcePermalink = "https://example.slack.com/archives/C0STUART/p" + refSeed + "000200"
	proposalID, err = s.CreateProposal(p)
	if err != nil {
		t.Fatalf("CreateProposal %s: %v", project, err)
	}

	r := sampleRun()
	r.Project = project
	r.ProposalID = &proposalID
	r.Status = "paused"
	r.Phase = "building"
	r.Note = "waiting on an answer for " + project
	runID, err = s.CreateRun(r)
	if err != nil {
		t.Fatalf("CreateRun %s: %v", project, err)
	}
	return inboxID, proposalID, runID
}

// TestListWorklistSpansProjects — the worklist is deliberately NOT
// project-scoped: the point is one call that shows everything in flight.
func TestListWorklistSpansProjects(t *testing.T) {
	s := newTestStore(t)

	aInbox, aProposal, aRun := seedWorklistProject(t, s, "alpha", "1756800001")
	bInbox, bProposal, bRun := seedWorklistProject(t, s, "beta", "1756800002")

	rows, err := s.ListWorklist()
	if err != nil {
		t.Fatalf("ListWorklist: %v", err)
	}

	want := []struct {
		kind    string
		project string
		id      int64
	}{
		{"inbox", "alpha", aInbox}, {"proposal", "alpha", aProposal}, {"run", "alpha", aRun},
		{"inbox", "beta", bInbox}, {"proposal", "beta", bProposal}, {"run", "beta", bRun},
	}
	for _, w := range want {
		if !worklistHas(rows, w.kind, w.project, w.id) {
			t.Errorf("worklist missing %s %d for project %s; got %+v", w.kind, w.id, w.project, rows)
		}
	}
	if len(rows) != len(want) {
		t.Errorf("want exactly %d rows, got %d: %+v", len(want), len(rows), rows)
	}

	// waiting_on is the column that makes the view actionable — a row with no
	// stated blocker is a row the human has to open something else to read.
	for _, r := range rows {
		if r.WaitingOn == "" {
			t.Errorf("%s %d has an empty waiting_on", r.Kind, r.ID)
		}
	}
}

// TestListWorklistExcludesClosedAndDecided — a worklist that shows finished
// work is noise, and noise is what stops it being read every sweep.
func TestListWorklistExcludesClosedAndDecided(t *testing.T) {
	s := newTestStore(t)
	project := "alpha"
	seedProject(t, s, project, "alpha does a thing", nil)

	// Open vs closed inbox rows. new/triaged/routed are open; closed is not.
	var openInbox []int64
	for i, status := range []string{"new", "triaged", "routed"} {
		it := sampleInbox(fmt.Sprintf("1756810000.0001%02d", i))
		it.Project = &project
		it.Status = status
		if status == "triaged" || status == "routed" {
			it.Triage = "plan"
		}
		id, err := s.CreateInbox(it)
		if err != nil {
			t.Fatalf("CreateInbox(%s): %v", status, err)
		}
		openInbox = append(openInbox, id)
	}
	closedRow := sampleInbox("1756810000.000199")
	closedRow.Project = &project
	closedRow.Status = "closed"
	closedID, err := s.CreateInbox(closedRow)
	if err != nil {
		t.Fatalf("CreateInbox(closed): %v", err)
	}

	// Pending proposal stays; approved and rejected are decided; superseded is
	// history. Only the pending one is work awaiting a human.
	newProposal := func(ref, summary string) int64 {
		p := sampleProposal(summary)
		p.Project = project
		p.SourceRef = ref
		p.SourcePermalink = "https://example.slack.com/archives/C0STUART/p" + strings.ReplaceAll(ref, ".", "")
		id, err := s.CreateProposal(p)
		if err != nil {
			t.Fatalf("CreateProposal(%s): %v", summary, err)
		}
		return id
	}
	pendingID := newProposal("1756810001.000100", "still pending")
	approvedID := newProposal("1756810001.000200", "approved")
	rejectedID := newProposal("1756810001.000300", "rejected")
	oldID := newProposal("1756810001.000400", "superseded")
	successorID := newProposal("1756810001.000500", "the successor")
	if err := s.UpdateProposalStatus(approvedID, "approved", ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := s.UpdateProposalStatus(rejectedID, "rejected", "no thanks"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := s.SupersedeProposal(oldID, successorID, "revised"); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	// running/paused runs stay; done/failed do not.
	newRun := func(status, phase string) int64 {
		r := sampleRun()
		r.Project = project
		r.ProposalID = nil
		r.Status = status
		r.Phase = phase
		id, err := s.CreateRun(r)
		if err != nil {
			t.Fatalf("CreateRun(%s): %v", status, err)
		}
		return id
	}
	runningID := newRun("running", "building")
	pausedID := newRun("paused", "building")
	doneID := newRun("done", "done")
	failedID := newRun("failed", "blocked")

	rows, err := s.ListWorklist()
	if err != nil {
		t.Fatalf("ListWorklist: %v", err)
	}

	for _, id := range openInbox {
		if !worklistHas(rows, "inbox", project, id) {
			t.Errorf("open inbox row %d missing from worklist", id)
		}
	}
	if worklistHas(rows, "inbox", project, closedID) {
		t.Errorf("closed inbox row %d must not appear", closedID)
	}
	if !worklistHas(rows, "proposal", project, pendingID) {
		t.Errorf("pending proposal %d missing from worklist", pendingID)
	}
	if !worklistHas(rows, "proposal", project, successorID) {
		t.Errorf("successor proposal %d is still pending and must appear", successorID)
	}
	for name, id := range map[string]int64{"approved": approvedID, "rejected": rejectedID, "superseded": oldID} {
		if worklistHas(rows, "proposal", project, id) {
			t.Errorf("%s proposal %d must not appear", name, id)
		}
	}
	if !worklistHas(rows, "run", project, runningID) {
		t.Errorf("running run %d missing from worklist", runningID)
	}
	if !worklistHas(rows, "run", project, pausedID) {
		t.Errorf("paused run %d missing from worklist", pausedID)
	}
	for name, id := range map[string]int64{"done": doneID, "failed": failedID} {
		if worklistHas(rows, "run", project, id) {
			t.Errorf("%s run %d must not appear", name, id)
		}
	}

	if n := len(worklistOfKind(rows, "inbox")); n != 3 {
		t.Errorf("want 3 open inbox rows, got %d", n)
	}
	if n := len(worklistOfKind(rows, "proposal")); n != 2 {
		t.Errorf("want 2 pending proposals, got %d", n)
	}
	if n := len(worklistOfKind(rows, "run")); n != 2 {
		t.Errorf("want 2 live runs, got %d", n)
	}
}

// TestListWorklistCarriesNoPayloads — same constraint ListIndex carries: a
// view called on every sweep must not drag plan bodies or proposal payloads
// into context. Ids, status, a short summary and waiting_on, nothing more.
func TestListWorklistCarriesNoPayloads(t *testing.T) {
	s := newTestStore(t)
	project := "alpha"
	seedProject(t, s, project, "alpha does a thing", []map[string]any{
		planWith("ALPHA-1", "the live one", "pending"),
	})

	it := sampleInbox("1756820000.000100")
	it.Project = &project
	if _, err := s.CreateInbox(it); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	p := sampleProposal("plan a thing")
	p.Project = project
	p.SourceRef = "1756820000.000200"
	p.Payload = map[string]any{
		"ticket": "ALPHA-2",
		"plan_steps": []any{
			map[string]any{"id": 1, "how": "PAYLOAD_CANARY_STEP_HOW", "files": []any{"a.go"}},
		},
	}
	pid, err := s.CreateProposal(p)
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}

	r := sampleRun()
	r.Project = project
	r.ProposalID = &pid
	r.Status = "running"
	r.Cursor = map[string]any{"branch": "feat/x", "note": "CURSOR_CANARY"}
	if _, err := s.CreateRun(r); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	rows, err := s.ListWorklist()
	if err != nil {
		t.Fatalf("ListWorklist: %v", err)
	}
	blob, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{
		"plan_steps", "PAYLOAD_CANARY_STEP_HOW", "payload",
		"acceptance_criteria", "resources", "CURSOR_CANARY", "cursor",
	} {
		if strings.Contains(string(blob), forbidden) {
			t.Errorf("worklist payload leaks %q: %s", forbidden, blob)
		}
	}
}

// ── worklist query budget ────────────────────────────────────────────────────
//
// The assertion that actually protects the feature: the query count must not
// grow with project count. ListIndex was built for exactly this reason, and an
// N+1 hidden behind a correct-looking output shape is the failure this catches.
//
// Counting happens in a driver wrapper rather than around the store, because
// the store's own methods are what we are measuring. Only queries are counted;
// schema Execs and inserts are not, and the counter is reset immediately before
// the measured call.

type queryCounter struct {
	mu sync.Mutex
	n  int
}

func (c *queryCounter) inc() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *queryCounter) reset() {
	c.mu.Lock()
	c.n = 0
	c.mu.Unlock()
}

func (c *queryCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

type countingDriver struct {
	inner   driver.Driver
	counter *queryCounter
}

func (d countingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: c, counter: d.counter}, nil
}

type countingConn struct {
	driver.Conn
	counter *queryCounter
}

func (c *countingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		// database/sql falls back to Prepare + Stmt.Query, counted below.
		return nil, driver.ErrSkip
	}
	c.counter.inc()
	return qc.QueryContext(ctx, q, args)
}

func (c *countingConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return ec.ExecContext(ctx, q, args)
}

func (c *countingConn) Prepare(q string) (driver.Stmt, error) {
	st, err := c.Conn.Prepare(q)
	if err != nil {
		return nil, err
	}
	return &countingStmt{Stmt: st, counter: c.counter}, nil
}

func (c *countingConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	pc, ok := c.Conn.(driver.ConnPrepareContext)
	if !ok {
		return c.Prepare(q)
	}
	st, err := pc.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	return &countingStmt{Stmt: st, counter: c.counter}, nil
}

type countingStmt struct {
	driver.Stmt
	counter *queryCounter
}

func (s *countingStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.counter.inc()
	return s.Stmt.Query(args)
}

func (s *countingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	s.counter.inc()
	return qc.QueryContext(ctx, args)
}

func (s *countingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return ec.ExecContext(ctx, args)
}

var countingDriverSeq int

// newCountingStore builds a store whose queries are counted. It reuses the
// registered "sqlite" driver underneath, so the DB behaves exactly as in
// production; only the call path is instrumented.
func newCountingStore(t *testing.T) (*store, *queryCounter) {
	t.Helper()

	probe, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatalf("open probe: %v", err)
	}
	inner := probe.Driver()
	probe.Close()

	counter := &queryCounter{}
	countingDriverSeq++
	name := fmt.Sprintf("sqlite-counting-%d", countingDriverSeq)
	sql.Register(name, countingDriver{inner: inner, counter: counter})

	db, err := sql.Open(name, filepath.Join(t.TempDir(), "registry.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open counting db: %v", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		t.Fatalf("WAL: %v", err)
	}
	s := &store{db: db}
	if err := s.createSchema(); err != nil {
		db.Close()
		t.Fatalf("createSchema: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, counter
}

func worklistQueryCount(t *testing.T, projects int) int {
	t.Helper()
	s, counter := newCountingStore(t)
	for i := 0; i < projects; i++ {
		seedWorklistProject(t, s, fmt.Sprintf("proj%02d", i), fmt.Sprintf("17569%05d", i))
	}

	counter.reset()
	rows, err := s.ListWorklist()
	if err != nil {
		t.Fatalf("ListWorklist(%d projects): %v", projects, err)
	}
	n := counter.count()
	if len(rows) != projects*3 {
		t.Fatalf("want %d rows for %d projects, got %d", projects*3, projects, len(rows))
	}
	if n == 0 {
		t.Fatal("counted zero queries — the counting driver is not on the call path")
	}
	return n
}

func TestListWorklistQueryCountIsBounded(t *testing.T) {
	four := worklistQueryCount(t, 4)
	twelve := worklistQueryCount(t, 12)

	if twelve != four {
		t.Errorf("query count grows with project count (N+1): 4 projects = %d queries, 12 projects = %d", four, twelve)
	}
	// A bound that is generous but still a bound: three tables plus slack.
	if four > 6 {
		t.Errorf("worklist should cost roughly one query per table (inbox, proposals, agent_runs), got %d", four)
	}
}

// ── inbox security + migration fixes (DOTFILES-40 ship-review remediation) ───
//
// These three cover the findings that blocked the first /ship: a caller forging
// server-owned linkage fields, a row written with none of the identity the tool
// schema calls required, and the dedup cursor moving between tables with no
// backfill.

func TestCreateInboxRequiresRawTextAndSourceLocation(t *testing.T) {
	s := newTestStore(t)

	noText := sampleInbox("1756700000.000200")
	noText.RawText = ""
	if _, err := s.CreateInbox(noText); err == nil {
		t.Error("want error when raw_text is empty, got nil — an inbox row with no text is untriageable")
	}

	noChannel := sampleInbox("1756700000.000201")
	noChannel.SourceChannel = ""
	if _, err := s.CreateInbox(noChannel); err == nil {
		t.Error("want error when source_channel is empty, got nil")
	}

	noPermalink := sampleInbox("1756700000.000202")
	noPermalink.SourcePermalink = ""
	if _, err := s.CreateInbox(noPermalink); err == nil {
		t.Error("want error when source_permalink is empty, got nil — a queue row with no way back to the conversation is not actionable")
	}
}

// TestCreateSchemaBackfillsInboxCursorFromProposals is the upgrade path: a
// registry that already holds proposals written before the inbox existed must
// not re-capture and re-plan those messages on its first post-upgrade sweep.
func TestCreateSchemaBackfillsInboxCursorFromProposals(t *testing.T) {
	s := newTestStore(t)

	p := sampleProposal("already handled before the inbox existed")
	if _, err := s.CreateProposal(p); err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}

	// Simulate the next server start, which is when the backfill runs.
	if err := s.createSchema(); err != nil {
		t.Fatalf("createSchema (re-run): %v", err)
	}

	items, err := s.ListInbox("closed")
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	var found *inboxItem
	for _, it := range items {
		if it.SourceRef == p.SourceRef {
			found = it
			break
		}
	}
	if found == nil {
		t.Fatalf("proposal source_ref %q was not backfilled into inbox; the first sweep after upgrade would re-capture and re-plan it", p.SourceRef)
	}
	if found.Status != "closed" {
		t.Errorf("backfilled cursor row should be closed (it is not work), got %q", found.Status)
	}
	if found.Triage != "" {
		t.Errorf("backfilled cursor row should carry no triage verdict, got %q", found.Triage)
	}

	// The whole point: capturing that same message again must now be refused.
	if _, err := s.CreateInbox(sampleInbox(p.SourceRef)); err == nil {
		t.Error("want 'already exists' on a source_ref that is already a proposal, got nil — the dedup cursor did not carry over")
	}
}

func TestCreateSchemaBackfillIsIdempotent(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateProposal(sampleProposal("run me twice")); err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.createSchema(); err != nil {
			t.Fatalf("createSchema run %d: %v", i, err)
		}
	}
	items, err := s.ListInbox("")
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("want exactly 1 backfilled row after 3 schema runs, got %d — backfill is not idempotent", len(items))
	}
}

// TestUpdateInboxRefusesTriagedWithoutClass is the co-requirement the tool
// description always claimed and nothing enforced. A class-less `triaged` row
// is unresumable: the sweep's resume loop performs "the action for the class it
// already carries", and there is no class, so the row is revisited forever.
func TestUpdateInboxRefusesTriagedWithoutClass(t *testing.T) {
	s := newTestStore(t)

	id, err := s.CreateInbox(sampleInbox("1756700000.000300"))
	if err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	if err := s.UpdateInbox(id, "triaged", "", "private-dotfiles", "", nil); err == nil {
		t.Error("want refusal for status=triaged with no triage class, got nil")
	} else if !strings.Contains(err.Error(), "requires a triage class") {
		t.Errorf("want a co-requirement error naming the cause, got: %v", err)
	}

	// The row must be untouched by the refused write.
	it, err := s.GetInbox(id)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if it.Status != "new" || it.Triage != "" || it.Project != nil {
		t.Errorf("refused update still mutated the row: status=%q triage=%q project=%v", it.Status, it.Triage, it.Project)
	}

	// With a class it succeeds.
	if err := s.UpdateInbox(id, "triaged", "investigate", "private-dotfiles", "", nil); err != nil {
		t.Fatalf("UpdateInbox with a class: %v", err)
	}

	// And a later status-only advance is still allowed, because the stored
	// class satisfies the co-requirement.
	if err := s.UpdateInbox(id, "triaged", "", "", "still triaged", nil); err != nil {
		t.Errorf("status-only re-advance on an already-classified row should succeed, got: %v", err)
	}

	// A genuinely missing row must still say "not found", not the new error.
	if err := s.UpdateInbox(999999, "triaged", "", "", "", nil); err == nil {
		t.Error("want error for a missing row")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("want 'not found' for a missing row, got: %v", err)
	}
}

// TestPlanIsShipped_PhaseOverridePrecedence codifies planIsShipped's new
// precedence rule (DOTFILES-47): when phase_override is set, it must
// short-circuit the steps/hasAudit derivation entirely — 'done' always
// means shipped, any other non-empty value always means not shipped,
// regardless of what the steps or audit log say. An absent or empty
// override must leave today's steps+audit behavior untouched.
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
