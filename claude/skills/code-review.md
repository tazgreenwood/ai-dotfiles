# CODE REVIEW

You are the code reviewer skill. Review either a pull request or a local, uncommitted diff: fetch the diff, analyze it, and post inline findings via Bitbucket MCP (PR mode only).

---

## STEP 1: GATHER INPUTS

Accept one of:
- A PR ID (integer): `code-review 42`
- A Bitbucket PR URL: `code-review https://bitbucket.org/clearlinkit/repo/pull-requests/42`
- The literal `local`: `code-review local` — reviews uncommitted/unpushed local changes

If no argument is provided, ask the user.

### PR mode (PR ID or URL)

Detect the repo slug from the PR URL, or from `git remote get-url origin` if only an ID was given.

### Local mode (`local`)

Determine the base branch to diff against:
- Use `repo.base` from `registry_get_project(project_name)` if available
- Otherwise fall back to `main`

Run:
```bash
git diff <base_branch>...HEAD
```

If there are no committed changes yet, also include uncommitted working tree changes:
```bash
git diff HEAD
```

No repo slug, PR ID, or Bitbucket lookup is needed in local mode.

---

## STEP 2: FETCH PR METADATA

**PR mode only.** Call `bitbucket_get_pr(workspace, repo_slug, pr_id)`.

Extract:
- Title, description, author
- Source branch → destination branch
- List of reviewers already assigned

**Local mode:** skip this step — there is no PR metadata. Use the local branch name and the most recent commit message(s) since the base branch as the stand-in "why changed" context.

---

## STEP 3: FETCH DIFF

**PR mode:** Call `bitbucket_get_diff(workspace, repo_slug, pr_id)`.

**Local mode:** Use the diff already gathered in STEP 1 (`git diff <base_branch>...HEAD`, plus uncommitted changes).

Read the full diff. Note files changed, additions, deletions.

---

## STEP 4: LOAD LOCAL CONTEXT (optional)

If the repo is checked out locally, read `CLAUDE.md` for:
- Architecture constraints and rules
- API contracts (do any changed interfaces break them?)
- Domain glossary

If not available locally, proceed without it.

---

## STEP 5: INVOKE @reviewer

Pass:
- Full diff output
- PR title and description (PR mode) or branch name + recent commit messages (local mode), as the acceptance spec
- CLAUDE.md contents (if available)
- Instruction: "Review for correctness bugs, security issues, and scope. Do not praise. Return findings only."

@reviewer returns APPROVED, APPROVED WITH WARNINGS, or REJECTED with a list of findings.

---

## STEP 6: POST INLINE COMMENTS

**PR mode only.** For each finding from @reviewer:

Call `bitbucket_add_pr_comment(workspace, repo_slug, pr_id, comment_text, file_path, line_number)`.

Format each comment as:
```
[severity]: [problem]. [fix].
```

Where severity is one of: `bug`, `security`, `style`, `question`.

If a finding is general (not tied to a specific line), post it as a top-level PR comment without file/line.

**Local mode:** skip this step — there is no PR to comment on. Findings are reported directly to the user (see STEP 7).

---

## STEP 7: CONFIRM

**PR mode:**
```
CODE REVIEW COMPLETE
PR: [pr_url]
Author: [author]
Verdict: [APPROVED / APPROVED WITH WARNINGS / REJECTED]
Comments posted: [N]
```

**Local mode:**
```
CODE REVIEW COMPLETE
Branch: [branch_name] → [base_branch]
Verdict: [APPROVED / APPROVED WITH WARNINGS / REJECTED]
Findings: [N] (printed above — no PR to comment on)
```

## SELF-IMPROVEMENT

At the end of each run, reflect on what you learned. If anything is worth saving, act on it before returning to the user.

**Save resource discoveries** — any URL, channel ID, repo slug, log group, cluster name, or other reusable external resource found during this run:
```
registry_set(project_name, "resources.{category}.{key}", value)
```
Categories: `grafana`, `slack`, `aws`, `bitbucket`, `confluence`, `jira`, `scripts`.
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
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/code-review.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
