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
Server tool. Git history supplies rename and copy lineage from the legacy cutoff,
so CI must fetch the full repository history. The tool's own `main.go` keeps its
`mixed` classification through `reviewed_mixed_paths` in `../policy.json`,
because the repository-wide diff exceeds Git's default rename limit.

Within `reviewed_mixed_override_prefixes` (only `server-zd/integ`), exact paths
in `reviewed_mixed_paths` take precedence over `forced_new_prefixes`.
The 91 reviewed copies under `server-zd/integ` retain legacy MIT material from
`server/integ`; their manifest entries record the source paths and licenses.
Both header writing and verification use this same precedence. Other files
under `server-zd`, including new integration files without legacy lineage,
remain subject to the forced-new policy. The migration tool also excludes these
reviewed paths within the override prefixes from forced-new classification and
follows their copy lineage.
