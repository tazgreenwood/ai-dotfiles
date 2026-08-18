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

Graph mode is the default (promoted from opt-in per the [2026-08-18] decision — see `registry_get_events("private-dotfiles", "decision")`). Accept an optional `--single` flag to opt into the old single-pass mode instead (e.g. `code-review local --single`, `code-review 42 --single`) when you want a faster, cheaper pass instead of full multi-dimension review.

### Graph mode (default)

Isolated-context multi-agent fan-out: 4 dimension reviewers in parallel, then one synthesis pass. Runs as a deterministic Workflow script, not improvised fan-out — the 4-way parallel dispatch and the single synthesis call are real control flow, not prose an LLM re-derives each run.

Call the `Workflow` tool with:
- `scriptPath`: `claude/workflows/code-review-workflow.js`
- `args`: `{ diff, acceptance_spec, claude_md }` — `diff` from STEP 3, `acceptance_spec` is the PR title/description (PR mode) or branch name + recent commit messages (local mode), `claude_md` from STEP 4 (omit if unavailable)

The script returns `{ dimensions: [...], synthesis }` where `synthesis` is the merged verdict (APPROVED / APPROVED WITH WARNINGS / REJECTED) with deduped findings — same contract as single-pass mode. Use `synthesis` as the STEP 5 output for the rest of the skill (STEP 6 onward proceed identically regardless of mode).

### Single-pass mode (`--single`)

Pass:
- Full diff output
- PR title and description (PR mode) or branch name + recent commit messages (local mode), as the acceptance spec
- CLAUDE.md contents (if available)
- Instruction: "Review for correctness bugs, security issues, and scope. Do not praise. Return findings only."

@reviewer returns APPROVED, APPROVED WITH WARNINGS, or REJECTED with a list of findings.

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

## STEP 7: PRINT REPORT

Applies to **both PR mode and local mode** — this is the final action of the skill. No inline Bitbucket comments are posted by this skill (that behavior belongs to `pr-respond.md`, not `code-review.md`).

### Assemble the report data

Gather everything produced by earlier steps:
- **Summary** — one-paragraph overview of the change and the verdict (APPROVED / APPROVED WITH WARNINGS / REJECTED)
- **Why** — PR title + description (PR mode) or branch name + recent commit messages (local mode), from STEP 2
- **Diff overview** — files changed, additions/deletions, from STEP 3
- **Execution mode** — `real` or `mock`, from STEP 6
- **Execution log** — the captured stdout/stderr or mock invocation output, from STEP 6
- **Suggestions/findings** — the list of findings from STEP 5, each tagged with severity (`bug`, `security`, `style`, `question`) and which execution mode (real/mock) backs it, from STEP 6

### Default: print Markdown to chat

Print the report directly in the response as Markdown: Title, Summary, Why, Diff Overview, Execution Results (mode + log), Suggestions. This is the default output; no file is written.

### On request: render a shareable HTML file

Only if the user asks to save or share the report, render a single self-contained HTML file (inline `<style>`, no external assets) using the section layout established by `claude/ui/templates/review.html` as the reference structure.

Write it to a temp path — note `mktemp -t` requires the `X`s to be the trailing characters of the template, so generate the random name first and append the extension:
```bash
f="$(mktemp -t code-review).html"
```

Populate the file's sections with the assembled data (escape HTML-sensitive characters in diff/log content), then open it:
```bash
open "$f"
```

If `open` is unavailable (non-macOS), print the file path to the user instead.

---

## STEP 7a: SAVE TO REGISTRY

Call `registry_write_event(project_name, "pr_review", data)` with:
```
data: {
  summary: [one-paragraph overview from STEP 7],
  verdict: [APPROVED / APPROVED WITH WARNINGS / REJECTED],
  why: [PR title+description or branch+commits, from STEP 2],
  diff_overview: [files changed, additions/deletions, from STEP 3],
  execution_mode: [real / mock, from STEP 6],
  execution_log: [captured stdout/stderr or mock invocation output, from STEP 6],
  suggestions: [findings list from STEP 5, each tagged with severity]
}
```
`project_name` is detected the same way as `/plan` (parse from `git remote get-url origin`). If registry MCP is unavailable, skip this step silently — do not block the report on it.

This makes the review viewable later at `/projects/{name}/reviews` in registry-ui, with a click-through detail page per review at `/projects/{name}/reviews/{id}`.

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

End of run: save any new resource/command via `registry_set(project_name, "resources.{category}.{key}", value)` (categories: grafana/slack/aws/bitbucket/confluence/jira/scripts), fix wrong project metadata the same way (e.g. `registry_set(project_name, "deploy.cluster", correct_value)`), and make a targeted edit to `claude/skills/code-review.md` if a better approach was found. Skip if nothing new was learned.
