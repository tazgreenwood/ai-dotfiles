#!/usr/bin/env bash
set -e

DOTFILES="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_DIR="$HOME/.claude"

echo "Installing private-dotfiles from $DOTFILES"

JARVIS_LABEL="com.tazgreenwood.jarvis"
JARVIS_LAUNCHAGENT="$HOME/Library/LaunchAgents/$JARVIS_LABEL.plist"

# ── Uninstall subcommand ───────────────────────────────────────────────────────
# `./install.sh uninstall-jarvis` removes only the poller: it unloads the job and
# deletes the installed plist. It deliberately does NOT touch ~/.config/jarvis/env
# (your tokens) or the logs.
if [ "${1:-}" = "uninstall-jarvis" ]; then
  if [ -f "$JARVIS_LAUNCHAGENT" ]; then
    launchctl unload "$JARVIS_LAUNCHAGENT" 2>/dev/null || true
    rm -f "$JARVIS_LAUNCHAGENT"
    echo "  ✓ jarvis poller unloaded and removed"
  else
    echo "  ✓ jarvis poller not installed — nothing to do"
  fi
  echo "  ℹ left in place: ~/.config/jarvis/env and ~/.config/jarvis/logs/"
  exit 0
fi


# ── Claude Skills ──────────────────────────────────────────────────────────────
echo "Linking Claude skills..."
mkdir -p "$CLAUDE_DIR/skills"
for skill_file in "$DOTFILES/claude/skills"/*.md; do
  skill_name=$(basename "$skill_file" .md)
  skill_dir="$CLAUDE_DIR/skills/$skill_name"
  mkdir -p "$skill_dir"
  ln -sf "$skill_file" "$skill_dir/SKILL.md"
  echo "  ✓ $skill_name"
done

# ── Claude Agents ──────────────────────────────────────────────────────────────
echo "Linking Claude agents..."
mkdir -p "$CLAUDE_DIR/agents"
for agent_file in "$DOTFILES/claude/agents"/*.md; do
  ln -sf "$agent_file" "$CLAUDE_DIR/agents/$(basename "$agent_file")"
  echo "  ✓ $(basename "$agent_file" .md)"
done

# ── Claude Hooks ──────────────────────────────────────────────────────────────
echo "Linking Claude hooks..."
mkdir -p "$CLAUDE_DIR/hooks"
for hook_file in "$DOTFILES/claude/hooks"/*.js; do
  [ -f "$hook_file" ] || continue
  ln -sf "$hook_file" "$CLAUDE_DIR/hooks/$(basename "$hook_file")"
  echo "  ✓ $(basename "$hook_file")"
done

# Register inject-registry-context in UserPromptSubmit hooks if not already present
SETTINGS="$CLAUDE_DIR/settings.json"
HOOK_CMD="node \"$CLAUDE_DIR/hooks/inject-registry-context.js\""
if [ -f "$SETTINGS" ] && command -v node &>/dev/null; then
  if ! grep -q "inject-registry-context" "$SETTINGS"; then
    node - "$SETTINGS" "$HOOK_CMD" <<'EOF'
const fs = require('fs');
const [,, settingsPath, hookCmd] = process.argv;
const s = JSON.parse(fs.readFileSync(settingsPath, 'utf8'));
if (!s.hooks) s.hooks = {};
if (!s.hooks.UserPromptSubmit) s.hooks.UserPromptSubmit = [];
s.hooks.UserPromptSubmit.push({
  hooks: [{ command: hookCmd, timeout: 5, type: 'command' }]
});
fs.writeFileSync(settingsPath, JSON.stringify(s, null, 2) + '\n');
EOF
    echo "  ✓ inject-registry-context registered in UserPromptSubmit hooks"
  else
    echo "  ✓ inject-registry-context already registered"
  fi
fi

# ── Claude Workflows ──────────────────────────────────────────────────────────
echo "Linking Claude workflows..."
mkdir -p "$CLAUDE_DIR/workflows"
for workflow_file in "$DOTFILES/claude/workflows"/*.js; do
  [ -f "$workflow_file" ] || continue
  ln -sf "$workflow_file" "$CLAUDE_DIR/workflows/$(basename "$workflow_file")"
  echo "  ✓ $(basename "$workflow_file")"
done

# ── Registry MCP Server ────────────────────────────────────────────────────────
SERVER_DIR="$DOTFILES/claude/mcp/server"
BINARY="$SERVER_DIR/registry"

if [ -f "$SERVER_DIR/main.go" ]; then
  echo "Building registry MCP server..."
  if command -v go &>/dev/null; then
    (cd "$SERVER_DIR" && go build -o registry .) && echo "  ✓ built $BINARY"
  else
    echo "  ⚠ Go not found — install Go then re-run install.sh"
  fi
fi

if [ -f "$BINARY" ]; then
  if claude mcp list 2>/dev/null | grep -q "^registry"; then
    echo "  ✓ registry already registered"
  else
    echo "  Registering registry MCP server (user scope)..."
    echo "  Enter BITBUCKET_USERNAME (e.g. taz.greenwood@clearlink.com):"
    read -r BB_USER
    echo "  Enter BITBUCKET_APP_PASSWORD:"
    read -rs BB_PASS
    claude mcp add --scope user registry "$BINARY" \
      -e BITBUCKET_USERNAME="$BB_USER" \
      -e BITBUCKET_APP_PASSWORD="$BB_PASS" \
      -e BITBUCKET_WORKSPACE="clearlinkit"
    echo "  ✓ registry registered"
  fi
fi

# ── Claude Desktop MCP servers ──────────────────────────────────────────────────
# Claude Desktop reads its own config, separate from ~/.claude.json (CLI-only).
# Mirror the registry + bitbucket servers already configured for the CLI so both
# surfaces see the same tools.
DESKTOP_CONFIG="$HOME/Library/Application Support/Claude/claude_desktop_config.json"
CLI_CONFIG="$HOME/.claude.json"

if [ "$(uname)" = "Darwin" ] && [ -f "$DESKTOP_CONFIG" ] && [ -f "$CLI_CONFIG" ] && command -v node &>/dev/null; then
  echo "Registering MCP servers with Claude Desktop..."
  node - "$DESKTOP_CONFIG" "$CLI_CONFIG" <<'EOF'
const fs = require('fs');
const [, , desktopPath, cliPath] = process.argv;
const desktop = JSON.parse(fs.readFileSync(desktopPath, 'utf8'));
const cli = JSON.parse(fs.readFileSync(cliPath, 'utf8'));
if (!desktop.mcpServers) desktop.mcpServers = {};
let changed = false;
for (const name of ['registry', 'bitbucket']) {
  const entry = cli.mcpServers && cli.mcpServers[name];
  if (!entry) {
    console.log(`  ⚠ ${name} not found in ~/.claude.json — skipping`);
    continue;
  }
  if (desktop.mcpServers[name]) {
    console.log(`  ✓ ${name} already registered in Claude Desktop`);
    continue;
  }
  desktop.mcpServers[name] = entry;
  changed = true;
  console.log(`  ✓ ${name} added to Claude Desktop config`);
}
if (changed) {
  fs.writeFileSync(desktopPath, JSON.stringify(desktop, null, 2) + '\n');
  console.log('  ⚠ Restart Claude Desktop to pick up the new MCP servers');
}
EOF
fi

# ── Registry UI ────────────────────────────────────────────────────────────────
UI_DIR="$DOTFILES/claude/ui"
UI_BINARY="/usr/local/bin/registry-ui"
UI_PLIST="$UI_DIR/com.tazgreenwood.registry-ui.plist"
UI_LAUNCHAGENTS="$HOME/Library/LaunchAgents/com.tazgreenwood.registry-ui.plist"

if [ -f "$UI_DIR/main.go" ]; then
  echo "Building registry UI..."
  if command -v go &>/dev/null; then
    (cd "$UI_DIR" && go build -o registry-ui .) && echo "  ✓ built $UI_DIR/registry-ui"
    if sudo cp "$UI_DIR/registry-ui" "$UI_BINARY" 2>/dev/null; then
      echo "  ✓ installed to $UI_BINARY"
    else
      echo "  ⚠ Could not copy to /usr/local/bin — run: sudo cp $UI_DIR/registry-ui $UI_BINARY"
    fi
  else
    echo "  ⚠ Go not found — install Go then re-run install.sh"
  fi
fi

mkdir -p "$HOME/.config/registry/logs"

if [ -f "$UI_BINARY" ] && [ -f "$UI_PLIST" ]; then
  cp "$UI_PLIST" "$UI_LAUNCHAGENTS"
  launchctl unload "$UI_LAUNCHAGENTS" 2>/dev/null || true
  launchctl load "$UI_LAUNCHAGENTS"
  echo "  ✓ registry-ui daemon loaded (auto-starts on login)"
fi

# ── Jarvis unattended poller ───────────────────────────────────────────────────
# Two-tier launchd job. Tier 1 is pure shell (registry read + one Slack
# conversations.history call) and spawns NO agent, so a quiet tick costs $0.
# Tier 2 escalates to `claude -p '/jarvis poll'` only when tier 1 sees a message
# NEWER than the persisted watermark (~/.config/jarvis/last_seen_ts), and only
# while the day's spend is under the cap.
#
# COST CAP: the per-run cap is the escalation's own --max-budget-usd flag
# ($JARVIS_RUN_USD_CAP, default 0.50) — claude aborts the run when the spend
# passes it, so no single tick can overshoot. Two additional guards sit on top:
# a per-UTC-day ledger at ~/.config/jarvis/spend-YYYY-MM-DD.txt accumulates each
# run's total_cost_usd and suppresses further escalation once it reaches
# $JARVIS_DAILY_USD_CAP (default 2.00), and --max-turns bounds a single run's
# length. The watermark advances on EVERY tick, so one message can trigger at
# most one escalation.
#
# PERMISSION MODE: tier 2 runs --permission-mode default rather than dontAsk.
# Under -p, default neither prompts nor hangs, and tier 2 needs write tools that
# the global allowlist does not cover; dontAsk would auto-deny them.
#
# CREDENTIALS: the committed plist contains NO secrets. It sources
# ~/.config/jarvis/env (mode 600) at run time and references only the variable
# NAMES $SLACK_BOT_TOKEN, $SLACK_USER_TOKEN and $CLAUDE_CODE_OAUTH_TOKEN.
JARVIS_DIR="$DOTFILES/claude/jarvis"
JARVIS_PLIST="$JARVIS_DIR/$JARVIS_LABEL.plist"
JARVIS_ENV="$HOME/.config/jarvis/env"

echo "Installing Jarvis poller..."

# Logs live under ~/.config/jarvis/logs — outside the repo, so a poll never
# dirties the working tree. launchd cannot create these itself.
mkdir -p "$HOME/.config/jarvis/logs"
touch "$HOME/.config/jarvis/logs/poll.log"
chmod 700 "$HOME/.config/jarvis"

# NEVER clobber an existing env file — it holds live tokens.
if [ -f "$JARVIS_ENV" ]; then
  chmod 600 "$JARVIS_ENV"
  echo "  ✓ $JARVIS_ENV already exists — left untouched"
else
  umask 177
  cat > "$JARVIS_ENV" <<'ENVEOF'
# Jarvis credentials. This file is sourced by the launchd poller; it is NOT in
# the repo and must never be committed. Mode 600.
#
# Slack bot token (the bot-token kind, not a user token). Used to POST — chat:write
# is sufficient for that and is already granted.
#SLACK_BOT_TOKEN=
#
# READ credential. The Jarvis channel is a PRIVATE channel, so reading its
# history needs the groups:history scope, which the aiportal bot token does NOT
# currently hold (granted: channels:history, chat:write, commands). Pick ONE:
#   (a) add groups:history to the aiportal Slack app and reinstall it — then the
#       bot token above is enough and you can leave SLACK_USER_TOKEN unset; or
#   (b) paste a user token that holds groups:history below. When set, the poller
#       reads with it in preference to the bot token.
# Until one of these is done, every tick logs 'unconfigured: slack read ...
# denied (missing_scope)' with the remediation and exits 0 without spawning an
# agent — the job does not hard-fail, it just cannot see incoming messages.
#SLACK_USER_TOKEN=
#
# Long-lived Claude Code token, so the poller does not depend on keychain auth.
# Mint it interactively (it cannot be created unattended):
#   claude setup-token
# then paste the value here.
#CLAUDE_CODE_OAUTH_TOKEN=
ENVEOF
  umask 022
  chmod 600 "$JARVIS_ENV"
  echo "  ✓ created $JARVIS_ENV (mode 600) with placeholders"
  echo "  ⚠ ACTION REQUIRED: fill in SLACK_BOT_TOKEN and CLAUDE_CODE_OAUTH_TOKEN"
  echo "    (run 'claude setup-token' for the latter — it is interactive)"
fi

# Surface the read-scope gap every install, not just on first creation: the
# poller cannot read the PRIVATE Jarvis channel until one of these is done.
if [ -f "$JARVIS_ENV" ] && ! grep -qE '^[[:space:]]*SLACK_USER_TOKEN=.' "$JARVIS_ENV"; then
  echo "  ⚠ ACTION REQUIRED (slack read scope): the Jarvis channel is PRIVATE, so"
  echo "    conversations.history needs groups:history, which the aiportal bot token"
  echo "    does not hold. Either add groups:history to the app and reinstall it, or"
  echo "    append SLACK_USER_TOKEN=<user token with groups:history> to $JARVIS_ENV."
  echo "    Until then each tick exits 0 with an 'unconfigured' log line."
fi

if [ -f "$JARVIS_PLIST" ]; then
  cp "$JARVIS_PLIST" "$JARVIS_LAUNCHAGENT"
  # Defense in depth: the plist holds no secret today, but keep it non-world-readable.
  chmod 600 "$JARVIS_LAUNCHAGENT"
  launchctl unload "$JARVIS_LAUNCHAGENT" 2>/dev/null || true
  launchctl load "$JARVIS_LAUNCHAGENT"
  echo "  ✓ jarvis poller loaded (polls every 300s; logs to ~/.config/jarvis/logs/poll.log)"
  echo "    daily cost cap: \$${JARVIS_DAILY_USD_CAP:-2.00} (override via JARVIS_DAILY_USD_CAP in $JARVIS_ENV)"
  echo "    per-run cost cap: \$${JARVIS_RUN_USD_CAP:-0.50} via claude --max-budget-usd (override via JARVIS_RUN_USD_CAP in $JARVIS_ENV)"
  echo "    tail -f ~/.config/jarvis/logs/poll.log"
  echo "    ./install.sh uninstall-jarvis   # to remove"
fi

echo ""
echo "Add to /etc/hosts for clean URL:"
echo "  sudo sh -c 'echo \"127.0.0.1 registry.local\" >> /etc/hosts'"

echo ""
echo "Done. Run 'git pull && ./install.sh' to update."
