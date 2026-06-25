import { describe, expect, test } from "bun:test";
import { getWtDir } from "./wt-path";

const HOME = process.env.HOME ?? "/tmp";
const DEFAULT_BASE = `${HOME}/.worktrees`;

describe("getWtDir", () => {
  test("ssh remote → correct repoName", () => {
    const result = getWtDir(
      "/repo",
      "git@github.com:tazgreenwood/private-dotfiles.git",
      "main",
    );
    expect(result).toBe(`${DEFAULT_BASE}/private-dotfiles/main`);
  });

  test("https remote → correct repoName", () => {
    const result = getWtDir(
      "/repo",
      "https://github.com/tazgreenwood/private-dotfiles.git",
      "main",
    );
    expect(result).toBe(`${DEFAULT_BASE}/private-dotfiles/main`);
  });

  test("branch with slash → uses basename only (not full path)", () => {
    const result = getWtDir(
      "/repo",
      "git@github.com:tazgreenwood/private-dotfiles.git",
      "feat/DOTFILES-6-worktree-scripts",
    );
    expect(result).toBe(
      `${DEFAULT_BASE}/private-dotfiles/DOTFILES-6-worktree-scripts`,
    );
  });

  test("branch without slash → used as-is", () => {
    const result = getWtDir(
      "/repo",
      "git@github.com:tazgreenwood/private-dotfiles.git",
      "my-feature",
    );
    expect(result).toBe(`${DEFAULT_BASE}/private-dotfiles/my-feature`);
  });

  test("WORKTREE_BASE override respected", () => {
    const customBase = "/custom/worktrees";
    const result = getWtDir(
      "/repo",
      "git@github.com:tazgreenwood/private-dotfiles.git",
      "feat/DOTFILES-6-worktree-scripts",
      customBase,
    );
    expect(result).toBe(
      `${customBase}/private-dotfiles/DOTFILES-6-worktree-scripts`,
    );
  });
});
