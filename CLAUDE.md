# CLAUDE.md — private-dotfiles

This is a personal development utilities repo integrating JIRA, Confluence, Bitbucket, and a custom registry for workflow automation.

---

## Project

- **Name**: private-dotfiles
- **Workspace**: tazgreenwood
- **Repo**: tazgreenwood/private-dotfiles (Bitbucket)
- **Base branch**: main
- **PR target**: main

---

## Architecture

The codebase provides a suite of user-facing skills and supporting MCP tools:

**Skills** (prompt files in `claude/skills/`):
- `plan.md` — auto-generate ticket keys via registry counter; create plans with acceptance criteria
- `ship.md` — review, test, document, create PR, and write audit trail
- `jira.md` — direct JIRA access (lookup, create, transition, comment)
- `confluence.md` — direct Confluence access (search, create, update pages)
- `standup.md` — synthesize Yesterday/Today/Blockers from JIRA + Slack
- `shipped.md` — work history report by month/quarter/year with optional narrative mode

**MCP Servers**:
- **Registry Server** (`claude/mcp/server/`):
  - `registry.go` — project metadata, plan storage, audit trail, issue reporting (uses `~/.config/registry/data/`)
  - `main.go` — JSON-RPC dispatcher and MCP setup
- **Bitbucket Server** (`claude/mcp/bitbucket/`):
  - `bitbucket.go` — PR, branch, and commit operations
  - `main.go` — JSON-RPC dispatcher and MCP setup

**Hooks** (in `claude/hooks/`):
- `inject-registry-context.js` — UserPromptSubmit hook; reads project metadata and resources from registry; emits as system-reminder; dedupes per session

**Key flows**:
1. **Plan**: Auto-increment fake ticket counter in registry; store plan JSON in `registry_write_plan`
2. **Build**: Load plan from registry, execute steps with status tracking via `registry_update_step`, STOP with error if registry unavailable
3. **Ship**: Write rich audit entry via `registry_write_audit`; skip JIRA transition if ticket key is auto-generated; STOP with error if registry unavailable
4. **Shipped**: Query audit entries via `registry_get_audit` with date range filtering
5. **Resource discovery**: Skills discover external resources (dashboards, channels, repos, log groups) at runtime; save via `registry_set()` to resources subtree; hook injects these on next session

**Registry is a hard dependency**: All phase skills (`/plan`, `/build`, `/ship`, `/init`) require the registry MCP server to be available. If registry is unavailable, skills STOP immediately with a clear error message — there is no fallback to local files. This ensures single source of truth and prevents data drift.

---

## Tech Stack

- **Language**: Go (MCP server), Markdown (skills/prompts)
- **External APIs**: 
  - Atlassian (JIRA, Confluence) — via `mcp__atlassian__*` tools
  - Slack — via `mcp__slack__*` tools
  - Bitbucket — via custom `bitbucket_*` tools
- **Data storage**: JSON files in `~/.config/registry/data/`
- **Test command**: `cd claude/ui && go test ./...`

---

## Commands

- Build registry MCP server: `cd claude/mcp/server && go build -o registry`
- Build bitbucket MCP server: `cd claude/mcp/bitbucket && go build -o bitbucket`
- Run server: `./registry` or `./bitbucket` (listens on stdin/stdout for JSON-RPC)

---

## API Contracts

### MCP Tools

#### `registry_get_project(name: string, path?: string) -> map[string]any | error`
Returns project metadata. If `path` is provided, returns the value at that dot-path (e.g. `"deploy.cluster"`).

```
Response: { "name": "...", "repo": {...}, "deploy": {...}, "ticket_counter": N, ... }
```

#### `registry_set(name: string, path: string, value: any) -> {ok: bool, path: string, value: any} | error`
Sets a value in project metadata using dot-path notation. Creates intermediate objects as needed.

#### `registry_init_project(name: string, workspace?: string, localPath?: string, base?: string, prTarget?: string, profile?: string, cluster?: string, logGroup?: string, env?: string) -> {ok: bool, created: string, data: map[string]any} | error`
Creates a new project entry with defaults applied to missing fields.

#### `registry_list_projects() -> {projects: []string}`
Lists all projects with a `project.json` file.

#### `registry_list_plans(name: string) -> {plans: [{ticket: string, summary: string, status: "active"|"shipped"}]}`
Lists all plans for a project. Status is `"shipped"` if all plan steps have status `"done"`.

