# ARCH STUDY

You are doing an architecture study of this repository. Do NOT write any
implementation code. Produce analysis and a published artifact.

Work empirically — measure the codebase, don't theorize about it. Use git
history as evidence. Launch parallel subagents for the audits below.

---

## STEP 1 — Find the repeated unit of change

Every maintained codebase has one thing it does over and over: add an
integration, add an endpoint, add a report, add a device driver, add a payment
method, onboard a tenant. Identify it. If there are two, pick the most frequent.

---

## STEP 2 — Measure what that unit actually costs today

Find 3-4 real instances in git history (`git log --oneline --all | grep -i <term>`,
then `git show --stat <sha>`). For each, report: files touched, LOC, how many
distinct directories, whether tests shipped, whether a migration/config/deploy
was needed. Build a table. Do not estimate — cite commits.

---

## STEP 3 — Separate boilerplate from genuinely-specific

Of the files touched each time, which are mechanically identical in shape, and
which contain real domain logic? Quantify roughly what share is boilerplate.

---

## STEP 4 — Find the hidden coupling and the drift

Look specifically for:
- hand-numbered enums or registries that must be edited per unit
- switch statements / if-chains that must gain a case per unit
- the same map maintained in two places (check whether it has ALREADY drifted —
  it usually has, and that's your strongest evidence)
- config/env plumbing required per unit, and the deploy steps it implies
- dead code: migrations with no model, config with no reader, files with no
  references. Count it.

---

## STEP 5 — Determine whether a canonical schema exists

Does the core domain object have one authoritative typed definition, or is it
duck-typed across N files? Check whether field access fails loudly or silently
on a miss (e.g. `$x[$k] ?? ''` returns empty string = silent). If there are
competing naming dialects, tabulate them side by side.

---

## STEP 6 — Audit the invariant surface

What correctness, security, compliance, or legal invariants live in this code?
Where are they enforced, and where are they merely asserted or assumed? Flag
anything that a config-driven or self-service version of this system could let
someone bypass. Be specific and cite file:line. This step is not optional —
it determines whether the redesign is safe to build.

---

## STEP 7 — Design the target

Propose a structure applying:
- colocation: one directory per unit of change, containing everything that unit is
- a typed core with zero framework dependencies, holding the domain vocabulary
- declarative manifests for the ENUMERABLE parts (transport, field maps,
  thresholds, retry)
- a small set of NAMED interfaces as the only extension points, for the
  arbitrary parts. Config names a class; it never contains a conditional
  expression language. Explicitly avoid building a DSL — justify anything
  you put in config rather than in a class.
- golden fixtures per unit (input + expected output snapshot) so the task is
  self-verifiable without domain knowledge
- a GENERATED index of all units, rebuilt in CI, replacing hand-maintained maps
- dependency direction enforced by a linter in CI, not just described in prose
- secrets referenced by key, never stored as literal values
- a scaffolding generator, so the pattern lives in one place instead of N imitations

---

## STEP 8 — Show the payoff

Measured before/after table, using the real numbers from step 2.

---

## STEP 9 — Give a migration order

Strangler pattern, no rewrite. Sequence by risk-reduction-per-effort, and state
explicitly what you would NOT migrate and why. Call out anything from step 6
that is independently shippable and should not be sequenced behind the refactor.

---

## STEP 10 — Publish

Publish as an artifact: annotated directory tree, a sample manifest, the
dependency rules, the before/after table, the migration order.

---

Throughout: cite file:line. Flag where you are uncertain rather than
asserting. If the evidence contradicts a recommendation above, say so and
follow the evidence.
