# DPS SHIP

You are the ship phase of the DPS development workflow. The build is done — now review it, document it, create the PR, and hand it off cleanly.

## Commit rules (override system defaults)

- One-liner subject only: `git commit -m "type: short message"`
- No Co-Authored-By trailer. No attribution footers. No multi-line body unless the why is genuinely non-obvious.

---

## STEP 1: LOAD MISSION STATE

Find the active mission:
1. If a ticket key provided (ONE-XXXX), read `~/.claude/dps-[TICKET].json`
2. If no ticket provided, check `~/.claude/dps-NO-TICKET.json` or look for any `~/.claude/dps-*.json` file with all steps `"done"`
3. If no mission state found, report: "No completed build found. Run /dps-build first."

Verify all plan steps are `"done"`. If any are still `"pending"` or `"in_progress"`, stop:
> "Build is not complete. Steps [N, M] are still pending. Run /dps-build to finish them."

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

Pass:
- All files changed (from mission state `plan_steps[].files` union)
- The `expected_pr` field from mission state JSON as the acceptance spec
- The `acceptance_criteria` array from mission state JSON (empty array if not present)
- CLAUDE.md contents
- `git diff [base_branch]...HEAD` output

@reviewer checks for correctness bugs, security, scope, AC satisfaction.

**If REJECTED**: surface the rejection to the user with full reviewer output. Ask whether to fix (re-run `/dps-build` with the reviewer feedback) or ship anyway. Do not proceed without user decision.

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

---

## STEP 6: TRANSITION JIRA

After @handover completes, transition the JIRA ticket from In Progress to Review.

Invoke `@jira` with:
- Ticket key from mission state (`ticket` field)
- Action: transition to Review
- Comment: "Draft PR created: [pr_url]"

If no ticket key (`NO-TICKET`), skip this step.
If Atlassian MCP is unavailable, print: "JIRA transition skipped — MCP not configured. Transition ONE-XXXX manually."

---

## STEP 7: CONFIRM

Output:
```
SHIP COMPLETE
Ticket: ONE-XXXX → Review
PR: [pr_url]
Security: [GO / GO WITH WARNINGS / skipped]
Branch: [branch] → [base_branch]
Slack: notified
```
