#!/usr/bin/env node
// Deterministic scorer for the eval corpus. Takes {results:[{name, expected,
// actual}]} as JSON on stdin — `actual` is the real {verdict, findings} object
// code-review-workflow.js returns, not text to extract from a CLI envelope.
// Run in-session by claude/skills/eval.md, never headless — see the
// 2026-08-18-era decision against a `claude -p` subprocess: it cannot approve
// the Workflow tool's permission gate non-interactively, so every fixture
// silently no-ops and the run still spends money on refusal prose.
const fs = require('fs')

const [, , baselinePath, updateFlag, toleranceArg] = process.argv
const update = updateFlag === '1'
const tolerance = parseFloat(toleranceArg || '0.05')

const input = JSON.parse(fs.readFileSync(0, 'utf8'))
const report = []

for (const { name, expected, actual } of input.results) {
  const checks = []
  const gotVerdict = (actual && actual.verdict) || ''
  checks.push(['verdict', gotVerdict === expected.expected_verdict, `want ${JSON.stringify(expected.expected_verdict)} got ${JSON.stringify(gotVerdict)}`])

  const gotFindings = (actual && actual.findings) || []
  if (!expected.expected_findings || expected.expected_findings.length === 0) {
    checks.push(['silence', gotFindings.length === 0, `want no findings, got ${gotFindings.length}`])
  } else {
    for (const want of expected.expected_findings) {
      const hit = gotFindings.some(f =>
        String(f.severity || '').toUpperCase() === want.severity &&
        String(f.file || '').includes(want.file) &&
        JSON.stringify(f).toLowerCase().includes(String(want.must_mention).toLowerCase())
      )
      checks.push([`finding:${want.severity}`, hit, want.must_mention])
    }
  }

  const passed = checks.filter(([, ok]) => ok).length
  const score = passed / checks.length
  report.push({ name, score, checks })
}

const agg = report.length ? report.reduce((s, r) => s + r.score, 0) / report.length : 0

for (const { name, score, checks } of report) {
  console.log(`\n${name}: ${score.toFixed(2)}`)
  for (const [label, ok, detail] of checks) {
    console.log(`   ${ok ? 'PASS' : 'FAIL'}  ${label}  (${detail})`)
  }
}
console.log(`\naggregate: ${agg.toFixed(3)}`)

if (update || !fs.existsSync(baselinePath)) {
  fs.writeFileSync(baselinePath, JSON.stringify({ aggregate: Math.round(agg * 1000) / 1000, cases: report.length }, null, 2))
  console.log(`baseline recorded: ${agg.toFixed(3)}`)
  process.exit(0)
}

const base = JSON.parse(fs.readFileSync(baselinePath, 'utf8')).aggregate
const floor = base - tolerance
console.log(`baseline: ${base.toFixed(3)}  floor: ${floor.toFixed(3)}`)
if (agg < floor) {
  console.log('REGRESSION — the review pipeline scores below baseline')
  process.exit(1)
}
console.log('OK')
