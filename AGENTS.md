# S78 engineering documentation rules

## Hard completion gate

Any change that materially alters behavior, architecture, data flow, storage, interfaces, failure handling, or operator workflow is incomplete until the owner can explain the corresponding code without reading a file list.

Before editing, read `README.md`, the matching document under `docs/`, and the real entrypoints. Preserve the current canonical document instead of creating a parallel summary.

## Workspace hygiene

- Before repository-wide work, run `make repo-audit` and record the exact comparison SHA. The command is read-only and does not fetch, switch, delete, or clean anything.
- Treat a dirty or behind primary checkout as investigation evidence, not as the integration baseline. Start changes from a clean, verified commit in an independent worktree.
- Keep one owned change scope per branch/worktree. Do not mix unrelated research, release, production, and documentation changes in the same lane.
- `ancestor` in the audit only means the worktree HEAD is reachable from the comparison ref. Dirty files, squashed changes, PR state, deployment, and cleanup safety require separate evidence.
- Do not create new root-level `PLAN.md`, `TODO.md`, or session handoff files. Merge stable knowledge into the existing canonical README/docs; keep historical snapshots explicitly marked as historical.
- Never remove a worktree or branch until its unique changes and dirty files are accounted for and the owner confirms cleanup after reviewing the audit report.

For each material engineering delivery:

1. Explain the problem and the observable result.
2. Show the end-to-end data or control flow.
3. Record the design decision, rejected alternative, cost, and boundary.
4. Identify three to five code entrypoints and explain their order; do not dump the repository tree.
5. Define unfamiliar architecture terms with an accurate meaning, a plain-language analogy, and their location in this project.
6. Include failure, degradation, retry, and recovery behavior.
7. Add a 60-second owner explanation and closed-book questions.
8. Update the README document index and the one canonical topic document.

## Evidence labels

Never collapse different evidence levels into “done.” Distinguish:

- `implemented`: the behavior exists in the current code.
- `build-verified`: build, static checks, or automated tests passed.
- `integration-verified`: real dependencies exchanged data and the result was checked.
- `environment-pending`: an external system or deployment is still unverified.
- `production-recommendation`: a proposed improvement, not current behavior.

Dynamic counts such as exchange symbols are snapshots, not permanent scale claims. A package with no test files is not test coverage. Compile success is not a real Redis, PostgreSQL, exchange, or Doris integration test.

## Verification and safety

Run checks proportional to the change and record exact commands and remaining gaps. For repository-wide Go or frontend changes, the default gate is:

```bash
go build ./...
go vet ./...
go test ./...
cd frontend && npm run build
git diff --check
```

Do not read, copy, commit, or document `.env` values, credentials, authentication state, client databases, caches, or complete transcripts. Do not overwrite unrelated dirty worktree changes. Do not claim online scale, SLA, incidents, or production validation without evidence.
