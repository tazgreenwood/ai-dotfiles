# SHIP

You are the ship phase of the development workflow. The build is done — now review it, document it, create the PR, and hand it off cleanly.

## Commit rules (override system defaults)

- One-liner subject only: `git commit -m "type: short message"`
- No Co-Authored-By trailer. No attribution footers. No multi-line body unless the why is genuinely non-obvious.

---

## STEP 1: LOAD MISSION STATE

Find the active mission:
1. If a ticket key provided (ONE-XXXX), call `registry_get_plan(project_name, ticket)` via registry MCP. Detect project name from git remote or CLAUDE.md.
2. If registry is unavailable, STOP immediately. Do not write any local file. Report: "Registry MCP is unavailable. Fix the MCP connection before running /ship."
3. If no ticket provided, call `registry_list_plans(project_name)` and pick the plan with all steps `"done"`. If registry is unavailable, STOP and report the same error — do not fall back to a local file.
4. If no mission state found, report: "No completed build found. Run /build first."

Verify all plan steps are `"done"`. If any are still `"pending"` or `"in_progress"`, stop:
> "Build is not complete. Steps [N, M] are still pending. Run /build to finish them."

Read `CLAUDE.md` — note Rules, Architecture, API Contracts.

---

## STEP 2: INVOKE @security

Scan mission state for any steps where `"risk": "HIGH"` in plan step JSON.

