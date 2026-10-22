# Changelog

## Unreleased

- Preserve workspaces referenced by direct symlinks from other discovered workspaces, and reject saved cleanup plans when a new inbound reference appears.

## 0.1.0

Initial public release for macOS and Linux.

- Workspace and linked-worktree discovery, artifact attribution, shared Git storage, and large unattributed directory reporting.
- Persistent idle observations, explicit artifact enrollment, pins, and coordinated command leases.
- Saved plans with expiration and fresh precondition checks; permanent artifact eviction and conservative worktree retirement without branch deletion.
- Pressure-driven background sweeps, launchd/systemd user-service integration, and operation journals.
- JSON output, agent setup guidance, isolated Git fixtures, CLI smoke tests, and cross-platform CI.

Automatic mode is off by default. This release uses periodic scans and cooperative leases; it does not include filesystem watchers, native agent-app lifecycle adapters, source archives, global cache pruning, or hard quotas. See README.md for the complete operating contract.
