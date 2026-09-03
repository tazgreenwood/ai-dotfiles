# EVAL

Score the review pipeline (`code-review-workflow.js`) against `evals/fixtures/`
and gate against the recorded baseline. Manual, in-session only — see
`evals/README.md`.

**Why in-session, not a headless script.** An earlier version shelled out to
`claude -p` in a bash script. That subprocess cannot approve the Workflow
tool's permission gate non-interactively, so every fixture silently no-op'd —
the run still spent real money on refusal prose while gating nothing. This
skill instead calls the Workflow tool directly, in this session: each
fixture's review-graph run shows its normal per-call approval, and invoking
this skill is itself the explicit opt-in the Workflow tool requires.

**Costs real money.** Each fixture drives the full 4-dimension `@review-dimension`
fan-out plus an adversarial refuter on every BLOCKER/MAJOR — on the order of
$0.40–0.50 per fixture, several dollars for the whole corpus. Tell the user the
fixture count and ask before running if they didn't explicitly invoke `/eval`.

---

## STEP 1: Load fixtures

For each subdirectory under `evals/fixtures/`, read `input.diff` and
`expected.json`. Skip (and report) any directory missing either file.

## STEP 2: Run the real pipeline, one fixture at a time

For each fixture, in order:

Call the Workflow tool with `scriptPath` set to
`claude/workflows/code-review-workflow.js` and `args: {diff: <contents of
input.diff>}`. Wait for it to complete — it returns `{dimensions, findings,
verdict}` directly; there is no text envelope to parse.

Sequential, not parallel: bounds concurrent spend and keeps the per-fixture
cost visible before starting the next one.

## STEP 3: Score

Build `{results: [{name, expected: <parsed expected.json>, actual: <the
workflow's returned object>}, ...]}` for every fixture and pipe it as JSON on
stdin to:

```bash
node scripts/score-eval.js evals/baseline.json <update:0|1> <tolerance:0.05>
```

- `update=1` (user passed `--update` or explicitly asked to record a new
  baseline) always (re)writes `evals/baseline.json`.
- `update=0`: if `evals/baseline.json` does not exist yet, the script still
  writes it (first run) and exits 0. Otherwise it compares the aggregate
  against `baseline - tolerance` and exits 1 on regression.

This is deterministic scoring logic in code, not an LLM judgment call — same
reasoning as every other `registry_*` pure-function tool in this repo (see
CLAUDE.md's "Deterministic helpers" decision). Do not re-score by eyeballing
the findings; call the script.

## STEP 4: Report

Print the script's own per-fixture PASS/FAIL breakdown and aggregate verbatim.
Add total spend by summing whatever cost the Workflow tool result surfaces per
run (if none is surfaced, say so rather than inventing a number). State
plainly: baseline recorded / OK / REGRESSION, matching the script's exit code.

## SELF-IMPROVEMENT

If a fixture's `expected.json` no longer matches the pipeline's genuinely
correct behavior (not a regression, a deliberate scale change — e.g. severity
scale itself changed), update the fixture and note why in the eval report;
do not silently pass a wrong expectation by loosening `score-eval.js`.
