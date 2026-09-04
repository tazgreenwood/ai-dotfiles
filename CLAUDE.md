# CLAUDE.md — private-dotfiles

Personal dev utilities repo. Integrates JIRA, Confluence, Bitbucket, custom registry for workflow automation.

---

## Project

- **Name**: private-dotfiles
- **Workspace**: tazgreenwood
- **Repo**: `git@github.com:tazgreenwood/ai-dotfiles.git` (GitHub). The registry project key and local directory are both `private-dotfiles`; the GitHub repo is named `ai-dotfiles`. Use `gh`, not the Bitbucket MCP, for PRs here — the Bitbucket tooling in this repo serves *other* projects.
- **Base branch**: main
- **PR target**: main

---

## Architecture

Codebase = user-facing skills + supporting MCP tools:

**Skills** (prompt files in `claude/skills/`):
- `plan.md` — auto-gen ticket keys via registry counter; make plans w/ acceptance criteria
- `ship.md` — review, test, doc, make PR, write audit trail
- `idea-validation.md` — agent-graph fan-out: idea-fleshing agent, then 4 parallel isolated research agents (competitor, market-trend, risk-assumption, technical-feasibility), synthesized into a self-contained HTML report
- `lead.md` — **Stuart**, the team lead. `/lead <task>` in chat: he routes the task to a project, classifies it (investigation / bug-fix-or-feature / planning-only / ticket-only), and runs the matching skill(s) himself — `@investigator`, or `plan.md`→`build.md`→`ship.md`, or a direct in-chat plan, or `ticket.md`. No Slack inbox, no proposal/approval loop, no resumability — one task, one chat. Reports back (in chat, Slack-pinging only if Taz may be away) only on a genuine question, findings, or a PR ready. Simplified from an earlier unattended-sweep design on 2026-09-04 — see `registry_get_events("private-dotfiles", "decision")`.

**Shared skill references** (`claude/skills/_*.md`): files whose basename starts with `_` are reference docs that skills read, not invocable commands. `install.sh` links them **flat** into `~/.claude/skills/` rather than as a `<name>/SKILL.md` skill directory, so they never become a slash command and so skills can read them by an absolute path that resolves in any repo.
- `_record-agent-calls.md` — how `/ship`, `/code-review` and `/build` write `agent_calls` rows from a Workflow result. Lives in the callers because a workflow script cannot see its own agents' token usage.

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
- **Test commands** — three separate Go modules, each with its own `go.mod`. There is no single root command, and citing only the first is how DOTFILES-40 nearly ran its whole build against the wrong module:
  - `cd claude/ui && go test ./...` — registry-ui
  - `cd claude/mcp/server && go test ./...` — registry MCP server (store, tool handlers, plan validation)
  - `cd claude/mcp/bitbucket && go test ./...` — bitbucket MCP server

---

## Commands

- Run agent evals: `/eval` in a live session (manual, in-session only — no headless script; a `claude -p` subprocess can't approve the Workflow tool's permission gate, see `evals/README.md`). Drives the real `code-review-workflow.js` graph against `evals/fixtures/`, scores via `scripts/score-eval.js`; exits non-zero on regression. Costs real money.
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
Makes new project entry. **An omitted field is not left unset — it is stamped with a default**, and the defaults are this workspace's, not the caller's repo's: `workspace` → `$BITBUCKET_WORKSPACE` else `clearlinkit` (a *Bitbucket* workspace, written onto GitHub repos too), `base` → `production`, `prTarget` → `staging`, `profile` → `martech`, `cluster` → `general-production`, `logGroup` → `<name>-production`, `env` → `production`.

So "omit it rather than guess" is never available here: omitting *is* a guess, just an invisible one. A repo whose default branch is `master` registered without an explicit `base` points every later `/plan`, `/build` and `/ship` at a branch that does not exist. Callers must pass what they know (resolve the real default branch with `git -C <path> symbolic-ref --short refs/remotes/origin/HEAD`) and **report every value that was stamped rather than chosen**. Re-initialising an existing name is refused — `project '%s' already exists`; use `registry_set` to update fields.

**There is no delete counterpart.** Nothing in the MCP surface removes a project, so a wrong registration is permanent and degrades `registry_index()` routing for every later request until the database is edited by hand. Callers that register from an *inferred* target (rather than a human naming it) must corroborate the inference before writing — `lead.md` STEP 1 asks Taz to confirm before registering.

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

