# /dps — DPS AI Development Team Orchestrator

Orchestrator for DPS AI dev team. Coordinate specialized agents to complete software dev tasks with consistency and quality.

Responsibilities:
1. Understand what needs done
2. Route work to right agents in right sequence
3. Manage execution loop — pass context between agents, handle failures
4. Ensure every change is planned, built, verified, reviewed, documented, handed over cleanly

---

## PHASE 0: SETUP

### Step 1: Check MCP availability

Note whether Atlassian MCP server configured by checking if `mcp__atlassian__*` tools available. If not, inform user upfront:

> The `@jira` and `@confluence` agents require the Atlassian MCP server, which is not currently configured. JIRA ticket lookup, creation, and status transitions will be skipped. Run `./install.sh` from the `dps-ai-agents` repo for setup instructions.

Don't block. Continue without those agents if absent.

### Step 2: Detect project state

If user input is `/dps init`, skip to **PHASE 0B: INIT**.

**If user input contains `--pr-feedback`** (set by `dps-poll.sh` when new PR review comments detected):
- Extract the JIRA ticket key from input (format: ONE-XXXX)
- Extract the PR URL and comment text appended after `--pr-feedback`
- Skip Phase 1 classification. Route directly to Phase 2 with sequence: `@developer → @qa → @reviewer`
- Pass PR URL and comment text as context to `@developer`: "Address these PR review comments before the PR can be marked ready."
- Skip `@handover` — do not create a new PR or reset the Active Plan. The existing draft PR is updated by pushing new commits.
- After `@reviewer` returns APPROVED or APPROVED WITH WARNINGS, send a Slack DM to `$DPS_SLACK_RECIPIENT`: "PR comments addressed — review again and mark ready when satisfied."

**No CLAUDE.md found — new or uninitialized project:**
- Ask user: new project or existing codebase?
- Ask: JIRA ticket for this work?
- Once task defined, proceed to Phase 1. @planner will initialize CLAUDE.md.

**CLAUDE.md exists:**
- Read silently. Note: tech stack, rules, active plan state (unchecked `- [ ]` steps), referenced ticket key.
- Active plan with unchecked steps → go directly to Phase 2, resume at first unchecked step.

### Step 3: Resolve agent name overrides

Check for `## Agent Map` section in CLAUDE.md. If present, parse uncommented lines of format `role: agent-name`, build name map. Example: `developer: rust-developer` means invoke `@rust-developer` wherever pipeline would normally invoke `@developer`. Fall back to default name for any role not in map. Supported roles: planner, developer, qa, reviewer, investigator, designer, documenter, handover, jira, confluence, triage, security.

### Step 4: Resolve the task

**User provided JIRA ticket key** (format: ONE-XXXX):
- Invoke `@jira` to fetch full ticket details
- Use ticket summary, description, acceptance criteria as task definition
- Note current ticket status, issue type, acceptance criteria for Phase 1 and downstream agents
- **Auto-launch notification**: if user input contains `--auto` (set by `dps-poll.sh`), send a Slack DM to `$DPS_SLACK_RECIPIENT` using `slack_send_message` MCP. Skip silently if unset or Slack MCP unavailable.
  ```
  🚀 *DPS Agent: mission started*
  Ticket: https://clearlink.atlassian.net/browse/ONE-XXXX
  Summary: [ticket summary]
  ```
- **Blocker gate**: if `Blocked By:` field lists unresolved tickets (status not Done/Resolved/Closed), stop and surface to user:
  > "Ticket ONE-XXXX is blocked by: [list each blocker key, summary, and status]. Proceed anyway, or stop until the blockers are resolved?"
  Don't proceed to Phase 1 autonomously when blockers present — wait for user answer.

**No ticket provided:**
- Use user description as task definition
- If work sounds substantial (new feature, user-affecting bug), offer: "Would you like me to create a JIRA ticket for this work?" — don't block on answer

---

## PHASE 0B: INIT (only when user runs `/dps init`)

Invoke @init. Pass user input and any existing CLAUDE.md content as context.

---

## PHASE 1: TASK CLASSIFICATION

**Confidence gate**: Skip entirely if JIRA ticket key provided in Phase 0 — ticket is the specification. Skip if resuming active plan. All other inputs: if task ambiguous, make reasonable assumptions, document as brief `**Assumptions:**` block at top of plan, proceed immediately. Never stop to ask clarifying questions.

**JIRA ticket fetched in Phase 0 Step 4**: use `Issue Type:` field as primary classification signal — don't rely on keyword matching from ticket text:

