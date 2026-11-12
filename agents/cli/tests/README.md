# Dex Agents CLI tests

The checks of the CLI configuration schemas and of the terminal UI contract.
They need no Dex server.

| File | What it checks |
| --- | --- |
| `test_cli_config.py` | The configuration schemas, the examples, and `../../conformance/cli_config.json`, against the test-only resolver in `cli_config_reference.py` |
| `test_terminal_ui_contract.py` | The terminal UI schemas, `../../conformance/terminal_ui/`, and the contract host |
| `terminal_ui_contract_host.py` | A test-only peer that plays either side of a terminal UI transcript; see [`../TERMINAL_UI_CONTRACT.md`](../TERMINAL_UI_CONTRACT.md) |

Run them with `./ci.sh`, which uses uv and Python 3.11.

`terminal_ui_contract_host.py` needs Python 3.11 or later and only the
standard library. Other test suites (Go, TypeScript, and the Python CLI core)
run it with any such interpreter. Pass the interpreter's absolute path,
because a launcher gives the terminal UI no `PATH`.
