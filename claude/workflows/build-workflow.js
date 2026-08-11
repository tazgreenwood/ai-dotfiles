export const meta = {
  name: 'build-workflow',
  description: 'Deterministic execution of an approved plan: sync/async run partitioning, developer/QA retry loop, git-worktree lifecycle, sequential merge-back',
  phases: [
    { title: 'Execute' },
    { title: 'Merge' },
  ],
}

const MAX_ATTEMPTS = 3

function acList(criteria) {
  return (criteria || []).map(c => `- ${c}`).join('\n') || '(none)'
}

function devPrompt(ctx, step, index, attempt, failureReason, cwd) {
  return `You are the developer agent. Implement exactly one plan step.
${cwd ? `\nWork inside: ${cwd} (all commands must run there, not the main checkout)\n` : ''}
## Ticket
${ctx.ticket} — ${ctx.summary}

## This step (index ${index})
Title: ${step.title}
Why: ${step.why}
How: ${step.how}
Tests: ${step.tests}
Files: ${JSON.stringify(step.files)}
Verification: ${step.verification}

## Acceptance criteria (full list)
${acList(ctx.acceptance_criteria)}

## Expected PR
${JSON.stringify(ctx.expected_pr, null, 2)}

## CLAUDE.md
${ctx.claude_md}
${attempt > 1 ? `\n## Retry context (attempt ${attempt})\nPrevious attempt failed: ${failureReason}\nReview current code state, skip completed sub-tasks, focus only on what was not completed or failed.\n` : ''}
## Instructions
1. First call registry_update_step("${ctx.project_name}", "${ctx.ticket}", ${index}, "in_progress").
2. Write failing tests first if the Tests field is present.
3. Implement the step exactly as described.
4. Run linter, formatter, and test suite per CLAUDE.md Commands.
5. Return one of:
   - DEVELOPER STATUS: DONE — [one-line summary of what was done]
   - DEVELOPER STATUS: BLOCKED — [specific reason]`
}

function qaPrompt(ctx, step, index, cwd) {
  return `You are the QA agent. Verify one completed plan step.
${cwd ? `\nWork inside: ${cwd}\n` : ''}
## Ticket
${ctx.ticket} — ${ctx.summary}

## Step verified (index ${index})
Title: ${step.title}
Why: ${step.why}
How: ${step.how}
Tests: ${step.tests}
Files: ${JSON.stringify(step.files)} — the only files that should have changed
Verification: ${step.verification}

## Acceptance criteria
${acList(ctx.acceptance_criteria)}

## Expected PR
${JSON.stringify(ctx.expected_pr, null, 2)}

## CLAUDE.md
${ctx.claude_md}

## Instructions
Run in order:
1. Linter — command from CLAUDE.md Commands → Lint
2. Formatter check — command from CLAUDE.md Commands → Format
3. Test suite — command from CLAUDE.md Commands → Tests
4. Logic audit: does the implementation match the step's How and Verification?
5. AC check: which acceptance criteria does this step satisfy?

Return one of:
- QA STATUS: GO — [brief summary, ACs covered]
- QA STATUS: NO-GO — [specific failure reason, what must be fixed]`
}

function commitPrompt(ctx, step, index, cwd) {
  return `${cwd ? `Work inside: ${cwd}\n\n` : ''}Stage only these files and commit:

Files: ${JSON.stringify(step.files)}

Run:
git add ${step.files.join(' ')}
git commit -m "TYPE(${ctx.ticket}): short description under 60 chars"

Pick TYPE from feat/fix/refactor/test/docs/chore matching the step content. One-line subject only, no body unless the why is genuinely non-obvious, no Co-Authored-By or attribution lines.

Then call registry_update_step("${ctx.project_name}", "${ctx.ticket}", ${index}, "done").

Return COMMIT STATUS: DONE when finished.`
}

function blockPrompt(ctx, index, reason) {
  return `Call registry_update_step("${ctx.project_name}", "${ctx.ticket}", ${index}, "blocked").

Reason this step is blocked: ${reason}

Return BLOCK STATUS: RECORDED when done.`
}

function worktreeSetupPrompt(branch, stepBranch, worktreePath) {
  return `From the main checkout (already on branch ${branch}), run:

git worktree add ${worktreePath} -b ${stepBranch} ${branch}

Return WORKTREE STATUS: READY when done, or WORKTREE STATUS: FAILED — [reason] if it errors.`
}

function worktreeMergePrompt(branch, stepBranch, worktreePath, ctx, index) {
  return `From the main checkout, run:

git checkout ${branch}
git merge --no-ff ${stepBranch}

If the merge succeeds: run "git worktree remove ${worktreePath}", then call registry_update_step("${ctx.project_name}", "${ctx.ticket}", ${index}, "done"). Return MERGE STATUS: DONE.

If the merge conflicts: run "git merge --abort" and do not attempt to resolve conflicts. Return MERGE STATUS: CONFLICT — [brief description].`
}

async function runStep(ctx, step, index, cwd) {
  let attempt = 0
  let failureReason = ''
  while (attempt < MAX_ATTEMPTS) {
    attempt++
    const dev = await agent(devPrompt(ctx, step, index, attempt, failureReason, cwd), { phase: 'Execute', label: `dev:${index} attempt ${attempt}` })
    if (/BLOCKED/.test(dev)) {
      failureReason = dev
      continue
    }
    const qa = await agent(qaPrompt(ctx, step, index, cwd), { phase: 'Execute', label: `qa:${index} attempt ${attempt}` })
    if (/NO-GO/.test(qa)) {
      failureReason = qa
      continue
    }
    if (!cwd) {
      // sync steps commit immediately; async steps commit inside their worktree
      // after QA GO too, but merge-back (not this commit) is what marks "done"
      // in the registry — see worktreeMergePrompt.
      await agent(commitPrompt(ctx, step, index, cwd), { phase: 'Execute', label: `commit:${index}` })
    } else {
      await agent(commitPrompt(ctx, step, index, cwd).replace(
        `Then call registry_update_step("${ctx.project_name}", "${ctx.ticket}", ${index}, "done").\n\n`,
        ''
      ), { phase: 'Execute', label: `commit:${index}` })
    }
    return { index, status: 'done' }
  }
  await agent(blockPrompt(ctx, index, failureReason), { phase: 'Execute', label: `block:${index}` })
  return { index, status: 'blocked', reason: failureReason }
}

function partitionRuns(steps) {
  const pending = steps
    .map((step, index) => ({ step, index }))
    .filter(x => x.step.status === 'pending')

  const runs = []
  let i = 0
  while (i < pending.length) {
    const cur = pending[i]
    if (cur.step.execution === 'async' && cur.step.parallel_group != null) {
      const group = cur.step.parallel_group
      const items = [cur]
      let j = i + 1
      while (
        j < pending.length &&
        pending[j].step.execution === 'async' &&
        pending[j].step.parallel_group === group
      ) {
        items.push(pending[j])
        j++
      }
      runs.push({ type: 'async', items })
      i = j
    } else {
      runs.push({ type: 'sync', items: [cur] })
      i++
    }
  }
  return runs
}

phase('Execute')

const ctx = args
const runs = partitionRuns(ctx.plan_data.plan_steps)
const results = []

for (const run of runs) {
  if (run.type === 'sync') {
    const { step, index } = run.items[0]
    const r = await runStep(ctx, step, index, null)
    results.push(r)
    if (r.status === 'blocked') {
      return { status: 'blocked', results }
    }
    continue
  }

  // Async run — hand-rolled git worktrees per step, not agent()'s isolation:'worktree'
  // option: this run's worktrees must branch off the feature branch's current HEAD
  // and merge back sequentially in index order, which needs verifying against the
  // built-in isolation option's exact semantics before relying on it for a shared
  // production pipeline. Explicit git commands match build.md's existing, proven flow.
  const group = run.items[0].step.parallel_group
  log(`Async run: ${run.items.length} steps in parallel_group ${group}`)

  const worktrees = run.items.map(({ step, index }) => ({
    step,
    index,
    branch: `${ctx.plan_data.branch}-step-${index}`,
    path: `~/.worktrees/${(ctx.plan_data.repo || '').split('/').pop() || ctx.project_name}/${ctx.plan_data.branch}-step-${index}`,
  }))

  await parallel(worktrees.map(w => () =>
    agent(worktreeSetupPrompt(ctx.plan_data.branch, w.branch, w.path), { phase: 'Execute', label: `worktree-setup:${w.index}` })
  ))

  const stepResults = await parallel(worktrees.map(w => () =>
    runStep(ctx, w.step, w.index, w.path)
  ))
  results.push(...stepResults)

  if (stepResults.some(r => r.status === 'blocked')) {
    return { status: 'blocked', results }
  }

  phase('Merge')
  const ordered = worktrees.slice().sort((a, b) => a.index - b.index)
  for (const w of ordered) {
    const mergeResult = await agent(
      worktreeMergePrompt(ctx.plan_data.branch, w.branch, w.path, ctx, w.index),
      { phase: 'Merge', label: `merge:${w.index}` }
    )
    if (/CONFLICT/.test(mergeResult)) {
      await agent(blockPrompt(ctx, w.index, mergeResult), { phase: 'Merge', label: `block-merge:${w.index}` })
      return { status: 'blocked', results, mergeConflictAt: w.index }
    }
  }
  phase('Execute')
}

return { status: 'complete', results }
