# Legacy and third-party notices

The Super Durable Source License 1.0 applies only to material owned or
licensable by Super Durable, Inc. It does not replace the licenses below.

## Legacy cutoff

The complete repository snapshot at commit
`1a61670238cbdc8f2a0a6dd78cc5668fb014f283`, including both parents of that
merge commit, is Legacy Materials. Later copies and adaptations retain the
applicable legacy license for their legacy portions.

At the cutoff, the principal directory licenses were:

| Cutoff path | Legacy license |
| --- | --- |
| `server/` | MIT |
| `sdk-go/` | Apache License 2.0 |
| `sdk-java/` | Apache License 2.0 |
| `sdk-python/` | Apache License 2.0 |
| `protos/` | MIT or Apache License 2.0, at the recipient's option |
| `samples-go/` | MIT |
| `samples-java/` | Apache License 2.0 |
| `samples-python/` | Apache License 2.0 |

The MIT and Apache License 2.0 texts are in `LICENSES/`. Historical copyright
and attribution notices remain authoritative and must be preserved. Git history
identifies the exact material present at the cutoff.

## Excluded directories

This relicensing does not cover `docs/` or `examples/`.

- `examples/go/` remains under its existing MIT License.
- `examples/java/` and `examples/python/` remain under their existing Apache
  License 2.0 terms.
- `docs/` retains its legacy copyright and licensing status. This repository
  makes no new license grant for that directory.

## Existing third-party notices

Some source files retain notices naming Cadence workflow OSS organization,
Uber Technologies, Inc., Indeed, Inc., individual contributors, or other
holders. Those notices and their accompanying licenses remain in force.

Dependencies not copied into this repository remain governed by the license
distributed with each dependency.

## IWF Java SDK integration fixtures

The workflow and integration-test fixtures under these paths are adaptations
or translations of the `indeedeng/iwf-java-sdk` integration suite at commit
`8fa04457c0abcc4473300f17ea0a033d8f93ed88`:

- `sdk-java/src/test/java/io/superdurable/dex/integ/`
- `sdk-python/tests/iwfcompat/`
- `sdk-typescript/test/iwfcompat/`

The upstream portions remain licensed under the Apache License 2.0. Super
Durable modifications are licensed under the Super Durable Source License 1.0.
The Apache License 2.0 text is in `LICENSES/Apache-2.0.txt`.
