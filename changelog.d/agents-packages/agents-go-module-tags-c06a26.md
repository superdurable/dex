---
section: Added
---
- The Go modules get release tags. Each agents release now tags every Go
  module under `agents/go/` at the release version, on the same commit as
  `agents/v<version>`: the core is `agents/go/core/v<version>`. So
  `go get github.com/superdurable/dex/agents/go/core@v<version>` resolves a
  release instead of a pseudo-version of `main`. A Go module that requires
  another agents Go module requires it at that same version.
  `stamp_agents_release_version.py --go` stamps those requires, and the new
  release check `script/release/check_agents_go_modules.py` fails a Go module
  that requires a pseudo-version of a Dex module, carries a `replace` or
  `exclude` that a consumer's build would skip, or requires another agents Go
  module at a different version. Agents CI and **Release changed components**
  run the check, and the coordinated release pushes the Go tags. The Go core
  now ships the same `LICENSE` file as the other agents packages.
