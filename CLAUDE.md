# CLAUDE.md — private-dotfiles

Personal dev utilities repo. Integrates JIRA, Confluence, Bitbucket, custom registry for workflow automation.

---

## Project

- **Name**: private-dotfiles
- **Workspace**: tazgreenwood
- **Repo**: tazgreenwood/private-dotfiles (Bitbucket)
- **Base branch**: main
- **PR target**: main

---

## Architecture

Codebase = user-facing skills + supporting MCP tools:

**Skills** (prompt files in `claude/skills/`):
- `plan.md` — auto-gen ticket keys via registry counter; make plans w/ acceptance criteria
- `ship.md` — review, test, doc, make PR, write audit trail
- `idea-validation.md` — agent-graph fan-out: idea-fleshing agent, then 4 parallel isolated research agents (competitor, market-trend, risk-assumption, technical-feasibility), synthesized into a self-contained HTML report

**Workflow scripts** (`claude/workflows/`):
- `build-workflow.js` — deterministic run-partitioning (sync/async), developer/QA retry loop, git-worktree lifecycle, sequential merge-back. Invoked by `build.md`, which is now a thin dispatcher.
- `code-review-workflow.js` — `--graph` mode's 4-dimension parallel fan-out + synthesis, as real control flow. Invoked by `code-review.md`.
- `ship-review-workflow.js` — security gate (HIGH-risk steps only) + `@reviewer` pass for `/ship`, as one deterministic call. Invoked by `ship.md`.

**MCP Servers**:
- **Registry Server** (`claude/mcp/server/`):
  - `registry.go` — MCP tool handlers for project metadata, plans, audit trail, issues, deploy checks, event log
  - `store.go` — SQLite-backed persistence layer (single DB at `~/.config/registry/data/registry.db`, WAL mode)
  - `main.go` — JSON-RPC dispatcher, MCP setup
- **Bitbucket Server** (`claude/mcp/bitbucket/`):
  - `bitbucket.go` — PR, branch, commit ops
  - `main.go` — JSON-RPC dispatcher, MCP setup

**Hooks** (`claude/hooks/`):
- `inject-registry-context.js` — UserPromptSubmit hook; reads project metadata + resources from registry; emits as system-reminder; dedupes per session.

**Key flows**:
1. **Plan**: Auto-increment fake ticket counter in registry; store plan JSON in `registry_write_plan`. Bug/Defect tickets get a root-cause gate via `@investigator` before step design.
2. **Build**: `build.md` loads the plan then runs `build-workflow.js` — deterministic run-partitioning, status tracking via `registry_update_step` (called from inside spawned subagents), STOP w/ error if registry down. Async runs (contiguous `plan_steps` sharing a `parallel_group`) execute concurrently via git-worktree isolation — one worktree per step — merged back into the feature branch sequentially in step-id order via `git merge --no-ff`.
3. **Ship**: `ship.md` runs `ship-review-workflow.js` for the security+reviewer pass, then writes a rich audit entry via `registry_write_audit`; skip JIRA transition if ticket key auto-gen; STOP w/ error if registry down
4. **Resource discovery**: Skills find external resources (dashboards, channels, repos, log groups) at runtime; save via `registry_set()` to resources subtree; hook injects on next session
5. **Event log**: `code-review.md` (and future interaction-producing skills) persist structured events via `registry_write_event`; viewable at `/projects/{name}/reviews` in registry-ui

**Registry hard dependency**: All phase skills (`/plan`, `/build`, `/ship`) need registry MCP server up. Registry down → skills STOP immediately, clear error, no fallback to local files. Keeps single source of truth, no data drift.

---

## Tech Stack

- **Language**: Go (MCP server), Markdown (skills/prompts), Python (hook utilities)
- **External APIs**:
  - Atlassian (JIRA, Confluence) — via `mcp__atlassian__*` tools
  - Slack — via `mcp__slack__*` tools
  - Bitbucket — via custom `bitbucket_*` tools
- **Data storage**: Single SQLite DB (WAL mode) at `~/.config/registry/data/registry.db`, replacing per-project JSON files
- **Dependencies**:
  - `modernc.org/sqlite` (pure-Go SQLite driver, no cgo, used by registry MCP server)
