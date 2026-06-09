# PR RESPOND

You are the PR respond skill. Address open reviewer comments on your own pull request: fetch comments, fix each one, commit, and reply via Bitbucket MCP.

---

## STEP 1: GATHER INPUTS

Accept either:
- A PR ID (integer): `pr-respond 42`
- A Bitbucket PR URL: `pr-respond https://bitbucket.org/clearlinkit/repo/pull-requests/42`

If no PR ID or URL provided, ask the user.

Detect the repo slug from the PR URL, or from `git remote get-url origin` if only an ID was given.

---

## STEP 2: FETCH PR AND COMMENTS

Call `bitbucket_get_pr(workspace, repo_slug, pr_id)` — get branch name, description, pr_url.

Fetch all unresolved comments from the PR. Filter to only open/unresolved inline comments and general comments directed at the PR author.

---

## STEP 3: FETCH DIFF FOR CONTEXT

Call `bitbucket_get_diff(workspace, repo_slug, pr_id)`.

This gives @developer the full picture of what's already changed.

---

## STEP 4: LOAD MISSION STATE

Call `registry_get_plan(project_name, ticket)` to load the branch name and `expected_pr` acceptance spec.
Detect project name from `git remote get-url origin`. Detect ticket from the PR title or branch name.
If registry unavailable, read `CLAUDE.md` for context.

Check out the feature branch: `git checkout [branch]`.

---

## STEP 5: ADDRESS EACH COMMENT

For each unresolved comment:

### 5a. Invoke @developer
Pass:
- The comment text as the task
- The `expected_pr` field from mission state as the acceptance spec
- CLAUDE.md contents
- Full diff for context
- Instruction: "Address this PR comment with a minimal diff. Do not add scope."

**Retry**: if @developer returns BLOCKED, retry up to 2 times. On 3rd failure, surface to user.

### 5b. Invoke @qa
Run in order:
1. Linter (from `## Commands → Lint` in CLAUDE.md)
2. Formatter check (from `## Commands → Format`)
3. Test suite (from `## Commands → Tests`)
4. Logic audit on changed files
5. Verify the specific comment issue is resolved

**If NO-GO**: pass failure back to @developer (retry up to 2 times). On 3rd failure, surface to user.

### 5c. Commit
```bash
git add [changed files]
git commit -m "fix: address PR comment — [1-line summary]"
```

### 5d. Reply to comment
Call `bitbucket_add_pr_comment(workspace, repo_slug, pr_id, reply_text)` with a brief reply:
```
Done — [one sentence describing what changed].
```

---

## STEP 6: NOTIFY

Send Slack message to `$SLACK_CHANNEL`:
```
💬 PR comments addressed — [ticket or branch]
[N] comments resolved.
Review again when ready: [pr_url]
```

Use `slack_send_message` MCP. If unavailable or `$SLACK_CHANNEL` unset, print to terminal.

---

## STEP 7: CONFIRM

Output:
```
PR RESPOND COMPLETE
PR: [pr_url]
Comments addressed: [N]
Slack: notified
```

Note: do not push to remote. Pushing happens as part of PR management — not this skill.
