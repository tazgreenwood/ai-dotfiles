# CODE REVIEW

You are the code reviewer skill. Review either a pull request or a local, uncommitted diff: fetch the diff, analyze it, prove behavior via real or mock execution, and present the results as a local HTML report.

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

Accept an optional `--graph` flag on the skill invocation (e.g. `code-review local --graph`, `code-review 42 --graph`). Default (no flag) behavior is unchanged from below.

### Default mode (no `--graph`)

Pass:
- Full diff output
- PR title and description (PR mode) or branch name + recent commit messages (local mode), as the acceptance spec
- CLAUDE.md contents (if available)
- Instruction: "Review for correctness bugs, security issues, and scope. Do not praise. Return findings only."

@reviewer returns APPROVED, APPROVED WITH WARNINGS, or REJECTED with a list of findings.

### Graph mode (`--graph`)

Spike: isolated-context multi-agent fan-out, to compare against the single-pass default above before committing to the pattern elsewhere.

Fire 4 parallel `Agent` calls (subagent_type: `cavecrew-reviewer`), each scoped to exactly one review dimension. Each call is a fresh, isolated invocation — no agent sees another agent's output, and no agent's prompt references the existence of the other three. Each gets the same base context (full diff, PR title/description or branch name + commit messages, CLAUDE.md contents if available) plus a dimension-specific instruction:

1. **Bugs/correctness**: "Review this diff for correctness bugs only — logic errors, off-by-one, null/nil handling, race conditions, broken control flow. Ignore security, scope, and style. Do not praise. Return findings only."
2. **Security**: "Review this diff for security issues only — injection, auth/authz gaps, secret exposure, unsafe deserialization, unvalidated input. Ignore correctness, scope, and style. Do not praise. Return findings only."
3. **Scope/AC**: "Review this diff for scope only — does it match the stated PR title/description or commit messages (the acceptance spec)? Flag anything out-of-scope, missing, or over-built. Ignore correctness, security, and style. Do not praise. Return findings only."
4. **Style**: "Review this diff for style only — naming conventions, dead code, formatting, consistency with CLAUDE.md conventions and domain glossary. Ignore correctness, security, and scope. Do not praise. Return findings only."

Once all 4 dimension agents return, invoke one more `Agent` call (subagent_type: `reviewer`) as the synthesis pass. Pass it:
- The full diff
- The PR title/description or branch name + commit messages
- CLAUDE.md contents (if available)
- All 4 dimension-reviewer outputs, labeled by dimension
- Instruction: "You are given 4 independent dimension reviews (bugs/correctness, security, scope, style) of the same diff. Dedupe overlapping findings, merge related ones, and produce a single consolidated verdict. Do not praise. Return findings only."

The synthesis agent returns APPROVED, APPROVED WITH WARNINGS, or REJECTED with the merged findings list — same contract as default mode. Use this as the STEP 5 output for the rest of the skill (STEP 6 onward proceed identically regardless of mode).

---

## STEP 6: PROVE BEHAVIOR (EXECUTE, DON'T ASSUME)

Findings from STEP 5 are based on reading the diff alone — they assume correctness. This step captures real, observed output for the changed code.

### Find the target repo's test command

Read the target repo's own `CLAUDE.md` (the repo under review, not private-dotfiles) and look for its `## Commands` or "Test command" section.

- If not available locally, or the target repo has no CLAUDE.md, ask `registry_get_project(project_name)` for a stored test command, or infer one from common conventions (`package.json` `scripts.test`, `Makefile` `test:` target, `go test ./...`, etc.)

### Attempt real execution first

Run the discovered test command, scoped to the changed files where possible (e.g. `go test ./path/to/changed/pkg/...`, `npm test -- <changed_test_files>`, `pytest <changed_test_files>`).

Capture the real stdout/stderr output (pass/fail counts, error messages, stack traces).

If the test command succeeds in running (regardless of pass/fail) and its output covers the changed code, use this as the proof of behavior. Skip mock execution.

### Fall back to synthesized mock execution

If there is no test command, the test command fails to run (missing deps, no test infra), or it doesn't target the changed code (e.g. changed code has no covering tests):

- Identify the changed functions/endpoints from the diff (STEP 3)
- Synthesize minimal, realistic mock inputs for each (valid case + one edge case, e.g. empty/nil/error input)
- Run the changed function(s) directly against those mock inputs — via a scratch script, REPL, or inline invocation, whichever is fastest for the language — and capture the real output/return value/thrown error
- If the changed code cannot be invoked in isolation (e.g. requires live infra with no local equivalent), state this explicitly and note it as a gap in the findings — do not fabricate output

### Record

Attach the captured output (real or mock) to the corresponding finding(s) from STEP 5. Note which mode was used (real test execution vs. synthesized mock) for each.

---

## STEP 7: GENERATE AND OPEN HTML REPORT

Applies to **both PR mode and local mode** — this is the final action of the skill. No inline Bitbucket comments are posted by this skill (that behavior belongs to `pr-respond.md`, not `code-review.md`).

### Assemble the report data

Gather everything produced by earlier steps:
- **Summary** — one-paragraph overview of the change and the verdict (APPROVED / APPROVED WITH WARNINGS / REJECTED)
- **Why** — PR title + description (PR mode) or branch name + recent commit messages (local mode), from STEP 2
- **Diff overview** — files changed, additions/deletions, from STEP 3
- **Execution mode** — `real` or `mock`, from STEP 6
- **Execution log** — the captured stdout/stderr or mock invocation output, from STEP 6
- **Suggestions/findings** — the list of findings from STEP 5, each tagged with severity (`bug`, `security`, `style`, `question`) and which execution mode (real/mock) backs it, from STEP 6

### Render to a self-contained HTML file

Write a single self-contained HTML file (inline `<style>`, no external assets) using the section layout established by `claude/ui/templates/review.html` as the reference structure: Title, Summary, Why, Diff Overview, Execution Results (mode + log), Suggestions.

Write it to a temp path:
```bash
mktemp -t code-review-XXXX.html
```

Populate the file's sections with the assembled data (escape HTML-sensitive characters in diff/log content).

### Open the report

```bash
open <path>
```

If `open` is unavailable (non-macOS), print the file path to the user instead.

---

## STEP 8: CONFIRM

**PR mode:**
```
CODE REVIEW COMPLETE
PR: [pr_url]
Author: [author]
Verdict: [APPROVED / APPROVED WITH WARNINGS / REJECTED]
Report: [path to HTML file] (opened in browser)
```

**Local mode:**
```
CODE REVIEW COMPLETE
Branch: [branch_name] → [base_branch]
Verdict: [APPROVED / APPROVED WITH WARNINGS / REJECTED]
Execution: [real test run / synthesized mock — see report]
Report: [path to HTML file] (opened in browser)
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