#### `registry_get_plan(name: string, ticket: string) -> map[string]any | error`
Returns the full plan JSON for a ticket.

#### `registry_update_step(name: string, ticket: string, step_index: int, status: string) -> {ok: bool, step_index: int, status: string} | error`
Updates the status of a single step by zero-based index. Preferred over `registry_write_plan` for status-only changes — no full plan round-trip.

Valid statuses: `pending`, `in_progress`, `done`, `blocked`.

#### `registry_write_plan(name: string, ticket: string, data: map[string]any) -> {ok: bool, file: string} | error`
Writes or updates a plan file. Used by `/plan` to persist mission state.

#### `registry_write_audit(name: string, entry: map[string]any) -> {ok: bool, total_entries: int} | error`
Appends an entry to `data/{project}/audit.json`. Caller provides all fields; `_recorded_at` (RFC3339) is added automatically.

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
Returns cached project resources, optionally filtered by category. Resources live under the `resources` key in `project.json`.

```
Response: { "resources": { "grafana": { "api_dashboard": "http://..." }, "slack": { "standup_channel": "C0XXX" }, "aws": { "log_group": "/app/logs" }, "bitbucket": { "repos": "mapi-js,emily" } } }
```

When `category` is provided (e.g. `"grafana"`), only that category's entries are returned:
```
Response: { "resources": { "grafana": { "api_dashboard": "http://..." } } }
```

Returns `{ "resources": {} }` if the project has no resources stored yet.

#### `registry_get_audit(name: string, since?: string, until?: string) -> {entries: [map[string]any], total: int}`
Queries audit entries by date range. Dates are ISO 8601 (YYYY-MM-DD) and inclusive.

**Date comparison logic**: Extracts first 10 chars of `date` field (or `_recorded_at` if missing) and does lexicographic string comparison. Works correctly for ISO dates because they sort chronologically.

#### `registry_report_issue(name: string, issue: map[string]any) -> {ok: bool, total_entries: int} | error`
Appends an issue report entry to `data/{project}/issues.json`. Used to log failures, blockers, or incidents during skill execution.

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

Caller provides all fields; `_reported_at` (RFC3339) is added automatically.

---

## Skills Status

| Skill | Invocation | Status | Dependencies |
|-------|-----------|--------|--------------|
| plan | `/plan` or `/plan ONE-XXXX` | ✓ Shipped | registry, jira (optional) |
| ship | `/ship` or `/ship ONE-XXXX` | ✓ Shipped | registry, bitbucket, jira, security, reviewer, documenter, handover |
| jira | `/jira` + subcommand | ✓ Shipped | mcp__atlassian__* |
| confluence | `/confluence` + subcommand | ✓ Shipped | mcp__atlassian__* |
| standup | `/standup` | ✓ Shipped | mcp__atlassian__*, mcp__slack__* (optional) |
| shipped | `/shipped` + optional flags | ✓ Shipped | registry, mcp__atlassian__* (optional) |

---

## Decisions

### [2026-06-09] — Auto-generated ticket counter via registry
- **Context**: Skills need to work without external JIRA tickets (for personal projects). A counter must persist and auto-increment.
- **Decision**: Store `ticket_counter` in `registry_get_project(project_name)` under dot-path. Increment in-memory during plan phase, call `registry_set()` to persist.
- **Rejected alternatives**: 
  - Separate counter file: more fragile, doesn't integrate with registry pattern.
  - Always require JIRA ticket: breaks offline workflows for personal repos.
- **Consequences**: Fake ticket keys (e.g. `DOTFILES-1`) never transition in JIRA; `ship.md` detects and skips JIRA transition if key matches auto-generated pattern.

### [2026-06-09] — Audit trail in registry, not separate tool
- **Context**: Need to track shipped work for monthly/quarterly reporting (perf reviews, reconciliation).
- **Decision**: Store audit entries in `data/{project}/audit.json` via `registry_write_audit()`. Query via `registry_get_audit()` with date range filtering.
- **Rejected alternatives**:
  - Separate audit database: adds operational complexity, out of sync with plans.
  - Git log parsing: fragile, doesn't capture metadata (story points, labels, impact).
- **Consequences**: Each plan that ships writes exactly one audit entry. Skills can query with lexicographic date comparison (works because ISO 8601 is sortable).