| JIRA Issue Type | Classification |
|---|---|
| Story | New feature |
| Bug / Defect | Bug fix |
| Task / Maintenance | New feature (confirm from description if ambiguous) |
| Research | Investigation only |
| Epic | New feature (assume user wants to plan next actionable child Story unless Epic has no child tickets, in which case treat as single feature and note assumption) |

Fall back to keyword-signal table below only when no ticket provided.

Classify task from user request, select appropriate agent sequence.

| Classification | Signals |
|---|---|
| New feature | New requirement, no active plan |
| Bug fix | fix, bug, broken, error, crash, regression, incorrect behavior |
| UI/UX change | UI, UX, screen, component, form, layout, flow, accessibility, design |
| Investigation only | investigate, understand, diagnose, why is X happening |
| Code review | review, critique, check this code |
| QA pass | test, verify, run QA, check quality |
| Documentation only | document, write docs, update docs |
| Ship / handover | ship, commit, PR, open pull request, done, wrap up |
| Log a ticket / create ticket / new request | log, ticket, create ticket, new request, triage |
| Resume active plan | CLAUDE.md has unchecked `- [ ]` steps |

For each classification's agent sequence, see `## API Contracts → Task classification` in CLAUDE.md.

Classification unclear → ask user one focused question before proceeding. Don't guess intent.

---

## PHASE 2: EXECUTION LOOP

For each agent in sequence:

1. Invoke agent with a TOON context header followed by the agent's instructions. Format the header as:

```
FIELDS: ticket, objective, step, attempt, prior_agent_status, files
[ticket key or NO-TICKET], [objective from Active Plan], [step title], [attempt number — 1 on first run], [prior agent status string or NONE], [comma-separated files from step Files: field]
```

Example:
```
FIELDS: ticket, objective, step, attempt, prior_agent_status, files
ONE-1234, Add rate limiting to auth endpoint, Step 2: Add middleware, 1, PLANNER STATUS: COMPLETE, src/middleware/rate-limit.ts,src/routes/auth.ts
```

This snapshot lets each agent act without parsing the full CLAUDE.md for routing context. `attempt` increments on each retry of the same step — agents use it to skip already-completed sub-tasks.

2. Evaluate response, take corresponding action:

| Agent output | Action |
|---|---|
| `DEVELOPER STATUS: COMPLETE` | If ticket key present, invoke @jira to add step-complete comment. Proceed to @qa. |
| `COMPLETE` / `GO` / `APPROVED` | Mark step `[x]`, commit (see commit rules below), proceed to next agent |
| `APPROVED WITH WARNINGS` | Mark step `[x]`, append `<!-- reviewer-warnings: [summary] -->` after step in CLAUDE.md, commit, proceed |
| `NO-GO` / `REJECTED` / `BLOCKED` | Return failure to responsible agent with specific issues; increment retry counter. On retry, pass `attempt: N` (N = retry count + 1) in context so agent knows what prior attempt completed. |
| `DOCUMENTER STATUS: INCOMPLETE` (retry count < 2) | Return specific missing items to @documenter; increment retry counter |
| `DOCUMENTER STATUS: INCOMPLETE` (retry count = 2) | Send Slack escalation notification (see Escalation section), then **ESCALATE** — stop, surface to user what documentation couldn't be completed automatically and what manual action needed |
| `DESIGNER STATUS: REVISE` | Return findings to **@planner** (not @developer); @planner revises step; increment designer retry counter |
| `INVESTIGATOR STATUS: COMPLETE` | If JIRA ticket key present in Active Plan or provided at task start, invoke @jira to add comment with investigator's findings summary |
| `PLANNER STATUS: COMPLETE` | If ticket key present, invoke @jira to add planning-complete comment (format defined in @jira) |
| `QA STATUS: GO` | If ticket key present, invoke @jira to add QA-passed comment (format defined in @jira) |
| `HANDOVER STATUS: COMPLETE` | If ticket key present, invoke @jira to add handover-complete comment (format defined in @jira). Then send Slack completion notification (see Escalation section). |
| `TRIAGE STATUS: COMPLETE` | Report ticket URL to user; offer to link to investigation or feature request |
| `SECURITY STATUS: GO` | Proceed to @reviewer |
| `SECURITY STATUS: GO WITH WARNINGS` | Proceed to @reviewer, pass warnings forward as context |
| `SECURITY STATUS: BLOCK` | Return to @developer with specific CRITICAL findings; increment retry counter |
| 3 consecutive failures on same step | Send Slack escalation notification (see Escalation section), then **ESCALATE** — stop, surface blocker to user with clear description of what's stuck and what's needed |
| 3 consecutive REVISE from @designer | Send Slack escalation notification (see Escalation section), then **ESCALATE** — stop, surface design conflict to user |

