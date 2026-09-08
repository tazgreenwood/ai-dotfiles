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
	if err := s.backfillPlanStatuses(); err != nil {
		db.Close()
		return nil, fmt.Errorf("backfill plan statuses: %w", err)
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
		`CREATE TABLE IF NOT EXISTS agent_calls (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER,
			project TEXT NOT NULL,
			workflow TEXT,
			agent_label TEXT,
			model TEXT,
			status TEXT,
			started_at TEXT,
			ended_at TEXT,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cost_usd REAL DEFAULT 0,
			verdict TEXT,
			trajectory TEXT,
			error TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_calls_project_started ON agent_calls(project, started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_calls_run ON agent_calls(run_id)`,
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
	return s.dropDeadSchema()
}

// dropDeadSchema removes backend surface confirmed to have zero callers in
// claude/skills/ or claude/agents/ (DOTFILES-48): the agent_runs and inbox
// tables (which backed the earlier unattended Slack-inbox/proposal/approval/
// resumable-run design, simplified away on 2026-09-04 — see
// registry_get_events("private-dotfiles", "decision")), and the plans.status
// SQL column, which WritePlan wrote but nothing ever read — the real,
// authoritative status lives in the JSON data blob (data["status"], computed
// by ComputePlanStatus) and is what GetPlan/ListPlans/ListIndex return.
//
// DROP TABLE IF EXISTS is naturally idempotent. Dropping a column is not —
// SQLite has no "DROP COLUMN IF EXISTS" — so that one is guarded by an
// explicit pragma check; running it against a fresh DB (which never had the
// column) would otherwise error on every single startup.
func (s *store) dropDeadSchema() error {
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS agent_runs`); err != nil {
		return fmt.Errorf("drop agent_runs: %w", err)
	}
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS inbox`); err != nil {
		return fmt.Errorf("drop inbox: %w", err)
	}

	hasStatusCol, err := s.columnExists("plans", "status")
	if err != nil {
		return fmt.Errorf("check plans.status: %w", err)
	}
	if hasStatusCol {
		if _, err := s.db.Exec(`ALTER TABLE plans DROP COLUMN status`); err != nil {
			return fmt.Errorf("drop plans.status: %w", err)
		}
	}
	return nil
}