### [2026-06-09] — Skills are pure routing, not execution
- **Context**: Skills need to be composable, user-invocable, and kept in sync with architectural changes.
- **Decision**: Skills (markdown prompt files) act as routers: parse user input, invoke `@agent` (e.g. `@jira`, `@confluence`), return output directly. No logic duplication.
- **Rejected alternatives**:
  - Skills contain business logic: creates duplication with agents, harder to maintain.
  - Single monolithic skill: poor UX, hard to find subcommands.
- **Consequences**: Each skill is ~30–50 lines of MD. Agents (@jira, @confluence, @standup, etc.) contain actual business logic and can be updated independently.

### [2026-06-22] — Hook-based resource injection, not per-skill queries
- **Context**: Skills need access to discovered external resources (Slack channels, Grafana dashboards, Bitbucket repos, log groups). Resources can be discovered at runtime and saved to registry, but need to be available without extra API calls on next run.
- **Decision**: Implement `inject-registry-context.js` as a UserPromptSubmit hook that emits project metadata and all resources as a system-reminder on each session start. Skills check this injected context first (zero cost), then fall back to environment variables, then interactive prompts. Skills that discover new resources save them via `registry_set()`.
- **Rejected alternatives**:
  - Each skill calls `registry_get_resources()` on startup: adds API latency and coupling to registry availability; breaks offline workflows.
  - Pre-compute and cache resources in env vars: doesn't evolve with discoveries; requires manual sync.
  - Store in gitignored config files: fragile, per-machine, hard to reconcile.
- **Consequences**: Skills are stateless discovery engines. Registry is the source of truth for external integrations. Hook dedupes per session (flag file) to avoid re-parsing on each prompt. New resources discovered by one skill are immediately available to others without restart.

### [2026-07-14] — Registry is a hard dependency; no local fallback
- **Context**: Early skill implementations fell back to local JSON files in `~/.claude/` when registry MCP was unavailable, creating dual sources of truth and data drift. This led to bugs where stale local data was used instead of authoritative registry state.
- **Decision**: Registry MCP (`registry_write_plan`, `registry_get_plan`, `registry_list_plans`, `registry_update_step`, `registry_write_audit`) is now a mandatory hard dependency for `/plan`, `/build`, and `/ship`. All skills that depend on mission state or audit trail now STOP immediately with a clear error message if the registry MCP is unavailable. No fallback to local files.
- **Rejected alternatives**:
  - Dual-path with local fallback: creates data drift, audit trail inconsistency, and silent failures when local data is stale.
  - Offline mode with sync-on-reconnect: adds complexity, doesn't prevent race conditions between offline edits and registry state.
- **Consequences**: Skills require a working registry MCP connection to run. This simplifies the data model and ensures single source of truth. Operators must ensure the registry server is available before invoking `/plan`, `/build`, `/ship`, or `/init`. Error messages are clear ("Registry MCP is unavailable. Fix the MCP connection before running /build.").

---

## Domain Glossary

- **Ticket key**: Unique identifier for work. Real JIRA keys (e.g. `ONE-1234`) or auto-generated fake keys (e.g. `DOTFILES-3`).
- **Fake ticket**: Auto-generated key for projects without JIRA integration. Uses registry counter.
- **Plan**: Mission state object with acceptance criteria, step breakdown, and status. Stored as JSON in registry.
- **Audit entry**: Metadata about shipped work (ticket, type, impact, PR URL, files changed, story points, labels, date).
- **Registry**: Persistent key-value store in `~/.config/registry/data/` with project metadata, plans, and audit trails. Single source of truth for mission state; phase skills (`/plan`, `/build`, `/ship`, `/init`) are hard-dependent on registry availability (no local fallback).
- **Resource cache**: External integrations (Slack channels, Grafana dashboards, Bitbucket repos, AWS log groups, etc.) stored under `project.resources` in registry. Organized by category (grafana, slack, aws, bitbucket, confluence, jira).
- **Registry context**: System-reminder block emitted by `inject-registry-context` hook on session start; contains project metadata and all cached resources; used by skills to avoid redundant API calls.
- **Skill**: User-invocable markdown prompt file that routes commands to agents. All skills include a SELF-IMPROVEMENT section for discovering and caching resources.
- **Agent**: Background orchestration logic (e.g. `@jira`, `@confluence`, `@standup`) invoked by skills.
- **Hook**: Node.js script (in `claude/hooks/`) registered in settings.json that runs at a defined event (e.g. UserPromptSubmit) to inject context or perform setup.

---

## Confluence

No external Confluence pages are maintained by this project. Documentation is inline in code and skill prompts.
