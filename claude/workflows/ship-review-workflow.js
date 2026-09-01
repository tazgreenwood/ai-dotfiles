export const meta = {
  name: 'ship-review-workflow',
  description: 'Security audit (full diff, every ship) then @reviewer pass for /ship; returns both verdicts for ship.md to act on',
  phases: [
    { title: 'Security' },
    { title: 'Review' },
  ],
}

function securityPrompt(ctx) {
  return `Audit this diff for OWASP Top 10 and auth/authz issues. HIGH-risk step diffs are called out separately below for extra scrutiny, but audit the full diff — not just those steps.

## Full diff
${ctx.diff}
${ctx.high_risk_diffs ? `\n## HIGH-risk step diffs (extra scrutiny)\n${ctx.high_risk_diffs}\n` : ''}
## CLAUDE.md Rules and API Contracts
${ctx.claude_md}

## Expected PR
${JSON.stringify(ctx.expected_pr, null, 2)}

Return one of:
- SECURITY STATUS: GO
- SECURITY STATUS: GO WITH WARNINGS
  - [WARNING] [SECURITY-NN: description] — [file:line]
- SECURITY STATUS: BLOCK
  - [CRITICAL] [SECURITY-NN: description] — [file:line]`
}

function reviewerPrompt(ctx) {
  return `Review this PR for correctness bugs, security, and scope. Do not praise. Return findings only.

## Files changed
${JSON.stringify(ctx.files_changed)}

## Expected PR (acceptance spec)
${JSON.stringify(ctx.expected_pr, null, 2)}

## Acceptance criteria
${(ctx.acceptance_criteria || []).map(c => `- ${c}`).join('\n') || '(none)'}

## CLAUDE.md
${ctx.claude_md}

## Diff
${ctx.diff}

Every finding carries exactly one of five severities: BLOCKER, MAJOR, MINOR, NIT, QUESTION. Only BLOCKER/MAJOR block. Render each finding as What's wrong / Why it matters / Evidence / Suggested fix / Confidence.

Compute REVIEWER STATUS from the severities found, not free-handed:
- Any BLOCKER or MAJOR finding present → REJECTED
- No BLOCKER/MAJOR, but at least one MINOR/NIT/QUESTION → APPROVED WITH WARNINGS
- No findings at all → APPROVED

Return one of:
- REVIEWER STATUS: APPROVED
- REVIEWER STATUS: APPROVED WITH WARNINGS
  - [MINOR] ...
  - [NIT] ...
  - [QUESTION] ...
- REVIEWER STATUS: REJECTED
  - [BLOCKER] ...
  - [MAJOR] ...`
}

// Recording is best-effort by design: a registry outage must degrade the
// RECORD, never fail the ship — the same guard the review_learning phase uses.
// Skipped entirely when project_name is absent.
//
// Timestamps are stamped SERVER-side by registry_write_call. Workflow scripts
// cannot call Date (it would break resume), so there is nothing sensible to
// send from here.
async function record(ctx, label, verdictText) {
  if (!ctx.project_name) return
  const m = String(verdictText || '').match(/(?:SECURITY|REVIEWER) STATUS:\s*([A-Z][A-Z ]*)/)
  const verdict = m ? m[1].trim() : ''
  try {
    await agent(
      `Record one agent invocation in the registry. Mechanical relay: make the call, report the result. Do not summarize, judge, or retry.\n\n` +
      `Call the \`registry_write_call\` MCP tool with:\n` +
      `- name: "${ctx.project_name}"\n` +
      `- workflow: "ship-review"\n` +
      `- agent_label: "${label}"\n` +
      `- status: "ok"\n` +
      `- verdict: "${verdict}"\n` +
      (ctx.run_id ? `- run_id: ${ctx.run_id}\n` : '') +
      `\nReturn {"ok":true,"id":<id>} on success. If the tool is unavailable or errors, return {"ok":false,"error":"..."} — do NOT retry and do NOT fail.`,
      { label: `record:${label}`, phase: 'Review', effort: 'low' },
    )
  } catch (e) {
    // Swallowed on purpose: the ship's verdicts are the product, not the record.
  }
}

phase('Security')

const ctx = typeof args === 'string' ? JSON.parse(args) : args
const security = await agent(securityPrompt(ctx), { phase: 'Security', agentType: 'security', effort: 'high' })
await record(ctx, 'security', security)

phase('Review')
const reviewer = await agent(reviewerPrompt(ctx), { phase: 'Review', agentType: 'reviewer', effort: 'high' })
await record(ctx, 'reviewer', reviewer)

return { security, reviewer }
