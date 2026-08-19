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

phase('Security')

const ctx = typeof args === 'string' ? JSON.parse(args) : args
const security = await agent(securityPrompt(ctx), { phase: 'Security', agentType: 'security', effort: 'high' })

phase('Review')
const reviewer = await agent(reviewerPrompt(ctx), { phase: 'Review', agentType: 'reviewer', effort: 'high' })

return { security, reviewer }
