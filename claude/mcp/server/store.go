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
	db, err := sql.Open("sqlite", dbPath)
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

func (s *store) UpdateStep(project, ticket string, stepIndex int, status string) error {
	var raw string
	err := s.db.QueryRow(
		`SELECT data FROM plans WHERE project = ? AND ticket = ?`,
		project, ticket,
	).Scan(&raw)
	if err != nil {
		return err
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return err
	}
	steps, ok := plan["plan_steps"].([]any)
	if !ok || stepIndex < 0 || stepIndex >= len(steps) {
		return fmt.Errorf("step index %d out of range", stepIndex)
	}
	step, ok := steps[stepIndex].(map[string]any)
	if !ok {
		return fmt.Errorf("step %d is not an object", stepIndex)
	}
	step["status"] = status
	if status == "done" {
		step["done_at"] = time.Now().UTC().Format(time.RFC3339)
	} else {
		delete(step, "done_at")
	}

	b, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE plans SET data = ?, status = ? WHERE project = ? AND ticket = ?`,
		string(b), status, project, ticket,
	)
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
	if p.SourceChannel == "" || p.SourcePermalink == "" {
		return 0, fmt.Errorf("proposal source_channel and source_permalink are required")
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

// SupersedeProposal marks oldID superseded by newID. Both writes land in one
// transaction so a proposal can never be left "superseded" with a NULL
// superseded_by (or vice versa) — a revision chain with a hole in it would lose
// the history this table exists to preserve. Deliberately NOT the
// read-modify-write shape used by UpdateStep above.
func (s *store) SupersedeProposal(oldID, newID int64) error {
	if oldID == newID {
		return fmt.Errorf("proposal %d cannot supersede itself", oldID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE proposals SET status = 'superseded', superseded_by = ? WHERE id = ?`,
		newID, oldID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("proposal %d not found", oldID)
	}

	// The successor must exist, or the chain points at nothing. Checked inside
	// the transaction so a bad newID rolls the UPDATE above back.
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM proposals WHERE id = ?`, newID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("superseding proposal %d not found", newID)
	}

	return tx.Commit()
}
