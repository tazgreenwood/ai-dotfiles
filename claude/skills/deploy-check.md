# DEPLOY CHECK

Check ECS service health and CloudWatch logs after a deploy. Works for any app in any ECS cluster.

---

## APP REGISTRY

Before resolving config, call the registry MCP to look up the app:

```
registry_get_project(app)
```

If the project is found, use the returned fields directly:
- `deploy.profile` → AWS profile
- `deploy.cluster` → ECS cluster name
- `deploy.logGroup` → CloudWatch log group name

If the project is not found (404 or error), fall back to the defaults in STEP 1.

If app config is found in registry: skip Step 2a (list-services grep) and Step 3a (describe-log-groups). Use registry values directly.

---

## ARGS

Parse from the user's invocation. All optional — fall back to defaults:

| Arg | Flag | Default |
|-----|------|---------|
| App name | `--app` or first positional arg | _(required — ask if missing)_ |
| AWS profile | `--profile` | registry value or `martech` |
| Environment | `--env` | `production` |
| Cluster | `--cluster` | registry value or `general-{env}` |
| Log window | `--minutes` | `15` |

Examples:
- `/deploy-check emily`
- `/deploy-check --app brandy --minutes 30`
- `/deploy-check --app hsi --profile martech-dev --env staging`

If no app name provided and none can be inferred from the current working directory (check `git remote` or `CLAUDE.md`), ask:
> "Which app should I check? (e.g. emily, brandy, hsi)"

---

## STEP 1: RESOLVE CONFIG

Call `registry_get_project(APP)` to look up deploy config. Set variables:
- `APP` — from args
- `PROFILE` — `deploy.profile` from registry, or `martech`
- `ENV` — from args, or `production`
- `CLUSTER` — `deploy.cluster` from registry, or `general-{env}`
- `LOG_GROUP` — `deploy.logGroup` from registry, or `{app}-{env}`
- `MINUTES` — from args, or `15`

Compute timestamps:
```bash
# macOS
START_MS=$(date -v-${MINUTES}M +%s000)
# Linux fallback
START_MS=$(date -d "${MINUTES} minutes ago" +%s000 2>/dev/null || date -v-${MINUTES}M +%s000)
```

---

## STEP 2: ECS SERVICE HEALTH + LOG SCAN (run in parallel)

**Run both commands at the same time** — do not wait for ECS before starting log scan.

### 2a. ECS — known app: describe directly

If app is in registry, skip list-services. Describe using service name prefix filter:
```bash
aws --profile $PROFILE ecs list-services \
  --cluster $CLUSTER \
  --output text \
  --query "serviceArns[]" \
| tr '\t' '\n' \
| grep "${APP}-${ENV}"
```
Then describe all matched services:
```bash
aws --profile $PROFILE ecs describe-services \
  --cluster $CLUSTER \
  --services <service1> <service2> ... \
  --query "services[*].{name:serviceName,desired:desiredCount,running:runningCount,pending:pendingCount,status:status,deployments:deployments[*].{status:status,desired:desiredCount,running:runningCount,pending:pendingCount,createdAt:createdAt}}" \
  --output json
```

If no services found:
```
⚠  No ECS services found matching "${APP}-${ENV}" in cluster "${CLUSTER}"
   Profile: $PROFILE
   Try: --cluster <name> or --env <env>
```
Then stop.

Extract `DEPLOY_MS` from the PRIMARY deployment's `createdAt`:
```bash
# convert ISO timestamp from describe-services output to epoch ms
DEPLOY_MS=$(date -j -f "%Y-%m-%dT%H:%M:%S%z" "<createdAt>" +%s000)
```

For each service, evaluate:
- `running == desired && pending == 0` → ✅ HEALTHY
- `pending > 0` → ⏳ DEPLOYING
- `running < desired` → ❌ DEGRADED

### 2b. CloudWatch — known app: skip log group discovery

If app is in registry, use `LOG_GROUP` directly. Otherwise:
```bash
aws --profile $PROFILE logs describe-log-groups \
  --log-group-name-prefix "${APP}-${ENV}" \
  --query "logGroups[0].logGroupName" \
  --output text
```
If none found, report warning and skip log check.

---

## STEP 3: BEFORE/AFTER DEPLOY LOG COMPARISON

Always split the log window at `DEPLOY_MS`. Run both scans — before and after.

**Before deploy** (from `START_MS` to `DEPLOY_MS`):
```bash
aws --profile $PROFILE logs filter-log-events \
  --log-group-name "$LOG_GROUP" \
  --start-time $START_MS \
  --end-time $DEPLOY_MS \
  --filter-pattern "?ERROR ?CRITICAL ?exception ?Exception ?Fatal ?FATAL" \
  --query "events[*].{time:timestamp,msg:message}" \
  --output json
```

