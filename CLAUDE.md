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
- `jira.md` — direct JIRA access (lookup, create, transition, comment)
- `confluence.md` — direct Confluence access (search, create, update pages)
- `standup.md` — synth Yesterday/Today/Blockers from JIRA + Slack
- `shipped.md` — work history report by month/quarter/year, optional narrative mode

**MCP Servers**:
- **Registry Server** (`claude/mcp/server/`):
  - `registry.go` — project metadata, plan storage, audit trail, issue reporting (uses `~/.config/registry/data/`)
  - `main.go` — JSON-RPC dispatcher, MCP setup
- **Bitbucket Server** (`claude/mcp/bitbucket/`):
  - `bitbucket.go` — PR, branch, commit ops
  - `main.go` — JSON-RPC dispatcher, MCP setup

**Hooks** (`claude/hooks/`):
- `inject-registry-context.js` — UserPromptSubmit hook; reads project metadata + resources from registry; emits as system-reminder; dedupes per session. Applies best-effort Headroom compression to context block via `tools/hooks/headroom_compress.py` subprocess; graceful fallback to raw output if python3 or headroom-ai unavailable (try/catch, no exception propagated).

**Key flows**:
1. **Plan**: Auto-increment fake ticket counter in registry; store plan JSON in `registry_write_plan`
2. **Build**: Load plan from registry, run steps w/ status tracking via `registry_update_step`, STOP w/ error if registry down
3. **Ship**: Write rich audit entry via `registry_write_audit`; skip JIRA transition if ticket key auto-gen; STOP w/ error if registry down
4. **Shipped**: Query audit entries via `registry_get_audit` w/ date range filter
5. **Resource discovery**: Skills find external resources (dashboards, channels, repos, log groups) at runtime; save via `registry_set()` to resources subtree; hook injects on next session

**Registry hard dependency**: All phase skills (`/plan`, `/build`, `/ship`, `/init`) need registry MCP server up. Registry down → skills STOP immediately, clear error, no fallback to local files. Keeps single source of truth, no data drift.

---

## Tech Stack

- **Language**: Go (MCP server), Markdown (skills/prompts), Python (hook utilities)
- **External APIs**:
  - Atlassian (JIRA, Confluence) — via `mcp__atlassian__*` tools
  - Slack — via `mcp__slack__*` tools
  - Bitbucket — via custom `bitbucket_*` tools
- **Data storage**: JSON files in `~/.config/registry/data/`
- **Dependencies**: `headroom-ai>=0.32.1` (Python package, optional for compression in `inject-registry-context.js`)
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
Lists all projects w/ `project.json` file.

#### `registry_list_plans(name: string) -> {plans: [{ticket: string, summary: string, status: "active"|"shipped"}]}`
Lists all plans for project. Status `"shipped"` if all plan steps status `"done"`.

#### `registry_get_plan(name: string, ticket: string) -> map[string]any | error`
Returns full plan JSON for ticket.

#### `registry_update_step(name: string, ticket: string, step_index: int, status: string) -> {ok: bool, step_index: int, status: string} | error`
Updates status of single step by zero-based index. Preferred over `registry_write_plan` for status-only changes — skips full plan round-trip.

Valid statuses: `pending`, `in_progress`, `done`, `blocked`.

#### `registry_write_plan(name: string, ticket: string, data: map[string]any) -> {ok: bool, file: string} | error`
Writes/updates plan file. Used by `/plan` to persist mission state.

#### `registry_write_audit(name: string, entry: map[string]any) -> {ok: bool, total_entries: int} | error`
Appends entry to `data/{project}/audit.json`. Caller gives all fields; `_recorded_at` (RFC3339) added auto.

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
Returns cached project resources, optionally filtered by category. Resources live under `resources` key in `project.json`.

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
Appends issue report entry to `data/{project}/issues.json`. Logs failures, blockers, incidents during skill execution.

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
- **Context**: Skills need to work w/o external JIRA tickets (personal projects). Counter must persist + auto-increment.
- **Decision**: Store `ticket_counter` in `registry_get_project(project_name)` under dot-path. Increment in-memory during plan phase, call `registry_set()` to persist.
- **Rejected alternatives**:
  - Separate counter file: more fragile, doesn't fit registry pattern.
  - Always require JIRA ticket: breaks offline workflows for personal repos.
