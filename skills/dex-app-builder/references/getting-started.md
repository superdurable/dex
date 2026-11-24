# Getting started with Dex

Use this route when the user wants to learn Dex, install it, set it up, or
understand how it works, and has no concrete business process yet. Keep it
short. Do not start product discovery until the user picks a next step.

## 1. Explain the core model

Explain Dex in three to five sentences. Take the content from the
[Dex SDK mental model](../../dex-sdk/SKILL.md#mental-model), and do not
restate its whole table. Cover these points:

- a Flow is one durable run of a business process, with a stable Flow ID;
- a Flow is a graph of Steps, and each Step has WaitFor and Execute;
- Attributes hold the Flow's durable state;
- RPCs are the typed way to read and change a running Flow from outside;
- Dex Web shows and manages the Runs.

## 2. Install dexcli

Run `dexcli version`. If `dexcli` is missing, ask before you install software
on the user's machine. `dexcli` runs on macOS and Linux.

- With Homebrew, on macOS or Linux: `brew install superdurable/tap/dexcli`.
- Without Homebrew: download the archive for the system from the latest
  `cli-v` release of `superdurable/dex` on GitHub. Put `dexcli` and
  `dex-server-zd` from the archive in the same directory on `PATH`.

Run `dexcli version` again to confirm the install.

## 3. Start Dex locally

Start `dexcli dev --open=false` in the background as a long-lived process. It
runs Dex Server and Dex Web on the user's machine. Give the user the Dex Web
URL that it prints. Keep it running for the rest of the session. The
[CLI reference](https://docs.superdurable.io/reference/cli/#start-locally)
has its flags.

## 4. Ask for the next step

Ask the user one question: start from an example, or describe their own
process.

- **An example.** Pick the language with the rule in
  [workspace bootstrap](workspace-bootstrap.md#choose-the-language). Use that
  language's quick start:
  [Go](https://github.com/superdurable/dex/tree/main/examples/go/quick-start),
  [Python](https://github.com/superdurable/dex/tree/main/examples/python/quick-start),
  [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/quick-start),
  [Java](https://github.com/superdurable/dex/tree/main/examples/java/quick-start), or
  [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/quick-start).
  Run its Worker against the local stack and show the Run in Dex Web.
- **Their own process.** Continue with the App Builder workflow from
  Stage 0.