// columnExists reports whether table has a column named col, via
// pragma_table_info — the only portable way to ask SQLite this short of
// parsing sqlite_master's stored CREATE TABLE text.
func (s *store) columnExists(table, col string) (bool, error) {
	var name string
	err := s.db.QueryRow(
		`SELECT name FROM pragma_table_info(?) WHERE name = ?`, table, col,
	).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
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
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(
		`INSERT INTO plans (project, ticket, created_at, data) VALUES (?, ?, ?, ?)
		 ON CONFLICT(project, ticket) DO UPDATE SET data=excluded.data`,
		project, ticket, now, string(b),
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
//
// DOTFILES-48: the step-status UPDATE and the plan-level status recompute it
// drives (via ComputePlanStatus) are wrapped in one transaction so a step
// write can never persist without the plan-level status it implies — same
// reasoning as SetPlanPhase's transactional phase_override+event write below.
func (s *store) UpdateStep(project, ticket string, stepIndex int, status string) error {
	if stepIndex < 0 {
		return fmt.Errorf("step index %d out of range", stepIndex)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// This tx.Exec always fails — verified directly: s.db.Begin() has already
	// opened a (deferred) transaction, so SQLite rejects the nested BEGIN
	// with "cannot start a transaction within a transaction", 100% of the
	// time, not intermittently. It does NOT upgrade this transaction's lock;
	// no code here should assume it succeeded.
	//
	// It is nonetheless empirically required: removing it reliably
	// reproduces the exact lost-update race this function exists to avoid
	// (confirmed by deleting it and re-running TestUpdateStep_
	// ConcurrentUpdatesAllPersist — 9 of 12 concurrent updates lost, same
	// failure shape as the read-modify-write design this replaced). The
	// mechanism isn't fully understood — likely database/sql/the driver
	// deferring the real BEGIN until the first statement executes, so
	// issuing (and failing) this one first changes when/how the underlying
	// transaction actually opens relative to the SELECT below. Do not remove
	// this call without re-running that concurrency test first.
	if _, err := tx.Exec(`BEGIN IMMEDIATE`); err != nil {
		_ = err
	}

	// Validate the index against the current plan. A concurrent write between
	// this check and the UPDATE is harmless: the UPDATE addresses the step by
	// path, so it either applies to that step or matches nothing.
	var raw string
	if err := tx.QueryRow(
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
	stepMap, ok := steps[stepIndex].(map[string]any)
	if !ok {
		return fmt.Errorf("step %d is not an object", stepIndex)
	}

	statusPath := fmt.Sprintf("$.plan_steps[%d].status", stepIndex)
	doneAtPath := fmt.Sprintf("$.plan_steps[%d].done_at", stepIndex)

	// `status` is deliberately NOT written to the plans.status column via this
	// path. It used to receive the STEP's status, so marking step 3
	// in_progress set the whole PLAN's status — a persisted lie. The
	// plan-level status field written below is the real, computed value.
	if status == "done" {
		_, err = tx.Exec(
			`UPDATE plans
			    SET data = json_set(json_set(data, ?, ?), ?, ?)
			  WHERE project = ? AND ticket = ?`,
			statusPath, status,
			doneAtPath, time.Now().UTC().Format(time.RFC3339),
			project, ticket,
		)
	} else {
		_, err = tx.Exec(
			`UPDATE plans
			    SET data = json_remove(json_set(data, ?, ?), ?)
			  WHERE project = ? AND ticket = ?`,
			statusPath, status, doneAtPath,
			project, ticket,
		)
	}
	if err != nil {
		return err
	}

	// Reflect this step's change into the local copy (steps[stepIndex] is the
	// same map this local `steps` slice already holds) so ComputePlanStatus
	// below sees the update this transaction is committing, not the stale
	// status read at the top.
	stepMap["status"] = status
	plan["plan_steps"] = steps

	hasAudit, err := hasAuditEntryTx(tx, project, ticket)
	if err != nil {
		return err
	}
	newStatus := ComputePlanStatus(plan, hasAudit)
	if _, err := tx.Exec(
		`UPDATE plans SET data = json_set(data, '$.status', ?) WHERE project = ? AND ticket = ?`,
		newStatus, project, ticket,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// hasAuditEntryTx reports whether project has at least one audit entry whose
// "ticket" field matches ticket, read within tx so the plan-level status
// mutations above (UpdateStep, SetPlanPhase) see a consistent snapshot
// alongside the plan write they commit with. Mirrors auditTicketSet's
// per-entry ticket extraction but scoped to one ticket and one transaction
// instead of building the whole project's set.
func hasAuditEntryTx(tx *sql.Tx, project, ticket string) (bool, error) {
	rows, err := tx.Query(`SELECT data FROM audit WHERE project = ?`, project)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return false, err
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return false, err
		}
		if t, _ := entry["ticket"].(string); t == ticket {
			return true, nil
		}
	}
	return false, rows.Err()
}

// SetPlanPhase sets or clears a plan's phase_override via a single atomic
// json_set/json_remove UPDATE — same reasoning as UpdateStep just above: a
// read-modify-write here would reintroduce the exact lost-update race that
// motivated UpdateStep's rewrite (and the plan_steps-clobbering incident
// documented in CLAUDE.md), since a concurrent UpdateStep call landing
// between this function's read and write would have its step-status change
// silently reverted when this write commits the stale blob it read earlier.
// Also never round-trips GetPlan's decorated map (which injects "id" and
// "created_at" for API-response convenience), so it can't leak those into
// the persisted document the way a GetPlan-then-WritePlan call would.
//
// The phase_override mutation and its plan_phase_override_set event are
// written in the SAME transaction: phase_override silently overrides
// planIsShipped's shipped/done determination, so the event is the only
// record of who/when/why, and a fire-and-forget event write that can fail
// independently of the mutation would let a plan flip to "shipped" with zero
// trace. If the event can't be recorded, the phase change rolls back too.
func (s *store) SetPlanPhase(project, ticket, phase string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var raw string
	if err := tx.QueryRow(
		`SELECT data FROM plans WHERE project = ? AND ticket = ?`,
		project, ticket,
	).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("plan '%s' not found for project '%s'", ticket, project)
		}
		return err
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return err
	}

	if phase == "" {
		_, err = tx.Exec(
			`UPDATE plans SET data = json_remove(data, '$.phase_override') WHERE project = ? AND ticket = ?`,
			project, ticket,
		)
	} else {
		_, err = tx.Exec(
			`UPDATE plans SET data = json_set(data, '$.phase_override', ?) WHERE project = ? AND ticket = ?`,
			phase, project, ticket,
		)
	}
	if err != nil {
		return err
	}

	// Recompute the plan-level status now that phase_override has changed —
	// an override wins outright in ComputePlanStatus's precedence, so update
	// plan's local copy to match what was just written before computing.
	if phase == "" {
		delete(plan, "phase_override")
	} else {
		plan["phase_override"] = phase
	}
	hasAudit, err := hasAuditEntryTx(tx, project, ticket)
	if err != nil {
		return err
	}
	newStatus := ComputePlanStatus(plan, hasAudit)
	if _, err := tx.Exec(
		`UPDATE plans SET data = json_set(data, '$.status', ?) WHERE project = ? AND ticket = ?`,
		newStatus, project, ticket,
	); err != nil {
		return err
	}

	eventData, err := json.Marshal(map[string]any{"ticket": ticket, "phase": phase})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO events (project, type, occurred_at, data, tags) VALUES (?, ?, ?, ?, ?)`,
		project, "plan_phase_override_set", time.Now().UTC().Format(time.RFC3339), string(eventData), "phase-override",
	); err != nil {
		return err
	}

	return tx.Commit()
}

// ── audit ────────────────────────────────────────────────────────────────────

// WriteAudit inserts the audit entry and, if it names a ticket with a plan
// on record, recomputes and persists that plan's status field in the SAME
// transaction (DOTFILES-48) — an audit entry is exactly the event that can
// flip a plan from pr_ready to done, so the two writes must commit together
// or not at all.
func (s *store) WriteAudit(project string, entry map[string]any) (int, error) {
	date, _ := entry["date"].(string)
	ticket, _ := entry["ticket"].(string)
	b, err := json.Marshal(entry)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO audit (project, date, data) VALUES (?, ?, ?)`,
		project, date, string(b),
	); err != nil {
		return 0, err
	}
	var total int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM audit WHERE project = ?`, project).Scan(&total); err != nil {
		return 0, err
	}

	if ticket != "" {
		var raw string
		err := tx.QueryRow(
			`SELECT data FROM plans WHERE project = ? AND ticket = ?`,
			project, ticket,
		).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if err == nil {
			var plan map[string]any
			if err := json.Unmarshal([]byte(raw), &plan); err != nil {
				return 0, err
			}
			// This entry itself is the audit entry for ticket, so hasAudit is
			// unconditionally true here regardless of any that preceded it.
			newStatus := ComputePlanStatus(plan, true)
			if _, err := tx.Exec(
				`UPDATE plans SET data = json_set(data, '$.status', ?) WHERE project = ? AND ticket = ?`,
				newStatus, project, ticket,
			); err != nil {
				return 0, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
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
	proposalKinds    = map[string]bool{"plan": true, "fix": true, "review": true, "improvement": true, "registration": true}
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
		return 0, fmt.Errorf("invalid proposal kind %q (want plan|fix|review|improvement|registration)", p.Kind)
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
	Ticket    string `json:"ticket"`
	Summary   string `json:"summary"`
	Status    string `json:"status"`
	IsShipped bool   `json:"is_shipped"`
}

type projectIndexEntry struct {
	Name       string         `json:"name"`
	Purpose    string         `json:"purpose"`
	Repo       string         `json:"repo"`
	LocalPath  string         `json:"local_path"`
	ActivePlan *activePlanRef `json:"active_plan"`
}

// planIsShipped reports whether every step of a plan is done AND hasAudit is
// true (an audit entry exists for the plan's ticket). A plan with no steps is
// not shipped — an empty plan is unfinished, not complete. All-steps-done
// alone used to be sufficient, which meant a plan dropped out of
// registry_index()'s active_plan the instant /build finished, before /ship
// had even run — there's a real gap between build-complete and
// actually-shipped (audit write) that the derived pr_ready status now names.
//
// Takes hasAudit as a plain bool rather than looking it up itself: the
// lookup is a per-project (or per-index-call) audit scan, and doing it once
// per candidate plan here previously turned registry_index() and
// registry_list_plans into an audit-log scan per plan row (see
// auditTicketsByProject / auditTicketSet, which callers use to batch this
// once instead).
// ComputePlanStatus computes a plan's single persisted Kanban status field
// (pending/in_progress/in_review/pr_ready/blocked/done) from its raw plan
// data (as decoded from the plans table's JSON blob — must carry
// "plan_steps" and, optionally, "phase_override") and whether an audit entry
// already exists for its ticket. This is the one authoritative port of the
// precedence logic that used to be duplicated between planIsShipped's bool
// (below) and the UI's derivePlanStatus (claude/ui/aggregate.go) — DOTFILES-48
// consolidates both into this single function, called at write-time (inside
// UpdateStep, WriteAudit, SetPlanPhase) rather than re-derived on every read.
//
// Precedence: phase_override wins outright over everything else; then
// blocked (any step blocked) beats in_review (any step in_review) beats
// in_progress (any step in_progress, or the plan is partially done) beats
// pr_ready/done (every step done — pr_ready until an audit entry exists for
// the ticket, done once it does) beats pending (the default — no step is
// done, blocked, in_review, or in_progress).
func ComputePlanStatus(data map[string]any, hasAudit bool) string {
	if ov, _ := data["phase_override"].(string); ov != "" {
		return ov
	}

	steps, _ := data["plan_steps"].([]any)
	anyBlocked := false
	anyInReview := false
	anyInProgress := false
	anyDone := false
	allDone := len(steps) > 0
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			allDone = false
			continue
		}
		status, _ := step["status"].(string)
		switch status {
		case "blocked":
			anyBlocked = true
		case "in_review":
			anyInReview = true
		case "in_progress":
			anyInProgress = true
		case "done":
			anyDone = true
		}
		if status != "done" {
			allDone = false
		}
	}
	switch {
	case anyBlocked:
		return "blocked"
	case anyInReview:
		return "in_review"
	case anyInProgress || (anyDone && !allDone):
		return "in_progress"
	case allDone:
		if hasAudit {
			return "done"
		}
		return "pr_ready"
	default:
		return "pending"
	}
}

// backfillPlanStatuses is an idempotent migration pass, run once on every
// store startup (see newStore), that populates the persisted "$.status"
// field on any plan row written before that field existed. Without it, a
// pre-migration row would read back with an empty status forever — the
// on-read derivation fallback this replaces is gone, so this backfill is the
// only thing left that can ever populate those rows.
//
// Skips any row that already carries a non-empty "status" (the common case
// on every startup after the first), so re-running this on an
// already-backfilled DB is a cheap no-op rather than clobbering statuses
// that mutation points (UpdateStep/WriteAudit/SetPlanPhase) already computed
// more precisely than a stale snapshot here could.
func (s *store) backfillPlanStatuses() error {
	auditTickets, err := s.auditTicketsByProject()
	if err != nil {
		return fmt.Errorf("audit tickets: %w", err)
	}

	rows, err := s.db.Query(`SELECT id, project, ticket, data FROM plans`)
	if err != nil {
		return err
	}

	type update struct {
		id     int64
		status string
	}
	var updates []update
	for rows.Next() {
		var id int64
		var project, ticket, raw string
		if err := rows.Scan(&id, &project, &ticket, &raw); err != nil {
			rows.Close()
			return err
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			rows.Close()
			return fmt.Errorf("plan id %d data: %w", id, err)
		}
		if status, _ := data["status"].(string); status != "" {
			continue // already backfilled, or written after this field existed
		}
		if t, _ := data["ticket"].(string); t != "" {
			ticket = t
		}
		updates = append(updates, update{
			id:     id,
			status: ComputePlanStatus(data, auditTickets[project][ticket]),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, u := range updates {
		if _, err := s.db.Exec(
			`UPDATE plans SET data = json_set(data, '$.status', ?) WHERE id = ?`,
			u.status, u.id,
		); err != nil {
			return fmt.Errorf("plan id %d: %w", u.id, err)
		}
	}
	return nil
}

func planIsShipped(hasAudit bool, data map[string]any) bool {
	if ov, _ := data["phase_override"].(string); ov != "" {
		return ov == "done"
	}

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
	return hasAudit
}

// auditTicketsByProject returns, for every project, the set of tickets that
// have at least one audit entry — one query total across all projects,
// rather than one query per plan. Backs ListIndex, which checks
// shipped-status for a variable, potentially large number of plan rows in a
// single call.
func (s *store) auditTicketsByProject() (map[string]map[string]bool, error) {
	rows, err := s.db.Query(`SELECT project, data FROM audit`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]map[string]bool{}
	for rows.Next() {
		var project, raw string
		if err := rows.Scan(&project, &raw); err != nil {
			return nil, err
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, err
		}
		ticket, _ := entry["ticket"].(string)
		if ticket == "" {
			continue
		}
		if result[project] == nil {
			result[project] = map[string]bool{}
		}
		result[project][ticket] = true
	}
	return result, rows.Err()
}

// auditTicketSet returns the set of tickets with an audit entry for a single
// project — one query for the whole call, reused across every plan checked
// in that call, rather than one query per plan. Backs registry_list_plans.
func (s *store) auditTicketSet(project string) (map[string]bool, error) {
	entries, _, err := s.GetAudit(project, "", "")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, entry := range entries {
		if t, _ := entry["ticket"].(string); t != "" {
			set[t] = true
		}
	}
	return set, nil
}

// ListIndex returns one row per project, newest-non-shipped plan included.
// Three queries total regardless of project or plan count — one for
// projects, one for all plans, one for all audit tickets (auditTicketsByProject)
// — deliberately not N+1, since this runs on every routed request.
func (s *store) ListIndex() ([]projectIndexEntry, error) {
	auditTickets, err := s.auditTicketsByProject()
	if err != nil {
		return nil, fmt.Errorf("audit tickets: %w", err)
	}

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
		if t, _ := data["ticket"].(string); t != "" {
			ticket = t
		}
		// Prefer the persisted status field (written at UpdateStep, WriteAudit,
		// SetPlanPhase). Fall back to computing it for plan rows written before
		// those mutation points landed or otherwise missing the field, so
		// is_shipped is never wrong just because a row predates the field.
		status, _ := data["status"].(string)
		if status == "" {
			status = ComputePlanStatus(data, auditTickets[project][ticket])
		}
		if status == "done" {
			continue
		}
		summary, _ := data["summary"].(string)
		entries[idx].ActivePlan = &activePlanRef{Ticket: ticket, Summary: summary, Status: status, IsShipped: false}
	}
	return entries, planRows.Err()
}

// ── agent calls: per-invocation trajectory + cost (DOTFILES-35) ──────────────
//
// agent_runs records WHAT happened in a /lead build chain (phase, cursor).
// This records HOW: which model, how long, how many tokens, what it cost, and
// the tool-call trace. One run spawns many agent invocations (developer, qa,
// security, reviewer, refuters), so this is a CHILD of a run — and run_id is
// nullable, because a bare /ship or /code-review has no run to belong to.
//
// Recording outcomes without trajectories is the documented blind spot: an
// audit of 731 agent trajectories found 63% of *successful* resolutions
// retrieved the fix rather than deriving it — invisible if you only check
// whether the result looked right.

type agentCall struct {
	ID           int64          `json:"id"`
	RunID        *int64         `json:"run_id"`
	Project      string         `json:"project"`
	Workflow     string         `json:"workflow"`
	AgentLabel   string         `json:"agent_label"`
	Model        string         `json:"model"`
	Status       string         `json:"status"`
	StartedAt    string         `json:"started_at"`
	EndedAt      string         `json:"ended_at"`
	InputTokens  int64          `json:"input_tokens"`
	OutputTokens int64          `json:"output_tokens"`
	CostUSD      float64        `json:"cost_usd"`
	Verdict      string         `json:"verdict,omitempty"`
	Trajectory   map[string]any `json:"trajectory,omitempty"`
	Error        string         `json:"error,omitempty"`
}

const agentCallColumns = `id, run_id, project, workflow, agent_label, model, status,
	started_at, ended_at, input_tokens, output_tokens, cost_usd, verdict, trajectory, error`

func scanAgentCall(sc interface{ Scan(...any) error }) (*agentCall, error) {
	var c agentCall
	var verdict, trajectory, errStr, model, label, workflow, status, started, ended sql.NullString
	if err := sc.Scan(
		&c.ID, &c.RunID, &c.Project, &workflow, &label, &model, &status,
		&started, &ended, &c.InputTokens, &c.OutputTokens, &c.CostUSD,
		&verdict, &trajectory, &errStr,
	); err != nil {
		return nil, err
	}
	c.Workflow, c.AgentLabel, c.Model, c.Status = workflow.String, label.String, model.String, status.String
	c.StartedAt, c.EndedAt = started.String, ended.String
	c.Verdict, c.Error = verdict.String, errStr.String
	if trajectory.String != "" {
		if err := json.Unmarshal([]byte(trajectory.String), &c.Trajectory); err != nil {
			return nil, fmt.Errorf("agent_call %d trajectory: %w", c.ID, err)
		}
	}
	return &c, nil
}

func (s *store) CreateAgentCall(c *agentCall) (int64, error) {
	if c == nil || c.Project == "" {
		return 0, fmt.Errorf("agent call requires a project")
	}
	trajectory := ""
	if c.Trajectory != nil {
		b, err := json.Marshal(c.Trajectory)
		if err != nil {
			return 0, err
		}
		trajectory = string(b)
	}
	res, err := s.db.Exec(
		`INSERT INTO agent_calls
		   (run_id, project, workflow, agent_label, model, status, started_at, ended_at,
		    input_tokens, output_tokens, cost_usd, verdict, trajectory, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.RunID, c.Project, c.Workflow, c.AgentLabel, c.Model, c.Status,
		c.StartedAt, c.EndedAt, c.InputTokens, c.OutputTokens, c.CostUSD,
		c.Verdict, trajectory, c.Error,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAgentCalls returns a project's calls newest first. since/until are
// inclusive ISO dates compared against the first 10 chars of started_at, the
// same lexicographic trick registry_get_audit uses.
func (s *store) ListAgentCalls(project, since, until string) ([]*agentCall, error) {
	query := `SELECT ` + agentCallColumns + ` FROM agent_calls WHERE project = ?`
	args := []any{project}
	if since != "" {
		query += ` AND substr(started_at, 1, 10) >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND substr(started_at, 1, 10) <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY id DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var calls []*agentCall
	for rows.Next() {
		c, err := scanAgentCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}

// SumCostSince is deliberately GLOBAL, not project-scoped: a daily spend
// ceiling is a property of the machine, not of one project.
func (s *store) SumCostSince(since string) (float64, int, error) {
	var total sql.NullFloat64
	var n int
	err := s.db.QueryRow(
		`SELECT COALESCE(SUM(cost_usd), 0), COUNT(*) FROM agent_calls
		  WHERE substr(started_at, 1, 10) >= ?`, since,
	).Scan(&total, &n)
	if err != nil {
		return 0, 0, err
	}
	return total.Float64, n, nil
}
