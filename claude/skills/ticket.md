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

## STEP 1.5: CHECK FOR DUPLICATES

Search existing issues via `mcp__atlassian__jira_search_issues` (JQL on `projectKey = ONE`, matching the title/keywords). If a likely duplicate is found, surface it to the user before proceeding:

```
Found a possible duplicate: [ONE-XXXX] "[title]" ([status])
https://clearlink.atlassian.net/browse/ONE-XXXX

Create a new ticket anyway, or add a comment to the existing one instead?
```

If the user confirms a new ticket is warranted, continue to STEP 2. If they pick the existing ticket, add a comment via `mcp__atlassian__jira_add_comment` and stop.

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

End of run: save any new resource/command via `registry_set(project_name, "resources.{category}.{key}", value)` (categories: grafana/slack/aws/bitbucket/confluence/jira/scripts), fix wrong project metadata the same way (e.g. `registry_set(project_name, "deploy.cluster", correct_value)`), and make a targeted edit to `claude/skills/ticket.md` if a better approach was found. Skip if nothing new was learned.
