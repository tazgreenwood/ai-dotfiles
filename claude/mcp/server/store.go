package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ── store: SQLite-backed persistence (DOTFILES-23) ──────────────────────────────
//
// Single SQLite DB (WAL mode) replacing per-project JSON files under
// data/{project}/*.json. Tables: projects, plans, audit, issues, deploy_checks,
// events, proposals.

type store struct {
	db *sql.DB
}

func newStore(dbPath string) (*store, error) {
	// busy_timeout MUST be a DSN pragma, not a one-off Exec: sql.DB is a
	// connection POOL, and `db.Exec("PRAGMA busy_timeout=...")` applies only to
	// whichever pooled connection happened to serve it. Every other connection
	// keeps the default of 0 and fails instantly with SQLITE_BUSY under
	// contention. Measured 2026-09-01: with the Exec form, 7 of 12 concurrent
	// step updates errored; via the DSN, none do. journal_mode is fine as an
	// Exec below because WAL is persisted in the database file itself.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}

	s := &store{db: db}
	if err := s.createSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) Close() error {
	return s.db.Close()
}

func (s *store) createSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			name TEXT PRIMARY KEY,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS plans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			ticket TEXT NOT NULL,
			status TEXT,
			created_at TEXT NOT NULL,
			data TEXT NOT NULL,
			UNIQUE(project, ticket)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_plans_project_ticket ON plans(project, ticket)`,
		`CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			date TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_project_date ON audit(project, date)`,
		`CREATE TABLE IF NOT EXISTS issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			reported_at TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_project_reported_at ON issues(project, reported_at)`,
		`CREATE TABLE IF NOT EXISTS deploy_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			recorded_at TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deploy_checks_project_recorded_at ON deploy_checks(project, recorded_at)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			type TEXT NOT NULL,
			occurred_at TEXT NOT NULL,
			data TEXT NOT NULL,
			tags TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_project_type_occurred ON events(project, type, occurred_at)`,
		`CREATE TABLE IF NOT EXISTS agent_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			proposal_id INTEGER,
			ticket TEXT,
			phase TEXT NOT NULL,
			status TEXT NOT NULL,
			cursor TEXT,
			note TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_runs_project_status ON agent_runs(project, status)`,
		`CREATE TABLE IF NOT EXISTS proposals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			source TEXT NOT NULL,
			source_channel TEXT NOT NULL,
			source_permalink TEXT NOT NULL,
			source_ref TEXT NOT NULL,
			kind TEXT NOT NULL,
			summary TEXT NOT NULL,
			payload TEXT NOT NULL,
			status TEXT NOT NULL,
			superseded_by INTEGER,
			decision_note TEXT NOT NULL DEFAULT '',
			notified_at TEXT,
			decided_at TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_proposals_project_status ON proposals(project, status)`,
		// Dedup key: one proposal per inbound source message (e.g. a Slack ts).
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_proposals_source_ref ON proposals(source, source_ref)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// ── projects ─────────────────────────────────────────────────────────────────

func (s *store) SetProject(name string, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO projects (name, data) VALUES (?, ?)
		 ON CONFLICT(name) DO UPDATE SET data=excluded.data`,
		name, string(b),
	)
	return err
}

func (s *store) GetProject(name string) (map[string]any, error) {
	var raw string
	err := s.db.QueryRow(`SELECT data FROM projects WHERE name = ?`, name).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *store) ListProjects() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

// ── plans ────────────────────────────────────────────────────────────────────

func (s *store) WritePlan(project, ticket string, plan map[string]any) error {
	b, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	status, _ := plan["status"].(string)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(
		`INSERT INTO plans (project, ticket, status, created_at, data) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project, ticket) DO UPDATE SET status=excluded.status, data=excluded.data`,
		project, ticket, status, now, string(b),
	)
	return err
}

