export const meta = {
  name: 'code-review-workflow',
  description: 'Graph-mode PR/diff review: 4 isolated @review-dimension agents in parallel, structured severity-graded findings, an adversarial refuter pass on every BLOCKER/MAJOR, then synthesis with a code-computed verdict',
  phases: [
    { title: 'Learn' },
    { title: 'Review' },
    { title: 'Verify' },
    { title: 'Synthesize' },
  ],
}

const SEVERITIES = ['BLOCKER', 'MAJOR', 'MINOR', 'NIT', 'QUESTION']
const BLOCKING_SEVERITIES = ['BLOCKER', 'MAJOR']

const FINDING_SCHEMA = {
  type: 'object',
  properties: {
    severity: { type: 'string', enum: SEVERITIES },
    title: { type: 'string' },
    file: { type: 'string' },
    line: { type: 'string', description: 'Line number or range, as a string (e.g. "120" or "118-124")' },
    whats_wrong: { type: 'string' },
    why_it_matters: { type: 'string' },
    evidence: { type: 'string' },
    suggested_fix: { type: 'string', description: 'Omit or empty for QUESTION findings' },
    confidence: { type: 'string', enum: ['high', 'medium', 'low'] },
    demoted_from: { type: 'string', description: 'Set only when a refuter pass demoted this finding — original severity before demotion' },
  },
  required: ['severity', 'title', 'file', 'whats_wrong', 'why_it_matters', 'evidence', 'confidence'],
}

const DIMENSION_SCHEMA = {
  type: 'object',
  properties: {
    findings: { type: 'array', items: FINDING_SCHEMA },
  },
  required: ['findings'],
}

const REFUTER_SCHEMA = {
  type: 'object',
  properties: {
    refuted: { type: 'boolean', description: 'true if the finding does not hold up; default to true when uncertain' },
    reason: { type: 'string' },
  },
  required: ['refuted', 'reason'],
}

const SYNTHESIS_SCHEMA = {
  type: 'object',
  properties: {
    findings: { type: 'array', items: FINDING_SCHEMA },
  },
  required: ['findings'],
}

const LEARNINGS_FETCH_SCHEMA = {
  type: 'object',
  properties: {
    learnings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          pattern: { type: 'string' },
          signal: { type: 'string' },
          action: { type: 'string' },
        },
      },
    },
  },
  required: ['learnings'],
}