**After deploy** (from `DEPLOY_MS` to now):
```bash
aws --profile $PROFILE logs filter-log-events \
  --log-group-name "$LOG_GROUP" \
  --start-time $DEPLOY_MS \
  --filter-pattern "?ERROR ?CRITICAL ?exception ?Exception ?Fatal ?FATAL" \
  --query "events[*].{time:timestamp,msg:message}" \
  --output json
```

Parse results for each window:
- 0 matches → ✅ No errors
- 1–10 matches → show each: `[HH:MM:SS] <message truncated to 200 chars>`
- >10 matches → show first 10, note total count

If `DEPLOY_MS` cannot be determined (e.g. no recent deployment in service data), fall back to single scan from `START_MS` with no split.

---

## STEP 4: REPORT

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  DEPLOY CHECK — {APP} ({ENV})
  Profile: {PROFILE} | Cluster: {CLUSTER}
  Log window: last {MINUTES} min | Deploy: {DEPLOY_TIME}
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

ECS SERVICES
  ✅  {service-name}   desired=2  running=2  pending=0
  ✅  {service-name}   desired=2  running=2  pending=0

CLOUDWATCH LOGS ({LOG_GROUP})
  Before deploy: ✅ No errors  (or list errors)
  After deploy:  ✅ No errors  (or list errors)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  RESULT: ✅ ALL CLEAR
  Report: {REPORT_PATH}
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

If any service DEGRADED or post-deploy errors found:
```
  RESULT: ❌ ISSUES FOUND — investigate before marking deploy complete
```

If deploy still in progress (pending > 0):
```
  ⏳  Deploy still in progress — re-run in a few minutes
```

If errors existed before deploy but not after:
```
  🔧  Pre-existing errors cleared by this deploy
```

---

### STEP 4a: Print report

Assemble the report data:
- **Deploy check summary** — app, env, cluster, profile, timestamp
- **ECS service health** — the per-service results from STEP 2a (name, desired/running/pending, status)
- **CloudWatch logs** — before/after deploy log comparison from STEP 3
- **Overall result** — ✅ ALL CLEAR / ❌ ISSUES FOUND / ⏳ DEPLOYING / 🔧 pre-existing errors cleared, from STEP 4

By default, print the report directly in the response as Markdown: Title, Deploy Check Summary (app/env/cluster/profile/timestamp), ECS Services table, CloudWatch Logs (before/after), Result. No file is written.

Only if the user asks to save or share the report, render a single self-contained HTML file (inline `<style>`, no external assets) with the same sections. Write it to a temp path — note `mktemp -t` requires the `X`s to be the trailing characters of the template, so generate the random name first and append the extension:
```bash
f="$(mktemp -t deploy-check).html"
```

Escape HTML-sensitive characters in log content, then open it:
```bash
open "$f"
```

If `open` is unavailable (non-macOS), print the file path to the user instead.

### STEP 4b: Write to registry

Map the overall result to `status` (`pass` for ✅ ALL CLEAR or 🔧 pre-existing errors cleared, `fail` for ❌ ISSUES FOUND, `pending` for ⏳ DEPLOYING) and write a one-line `summary` (e.g. "All clear — 3/3 services healthy" or "2 services unhealthy, errors found"). `date` is today's date in ISO 8601 (`YYYY-MM-DD`). `report_path` is `null` unless STEP 4a rendered an HTML file.

Call:
```
registry_write_deploy_check(app, {app, env, cluster, profile, status, summary, date, services, errors_before, errors_after, report_path})
```

If this call fails, warn but do not block — print a warning line and continue to the final report, do not stop the skill.

### STEP 4c: Auto-flip pr_ready → done on a passing check

Only run this when the mapped `status` from STEP 4b is `pass` (✅ ALL CLEAR or 🔧 pre-existing errors cleared). **Never** run it for `fail` (❌ ISSUES FOUND) or `pending` (⏳ DEPLOYING).

**Correlation scope — narrow to the project's newest active plan.** To avoid flipping a stale, unrelated `pr_ready` ticket that just happens to sit in the same project, the candidate pool is **not** every active plan for APP — it is only the single plan `registry_index()` reports as APP's `active_plan` (documented as "the newest non-shipped plan"), matching the existing one-active-plan-per-project convention in CLAUDE.md.

