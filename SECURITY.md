# Security

Janitor deletes explicitly designated local build artifacts. Data preservation bugs, path escapes, protection bypasses, and unsafe plan application are security-relevant.

Please report sensitive findings through [GitHub private vulnerability reporting](https://github.com/jstEagle/Janitor/security/advisories/new). If that channel is unavailable, open a minimal issue asking for a private contact without including exploit details or private paths.

Include the version, OS/filesystem, a minimal temporary fixture, expected protection, actual behavior, and whether deletion occurred. Never include credentials, source archives, or real machine inventories.

The initial 0.x series may change its CLI and state schema; unsupported schema versions fail closed. Use current releases and keep backups of important work.

Janitor is a cooperative per-user utility, not a security boundary against malicious processes with the same user permissions. Do not run it as root or on network filesystems. Artifact deletion has no undo. Review README.md for the exact safety and lifecycle contract.
