# STANDUP

Synthesize Yesterday / Today / Blockers from JIRA, Slack, and Bitbucket. Yesterday is a narrative paragraph. Today is context-rich bullets. Post to configured Slack channel.

---

## MCP CHECK

Check availability of MCPs before proceeding:

- `mcp__atlassian__*` — needed for JIRA queries
- `mcp__slack__*` — needed for Slack inference and posting
- `mcp__registry__bitbucket_*` — needed for Bitbucket PR activity (optional)

If Atlassian MCP is unavailable: warn `STANDUP WARNING: Atlassian MCP not configured — JIRA data will be skipped.` Continue with remaining sources.

If Slack MCP is unavailable: warn `STANDUP WARNING: Slack MCP not configured — Slack data will be skipped and posting will not be available.` Continue with remaining sources.

If Bitbucket MCP is unavailable: silently skip Bitbucket inference.

If both Atlassian and Slack are unavailable, stop:

```
STANDUP STATUS: NO DATA SOURCES

Neither Atlassian nor Slack MCPs are configured. At least one is required.

To configure Atlassian MCP:
  claude mcp add --transport sse atlassian https://mcp.atlassian.com/v1/sse

To configure Slack MCP:
  claude mcp add --transport sse slack https://mcp.slack.com/v1/sse
```

---

## STEP 1: GATHER YESTERDAY

Prompt the user:

```
What did you work on yesterday? (press Enter to infer from JIRA + Slack + Bitbucket)
```

**If the user provides a response:** use it as the core Yesterday content. Still run Slack inference to enrich with context (conversations, collaborators, outcomes) — but treat the user's input as ground truth, not Slack.

**If the user leaves it blank:** infer fully from available sources in parallel:

- **JIRA** (if available): query tickets that moved to Done in the last 24 hours.
  - JQL: `assignee = currentUser() AND status changed TO Done AFTER -1d ORDER BY updated DESC`
  - cloudId: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
  - Collect ticket keys, summaries.

- **Slack** (if available): run two searches in parallel:
  1. `from:me after:yesterday` — messages the user sent. Extract topics discussed, decisions made, work shared.
  2. `from:me after:yesterday is:thread` — threads the user participated in. Look for discussions, collaborators named, outcomes reached.
  - Include DMs and all channel types (default behaviour of `slack_search_public_and_private`).
  - Extract: what was discussed, who was involved, what was decided or shipped.

- **Bitbucket** (if available):
  - Read `$BITBUCKET_REPOS` env var via Bash: `echo $BITBUCKET_REPOS`
  - If not set: use default repos `mapi-js,mapi-server,emily`.
  - For each repo, call `bitbucket_list_prs` with `state: MERGED`. Filter for PRs authored by the current user merged in the last 24 hours. Collect repo, PR title, PR link.

**Format Yesterday as a narrative paragraph** (2–3 sentences). Write it like a human would say it in a standup — what you worked on, who you worked with, what shipped or was decided. Do not use bullets. Do not list ticket keys unless they add meaningful context. Examples of good tone:

> Spent most of the day on MAPI architecture research and had a good sync with James in #mapi-dev — landed on an approach for the new service boundary. Also pushed Emily test and lint upgrades through review and got them merged.

> Wrapped up the performance review process and did a deep dive on the VWO poller timeout bug. Discussed the root cause in #dps-eng and confirmed the fix approach.

---

## STEP 2: GATHER TODAY

Pull from three sources in parallel:

- **JIRA** (if available):
  - JQL: `assignee = currentUser() AND status in ("In Progress", "To Do") ORDER BY priority DESC`
  - cloudId: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
  - Collect up to 5 tickets: key, summary, status, priority.

- **Slack** (if available): search for action items and commitments from yesterday's conversations.
  - Query 1: `to:me after:yesterday` — requests directed at the user. Flag anything that sounds like a task ask: "can you", "could you", "please", "take a look", "when you get a chance".
  - Query 2: `from:me after:yesterday` — scan for commitments the user made: "I'll", "I will", "I can do that", "on it", "will follow up", "will take a look".
  - Surface any Slack-sourced work items not already covered by JIRA tickets.

- **Bitbucket** (if available):
  - For each repo in `$BITBUCKET_REPOS` (or defaults), call `bitbucket_list_prs` with `state: OPEN`. Look for PRs authored by the user that need a push (no recent activity, changes requested) or PRs from others that the user has been tagged to review.

**Format Today as context-rich bullets.** Prioritise: In Progress JIRA tickets first, then Slack commitments, then To Do JIRA tickets, then Bitbucket follow-ups. Append a short context tag where it adds signal:

```
Today
- ONE-24400 — Fix VWO experiment poller timeout (High)
- Follow up with James on MAPI service boundary decision (from #mapi-dev)
- ONE-23706 — Integrate Grafana to DPS apps (In Progress)
- ONE-24281 — Bitbucket MCP server setup
```

Omit the context tag when the ticket summary is self-explanatory.

---

## STEP 3: GATHER BLOCKERS

Identify blockers from available sources:

- **JIRA** (if available):
  - JQL: `assignee = currentUser() AND (labels = blocked OR status = "Blocked") ORDER BY updated DESC`
  - cloudId: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
  - Also check comments on In Progress tickets for language like "waiting on", "blocked by", "pending review".

- **Slack** (if available): call `slack_search_public_and_private` with query `from:me has:question after:-3d` to find threads where the user asked something and received no reply. Flag unanswered threads that are blocking work.

If no blockers found: `None identified.`

---

## STEP 4: FORMAT REPORT

```
--- STANDUP [YYYY-MM-DD] ---

Yesterday
[2–3 sentence narrative paragraph]

Today
- [context-rich bullet]
- [context-rich bullet]

Blockers
- [item] OR None identified.
```

Print the formatted report to the user before attempting to post.

---

## STEP 5: POST TO SLACK

Read the `$SLACK_CHANNEL` environment variable via Bash: `echo $SLACK_CHANNEL`

**If a value is returned:** post the formatted report using `slack_send_message`. Confirm:

```
STANDUP STATUS: POSTED — [channel]
```

**If not set:**

```
STANDUP STATUS: CHANNEL NOT CONFIGURED

To enable automatic posting, set the SLACK_CHANNEL environment variable:

  export SLACK_CHANNEL=C0XXXXXXXXX

Add it to your shell profile to persist across sessions. Run /standup again once set.
```

## SELF-IMPROVEMENT

At the end of each run, reflect on what you learned. If anything is worth saving, act on it before returning to the user.

**Save resource discoveries** — any URL, channel ID, repo slug, log group, cluster name, or other reusable external resource found during this run:
```
registry_set(project_name, "resources.{category}.{key}", value)
```
Categories: `grafana`, `slack`, `aws`, `bitbucket`, `confluence`, `jira`.
Example: `registry_set("emily", "resources.grafana.api_dashboard", "http://grafana/d/abc123")`

**Fix wrong project metadata** — if deploy.cluster, repo.base, or any other registry field was incorrect:
```
registry_set(project_name, "deploy.cluster", correct_value)
```

**Improve this skill** — if a better approach was found, make a targeted minimal edit to:
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/standup.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