#### `registry_report_issue(project?, tool, error, severity?, context?) -> {ok: bool, total_entries: int} | error`
**This contract was documented wrongly until 2026-09-02 and the wrong version is not accepted by the server.** It is a *registry-failure* reporter, not a general issue log: its purpose is "a registry MCP call failed, record that". Real arguments, all top-level (NOT nested under an `issue` object):

- `tool` — name of the registry tool that failed (**required**)
- `error` — the error message (**required**)
- `project` — defaults to `"registry"`
- `severity` — `error` | `warning`, default `error`
- `context` — what you were trying to do

The old documented shape (`issue: {ticket, severity: critical|high|medium|low, category, title, description}`) fails input validation on `tool` and `error`. There is **no** general per-ticket issue log — for a blocker or an incident tied to a ticket, write an event instead: `registry_write_event(project, "ship_blocked"|"security_debt"|..., data, tags)`.

#### `registry_write_event(name: string, type: string, data: map[string]any, tags?: []string) -> {ok: bool, total_entries: int} | error`
Appends a structured interaction event to the project's event log. `type` examples: `pr_review`, `investigation`, `idea_validation`, `review_learning`. `data` shape depends on `type` — no fixed schema, unlike audit/issues.

#### `registry_get_events(name: string, type?: string, since?: string, until?: string) -> {entries: [map[string]any], total: int}`
Queries the event log, optionally filtered by `type` and date range (ISO 8601, inclusive, matched against `occurred_at`).

#### `registry_derive_branch_name(ticket: string, ticket_type?: string, description?: string) -> {branch: string, prefix: string} | error`
Deterministic branch-name derivation: `ticket_type` maps to a prefix (Story/Task/Feature→`feat`, Bug/Defect→`fix`, Research→`research`, Refactor/Maintenance→`chore`; unrecognized types default to `chore`), `description` is slugified (lowercased, non-alphanumeric collapsed to `-`, truncated to 40 chars) and appended.

#### `registry_infer_audit_type(branch: string) -> {type: string}`
Maps a branch's prefix (before the first `/`) to an audit `type`: `feat`→`feature`, `fix`→`bugfix`, `chore`→`chore`, `refactor`→`refactor`, `docs`→`docs`, `test`→`test`. Unrecognized prefixes default to `chore`.

#### `registry_is_fake_ticket(name: string, ticket: string, prefix?: string) -> {is_fake: bool}`
Checks whether `ticket` is a registry auto-generated fake ticket (vs. a real JIRA key) by comparing its numeric suffix against the project's `ticket_counter`. `prefix` defaults to the last hyphen-segment of `name`, uppercased (e.g. `private-dotfiles` → `DOTFILES`) — override when a project's ticket prefix doesn't follow that convention.

#### `registry_check_egress(path?: string, text?: string) -> {clean: bool, matched: [string]} | error`
The deterministic credential-shape screen Stuart's Slack pings run through before `chat.postMessage`. Go, not prose — a promise in a skill file enforces nothing.

**Prefer `path`.** The post path writes the body to a temp file and posts *that file*; `text` is whatever the caller retyped into this call. Those are two different strings and only one leaves the machine, so a transcription slip — or a deliberately mangled retype — passes a `text` check while the file still carries the credential. When `path` is present and non-empty its real bytes are screened and `text` is **ignored entirely**, not OR-ed in (OR-ing would let a dirty retype refuse a clean post and re-introduce the retyped string as an input). An empty `path` string means "not supplied", matching the COALESCE convention of the update tools.

**An unreadable `path` is an error, never a clean result.** "Could not check" and "checked, found nothing" are the same value to a caller that only reads `clean`, and collapsing them turns every unreadable body into a permitted one.

`clean: false` means **refuse, do not redact**: `matched` names the patterns that fired, in table order, and **never contains the matched text** — this value travels back through a tool result into a transcript, so echoing the secret to explain the refusal would recreate the leak the refusal exists to prevent. A caller therefore cannot know which span matched, by design. A clean result serializes `matched` as `[]`, never `null`.

