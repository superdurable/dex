# Agent starter (Python)

A durable OpenAI Agents SDK agent in one file, [`agent.py`](agent.py). It
imports only published packages, so you can copy it into an empty folder.

The file has three parts: the plain agent with two tools, the Dex wrap with a
policy for each tool, and a host that runs the Worker and the Client in this
process. `look_up_order` is a read and runs without approval. `cancel_order` is
a write, so it waits for your approval on the terminal and runs at most once.

## Run it

Start Dex in one terminal:

```bash
dexcli dev
```

In a second terminal, from this folder (or an empty folder with `agent.py`
copied in):

```bash
uv sync --locked            # or: pip install dex-agents-openai-agents==0.5.0 dex-python-sdk==1.45.0
export OPENAI_API_KEY=...
uv run --frozen python agent.py "Cancel order 1002, it was a mistake"
```

Set `AGENT_MODEL` to choose the model, and `DEX_FLOW_SERVICE_ADDRESS` when Dex
is not at `127.0.0.1:8801`.

## Test it

The tests replace the model with a keyless scripted one:

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 uv run --frozen pytest
```