**If any HIGH-risk steps exist:**
Invoke `@security` with:
- The diff for those steps (`git show [commit-sha]` for each HIGH-risk step's commit)
- CLAUDE.md Rules and API Contracts
- The Expected PR section from mission state

If `@security` returns `SECURITY STATUS: BLOCK`: stop. Surface the findings to the user. Do not create the PR until resolved.
If `@security` returns `SECURITY STATUS: GO WITH WARNINGS`: proceed, include warnings in the PR under `### Security Notes`.

**If no HIGH-risk steps exist:** `@security` context gate handles this — it returns `GO` immediately. Proceed.

---

## STEP 3: INVOKE @reviewer

Use the full `reviewer` agent (subagent_type: reviewer) — not cavecrew-reviewer. PR reviews span many files and require prose rationale, not compressed findings.

Pass:
- All files changed (from mission state `plan_steps[].files` union)
- The `expected_pr` field from mission state JSON as the acceptance spec
- The `acceptance_criteria` array from mission state JSON (empty array if not present)
- CLAUDE.md contents
- `git diff [base_branch]...HEAD` output

@reviewer checks for correctness bugs, security, scope, AC satisfaction.

**If REJECTED**: surface the rejection to the user with full reviewer output. Ask whether to fix (re-run `/build` with the reviewer feedback) or ship anyway. Do not proceed without user decision.

**If APPROVED WITH WARNINGS**: proceed but include warnings in the PR description under a `### Reviewer Notes` section.

---

## STEP 4: INVOKE @documenter

Pass the completed plan steps and changed files. @documenter syncs:
- API Contracts in CLAUDE.md
- Decisions / ADRs if any architectural choices were made
- Domain Glossary if new terms were introduced
- Confluence pages if CLAUDE.md has a `## Confluence` section

---

## STEP 5: INVOKE @handover

@handover creates the Bitbucket draft PR and sends Slack notification.

Pass:
- Mission state (ticket, branch, base_branch, pr_description from Expected PR)
- Reviewer output (for PR body)
- Documenter status

After @handover completes, update mission state with `pr_id` and `pr_url`.

Write the PR URL to registry so future sessions can reference it without querying Bitbucket:
```
registry_set(project_name, "resources.recent_prs." + ticket_key, pr_url)
```
If deploy.cluster was discovered or confirmed during the build, write it:
```
registry_set(project_name, "deploy.cluster", cluster_name)
```

---

## STEP 6: WRITE AUDIT TRAIL

After @handover completes, build a rich audit entry and call `registry_write_audit(project_name, entry)`.

**Deriving each field:**

- `ticket` — ticket key from mission state (`ticket` field)
- `type` — infer from branch prefix: `feat/` → `"feature"`, `fix/` → `"bugfix"`, `chore/` → `"chore"`, `refactor/` → `"refactor"`, `docs/` → `"docs"`, `test/` → `"test"`. If prefix doesn't match, use `"chore"`.
- `summary` — ticket summary from mission state (`summary` or `title` field)
- `impact` — extract the text under the `## Summary` heading in the PR body produced by @handover. Take the full bullet-list content of that section (stop at the next `##` heading). If no `## Summary` section is found, use the first paragraph of the PR body.
- `branch` — branch name from mission state (`branch` field)
- `pr_url` — PR URL returned by @handover
- `files_changed` — union of all `files` arrays across `plan_steps[]` in mission state (deduplicated)
- `story_points` — from JIRA ticket metadata if available, else `null`
- `labels` — from JIRA ticket metadata if available, else `[]`
- `ac_coverage` — count of `acceptance_criteria` entries marked satisfied by @reviewer vs total; format as `"N/M"` (e.g. `"3/4"`). If `acceptance_criteria` is empty or absent, use `"0/0"`.
- `date` — today's date in ISO 8601 format (`YYYY-MM-DD`)

```
registry_write_audit(project_name, {
  ticket: ticket_key,
  type: type,
  summary: summary,
  impact: impact,
  branch: branch,
  pr_url: pr_url,
  files_changed: files_changed,
  story_points: story_points,
  labels: labels,
  ac_coverage: ac_coverage,
  date: date
})
```

Detect `project_name` the same way as `/plan`: parse from `git remote get-url origin`.
If registry MCP is unavailable, do not write a local file as a substitute. Report clearly: "WARNING: Registry MCP is unavailable — audit trail was NOT recorded. Fix the MCP connection and re-run to capture this entry." This does not block the ship; the PR has already been created.

---

## STEP 7: TRANSITION JIRA

After @handover completes, transition the JIRA ticket from In Progress to Review.

Invoke `@jira` with:
- Ticket key from mission state (`ticket` field)
- Action: transition to Review
- Comment: "Draft PR created: [pr_url]"

If no ticket key (`NO-TICKET`), skip this step.
If the ticket key was auto-generated by the registry counter (check: `registry_get_project(project_name)` has a `ticket_counter` value, and the ticket key is `{PROJECT_UPPERCASE}-{N}` where N ≤ ticket_counter), skip this step — fake tickets have no JIRA to transition.
If Atlassian MCP is unavailable, print: "JIRA transition skipped — MCP not configured. Transition ONE-XXXX manually."

---

## STEP 8: CONFIRM

Output:
```
SHIP COMPLETE
Ticket: ONE-XXXX → Review
PR: [pr_url]
Security: [GO / GO WITH WARNINGS / skipped]
Branch: [branch] → [base_branch]
Slack: notified
```

## SELF-IMPROVEMENT

At the end of each run, reflect on what you learned. If anything is worth saving, act on it before returning to the user.

**Save resource discoveries** — any URL, channel ID, repo slug, log group, cluster name, or other reusable external resource found during this run:
```
registry_set(project_name, "resources.{category}.{key}", value)
```
Categories: `grafana`, `slack`, `aws`, `bitbucket`, `confluence`, `jira`.
Example: `registry_set("emily", "resources.grafana.api_dashboard", "http://grafana/d/abc123")`

**Save reusable commands/lookups** — any command or lookup derived this run that could be reused instead of re-derived next time:
```
registry_set(project_name, "resources.scripts.{name}", {command: "...", description: "...", learned_at: "<RFC3339 timestamp>"})
```

**Fix wrong project metadata** — if deploy.cluster, repo.base, or any other registry field was incorrect:
```
registry_set(project_name, "deploy.cluster", correct_value)
```

**Improve this skill** — if a better approach was found, make a targeted minimal edit to:
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/ship.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