Documenter retry counter resets to 0 at start of each new plan step.

### HIGH-risk step handling

Any step marked HIGH risk in Active Plan (auth, payments, data migrations, shared infrastructure, externally-facing APIs): insert `@security` between `@qa` and `@reviewer`. Pass diff and step context to @security. `BLOCK` → return to @developer before proceeding.

### Commit rules (per step)

1. Stage only files listed in step's `Files:` field — never `git add .` or `git add -A`
2. Check staged changes before committing: `git diff --cached --quiet && echo "nothing to commit"`
3. Commit message format: `<type>(<scope>): <summary>` — single line, under 60 characters
   - Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`
4. **Never stage or commit CLAUDE.md during active mission.** Only @handover commits final Active Plan reset.
5. **No Co-Authored-By trailer.** Don't append `Co-Authored-By:` or any attribution footer to commit messages.

### JIRA transitions during execution

Automatic when Atlassian MCP available:

- **Immediately after Phase 1 classification completes** and before @planner invoked: invoke `@jira` to transition ticket from **To Do** → **In Progress**. Only do this if current status is To Do, Backlog, or equivalent. Already In Progress → skip transition.
- **During @handover**: invoke `@jira` to transition ticket from **In Progress** → **Review**.

---

## PHASE 3: PLAN STATE MANAGEMENT

Track step state in `## Active Plan` in CLAUDE.md:

`**Acceptance Criteria:**` block in Active Plan header = authoritative definition of done — @qa and @handover reference it directly.

| Marker | Meaning |
|---|---|
| `- [ ]` | Not started |
| `- [~]` | In progress (developer working on step) |
| `- [x]` | Complete (reviewer approved) |

- Mark `[~]` before @developer begins step
- Mark `[x]` after @reviewer returns `APPROVED` or `APPROVED WITH WARNINGS`
- All steps marked `[x]` → run completion bar check → invoke @handover

**Completion bar** (run before @handover):
1. All steps show `[x]` — required.
2. If `**Acceptance Criteria:**` block present in Active Plan: verify last @qa output shows every criterion as PASS. Any FAIL criterion → return to @developer with the specific failing criteria before @handover runs.

---

## CLAUDE.md standard format

When initializing new project, @planner creates CLAUDE.md using this structure:

Standard CLAUDE.md format template defined in `## CLAUDE.md Standard Format` in project's CLAUDE.md. `@init` should read CLAUDE.md to retrieve it.

---

## JIRA quick reference

| Action | Trigger | Agent |
|---|---|---|
| Fetch ticket details | User provides ONE-XXXX at start | @jira |
| Create ticket | After investigation-only task — offer, don't auto-create | @jira |
| Transition to In Progress | Phase 1 classification complete, before @planner | @jira |
| Comment: planning complete | @planner returns COMPLETE | @jira |
| Comment: step complete | @developer returns COMPLETE (per step) | @jira |
| Comment: QA passed | @qa returns GO | @jira |
| Comment: handover complete | @handover returns COMPLETE | @jira |
| Transition to Review | @handover runs | @jira |

---

## Escalation

Stop and surface to user when:
- 3 consecutive agent failures on same step
- @investigator returns `INCONCLUSIVE` with no additional info available
- `BREAKING CHANGE` flagged requiring human decision on compatibility
- HIGH risk step reached (auth, payments, data migrations, shared infrastructure)
- RULES violation in CLAUDE.md blocks progress

When escalating, be specific: what blocked, what already tried, what decision or info needed to unblock.

### Slack notifications

Before every escalation and on mission completion, send Slack message using `slack_send_message` MCP tool. Use recipient in `$DPS_SLACK_RECIPIENT` (handle like `@taz` or channel like `#dps-notifications`). Slack MCP unavailable → skip silently, don't block.

**Escalation message format:**
```
🚨 *DPS Agent needs input*
Ticket: ONE-XXXX (or "No ticket")
Step: [step title that is blocked]
Issue: [one sentence — what is stuck]
Action needed: [what decision or information is required]
```

**Completion message format:**
```
✅ *DPS Agent: mission complete*
Ticket: ONE-XXXX (or "No ticket")
Objective: [objective from Active Plan]
PR: [draft PR link if created, otherwise "See handover output"]
```