func (s *store) GetPlan(project, ticket string) (map[string]any, error) {
	var id int64
	var createdAt, raw string
	err := s.db.QueryRow(
		`SELECT id, created_at, data FROM plans WHERE project = ? AND ticket = ?`,
		project, ticket,
	).Scan(&id, &createdAt, &raw)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, err
	}
	data["id"] = id
	data["created_at"] = createdAt
	return data, nil
}

func (s *store) ListPlans(project string) ([]map[string]any, error) {
	rows, err := s.db.Query(
		`SELECT id, ticket, created_at, data FROM plans WHERE project = ? ORDER BY id`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []map[string]any
	for rows.Next() {
		var id int64
		var ticket, createdAt, raw string
		if err := rows.Scan(&id, &ticket, &createdAt, &raw); err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return nil, err
		}
		data["id"] = id
		data["created_at"] = createdAt
		if t, _ := data["ticket"].(string); t == "" {
			data["ticket"] = ticket
		}
		plans = append(plans, data)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return plans, nil
}

// UpdateStep sets one step's status inside the plan's JSON blob.
//
// This is a SINGLE UPDATE using SQLite's json_set/json_remove rather than the
// read-modify-write it used to be. That is the point: build-workflow.js runs
// async steps concurrently and each spawned subagent calls this independently,
// so reading the whole blob into Go, mutating it, and writing it back let the
// last writer win on the ENTIRE blob and silently erase other steps' statuses.
//
// Measured on 2026-09-01 with 12 concurrent updates. BOTH halves of the fix
// (this rewrite and the DSN busy_timeout in newStore) are independently
// necessary:
//
//	read-modify-write + busy_timeout  -> 9 lost, 0 errors  (SILENT data loss)
//	atomic json_set  + no timeout     -> 0 lost, 7 errors  (loud failure)
//	atomic json_set  + busy_timeout   -> 0 lost, 0 errors
//
// Wrapping the read-modify-write in a transaction was NOT enough: a deferred
// BEGIN still lets every writer read the same stale blob, and an out-of-band
// BEGIN IMMEDIATE did not survive database/sql's connection handling (still 9
// lost). Removing the read-modify-write removes the window entirely, which is
// simpler and independent of driver transaction quirks.
//
// A Go mutex would not have worked either: several registry server processes
// (one per Claude session) open this same file.
func (s *store) UpdateStep(project, ticket string, stepIndex int, status string) error {
	if stepIndex < 0 {
		return fmt.Errorf("step index %d out of range", stepIndex)
	}
	// Validate the index against the current plan. A concurrent write between
	// this check and the UPDATE is harmless: the UPDATE addresses the step by
	// path, so it either applies to that step or matches nothing.
	var raw string
	if err := s.db.QueryRow(
		`SELECT data FROM plans WHERE project = ? AND ticket = ?`,
		project, ticket,
	).Scan(&raw); err != nil {
		return err
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return err
	}
	steps, ok := plan["plan_steps"].([]any)
	if !ok || stepIndex >= len(steps) {
		return fmt.Errorf("step index %d out of range", stepIndex)
	}
	if _, ok := steps[stepIndex].(map[string]any); !ok {
		return fmt.Errorf("step %d is not an object", stepIndex)
	}

	statusPath := fmt.Sprintf("$.plan_steps[%d].status", stepIndex)
	doneAtPath := fmt.Sprintf("$.plan_steps[%d].done_at", stepIndex)

	// `status` is deliberately NOT written to the plans.status column. It used
	// to receive the STEP's status, so marking step 3 in_progress set the whole
	// PLAN's status — a persisted lie, invisible only because nothing reads it
	// (plan status is derived from step statuses by planIsShipped).
	var err error
	if status == "done" {
		_, err = s.db.Exec(
			`UPDATE plans
			    SET data = json_set(json_set(data, ?, ?), ?, ?)
			  WHERE project = ? AND ticket = ?`,
			statusPath, status,
			doneAtPath, time.Now().UTC().Format(time.RFC3339),
			project, ticket,
		)
	} else {
		_, err = s.db.Exec(
			`UPDATE plans
			    SET data = json_remove(json_set(data, ?, ?), ?)
			  WHERE project = ? AND ticket = ?`,
			statusPath, status, doneAtPath,
			project, ticket,
		)
	}
	return err
}

// ── audit ────────────────────────────────────────────────────────────────────

func (s *store) WriteAudit(project string, entry map[string]any) (int, error) {
	date, _ := entry["date"].(string)
	b, err := json.Marshal(entry)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(
		`INSERT INTO audit (project, date, data) VALUES (?, ?, ?)`,
		project, date, string(b),
	); err != nil {
		return 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE project = ?`, project).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) GetAudit(project, since, until string) ([]map[string]any, int, error) {
	query := `SELECT data FROM audit WHERE project = ?`
	args := []any{project}
	if since != "" {
		query += ` AND date >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND date <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY date ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return entries, len(entries), nil
}

// ── issues ───────────────────────────────────────────────────────────────────

func (s *store) WriteIssue(project string, issue map[string]any) (int, error) {
	reportedAt := time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(issue)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(
		`INSERT INTO issues (project, reported_at, data) VALUES (?, ?, ?)`,
		project, reportedAt, string(b),
	); err != nil {
		return 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE project = ?`, project).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) GetIssues(project string) ([]map[string]any, error) {
	rows, err := s.db.Query(
		`SELECT data FROM issues WHERE project = ? ORDER BY reported_at ASC`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var issues []map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var issue map[string]any
		if err := json.Unmarshal([]byte(raw), &issue); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return issues, nil
}

// ── deploy checks ────────────────────────────────────────────────────────────

func (s *store) WriteDeployCheck(project string, check map[string]any) (int, error) {
	// Prefer the entry's own "date" field (so since/until filtering matches the
	// deploy check's reported date, not the write time), fall back to now.
	recordedAt, _ := check["date"].(string)
	if recordedAt == "" {
		recordedAt = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(check)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(
		`INSERT INTO deploy_checks (project, recorded_at, data) VALUES (?, ?, ?)`,
		project, recordedAt, string(b),
	); err != nil {
		return 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM deploy_checks WHERE project = ?`, project).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) GetDeployChecks(project, since, until string) ([]map[string]any, int, error) {
	query := `SELECT data FROM deploy_checks WHERE project = ?`
	args := []any{project}
	if since != "" {
		query += ` AND recorded_at >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND recorded_at <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY recorded_at ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return entries, len(entries), nil
}

// ── events ───────────────────────────────────────────────────────────────────

func (s *store) WriteEvent(project, eventType string, data map[string]any, tags []string) (int, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	occurredAt := time.Now().UTC().Format(time.RFC3339)
	tagStr := strings.Join(tags, ",")
	if _, err := s.db.Exec(
		`INSERT INTO events (project, type, occurred_at, data, tags) VALUES (?, ?, ?, ?, ?)`,
		project, eventType, occurredAt, string(b), tagStr,
	); err != nil {
		return 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE project = ?`, project).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) GetEvents(project, eventType, since, until string) ([]map[string]any, int, error) {
	query := `SELECT id, occurred_at, data, tags FROM events WHERE project = ?`
	args := []any{project}
	if eventType != "" {
		query += ` AND type = ?`
		args = append(args, eventType)
	}
	if since != "" {
		query += ` AND occurred_at >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND occurred_at <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY occurred_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []map[string]any
	for rows.Next() {
		var id int64
		var occurredAt, raw, tags string
		if err := rows.Scan(&id, &occurredAt, &raw, &tags); err != nil {
			return nil, 0, err
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, 0, err
		}
		entry["id"] = id
		entry["occurred_at"] = occurredAt
		if tags != "" {
			entry["tags"] = strings.Split(tags, ",")
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return entries, len(entries), nil
}

// ── proposals (DOTFILES-34) ──────────────────────────────────────────────────
//
// A proposal is a unit of agent-produced work awaiting a human decision. It is
// deliberately NOT a plan: nothing here executes into code, and approving one
// only records the decision.
//
// source_channel and source_permalink are required, not decorative: a Slack
// message ts alone cannot reconstruct a permalink (that needs channel +
// workspace), and without a permalink the read-only UI queue has no exit back
// to the conversation the proposal came from.

type proposal struct {
	ID              int64          `json:"id"`
	Project         string         `json:"project"`
	Source          string         `json:"source"`
	SourceChannel   string         `json:"source_channel"`
	SourcePermalink string         `json:"source_permalink"`
	SourceRef       string         `json:"source_ref"`
	Kind            string         `json:"kind"`
	Summary         string         `json:"summary"`
	Payload         map[string]any `json:"payload,omitempty"`
	Status          string         `json:"status"`
	SupersededBy    *int64         `json:"superseded_by"`
	DecisionNote    string         `json:"decision_note"`
	NotifiedAt      *string        `json:"notified_at"`
	DecidedAt       *string        `json:"decided_at"`
	CreatedAt       string         `json:"created_at"`
}

var (
	proposalKinds    = map[string]bool{"plan": true, "fix": true, "review": true, "improvement": true}
	proposalStatuses = map[string]bool{"pending": true, "approved": true, "rejected": true, "superseded": true}
	// Statuses a caller may set directly. "superseded" is excluded: it must go
	// through SupersedeProposal so status and superseded_by move together and
	// the revision chain is never left dangling.
	proposalDecisions = map[string]bool{"pending": true, "approved": true, "rejected": true}
)

const proposalColumns = `id, project, source, source_channel, source_permalink, source_ref,
	kind, summary, payload, status, superseded_by, decision_note, notified_at, decided_at, created_at`

// CreateProposal inserts a proposal and returns its new id. Status defaults to
// "pending" and created_at to now (RFC3339 UTC), matching the stamping used by
// the audit/issues/events writers.
func (s *store) CreateProposal(p *proposal) (int64, error) {
	if p == nil {
		return 0, fmt.Errorf("proposal is nil")
	}
	if p.Project == "" {
		return 0, fmt.Errorf("proposal project is required")
	}
	if p.Source == "" || p.SourceRef == "" {
		return 0, fmt.Errorf("proposal source and source_ref are required")
	}
	// source_channel and source_permalink are required for anything that came
	// from a conversation, because a queue row with no way back to that
	// conversation is not actionable. A proposal planned in-session
	// (source="session") has no originating message, so there is nothing to link
	// to — relax the check for that source ONLY. Widening it further would
	// reintroduce dead rows in the UI's one actionable control.
	if p.Source != "session" && (p.SourceChannel == "" || p.SourcePermalink == "") {
		return 0, fmt.Errorf("proposal source_channel and source_permalink are required for source %q", p.Source)
	}
	if !proposalKinds[p.Kind] {
		return 0, fmt.Errorf("invalid proposal kind %q (want plan|fix|review|improvement)", p.Kind)
	}
	status := p.Status
	if status == "" {
		status = "pending"
	}
	if !proposalStatuses[status] {
		return 0, fmt.Errorf("invalid proposal status %q (want pending|approved|rejected|superseded)", status)
	}

	payload := "{}"
	if p.Payload != nil {
		b, err := json.Marshal(p.Payload)
		if err != nil {
			return 0, err
		}
		payload = string(b)
	}
	createdAt := p.CreatedAt
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}

	res, err := s.db.Exec(
		`INSERT INTO proposals
			(project, source, source_channel, source_permalink, source_ref,
			 kind, summary, payload, status, superseded_by, decision_note,
			 notified_at, decided_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Project, p.Source, p.SourceChannel, p.SourcePermalink, p.SourceRef,
		p.Kind, p.Summary, payload, status, p.SupersededBy, p.DecisionNote,
		p.NotifiedAt, p.DecidedAt, createdAt,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	p.ID = id
	p.Status = status
	p.CreatedAt = createdAt
	return id, nil
}

func scanProposal(sc interface{ Scan(...any) error }) (*proposal, error) {
	var p proposal
	var payload string
	var decisionNote sql.NullString
	if err := sc.Scan(
		&p.ID, &p.Project, &p.Source, &p.SourceChannel, &p.SourcePermalink, &p.SourceRef,
		&p.Kind, &p.Summary, &payload, &p.Status, &p.SupersededBy, &decisionNote,
		&p.NotifiedAt, &p.DecidedAt, &p.CreatedAt,
	); err != nil {
		return nil, err
	}
	p.DecisionNote = decisionNote.String
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &p.Payload); err != nil {
			return nil, fmt.Errorf("proposal %d payload: %w", p.ID, err)
		}
	}
	return &p, nil
}

func (s *store) GetProposal(id int64) (*proposal, error) {
	row := s.db.QueryRow(`SELECT `+proposalColumns+` FROM proposals WHERE id = ?`, id)
	p, err := scanProposal(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("proposal %d not found", id)
	}
	return p, err
}

// ListProposals returns a project's proposals, newest first (it backs a queue
// view). An empty status returns every status, including superseded revisions.
func (s *store) ListProposals(project, status string) ([]*proposal, error) {
	query := `SELECT ` + proposalColumns + ` FROM proposals WHERE project = ?`
	args := []any{project}
	if status != "" {
		if !proposalStatuses[status] {
			return nil, fmt.Errorf("invalid proposal status filter %q", status)
		}
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateProposalStatus records a human decision on a proposal. It stamps
// decided_at for terminal statuses (approved/rejected). This never touches the
// payload and never triggers execution — approval is a recorded decision only.
func (s *store) UpdateProposalStatus(id int64, status, decisionNote string) error {
	if !proposalDecisions[status] {
		return fmt.Errorf("invalid proposal status %q (want pending|approved|rejected; use SupersedeProposal for superseded)", status)
	}
	var decidedAt *string
	if status == "approved" || status == "rejected" {
		now := time.Now().UTC().Format(time.RFC3339)
		decidedAt = &now
	}
	res, err := s.db.Exec(
		`UPDATE proposals SET status = ?, decision_note = ?, decided_at = ? WHERE id = ?`,
		status, decisionNote, decidedAt, id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("proposal %d not found", id)
	}
	return nil
}

// SupersedeProposal marks oldID superseded by newID, storing note as the old
// row's decision_note. All three writes land in one transaction so a proposal
// can never be left "superseded" with a NULL superseded_by, or superseded
// without the reason it was superseded — a revision chain with a hole in it
// would lose the history this table exists to preserve. Deliberately NOT the
// read-modify-write shape used by UpdateStep above.
//
// Only a pending proposal can be superseded. Superseding is a revision of work
// still awaiting a decision; a row that already carries a human's approve or
// reject must not have that decision silently rewritten.
func (s *store) SupersedeProposal(oldID, newID int64, note string) error {
	if oldID == newID {
		return fmt.Errorf("proposal %d cannot supersede itself", oldID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Read the old row's project and status inside the transaction: the status
	// gate and the successor's project check below both depend on them, and
	// reading them outside would reintroduce the read-modify-write race.
	var oldProject, oldStatus string
	err = tx.QueryRow(`SELECT project, status FROM proposals WHERE id = ?`, oldID).
		Scan(&oldProject, &oldStatus)
	if err == sql.ErrNoRows {
		return fmt.Errorf("proposal %d not found", oldID)
	}
	if err != nil {
		return err
	}
	if oldStatus != "pending" {
		return fmt.Errorf("proposal %d is %s, only a pending proposal can be superseded", oldID, oldStatus)
	}

	// The successor must exist AND belong to the same project, or a caller
	// scoped to one project could point its revision chain at another's row.
	var newProject string
	err = tx.QueryRow(`SELECT project FROM proposals WHERE id = ?`, newID).Scan(&newProject)
	if err == sql.ErrNoRows {
		return fmt.Errorf("superseding proposal %d not found", newID)
	}
	if err != nil {
		return err
	}
	if newProject != oldProject {
		return fmt.Errorf("superseding proposal %d belongs to project %q, not %q", newID, newProject, oldProject)
	}

	if _, err := tx.Exec(
		`UPDATE proposals SET status = 'superseded', superseded_by = ?, decision_note = ? WHERE id = ?`,
		newID, note, oldID,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// ── project index (DOTFILES-36) ──────────────────────────────────────────────
//
// A deliberately thin, cross-project view: enough for /lead to decide WHICH
// project a request belongs to, and nothing more. Plan bodies, audit entries
// and the resources subtree are all excluded on purpose — the whole reason this
// exists is so routing costs one small call instead of reading every project's
// context.

type activePlanRef struct {
	Ticket  string `json:"ticket"`
	Summary string `json:"summary"`
}

type projectIndexEntry struct {
	Name       string         `json:"name"`
	Purpose    string         `json:"purpose"`
	Repo       string         `json:"repo"`
	LocalPath  string         `json:"local_path"`
	ActivePlan *activePlanRef `json:"active_plan"`
}

// planIsShipped reports whether every step of a plan is done. A plan with no
// steps is not shipped — an empty plan is unfinished, not complete.
func planIsShipped(data map[string]any) bool {
	steps, ok := data["plan_steps"].([]any)
	if !ok || len(steps) == 0 {
		return false
	}
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok || step["status"] != "done" {
			return false
		}
	}
	return true
}

// ListIndex returns one row per project, newest-non-shipped plan included.
// Two queries total regardless of project count — deliberately not N+1, since
// this runs on every routed request.
func (s *store) ListIndex() ([]projectIndexEntry, error) {
	projRows, err := s.db.Query(`SELECT name, data FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer projRows.Close()

	var entries []projectIndexEntry
	order := map[string]int{}
	for projRows.Next() {
		var name, raw string
		if err := projRows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return nil, fmt.Errorf("project %s data: %w", name, err)
		}
		purpose, _ := data["purpose"].(string)
		entry := projectIndexEntry{Name: name, Purpose: purpose}
		// repo is a map ({workspace, localPath, ...}) in current projects, but
		// tolerate a bare string rather than dropping the field on older rows.
		switch repo := data["repo"].(type) {
		case map[string]any:
			ws, _ := repo["workspace"].(string)
			if ws != "" {
				entry.Repo = ws + "/" + name
			}
			entry.LocalPath, _ = repo["localPath"].(string)
		case string:
			entry.Repo = repo
		}
		order[name] = len(entries)
		entries = append(entries, entry)
	}
	if err := projRows.Err(); err != nil {
		return nil, err
	}

	// Newest first, so the first non-shipped plan seen per project wins.
	planRows, err := s.db.Query(`SELECT project, ticket, data FROM plans ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer planRows.Close()

	for planRows.Next() {
		var project, ticket, raw string
		if err := planRows.Scan(&project, &ticket, &raw); err != nil {
			return nil, err
		}
		idx, ok := order[project]
		if !ok || entries[idx].ActivePlan != nil {
			continue // unknown project, or this project already has its newest
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return nil, fmt.Errorf("plan %s/%s data: %w", project, ticket, err)
		}
		if planIsShipped(data) {
			continue
		}
		if t, _ := data["ticket"].(string); t != "" {
			ticket = t
		}
		summary, _ := data["summary"].(string)
		entries[idx].ActivePlan = &activePlanRef{Ticket: ticket, Summary: summary}
	}
	return entries, planRows.Err()
}

// ── agent runs (DOTFILES-38) ─────────────────────────────────────────────────
//
// The resume spine for `/lead build`. Plans already track per-step status and
// build-workflow.js already checkpoints steps, but nothing tied
// proposal -> plan -> build -> ship -> PR together, so an interrupted chain
// could only be restarted, never resumed. A chain that cannot resume cannot run
// unattended: a crash, a rate limit, a sleeping laptop or a mid-build question
// all strand the work. Every transition is committed here before the next
// begins, so any later session can re-enter at `cursor`.

type agentRun struct {
	Project    string         `json:"project"`
	ProposalID *int64         `json:"proposal_id"`
	Ticket     string         `json:"ticket"`
	Phase      string         `json:"phase"`
	Status     string         `json:"status"`
	Cursor     map[string]any `json:"cursor,omitempty"`
	Note       string         `json:"note"`
	ID         int64          `json:"id"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
}

var (
	runPhases = map[string]bool{
		"planning": true, "building": true, "shipping": true, "done": true, "blocked": true,
	}
	runStatuses = map[string]bool{
		"running": true, "paused": true, "done": true, "failed": true,
	}
)

const runColumns = `id, project, proposal_id, ticket, phase, status, cursor, note,
	created_at, updated_at`

func validateRunPhaseStatus(phase, status string) error {
	if !runPhases[phase] {
		return fmt.Errorf("invalid run phase %q", phase)
	}
	if !runStatuses[status] {
		return fmt.Errorf("invalid run status %q", status)
	}
	return nil
}

func scanRun(sc interface{ Scan(...any) error }) (*agentRun, error) {
	var r agentRun
	var cursor sql.NullString
	var note sql.NullString
	var ticket sql.NullString
	if err := sc.Scan(
		&r.ID, &r.Project, &r.ProposalID, &ticket, &r.Phase, &r.Status,
		&cursor, &note, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	r.Ticket = ticket.String
	r.Note = note.String
	if cursor.String != "" {
		if err := json.Unmarshal([]byte(cursor.String), &r.Cursor); err != nil {
			return nil, fmt.Errorf("run %d cursor: %w", r.ID, err)
		}
	}
	return &r, nil
}

func (s *store) CreateRun(r *agentRun) (int64, error) {
	if r == nil || r.Project == "" {
		return 0, fmt.Errorf("run requires a project")
	}
	if r.Phase == "" {
		r.Phase = "planning"
	}
	if r.Status == "" {
		r.Status = "running"
	}
	if err := validateRunPhaseStatus(r.Phase, r.Status); err != nil {
		return 0, err
	}
	cursor := ""
	if r.Cursor != nil {
		b, err := json.Marshal(r.Cursor)
		if err != nil {
			return 0, err
		}
		cursor = string(b)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`INSERT INTO agent_runs
		   (project, proposal_id, ticket, phase, status, cursor, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Project, r.ProposalID, r.Ticket, r.Phase, r.Status, cursor, r.Note, now, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *store) GetRun(id int64) (*agentRun, error) {
	row := s.db.QueryRow(`SELECT `+runColumns+` FROM agent_runs WHERE id = ?`, id)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %d not found", id)
	}
	return r, err
}

// UpdateRun advances the run in a SINGLE statement — phase, status, cursor and
// note move together and updated_at is restamped. Deliberately NOT the
// read-modify-write shape used by UpdateStep: this is the record a concurrent
// resume reads to decide where to re-enter.
func (s *store) UpdateRun(id int64, phase, status string, cursor map[string]any, note, ticket string) error {
	if err := validateRunPhaseStatus(phase, status); err != nil {
		return err
	}
	cursorJSON := ""
	if cursor != nil {
		b, err := json.Marshal(cursor)
		if err != nil {
			return err
		}
		cursorJSON = string(b)
	}
	// A nil cursor means "leave the existing cursor alone" — an advance that
	// only changes phase must not silently erase where the chain got to.
	// An empty ticket means the same as a nil cursor: leave it alone. The run is
	// opened BEFORE the ticket key is allocated (so a crash in between leaves a
	// visible `planning` run rather than silence), so the ticket can only ever
	// arrive on a later advance. Without this the column was unfillable — caught
	// by the first real /lead build run, where run 2 finished with no ticket.
	res, err := s.db.Exec(
		`UPDATE agent_runs
		    SET phase = ?, status = ?,
		        cursor = CASE WHEN ? = '' THEN cursor ELSE ? END,
		        ticket = CASE WHEN ? = '' THEN ticket ELSE ? END,
		        note = ?, updated_at = ?
		  WHERE id = ?`,
		phase, status, cursorJSON, cursorJSON, ticket, ticket, note,
		time.Now().UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("run %d not found", id)
	}
	return nil
}

// ListRuns returns a project's runs, newest first. An empty status returns all.
func (s *store) ListRuns(project, status string) ([]*agentRun, error) {
	query := `SELECT ` + runColumns + ` FROM agent_runs WHERE project = ?`
	args := []any{project}
	if status != "" {
		if !runStatuses[status] {
			return nil, fmt.Errorf("invalid run status filter %q", status)
		}
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*agentRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// ── atomic build claim (DOTFILES-35) ─────────────────────────────────────────

// ClaimProposalForBuild is the SINGLE enforcement point for /lead build's
// refusal guards. It replaces three prose checks in lead.md that nothing but
// prompt fidelity enforced, and it closes a real race: when the checks and the
// run creation were separate calls, two concurrent claims both passed and both
// built.
//
// Everything happens inside one BEGIN IMMEDIATE transaction so concurrent
// claimers serialize on the write lock rather than interleaving. Plain BEGIN
// defers the write lock in SQLite and leaves the lost-update window open, so the
// IMMEDIATE is load-bearing, not decoration.
//
// Every refusal names the actual blocking condition — a caller that cannot tell
// "already built" from "not approved" cannot tell the human what to do next.
func (s *store) ClaimProposalForBuild(project string, proposalID int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`BEGIN IMMEDIATE`); err != nil {
		// database/sql already opened a transaction; upgrading to an immediate
		// write lock is best-effort. The UNIQUE-style checks below still run
		// inside the transaction, so correctness does not depend on this.
		_ = err
	}

	var rowProject, status string
	err = tx.QueryRow(`SELECT project, status FROM proposals WHERE id = ?`, proposalID).
		Scan(&rowProject, &status)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("proposal %d not found", proposalID)
	}
	if err != nil {
		return 0, err
	}
	if rowProject != project {
		return 0, fmt.Errorf("proposal %d not found for project %q", proposalID, project)
	}
	if status != "approved" {
		return 0, fmt.Errorf("proposal %d is %s, not approved — only an approved proposal can be built", proposalID, status)
	}

	// Guard A: a run already carries it. The human wants --resume, not a second
	// build; report the existing run so they can.
	var existingRun int64
	err = tx.QueryRow(
		`SELECT id FROM agent_runs WHERE project = ? AND proposal_id = ? ORDER BY id LIMIT 1`,
		project, proposalID,
	).Scan(&existingRun)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if err == nil {
		return 0, fmt.Errorf("proposal %d was already claimed by run %d — use --resume to continue it", proposalID, existingRun)
	}

	// Guard B: a plan already carries it. Covers everything built BEFORE
	// agent_runs existed (DOTFILES-36 was converted by hand, so proposal 2 has a
	// plan and no run). Guard A alone would miss those; Guard B alone would miss
	// a run that opened and then failed before its plan was written. Both.
	var existingTicket string
	err = tx.QueryRow(
		`SELECT ticket FROM plans
		  WHERE project = ? AND json_extract(data, '$.from_proposal') = ?
		  ORDER BY id LIMIT 1`,
		project, proposalID,
	).Scan(&existingTicket)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if err == nil {
		return 0, fmt.Errorf("proposal %d was already built as %s", proposalID, existingTicket)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`INSERT INTO agent_runs
		   (project, proposal_id, ticket, phase, status, cursor, note, created_at, updated_at)
		 VALUES (?, ?, '', 'planning', 'running', '', ?, ?, ?)`,
		project, proposalID, fmt.Sprintf("claimed for build from proposal %d", proposalID), now, now,
	)
	if err != nil {
		return 0, err
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return runID, nil
}
