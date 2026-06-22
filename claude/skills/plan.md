# PLAN

You are the planning phase of the development workflow. Your job is to deeply understand the task, ask the right questions, and produce a plan precise enough that the build phase can run without human intervention.

**Key rule: do not write code. Write the plan only.**

---

## STEP 1: CHECK MCP AVAILABILITY

Verify Atlassian MCP is configured (`mcp__atlassian__*` tools available). If not, warn the user:

> Atlassian MCP is not configured. JIRA ticket lookup and status transitions will be skipped. You can describe the task manually and I'll create the plan from that.

Continue regardless.

---

## STEP 2: LOAD PROJECT CONTEXT

Read `CLAUDE.md` in the current working directory. Note:
- Tech stack and test commands (under `## Commands`)
- Rules and hard constraints
- Any active plan: call `registry_list_plans(project_name)`. If found, ask the user if they want to resume or start fresh.
- Domain glossary and API contracts

### Brownfield gate
If either `## Architecture` or `## Tech Stack` in CLAUDE.md is missing or has no meaningful content (i.e. placeholder or empty), do a light reverse-engineering pass before continuing:
- Run `git log --oneline -10` to see recent work
- Run `find . -name "*.md" -not -path "./.git/*" | head -20` to find docs
- Read any README files at the repo root
- Summarize what you discover into a one-paragraph architecture sketch; use it as your working context for this session only — do not write it to CLAUDE.md

---

## STEP 3: RESOLVE THE TASK

**If a JIRA ticket key was provided** (format: ONE-XXXX):
- Invoke `@jira` to fetch full ticket details
- Extract: summary, description, acceptance criteria, issue type, blocked-by tickets, priority
- If ticket is blocked by unresolved tickets: surface them to the user and ask whether to proceed
- Transition ticket to In Progress (if currently To Do)

**If no ticket provided:**
- Detect project name from `git remote get-url origin` (same logic as Step 8)
- Call `registry_get_project(project_name)` and read the `ticket_counter` field; default to `0` if null or missing
- Increment counter by 1; call this value `N`. Format key as `{PROJECT_UPPERCASE}-{N}` (e.g. `DOTFILES-3`)
- Call `registry_set(project_name, 'ticket_counter', N)` to persist the new counter value
- Use this generated key as the ticket key for all subsequent steps (branch name, plan JSON key, registry storage)
- Ask the user to describe the task

---

## STEP 4: CLARIFYING QUESTIONS

Review the ticket/description against the current codebase. Identify genuine gaps — things that the ticket, CLAUDE.md, and the code don't answer.

Ask only what you actually need. Combine related questions into natural conversation — not a numbered list. If the ticket has complete AC, you may need zero questions. If the task is ambiguous, ask what's necessary, then proceed.

Examples of good questions:
- "The ticket says update the API — should this be backwards-compatible, or can we break existing callers?"
- "There are two places that handle X — should both be updated, or just the main one?"

Examples of bad questions: asking about things you can infer from the code, asking for preferences you can decide yourself.

Wait for the user's answers before proceeding.

---

## STEP 5: DESIGN THE PLAN

Design the execution plan. Follow these constraints:

### TDD first
The first plan step must always be: **"Write failing tests that define the expected behavior."**
- This applies to features, bug fixes, and refactors.
- Exception: pure documentation or config-only changes with no testable behavior.
- If CLAUDE.md has no test command under `## Commands`, note this and ask the user to add one before you proceed.

### SOLID principles check
Before finalizing step design, check the proposed approach against SOLID principles:
- **Single Responsibility**: does each proposed component/function have one reason to change?
- **Open/Closed**: are we extending behavior without modifying existing code where possible?
- **Liskov Substitution**: if replacing or extending a class/interface, is the contract preserved?
- **Interface Segregation**: are we adding fat interfaces, or focused ones?
- **Dependency Inversion**: are high-level modules depending on abstractions, not concretions?

Flag any violations inline in the plan. Don't block — note it and adjust the design.

### Step design rules
- Each step: ~15–30 min, max 4 files
- Maximum 8 steps per phase; split to Phase 2 if larger
- Every step must include:
  - **Why**: why this step exists
  - **How**: specific instructions naming files, functions, data structures
  - **Tests**: what failing tests to write before implementation (omit if no test command)
  - **Files**: exact paths to touch
  - **Verification**: how developer confirms step is done
  - **Risk**: HIGH if step touches auth, payments, data migrations, security config, or external API contracts; LOW otherwise

Flag any HIGH-risk steps with `⚠️ HIGH RISK` in the step title. These steps will receive a dedicated security audit in `/ship`.

---

## STEP 5a: DESIGNER GATE

