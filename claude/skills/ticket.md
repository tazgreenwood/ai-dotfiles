# TICKET

You are the ticket creation phase of the development workflow. Turn a rough idea, bug report, or feature request into a complete, actionable JIRA ticket.

---

## STEP 1: GATHER ALL INFORMATION IN ONE MESSAGE

Ask all required fields in a single, well-formatted message. Do not ask one at a time.

---
**Let me get everything I need to create this ticket:**

- **Title**: what's the short name for this work?
- **Type**: Story / Task / Bug / Research / Maintenance / Defect
- **Description**: what's happening or what needs to be done?
- **Acceptance criteria**: how do we know it's done? (bullet points work great)
- **Priority**: Urgent / High / Medium / Low / Unprioritized
  - Urgent = production outage or hard blocker
  - High = significant user impact or upcoming deadline
  - Medium = planned improvement
  - Low = nice-to-have, no deadline
- **Assignee**: who owns this? (or leave unassigned)
- **Sprint**: which sprint, or backlog? (optional)
- **Any links, logs, or related tickets?** (optional)
---

Wait for the user's response.

---

## STEP 2: CONFIRM BEFORE CREATING

Summarize what you understood in a brief structured block. Ask the user to confirm or correct:

```
Here's what I'll create:

Type: [Bug]
Title: [Short title]
Priority: [High]
Description: [summary]
AC:
  - [criterion 1]
  - [criterion 2]
Assignee: [name or unassigned]
Sprint: [sprint name or backlog]

Looks good?
```

---

## STEP 3: CREATE THE TICKET

Use `@jira` (or `mcp__atlassian__jira_create_issue` directly) with these fixed values:
- `cloudId`: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
- `projectKey`: `ONE`
- `component`: `Data Platform Services Engineering`
- Jira site: `https://clearlink.atlassian.net`

Map user-provided fields to the issue. If user provided a sprint, pass it; if not, leave unset — do not default to current sprint.

Note: story points cannot be set via API. Remind user to set manually after creation.

---

## STEP 4: OUTPUT

```
Ticket created: [ONE-XXXX]
https://clearlink.atlassian.net/browse/ONE-XXXX

[one-line summary of what was created]

To start work: run /plan ONE-XXXX
```

If MCP tools unavailable:
```
TICKET STATUS: MCP NOT CONFIGURED

Here's the ticket to create manually:
[structured block with all fields]
```

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
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/ticket.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