1. Call `registry_index()` and find the entry where `name == APP`. Read its `active_plan` field (`{ticket, summary}` or `null`).
   - If `registry_index()` itself fails or is unavailable, **fall back** to the old behavior: call `registry_list_plans(APP)` and scan every plan with `status == "active"` as candidates (step 2 below applies to each). Explicitly note in the STEP 4 report: `registry_index unavailable, scanned all active plans - verify the flipped ticket is correct`.
   - If `active_plan` is `null`, there is no candidate — skip silently, say nothing about this step in the report.
   - Otherwise the candidate pool is that one ticket only (unless the fallback above is in effect).
2. Call `registry_get_audit(APP)` **once**, before evaluating any candidate, and reuse the returned `entries` for every candidate's ticket-match check below (do not re-call it per plan).
3. For each candidate ticket in the pool:
   - Call `registry_get_plan(APP, ticket)`. If this call fails, skip only this plan, note `{ticket}: skipped due to registry error - not evaluated` in the STEP 4 report, and continue to the next candidate (do not abort STEP 4c).
   - Check whether every entry in `plan_steps[]` has `status == "done"`. If not, skip this plan (not pr_ready).
   - Check whether any entry in the `registry_get_audit(APP)` results from step 2 has a matching `ticket`. If a match exists, skip this plan (already shipped/audited). If the step-2 `registry_get_audit` call itself failed, skip only this plan, note `{ticket}: skipped due to registry error - not evaluated`, and continue.
   - A plan with all steps done and no matching audit entry is a **pr_ready candidate**.
4. Based on the candidate count:
   - **0 candidates**: skip silently — say nothing about this step in the report.
   - **1 candidate**: auto-write the audit entry that flips it to done:
     ```
     registry_write_audit(APP, {
       ticket: plan.ticket,
       type: registry_infer_audit_type(plan.branch).type,   // plan.branch may be null — defaults to "chore"
       summary: plan.summary,
       impact: "<the STEP 4 one-line result summary> (auto-flipped by deploy-check - verify code correlation manually if this looks wrong)",
       branch: plan.branch,                                  // nullable — pass through as-is
       pr_url: plan.pr_url,                                  // nullable — never assume a PR exists
       files_changed: registry_union_files(plan.plan_steps[].files).files,
       story_points: null,
       labels: ["deploy-check-verified"],
       ac_coverage: "{done_count}/{total_count}",             // e.g. "5/5" — all steps done
       date: "<today, ISO 8601 YYYY-MM-DD>"
     })
     ```
     Report the flip prominently in the STEP 4 output so a wrong flip is immediately visible, e.g.:
     ```
     🔀  {ticket} flipped pr_ready → done (deploy-check verified)
     ```
   - **>1 candidates** (only possible via the `registry_index()`-unavailable fallback, since the normal path yields at most one): do not guess. List them all in the STEP 4 report (ticket + summary) and ask the user which one to flip, e.g.:
     > "Multiple pr_ready plans found for {APP}: {ticket1} ({summary1}), {ticket2} ({summary2}). Which one should I mark done?"
     This is a question only — it does **not** itself write anything. Once Taz answers with a ticket key (in a later turn of the same conversation), call `registry_get_plan(APP, ticket)` for the chosen ticket and then `registry_write_audit(APP, entry)` using the exact same field-construction as the 1-candidate path above (type/summary/impact/branch/pr_url/files_changed/story_points/labels/ac_coverage/date), then report the flip the same way (`🔀  {ticket} flipped pr_ready → done (deploy-check verified)`). Do not consider STEP 4c complete for the >1-candidate path until this follow-up write has happened.

If the very first call (`registry_index()`, or `registry_list_plans(APP)` in the fallback path) fails outright, warn but do not block — print a warning line and continue to the final report, do not stop the skill. A failure on a later, per-plan call (`registry_get_plan` or `registry_get_audit`) skips only that plan per step 3 above, not the whole of STEP 4c.

> **Why `plan.branch`/`plan.pr_url`/`plan.summary`/`plan_steps[].files` are safe to read here:** these are guaranteed fields on every plan object, written by `plan.md`'s own mission-state schema per CLAUDE.md's `registry_write_plan` section — not a new assumption introduced by this step.

## SELF-IMPROVEMENT

End of run: save any new resource/command via `registry_set(project_name, "resources.{category}.{key}", value)` (categories: grafana/slack/aws/bitbucket/confluence/jira/scripts), fix wrong project metadata the same way (e.g. `registry_set(project_name, "deploy.cluster", correct_value)`), and make a targeted edit to `claude/skills/deploy-check.md` if a better approach was found. Skip if nothing new was learned.
