# Contributing

Small, well-tested improvements are welcome. Open an issue before adding a new deletion capability or changing the enrollment contract.

Use Go 1.25+, Git 2.31+, and macOS or Linux. Run:

```sh
gofmt -w cmd internal
go test -race ./...
go vet ./...
go build -o bin/janitor ./cmd/janitor
python3 scripts/smoke.py bin/janitor
```

Tests must create their own temporary repositories and state. Never use real project roots, app-managed worktrees, package stores, or a contributor's home directory as deletion fixtures.

For an artifact detector, include its project marker, false-positive cases, tracked-file protection, nested-repository protection, and a real cleanup test. For lifecycle changes, exercise active and abandoned leases, stale plans, failures, and restart behavior.

Unknown or inaccessible state must remain protected. Preserve source by default. Do not add a force bypass, infer disposability from `.gitignore`, delete branches, execute repository cleanup scripts, or weaken checks under disk pressure. Keep non-secret configuration in the config file.

Document user-visible changes and limitations. Benchmarks need workload size, filesystem, hardware, and cold/warm conditions. Do not label cross-compilation as runtime testing.

By contributing, you agree that your contributions are licensed under the project's MIT license.
