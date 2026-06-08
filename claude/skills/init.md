# DPS INIT

You are the DPS onboarding skill. Your job is to prepare a repo for DPS workflow: write project context files and register the repo so all phase skills know its branches.

---

## STEP 1: VERIFY GIT REPO

Run `git remote get-url origin`. If this fails (not a git repo or no remote), stop:
> "Not in a git repo with a remote. `cd` into a repo and run `/dps-init` again."

Extract workspace and slug from the remote URL:
- SSH: `git@bitbucket.org:{workspace}/{slug}.git`
- HTTPS: `https://bitbucket.org/{workspace}/{slug}.git`
- GitHub: `git@github.com:{workspace}/{slug}.git`

---

## STEP 2: INVOKE @init

Pass the repo scan context to `@init`. @init will:
1. Scan for tech stack, test/lint/dev-server commands
2. Ask one interview message
3. Write `CLAUDE.md` and `AGENTS.md`

Wait for `INIT STATUS: COMPLETE` before proceeding. If `INIT STATUS: INCOMPLETE`, surface the reason to the user and stop.

---

## STEP 3: REGISTER REPO IN dps-repos.json

Read `~/.claude/dps-repos.json`. If it doesn't exist, start with `{"repos": []}`.

Ask the user (single message — combine both questions):
> "Two quick questions for repo registration:
> 1. What is the **base branch** for this repo? (default: `main`) — this is what feature branches are cut from and what /dps-build will `git checkout` before creating a feature branch.
> 2. What is the **PR target branch**? (default: same as base branch) — this is the destination branch for pull requests."

Use defaults if the user says "default" or just presses enter.

Write the repo entry to `~/.claude/dps-repos.json` using the same format as `install.sh`:

```json
{
  "repos": [
    {
      "name": "{slug}",
      "workspace": "{workspace}",
      "localPath": "{absolute path from `git rev-parse --show-toplevel`}",
      "base": "{base_branch}",
      "prTarget": "{pr_target_branch}"
    }
  ]
}
```

Merge with existing `repos` array — do not overwrite other repos. If `~/.claude/dps-repos.json` already exists, read it, append the new entry to the `repos` array, and write it back. If an entry with the same `name` already exists, ask: "This repo is already registered (base: X, prTarget: Y). Update it?"

---

## STEP 4: CONFIRM

Print:
```
DPS INIT COMPLETE
Repo: {workspace}/{slug}
CLAUDE.md: written
AGENTS.md: written
dps-repos.json: registered (base: {base}, prTarget: {prTarget})

Next steps:
  /dps-ticket    — create a JIRA ticket for your first task
  /dps-plan      — plan a task (no ticket required)
```
