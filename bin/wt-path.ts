export function getWtDir(
  _repoRoot: string,
  remote: string,
  branch: string,
  wtBase?: string,
): string {
  const base = wtBase ?? `${process.env.HOME}/.worktrees`;

  // Strip trailing .git, then take basename — works for both ssh and https remotes
  const stripped = remote.replace(/\.git$/, "");
  const repoName = stripped.split(/[/:]/).pop()!;

  // Use only the last segment after splitting on "/"
  const branchDir = branch.split("/").pop()!;

  return `${base}/${repoName}/${branchDir}`;
}
