# Architecture

Janitor has one executable and a dependency-free core in `internal/janitor`.

```text
configured roots → discovery → workspace / artifact measurements
                                     ↓
                         observations + enrollments + leases
                                     ↓
                          deterministic policy → saved plan
                                     ↓
                   fresh reconciliation → exclusive workspace lock
                                     ↓
                       journal → bounded deletion → result
```

## State and locking

The default state directory is `~/.local/state/janitor` with mode 0700. Snapshots and records are mode 0600, written to a same-directory temporary file, synced, renamed, and followed by a directory sync.

`state.json` holds enrollment, pins, and the latest observation for each workspace. `inventory.json` holds the last scan. `plans/`, `leases/`, `journal/`, and `locks/` store plans, session records, operation results, and advisory lock files. Lock files are never removed while the installation is in use; removing one could split synchronization across inodes.

A global flock serializes mutations and scans. A wrapper briefly holds that lock while registering, then holds a shared workspace flock during execution. Cleanup obtains an exclusive nonblocking workspace lock. The wrapper passes the descriptor to the child and closes its own descriptor when done; descendants that retain the descriptor retain the lock. The cooperation contract excludes daemonizing commands that discard inherited descriptors and outlive the session.

Persistent lease records survive crashes. No PID timeout authorizes deletion. Signal-terminated commands retain their record; normal completion, including an ordinary nonzero exit, removes it. Explicit abandoned-lease release requires an unheld lock and resets observations.

All instances managing a root must share the same state directory and user. Advisory locks cannot constrain unrelated programs or hostile same-user mutations.

## Measurement and observation

Discovery finds `.git` entries under roots and asks Git for linked worktrees using NUL-separated porcelain output. Registered paths outside configured roots are warnings. Metadata walks do not follow symlinks and report mount crossings, special files, nested repositories, and errors.

A fingerprint includes relative paths, inode/device identity, type/mode, sizes, and nanosecond mtimes, plus workspace HEAD and branch. It is an observation of ordinary changes, not a cryptographic hash of file contents. No file contents or credentials are copied into state.

Workspace sizing excludes `.git` metadata, which is measured separately once per common Git directory. Artifact sizes are subsets of workspace sizes. Independently measured trees may share hardlinks or cloned blocks, so the inventory deliberately does not advertise a single summed physical-savings number. Unknown data outside discovered workspaces remains visible in root totals.

The initial release uses full periodic scans. It does not depend on lossy filesystem events for safety. A future event adapter can invalidate cached subtrees, but must preserve periodic reconciliation and overflow recovery.

## Policy and execution

New observations start an idle grace period. Enrollment and session completion reset it. Unknown state blocks deletion. Artifact cleanup requires a matching ecosystem marker, an explicitly enrolled kind, no tracked files or nested repository, an idle workspace, and no workspace protection. Full retirement additionally requires dual opt-in, a clean linked tree without ignored data, no locks/submodules, and retained branch/remote-tracking evidence.

A plan captures policy and action fingerprints. Apply re-scans and verifies every planned action before mutation; if any differs, the whole plan is rejected. It then rechecks each action under its workspace lock. Multiple artifact actions in one workspace are allowed: their individual fingerprints remain valid as earlier siblings are removed. A new workspace observation is required after successful cleanup.

Artifact execution opens a traversal-resistant root for the workspace, verifies its identity, opens the artifact parent relative to that root, checks target identity, renames the target to a unique sibling, verifies captured identity, and removes that sibling. It does not follow symlinks or intentionally cross mounts. The cooperation contract is necessary to prevent arbitrary external writes during inspection and execution. Git retirement uses `git worktree remove` without force and never deletes refs.

Journals are written before execution and after each action. Interrupted attempts cannot be replayed; new plans must re-observe current state. A `running` journal is evidence of an interrupted or unfinished attempt, not a claim of rollback. There is no automatic rollback after irreversible artifact removal.

The daemon runs the same engine and only applies under configured disk pressure when automatic mode is enabled. If protected or active data prevents reaching the target, it retains that data. It has no job admission control or hard quotas.

## Scope and future work

The research proposed SQLite, filesystem watchers, owner integrations, and broader cache adapters. The first implementation uses atomic snapshots to simplify distribution, explicit leases to establish activity, and conservative Git retirement. Native application lifecycle APIs, watcher-driven sizing, verified source archives, global package-cache GC, Docker GC integration, and admission control remain separate future features. None are implied by current setup commands.