- **Test command**: `cd claude/ui && go test ./...`

---

## Commands

- Build registry MCP server: `cd claude/mcp/server && go build -o registry`
- Build bitbucket MCP server: `cd claude/mcp/bitbucket && go build -o bitbucket`
- Run server: `./registry` or `./bitbucket` (listens stdin/stdout, JSON-RPC)

---

## API Contracts

### MCP Tools

#### `registry_get_project(name: string, path?: string) -> map[string]any | error`
Returns project metadata. `path` given → returns value at dot-path (e.g. `"deploy.cluster"`).

```
Response: { "name": "...", "repo": {...}, "deploy": {...}, "ticket_counter": N, ... }
```

#### `registry_set(name: string, path: string, value: any) -> {ok: bool, path: string, value: any} | error`
Sets value in project metadata via dot-path. Makes intermediate objects as needed.

#### `registry_init_project(name: string, workspace?: string, localPath?: string, base?: string, prTarget?: string, profile?: string, cluster?: string, logGroup?: string, env?: string) -> {ok: bool, created: string, data: map[string]any} | error`
Makes new project entry, defaults on missing fields.

#### `registry_list_projects() -> {projects: []string}`
Lists all projects in registry.

#### `registry_list_plans(name: string) -> {plans: [{ticket: string, summary: string, status: "active"|"shipped"}]}`
Lists all plans for project. Status `"shipped"` if all plan steps status `"done"`.

#### `registry_get_plan(name: string, ticket: string) -> map[string]any | error`
Returns full plan JSON for ticket.

#### `registry_update_step(name: string, ticket: string, step_index: int, status: string) -> {ok: bool, step_index: int, status: string} | error`
Updates status of single step by zero-based index. Preferred over `registry_write_plan` for status-only changes — skips full plan round-trip.

Valid statuses: `pending`, `in_progress`, `done`, `blocked`.

#### `registry_write_plan(name: string, ticket: string, data: map[string]any) -> {ok: bool, file: string} | error`
Writes/updates plan file. Used by `/plan` to persist mission state.

**Plan step schema additions**: Each entry in `plan_steps[]` may carry:
- `execution`: `"sync"|"async"` (default `"sync"`) — `"async"` marks the step eligible for concurrent execution during `/build`.
- `parallel_group`: `int` — only meaningful when `execution` is `"async"`; identifies which contiguous block of async steps run concurrently together.
- `owner`: `"ai"|"human"` (default `"ai"`) — `"human"` marks a mechanical, unambiguous step the user does themselves; `/build` pauses before it (`status: "awaiting_human"`) instead of spawning `@developer`/`@qa`.
- `tdd`: `"required"|"optional"` (default `"required"`) — `"optional"` skips the failing-test-first step for config/docs/no-behavior-change steps.
- `model`: `"inherit"|"haiku"` (default `"inherit"`) — `"haiku"` routes that step's `@developer`/`@qa` agent calls to the cheap model; only for steps small/unambiguous enough that the plan's `how` fully specifies the work.

`registry_write_plan` validates async groups at write time (`validatePlanSteps`): rejects (returns an error, does not persist) any plan where async steps sharing a `parallel_group` have overlapping files, or where a step's files are missing/unknown.

#### `registry_write_audit(name: string, entry: map[string]any) -> {ok: bool, total_entries: int} | error`
Appends entry to project's audit log in registry. Caller gives all fields; `_recorded_at` (RFC3339) added auto.

**Audit entry schema** (per `ship.md` spec):
```json
{
  "ticket": "string",
  "type": "feature|bugfix|chore|refactor|docs|test",
  "summary": "string",
  "impact": "string",
  "branch": "string",
  "pr_url": "string",
  "files_changed": ["path/to/file", ...],
  "story_points": "number|null",
  "labels": ["string"],
  "ac_coverage": "M/N",
  "date": "YYYY-MM-DD"
}
```

#### `registry_get_resources(name: string, category?: string) -> {resources: map[string]any} | error`
Returns cached project resources, optionally filtered by category. Resources live under `resources` key in project metadata.

