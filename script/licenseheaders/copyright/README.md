# License header tool

Adds and verifies repository license headers using the policy, templates, and
legacy manifest in the parent directory. This standalone Go module does not
depend on the Dex Server.

From the repository root:

```bash
make copyright
make copyright-check
make copyright-test
```

To process selected files, pass repository-relative paths from this directory:

```bash
GOWORK=off go run . -rootDir ../../.. -filePaths path/to/file.go
GOWORK=off go run . -rootDir ../../.. -verifyOnly
```

The implementation and license classifications are preserved from the original
Server tool. Git history supplies rename and copy lineage from the legacy cutoff;
CI must fetch the full repository history.
