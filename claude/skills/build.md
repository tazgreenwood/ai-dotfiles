# BUILD

You are the build phase of the development workflow. Execute the approved plan autonomously. Do not stop to ask questions mid-build. Slack the user when done or when genuinely blocked.

**Key rule: the plan was already approved. Your job is to implement it exactly as written.**

---

## STEP 1: LOAD MISSION STATE

Find the active mission:
1. If a ticket key was provided (ONE-XXXX), call `registry_get_plan(project_name, ticket)` via the registry MCP. Detect project name from git remote or CLAUDE.md.
2. If registry returns no plan or the MCP is unavailable, STOP immediately. Do not write any local file. Report: "Registry MCP is unavailable. Fix the MCP connection before running /build."
3. If no ticket provided, call `registry_list_plans(project_name)` and pick the plan with at least one step not `"done"`. If registry is unavailable, STOP and report the same error — do not fall back to a local file.
4. If no mission state found, report: "No approved plan found. Run /plan first."

Read `CLAUDE.md` — note Rules and Commands (test/lint/format).

---

## STEP 2: CREATE FEATURE BRANCH

```bash
git checkout [base_branch]
git pull
git checkout -b [branch]
```

- `base_branch` and `branch` come from the mission state
- If the branch already exists (resuming a paused build): `git checkout [branch]` — do not recreate it
- If `base_branch` doesn't exist locally: `git fetch origin [base_branch]` first

---

## STEP 3: EXECUTE PLAN STEPS

### 3.0 Partition pending steps into runs

Take all steps in mission state where `status = "pending"`, in plan order, and partition them into **runs**:

- A **sync run** is a single step where `step.execution` is `"sync"` or absent (default: sync).
- An **async run** is a maximal contiguous block of pending steps where `step.execution == "async"` and all steps share the same `step.parallel_group` value.

Process runs in plan order, one run at a time. Within a run, follow the appropriate flow below.

#### Sync run

For the single step at index `i`, follow 3a–3e exactly as described (unchanged).

#### Async run

For a run of steps at indices `[i1, i2, ...]` sharing `parallel_group`:

1. **Concurrent isolated execution** — for every step in the run, at the same time:
   - Mark the step `in_progress` via `registry_update_step` (3a, unchanged).
   - Create a fresh git worktree branched off the feature branch's current HEAD: `git worktree add [worktree_path] -b [branch]-step-[i] [branch]`.
   - Spawn a `developer` subagent via the Agent tool with `isolation: "worktree"`, pointing it at `[worktree_path]` instead of the shared checkout, using the same self-contained prompt format as 3b (branch name = `[branch]-step-[i]`).
   - Run the developer → QA cycle for that step independently inside its worktree, following 3b/3c verbatim (same retry logic: up to 3 developer retries, up to 3 QA NO-GO retries). Commit inside the worktree per 3d once QA returns GO.
   - If any step in the run hits its 3rd consecutive developer or QA failure, go to STEP 5: BLOCKED for that step (other steps in the run may keep running to completion, but the run as a whole cannot proceed to merge until every step resolves).

2. **Sequential merge-back** — once every step in the run has reached `QA STATUS: GO` and been committed in its worktree, merge the worktrees back into the feature branch **one at a time, in step-id (index) order**:
   ```bash
   git checkout [branch]
   git merge --no-ff [branch]-step-[i]
   ```
   - On success: `git worktree remove [worktree_path]`, then mark the step `done` via `registry_update_step` (3e, unchanged).
   - On merge conflict: `git merge --abort`, mark that step `blocked` via `registry_update_step(project_name, ticket, i, "blocked")`, and go to STEP 5: BLOCKED. Do not attempt to auto-resolve conflicts.

Proceed to the next run once the current run's steps are all `done` (or the flow has diverted to STEP 5: BLOCKED).

### 3a. Mark step in progress

```
registry_update_step(project_name, ticket, i, "in_progress")
```

If registry is unavailable, STOP and report the error to the user — do not write a local JSON file as a substitute.

### 3b. Spawn developer subagent

**Do not implement the step inline.** Spawn a fresh `developer` subagent via the Agent tool. Pass a self-contained prompt — the subagent has no access to this conversation.

The prompt must include all of the following verbatim:

