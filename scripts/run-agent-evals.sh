#!/usr/bin/env bash
# Score the review pipeline against evals/fixtures/ and gate on a baseline.
#
# COST: every case drives the full 4-dimension fan-out plus refuters. One run
# per case by default; --repeat is opt-in; total spend is capped and reported.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURES="$ROOT/evals/fixtures"
BASELINE="$ROOT/evals/baseline.json"
TOLERANCE=0.05
BUDGET=2.00
UPDATE=0
REPEAT=1

while [ $# -gt 0 ]; do
  case "$1" in
    --update)    UPDATE=1; shift ;;
    --budget)    BUDGET="$2"; shift 2 ;;
    --repeat)    REPEAT="$2"; shift 2 ;;
    --tolerance) TOLERANCE="$2"; shift 2 ;;
    -h|--help)   sed -n '2,8p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

[ -d "$FIXTURES" ] || { echo "no fixtures at $FIXTURES" >&2; exit 2; }
command -v claude >/dev/null || { echo "claude CLI not found" >&2; exit 2; }

PER_CASE=$(awk -v b="$BUDGET" -v n="$(find "$FIXTURES" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')" \
  'BEGIN{ printf "%.4f", (n>0 ? b/n : b) }')

RESULTS="$(mktemp)"; trap 'rm -f "$RESULTS"' EXIT
TOTAL_COST=0

for dir in "$FIXTURES"/*/; do
  name="$(basename "$dir")"
  [ -f "$dir/input.diff" ] && [ -f "$dir/expected.json" ] || { echo "  skip $name (incomplete fixture)"; continue; }
  echo "▸ $name"

  # Drive the REAL pipeline, not an approximation. An earlier version of this
  # runner used a plain review prompt; it had no refuter pass, so the
  # demote-refutable-major case could never pass and the corpus was not gating
  # the thing it claims to gate. code-review-workflow.js takes {diff} directly.
  diff_json="$(python3 -c 'import json,sys; print(json.dumps(open(sys.argv[1]).read()))' "$dir/input.diff")"
  prompt="Call the Workflow tool with scriptPath \"$HOME/.claude/workflows/code-review-workflow.js\" and args {\"diff\": $diff_json}.

Wait for it to finish, then return ONLY this JSON — no prose, no code fence:
{\"verdict\":\"APPROVED|APPROVED WITH WARNINGS|REJECTED\",\"findings\":[{\"severity\":\"BLOCKER|MAJOR|MINOR|NIT|QUESTION\",\"file\":\"...\",\"summary\":\"...\",\"demoted_from\":\"...\"}]}

Report the workflow's own verdict and findings verbatim. Do not re-judge them, do not add findings of your own, and carry demoted_from through for anything the refuter demoted."

  out="$(claude -p "$prompt" --output-format json --max-budget-usd "$PER_CASE" 2>/dev/null || true)"
  cost="$(printf '%s' "$out" | python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("total_cost_usd",0) or 0)
except Exception: print(0)')"
  TOTAL_COST=$(awk -v a="$TOTAL_COST" -v b="$cost" 'BEGIN{printf "%.4f", a+b}')

  printf '%s\0%s\0%s\0' "$name" "$dir/expected.json" "$out" >> "$RESULTS"
done

python3 - "$RESULTS" "$BASELINE" "$UPDATE" "$TOLERANCE" "$TOTAL_COST" <<'PY'
import json, sys, os, re

def extract(blob):
    """Pull the review JSON out of a `claude -p --output-format json` envelope.

    The model is asked for bare JSON but reliably wraps it — markdown fences, a
    sentence of preamble, or both. Treating that as an empty result scores a
    correct review as 0 and records a falsely low baseline, which is worse than
    no baseline at all: it lets real regressions through.
    """
    try:
        env = json.loads(blob)
        text = env.get("result", "") if isinstance(env, dict) else ""
    except Exception:
        text = blob
    if not isinstance(text, str):
        return {}
    text = re.sub(r"^\s*```(?:json)?|```\s*$", "", text.strip(), flags=re.MULTILINE)
    try:
        return json.loads(text)
    except Exception:
        pass
    # Fall back to the outermost {...} span.
    start, depth = text.find("{"), 0
    if start < 0:
        return {}
    for idx in range(start, len(text)):
        if text[idx] == "{":
            depth += 1
        elif text[idx] == "}":
            depth -= 1
            if depth == 0:
                try:
                    return json.loads(text[start:idx + 1])
                except Exception:
                    return {}
    return {}

results_path, baseline_path, update, tol, total_cost = sys.argv[1:6]
update, tol = int(update), float(tol)
raw = open(results_path, 'rb').read().split(b'\0')
scores, report = [], []

for i in range(0, len(raw) - 1, 3):
    name = raw[i].decode()
    expected = json.load(open(raw[i+1].decode()))
    actual = extract(raw[i+2].decode())
    checks, passed = [], 0
    want_v = expected["expected_verdict"]
    got_v = (actual.get("verdict") or "").strip()
    checks.append(("verdict", got_v == want_v, f"want {want_v!r} got {got_v!r}"))
    got_f = actual.get("findings") or []
    if not expected["expected_findings"]:
        checks.append(("silence", len(got_f) == 0, f"want no findings, got {len(got_f)}"))
    else:
        for want in expected["expected_findings"]:
            hit = any(
                (f.get("severity") or "").upper() == want["severity"]
                and want["file"] in (f.get("file") or "")
                and want["must_mention"].lower() in json.dumps(f).lower()
                for f in got_f
            )
            checks.append((f"finding:{want['severity']}", hit, want["must_mention"]))
    passed = sum(1 for _, ok, _ in checks if ok)
    score = passed / len(checks)
    scores.append(score)
    report.append((name, score, checks))

agg = sum(scores) / len(scores) if scores else 0.0
for name, score, checks in report:
    print(f"\n{name}: {score:.2f}")
    for label, ok, detail in checks:
        print(f"   {'PASS' if ok else 'FAIL'}  {label}  ({detail})")
print(f"\naggregate: {agg:.3f}   spend: ${total_cost}")

if update or not os.path.exists(baseline_path):
    json.dump({"aggregate": round(agg, 3), "cases": len(scores)}, open(baseline_path, "w"), indent=2)
    print(f"baseline recorded: {agg:.3f}")
    sys.exit(0)

base = json.load(open(baseline_path))["aggregate"]
floor = base - tol
print(f"baseline: {base:.3f}  floor: {floor:.3f}")
if agg < floor:
    print("REGRESSION — the review pipeline scores below baseline")
    sys.exit(1)
print("OK")
PY
