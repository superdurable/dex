
# dex-python-sdk

Python SDK for [Dex workflow engine](https://github.com/superdurable/dex)

```
pip install dex-python-sdk==0.0.1
```

See [samples](../examples/python) for use case examples.

## Requirements

- Python 3.9+
- [Dex server](https://github.com/superdurable/dex#how-to-use)

## Concepts

To implement a workflow, the two most core interfaces are

* [Workflow interface](https://github.com/superdurable/dex/blob/main/sdk-python/dex/workflow.py)
  defines the workflow definition

* [WorkflowState interface](https://github.com/superdurable/dex/blob/main/sdk-python/dex/workflow_state.py)
  defines the workflow states for workflow definitions

A workflow can contain any number of WorkflowStates.

See more in https://github.com/superdurable/dex#what-is-dex


# Development Plan

## 1.0 -- the basic and most frequently needed features
- [x] Start workflow API
- [x] Executing `wait_until`/`execute` APIs and completing workflow
- [x] Parallel execution of multiple states
- [x] GetWorkflowResultsWithWait API
- [x] StateOption: WaitUntil(optional)/Execute API timeout and retry policy
- [x] Get workflow with wait API
- [x] Timer command
- [x] AnyCommandCompleted and AllCommandCompleted waitingType
- [x] InternalChannel command
- [x] DataAttribute
- [x] Stop workflow API
- [x] Improve workflow uncompleted error return(canceled, failed, timeout, terminated)
- [x] Support execute API failure policy
- [x] Support workflow RPC
- [x] Signal command
- [x] Reset workflow API
- [x] Skip timer API for testing/operation

### Running dex-server locally

#### Option 1: use docker compose
See [dex README](https://github.com/superdurable/dex#using-docker-image--docker-compose)

#### Option 2: VSCode Dev Container

Dev Container is an easy way to get dex-server running locally. Follow these steps to launch a dev container:
- Install Docker, VSCode, and [VSCode Dev Container plugin](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers).
- Open the project in VSCode.
    ```bash
    cd dex-python-sdk
    code .
    ```
- Launch the Remote-Containers: Reopen in Container command from Command Palette (Ctrl + Shift + P). You can also click in the bottom left corner to access the remote container menu.
- Once the dev container starts, dex-server will be listening on port 8801.

## How To Contribute

This project uses [Poetry](https://python-poetry.org/) as a dependency manager. Check out Poetry's [documentation on how to install it](https://python-poetry.org/docs/#installing-with-the-official-installer) on your system before proceeding.

> ❗Note: If you use Conda or Pyenv as your environment / package manager, avoid dependency conflicts by doing the following first:
1. Before installing Poetry, create and activate a new Conda env (e.g. conda create -n langchain python=3.9)
2. Install Poetry (see above)
3. Tell Poetry to use the virtualenv python environment (poetry config virtualenvs.prefer-active-python true)
4. Continue with the following steps.

To install requirements:

```bash
poetry install
```

#### Update IDL

Edit [`protos/dex.proto`](../protos/dex.proto). Rename catalog: [`docs/design/idl-renames.md`](../docs/design/idl-renames.md).

#### Generate stubs from IDL

```bash
make -C ../protos proto
```

Checked-in Python stubs land in `dex/dexpb/`.
#### Linting

To run linting for this project:

```bash
poetry run pre-commit run --show-diff-on-failure --color=always --all-files
```

## Code of Conduct
This project is governed by the [Contributor Covenant v 1.4.1](CODE_OF_CONDUCT.md). (Review the Code of Conduct and remove this sentence before publishing your project.)

## Publishing to PyPI

1. Bump `version` in `pyproject.toml` (and the `pip install` line above).
2. Create a GitHub Release with tag `sdk-python-vX.Y.Z` (for example `sdk-python-v0.0.1`), or run the **Publish Python SDK to PyPI** workflow manually.
3. CI runs `poetry publish --build` using the `PYPI_TOKEN` repository secret.

See [CONTRIBUTING.md](../CONTRIBUTING.md#releases-monorepo-tags) for monorepo tag conventions.

## License
This project uses the [Apache 2.0](LICENSE) license. (Update this and the LICENSE file if your project uses a different license.)