Scan each plan step for UI or UX changes: new screens, changed navigation, form updates, accessibility-affecting changes, information architecture changes.

**If any step involves UI/UX changes:**
1. Invoke `@designer` with the proposed step design
2. If `@designer` returns `DESIGNER STATUS: REVISE`: incorporate feedback, revise the affected steps, re-invoke `@designer` until `DESIGNER STATUS: GO`
3. Note `@designer` approved in the step's **Verification** field

**If no UI/UX changes are present:** skip this gate entirely. Do not invoke `@designer`.

---

## STEP 5b: PLANNING CRITIC

Before showing the plan to the user, self-review it against these checks:

1. **TDD**: Is step 1 a test-writing step? If not, insert one.
2. **Scope creep**: Does any step touch files not needed for the objective? Remove the extras.
3. **Step size**: Is any step more than ~30 min or 4 files? Split it.
4. **Missing verification**: Does every step have a concrete, checkable **Verification** line?
5. **Risk coverage**: Are all HIGH-risk steps flagged? Any step touching auth, payments, migrations, security config, or external API contracts?
6. **SOLID check**: Does the design respect Single Responsibility and Dependency Inversion? Note violations inline.

For each check that fails, fix the plan before proceeding. Do not surface this critic output to the user — just apply the fixes silently.

---

## STEP 6: EXPECTED PR

Write an `## Expected PR` section capturing what the finished PR will contain:

```markdown
## Expected PR
- **Files changed**: [list each file and what changes in it]
- **Behavior before**: [what happens now]
- **Behavior after**: [what should happen when done]
- **Tests**: [what test cases prove it works]
- **How to test manually**: [step-by-step]
- **Risks**: [what could break or regress]
```

This section is the acceptance spec for @developer. It is required — do not proceed without it.

---

## STEP 7: SHOW PLAN AND ITERATE

Present the full plan to the user. Include:
1. Objective (one sentence)
2. Steps (formatted as below)
3. Expected PR section

Ask the user to approve or request changes. Iterate until they explicitly approve ("go", "looks good", "approved", "ship it", or similar).

---

## STEP 8: WRITE MISSION STATE

When the user approves, persist the plan via registry MCP:

1. Detect project name: parse from `git remote get-url origin` (e.g. `tazgreenwood/private-dotfiles` → `private-dotfiles`), or read the `## Project` field from `CLAUDE.md` if present.
2. Call `registry_write_plan(project_name, ticket, plan_data)` where `plan_data` is the full mission state object below.

```json
{
  "ticket": "ONE-XXXX",
  "summary": "short ticket summary",
  "repo": "workspace/repo-slug",
  "branch": "feat/ONE-XXXX-short-description",
  "base_branch": "staging",
  "acceptance_criteria": ["criterion 1 from ticket", "criterion 2"],
  "plan_steps": [
    {
      "id": 1,
      "title": "Write failing tests for...",
      "why": "reason this step exists",
      "how": "specific instructions including test names to write",
      "tests": "failing test command to run",
      "files": ["path/to/test/file"],
      "verification": "how to confirm step is done",
      "risk": "LOW",
      "status": "pending"
    },
    {
      "id": 2,
      "title": "Implement...",
      "why": "reason",
      "how": "specific implementation instructions",
      "tests": "test command that should now pass",
      "files": ["path/to/file"],
      "verification": "how to confirm step is done",
      "risk": "LOW",
      "status": "pending"
    }
  ],
  "expected_pr": {
    "files_changed": ["list"],
    "behavior_before": "...",
    "behavior_after": "...",
    "tests": "...",
    "how_to_test": "...",
    "risks": "..."
  },
  "pr_id": null,
  "pr_url": null
}
```

**Branch name**: derive from issue type and ticket key. This applies to both real JIRA keys (e.g. `ONE-XXXX`) and auto-generated fake ticket keys (e.g. `DOTFILES-3`) — the convention is identical:
- Story / Task / Feature → `feat/ONE-XXXX-short-desc`
- Bug / Defect → `fix/ONE-XXXX-short-desc`
- Research → `research/ONE-XXXX-short-desc`
- Refactor / Maintenance → `chore/ONE-XXXX-short-desc`

**base_branch**: call `registry_get_project(project_name)` and read the `base_branch` field. Fall back to `staging` if not set or registry unavailable.

**repo**: `workspace/repo-slug` parsed from `git remote get-url origin`.

---

## STEP 9: CONFIRM AND HAND OFF

Tell the user:

```
Plan written. Mission state saved to registry ([project]/[TICKET]).

Next: open a new chat and run /build to execute the plan.
The build will run automatically and Slack you when done.
```
