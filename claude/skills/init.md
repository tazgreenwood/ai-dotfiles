# INIT

You are the onboarding skill. Your job is to prepare a repo for the development workflow: write project context files and register the repo so all phase skills know its branches.

---

## STEP 1: VERIFY GIT REPO

Run `git remote get-url origin`. If this fails (not a git repo or no remote), stop:
> "Not in a git repo with a remote. `cd` into a repo and run `/init` again."

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

## STEP 3: REGISTER REPO IN REGISTRY

Ask the user (single message — combine both questions):
> "Two quick questions for repo registration:
> 1. What is the **base branch** for this repo? (default: `main`) — this is what feature branches are cut from and what /build will `git checkout` before creating a feature branch.
> 2. What is the **PR target branch**? (default: same as base branch) — this is the destination branch for pull requests."

Use defaults if the user says "default" or just presses enter.

Check if the repo is already registered: call `registry_get_project(slug)`. If it exists, ask:
> "This repo is already registered (base: X, prTarget: Y). Update it?"

Call `registry_init_project(slug, workspace, localPath, base, prTarget)` to register (or update) the repo.
- `localPath`: from `git rev-parse --show-toplevel`
- `base`: user-provided base branch (default: `main`)
- `prTarget`: user-provided PR target (default: same as base)

If registry MCP is unavailable, STOP immediately. Do not write any local file. Report: "Registry MCP is unavailable. Fix the MCP connection before running /init."

---

## STEP 4: CONFIRM

Print:
```
INIT COMPLETE
Repo: {workspace}/{slug}
CLAUDE.md: written
AGENTS.md: written
Registry: registered (base: {base}, prTarget: {prTarget})

Next steps:
  /ticket    — create a JIRA ticket for your first task
  /plan      — plan a task (no ticket required)
```
