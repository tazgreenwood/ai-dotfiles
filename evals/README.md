# Agent evals

The review pipeline (`@review-dimension` ×4, the refuter pass, the code-computed
verdict) had **zero tests**. Editing a word in an agent prompt had unknown
effect — and `lead.md` alone was rewritten several times on 2026-09-01.

This corpus turns "I edited a prompt" from an act of faith into a checkable
change.

## Running

```bash
./scripts/run-agent-evals.sh              # score against baseline.json
./scripts/run-agent-evals.sh --update     # record a new baseline
./scripts/run-agent-evals.sh --budget 2.0 # cap total spend (default 2.00)
```

Exits non-zero when the aggregate score drops below the recorded baseline minus
a tolerance.

**Runs cost real money.** Each case drives `code-review-workflow.js` in full —
four `@review-dimension` agents plus an adversarial refuter on every
BLOCKER/MAJOR — so a four-case run is on the order of several dollars, not
cents. One run per case by default; `--repeat` is opt-in; `--budget` caps the
total and the actual spend is printed.

The runner deliberately drives the **real workflow**, not a plain review prompt.
An earlier version used a generic prompt and scored 0.50 — but two of those
failures were artifacts of the harness (no refuter pass exists in a single
review call, so `demote-refutable-major` could never pass). A corpus that does
not exercise the real pipeline gates nothing. There is no baseline recorded
until a full-graph run produces one; a baseline from the wrong pipeline is worse
than none, because it lets real regressions through.

## Fixture schema

One directory per case, holding `input.diff` and `expected.json`:

```json
{
  "case": "short-slug",
  "expected_verdict": "APPROVED | APPROVED WITH WARNINGS | REJECTED",
  "expected_findings": [
    { "severity": "BLOCKER|MAJOR|MINOR|NIT|QUESTION",
      "file": "path/in/the/diff",
      "must_mention": "substring the finding text must contain",
      "demoted_from": "MAJOR" }
  ],
  "notes": "why this case exists and what it is actually scoring"
}
```

Scoring per case: the verdict must match; every expected finding must be present
(matched on severity + file + `must_mention` substring); and for a case whose
`expected_findings` is empty, the pipeline must produce **no** findings.

## The cases, and why each exists

| Case | Scores |
|---|---|
| `blocker-shell-injection` | Catches a real BLOCKER. The actual DOTFILES-34 finding: untrusted Slack text substituted into a `curl -d '...'` argument in the same shell that sourced the bot token. A reviewer that misses this is not usable as a security gate. |
| `silence-clean-diff` | **Correctly says nothing.** A reviewer that never shuts up is the documented noise failure; without a scored silence case the pipeline drifts toward flagging everything. |
| `demote-refutable-major` | The refuter pass checks *real code*, not the diff. A plausible "XSS via `javascript:` in an href" MAJOR must demote to MINOR with a `demoted_from` note, because `html/template` neuters those URLs — verified empirically. Demoted, never silently dropped. |
| `minor-only-template` | Severity calibration: a stale doc comment is a genuine MINOR and must **not** produce a blocking verdict. Only BLOCKER/MAJOR feed the REJECTED branch. |

All four are drawn from this repo's own review history, so the expectations are
grounded in verdicts a human actually checked rather than ones invented for the
fixture.

## Migration to `claude plugin eval`

Fixtures are stored as **plain data** (a diff plus an expected verdict), not in
`plugin eval`'s `evals/<case>/prompt.md` + `graders/*.md` layout. `claude plugin
eval` was verified present in claude 2.1.226 but gated: running it prints
"`plugin eval` is currently in early access" and does not execute. When access
lands, the fixtures port without being rewritten — only the runner is throwaway.
