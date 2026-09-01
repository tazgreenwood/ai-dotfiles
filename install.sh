#!/usr/bin/env bash
set -e

DOTFILES="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_DIR="$HOME/.claude"

echo "Installing private-dotfiles from $DOTFILES"

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

# ── Bitbucket MCP Server ───────────────────────────────────────────────────────
# Built from source like the registry server above. ~/.claude.json points the
# bitbucket MCP server at this path, and the binary is gitignored, so a fresh
# clone has nothing here until this runs.
BB_DIR="$DOTFILES/claude/mcp/bitbucket"
BB_BINARY="$BB_DIR/bitbucket"

if [ -f "$BB_DIR/main.go" ]; then
  echo "Building bitbucket MCP server..."
  if command -v go &>/dev/null; then
    (cd "$BB_DIR" && go build -o bitbucket .) && echo "  ✓ built $BB_BINARY"
  else
    echo "  ⚠ Go not found — install Go then re-run install.sh"
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

# ── Stuart (team lead) Slack credentials ───────────────────────────────────────
# Stuart is invoked by hand (`/lead <request>` / `/lead check`), so there is no
# launchd job and no long-lived Claude token on disk — a human is present for
# every run. This block only provisions the Slack tokens Stuart needs to post a
# proposal to your phone and read the approve/pushback replies back.
STUART_ENV="$HOME/.config/stuart/env"

echo "Configuring Stuart Slack credentials..."

mkdir -p "$HOME/.config/stuart"
chmod 700 "$HOME/.config/stuart"

# One-time migration from the old jarvis path. Move, never copy: two files with
# live tokens is one more place to leak from.
if [ -f "$HOME/.config/jarvis/env" ] && [ ! -f "$STUART_ENV" ]; then
  mv "$HOME/.config/jarvis/env" "$STUART_ENV"
  chmod 600 "$STUART_ENV"
  echo "  ✓ migrated ~/.config/jarvis/env → $STUART_ENV"
fi

# NEVER clobber an existing env file — it holds live tokens.
if [ -f "$STUART_ENV" ]; then
  chmod 600 "$STUART_ENV"
  echo "  ✓ $STUART_ENV already exists — left untouched"
else
  umask 177
  cat > "$STUART_ENV" <<'ENVEOF'
# Stuart's Slack credentials. Sourced at run time by the /lead skill; NOT in the
# repo and must never be committed. Mode 600.
#
# Slack bot token (the bot-token kind, not a user token). Used to POST the
# proposal — chat:write is sufficient. It must be a BOT token: Slack suppresses
# push notifications for messages you author yourself, so a user token posts
# successfully and never reaches your phone.
#SLACK_BOT_TOKEN=
#
# OPTIONAL — currently unnecessary. Verified 2026-09-01 that BOTH reads
# `/lead check` needs work with the bot token alone: conversations.replies
# (per-thread, the decisions half) and conversations.history (channel scan, the
# poll half) each return ok:true. Leave unset.
#SLACK_USER_TOKEN=
ENVEOF
  umask 022
  chmod 600 "$STUART_ENV"
  echo "  ✓ created $STUART_ENV (mode 600) with placeholders"
  echo "  ⚠ ACTION REQUIRED: fill in SLACK_BOT_TOKEN"
fi


echo ""
echo "Add to /etc/hosts for clean URL:"
echo "  sudo sh -c 'echo \"127.0.0.1 registry.local\" >> /etc/hosts'"

echo ""
echo "Done. Run 'git pull && ./install.sh' to update."