```
Response: { "resources": { "grafana": { "api_dashboard": "http://..." }, "slack": { "standup_channel": "C0XXX" }, "aws": { "log_group": "/app/logs" }, "bitbucket": { "repos": "mapi-js,emily" }, "scripts": { "find_recent_prs": { "command": "gh pr list --state merged --limit 10", "description": "List recently merged PRs", "learned_at": "2026-07-14T00:00:00Z" } } } }
```

**Scripts category schema**: Each entry under `resources.scripts.{name}` follows:
```json
{
  "command": "string",
  "description": "string",
  "learned_at": "string (RFC3339)"
}
```

`category` given (e.g. `"grafana"`) → only that category's entries returned:
```
Response: { "resources": { "grafana": { "api_dashboard": "http://..." } } }
```

Returns `{ "resources": {} }` if project has no resources stored yet.

#### `registry_get_audit(name: string, since?: string, until?: string) -> {entries: [map[string]any], total: int}`
Queries audit entries by date range. Dates ISO 8601 (YYYY-MM-DD), inclusive.

**Date comparison logic**: Grabs first 10 chars of `date` field (or `_recorded_at` if missing), does lexicographic string compare. Works fine — ISO dates sort chronologically.

#### `registry_report_issue(name: string, issue: map[string]any) -> {ok: bool, total_entries: int} | error`
Appends issue report entry to project's issue log in registry. Logs failures, blockers, incidents during skill execution.

**Issue schema**:
```json
{
  "ticket": "string",
  "severity": "critical|high|medium|low",
  "category": "string",
  "title": "string",
  "description": "string",
  "context": "string (optional)"
}
```

Caller gives all fields; `_reported_at` (RFC3339) added auto.

#### `registry_write_event(name: string, type: string, data: map[string]any, tags?: []string) -> {ok: bool, total_entries: int} | error`
Appends a structured interaction event to the project's event log. `type` examples: `pr_review`, `investigation`, `idea_validation`. `data` shape depends on `type` — no fixed schema, unlike audit/issues.

#### `registry_get_events(name: string, type?: string, since?: string, until?: string) -> {entries: [map[string]any], total: int}`
Queries the event log, optionally filtered by `type` and date range (ISO 8601, inclusive, matched against `occurred_at`).

#### `registry_derive_branch_name(ticket: string, ticket_type?: string, description?: string) -> {branch: string, prefix: string} | error`
Deterministic branch-name derivation: `ticket_type` maps to a prefix (Story/Task/Feature→`feat`, Bug/Defect→`fix`, Research→`research`, Refactor/Maintenance→`chore`; unrecognized types default to `chore`), `description` is slugified (lowercased, non-alphanumeric collapsed to `-`, truncated to 40 chars) and appended.

#### `registry_infer_audit_type(branch: string) -> {type: string}`
Maps a branch's prefix (before the first `/`) to an audit `type`: `feat`→`feature`, `fix`→`bugfix`, `chore`→`chore`, `refactor`→`refactor`, `docs`→`docs`, `test`→`test`. Unrecognized prefixes default to `chore`.

#### `registry_is_fake_ticket(name: string, ticket: string, prefix?: string) -> {is_fake: bool}`
Checks whether `ticket` is a registry auto-generated fake ticket (vs. a real JIRA key) by comparing its numeric suffix against the project's `ticket_counter`. `prefix` defaults to the last hyphen-segment of `name`, uppercased (e.g. `private-dotfiles` → `DOTFILES`) — override when a project's ticket prefix doesn't follow that convention.

#### `registry_union_files(file_groups: [[string]]) -> {files: [string]}`
Flattens and deduplicates multiple file-path arrays into one union, preserving first-occurrence order.

---

## Skills Status

| Skill | Invocation | Status | Dependencies |
|-------|-----------|--------|--------------|
| plan | `/plan` or `/plan ONE-XXXX` | ✓ Shipped | registry, jira (optional) |
| ship | `/ship` or `/ship ONE-XXXX` | ✓ Shipped | registry, bitbucket, jira, security, reviewer, documenter, handover |
| idea-validation | `/idea-validation` | ✓ Shipped | WebSearch, WebFetch, general-purpose agent |