- **Consequences**: Fake ticket keys (e.g. `DOTFILES-1`) never transition in JIRA; `ship.md` detects + skips JIRA transition if key matches auto-gen pattern.

### [2026-06-09] — Audit trail in registry, not separate tool
- **Context**: Need to track shipped work for monthly/quarterly reporting (perf reviews, reconciliation).
- **Decision**: Store audit entries in `data/{project}/audit.json` via `registry_write_audit()`. Query via `registry_get_audit()` w/ date range filter.
- **Rejected alternatives**:
  - Separate audit database: adds ops complexity, out of sync w/ plans.
  - Git log parsing: fragile, misses metadata (story points, labels, impact).
- **Consequences**: Each shipped plan writes exactly one audit entry. Skills query w/ lexicographic date compare (works — ISO 8601 sortable).

### [2026-06-09] — Skills are pure routing, not execution
- **Context**: Skills need to be composable, user-invocable, kept in sync w/ arch changes.
- **Decision**: Skills (markdown prompt files) act as routers: parse user input, invoke `@agent` (e.g. `@jira`, `@confluence`), return output direct. No logic duplication.
- **Rejected alternatives**:
  - Skills hold business logic: duplicates agents, harder to maintain.
  - Single monolithic skill: poor UX, hard to find subcommands.
- **Consequences**: Each skill ~30–50 lines MD. Agents (@jira, @confluence, @standup, etc.) hold actual business logic, update independently.

### [2026-06-22] — Hook-based resource injection, not per-skill queries
- **Context**: Skills need access to discovered external resources (Slack channels, Grafana dashboards, Bitbucket repos, log groups). Resources discovered at runtime, saved to registry, but need availability w/o extra API calls next run.
- **Decision**: `inject-registry-context.js` as UserPromptSubmit hook emits project metadata + all resources as system-reminder each session start. Skills check injected context first (zero cost), fall back to env vars, then interactive prompts. Skills discovering new resources save via `registry_set()`.
- **Rejected alternatives**:
  - Each skill calls `registry_get_resources()` on startup: adds API latency, couples to registry uptime; breaks offline workflows.
  - Pre-compute + cache resources in env vars: doesn't evolve w/ discoveries; needs manual sync.
  - Store in gitignored config files: fragile, per-machine, hard to reconcile.
- **Consequences**: Skills = stateless discovery engines. Registry = source of truth for external integrations. Hook dedupes per session (flag file), skips re-parse each prompt. New resources from one skill instant-available to others, no restart.

### [2026-07-14] — Registry is a hard dependency; no local fallback
- **Context**: Early skill impls fell back to local JSON files in `~/.claude/` when registry MCP down — dual sources of truth, data drift. Bugs from stale local data used over authoritative registry state.
- **Decision**: Registry MCP (`registry_write_plan`, `registry_get_plan`, `registry_list_plans`, `registry_update_step`, `registry_write_audit`) now mandatory hard dependency for `/plan`, `/build`, `/ship`. Skills depending on mission state/audit trail STOP immediately w/ clear error if registry MCP down. No fallback to local files.
- **Rejected alternatives**:
  - Dual-path w/ local fallback: data drift, audit trail inconsistency, silent fails on stale local data.
  - Offline mode w/ sync-on-reconnect: adds complexity, doesn't stop race conditions between offline edits + registry state.
- **Consequences**: Skills need working registry MCP connection to run. Simplifies data model, ensures single source of truth. Operators must ensure registry server up before `/plan`, `/build`, `/ship`, `/init`. Error msgs clear ("Registry MCP is unavailable. Fix the MCP connection before running /build.").

