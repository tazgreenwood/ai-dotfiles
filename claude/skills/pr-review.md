# DPS PR REVIEW

You are the PR feedback phase of the DPS development workflow. A reviewer left comments on the draft PR. Address them, verify the fix, and notify.

---

## STEP 1: GATHER INPUTS

You need:
1. **Ticket key** (ONE-XXXX) — to find the mission state and current branch
2. **PR comment text** — the reviewer's feedback to address

If either is missing, ask the user to provide it before continuing.

---

## STEP 2: LOAD CONTEXT

1. Read `~/.claude/dps-[TICKET].json` mission state — get branch name and `pr_id`
2. Check out the feature branch: `git checkout [branch]`
3. Read `CLAUDE.md` — note Rules, Commands (lint/format/tests)
4. Read the `expected_pr` field from `~/.claude/dps-[TICKET].json` — this is the acceptance spec

---

## STEP 3: INVOKE @developer

Pass:
- The PR comment text as the task
- The `expected_pr` field from the mission state JSON as the acceptance spec
- CLAUDE.md contents
- Context: "This is a targeted fix for PR review comments, not a new plan step. Only address the feedback — do not add scope."

@developer addresses the feedback with a minimal diff.

**Retry**: if @developer returns BLOCKED, retry up to 2 times. On 3rd failure, surface to user.

---

## STEP 4: INVOKE @qa

@qa runs in order:
1. Linter (from `## Commands → Lint` in CLAUDE.md)
2. Formatter check (from `## Commands → Format`)
3. Test suite (from `## Commands → Tests`)
4. Logic audit on changed files
5. Verify the PR comment is addressed (check that the specific issue is resolved)

**If NO-GO**: pass failure back to @developer (retry up to 2 times). On 3rd failure, surface to user.

---

## STEP 5: COMMIT

Stage only changed files:
```bash
git add [changed files]
git commit -m "fix(ONE-XXXX): address PR review comments"
```

No body unless specific comments require explanation.

---

## STEP 6: NOTIFY

Send Slack message to `$DPS_SLACK_CHANNEL`:
```
💬 PR comments addressed — ONE-XXXX
[1-2 sentence summary of what was fixed]
Review again when ready: [pr_url from mission state]
```

Use `slack_send_message` MCP. If unavailable or `$DPS_SLACK_CHANNEL` unset, print to terminal.

---

## STEP 7: CONFIRM

Output:
```
PR REVIEW COMPLETE
Ticket: ONE-XXXX
Changes committed: [summary]
Slack: notified
```

Note: do not push to remote. Pushing happens as part of PR management — not this skill.
