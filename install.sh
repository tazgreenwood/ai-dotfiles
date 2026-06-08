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

# ── Registry MCP Server ────────────────────────────────────────────────────────
if [ -f "$DOTFILES/claude/mcp/server/index.js" ]; then
  echo "Configuring registry MCP server..."
  # Patch settings.json mcpServers block (requires jq)
  SETTINGS="$CLAUDE_DIR/settings.json"
  if [ -f "$SETTINGS" ] && command -v jq &>/dev/null; then
    tmp=$(mktemp)
    jq --arg path "$DOTFILES/claude/mcp/server/index.js" \
      '.mcpServers.registry = {"command": "node", "args": [$path]}' \
      "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
    echo "  ✓ registry MCP wired to settings.json"
  else
    echo "  ⚠ Add manually to ~/.claude/settings.json:"
    echo '    "mcpServers": { "registry": { "command": "node", "args": ["'"$DOTFILES/claude/mcp/server/index.js"'"] } }'
  fi
fi

echo ""
echo "Done. Run 'git pull && ./install.sh' to update."
