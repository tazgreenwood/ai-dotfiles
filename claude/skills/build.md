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

Read `CLAUDE.md`. Extract only the `## Rules`, `## Commands`, and `## Domain Glossary` sections verbatim (not summarized) — these are the only sections `build-workflow.js`'s dev/QA prompts actually use, and this text gets re-embedded in every developer/QA agent call across every step and retry, so skipping Architecture/Decisions/API Contracts here is a real per-call token saving, not just a read-time one. If a step's `how` references an interface documented under `## API Contracts`, include that specific contract entry too — otherwise omit the section entirely.

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

When the workflow completes, it returns `{ status: 'complete' | 'blocked' | 'awaiting_human', results, ... }`.

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

**If `status: 'awaiting_human'`:**
```
BUILD PAUSED at step [awaitingHumanAt]: [step title] — owner: human
Why: [step's why]
How: [step's how]
Files: [step's files]
Verification: [step's verification]

Do this step yourself, then run /build ONE-XXXX to continue.
```
The step is not marked `"done"` automatically — call `registry_update_step` yourself when finished (or just re-run `/build`, which will re-check it next time and still show it as pending if you forgot).

**If the workflow reports a merge conflict** (`mergeConflictAt` present): report the conflicting step's branch name and instruct the user to resolve it manually — the script does not auto-resolve.

---

## NOTES

- Never stop mid-build to ask clarifying questions. Make reasonable assumptions and proceed.
- Never commit `CLAUDE.md` during the build — only step files.
- Never push to remote — that happens in `/ship`.
- If a step discovers out-of-scope improvements: note them in the commit message, do not implement them.

---

## SELF-IMPROVEMENT

End of run: save any new reusable command/lookup via `registry_set(project_name, "resources.scripts.{name}", {command: "...", description: "...", learned_at: "<RFC3339 timestamp>"})`, and make a targeted edit to `claude/skills/build.md` or `claude/workflows/build-workflow.js` if a better approach was found. Skip if nothing new was learned.
