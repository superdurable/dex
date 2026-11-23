---
section: Fixed
---
- Python: `ConnectorToolSource` runs writes of Connector packages built for `dex-connectors-sdk` 0.12 and later. Their fence saves a plain `dict` heartbeat, which failed with "dispatch claim could not be recorded"; a `dict` or `list` heartbeat is now kept as JSON text.
- Python: the `connectors` extra accepts `dex-connectors-sdk` up to 0.14, and every Python package requires `dex-python-sdk==1.45.17`, which `dex-connectors-sdk` 0.14 needs.
