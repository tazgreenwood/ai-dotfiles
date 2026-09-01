# RECORD AGENT CALLS (shared step)

Invoked by `/ship`, `/code-review` and `/lead build` immediately after a
`Workflow` call returns. Records one `agent_calls` row per agent the workflow
ran, so cost and trajectory are queryable afterwards.

**Why it lives in the calling skill, not the workflow script:** a workflow script
cannot see its own agents' usage — `agent()` returns the final text or the schema
object, never tokens, model or duration. A recorder inside the script could only
write the verdict, leaving `model=""` and tokens/cost at 0. A cost dashboard
built on those rows would read as authoritative and be uniformly zero, which is
worse than no rows at all. The real numbers are in the Workflow tool's result,
which only the caller sees.

---

## What to record

The Workflow result carries a `workflowProgress` array with one entry per agent:
`label`, `agentType`, `model`, `tokens`, `durationMs`, `startedAt`, `state`.

For **each** entry whose `type` is `workflow_agent`, call `registry_write_call`:

| Field | Source |
|---|---|
| `name` | the routed project |
| `run_id` | the current `/lead build` run id, **omitted** outside a chain |
| `workflow` | the workflow's `meta.name` (`ship-review`, `code-review`, `build`) |
| `agent_label` | the entry's `label` |
| `model` | the entry's `model` |
| `status` | `ok` when `state` is `done`, else `error` |
| `input_tokens` / `output_tokens` | see the caveat below |
| `cost_usd` | derived — see below |
| `verdict` | the verdict this agent produced, when it produced one |
| `trajectory` | `{tool_calls, duration_ms, phase, agent_type}` from the entry |

**Token caveat, state it rather than paper over it:** `workflowProgress` reports a
single `tokens` figure per agent, not an input/output split. Record it as
`input_tokens` and leave `output_tokens` at 0, and put `"tokens_are_combined":
true` in the `trajectory` so nothing downstream mistakes the split for real. A
cost figure derived this way is an **upper bound** if priced at input rates and
a lower bound if priced at output rates — say which you used.

## Deriving `cost_usd`

Read `registry_get_resources(project, "pricing")` →
`model_usd_per_mtok[<model>]`. Compute `tokens / 1_000_000 × rate`.

If the model is **not** in the table, record `cost_usd: 0` and put the model name
in the `trajectory`. Never guess a price: a silently-zero cost is discoverable,
an invented one is not.

## Failure handling

Recording is **best-effort**. If the registry is unavailable or a write fails,
report it once and carry on — a failed record must never fail a ship, a review or
a build. Never retry more than once.

Skip this step entirely when there is no routed project name.