Families in `egressPatterns` (each anchored on prefix **plus** a length/charset shape, never prefix-only, so the false-positive guard holds): `aws_access_key` (AKIA/ASIA + 16 alphanumerics), `slack_token` (`xox[abeprs]-` + 10+ chars), `slack_app_token` (`xapp-<digit>-<alphanumeric>+-<digit>-<32+ hex>` — a separate family; the `xox` pattern does not match it), `pem_private_key` (any PEM header `-----BEGIN ... PRIVATE KEY-----`), `github_pat` (`gh[opsur]_` + 20+ alphanumerics), `bearer_token` (literal keyword `Bearer` + 40+ token chars), `atlassian_api_token` (`ATATT` + 20+ base64-ish chars), `anthropic_api_key` (`sk-ant-` + 24+ alphanumerics/underscores/hyphens), `generic_sk_api_key` (`\bsk-` with optional `proj-` + 32+ alphanumerics, hyphen excluded from the body so it cannot match through prose like `risk-assumption-...`), `jwt` (`eyJ` + 8+ base64 + dot + 8+ base64 + dot + 8+ base64 — the two-dot three-segment structure, never `eyJ` alone), `google_api_key` (`AIza` + exactly 35 alphanumerics/underscores/hyphens), `api_key_assignment` (`api_key`/`api-key`/`X-API-Key` + `=`/`:` + exactly 40 hex digits — the keyword is required, since a bare 40-hex run is also every git object id's shape).

Unit-tested in `claude/mcp/server/registry_test.go`: table-driven positives per family, a false-positive guard over real proposal bodies, diffs, paths, Slack ts, git SHAs and base64/hex/URL near-misses, a test proving refusals never echo the secret, and wrapper tests covering `path`, the arg-validation error paths and dispatch registration. **A test of the pattern function is not evidence that the gate is invoked** — that each post site calls it is a separate claim, backed by the call sites in `lead.md`, not by these tests.

#### `registry_union_files(file_groups: [[string]]) -> {files: [string]}`
Flattens and deduplicates multiple file-path arrays into one union, preserving first-occurrence order.

#### `registry_index() -> {projects: [{name, purpose, repo, local_path, active_plan}]}`
Thin cross-project index backing `/lead`'s routing decision (STEP 1). One row per project; `active_plan` is `{ticket, summary}` for the newest non-shipped plan, or `null`. Deliberately carries **no** plan bodies, audit entries or `resources` subtree — the whole point is that choosing a project costs one small call instead of reading every project's `CLAUDE.md`. Two queries regardless of project count, never N+1. ~4KB across 8 projects.

`purpose` is a one-line project description stored in project metadata via `registry_set(name, "purpose", ...)`. It is the entire routing signal, so a vague one degrades routing silently. Near-identical names must be distinguishable from their purpose lines alone — `mapi` (local dev orchestrator, no product code) vs `mapi-server` (the Laravel API) vs `mapi-js` (the browser SDK) is the known collision.

#### `registry_write_call(name, workflow?, agent_label?, model?, status?, input_tokens?, output_tokens?, cost_usd?, verdict?, trajectory?, error?) -> {ok, id}`
#### `registry_get_calls(name, since?, until?) -> {calls: [...]}`
#### `registry_sum_cost(since) -> {cost_usd, run_count}`
`agent_calls` records per-invocation trajectory and cost — which model, how long, how many tokens, what it cost, and the tool-call trace, for one `/ship`, `/build` or `/code-review` run. Timestamps are stamped server-side when omitted, because workflow scripts cannot call `Date`.

**Note:** `registry_claim_proposal_for_build`, `agent_runs` (`registry_write_run`/`get_runs`/`update_run`), the `inbox` table (`registry_write_inbox`/`get_inbox`/`update_inbox`), `registry_worklist`, and the `proposals` table (`registry_write_proposal`/`get_proposals`/`update_proposal`) still exist as MCP tools on the registry server, but nothing in `claude/skills/` calls them as of the 2026-09-04 Stuart simplification (see `registry_get_events("private-dotfiles", "decision")`) — they backed the earlier unattended Slack-inbox/proposal/approval/resumable-run design. Left in place rather than torn out of the Go server in the same pass; a future cleanup ticket can drop them once nothing references the tables.

---

## Skills Status

| Skill | Invocation | Status | Dependencies |
|-------|-----------|--------|--------------|
| plan | `/plan` or `/plan ONE-XXXX` | ✓ Shipped | registry, jira (optional) |
| ship | `/ship` or `/ship ONE-XXXX` | ✓ Shipped | registry, bitbucket, jira, security, reviewer, documenter, handover |
| idea-validation | `/idea-validation` | ✓ Shipped | WebSearch, WebFetch, general-purpose agent |
| lead (Stuart) | `/lead <task>` | ✓ Shipped | registry, slack (bot token, ping-only), investigate, plan, build, ship, ticket |

---

## Decisions

Full architecture-decision history lives in the registry event log, not here — query via `registry_get_events("private-dotfiles", "decision")`. Migrated from this file on 2026-08-18 (11 entries, 2026-06-09 through 2026-08-18) to stop loading the whole growing log into every session. `@documenter` writes new decisions there going forward (see `documenter.md` section 3); it no longer appends full entries to this file.

---

## Domain Glossary

- **Ticket key**: Unique ID for work. Real JIRA keys (e.g. `ONE-1234`) or auto-gen fake keys (e.g. `DOTFILES-3`).
- **Fake ticket**: Auto-gen key for projects w/o JIRA integration. Uses registry counter.
- **Plan**: Mission state object w/ acceptance criteria, step breakdown, status. Stored as JSON in registry.
- **Audit entry**: Metadata about shipped work (ticket, type, impact, PR URL, files changed, story points, labels, date).
- **Registry**: Single SQLite DB (WAL mode) at `~/.config/registry/data/registry.db` w/ project metadata, plans, audit trails, issues, deploy checks, events, proposals. Single source of truth for mission state; phase skills (`/plan`, `/build`, `/ship`) hard-dependent on registry uptime (no local fallback).
- **Stuart**: The team lead persona (`claude/skills/lead.md`, invoked `/lead <task>`). Taz hands him a task directly in chat — investigation, bug fix, feature, planning, or just a ticket — and he routes it to a project, classifies it, and runs the matching existing skill(s) himself (`@investigator`, `plan.md`→`build.md`→`ship.md`, an in-chat plan, or `ticket.md`). Named a lead rather than an assistant on purpose: the prompt requires him to push back when a request is the wrong work, since deference is the failure mode that produces plausible plans for bad ideas. Renamed from "Jarvis" on 2026-09-01; simplified from an unattended Slack-inbox/proposal/approval design to a direct-task team-lead model on 2026-09-04 (see `registry_get_events("private-dotfiles", "decision")`) — no inbox, no proposals, no resumability, no worktree/concurrency dispatcher.
- **Agent call**: A row in `agent_calls` — one agent invocation with its model, timings, tokens, USD cost and trajectory, recorded for a `/ship`, `/build` or `/code-review` run. Recording outcomes without trajectories is the documented blind spot: an audit of 731 agent trajectories found 63% of *successful* resolutions retrieved the fix rather than deriving it, which is invisible if you only check whether the result looked right.
- **Project index**: The `registry_index()` view — name, one-line `purpose`, repo, `local_path` and active plan per project. Read by `/lead` STEP 1 to route a task to the right project without opening any other project's context. Ambiguity stops and asks Taz rather than guessing.
- **Resource cache**: External integrations (Slack channels, Grafana dashboards, Bitbucket repos, AWS log groups, etc.) stored under `project.resources` in registry. Organized by category (grafana, slack, aws, bitbucket, confluence, jira, scripts).
- **Registry context**: System-reminder block emitted by `inject-registry-context` hook on session start; holds project metadata + all cached resources; used by skills to skip redundant API calls.
- **Skill**: User-invocable markdown prompt file routing commands to agents. All skills include SELF-IMPROVEMENT section for discovering + caching resources.
- **Agent**: Background orchestration logic (e.g. `@jira`, `@confluence`) invoked by skills.
- **Hook**: Node.js script (in `claude/hooks/`) registered in settings.json, runs at defined event (e.g. UserPromptSubmit) to inject context or setup.
- **Parallel group**: `int` identifying a contiguous block of async plan steps meant to run concurrently in `/build`; validated for non-overlapping files at plan-write time.
- **Async step**: A plan step marked `execution:"async"`; runs concurrently with other steps in the same `parallel_group`, each in an isolated git worktree, merged back into the feature branch in step-id order.
- **Agent-graph fan-out**: Pattern where a task is split across multiple independent, context-isolated subagents that run in parallel, each producing a partial result, then a dedicated synthesis step merges those outputs into one final artifact. Used in `code-review --graph` mode (4 dimension-reviewer agents + synthesis) and `idea-validation` (4 research agents + synthesis).
- **Workflow script**: A checked-in JS file under `claude/workflows/` run via the `Workflow` tool — real control flow (loops, retries, `parallel()`/`pipeline()`) instead of markdown prose an LLM re-derives each run. Used for `/build`'s internals (`build-workflow.js`), `code-review --graph` (`code-review-workflow.js`), and `/ship`'s security+reviewer pass (`ship-review-workflow.js`).
- **Event log**: `events` table in the registry, written via `registry_write_event(project, type, data, tags?)`. Generic across interaction types (`pr_review`, `review_learning`, future `investigation`/`idea_validation`) rather than one bespoke table per type. Viewable at `/projects/{name}/reviews` in registry-ui (currently `pr_review` only — `review_learning` events aren't rendered there yet, query via `registry_get_events` directly).
- **Root-cause gate**: `/plan` STEP 3a — for Bug/Defect tickets, invokes `@investigator` before step design so the fix step's `Why:` cites the confirmed root cause instead of the raw ticket symptom. Skipped for obvious bugs (ticket already pinpoints the exact cause and a quick read confirms it) — see [2026-08-18] decision.
- **Human-owned step**: A plan step marked `owner:"human"`; `/build` pauses (`status: "awaiting_human"`) before it instead of spawning `@developer`/`@qa`, since the work is mechanical enough the user does it faster themselves.
- **Severity scale**: The 5-level finding grade used across `/code-review` (graph-mode `@review-dimension` agents) and `/ship` (`@reviewer`): **BLOCKER** (breaks prod, corrupts/loses data, opens a security hole, or violates a `## Rules` line in CLAUDE.md — blocks), **MAJOR** (real defect/design flaw causing a bug, outage, or expensive rework, not immediately catastrophic — blocks), **MINOR** (genuine issue, no correctness impact, e.g. dead code, misleading name, swallowed error context, weak test — never blocks), **NIT** (subjective polish, rendered `Nit:` — never blocks), **QUESTION** (reviewer can't tell without author input — never blocks). Only BLOCKER/MAJOR feed the code-computed verdict's REJECTED branch; MINOR/NIT are always informational. This CLAUDE.md entry is the documented source of truth for the 5 levels — the scale is duplicated verbatim in `claude/agents/review-dimension.md` and `claude/agents/reviewer.md` (agent system prompts must be self-contained), so drift between those files and this entry means one of them is stale.
- **Refuter pass**: The adversarial verification step in `code-review-workflow.js`'s Verify phase — every BLOCKER/MAJOR finding from the 4 `@review-dimension` agents is re-argued against by a separate `general-purpose` agent instructed to try to refute it using real code/call-sites/tests, not just the diff. A finding that can't be defended demotes to MINOR with a `demoted_from` note recording its original severity; it is never silently dropped. Only BLOCKER/MAJOR findings pay this cost — MINOR/NIT/QUESTION skip the refuter entirely.
- **Review learning**: An event of type `review_learning` (data: `{pattern, signal, action}`) written to a project's event log by `code-review-workflow.js`'s Learn phase — a recurring pattern or tooling gap surfaced by a review run, judged reusable across future reviews rather than specific to one diff. Fetched at the start of the next run (via `registry_get_events(project, "review_learning")`) and injected into the dimension/synthesis agent prompts as extra context; written at the end of a run only if an agent judges the run surfaced something generalizable — most runs write nothing. Requires `project_name` in the workflow's `args`; skipped silently if absent or the registry is unavailable. Kept in the registry event log rather than user memory because it's project-scoped recurring signal, not a cross-project user preference.
- **@review-dimension**: The graph-mode dimension-reviewer agent (`claude/agents/review-dimension.md`) invoked 4x in parallel by `code-review-workflow.js` (one per dimension: `bugs`, `security`, `scope`, `style`). Replaced `cavecrew-reviewer` on [2026-08-19] — see `registry_get_events("private-dotfiles", "decision")`. Has `Read`/`Glob`/`Grep`/`Bash` access and is instructed to open changed files in full and grep every call site rather than judge from diff lines alone; a finding it can't back with a file:line outside the diff is capped at QUESTION, not MAJOR/BLOCKER.

---

## Confluence

No external Confluence pages maintained by this project. Docs inline in code + skill prompts.