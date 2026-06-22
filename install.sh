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

echo ""
echo "Add to /etc/hosts for clean URL:"
echo "  sudo sh -c 'echo \"127.0.0.1 registry.local\" >> /etc/hosts'"

echo ""
echo "Done. Run 'git pull && ./install.sh' to update."
