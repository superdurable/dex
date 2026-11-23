---
section: Breaking
---
- `dexcli visualize` generates only Flow Definition Graph 2.0. Remove `--schema-version 1.0` from scripts to avoid a usage error. Add one `dex:group` and one `dex:explanation` per Step. In Go, define `GetDexSummary` and `GetDexDisplay` and register both in `GetRPCs`. Missing metadata produces blocking diagnostics. With `--json`, the command still writes partial JSON and exits with status 1.
