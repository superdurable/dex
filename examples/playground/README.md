# Examples playground

A single static page that calls every example HTTP controller (products,
patterns, and primitives) without hand-building GET/POST URLs.

Dataset Deal (Go) and AI Agent Email (Python) already have their own UIs and
are not included here.

Start Dex, then one language example server, then this page. Commands and
default ports (HTTP `8080` / Worker `8803`, Python sync `8081` / `8804`, Dex
`8801`, Dex Web `8802`) are in [../README.md](../README.md). Only one language
backend can bind `8080` at a time; Python sync can run alongside on `8081`.

## Start

From this directory:

```bash
./start.sh --port 3333 --backend http://127.0.0.1:8080 --dex-web http://127.0.0.1:8802
```

Or with defaults only:

```bash
./start.sh
```

Defaults:

| Flag / env | Default | Meaning |
|---|---|---|
| `--port` / `PLAYGROUND_PORT` | `3333` | Playground listen port |
| `--backend` / `PLAYGROUND_BACKEND` | `http://127.0.0.1:8080` | Example HTTP controller |
| `--dex-web` / `PLAYGROUND_DEX_WEB` | `http://127.0.0.1:8802` | Dex Web (`dexcli dev`) |

Example, Python sync backend on `8081`:

```bash
./start.sh --port 3333 --backend http://127.0.0.1:8081 --dex-web http://127.0.0.1:8802
```

Then open [http://127.0.0.1:3333](http://127.0.0.1:3333). The page can also
change backend and Dex Web URLs in the header; those values persist in
`localStorage`.

`start.sh --print-config` prints the three URLs and exits. `./smoke.sh` checks
that output.

The playground and the example backend listen on different ports. Each language
example server allows CORS so the browser can call the APIs directly.

## Dex Web links

After a flow ID is known, the page links to Dex Web:

- current run: `{dexWeb}/flows/{flowId}`
- specific run: `{dexWeb}/flows/{flowId}/{runId}`
- search: `{dexWeb}/?q=WorkflowId="{flowId}"`

Those routes come from `web/app/App.tsx`.
