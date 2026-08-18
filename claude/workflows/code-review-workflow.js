export const meta = {
  name: 'code-review-workflow',
  description: 'Graph-mode PR/diff review: 4 isolated dimension reviewers in parallel, then one synthesis pass',
  phases: [
    { title: 'Review' },
    { title: 'Synthesize' },
  ],
}

const DIMENSIONS = [
  {
    key: 'bugs',
    prompt: 'Review this diff for correctness bugs only — logic errors, off-by-one, null/nil handling, race conditions, broken control flow. Ignore security, scope, and style. Do not praise. Return findings only.',
  },
  {
    key: 'security',
    prompt: 'Review this diff for security issues only — injection, auth/authz gaps, secret exposure, unsafe deserialization, unvalidated input. Ignore correctness, scope, and style. Do not praise. Return findings only.',
  },
  {
    key: 'scope',
    prompt: 'Review this diff for scope only — does it match the stated PR title/description or commit messages (the acceptance spec)? Flag anything out-of-scope, missing, or over-built. Ignore correctness, security, and style. Do not praise. Return findings only.',
  },
  {
    key: 'style',
    prompt: 'Review this diff for style only — naming conventions, dead code, formatting, consistency with CLAUDE.md conventions and domain glossary. Ignore correctness, security, and scope. Do not praise. Return findings only.',
  },
]

function dimensionPrompt(ctx, dimension) {
  return `${dimension.prompt}

## Diff
${ctx.diff}

## Acceptance spec (PR title/description, or branch name + commit messages)
${ctx.acceptance_spec}

## CLAUDE.md
${ctx.claude_md || '(not available)'}`
}

function synthesisPrompt(ctx, findings) {
  return `You are given 4 independent dimension reviews (bugs/correctness, security, scope, style) of the same diff. Dedupe overlapping findings, merge related ones, and produce a single consolidated verdict. Do not praise. Return findings only.

## Diff
${ctx.diff}

## Acceptance spec
${ctx.acceptance_spec}

## CLAUDE.md
${ctx.claude_md || '(not available)'}

## Dimension reviews
${findings.map(f => `### ${f.key}\n${f.output}`).join('\n\n')}

Return one of:
- REVIEWER STATUS: APPROVED
- REVIEWER STATUS: APPROVED WITH WARNINGS
  - [WARNING] ...
- REVIEWER STATUS: REJECTED
  - [BLOCKER] ...`
}

phase('Review')

const ctx = typeof args === 'string' ? JSON.parse(args) : args
const findings = await parallel(
  DIMENSIONS.map(d => () =>
    agent(dimensionPrompt(ctx, d), { phase: 'Review', label: `review:${d.key}`, agentType: 'cavecrew-reviewer', effort: 'high' })
      .then(output => ({ key: d.key, output }))
  )
)

phase('Synthesize')
const synthesis = await agent(synthesisPrompt(ctx, findings.filter(Boolean)), {
  phase: 'Synthesize',
  label: 'synthesis',
  agentType: 'reviewer',
  effort: 'high',
})

return { dimensions: findings, synthesis }
