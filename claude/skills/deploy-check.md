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

## SELF-IMPROVEMENT

At the end of each run, reflect on what you learned. If anything is worth saving, act on it before returning to the user.

**Save resource discoveries** — any URL, channel ID, repo slug, log group, cluster name, or other reusable external resource found during this run:
```
registry_set(project_name, "resources.{category}.{key}", value)
```
Categories: `grafana`, `slack`, `aws`, `bitbucket`, `confluence`, `jira`, `scripts`.
Example: `registry_set("emily", "resources.grafana.api_dashboard", "http://grafana/d/abc123")`

**Save reusable commands/lookups** — any command or lookup derived this run that could be reused instead of re-derived next time:
```
registry_set(project_name, "resources.scripts.{name}", {command: "...", description: "...", learned_at: "<RFC3339 timestamp>"})
```

**Fix wrong project metadata** — if deploy.cluster, repo.base, or any other registry field was incorrect:
```
registry_set(project_name, "deploy.cluster", correct_value)
```

**Improve this skill** — if a better approach was found, make a targeted minimal edit to:
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/deploy-check.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
