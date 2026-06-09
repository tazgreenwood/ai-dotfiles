# STANDUP

Synthesize Yesterday / Today / Blockers from JIRA and Slack, print a formatted standup report, and post it to the configured Slack channel.

---

## MCP CHECK

Check availability of both MCPs before proceeding:

- `mcp__atlassian__*` — needed for JIRA queries
- `mcp__slack__*` — needed for Slack inference and posting

If Atlassian MCP is unavailable: warn `STANDUP WARNING: Atlassian MCP not configured — JIRA data will be skipped.` Continue with Slack only.

If Slack MCP is unavailable: warn `STANDUP WARNING: Slack MCP not configured — Slack data will be skipped and posting will not be available.` Continue with JIRA only.

If both are unavailable, stop:

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
What did you work on yesterday? (press Enter to infer from JIRA + Slack)
```

**If the user provides a response:** use it verbatim as the Yesterday content. Skip inference.

**If the user leaves it blank:** infer from available sources:

- JIRA (if available): query for tickets assigned to `currentUser()` where status transitioned to Done in the last 24 hours.
  - JQL: `assignee = currentUser() AND status changed TO Done AFTER -1d ORDER BY updated DESC`
  - cloudId: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
  - Collect ticket keys, summaries, and any linked PR or commit info.
- Slack (if available): call `slack_search_public_and_private` with query `from:me after:yesterday` to find messages sent by the user in the last 24 hours. Extract channel names and brief summaries of what was discussed or shared.

Synthesize findings into 2–4 bullet points for the Yesterday section.

---

## STEP 2: GATHER TODAY

Query JIRA for the user's current workload (if available):

- JQL: `assignee = currentUser() AND status in ("In Progress", "To Do") ORDER BY priority DESC`
- cloudId: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
- Collect up to 5 tickets: key, summary, status, priority.

Supplement with Slack urgency signals (if available):

- Call `slack_search_public_and_private` with query `to:me after:yesterday` to find messages directed at the user.
- Look for requests, pings, or time-sensitive threads. If any surface work not in JIRA, include it.

Prioritise: In Progress tickets first, then To Do by JIRA priority, then any Slack-surfaced items.

Synthesise into 3–5 bullet points for the Today section.

---

## STEP 3: GATHER BLOCKERS

Identify blockers from available sources:

- JIRA (if available): query for tickets assigned to `currentUser()` with a blocked status or label.
  - JQL: `assignee = currentUser() AND (labels = blocked OR status = "Blocked") ORDER BY updated DESC`
  - cloudId: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
  - Also check comments on In Progress tickets for language like "waiting on", "blocked by", "pending review".
- Slack (if available): call `slack_search_public_and_private` with query `from:me has:question after:-3d` to find threads where the user asked something and has not received a reply. Flag any unanswered threads.

If no blockers are found, the section reads: `None identified.`

---

## STEP 4: FORMAT REPORT

Assemble the three sections into a standup report:

```
--- STANDUP [YYYY-MM-DD] ---

Yesterday
- [bullet]
- [bullet]

Today
- [ONE-XXXX — Summary (In Progress)](https://clearlink.atlassian.net/browse/ONE-XXXX)
- [ONE-XXXX — Summary (To Do)](https://clearlink.atlassian.net/browse/ONE-XXXX)
- [item from Slack if applicable]

Blockers
- [ONE-XXXX — reason blocked](https://clearlink.atlassian.net/browse/ONE-XXXX)
  OR
- None identified.
```

Print the formatted report to the user before attempting to post.

---

## STEP 5: POST TO SLACK

Call `registry_get_project('private-dotfiles', 'standup.slack_channel')` via the clearlink-registry MCP.

**If a channel is returned:** post the formatted report to that channel using `slack_send_message`. Confirm:

```
STANDUP STATUS: POSTED — #[channel-name]
```

**If the key is not set or the registry call fails:** print setup instructions:

```
STANDUP STATUS: CHANNEL NOT CONFIGURED

To enable automatic posting, set the Slack channel in the registry:

  registry_set_project('private-dotfiles', 'standup.slack_channel', '#your-channel')

Run /standup again once the channel is configured.
```
