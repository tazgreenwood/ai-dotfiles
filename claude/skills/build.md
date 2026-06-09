# BUILD

You are the build phase of the development workflow. Execute the approved plan autonomously. Do not stop to ask questions mid-build. Slack the user when done or when genuinely blocked.

**Key rule: the plan was already approved. Your job is to implement it exactly as written.**

---

## STEP 1: LOAD MISSION STATE

Find the active mission:
1. If a ticket key was provided (ONE-XXXX), call `registry_get_plan(project_name, ticket)` via the clearlink-registry MCP. Detect project name from git remote or CLAUDE.md.
2. If registry returns no plan (or MCP unavailable), fall back to `~/.claude/plan-[TICKET].json` (or legacy `~/.claude/[TICKET].json`)
3. If no ticket provided, call `registry_list_plans(project_name)` and pick the plan with at least one step not `"done"`. Fall back to any `~/.claude/plan-*.json` or `~/.claude/[ticket-key].json` if registry unavailable.
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

Loop through all steps in mission state where `status = "pending"`. For each step:

### 3a. Mark step in progress
Call `registry_set(project_name, "plans.[ticket].plan_steps.[i].status", "in_progress")`.
If registry unavailable, update the local JSON file.

### 3b. Invoke @developer
Pass the following context:
- Current step (Why, How, Tests, Files, Verification)
- The `## Expected PR` section from the mission state (this is the acceptance spec)
- CLAUDE.md contents (Rules, Commands, API Contracts, Glossary)
- Ticket key and summary

@developer writes failing tests first (if Tests field present), then implements.

**Retry logic**: if @developer returns `DEVELOPER STATUS: BLOCKED`, retry up to 3 times passing the failure reason. On the 3rd consecutive failure, stop and Slack (see STEP 5: BLOCKED).

### 3c. Invoke @qa
Pass: changed files, current step details (why, how, tests, verification), `expected_pr` from mission state, and `acceptance_criteria` array from mission state.

@qa runs in order:
1. Linter — command from `## Commands → Lint` in CLAUDE.md
2. Formatter check — command from `## Commands → Format` in CLAUDE.md
3. Test suite — command from `## Commands → Tests` in CLAUDE.md
4. TDD verification — tests were written before implementation (check git diff order)
5. Logic audit and regression check
6. AC verification if present

**If @qa returns NO-GO**: pass the failure report back to @developer as a retry. Count retries per step (max 3). On 3rd consecutive NO-GO, stop and Slack (see STEP 5: BLOCKED).

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
Call `registry_set(project_name, "plans.[ticket].plan_steps.[i].status", "done")`.
If registry unavailable, update the local JSON file.

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

3. Leave mission state with the failed step as `"in_progress"` so the next `/build` invocation resumes at the right step.

---

## NOTES

- Never stop mid-build to ask clarifying questions. Make reasonable assumptions and proceed.
- Never commit `CLAUDE.md` during the build — only step files.
- Never push to remote — that happens in `/ship`.
- If a step discovers out-of-scope improvements: note them in the commit message, do not implement them.
