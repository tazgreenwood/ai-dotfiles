#!/usr/bin/env node
// inject-registry-context — UserPromptSubmit hook
//
// Injects project resources and metadata from the registry into each session
// once. Subsequent prompts in the same session get a no-op (flag file check).
//
// Output format: system-reminder string consumed by Claude Code.

const fs = require('fs');
const path = require('path');
const os = require('os');
const { execFileSync } = require('child_process');

// ── Session dedup ──────────────────────────────────────────────────────────────

const sessionId = process.env.CLAUDE_CODE_SESSION_ID || '';
const flagPath = sessionId
  ? path.join(os.tmpdir(), `registry-injected-${sessionId}`)
  : null;

if (flagPath && fs.existsSync(flagPath)) {
  process.stdout.write('OK');
  process.exit(0);
}

// ── Detect project from git remote ────────────────────────────────────────────

function detectProjectName(cwd) {
  try {
    const remote = execFileSync('git', ['remote', 'get-url', 'origin'], {
      cwd,
      stdio: ['ignore', 'pipe', 'ignore'],
      encoding: 'utf8',
    }).trim();
    // Extract repo slug from SSH or HTTPS remote URL
    // e.g. git@bitbucket.org:tazgreenwood/private-dotfiles.git
    //      https://tazgreenwood@bitbucket.org/tazgreenwood/private-dotfiles.git
    const match = remote.match(/[/:]([^/:]+?)(?:\.git)?$/);
    return match ? match[1] : null;
  } catch (e) {
    return null;
  }
}

const cwd = process.cwd();
const projectName = detectProjectName(cwd);

if (!projectName) {
  process.stdout.write('OK');
  process.exit(0);
}

// ── Read project.json ──────────────────────────────────────────────────────────

const registryDir = process.env.REGISTRY_DATA_DIR ||
  path.join(os.homedir(), '.config', 'registry', 'data');
const projectFile = path.join(registryDir, projectName, 'project.json');

let project;
try {
  project = JSON.parse(fs.readFileSync(projectFile, 'utf8'));
} catch (e) {
  // No registry entry for this project — silent exit
  process.stdout.write('OK');
  process.exit(0);
}

// ── Build context block ────────────────────────────────────────────────────────

const lines = [`REGISTRY CONTEXT — ${projectName}`];

// Project metadata
const repo = project.repo || {};
const deploy = project.deploy || {};
if (repo.base) lines.push(`repo.base: ${repo.base}`);
if (repo.prTarget) lines.push(`repo.prTarget: ${repo.prTarget}`);
if (deploy.cluster) lines.push(`deploy.cluster: ${deploy.cluster}`);
if (deploy.logGroup) lines.push(`deploy.logGroup: ${deploy.logGroup}`);
if (deploy.env) lines.push(`deploy.env: ${deploy.env}`);

// Resources by category
const resources = project.resources || {};
const categories = Object.keys(resources);
if (categories.length > 0) {
  lines.push('');
  lines.push('Resources:');
  for (const cat of categories) {
    const entries = resources[cat];
    if (typeof entries === 'object' && entries !== null) {
      for (const [key, val] of Object.entries(entries)) {
        lines.push(`  ${cat}.${key}: ${val}`);
      }
    }
  }
}

const contextBlock = lines.join('\n');

// ── Write flag and emit ────────────────────────────────────────────────────────

if (flagPath) {
  try { fs.writeFileSync(flagPath, '1'); } catch (e) { /* non-fatal */ }
}

process.stdout.write(contextBlock);
