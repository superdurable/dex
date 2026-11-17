# Managed Dex App

A **Managed Dex App** is a Dex application that Dex Cloud imports from a
GitHub repository in the Dex app structure. Dex Cloud builds it and runs its
web server and Worker in Live, and in an optional Preview. Dex Cloud's
**Runs** tab opens Dex Web on that deployment for management. The user keeps
the code in GitHub.

The repository rules, manifest fields, limits, and finding codes live in the
[Dex app manifest and repository layout](https://docs.superdurable.io/reference/dex-app/manifest)
reference. Read it for every exact value. This file keeps only the stable
rules.

## When to choose it

| The user wants | Choose |
| --- | --- |
| To build and run the application on their own machine, or to deploy the Worker themselves | Local only: the normal stages and `dexcli dev` |
| Dex Cloud to build and run the application and its Workers from GitHub | Managed Dex App: this file |

Record the answer. A Managed Dex App keeps App Builder discovery, Flow design,
and the language-neutral product constraints: Dex as the store, effects only
in `Execute`, start identity, and credentials outside the code. Implement the
TypeScript shape with the [Dex SDK](../../dex-sdk/SKILL.md) TypeScript
references, and follow the example's `AGENTS.md` for the Flow shapes its check
accepts. Go FDG authoring applies only to the Go profile. Connectors work on
every profile; use the connector packages of the application's language. The
repository shape and the checks come from this file.

## Supported stacks

The import accepts three kinds of application:

| Application | `kind` | `stack` | Build profile |
| --- | --- | --- | --- |
| Go process app | `process` | `go` | `go-react-v1` |
| TypeScript process app | `process` | `typescript` | `node-ts-v1` |
| TypeScript Dex Agents agent app | `agent` | `pi` | `node-ts-v1` |

The import refuses other agent frameworks. There are no Go agent apps. The
[template manifest](https://docs.superdurable.io/reference/dex-app/manifest#template-manifest)
reference keeps the current list.

## Start from the example

Start from the public example repository
`superdurable-apps/cloud-dex-app-example`. It is the `typescript-greeting`
TypeScript process: a page and an HTTP API start a Flow whose Steps write and
save a greeting. Copy its files and layout, then replace the greeting Flow,
API, page, and tests with the first business feature. Do not invent a layout.

- Keep its base files as they are: the entry point, the Dex client and
  Worker wiring, the HTTP server, and the API test harness. They implement the
  runtime contract (S6).
- Follow the example's `AGENTS.md` for where each feature file goes. Keep one
  Flow per source file.
- The reference also documents a Go build profile. Use it only when the user
  asks for a Go Managed Dex App, and take its file list from the reference.

## Repository rules S1 to S8

Each link has the exact fields and limits.

- **S1, template manifest.** The root `.dex/template.json` is a JSON object
  with `"schemaVersion": 1`. It names a build profile, `kind`, and `stack`
  that agree with each other. Its `openapiSpec` is `openapi/openapi.yaml` and
  its `agentInstructions` is `AGENTS.md`, and both files exist. Its
  `commands` declare `bootstrap`, `checkStatic`, and `testApplication`, each
  written as `make <target>`.
  [S1](https://docs.superdurable.io/reference/dex-app/manifest#s1)
- **S2, deployment manifest.** The root `dex-app.yaml` starts with
  `schemaVersion: dex.superdurable.io/dex-app/v1`. It declares
  `application` (port and health path), one `flowDefinitions` entry per Flow
  source file, and `connectors`, which is `[]` when the application uses
  none. Each Flow source is a committed file outside `node_modules`. A field
  the reference does not list is an error.
  [S2](https://docs.superdurable.io/reference/dex-app/manifest#s2)
- **S3, build profile files.** The repository holds every file its build
  profile needs, and each Flow source has a suffix that profile accepts. For
  the TypeScript profile that means `package.json` with a `build` script and
  `main`, and `package-lock.json`. The repository needs no Dockerfile.
  [S3](https://docs.superdurable.io/reference/dex-app/manifest#s3)
- **S4, make targets.** The root `Makefile` itself defines every target the
  template manifest names. A target that only an included file defines
  counts as missing.
  [S4](https://docs.superdurable.io/reference/dex-app/manifest#s4)
- **S5, application tests.** `make test-app` runs the application's API
  tests against a Dex Server that exists only for that run, with the
  connectors' mock packages.
  [S5](https://docs.superdurable.io/reference/dex-app/manifest#s5)
- **S6, runtime contract.** One build runs in two modes chosen by its first
  argument. `web` serves the application and its health path on the port in
  `PORT`. `worker` runs the Worker and answers `GET /readyz` once its slots
  are registered. Both read the runtime settings file Dex Cloud provides,
  write only under `/tmp`, and stop cleanly on SIGTERM; the Worker drains its
  running tasks first. Dex Cloud publishes the Flow definitions, so the
  Worker uploads none.
  [S6](https://docs.superdurable.io/reference/dex-app/manifest#s6)
- **S7, connector mocks stay in tests.** Only test code imports a connector
  mock package.
  [S7](https://docs.superdurable.io/reference/dex-app/manifest#s7)
- **S8, repository shape.** The repository is active, not archived, and is
  the user's own, not one Dex Cloud created for a project. GitHub returns its
  complete tree listing. The default branch is `main` and has a commit.
  The repository holds only regular files, with no submodules or symbolic
  links. It commits no secret file, such as `.env`, a private SSH key, or a
  `*.pem` file. It stays within the file and size limits, and belongs to one
  project at most.
  [S8](https://docs.superdurable.io/reference/dex-app/manifest#s8)

These rules collide with two App Builder defaults:

- S1 and S6 always need a web server, `openapi/openapi.yaml`, and
  `AGENTS.md`. They are target requirements, not a custom UI decision. Dex Web
  still owns management.
- A Managed Dex App backend follows the example's stack, so it is TypeScript
  unless the user asked for Go. The Go-only rule applies to applications that
  run locally only.

## Strict YAML in dex-app.yaml

`dex-app.yaml` is strict YAML that YAML 1.1 and YAML 1.2 read the same way.

- Write one document with a mapping root and string keys, each key once.
- Use no anchors, aliases, merge keys, or tags.
- Quote every value that must be a string but could read as something else:
  release numbers, empty strings, `yes`, `no`, `on`, `off`, numbers with a
  leading zero, dates, and times.

```yaml
authMethodId: ""
enum: ["yes", "no"]
version: "<release>"
```

## Check before handoff

Use the example repository's own `Makefile` and README targets. Do not invent
others. They need Node.js, Go, Python 3, `dexcli`, and `dex-server-zd`.

1. `make bootstrap` restores npm dependencies and the Go module of the
   manifest decoder in `tools/appmanifest`.
2. `make check-static` renders and checks every Flow as strict FDG 2.0,
   type-checks, and builds. It reads `dex-app.yaml` with the
   `tools/appmanifest` decoder, exactly as the import does.
3. `make test-app` runs the API tests on a real Dex engine. Locally, set
   `DEX_SERVER_ZD_BINARY` to a `dex-server-zd` binary; the tests fail
   without it.

Report each result separately. A passing `check-static` is source readiness,
not a passing import or a running Live deployment.

## Import, Preview, and Live

Import and **Publish to Live** are the user's steps in Dex Cloud: a Teamspace
member imports, and a builder or owner of the project publishes. Prepare the
repository, push it to GitHub, and hand off these steps.

1. A Teamspace admin who is an owner of the GitHub organization, or the
   personal account itself, connects it under
   **Connections** by installing the **SuperDurable Dex Cloud** GitHub App with
   access to the repository and choosing **Connect GitHub**.
2. A Teamspace member chooses **Import project**, picks the repository or
   types `owner/name`, and chooses **Check repository**.
3. The check reads `main` and lists every rule the repository breaks. When it
   passes, the member chooses **Create project**. The import leaves the
   repository unchanged.
4. Complete **Setup** (connections and environment values). For Live, choose
   **Publish**, pick a version (a commit of `main`), and
   choose **Publish to Live**.
5. Preview is optional. It runs a version quickly on data separate from Live.
   Skip it when the user does not need to try a change first.

## Read a refused import

Each finding names where it is, what is wrong, and how to fix it, and links
its rule. Its finding code is an anchor on the manifest reference page, such
as `#flow_source_missing`. Fix the repository, push to `main`, and check
again. When the check cannot read the repository, give the SuperDurable Dex
Cloud GitHub App access to it on GitHub.