const LEARNING_WRITE_SCHEMA = {
  type: 'object',
  properties: {
    wrote: { type: 'boolean' },
    learning: {
      type: 'object',
      properties: {
        pattern: { type: 'string' },
        signal: { type: 'string' },
        action: { type: 'string' },
      },
    },
  },
  required: ['wrote'],
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

function learningsBlock(learnings) {
  if (!learnings || learnings.length === 0) return ''
  return `\n\n## Past review learnings for this project\n${JSON.stringify(learnings, null, 2)}\n\nThese are recurring patterns or tooling gaps surfaced by prior reviews. Weigh them, but don't let them substitute for reading the actual diff.`
}

function dimensionPrompt(ctx, dimension, learnings) {
  return `${dimension.prompt}

## Dimension
${dimension.key}

## Diff
${ctx.diff}

## Acceptance spec (PR title/description, or branch name + commit messages)
${ctx.acceptance_spec}

## CLAUDE.md
${ctx.claude_md || '(not available)'}${learningsBlock(learnings)}

Return JSON matching the given schema: a "findings" array. Each finding must carry one of the 5 severities (BLOCKER, MAJOR, MINOR, NIT, QUESTION), a file and (when known) a line, and the plain-English fields (whats_wrong, why_it_matters, evidence, suggested_fix, confidence). If this dimension has no findings, return an empty array.`
}

function refuterPrompt(ctx, finding) {
  return `You are an adversarial refuter. A dimension reviewer flagged the finding below as ${finding.severity}. Your job is to try to REFUTE it — argue against it holding up, using the actual code, call sites, and tests, not just the diff.

If you cannot find clear evidence the finding is wrong, or you are genuinely uncertain, default to refuted=true — a claim that can't be defended under adversarial pressure should not reach the user at full severity.

## Finding under challenge
Severity: ${finding.severity}
Title: ${finding.title}
File: ${finding.file}${finding.line ? `:${finding.line}` : ''}
What's wrong: ${finding.whats_wrong}
Why it matters: ${finding.why_it_matters}
Evidence: ${finding.evidence}

## Diff
${ctx.diff}

## Acceptance spec
${ctx.acceptance_spec}

## CLAUDE.md
${ctx.claude_md || '(not available)'}

Return JSON matching the given schema: refuted (boolean) and reason (string) explaining your call either way.`
}

function synthesisPrompt(ctx, findings, learnings) {
  return `You are given the post-verification findings from 4 independent dimension reviews (bugs/correctness, security, scope, style) of the same diff. Dedupe findings that describe the same file+line+issue and merge related ones into one entry — keep the higher severity and the union of evidence when merging. Do not re-grade severity otherwise, do not praise, do not compute a verdict.

## Diff
${ctx.diff}

## Acceptance spec
${ctx.acceptance_spec}

## CLAUDE.md
${ctx.claude_md || '(not available)'}${learningsBlock(learnings)}

## Findings to dedupe and merge
${JSON.stringify(findings, null, 2)}

Return JSON matching the given schema: a "findings" array, deduped and merged, each finding still carrying severity, title, file, line, whats_wrong, why_it_matters, evidence, suggested_fix, confidence, and demoted_from when present.`
}

function fetchLearningsPrompt(projectName) {
  return `Call the registry_get_events MCP tool with name="${projectName}" and type="review_learning" to fetch past code-review learnings for this project. Return the entries' "data" objects as the "learnings" array (each has pattern/signal/action). If the tool errors, the project is unknown, or there are no matching events, return an empty array — do not fabricate entries.`
}

function writeLearningPrompt(ctx, findings, verdict) {
  return `You are reviewing the outcome of a just-completed 4-dimension code review (bugs, security, scope, style) to decide whether it surfaced a reusable, generalizable learning worth persisting for future reviews of this project — as opposed to a one-off finding specific to this diff.

A learning is worth writing only if it's a recurring pattern likely to recur (e.g. "this codebase repeatedly misses nil checks after X helper", "the review tooling itself degraded silently"), not a restatement of a single finding.

## Verdict
${verdict}

## Findings (post-synthesis)
${JSON.stringify(findings, null, 2)}

## Acceptance spec
${ctx.acceptance_spec}

If there IS a worthwhile learning: call registry_write_event with name="${ctx.project_name}", type="review_learning", data={pattern, signal, action} (pattern: the recurring issue in one sentence; signal: what in this run revealed it; action: what a future review or the reviewer agents should do differently), tags=["code-review"]. Then return {wrote: true, learning: {pattern, signal, action}}.

If there is NOT a worthwhile learning, do not call any tool — just return {wrote: false}.`
}

function computeVerdict(findings) {
  const isBlocking = f => BLOCKING_SEVERITIES.includes(f.severity)
  if (findings.some(isBlocking)) return 'REJECTED'
  if (findings.length > 0) return 'APPROVED WITH WARNINGS'
  return 'APPROVED'
}

// Guards against the synthesis pass silently dropping a BLOCKER/MAJOR while
// deduping/merging: any pre-synthesis blocking finding with no matching
// blocking counterpart in the merged output is appended back verbatim, so
// what's displayed/tallied can never show fewer blocking findings than what
// actually produced the verdict. Also covers `synthesis.findings` coming
// back empty (`[]` is truthy, so `[] || findings` would otherwise not fall
// back to the pre-synthesis list).
//
// Matching is by file AND title (normalized), not file alone — two distinct
// blocking findings in the same file (e.g. a security dimension's SQLi and
// a bugs dimension's nil-deref both in db.go) must not be treated as the
// same issue just because synthesis kept one of them.
function findingKey(f) {
  return `${(f.file || '').trim().toLowerCase()}::${(f.title || '').trim().toLowerCase()}`
}

function reconcileFindings(preSynthesis, postSynthesis) {
  if (!postSynthesis || postSynthesis.length === 0) return preSynthesis
  const isBlocking = f => BLOCKING_SEVERITIES.includes(f.severity)
  const postBlockingKeys = new Set(postSynthesis.filter(isBlocking).map(findingKey))
  const missing = preSynthesis
    .filter(isBlocking)
    .filter(pre => !postBlockingKeys.has(findingKey(pre)))
  return missing.length > 0 ? [...postSynthesis, ...missing] : postSynthesis
}

const ctx = typeof args === 'string' ? JSON.parse(args) : args

phase('Learn')
let learnings = []
if (ctx.project_name) {
  const fetched = await agent(fetchLearningsPrompt(ctx.project_name), {
    phase: 'Learn',
    label: 'fetch-learnings',
    agentType: 'general-purpose',
    schema: LEARNINGS_FETCH_SCHEMA,
    effort: 'low',
  })
  learnings = (fetched && fetched.learnings) || []
}

const [findings] = await pipeline(
  [ctx],
  async () => {
    const dimensionResults = await parallel(
      DIMENSIONS.map(d => () =>
        agent(dimensionPrompt(ctx, d, learnings), {
          phase: 'Review',
          label: `review:${d.key}`,
          agentType: 'review-dimension',
          schema: DIMENSION_SCHEMA,
          effort: 'high',
        }).then(output => ({ key: d.key, findings: (output && output.findings) || [] }))
      )
    )
    return dimensionResults.filter(Boolean).flatMap(d => (d.findings || []).map(f => ({ ...f, _dimension: d.key })))
  },
  async (reviewFindings) => {
    const toVerify = reviewFindings.filter(f => BLOCKING_SEVERITIES.includes(f.severity))
    const rest = reviewFindings.filter(f => !BLOCKING_SEVERITIES.includes(f.severity))

    const verified = await parallel(
      toVerify.map(finding => async () => {
        const verdict = await agent(refuterPrompt(ctx, finding), {
          phase: 'Verify',
          label: `refute:${finding._dimension}:${finding.title}`,
          agentType: 'general-purpose',
          schema: REFUTER_SCHEMA,
          effort: 'high',
        })
        const refuted = !verdict || verdict.refuted !== false // default to refuted=true when uncertain/missing
        if (refuted) {
          return { ...finding, demoted_from: finding.severity, severity: 'MINOR' }
        }
        return finding
      })
    )

    return [...verified.filter(Boolean), ...rest]
  }
)

phase('Synthesize')
const synthesis = await agent(synthesisPrompt(ctx, findings, learnings), {
  phase: 'Synthesize',
  label: 'synthesis',
  agentType: 'general-purpose',
  schema: SYNTHESIS_SCHEMA,
  effort: 'high',
})
const finalFindings = reconcileFindings(findings, synthesis && synthesis.findings)

// Verdict is computed from the pre-synthesis findings, not the LLM-merged
// output — synthesis only dedupes for display. This stops a confirmed
// BLOCKER/MAJOR from silently vanishing during the free-form merge step
// and flipping the verdict to something more lenient than it should be.
const verdict = computeVerdict(findings)

if (ctx.project_name) {
  await agent(writeLearningPrompt(ctx, finalFindings, verdict), {
    phase: 'Learn',
    label: 'write-learning',
    agentType: 'general-purpose',
    schema: LEARNING_WRITE_SCHEMA,
    effort: 'low',
  })
}

return { dimensions: DIMENSIONS.map(d => d.key), findings: finalFindings, verdict }
