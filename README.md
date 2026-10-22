# Janitor

Keep agent workspaces from filling your disk.

Janitor is a local, open-source CLI and background service for **macOS and Linux**, written in Go. It finds Git worktrees and their build artifacts, keeps a history of observed activity, and removes explicitly disposable data when your storage policy allows it.

No account, cloud service, or language model required. No runtime dependencies beyond Git.

```text
$ janitor scan
Inventory … · 38 workspaces
  …
$ janitor plan
Plan … · 4 actions · expires …
  8.4 GiB  artifact "/work/project-task/target"
  2.1 GiB  artifact "/work/site-task/node_modules"
  …
```

Illustrative output. Your plan depends on enrollment, activity, and protection checks.

## Install

Download an archive for your OS and CPU from [Releases](https://github.com/jstEagle/Janitor/releases), verify it against the release's `SHA256SUMS`, and put `janitor` on your PATH. macOS binaries are not Apple-signed or notarized; build from source if your organization's policy requires it.

With Go **1.25 or newer**:

```sh
go install github.com/jstEagle/Janitor/cmd/janitor@latest
```

Or build a checkout:

```sh
git clone https://github.com/jstEagle/Janitor.git
cd Janitor
go build -o bin/janitor ./cmd/janitor
./bin/janitor version
```

Git 2.31+ is required. Janitor runs as your user; do not run it with `sudo`. Windows is not supported yet.

## Start with a storage census

```sh
janitor init --root ~/projects --root ~/.codex/worktrees
janitor scan
janitor --json scan
janitor explain ~/projects/my-app
```

Use roots that exist on your machine. `init` never overwrites a config. Avoid overlapping roots; if agent worktrees already live inside a project root, they do not need another root entry.

The scan includes registered worktrees within those roots, recognized artifacts in nested packages, shared Git metadata, and storage outside discovered workspaces. Each root includes its 20 largest unattributed immediate children so large unknown directories remain visible. Registered worktrees outside your roots are reported but not scanned. Symlinks are not followed during traversal. Mount boundaries and unreadable data are reported.

A scan writes local observations but never deletes project files. `status` reads the last inventory and displays its timestamp; use `scan` to refresh it. `--json` provides the full inventory, including protection reasons and artifact sizes.

## Opt a workspace into cleanup

**Enrollment is a promise about how you use that workspace.** Every agent session, build, dev server, and other activity must run through `janitor run`. A timestamp cannot prove that an uncoordinated process has stopped using files.

```sh
janitor enroll ~/projects/my-app --coordinated --artifacts node_modules,.next
janitor run --dir ~/projects/my-app -- claude
# Or another foreground command:
janitor run --dir ~/projects/my-app -- npm run dev
```

Only enroll artifact kinds you consider disposable, including everything inside those directories. A dependency directory containing manual edits is not disposable. Enrollment never edits your agent's settings.

Supported kinds:

| Artifact | Required sibling project marker |
| --- | --- |
| `node_modules`, `.next`, `.nuxt`, `.turbo` | `package.json` |
| `target` | `Cargo.toml` |
| `.build` | `Package.swift` |
| `.venv` | `pyproject.toml`, `requirements.txt`, or `Pipfile` |

Recognition also works in nested packages. Tracked files and embedded repositories block artifact deletion. Source modifications do not by themselves block deletion of separately enrolled disposable outputs. Pins, leases, and workspace errors do.

Do not enroll a workspace used concurrently by an editor, application, or agent that bypasses the wrapper. Wrap the whole foreground session. Commands that daemonize, detach, or intentionally close inherited descriptors need their own lifecycle integration and are unsupported for automatic cleanup. Use discovery and reviewed owner-managed cleanup for those workflows.

Re-enrolling replaces the workspace's artifact list and retirement permission, and resets its observation period. Revoke enrollment with `janitor forget PATH`.

## Review and apply

```sh
janitor plan
janitor --json plan
janitor apply PLAN_ID --yes
janitor history
```

Each `plan` creates a new saved plan; use the ID from the plan you reviewed. A new workspace must first be observed idle for the configured duration, even if its files are old. Successful wrapper sessions, released abandoned leases, enrollment changes, and cleanup reset that grace period.

`apply` verifies current policy, identity, Git state, activity, and file metadata again. A changed plan is refused. Plans expire after 30 minutes by default and cannot be replayed after an attempt. There is no `--force` bypass.

**Artifact deletion is permanent.** Janitor renames an artifact within its parent before deleting it, so it cannot sweep a new build created at the original name into the same operation. This is an execution safeguard, not a trash or backup feature. Interrupted remnants named `.janitor-removing-*` are retained for manual inspection; they are not automatically swept later.

Protect a workspace or subtree:

```sh
janitor pin ~/projects/my-app --reason "Release in progress"
janitor unpin ~/projects/my-app
```

A pin intersecting a workspace protects the entire workspace. `protected_paths` in configuration provides additional persistent protection.

## Keep it running

Edit `~/.config/janitor/config.json` to set roots, retention, and storage thresholds. All non-secret configuration is in this file, not environment variables.

```json
{
  "version": 1,
  "roots": ["/absolute/path/to/projects"],
  "protected_paths": [],
  "artifact_idle_hours": 48,
  "worktree_idle_hours": 168,
  "interval_seconds": 900,
  "plan_ttl_minutes": 30,
  "min_free_bytes": 21474836480,
  "target_free_bytes": 32212254720,
  "automatic": false,
  "retire_worktrees": false
}
```

Validate with `janitor config`. Start in the foreground with `janitor daemon`, or install the current executable as a per-user service:

```sh
janitor service render    # inspect the definition
janitor service install  # install and start
janitor service uninstall
```

Install a permanent binary first; do not install a service from `go run` or a temporary build directory. macOS uses a LaunchAgent and Linux uses a systemd user service. Headless Linux machines may need administrator-configured user lingering. Install errors preserve the service definition and explain the failed step. If startup failed, remove that inactive definition before reinstalling.

With `automatic: false`, the service scans and produces plans only. Set `automatic: true` to permit cleanup **only for enrolled, eligible workspaces** when a volume drops below `min_free_bytes`. Cleanup aims for `target_free_bytes`. Restart the service after configuration changes; the running process uses its startup configuration.

`janitor sweep` performs one cycle. `daemon` emits compact JSON cycle reports to stdout. Linux collects these in the user journal; macOS service output is not persisted by Janitor, so inspect `status` and `history`. State and completed journals are retained locally; daemon maintenance removes expired plans and completed journals older than 30 days. Interrupted journals are retained.

When there are no safe candidates, Janitor leaves the data alone. It does not stop jobs or promise a hard disk quota.

## Worktree retirement

Artifact cleanup and full checkout removal have separate permissions:

```sh
janitor enroll /work/task-worktree --coordinated --artifacts target --retire
```

Also set `retire_worktrees: true` in configuration. Retirement requires an idle linked worktree with no modified, untracked, or ignored files; an unlocked, named branch preserving HEAD; and HEAD reachable from a local remote-tracking ref. Janitor never fetches automatically, removes primary checkouts, force-removes worktrees, or deletes branches.

Detached HEADs, unique/unpushed commits without remote-tracking reachability, submodules, and nested repositories are retained. Squash merges may conservatively remain protected. Remote-tracking refs are local evidence, not proof that a remote server still holds the data; the local branch is retained regardless.

Known Codex, Claude, and Cursor worktree locations are excluded from automatic full retirement. Other app-owned worktrees must not be enrolled for retirement: path recognition is not an exhaustive ownership detector. Use the application's archive/remove operation. For a clean retired ordinary worktree, the retained branch can recreate it:

```sh
git -C /path/to/main-repo worktree add /path/to/restored branch-name
```

This restores committed files, not deleted build artifacts. Janitor does not archive unfinished source or credentials.

## Crashed sessions

```sh
janitor leases
# Inspect the workspace and verify the session and its child processes are stopped.
janitor release LEASE_ID --yes
```

The wrapper holds a shared filesystem lock, while cleanup needs an exclusive lock. A persistent lease survives a wrapper crash or signal-terminated command. Janitor does not guess that a lease is abandoned from a PID or timeout. `release` refuses a held workspace lock and resets the idle grace period.

Use the **same state directory** for every wrapper and daemon managing a root. Independent state directories cannot coordinate locks or pins.

## What the numbers mean

JSON separates logical bytes, allocated bytes, and a conservative reclaim estimate. Hard links are counted once within each measured tree; files with multiple links are excluded from its reclaim estimate. Totals across independent workspaces can still share physical blocks and must not be summed as guaranteed savings.

APFS clones, snapshots, open deleted files, and concurrent writes affect real reclamation. Cleanup results include free space before and after, not an assertion that every byte of the difference was caused by Janitor. Moving files into trash would not free space on the same volume, so Janitor does not disguise trashing as reclamation.

## Current boundaries

- macOS and Linux local filesystems. Network filesystems are unsupported for cleanup and reliable locking.
- Periodic full metadata reconciliation, with serialized scans. No continuous filesystem watcher yet. Large cold scans can be I/O-intensive; adjust the interval.
- Atomic JSON snapshots and per-plan journals; no database server or third-party Go dependencies.
- Explicit workspace enrollment, not an inference that every quiet process is dead.
- No arbitrary shell cleanup hooks, cloud uploads, telemetry, Docker-volume pruning, model-cache deletion, or LLM-directed deletion.
- Metadata fingerprints detect ordinary edits; they are not content hashes or protection against a malicious same-user process preserving timestamps. This is a cooperative workflow tool, not a sandbox against hostile local users.

The [research](RESEARCH.md) records the longer-term design; this README describes what is implemented.

## For agents and contributors

Read [agent setup](docs/agents.md) before enrolling or cleaning a workspace. See [architecture](docs/architecture.md), [contributing](CONTRIBUTING.md), and [security reporting](SECURITY.md).

```sh
go test -race ./...
go vet ./...
go build ./cmd/janitor
```

Tests use isolated temporary repositories, including real Git worktree operations. Never point cleanup tests at a person's actual workspaces.

MIT licensed. Inspired by the problem space explored by [Kondo](https://github.com/tbillington/kondo), [Bonsai](https://github.com/sauravpanda/bonsai), and [Chaff](https://github.com/gulsahsarsilmaz/chaff); this implementation is independent.
