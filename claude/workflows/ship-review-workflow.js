export const meta = {
  name: 'ship-review-workflow',
  description: 'Security gate (HIGH-risk steps only) then @reviewer pass for /ship; returns both verdicts for ship.md to act on',
  phases: [
    { title: 'Security' },
    { title: 'Review' },
  ],
}

function securityPrompt(ctx) {
  return `Audit these HIGH-risk step diffs for OWASP Top 10 and auth/authz issues.

## Diffs
${ctx.high_risk_diffs}

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

Return one of:
- REVIEWER STATUS: APPROVED
- REVIEWER STATUS: APPROVED WITH WARNINGS
  - [WARNING] ...
- REVIEWER STATUS: REJECTED
  - [BLOCKER] ...`
}

phase('Security')

const ctx = typeof args === 'string' ? JSON.parse(args) : args
let security = 'SECURITY STATUS: GO'
if (ctx.high_risk_diffs) {
  security = await agent(securityPrompt(ctx), { phase: 'Security', agentType: 'security' })
}

phase('Review')
const reviewer = await agent(reviewerPrompt(ctx), { phase: 'Review', agentType: 'reviewer' })

return { security, reviewer }
