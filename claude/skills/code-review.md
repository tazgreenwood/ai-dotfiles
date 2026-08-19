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
- `scriptPath`: `~/.claude/workflows/code-review-workflow.js` (absolute — resolves regardless of invoking cwd, unlike a repo-relative path)
- `args`: `{ diff, acceptance_spec, claude_md, project_name }` — `diff` from STEP 3, `acceptance_spec` is the PR title/description (PR mode) or branch name + recent commit messages (local mode), `claude_md` from STEP 4 (omit if unavailable), `project_name` detected the same way as STEP 7a (parse from `git remote get-url origin`; omit if it can't be determined)

`project_name` drives the workflow's own learnings loop: before reviewing, it fetches past `review_learning` events for the project (via `registry_get_events`) and feeds them to the dimension/synthesis agents as extra context; after synthesis, it asks an agent to decide whether this run surfaced a new generalizable pattern worth persisting, and if so writes it back via `registry_write_event(project_name, "review_learning", {pattern, signal, action})`. Both steps are skipped silently if `project_name` is omitted or the registry is unavailable.

The script returns `{ dimensions: [...], findings: [...], verdict }`:
- `dimensions` — the 4 dimension keys reviewed (`bugs`, `security`, `scope`, `style`)
- `findings` — the deduped, post-refuter-verification findings array, each shaped per `review-dimension.md`'s finding shape (`severity`, `title`, `file`, `line`, `whats_wrong`, `why_it_matters`, `evidence`, `suggested_fix`, `confidence`, `demoted_from` when a BLOCKER/MAJOR was demoted by the adversarial refuter)
- `verdict` — APPROVED / APPROVED WITH WARNINGS / REJECTED, computed in code from the severity mapping (BLOCKER/MAJOR present → REJECTED; only MINOR/NIT/QUESTION present → APPROVED WITH WARNINGS; no findings → APPROVED) — never free-handed by an LLM

Use `findings` and `verdict` as the STEP 5 output for the rest of the skill (STEP 6 onward proceed identically regardless of mode).

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
- **Bottom line** — one sentence: the verdict, plus the single most important thing to tell a coworker about this change. Written last, after the findings are grouped, so it actually reflects what's in them.
- **Verdict** — APPROVED / APPROVED WITH WARNINGS / REJECTED, from STEP 5 (graph mode: computed in code by the workflow; single-pass mode: returned by `@reviewer`)
- **Why** — PR title + description (PR mode) or branch name + recent commit messages (local mode), from STEP 2
- **Diff overview** — files changed, additions/deletions, from STEP 3
- **Execution mode** — `real` or `mock`, from STEP 6
- **Execution log** — the captured stdout/stderr or mock invocation output, from STEP 6
- **Findings, grouped by severity** — the `findings` array from STEP 5 (graph mode) or the findings list from `@reviewer` (single-pass mode), partitioned into BLOCKER, MAJOR, MINOR, NIT, QUESTION buckets (in that order), each finding rendered per `review-dimension.md`'s finding shape:
  ```
  ### [SEVERITY] <title> — `file:line`
  **What's wrong:** ...
  **Why it matters:** ...
  **Evidence:** ...
  **Suggested fix:** ... (omit for QUESTION)
  **Confidence:** high | medium | low
  ```
  A finding carrying `demoted_from` notes it: `(demoted from MAJOR — survived the refuter pass at reduced severity)`.

### Default: print Markdown to chat

Print the report directly in the response as Markdown, in this order: Title, **Bottom line**, Verdict, Why, Diff Overview, Execution Results (mode + log), then Findings grouped by severity — a `## BLOCKER` section, then `## MAJOR`, `## MINOR`, `## NIT`, `## QUESTION`, omitting any section with zero findings. This is the default output; no file is written.

**MINOR and NIT findings are informational, never merge blockers.** Quote the anti-perfectionism rule from `review-dimension.md` verbatim when presenting these sections: "MINOR and NIT never escalate. If you are tempted to mark polish as MAJOR, mark it MINOR. Perfection is the enemy of progress." Never phrase a MINOR or NIT finding as something that must be fixed before merge — only BLOCKER and MAJOR findings block; the computed verdict already reflects this (STEP 5), so the report's language must not contradict it.

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
  suggestions: [structured findings array from STEP 5 — pass the objects as-is, one per finding: { severity, title, file, line, whats_wrong, why_it_matters, evidence, suggested_fix, confidence, demoted_from } — not a prose summary]
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
Findings: [N] blocker, [N] major, [N] minor, [N] nit, [N] question
Report: [path to HTML file] (opened in browser)
```

**Local mode:**
```
CODE REVIEW COMPLETE
Branch: [branch_name] → [base_branch]
Verdict: [APPROVED / APPROVED WITH WARNINGS / REJECTED]
Findings: [N] blocker, [N] major, [N] minor, [N] nit, [N] question
Execution: [real test run / synthesized mock — see report]
Report: [path to HTML file] (opened in browser)
```

The severity tally counts the STEP 5 `findings` array (or `@reviewer`'s findings list in single-pass mode) by severity — e.g. `Findings: 0 blocker, 2 major, 3 minor, 1 nit`.

## SELF-IMPROVEMENT

End of run: save any new resource/command via `registry_set(project_name, "resources.{category}.{key}", value)` (categories: grafana/slack/aws/bitbucket/confluence/jira/scripts), fix wrong project metadata the same way (e.g. `registry_set(project_name, "deploy.cluster", correct_value)`), and make a targeted edit to `claude/skills/code-review.md` if a better approach was found. Skip if nothing new was learned.
