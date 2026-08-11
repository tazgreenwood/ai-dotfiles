# BUILD

You are the build phase of the development workflow. Execute the approved plan autonomously via a deterministic Workflow script. Do not stop to ask questions.

**Key rule: the plan was already approved. Your job is to run it exactly as written.**

---

## STEP 1: LOAD MISSION STATE

Find the active mission:
1. If a ticket key was provided (ONE-XXXX), call `registry_get_plan(project_name, ticket)` via the registry MCP. Detect project name from git remote or CLAUDE.md.
2. If registry returns no plan or the MCP is unavailable, STOP immediately. Do not write any local file. Report: "Registry MCP is unavailable. Fix the MCP connection before running /build."
3. If no ticket provided, call `registry_list_plans(project_name)` and pick the plan with at least one step not `"done"`. If registry is unavailable, STOP and report the same error — do not fall back to a local file.
4. If no mission state found, report: "No approved plan found. Run /plan first."

Read `CLAUDE.md` — capture its full contents (developer/QA subagents need Rules and Commands, but pass the whole file, not a summary).

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

## STEP 3: RUN THE BUILD WORKFLOW

Call the `Workflow` tool with:
- `scriptPath`: `claude/workflows/build-workflow.js`
- `args`: `{ project_name, ticket, summary, acceptance_criteria, expected_pr, claude_md, plan_data }`

Where `plan_data` is the full mission state object (same shape `/plan` wrote), `claude_md` is the CLAUDE.md contents read in STEP 1, and the rest are pulled from the same mission state for convenience.

The script partitions pending steps into sync/async runs, spawns `developer`/`qa` subagents per step with up to 3 retries each, commits per step, and for async runs creates isolated git worktrees, runs them concurrently, then merges back into the feature branch sequentially in step-index order. All registry step-status updates (`in_progress` / `done` / `blocked`) happen inside the spawned subagents, not in this skill.

Report to the user that the build is running in the background; they can watch live progress via `/workflows` or wait for the completion notification.

---

## STEP 4: REPORT RESULT

When the workflow completes, it returns `{ status: 'complete' | 'blocked', results, ... }`.

**If `status: 'complete'`:**
```
BUILD COMPLETE
Ticket: ONE-XXXX
Steps: [N] completed
Branch: [branch]
Next: open a new chat and run /ship
```

**If `status: 'blocked'`:**
```
BUILD BLOCKED at step [blocked step's index]: [step title]
Reason: [reason from the result]
Fix the issue, then run /build ONE-XXXX to resume.
```
(Steps already marked `"done"` before the block stay done — resuming re-partitions only the remaining `"pending"` steps.)

**If the workflow reports a merge conflict** (`mergeConflictAt` present): report the conflicting step's branch name and instruct the user to resolve it manually — the script does not auto-resolve.

---

## NOTES

- Never stop mid-build to ask clarifying questions. Make reasonable assumptions and proceed.
- Never commit `CLAUDE.md` during the build — only step files.
- Never push to remote — that happens in `/ship`.
- If a step discovers out-of-scope improvements: note them in the commit message, do not implement them.

---

## SELF-IMPROVEMENT

At the end of each run, reflect on what you learned. If anything is worth saving, act on it before returning to the user.

**Save reusable commands/lookups** — any command or lookup derived this run that could be reused instead of re-derived next time:
```
registry_set(project_name, "resources.scripts.{name}", {command: "...", description: "...", learned_at: "<RFC3339 timestamp>"})
```

**Improve this skill or the workflow script** — if a better approach was found, make a targeted minimal edit to `claude/skills/build.md` or `claude/workflows/build-workflow.js`. Edit only the specific line or section that was wrong or incomplete.

Skip if nothing new was learned.
