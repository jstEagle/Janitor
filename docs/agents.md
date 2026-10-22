# Setting up Janitor from an agent

Janitor can inspect agent workspaces without trusting a model to decide what files are expendable. Its deletion policy is deterministic and configured by the machine owner.

## Installation and discovery

1. Check for `janitor` and `git`, then read `janitor help`. Install a released binary or build with Go 1.25+ if requested.
2. Use `janitor config` to inspect existing policy. Do not replace an existing config or create a second state directory for the same roots.
3. On a new setup, use `janitor init --root /absolute/development/root`. Include hidden agent roots only when they exist and the user wants them scanned. All configuration belongs in the JSON config file; do not introduce environment-variable configuration.
4. Run `janitor --json scan`. Report unknown storage, failed checks, and registered worktrees outside the configured roots alongside known artifacts. Never equate allocated size with guaranteed reclaimable space.

Global flags (`--json`, `--config`, `--state`) precede the subcommand.

## Enrollment is a lifecycle agreement

Only enroll a workspace when the owner authorizes deletion of the chosen artifact kinds and every user of that workspace will cooperate with Janitor's activity lease. A general request to inspect disk usage does not authorize enrollment, automatic cleanup, or arbitrary deletion.

```sh
janitor enroll /work/task --coordinated --artifacts .next,node_modules
janitor run --dir /work/task -- claude
```

The orchestrator should wrap the agent session before it starts. Do not enroll the currently running unwrapped agent and assume its existing session has acquired a lease. An agent creating a worktree during a session must enroll and wrap activity in that new worktree separately; a lease on one workspace does not protect sibling worktrees.

Keep servers and builds in the wrapped foreground lifecycle. Do not launch detached/background jobs that outlive that lifecycle. Pins are a useful protection for unsupported editor/application workflows.

Do not overwrite Claude's `WorktreeCreate` hook to register Janitor: that hook replaces worktree creation. This release does not install hooks or change Codex/Claude/Cursor settings. App-managed retirement should go through the owner application.

## Cleaning up

```sh
janitor --json plan
janitor apply PLAN_ID --yes
janitor --json history
```

Show the saved plan's exact actions before applying when user approval is required in your environment. Apply only the reviewed ID. A failed precondition means stop and inspect; never work around it with `rm -rf`, `git clean`, `git worktree remove --force`, edited state, or artificially aged observations.

Artifact deletion is permanent. Ignored files may be secrets, data, or work. Do not expand the artifact list merely because a folder is large. Source retention and build-output removal are different operations.

Automatic mode requires the owner's explicit policy choice. It respects the same checks as manual apply. Disk pressure is never permission to weaken a protection. If no eligible space remains, explain the blocker instead of deleting unknown files.

## Recovery and diagnostics

- `explain PATH`: scan and show activity/protection evidence.
- `leases`: persistent active or unresolved session records.
- `release ID --yes`: only after a person or orchestrator verifies that session and children have stopped. Never automatically age out leases.
- `pin PATH --reason TEXT`: conservatively protect an entire intersecting workspace.
- `forget PATH`: revoke cleanup enrollment.
- `history`: inspect completed, partial, and interrupted attempts.

Interrupted `.janitor-removing-*` directories require manual inspection. They may contain partially deleted artifacts. Do not describe them as verified backups. A retired worktree's branch remains available, but deleted build artifacts do not have an undo operation.

Keep state private: file paths, branch names, and lease metadata can reveal project information. Do not upload state or inventories without authorization. Each machine manages its own state; JSON output can be collected over SSH if requested.
