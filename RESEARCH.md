# Janitor: research and proposed approach

Research date: 22 October 2026.

## Recommendation

Build a Go CLI and per-user background service for macOS and Linux. Its central job is to attribute storage to development workspaces and retire disposable data under an explicit policy. Keep the cleanup engine deterministic, local, and independent of an LLM.

Start with read-only inventory, then add artifact cleanup, then coordinated worktree retirement. Treat these as separate capabilities with separate permissions. Support existing workspaces through discovery and stronger automation through an optional session wrapper or agent integration.

The useful differentiation is trustworthy unattended storage management across tools. A scanner that deletes old worktrees already has several competitors. Evaluate the closest tools against representative fixtures before committing to a new implementation; their documented features are not evidence of audited safety.

This research reviewed public documentation and project READMEs. It did not benchmark competitors, inspect their full implementations, measure this machine's disk usage, or establish where the reported 500 GB–1 TB weekly growth actually originates. The local repository currently has no implementation.

## Existing approaches

| Tool | Documented capability | Implication for Janitor |
| --- | --- | --- |
| [Kondo](https://github.com/tbillington/kondo) | Rust utility for cleaning dependencies and build artifacts across 20+ project types | Artifact recognition is established territory; useful comparison set for detector coverage |
| [Bonsai](https://github.com/sauravpanda/bonsai) | Go agent-worktree cleanup; safety classifications, saved plans, revalidation, recovery options | Closest Go starting point to evaluate before building a new cleanup core |
| [Chaff](https://github.com/gulsahsarsilmaz/chaff) | Project attribution, caches, build output, worktrees, process inspection, scheduling, tiered cleanup | Much of the proposed breadth already exists; compare unattended behavior, measurement quality, and integration reliability |
| [Codex-managed worktrees](https://learn.chatgpt.com/docs/environments/git-worktrees) | Native worktree lifecycle and automatic cleanup settings | Cooperate with the owning application's lifecycle rather than independently deleting its folders |
| [Docker BuildKit GC](https://docs.docker.com/build/cache/garbage-collection/) | Ordered cache policies based on age, space budgets, and free space | Configure or invoke the owner's GC instead of deleting its internal storage |

## What needs challenging

“Old” and “unused” are different. An agent can be waiting for input; a service can read files without modifying them; an editor may retain a resumable session. Directory timestamps are not recursive activity logs. A process disappearing does not mean its work is disposable.

Likewise, 400 GB is not inherently excessive for hundreds of live agents. Large native builds, dependencies, datasets, and browser test output can make that a legitimate working set. A cleaner can bound disposable storage; it cannot guarantee a hard disk ceiling if active jobs can write without limit. A hard ceiling requires admission control, concurrency limits, quotas, or stopping jobs. Recommend warnings and optional admission control first, never automatic eviction of active work.

Do not assume the bytes are mostly source checkouts. Git linked worktrees share repository objects, while each checkout can grow independent dependencies and build products. The first investigation should distinguish these from shared caches, container storage, archives, temporary downloads, and unrelated files. [Git worktree documentation](https://git-scm.com/docs/git-worktree)

## Storage and discovery model

Use a bounded initial scan of configured roots, including opted-in hidden agent directories. Discover repositories and their registered worktrees using `git worktree list --porcelain -z`. Group linked worktrees by their common repository. Also surface standalone clones, unregistered directories, and unattributed large subtrees; never quietly omit unknown storage from the totals.

Represent ownership as a graph: volume → repository → workspace → artifact, with shared caches linked separately. An artifact can be used by several workspaces. Count each physical object once where measurable, and do not add a shared cache to every worktree's total.

Each workspace should record a stable identity, root identity, repository identity, owner adapter, branch/HEAD, protection state, observation times, activity evidence, artifact sizes, scan completeness, and the reason for each policy decision. Store this in local SQLite. Bound log retention and compact historical observations so Janitor does not itself become a storage leak.

Use ecosystem markers and explicit rules to identify outputs such as `.next`, `.turbo`, Rust `target`, Swift `.build`, dependencies, and test artifacts. A generic folder named `build` is insufficient evidence. Git-ignored files can include credentials, local databases, downloaded assets, and valuable experiments: ignored never means disposable. Recognized outputs can also contain manual edits; automatic deletion requires an explicit disposable-data policy.

Shared package stores, Docker, and other managed caches should have separate adapters and budgets. Use their supported cleanup interfaces after checking their semantics. Exclude container volumes, databases, model weights, and arbitrary user files from automatic cleanup in the first release.

## Activity tracking

Use two clearly distinguished modes:

**Discovered workspaces:** Combine owner state, process working directories/open files where available, recent observed changes, Git state, pins, and locks. Missing permissions, unsupported owner state, or failed checks mean unknown, not idle. Process scans are evidence and cannot establish exclusive ownership. Initially restrict full retirement to an explicit reviewed action or an owner-supported lifecycle operation.

**Cooperating sessions:** Offer a command such as `janitor run -- claude` that acquires a workspace lease and runs the process tree. Provide acquire/heartbeat/release APIs for integrations. Identify owners with process start identity and boot identity, not PID alone. Account for child processes, suspension, crashes, and reconnects. Lease expiry triggers investigation and a grace period; it does not directly authorize deletion.

Use shared activity leases and an exclusive cleanup lease so a cooperating session cannot begin while cleanup is in progress. Manual launches can bypass this protocol; communicate the resulting limit rather than promising race-free cleanup for arbitrary processes.

Claude Code exposes session and worktree hooks, but `WorktreeCreate` replaces its creation implementation rather than passively observing it. Integrate deliberately; do not install it merely to log an event. Hooks also do not cover arbitrary worktree creation through shell commands. [Claude Code hooks](https://code.claude.com/docs/en/hooks)

For Codex and other app-owned worktrees, prefer supported archive/remove operations. Do not write private app databases or assume an internal schema remains stable. Where a supported external operation is unavailable, report retirement candidates rather than bypass the owner.

## Incremental scanning

Start with scheduled, bounded reconciliation and cache the results. Then add event-driven invalidation of affected subtrees. Watchers accelerate discovery; they are not a complete ledger or proof of inactivity.

On macOS, prefer a dedicated FSEvents adapter for large roots. Apple explicitly requires rescans following dropped events. The current fsnotify backend uses kqueue on macOS, and its documentation notes file-descriptor costs and the lack of automatic recursive watching. Avoid a watch on every dependency file. Linux can use bounded inotify watches plus periodic reconciliation. [Apple FSEvents](https://developer.apple.com/library/archive/documentation/Darwin/Conceptual/FSEvents_ProgGuide/UsingtheFSEventsFramework/UsingtheFSEventsFramework.html), [fsnotify](https://github.com/fsnotify/fsnotify)

Coalesce event bursts, cap concurrent filesystem work, checkpoint scans, and recover after daemon restarts or event loss. A changed subtree becomes dirty; a parent directory's unchanged mtime must not suppress rescanning indefinitely. Network volumes and inaccessible paths should initially be report-only.

## Cleanup policies

| State | Proposed default |
| --- | --- |
| Active lease, running owner, pin, or worktree lock | Protect workspace and its artifacts |
| Uncertain activity, permissions, or scan completeness | Report; do not automatically delete |
| Inactive workspace with explicitly disposable outputs | Permit artifact eviction under opted-in policy; retain source |
| Completed, inactive, recoverable linked worktree | Retire through its owner or coordinated Git operation after revalidation |
| Dirty, untracked, ignored unknown data, or unique commits | Protect from automatic full retirement |
| Primary checkout | Never automatically remove the checkout |

Clean Git status is insufficient to prove recoverability. Check ignored content separately and account for detached HEADs, unpushed history, submodules, nested repositories, stashes, and missing remotes. Preserve any detached commit with a durable recovery reference before removal. Do not automatically delete branches in v1. A squash-merged PR is not established by an ancestry check; a PR adapter must match the actual reviewed head and still inspect later local changes.

`git worktree prune` cleans metadata for worktrees already missing; it does not reclaim existing worktree directories. Use owner lifecycle operations or `git worktree remove` for eligible linked trees, without force in automatic mode. [Git documentation](https://git-scm.com/docs/git-worktree)

Trigger cleanup by both age and disk pressure. Suggested experiment, not a universal default: consider opted-in outputs after 48 hours idle; consider completed worktrees after seven days; start a pressure sweep below 15% free and stop above 20%. Tune these with actual working-set data and allow absolute-byte floors as well. Pressure must never weaken protection rules.

Choose candidates by expected reclaimable bytes, inactivity, and rebuild cost. Record rehydration after eviction to expose cache thrashing. When nothing eligible remains, report that the budget cannot be met safely. An optional managed launcher can defer new jobs; it cannot stop existing jobs from growing without stronger resource enforcement.

## Deletion and recovery

Build a concrete plan with identities, evidence, estimated bytes, and preconditions. Immediately before execution acquire locks, verify root and target identities, repeat activity and data-preservation checks, and abort if the plan no longer applies.

Use descriptor-relative, traversal-resistant filesystem operations. A canonicalized path checked earlier is not adequate against concurrent symlink replacement. Go's `os.Root` APIs offer a useful foundation; review the exact operations and platform guarantees of the selected Go version. These APIs do not solve application-level activity races. [Go filesystem APIs](https://go.dev/blog/osroot)

Never follow deletion targets outside allowed roots, cross unexpected mount boundaries, execute repository-provided cleanup scripts automatically, or run as root by default. Repository configuration may narrow policy; it must not silently broaden machine-owner deletion authorization.

Journal the operation before mutation and make restart recovery idempotent. Keep evidence of partial failures. Source recovery, if added, needs a verified archive and tested restore path that includes relevant untracked and ignored data; a patch alone is not a backup.

Moving a directory into trash on the same volume frees no space. Separate recoverable quarantine from actual reclamation in the UI. Under pressure, delete only approved disposable data; do not promise both full local recovery and immediate space recovery for the same bytes. Archives also consume storage and need their own explicit retention budget.

## Honest space accounting

Report logical size, allocated size where available, estimated reclaimable bytes, and observed free-space change as separate concepts. Deduplicate hard links by volume/file identity and account for links outside the removal set. pnpm documents a hard-linked store layout; removing a workspace does not necessarily free the underlying package bytes. [pnpm layout](https://pnpm.io/symlinked-node-modules-structure)

APFS clones and snapshots further complicate physical reclaim estimates. Do not label ordinary directory totals as guaranteed savings. Apple documents copy-on-write clones and snapshots. [APFS features](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/Features/Features.html)

Measure volume availability before and after cleanup, while acknowledging concurrent writes, open deleted files, snapshots, and delayed reclamation can affect the result. Docker's internal reclaimed bytes and host-volume free space may also differ. Unexplained residual usage belongs in the report rather than being falsely attributed to a worktree.

## Implementation choice

Recommend Go for the first version: straightforward CLI/service development, concurrency, distribution, and maintenance. Rust is also suitable, particularly if the maintainers prefer it; neither language solves incorrect cleanup policy. Scanning throughput should be measured before optimizing around language choice.

Use one executable with CLI and service subcommands, SQLite for local state, TOML for non-secret policy, and the installed Git executable for Git semantics. Keep platform-specific filesystem/activity adapters behind narrow interfaces. Per-user launchd and systemd services cover macOS and Linux; defer Windows destructive operations until junction, locking, and process-observation behavior are verified. Native integrations or SQLite choices may affect cross-compilation; do not promise a zero-dependency static build before choosing those implementations.

No cloud account, LLM, or central server is needed. Run independently on each machine and provide JSON reports suitable for SSH collection. Fleet dashboards can follow real demand.

## Proposed first release sequence

1. **Read-only accounting:** `janitor scan`, `status`, `explain`, and `plan --json`. Attribute known storage and explicitly show unknown bytes, inaccessible roots, stale observations, and uncertainty. Compare against existing tools on the same fixtures.
2. **Controlled artifact cleanup:** explicit policy enrollment; revalidated plans; protected roots; per-volume measurements; operation journal; safe interruption. Prove useful reclamation while retaining unfinished source.
3. **Managed background operation:** leases, session wrapper, owner adapters, disk-pressure policies, bounded scans, service installation, and persistent pins.
4. **Worktree retirement:** owner-coordinated cleanup, verified recoverability, and recovery drills. Add more ecosystem adapters based on measured missing coverage.

Before unattended cleanup ships, exercise real Git fixture repositories containing dirty files, ignored secrets, detached commits, squash merges, submodules, nested repos, unusual filenames, stale remotes, and locked worktrees. Add concurrency fixtures for resumed sessions, child processes, PID reuse, event overflow, symlink swaps, cross-volume paths, and crash recovery. Test actual reclaim behavior on macOS and Linux, including hard links, and test restore for every recovery feature claimed.

The first validation on a real machine should be a read-only storage census followed by an observation period under normal agent workloads. It should answer where the bytes are, which can be removed without losing work, how often reclaimed outputs are rebuilt, and how much CPU/I/O the observer costs. Set quantitative overhead and coverage acceptance thresholds from that baseline rather than inventing performance claims now.
