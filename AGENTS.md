# Working on Janitor

Read README.md, docs/architecture.md, and docs/agents.md before changing cleanup behavior.

- Keep non-secret configuration in the config file. Never commit credentials or real machine inventories.
- Challenge unsafe assumptions. Unknown state protects data. Age alone is not proof of abandonment.
- Use isolated temporary Git repositories for tests. Never run destructive tests against real user roots.
- Every deletion change needs tests for protections and failure paths, not only a successful deletion.
- Preserve the enrollment/lease contract. Do not add force deletion, automatic abandoned-lease expiry, branch deletion, or ignored-file cleanup.
- Run gofmt, go test -race ./..., go vet ./..., and a CLI build before committing.
- Explain actual tested platforms and remaining limitations accurately.
- Do not add numbered eyebrow labels to documentation or UI.
