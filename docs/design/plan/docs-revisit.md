# Docs Revisit: Structure, Reader Paths, and the Writing Guide

Status: design only. No docs page, sidebar, or check changes yet. The
[Rollout](#rollout) lists the pull requests that build it.

Builds on: the 2026-11 readability pass (#1513, #1524, #1530, #1533, #1543).
That pass fixed sentences and gave each fact one home. This plan changes what
that pass left alone: the order of sections, the shape of each page type, the
paths that different readers take, and what coding agents get from the docs.

## Problem

The docs serve several readers from one tree. A developer new to Durable
Execution, a developer who knows another durable execution engine, a
developer who already has an agent built with a framework, and a coding agent
that builds for a developer all start
from the same 11 sidebar groups. Each needs a different first page.

Several pages mix their summary with their details. The reader has to read
edge cases before they reach the part they came for.

Measured on `main` at the start of this plan:

| Finding | Evidence |
| --- | --- |
| 11 top-level sidebar groups for several audiences | Agent Skills sits above Concepts. Correctness and Expressiveness has the same weight as Concepts. |
| The Agent Frameworks overview is 3,179 words | Crash matrices, outcome tables, and the reconcile steps come before "Choose a harness". #1533 moved the shared framework text here, which removed the repetition but made the overview the longest page in the section. |
| Each of the 8 framework pages repeats a Connect Connectors section of about 150 lines | No framework page shows what changes in the reader's existing agent code. |
| The Quick Start has about 1,200 lines of code across five languages | No step shows a failure and the recovery. No step is for a coding agent. |
| Readers from neighbouring tools get four sentences | "How this differs from traditional Durable Execution" at the end of Why Dex. Compared to Database is the only comparison page. |
| Agent-facing output has gaps | `llms.txt` descriptions stop mid-sentence, because the exporter takes the first source line of a wrapped paragraph. No `llms-full.txt`. Each `.md` page holds all five languages. |
| Bold carries every identifier | The no-inline-backticks rule puts API names in bold. Some agent pages have bold on nearly every line, so bold no longer marks emphasis. |
| 8 pages outside the sidebar | `production/*` and `references/*` hold only "This page moved" links. |

## Practices this plan adopts

- **A failure in the first run.** The Quick Start breaks something on purpose
  and shows the recovery.
- **A route for coding agents.** A copyable prompt written as a small spec: a
  source-of-truth URL, steps, and rules such as "Do not invent APIs". Dex Skills
  is the setup step.
- **Topic labels.** The home page names topics. It does not address readers by
  persona.
- **One full sidebar with shortcuts.** Every page shows the same full sidebar,
  with the current section open. Navbar shortcuts jump into it.
- **Short framework pages.** What is shared lives once, under a framework tab.
  The Markdown export writes one file per framework, so a page an agent
  fetches stays small.
- **Voice by sentence type.** Dex pages use "you" or "your" 0 to 12 times per
  1,000 words. That is lower than is typical for developer docs. The fix is not
  "you" as the default subject. The thing being defined is the subject of a
  definition, Dex is the subject of a behaviour, and an instruction is an
  imperative.
- **Nothing hidden from agents.** Every page has Markdown, and tab content
  stays in the Markdown version.
- **Fair comparisons.** Open with what the products share, then a term map and
  the same code side by side. Say when the other product is the better fit.

## Principles

1. **Keep the 2026-11 standard.** The two-question sentence test, the delete
   list, a real actor in every sentence, one home for each fact, and "flag a
   technical doubt, do not fix it quietly" all stay.
2. **Name the thing, not the reader.** Labels name topics, such as "Compared to
   Database". They never put words in the reader's mouth, such as "I'd use a
   database". The one exception is a short newcomer cue.
3. **The summary first, the edge cases later or elsewhere.** A page answers
   what, when, and how in its first screen. Failure matrices, option lists,
   defaults, limits, and version notes go to the versioned Developer reference.
4. **Every page works alone.** A coding agent fetches one URL. That page names
   its prerequisites with a one-line definition and a link.
5. **Show durability instead of describing it.** The Quick Start breaks
   something and shows Dex recovering.
6. **Regrouping is cheap and splitting is not.** URLs come from slugs, so a
   sidebar change moves no URL. Only a split creates new URLs, and each split
   needs redirects or link rewrites in both locales.

## Decisions

| ID | Decision | Rejected alternatives |
| --- | --- | --- |
| N | One full sidebar on every page, plus navbar shortcuts: Docs, Quick Start, Examples, Agents. | Separate tabs with their own sidebars. A reorder only. |
| H | Home page as a wayfinder with an agent prompt strip at the top. See [Home page](#home-page). | Persona doors ("coming from X"). A language-by-goal grid. One golden path. |
| D3 | Quick Start with two doors, a copyable agent prompt and the steps by hand, around a smaller example with a step that stops the Worker. | Keep order processing and add a crash step. A scaffold command first. |
| D4 | A fixed skeleton per page type. Edge cases move to the Developer reference. | An "In short" box with details folded on the same page. |
| D5 | Short framework pages, and one framework tab across the shared agent pages. | One complete page per framework. |
| D6 | One comparison page for existing durable execution tools. The home page names no other product. | One page per neighbouring product. |
| D7 | One interactive crash timeline, reused on the home page, Why Dex, the Quick Start, and How sessions work. | Mermaid sequence diagrams only. |
| D8 | Code font for identifiers in prose. Bold only for a term's first definition and for UI labels. | Bold for identifiers, as today. |
| D9 | Ship the static agent-facing pieces now. A docs MCP server later. | A docs MCP server in this round. |
| W1 | A fixed form for each sentence type. See [Voice](#voice). | Impersonal throughout. "You" as the default subject. |
| W2 | One clause per sentence. Order shows cause and effect. | Join cause and effect in one sentence. |
| W3 | Enforce the guide with `docs-prose-check` lints and a filled-in checklist in each rewrite PR, plus reading every page in a browser. | The guide and human review only. |
| R1 | Build with AI stays its own top-level group, right after Get Started. | Build with AI inside Get Started. |
| R2 | Design Patterns and Examples stay separate groups. | One Examples group holding products and patterns. |
| R3 | Durable Agents comes right after Build with AI, before Concepts. | Durable Agents after Concepts. |
| R4 | The AI Agent example moves into Durable Agents. Examples keeps a link to it. | It stays only in Examples. |
| R5 | Components becomes Run Dex, with two task pages: Run locally and Add Dex to your app. | Keep the label Components. |
| T | The current Quick Start does not become a tutorial. Its six-step structure stays, around a new, smaller example. The order-processing code moves to an Examples page. Its integration rules move to Add Dex to your app. | A tutorial page in Get Started. Fold it into Why Dex. Delete it. |
| — | Why Dex keeps its opening as written, including "platform". | — |

## Information architecture

### Sidebar

```text
Home
Get Started          Why Dex · Quick Start · Compared to Database
                     Compared to existing durable execution        (new)
Build with AI        Dex Skills · Dex App Builder · Dex SDK · Dex Connector Contributor
Durable Agents       Overview · Agent Quick Start (new) · How sessions work (new)
                     Frameworks ▸ (8, plus Any other framework, new)
                     AI Agent example
Concepts             Flow · Step · Primitives ▸ · Client · Connector
Design Patterns      Flow Control ▸ · Reliability ▸ · Messaging ▸ · Data ▸
Examples             Order processing (new) · 8 product examples · link to AI Agent
Run Dex              Overview · Run locally (new) · Add Dex to your app (new)
                     Application · Server · CLI · Web
Developer Reference ▸
Correctness and Expressiveness ▸
Glossary
```

- **Get Started** takes the Intro and Quick Start group and the new comparison
  page.
- **Durable Agents** replaces the Agent Frameworks label, because the built-in
  agent loop is not a framework. The `/agent-frameworks/...` URLs stay.
- **Design Patterns** uses the four groups of its overview page: Flow Control,
  Reliability, Messaging, and Data. Today the sidebar shows 13 items at one
  level.
- **Examples** replaces the Product Examples label. The `/use-cases/...` URLs
  stay.
- **Run Dex** replaces Components. The `/components/...` URLs stay.
- **Correctness and Expressiveness** stays a top-level group, as #1530 decided.
  It moves below the Developer Reference. Why Dex and the Guarantees sections
  link to it.
- The internal section keys in `docsSections.tsx` stay. Section colours and
  icons do not change.

### Navbar shortcuts

The navbar is the company header that the docs share with other company sites.
The four docs shortcuts (Docs, Quick Start, Examples, Agents) need a place that
does not crowd it. The proposal is a docs-only second row under the header,
which collapses into the mobile menu at phone width. Each shortcut opens its
page and its sidebar section. This needs a visual review before it is built.

### Home page

The home page orients the reader. It does not ask who they are. In order:

1. Two sentences on what Dex does.
2. A copyable agent prompt for the Quick Start.
3. The crash timeline (D7), compact.
4. **New to Dex**: Quick Start, Build with AI, Why Dex, Concepts.
5. **What you can build**: Durable agents, Business processes, Timers and
   schedules, Service orchestration.
6. **Compared to**: A database and queues, and Existing durable execution. The
   home page names no other product.
7. **Run Dex**: Run locally, Deploy Dex Server, Dex Web.
8. **Reference**: one link for each SDK, Agents, Server, CLI, and Web.

### Splits and new pages

| Page today | Becomes |
| --- | --- |
| Quick Start (3,183 words) | A new Quick Start at `/quick-start`. The order-processing code moves to an Examples page, Order processing. The integration rules move to Add Dex to your app. |
| Agent Frameworks overview (3,179 words) | An overview of about 400 words at `/agent-frameworks`. How sessions work, about 1,200 words, covers tool policies, approvals, outcomes, and Connectors as tools. The crash and outcome matrices, the write-order rules, and the reconcile steps move to the existing Agents reference pages for failures and Connector tools. |
| 8 framework pages (1,400 to 2,600 words) | Short pages of about 500 words: what the adapter does, install, how a turn maps to Steps, how it differs, and limits. The example code, Worker, routes, run steps, Dex Web approval, and Connect Connectors wiring move into the Agent Quick Start, under framework tabs. |
| Built-in agent loop (3,340 words) | A short page that keeps one more section, Features only the built-in loop has: durable context, steering, questions, plans, MCP tools, live events, and session access. |
| New | Compared to existing durable execution. Any other framework. Run locally. Add Dex to your app. Order processing. Agent Quick Start. |
| `production/*`, `references/*` stubs | Entries in `redirects.json`. The frozen reference snapshots link to these stubs, so the links in those snapshots are rewritten with `snapshot-reference.mjs --rewrite-links` before the stubs are deleted. |

Moved anchors on the Agent Frameworks overview, such as `#session-routes` and
`#connector-tools-crashes`, are rewritten in docs and in the reference
snapshots. Skills link only to the Developer reference, so they need no
change for these anchors. External links to a moved anchor land at the top of
the new page.

Renaming Components to Run Dex changes no URL, but three rule files name the
Components pages: `CLAUDE.md`, `AGENTS.md`, and `.cursor/rules/`. They change
together in the PR that renames the group. `skills/doc-map.yaml` changes in the
same PR as each new Run Dex task page, so that every stable page keeps its
skills counterpart.

## Quick Start

The Quick Start keeps today's six steps with a smaller example:

1. **Install Dex.** Unchanged.
2. **Flow and Steps.** Charge, then a Timer wait, then Ship, with a mock
   payment and shipping API. About 40 lines for each language. The code lives in
   a new `examples/<language>/quick-start` for all five SDKs, with an
   integration test that skips the Timer.
3. **Worker and Client.** The same structure, with less code.
4. **Start a Flow** from one `start` handler.
5. **Stop the Worker** during the Timer wait. Restart it. The Flow completes,
   and Charge does not run again.
6. **Dex Web.** The Execution graph and the Timeline, taken again for the new
   example.

A copyable agent prompt sits above step 1:

```text
Build the Dex Quick Start app in this folder.

1. Read https://docs.superdurable.io/quick-start.md and treat it as the source
   of truth for package names, commands, and code.
2. Install Dex Skills.
3. Ask which language I want: Python, Go, Java, TypeScript, or Rust.
4. Start dexcli dev, run the Worker, and start one Flow.
5. Stop the Worker while the Flow waits on its Timer, restart it, and show me
   that the Flow completed.

Rules: stay inside this Quick Start. Do not invent APIs. Ask me before
installing anything else.
```

**Order processing** becomes a product example page. It holds the full code:
the approve and describe handlers, the Worker and Client, and the run and
`curl` steps. Why Dex and the Examples overview link to it.

**Add Dex to your app** is a short rules page in Run Dex. It covers passing
services through the Flow constructor, never through a setter or a global.
It also says where the Worker and Client live in a service, and how to start a
Flow from a handler and wait for a Step. It links to Order processing for the
full code.

## Durable Agents

- The overview says what a durable session gives the reader in a few
  sentences, then offers the framework choice table and the example.
- How sessions work is the single home for the stable rules: tool policies and
  effect classes, approvals, tool call outcomes, and Connectors as tools.
- The Agent Quick Start and the shared task pages carry a framework tab. The tab
  remembers the reader's choice, the same way the SDK tabs remember a language.
  At phone width, the tab bar becomes a dropdown.
- The Markdown export writes one file per framework, such as
  `agent-quick-start.langgraph.md`. An agent then fetches one framework, not
  eight.
- Each framework page keeps "How a turn maps to Steps" with a diagram, because
  the Claude Agent SDK and LangGraph map turns onto Steps differently from the
  others.
- Any other framework is a checklist for adapting a framework that has no
  adapter.

## Comparison page

Compared to existing durable execution has one section for each family of
existing tools: replay-based workflow engines, journal-based durable function
runtimes, database-backed workflow libraries, event-driven step function
services, and agent framework checkpoints. Each section has:

1. What the tools share with Dex.
2. A term map, such as a workflow engine's activity to a Step's `Execute`.
3. The same small process in both.
4. What is different.
5. When the other tool is the better fit.

The page shows a "last checked" date. An engineer checks each term map against
the current Dex semantics before it ships.

## Visuals

| Job | Visual |
| --- | --- |
| The shape of a Flow | The Flow Definition Graph from the Dex Web Design view, as today. |
| How a concept works | The concept diagrams, as redesigned in 2026-11. |
| What a crash does | A new step-through crash timeline. Its `markdownDescription` tells the same story as numbered text. |
| How an agent turn maps to Steps | A sequence diagram on each framework page. |
| Where to find something in Dex Web | Screenshots, checked against the current Dex Web when a page changes. |

The 2026-11 visual direction stays: few borders, one or two accent colours,
icons where they help, both themes, and no sideways scroll at 390 px.

## Agent-facing docs

| Piece | Change |
| --- | --- |
| `llms.txt` | Each entry uses the page's frontmatter `description`, a complete sentence. The index is split into Docs, Agents, and Reference sections. |
| `llms-full.txt` | Added, one file per section. |
| `.md` per page | Adds one file per SDK language, such as `timer.python.md`. The current file with all languages stays. |
| `Accept: text/markdown` | Requests with this header get the Markdown version where hosting allows it. |
| Page menu | Copy page, Open in Claude, Open in ChatGPT, and Add the MCP server once it exists. |
| Agent prompts | Prompt blocks on the Quick Start, the Agent Quick Start, and each framework page. |
| `AGENTS.md` snippet | Build with AI offers a recommended snippet for a user's own Dex project. |
| Docs MCP server | Deferred. |

Page text never addresses instructions to agents. Instructions for agents go in
`llms.txt` or in a prompt block that the reader copies.

## Writing guide

This guide extends `.cursor/rules/docs-writing.mdc` and the 2026-11 standard.
When it is approved, the first build PR copies it into that rule file and adds
pointers from `AGENTS.md` and `CLAUDE.md`.

### Reading speeds

Every page serves three speeds: a skim of the title, the opening, and the
headings; the task section; and a lookup of details.

- **The opening stands alone.** At most three sentences. The first says what
  the thing is or what the reader has at the end. The second says when to use
  it or why it matters. The opening becomes the frontmatter `description`, the
  `llms.txt` entry, and the search snippet, so the first sentence must read as
  complete on its own. Never "This page explains".
- **The headings make an outline.** Read only the table of contents. If a
  heading does not say what its section is for, rename it. Task sections use
  an imperative verb, such as "Skip a Timer". Concept sections use a noun, such
  as "Guarantees". No "Overview", "Introduction", "Details", or "More". At most
  six words.
- **The common case first.** Order sections as what it is, using it, edge
  cases, and limits. Failure matrices, option lists, defaults, limits, and
  version notes go to the Developer reference. The page keeps one sentence and
  a link.
- **The first screen.** At 390 px, the first screen says what the page is about
  and where to go next.

### Page skeletons

| Page type | Sections, in order | Details go to |
| --- | --- | --- |
| Concept | Opening, When to use it, Guarantees, The smallest example, Common tasks, Reference links | Developer reference |
| Design pattern | Use it when (one sentence and one scenario), Flow graph, How it works, Code, Rules and pitfalls, Related | Concept pages |
| Product example | Goal, Requirements in product terms only, Flow design (graph, and which requirement maps to which Dex feature), Implementation, Run it | Design patterns |
| Framework page | What the adapter does, Install, How a turn maps to Steps, How this framework differs, Limits, link to its Agent Quick Start tab | How sessions work, Agents reference |
| Task page with framework tabs | Goal, Steps with framework tabs, Check it worked | Framework pages |
| How-to and operate | Goal, Before you start, Numbered steps with expected output, Check it worked, Troubleshooting with symptom headings | Server, CLI, and Web reference |
| Comparison | What they share, then for each product: term map, the same example, differences, when the other fits; Last checked date | Concepts |

### Voice

Each sentence type has its own form:

| Sentence type | Form | Example |
| --- | --- | --- |
| Definition | The thing is the subject. | A Timer is a durable wait until a point in time. |
| Instruction | An imperative. "Your" marks the reader's code. | To run a waiting Step early, skip its Timer. Call `SkipTimer` from your Client. |
| Behaviour | Dex or a component is the subject. | Dex treats a skipped Timer as fired. |
| Condition or outcome | "You" is allowed. | If the Worker stops, you see the Step as Waiting in Dex Web. |

Use the present tense and the active voice. Write "Dex retries the Step", not
"The Step will be retried". Do not write "can" for something that always
happens, and do not write "A Client can skip a Timer" for an instruction.

### Sentences

- **One clause per sentence.** One fact in each sentence. Show cause and effect
  by order: the cause first, the effect in the next sentence. A sentence may
  start with "So" or "Then" to make the link explicit. A sentence that stands
  alone carries emphasis, such as "It does not start over or stop halfway."
- **Condition first.** "If the ship retries run out, the Flow moves to
  RefundStep."
- **Concrete.** Name the Step, the number, and the default. Say what to do
  before what not to do.
- **Define on first use, or link.** The Glossary is the home of every term.
- **Requirements in product terms.** Requirement lists never name Dex
  features. The Flow design section maps each requirement to Dex.

### Words

| Use | Not | Note |
| --- | --- | --- |
| Flow run | Flow execution, workflow instance | "Step execution" is a separate term and stays. |
| Dex Server on first use, then the server | the backend, the engine | `server-zd` only for the binary and in the reference. |
| Flow, Step, Attribute, Channel, RPC, Stream, Timer, SubFlow, Worker, Client, Connector | lower case or synonyms | Always capitalised. |
| runs at most once, retries | exactly once without a condition | State the guarantee and its condition. |
| — | simply, just, easily, seamless, powerful, robust, leverage, note that | Adds to the 2026-11 delete list. |

### Formatting

- **Code font** for API names, types, fields, Step and RPC names, files,
  flags, and commands.
- **Bold** only for a term's first definition on a page and for UI labels, as
  in "Select **Approve**".
- **A table** when three or more items share two or more attributes. At most
  four columns, for phones. **A list** when the items are parallel, numbered
  only when order matters. **A paragraph** for reasoning.
- **Callouts.** `warning` only for data loss, a double effect, or a security
  risk. `note` for an exception the reader must not miss. Never a required step
  in a `tip`. At most one callout per screen.

### Code and commands

- **A lead-in sentence before each snippet** says what to notice: "The policy
  marks `cancel_order` as a non-idempotent write. It runs at most once."
- Show the smallest excerpt. Fold the setup code. Link the full file.
- Show "What changes in your code" as a diff.
- Put commands in a terminal block. Put their expected output in a separate
  `text` block.
- Write placeholders in `UPPER_SNAKE` and explain each one once.
- Do not retell code line by line in prose.

### Diagrams and agent-readability

- Draw a diagram when the point is a relationship or a sequence in time. Its
  `markdownDescription` is its text version. The prose says what to look at.
- No fact exists only in an image, a collapsed section, or a tab that the
  Markdown export drops.
- Prompt blocks have four parts: the goal, the source URL, the steps, and the
  rules.

### Chinese

Write the Chinese page from the same outline, in natural 简体中文. Do not
translate sentence by sentence. Dex terms and code names stay in English, such
as Flow, Step, and Worker. Use full-width punctuation. Keep every anchor the
same as the English page. The voice forms carry over: 祈使句 for instructions,
Dex as the subject of behaviour.

### Before and after

Requirements mixed with implementation, from Job posting:

```text
Before (requirement 4)
Accept a replacement title, description, and notes through an update RPC.
The RPC declares a lock on UpdatePostingLock, an Attribute that holds no value
and exists only for this lock. Dex then runs one update at a time, so only one
update at a time takes a version and publishes.

After
## Requirements
4. Accept edits to the title, description, and notes. Apply edits one at a
   time. Each edit gets its own version number.

## Flow design
The `update` RPC declares a lock on `UpdatePostingLock`. This Attribute holds
no value and exists only for the lock. Dex runs one `update` call at a time.
Each call takes the next version before it publishes.
```

A paragraph of rules, from Polling:

```text
Before
Use one maximum-wait value for both ExecuteMethodTimeout and TotalDuration.
Dex enforces them, so the Step does not compute a deadline.

After
Pick one maximum wait. Set both `ExecuteMethodTimeout` and `TotalDuration` to
it. Dex enforces both limits. So your Step does not track a deadline.
```

A mechanism, from the LangGraph page: the turn sequence (`RunModel`,
`CallTool`, `ResumeTurn`, `RunModel`) becomes a diagram. The prose keeps only
what the diagram cannot show:

```text
`DurableToolCallMiddleware` stops the graph at each tool call. No tool runs
inside `RunModel`. Dex runs each tool call in its own Step, in parallel. Reads
and idempotent writes run in `CallTool`. Other writes run once in
`CallSingleAttemptTool`. A call that needs approval waits in
`AwaitToolApproval` first.
```

## Rewrite workflow

Each rewritten page follows these steps. Step 2 keeps facts from being lost.

1. **Classify** the page as one of the page types. Split a page that holds two
   types.
2. **List every fact** before writing: behaviours, rules, guarantees, defaults,
   errors, and limits.
3. **Place each fact.** It stays, moves to the reference (note the anchor), or
   merges into its one home (note the page). A dropped fact needs a reason in
   the PR.
4. **Draft from the skeleton.** Write the opening and the headings first. Check
   them against the reading-speed rules before writing the body.
5. **Sentence pass.** The 2026-11 two questions, then Voice, Sentences, Words,
   and Formatting.
6. **Compare facts** with the list from step 2. Technical doubts go in a "to
   verify" list. They are never edited quietly.
7. **Check** the `.md` export, the 390 px width, and the Chinese page.
8. **Open the PR** with the filled-in checklist, the moved anchors and
   redirects, and the "to verify" list.

## Rollout

Each step is one PR or a small group of PRs, in both locales:

1. **This plan.**
2. **Guide and structure.** The writing guide goes into
   `.cursor/rules/docs-writing.mdc`, with pointers from `AGENTS.md` and
   `CLAUDE.md`. The sidebar is regrouped (N, R1 to R5). The home page cards
   follow the new order. The site smoke test checks the new order.
3. **Identifiers in code font (D8).** `docs-prose-check` allows inline code. A
   script converts bold identifiers to code font, and a reviewer checks the
   list of converted names. The three agent rule files change with it.
4. **Agent-facing basics (D9).** Frontmatter descriptions, the `llms.txt`
   sections, `llms-full.txt`, per-language Markdown, and the page menu.
5. **Durable Agents (D4, D5).** Three sample pages first, for review: the
   overview, the LangGraph page, and Polling as a non-agent sample. Then the
   overview split, How sessions work, the Agent Quick Start with framework
   tabs, the short framework pages, and Any other framework.
6. **Quick Start (D3, T).** The new example in five SDKs with tests, the new
   page, Order processing in Examples, and Add Dex to your app.
7. **Home page and visuals (H, D7).** The home page, the crash timeline, and
   the docs navbar shortcuts after their visual review.
8. **Comparison page (D6).** Engineering checks each term map first.
9. **The remaining pages.** Design patterns, examples, Run Dex, and concepts,
   each against its skeleton. The `docs-prose-check` lints from W3 start as
   warnings and become errors when a section is done.
10. **Stubs.** Rewrite snapshot links, add redirects, and delete the
    `production/*` and `references/*` stubs. The three rule files still say to
    put volatile facts in those folders until the Developer reference exists.
    That text is removed in the same PR.

## Tests

- `npm run build` in `docs/` for both locales. The build already fails on broken
  links and anchors, so every move and split is checked.
- `docs/tests/site-smoke.mjs` asserts the new sidebar order and the home page
  sections.
- Each new example under `examples/` has an integration test in all five SDKs.
  The Quick Start test skips the Timer, as the order-processing test does
  today.
- `docs-prose-check` gains the W3 lints: opening length, a complete
  `description`, banned words, generic headings, bold density, a lead-in before
  each snippet, and an anchor on every second-level heading.
- The Markdown exporter's tests cover the per-language and per-framework files.

## Documentation

This plan is documentation work. Every changed page ships in English and
Simplified Chinese, except the Developer reference. Each fact keeps one home
under Docs Information Placement. The skills that link to moved anchors change
in the same PR as the move.

## UI/UX

- The docs navbar shortcuts add a second row under the company header. They
  need a visual review in both themes, and at 390 px, before they are built.
- The framework tab component becomes a dropdown at phone width.
- The crash timeline is a new docs component with keyboard controls and a
  reduced-motion mode.
- Every changed page is read in a browser at desktop and phone width, as the
  2026-11 rules require.

## Open questions

- Where the docs navbar shortcuts sit relative to the company header. A second
  row is proposed.
- The sidebar category labels show in English on Chinese pages today. This
  plan keeps that and does not add translations.
