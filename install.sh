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
SERVER_DIR="$DOTFILES/claude/mcp/server"
BINARY="$SERVER_DIR/clearlink-registry"

if [ -f "$SERVER_DIR/main.go" ]; then
  echo "Building registry MCP server..."
  if command -v go &>/dev/null; then
    (cd "$SERVER_DIR" && go build -o clearlink-registry .) && echo "  ✓ built $BINARY"
  else
    echo "  ⚠ Go not found — install Go then re-run install.sh"
  fi
fi

if [ -f "$BINARY" ]; then
  if claude mcp list 2>/dev/null | grep -q "clearlink-registry"; then
    echo "  ✓ clearlink-registry already registered"
  else
    echo "  Registering clearlink-registry MCP server (user scope)..."
    echo "  Enter BITBUCKET_USERNAME (e.g. taz.greenwood@clearlink.com):"
    read -r BB_USER
    echo "  Enter BITBUCKET_APP_PASSWORD:"
    read -rs BB_PASS
    claude mcp add --scope user clearlink-registry "$BINARY" \
      -e BITBUCKET_USERNAME="$BB_USER" \
      -e BITBUCKET_APP_PASSWORD="$BB_PASS" \
      -e BITBUCKET_WORKSPACE="clearlinkit"
    echo "  ✓ clearlink-registry registered"
  fi
fi

echo ""
echo "Done. Run 'git pull && ./install.sh' to update."