---

## Decisions

Full architecture-decision history lives in the registry event log, not here — query via `registry_get_events("private-dotfiles", "decision")`. Migrated from this file on 2026-08-18 (11 entries, 2026-06-09 through 2026-08-18) to stop loading the whole growing log into every session. `@documenter` writes new decisions there going forward (see `documenter.md` section 3); it no longer appends full entries to this file.

---

## Domain Glossary

- **Ticket key**: Unique ID for work. Real JIRA keys (e.g. `ONE-1234`) or auto-gen fake keys (e.g. `DOTFILES-3`).
- **Fake ticket**: Auto-gen key for projects w/o JIRA integration. Uses registry counter.
- **Plan**: Mission state object w/ acceptance criteria, step breakdown, status. Stored as JSON in registry.
- **Audit entry**: Metadata about shipped work (ticket, type, impact, PR URL, files changed, story points, labels, date).
- **Registry**: Single SQLite DB (WAL mode) at `~/.config/registry/data/registry.db` w/ project metadata, plans, audit trails, issues, deploy checks. Single source of truth for mission state; phase skills (`/plan`, `/build`, `/ship`) hard-dependent on registry uptime (no local fallback).
- **Resource cache**: External integrations (Slack channels, Grafana dashboards, Bitbucket repos, AWS log groups, etc.) stored under `project.resources` in registry. Organized by category (grafana, slack, aws, bitbucket, confluence, jira, scripts).
- **Registry context**: System-reminder block emitted by `inject-registry-context` hook on session start; holds project metadata + all cached resources; used by skills to skip redundant API calls.
- **Skill**: User-invocable markdown prompt file routing commands to agents. All skills include SELF-IMPROVEMENT section for discovering + caching resources.
- **Agent**: Background orchestration logic (e.g. `@jira`, `@confluence`) invoked by skills.
- **Hook**: Node.js script (in `claude/hooks/`) registered in settings.json, runs at defined event (e.g. UserPromptSubmit) to inject context or setup.
- **Parallel group**: `int` identifying a contiguous block of async plan steps meant to run concurrently in `/build`; validated for non-overlapping files at plan-write time.
- **Async step**: A plan step marked `execution:"async"`; runs concurrently with other steps in the same `parallel_group`, each in an isolated git worktree, merged back into the feature branch in step-id order.
- **Agent-graph fan-out**: Pattern where a task is split across multiple independent, context-isolated subagents that run in parallel, each producing a partial result, then a dedicated synthesis step merges those outputs into one final artifact. Used in `code-review --graph` mode (4 dimension-reviewer agents + synthesis) and `idea-validation` (4 research agents + synthesis).
- **Workflow script**: A checked-in JS file under `claude/workflows/` run via the `Workflow` tool — real control flow (loops, retries, `parallel()`/`pipeline()`) instead of markdown prose an LLM re-derives each run. Used for `/build`'s internals (`build-workflow.js`), `code-review --graph` (`code-review-workflow.js`), and `/ship`'s security+reviewer pass (`ship-review-workflow.js`).
- **Event log**: `events` table in the registry, written via `registry_write_event(project, type, data, tags?)`. Generic across interaction types (`pr_review`, future `investigation`/`idea_validation`) rather than one bespoke table per type. Viewable at `/projects/{name}/reviews` in registry-ui (currently `pr_review` only).
- **Root-cause gate**: `/plan` STEP 3a — for Bug/Defect tickets, invokes `@investigator` before step design so the fix step's `Why:` cites the confirmed root cause instead of the raw ticket symptom. Skipped for obvious bugs (ticket already pinpoints the exact cause and a quick read confirms it) — see [2026-08-18] decision.
- **Human-owned step**: A plan step marked `owner:"human"`; `/build` pauses (`status: "awaiting_human"`) before it instead of spawning `@developer`/`@qa`, since the work is mechanical enough the user does it faster themselves.

---

## Confluence

No external Confluence pages maintained by this project. Docs inline in code + skill prompts.