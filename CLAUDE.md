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

**MCP Server** (`claude/mcp/server/`):
- `registry.go` — project metadata, plan storage, audit trail (uses `~/.config/clearlink-registry/data/`)
- `bitbucket.go` — PR, branch, and commit operations
- `main.go` — JSON-RPC dispatcher and MCP setup

**Key flows**:
1. **Plan**: Auto-increment fake ticket counter in registry; store plan JSON in `registry_write_plan`
2. **Ship**: Write rich audit entry via `registry_write_audit`; skip JIRA transition if ticket key is auto-generated
3. **Shipped**: Query audit entries via `registry_get_audit` with date range filtering

---

## Tech Stack

- **Language**: Go (MCP server), Markdown (skills/prompts)
- **External APIs**: 
  - Atlassian (JIRA, Confluence) — via `mcp__atlassian__*` tools
  - Slack — via `mcp__slack__*` tools
  - Bitbucket — via custom `bitbucket_*` tools
- **Data storage**: JSON files in `~/.config/clearlink-registry/data/`
- **Test command**: None (skills are prompt-based, no executable tests)

---

## Commands

- Build MCP server: `cd claude/mcp/server && go build -o clearlink-registry`
- Run server: `./clearlink-registry` (listens on stdin/stdout for JSON-RPC)

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

#### `registry_get_audit(name: string, since?: string, until?: string) -> {entries: [map[string]any], total: int}`
Queries audit entries by date range. Dates are ISO 8601 (YYYY-MM-DD) and inclusive.

**Date comparison logic**: Extracts first 10 chars of `date` field (or `_recorded_at` if missing) and does lexicographic string comparison. Works correctly for ISO dates because they sort chronologically.

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

---

## Domain Glossary

- **Ticket key**: Unique identifier for work. Real JIRA keys (e.g. `ONE-1234`) or auto-generated fake keys (e.g. `DOTFILES-3`).
- **Fake ticket**: Auto-generated key for projects without JIRA integration. Uses registry counter.
- **Plan**: Mission state object with acceptance criteria, step breakdown, and status. Stored as JSON in registry.
- **Audit entry**: Metadata about shipped work (ticket, type, impact, PR URL, files changed, story points, labels, date).
- **Registry**: Persistent key-value store in `~/.config/clearlink-registry/data/` with project metadata, plans, and audit trails.
- **Skill**: User-invocable markdown prompt file that routes commands to agents.
- **Agent**: Background orchestration logic (e.g. `@jira`, `@confluence`, `@standup`) invoked by skills.

---

## Confluence

No external Confluence pages are maintained by this project. Documentation is inline in code and skill prompts.