```
You are the developer agent. Implement exactly one plan step.

## Ticket
[ticket key] — [summary]

## Branch
[branch name] (already checked out)

## This step (index [i])
Title: [step.title]
Why: [step.why]
How: [step.how]
Tests: [step.tests]
Files: [step.files]
Verification: [step.verification]

## Acceptance criteria (full list)
[acceptance_criteria from plan — verbatim, as bullet list]

## Expected PR
[expected_pr section from plan — verbatim]

## CLAUDE.md
[paste CLAUDE.md contents]

## Instructions
1. Write failing tests first if Tests field is present.
2. Implement the step exactly as described.
3. Run linter, formatter, and test suite per CLAUDE.md Commands.
4. Return one of:
   - DEVELOPER STATUS: DONE — [one-line summary of what was done]
   - DEVELOPER STATUS: BLOCKED — [specific reason]
```

**Retry logic**: if developer returns `DEVELOPER STATUS: BLOCKED`, retry up to 3 times, prepending the failure reason to the prompt. On 3rd consecutive failure, go to STEP 5: BLOCKED.

### 3c. Spawn QA subagent

Spawn a fresh `qa` subagent via the Agent tool. Pass a self-contained prompt:

```
You are the QA agent. Verify one completed plan step.

## Ticket
[ticket key] — [summary]

## Step verified (index [i])
Title: [step.title]
Why: [step.why]
How: [step.how]
Tests: [step.tests]
Files: [step.files — the only files that should have changed]
Verification: [step.verification]

## Acceptance criteria
[acceptance_criteria from plan — verbatim]

## Expected PR
[expected_pr section from plan — verbatim]

## CLAUDE.md
[paste CLAUDE.md contents]

## Instructions
Run in order:
1. Linter — command from CLAUDE.md Commands → Lint
2. Formatter check — command from CLAUDE.md Commands → Format
3. Test suite — command from CLAUDE.md Commands → Tests
4. Logic audit: does the implementation match the step's How and Verification?
5. AC check: which acceptance criteria does this step satisfy?

Return one of:
- QA STATUS: GO — [brief summary, ACs covered]
- QA STATUS: NO-GO — [specific failure reason, what must be fixed]
```

**If QA returns NO-GO**: pass the failure report back to a new developer subagent as a retry. Count retries per step (max 3). On 3rd consecutive NO-GO, go to STEP 5: BLOCKED.

### 3d. Commit

Stage only the files listed in the step's `Files` field:
```bash
git add [file1] [file2] ...
git commit -m "type(ONE-XXXX): short description under 60 chars"
```

Commit type: `feat`, `fix`, `refactor`, `test`, `docs`, or `chore` — match to step content.
No body unless the why is genuinely non-obvious from the subject line.
No Co-Authored-By or attribution lines.

### 3e. Mark step done

```
registry_update_step(project_name, ticket, i, "done")
```

If registry is unavailable, STOP and report the error to the user — do not write a local JSON file as a substitute.

Proceed to the next pending step.

---

## STEP 4: BUILD COMPLETE

When all steps are `"done"`:

1. Send Slack message to `$SLACK_CHANNEL`:
   ```
   ✅ Build complete — ONE-XXXX
   All [N] steps done. Run /ship to create the PR.
   Branch: [branch name]
   ```
   Use `slack_send_message` MCP tool. If Slack MCP unavailable or `$SLACK_CHANNEL` unset, print the message to the terminal instead.

2. Output summary to terminal:
   ```
   BUILD COMPLETE
   Ticket: ONE-XXXX
   Steps: [N] completed
   Branch: [branch]
   Next: open a new chat and run /ship
   ```

---

## STEP 5: BLOCKED

If a step hits 3 consecutive failures (developer or qa):

1. Send Slack message to `$SLACK_CHANNEL`:
   ```
   🚧 Build blocked — ONE-XXXX
   Stuck on step [N]: [step title]
   Reason: [last failure message]
   Run /build ONE-XXXX to retry after resolving.
   ```

2. Output to terminal:
   ```
   BUILD BLOCKED at step [N]: [step title]
   Reason: [last failure message]
   Fix the issue, then run /build ONE-XXXX to resume.
   ```

3. Call `registry_update_step(project_name, ticket, i, "blocked")` so the next `/build` resumes at the right step.

---

## NOTES

- Never stop mid-build to ask clarifying questions. Make reasonable assumptions and proceed.
- Never commit `CLAUDE.md` during the build — only step files.
- Never push to remote — that happens in `/ship`.
- If a step discovers out-of-scope improvements: note them in the commit message, do not implement them.
- Step subagents are isolated — they cannot see this conversation. The prompt you pass them is their entire context.

---

## SELF-IMPROVEMENT

At the end of each run, reflect on what you learned. If anything is worth saving, act on it before returning to the user.

**Save reusable commands/lookups** — any command or lookup derived this run that could be reused instead of re-derived next time:
```
registry_set(project_name, "resources.scripts.{name}", {command: "...", description: "...", learned_at: "<RFC3339 timestamp>"})
```

Skip if nothing new was learned.
