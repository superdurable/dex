# Product route

Read this file on the product route, before Stage 0. The SDK-only route does
not need it. Then continue with every section of
[SKILL.md](../SKILL.md#mental-model) from Mental model on.

## The workflow

The product route builds the backend: the Flows, their Dex state, the Workers,
and the application API that the application's UI or callers use. It has one
reference per stage. Read only the sections the next decision needs. Record
decisions in the application's own documentation and do not reread a
reference once its facts are recorded.

| Stage | Goal | Reference |
| --- | --- | --- |
| 0. Workspace | Classify the repository, choose or keep the language, install tools | [Workspace bootstrap](workspace-bootstrap.md) |
| 1. Discovery | Actors, roles, permissions, lifecycle, Flow boundaries, storage, external systems; ask once about UI and Connectors | [Product discovery](product-discovery.md) |
| 2. Backend | Flow design, Dex-first state, Workers, the application API contract when a UI or caller needs one | [Backend implementation](backend-implementation.md) |
| 2a. FDG | Start input, Step groups, explanations, Summary, Display, Actions, diagnostics | [FDG authoring](fdg-authoring.md) |
| 3. Verify and hand off | Source gate, local `dexcli dev` stack, real journeys, packaging, handoff | [Verification and handoff](verification-handoff.md) |
| 3'. Managed Dex App | Repository rules, checks, import, and Live for Dex Cloud | [Managed Dex App](managed-dex-app.md) |

The UI and Connectors are optional. Discovery asks the user once about each.
Load these references only for the options the user chose:

| The user wants | Reference |
| --- | --- |
| Their own UI, which most products need | [UI workflow](ui-workflow.md) |
| Dex Web for operations and management, alone or with their own UI | [Dex Web](dex-web-v2.md) |
| A released Connector for an external system | [Connector architecture](connector-architecture.md) |

Before Stage 0 picks a language, ask whether the application runs locally only
or is imported into Dex Cloud as a Managed Dex App. A Managed Dex App starts
from the example repository's shape instead of Stage 0's bootstrap. Build it
to its repository rules, and hand
off with its checks and import steps instead of the local `dexcli dev` steps.
Import and publishing stay the user's steps.

Keep source readiness separate from configured real execution at every stage.
Never report compilation as provider or end-to-end success.

## Product constraints

Product route only; the SDK-only route skips this section. These are
product choices; the Dex facts, Must rules, and patterns in
[SKILL.md](../SKILL.md#facts) apply unchanged.

- **Prefer one backend language, chosen in Stage 0.** Go, Python,
  TypeScript, Java, and Rust are equal choices. Stage 0 keeps an existing
  application's language, uses the language the user names, and otherwise
  uses TypeScript. The rest of the workflow is the same for every language.
  Add a Worker in a second language only for a Flow whose required library
  exists only there.
- **Strict FDG 2.0, with the directives listed for the language.** Use only
  the `dex:` directives and options that the
  [Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization)
  lists for the application's language. Skip a capability it does not list.
  Never invent syntax, and never ask the user to switch languages for it.
  Give each local Step its `dex:group` and `dex:explanation` directives.
- **The UI is the user's choice.** Most products need their own UI. Dex Web
  can serve operations and management, alone or next to it. Never default to
  Dex Web alone, and never ask for a Dex Web gap before building the user's
  UI. When the user also uses Dex Web, leave its Runs, Work Queue, Actions,
  edits, history, and diagnostics to it unless the user asks for them in
  their own UI. Locally, `dexcli dev` always serves Dex Web to inspect Flows.
- **Dex is the default store.** Keep new process state in Attributes and
  AttributeMaps. Use a store outside Dex when another system already owns
  the fact, or Dex's model does not fit an access pattern or scale need;
  name one owner per fact. No speculative database, projection, or cache.
- **Write a short model.** For a non-trivial Flow, write a short model of
  its Steps, state, and failure handling in the application's own
  documentation before coding.
- **Connector connections come from configuration.** With a released
  Connector, the application reads its connections through the project
  configuration loader of its language's Connector SDK, and locally Dex Web
  configures them. An operator or hosting platform supplies the deployed
  configuration. Dex hosts none. Credentials in Flow state follow the
  [secrets facts](../SKILL.md#attributes-loading-and-secrets).
- **The graph reads from source.** Return analyzer-visible decisions from
  `Execute`, use targets the analyzer can resolve, compose Steps instead of
  subclassing them, and fix every `dexcli visualize` error diagnostic before
  handoff.