### [2026-07-22] — Agent model pinning: cheap models for lookups, expensive for reasoning
- **Context**: Agents serve different purposes (data lookup vs complex reasoning) with different cost/capability tradeoffs. Need consistent strategy to avoid overpaying for simple operations while ensuring reasoning agents have sufficient model capacity.
- **Decision**: Pin read-only/lookup agents (investigator, jira, confluence) to `claude-haiku-4-5-20251001` (cheap model). Explicitly document complex-reasoning agents (developer, planner, reviewer, security) as inheriting session model with frontmatter comment `# model: inherits session model (intentional — complex reasoning task)`.
- **Rejected alternatives**:
  - All agents same model: wastes budget on cheap read-only operations; expensive models on simple lookups.
  - All agents cheap model: breaks complex planning/review/security reasoning; false economy — cheap models fail on reasoning tasks.
- **Consequences**: Cost-efficient inference across agent fleet. Lookup operations run fast/cheap. Complex reasoning tasks inherit session model (typically Sonnet/Opus tier). New agents added going forward should be categorized + pinned accordingly (lookup → haiku, reasoning → inherit).

### [2026-07-23] — Best-effort context compression with graceful fallback, no local state
- **Context**: Registry context block injected at session start can grow large, risking token limit breaches. Need automatic compression, but can't add brittle mandatory dependencies or state management overhead.
- **Decision**: `inject-registry-context.js` wraps context in try/catch, calls `headroom_compress.py` subprocess (Python + headroom-ai) w/ 3s timeout. On success with non-empty output, use compressed block. On any failure (ImportError, timeout, headroom error), silently revert to original uncompressed block. No exception propagates; session always continues.
- **Rejected alternatives**:
  - Dual-path with cached compressed + raw files: adds state sync complexity, data drift, no reliability gain.
  - Mandatory compression: breaks if python3/headroom unavailable; unacceptable for CLI tool.
  - Skip compression: context unbounded, eventual token limit hit.
  - Async compression in background: complexity, timing unpredictability.
- **Consequences**: ~0–3s worst-case latency added to session start (subprocess spawn + compression timeout). Graceful degradation: tool always works. Headroom dependency is optional (try/catch fallback). Cost: one subprocess invocation per session.

---

## Domain Glossary

- **Ticket key**: Unique ID for work. Real JIRA keys (e.g. `ONE-1234`) or auto-gen fake keys (e.g. `DOTFILES-3`).
- **Fake ticket**: Auto-gen key for projects w/o JIRA integration. Uses registry counter.
- **Plan**: Mission state object w/ acceptance criteria, step breakdown, status. Stored as JSON in registry.
- **Audit entry**: Metadata about shipped work (ticket, type, impact, PR URL, files changed, story points, labels, date).
- **Registry**: Persistent key-value store in `~/.config/registry/data/` w/ project metadata, plans, audit trails. Single source of truth for mission state; phase skills (`/plan`, `/build`, `/ship`, `/init`) hard-dependent on registry uptime (no local fallback).
- **Resource cache**: External integrations (Slack channels, Grafana dashboards, Bitbucket repos, AWS log groups, etc.) stored under `project.resources` in registry. Organized by category (grafana, slack, aws, bitbucket, confluence, jira, scripts).
- **Registry context**: System-reminder block emitted by `inject-registry-context` hook on session start; holds project metadata + all cached resources; used by skills to skip redundant API calls.
- **Skill**: User-invocable markdown prompt file routing commands to agents. All skills include SELF-IMPROVEMENT section for discovering + caching resources.
- **Agent**: Background orchestration logic (e.g. `@jira`, `@confluence`, `@standup`) invoked by skills.
- **Hook**: Node.js script (in `claude/hooks/`) registered in settings.json, runs at defined event (e.g. UserPromptSubmit) to inject context or setup.

---

## Confluence

No external Confluence pages maintained by this project. Docs inline in code + skill prompts.