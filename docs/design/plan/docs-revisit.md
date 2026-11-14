# Docs Revisit: Structure, Reader Paths, and the Writing Guide

Status: built (2026-11-12 to 2026-11-13). The restructure shipped in #1692 and
the pull requests listed in [As built](#as-built), which records where the
result differs from the decisions below. Still open: the stubs, the remaining
lints, and the agent-facing pieces listed there.

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
- **Frameworks as tabs, not pages.** Each framework is a tab on the shared
  agent pages, and it has no sidebar entry of its own. The Markdown export
  writes one file per framework, so a page an agent fetches stays small.
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
| D5 | No page or sidebar entry per framework. Each framework is a tab, in alphabetical order, on the Agent Quick Start and on the concept pages where frameworks differ. The built-in agent loop keeps its own page, listed last, because most agents use a framework. The old framework URLs redirect to the Agent Quick Start. | One complete page per framework. Short framework pages next to the tabs. The built-in loop as the first tab. |
| D6 | One comparison page for existing durable execution tools. The home page names no other product. | One page per neighbouring product. |
| D7 | One interactive crash timeline, reused on the home page, Why Dex, the Quick Start, and Sessions. | Mermaid sequence diagrams only. |
| D8 | Code font for identifiers in prose. Bold only for a term's first definition and for UI labels. | Bold for identifiers, as today. |
| D9 | Ship the static agent-facing pieces now. A docs MCP server later. | A docs MCP server in this round. |
| W1 | A fixed form for each sentence type. See [Voice](#voice). | Impersonal throughout. "You" as the default subject. |
| W2 | One clause per sentence. Order shows cause and effect. | Join cause and effect in one sentence. |
| W3 | Enforce the guide with `docs-prose-check` lints and a filled-in checklist in each rewrite PR, plus reading every page in a browser. | The guide and human review only. |
| R1 | Build with AI stays its own top-level group, right after Get Started. It lists Dex App Builder and Dex SDK. Its overview links Dex Connector Contributor, which has no sidebar entry. | Build with AI inside Get Started. All three skill pages in the sidebar. |
| R2 | Design Patterns and Examples stay separate groups. | One Examples group holding products and patterns. |
| R3 | Durable Agents comes right after Build with AI, before Concepts. | Durable Agents after Concepts. |
| R4 | Every example lives in Examples. AI Agent and Order Support Agent come first, and the Durable Agents pages link to them. Order Support Agent holds the full per-framework example code. | Examples inside Durable Agents. |
| R5 | Components becomes Run Dex. It adds a Run Locally page. The rules for adding Dex to a service go into the Application page, listed as Your Application. | Keep the label Components. A separate Add Dex to your app page. |
| T | The current Quick Start does not become a tutorial. Its six-step structure stays, around a new, smaller example. The order-processing code moves to an Examples page, Order Processing. Its integration rules move to the Application page. Why Dex keeps its text and Flow graph, and links to Order Processing for the code. | A tutorial page in Get Started. Fold it into Why Dex. Delete it. |
| — | Why Dex keeps its opening as written, including "platform". | — |
| E | Evaluate Dex is a top-level group for readers deciding whether to adopt Dex: Why Dex?, Compared to Database, Compared to Existing Durable Execution, and Correctness and Expressiveness. Its header opens Why Dex?. | Comparisons under Get Started. Comparisons in the Developer Reference, which holds versioned API details. |
| G | Get Started opens a short landing page that routes readers to the Quick Start, Build with AI, the Agent Quick Start, or Concepts. The Quick Start is listed under it. | Get Started opening Why Dex? with the full order example. |
| P | Primitives open a short overview page with the primitives table from Concepts. Attribute stays in the list. | The Primitives header opening Attribute. |
| DP | Design Patterns lists pattern families and their variants, at most two levels. The overview page keeps the Flow control, Reliability, Messaging, and Data groups. | Four group headers above the families in the sidebar. |
| AQ | The Agent Quick Start has four steps: install, make the agent durable (a diff of the changes), run it, and stop the Worker while **cancel_order** waits for approval. After a restart and an approval, the Timeline shows that nothing before the restart ran again and the write ran once. | A Quick Start that only shows an approval. Stopping the Worker during a model call, which is hard to time. The full example as the Quick Start. |
| AC | Durable Agents has four concept pages for what Dex adds to any framework: Sessions, Tool Calls, Approvals, and Connectors as Tools. Tool Calls and Approvals are separate: one decides whether a call may run again, the other whether it should run at all. | One Tools and Approvals page. A How Sessions Work page. |
| H2 | Group headers that have a landing page open it on click. A page that a header opens is not listed again under that header. | Headers that only expand. |
| NV | (2026-11-11) Two-row header. Row 1: the logo, linking to the Super Durable homepage; a centered Search box (⌘K, local search index, English and Chinese); icon-only Discord and GitHub links; the theme toggle; and a small "Sign up Cloud" button. Both rows run full width, aligned with the sidebar and the table of contents. Row 2: four tabs, Docs, Quickstart, AI Agents, Examples, as extra navigation beside the full sidebar, with the language dropdown at the far right, shown as text. Dex Connectors, Blog, About, Log in and Sign up move to the footer. Refines N. | Language picker as an icon in row 1. No top tabs. |
| H3 | (2026-11-11) Home page as grouped icon cards (option H-A): Quick Start and Why Dex buttons, an SDK row, then First time here?, What are you building?, Evaluating Dex?, Run Dex and Reference. Reuses the production section icons and colours. Supersedes the text "Pick a path" list. | Language-first cards (H-B). The production 10-card grid restored (H-C). |
| G2 | (2026-11-11) Get Started uses the same cards (option G-A). No path's first pass takes more than 15 minutes. Deeper reading goes under "Go deeper" without a time. Times are computed from the Markdown sections each path uses and from timed clean runs of the Quick Starts, checked by AI reviewers' skip notes and one human timing, and enforced by `npm run check`. Cards and buttons show no time labels; the 15-minute limit is a design rule checked by the script, not a label. | Hand-written estimates. Time labels on some cards but not others. |
| F | (2026-11-11) Familiarity is an eighth persona review dimension for all four personas (option F-A), judged against docs each persona already uses. | Agent builder only. |
| RN | (2026-11-11) Renames: Limits and Sharp Edges → Limits (production defaults move to Run Dex in Production; Release status and License move to Limits, one home each). Performance and Maturity → Performance. Agents in Your App → Add Dex to Your Agent. Port Your Process → Add Dex to Existing Code, whose intro names functions, queue consumers, cron and background jobs, webhook handlers, service chains and scripts; it links Your Application's install section as step 0, and the home "Add to your app" card points to it (revises R5's rejected separate page). The path "Fix a process that breaks halfway" → "Make existing code survive crashes". Old URLs and anchors redirect. | Migrate Existing Code. Make Existing Code Durable. |
| SB | (2026-11-11, co-founder note, corrected by Zane) Clicking any group header opens that group's overview page. The overview is not listed again under the header, and no item is labelled "Overview". Every header's page is a real overview of the group, so Evaluate Dex gets its own short overview page and Why Dex becomes its first child. Keeps H2's click behaviour and fixes the headers that opened a specific page. | A child item labelled Overview. Headers that only expand. |
| PF | (2026-11-11, co-founder note) Each paragraph, list item and callout opens with what is true; a negation follows as a clarification. A negation that only restates what the positive already implies is cut (for example "dexcli does not run on Windows" after "macOS or Linux"). A lint flags negation-first openings in English and Chinese, with marked exceptions for real warnings. Reviewers quote any they find; one on their path caps Readability at 4.5, three or more at 4. | Reviewer judgement only. |
| RM | (2026-11-11, co-founder note) Docs never link to READMEs in the repo. Example links go to the example's source folder; the CLI README link goes to the CLI reference; the benchmark reproduce steps move onto Performance. A lint enforces it. READMEs keep developer notes and link to the docs. | Linking READMEs for detail. |
| RV | (2026-11-11) Each review round adds one fresh reviewer who has seen no earlier round and reports what feels unfamiliar or odd, for the four resumed reviewers to verify and score. | Only resumed reviewers. |
| DL | (2026-11-11, docs-lead review) Dex Cloud is real: a "Choose how to run Dex" page compares Dex Cloud and self-hosting, and the "no hosted service" claims go. Home is the router (hero, SDK chips, First time here?, Choose a path with four cards, then Run Dex and Reference as link rows); Get Started holds each path's ordered first pass and Go deeper, with no second card grid. The Quick Start downloads one self-contained example folder with one command instead of cloning the repo; a `dexcli init` may replace that later. Install steps sit at the top of every quick start page without scrolling, repeated at most twice across pages. Agent Markdown is one file per framework. The Step page splits its advanced guarantees out. Failover and Deploys and Change Flow Code move under Run Dex. Every page ends with one "Next" section. llms.txt gets proper nesting and an llms-full.txt. Search improvements wait. | Template repos per language. Keeping 19 home cards plus a Get Started card grid. |
| NP | (2026-11-11) No curated reading-list paths and no circular or matrix navigation. Every card or link goes straight to the one page that does the job; sequence comes only from each page's "Next" section. Home's "What do you want to do?" has four direct cards: Add Dex to existing code, Build a durable agent, Let a coding agent write it, Evaluate Dex. Get Started is short: the Quick Start first, then the same four links. "Make existing code survive crashes" is dropped. Supersedes the path parts of G2 and DL. | Numbered reading lists per path. Path names that restate a page. |
| CL | (2026-11-11, revised) The comparison pages are titled and labelled "Dex vs. Databases" and "Dex vs. Other Engines" (Chinese "Dex vs. 数据库", "Dex vs. 其他引擎"), so labels match titles. The header tab reads "Quick Start", matching the page titles. Dex Cloud facts come only from published sources; anything not found stays out of the docs. | Labels that differ from titles. "Quickstart" as one word. |
| BC | (2026-11-11) One brand accent across all sections. Section icons stay, in a soft brand-green tile; links, heading dividers and active marks use the brand green, softened where they repeat. Diagrams use brand green where Dex is the focus. Supersedes per-section accent colours. | A different accent colour per top-level section. |

## Information architecture

### Sidebar

```text
Home
Get Started          (opens the Get Started landing page) · Quick Start
Build with AI        (opens Dex Skills) · Dex App Builder · Dex SDK
Durable Agents       (opens the overview) · Agent Quick Start (new)
                     Sessions · Tool Calls · Approvals · Connectors as Tools (new)
                     Built-in Agent Loop
                     (each framework is a tab, in alphabetical order, not a sidebar entry)
Concepts             Flow · Step · Primitives ▸ (opens a new overview) · Client · Connector
Design Patterns      pattern families ▸ variants, at most two levels
Examples             AI Agent · Order Support Agent (new) · Order Processing (new) · 8 more
Run Dex              Run Locally (new) · Your Application · Server · CLI · Web
Evaluate Dex         (opens Why Dex?) · Compared to Database
                     Compared to Existing Durable Execution (new)
                     Correctness and Expressiveness ▸
Developer Reference ▸
Glossary
```

- **Get Started** opens a short landing page. Only the Quick Start sits under
  it.
- **Durable Agents** replaces the Agent Frameworks label, because the built-in
  agent loop is not a framework. The `/agent-frameworks/...` URLs stay.
- **Design Patterns** lists pattern families directly. The overview page keeps
  the Flow control, Reliability, Messaging, and Data groups.
- **Examples** replaces the Product Examples label. The `/use-cases/...` URLs
  stay.
- **Run Dex** replaces Components. The `/components/...` URLs stay.
- **Evaluate Dex** holds the material for deciding whether to adopt Dex:
  Why Dex?, both comparison pages, and Correctness and Expressiveness, which
  keeps its own sub-group as #1530 decided.
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
| Quick Start (3,183 words) | A new Quick Start at `/quick-start`. The order-processing code moves to an Examples page, Order Processing. The integration rules move to the Application page. |
| Agent Frameworks overview (3,179 words) | An overview of about 400 words at `/agent-frameworks`. Sessions, Tool Calls, Approvals, and Connectors as Tools take the shared rules, for every framework. The crash and outcome matrices, the write-order rules, and the reconcile steps move to the existing Agents reference pages for failures and Connector tools. |
| 7 framework pages (1,400 to 2,600 words) | Removed as pages. Install, the example code, Worker, routes, run steps, Dex Web approval, and Connect Connectors wiring move into the Agent Quick Start, under that framework's tab. What the adapter does and how a turn maps to Steps open the Agent Quick Start, under the same tab. Each framework's limits move to the Agents reference. Each old URL redirects to the Agent Quick Start. |
| Built-in agent loop (3,340 words) | Keeps its page and URL, listed last under Durable Agents. It holds its own turn model and the features only it has: durable context, steering, questions, plans, MCP tools, live events, and session access. |
| Why Dex? (2,462 words) | Keeps its text and Flow graph, about 700 words. The code moves to Order Processing. |
| New | Get Started landing page. Primitives overview. Compared to Existing Durable Execution. Run Locally. Order Processing. Order Support Agent. Agent Quick Start. Sessions. Tool Calls. Approvals. Connectors as Tools. |
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

**Your Application** (the Application page) gains the rules for adding Dex to
a service: pass services through the Flow constructor, never through a setter
or a global; start a Flow from a handler and wait for a Step; and send commands
and read state through RPCs. It links to Order Processing for the full code.

## Durable Agents

- The overview says what a durable session gives the reader, lists the pages,
  links the two agent examples, and offers the framework choice table. The
  built-in loop is the last row.
- **Agent Quick Start** (four steps, see AQ). Step 2 shows only the changes to
  a plain agent, as a diff per framework. It needs a small example per
  framework under `examples/`.
- **Sessions**: what a session keeps, and how each framework's turn maps onto
  Steps (framework tabs).
- **Tool Calls**: tool policies, effect classes, retries versus at most once,
  tool call outcomes, and reconciling `outcome_unknown`. Every framework user
  sets a policy for every tool, so this is the page they need most.
- **Approvals**: how an approval waits, deadlines, and approving in Dex Web.
- **Connectors as Tools**: which operations are offered, their effect classes
  and outcomes, write order, crashes, and the per-framework wiring (tabs).
- Per-framework limits are volatile. They move to the Agents reference.
- **Built-in Agent Loop** keeps its page, listed last, for new agents without a
  framework.
- Framework tabs are in alphabetical order, remember the reader's choice, and
  `?framework=` selects a tab in a link. The tab bar is one row that scrolls
  sideways when it is too wide. Links to one framework use `?framework=`.
- The Markdown export writes each framework under its own label. A later PR
  writes one file per framework.
- The redirect check in `site-smoke.mjs` accepts only a page path as a target.
  So the old framework URLs open the Agent Quick Start on its default tab.
  Opening the matching tab needs a change to that check or a small redirect
  page.
- No page covers frameworks without an adapter. Dex documents only the
  frameworks it ships adapters for.

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
| How an agent turn maps to Steps | A sequence diagram under each framework tab of the Agent Quick Start. |
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
| Agent prompts | Prompt blocks on the Quick Start and on each framework tab of the Agent Quick Start. |
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
| Framework tab | What the adapter does, Install, How a turn maps to Steps, How this framework differs, Limits | Agents reference |
| Task page with framework tabs | Goal, Steps with framework tabs, Check it worked | The Durable Agents concept pages |
| Quick start | One sentence on what the reader will run and how long it takes, What you will build, Before you start, Numbered steps, one closing sentence that names what the reader just did ("You have run your first durable agent."), Next | Examples, concept pages |
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

The structure only works as a whole. A regrouped sidebar points to pages that
do not exist until the new pages ship, so the structure and its pages ship in
one PR. Code ships first, because snippets come only from runnable examples.

1. **This plan.**
2. **Example code (D3, D5, T).** The new Quick Start example in five SDKs, a
   small example for each agent framework, and their tests. Engineering
   reviews it. The docs PR waits for it.
3. **Docs restructure (N, R1 to R5, H, D3, D4, D5, T).** The writing guide in
   `.cursor/rules/docs-writing.mdc`, with pointers from `AGENTS.md` and
   `CLAUDE.md`. The regrouped sidebar, the Get Started page, the home page, the
   Durable Agents pages with framework tabs, the Quick Start, Order Processing
   in Examples, Run Locally, the Application page rules, the Primitives
   overview, the trimmed Why Dex, the redirects from moved URLs, and the site
   smoke test for the new order. Both locales.
4. **Comparison page (D6).** Engineering checks each term map first. It ships
   on its own so the check does not hold back step 3.

Later, each on its own and in any order:

- **Identifiers in code font (D8).** `docs-prose-check` allows inline code. A
  script converts bold identifiers to code font, and a reviewer checks the list
  of converted names. The three agent rule files change with it.
- **Agent-facing basics (D9).** Frontmatter descriptions, the `llms.txt`
  sections, `llms-full.txt`, per-language Markdown, and the page menu.
- **Visuals (N, D7).** The crash timeline and the docs navbar shortcuts, after
  their visual review.
- **The remaining pages.** Design patterns, examples, Run Dex, and concepts,
  each against its skeleton. The `docs-prose-check` lints from W3 start as
  warnings and become errors when a section is done.
- **Stubs.** Rewrite snapshot links, add redirects, and delete the
  `production/*` and `references/*` stubs. The three rule files still say to
  put volatile facts in those folders until the Developer reference exists.
  That text is removed in the same PR.

## As built

The docs restructure shipped in #1692 and was refined over the next two days.
Where this section differs from the sections above, the docs follow this
section. `.cursor/rules/docs-writing.mdc` ("Page structure and voice") holds
the writing guide as it is enforced.

### Pull requests

| PR | What it built |
| --- | --- |
| #1686 | The standalone Quick Start example in five SDKs, the agent quick-start modules, and the one-file agent starter. |
| #1692 | The restructure: two-row header, home cards, Evaluate Dex, Run Dex pages, Durable Agents pages with framework tabs, the crash timeline, per-language and per-framework Markdown, `llms.txt` and `llms-full` files, identifiers in code font, and the structure and positive-first lints. |
| #1718 | All Chinese pages and the Glossary, translated sidebar labels, overviews as wayfinders, statement headings. |
| #1723 | Diagram components with fixed color roles, the coding agent box, Framework Support split from Sessions, Run Agents in Production. |
| #1731, #1736 | UI details and edge-case semantics moved to the Developer reference; Pi added to every framework page. |
| #1756 | The current sidebar, the Developer Reference "in Depth" groups, Dex vs. Temporal, How Durable Agents Work, Run on Dex Cloud, Self-Host Dex, Operate and Debug, the Quick Start run card. |
| #1769 | Dex vs. Temporal compares the core design and its effects. |
| #1772 | Each main-path page leads with its main answer. The Get Started page is removed, and the Quick Start is the single entry. |
| #1774, #1776, #1777 | The compact coding agent box, the "Welcome to Dex" home heading, and wording fixes. |

### Sidebar

```text
Home
Quick Start          (opens the Quick Start) · Add Dex to Existing Code
                     Test Your Flows · Build with AI
Durable Agents       (opens the overview) · Agent Quick Start
                     Add Dex to Your Agent · Framework Support
                     How Durable Agents Work · Run Agents in Production
Run Dex              (opens the overview) · Run on Dex Cloud · Self-Host Dex
                     Run Dex in Production · Operate and Debug
                     Deploy Code Changes
Concepts             Flow · Step · Primitives ▸ · Client · Connector
                     Dex and Your Database
Design Patterns      pattern families ▸ variants
Examples             AI Agent · Order Support Agent · Order Processing · 8 more
Evaluate Dex         (opens its overview) · Why Dex? · Dex vs. Databases
                     Dex vs. Temporal · Performance · Limits
                     Correctness and Expressiveness ▸
Developer Reference  components ▸
                     Running Dex in Depth: Run Locally · Your Application ·
                       Dex Server · CLI · Web · Failover and Deploys
                     Agents in Depth: Sessions · Tool Calls · Approvals ·
                       Connectors as Tools · Built-in Agent Loop ·
                       How Each Framework Runs on Dex
                     Dex Skills in Depth: the three skill pages
Glossary
```

What changed from the decisions above:

- **No Get Started page (replaces G, and the Get Started parts of DL and
  NP).** The first group is Quick Start, and its header opens the Quick Start
  tutorial. That tutorial owns the install, and it is the one exception to the
  wayfinder rule. `/get-started` redirects to `/quick-start`.
- **Build with AI is one page in the Quick Start group (replaces R1).** The
  three skill pages sit under Developer Reference › Dex Skills in Depth.
- **The main sections keep task pages only.** Pages that hold depth moved
  under three Developer Reference groups and kept their URLs: Running Dex in
  Depth, Agents in Depth, and Dex Skills in Depth. This moves Run Locally and
  Your Application (R5), and Sessions, Tool Calls, Approvals, and Connectors as
  Tools (AC), out of the main path.
- **Durable Agents has five task pages.** How Durable Agents Work maps
  agent-framework terms to Dex. Framework Support is a support grid and a
  limits table. How Each Framework Runs on Dex keeps the per-framework detail.
- **Run Dex has its own task pages.** Run on Dex Cloud, Self-Host Dex, Run Dex
  in Production, Operate and Debug, and Deploy Code Changes (the Change Flow
  Code page, retitled). Choose How to Run Dex folded into the Run Dex overview
  and redirects there.
- **Dex and Your Database sits under Concepts.**
- **Advanced Step Guarantees sits under Correctness and Expressiveness**, at
  `/correctness-and-expressiveness/step-guarantees`, with a redirect.
- **Sidebar labels are translated** in Chinese (reverses the open question
  below). Each Chinese group overview uses the translated label as its title.

### Header and home page

- **Header (NV, CL).** Row 1: the Super Durable wordmark, local search with
  ⌘K, icon links to Discord and GitHub, the theme switch, and **Sign up
  Cloud**. Row 2: the tabs Docs, Quick Start, AI Agents, and Examples, with
  the language switch at the right. The company links are in the footer. This
  answers the navbar open question below.
- **Home page (replaces H, H3, and the card list in NP).** "Welcome to Dex",
  two sentences on what Dex does, then two card groups. Start here: Quick
  Start, Durable Agents, Examples. Common tasks: Add Dex to existing code,
  Build a durable agent, Add Dex to your agent, Build with AI, Run Dex,
  Evaluate Dex. A row of Quick Start links, one per SDK, closes the page. The
  home page has no agent prompt strip and no crash timeline.
- **Coding agent box.** The prompt block is a compact box at the top of six
  task guides. It gives one shell line per coding agent that starts the agent
  with a one-line prompt pointing at the page's Markdown. Only the Quick Start
  box includes the Dex Skills install. The box is left out of the Markdown
  export, because the Markdown is what the prompt points the agent at.

### Quick Start and Agent Quick Start

- **Quick Start (D3, T).** Seven steps: install Dex and download the example,
  read the Flow, add a Worker and a Client, start a Flow, stop the Worker,
  stop the Worker inside a Step, and see the run in Dex Web. Step 1 downloads
  one example folder from the repository archive with `curl` and `tar`, with no
  clone. Step 2 draws the Flow with the Dex Web canvas. Step 7 shows the crash
  timeline and a run card styled like Dex Web.
- **Agent Quick Start (AQ).** Four steps: get the example and run it, approve
  a write, stop the program while the agent waits, and stop it while a tool
  runs. `--approve` lets a coding agent approve without a terminal. A last
  section, "What made it durable", shows the changes.
- **Framework tabs (D5).** Alphabetical, shared by every tab group on a page,
  remembered in the browser, and selected in a link by `?framework=`. The tab
  bar is one row that scrolls sideways at phone width; there is no dropdown.
  The old framework URLs redirect to the Agent Quick Start on its default tab.
- **The built-in agent loop** keeps its page under Agents in Depth. It is a
  tab on Connectors as Tools only, because the Agent Quick Start has no
  built-in loop example yet.

### Comparison, visuals, and agent-facing docs

- **Dex vs. Temporal (replaces D6 and the "Dex vs. Other Engines" label in
  CL).** The page compares Dex with the most common replay-based engine: the
  design difference, running the server, concurrency defaults, what they
  share, the same process in both, and a term map. An "Other engines" section
  covers journal-based runtimes, database-backed libraries, step function
  services, and agent framework checkpoints, with when each fits better. It
  names Temporal in its title, so D6's "the home page names no other product"
  still holds and the comparison page itself is the exception. It has no "last
  checked" date.
- **Visuals (D7, BC).** Diagrams are docs components with fixed color roles:
  green is Dex, blue is a third-party service or framework, grey is your own
  code, and amber marks a failure. The crash timeline is on the Quick Start and
  the Agent Quick Start, honours reduced motion, and has a text version in the
  Markdown export.
- **Agent-facing docs (D9).** Built: `llms.txt` with whole-sentence
  descriptions, grouped by sidebar section; `llms-full.txt` and one
  `llms-full.<language>.txt` per SDK language; a `.md` file per page, per SDK
  language, and per framework; and a "View as Markdown" link on every page.
  Not built: the Copy page and Open in menu, `Accept: text/markdown`, the
  recommended `AGENTS.md` snippet, and the docs MCP server.
- **Identifiers in code font (D8).** Done. `docs-prose-fix` is a no-op.

### Checks

`npm run check` runs `lint:structure`, which holds three scripts:

- `tests/docs-structure.mjs`: no README links; every group header opens its
  overview, titled with the group's name in both locales; pages end with Next;
  no reading lists on the home page; every page has a left nav; statement
  headings; and SDK, server image, and Dex Web version pins against
  `reference/versions.json`.
- `tests/docs-positive-first.mjs`: positive-first openings and headings in
  both locales (PF).
- `tests/docs-locale-sections.mjs`: each Chinese page has as many H2 sections
  as its English page.

The other W3 lints were not built: opening length, banned words, generic
headings, bold density, a lead-in before each snippet, and an anchor on every
second-level heading.

### Still open

- The `production/*` and `references/*` stubs still exist. They now show the
  left nav, and deleting them is the Stubs step of the rollout.
- A built-in agent loop example for the Agent Quick Start, so the loop can be
  a tab there.
- Dex Cloud connection details, regions, pricing, and status, until they are
  published.
- The remaining W3 lints and the agent-facing pieces listed as not built.

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

Both questions are settled; see [As built](#as-built).

- Where the docs navbar shortcuts sit relative to the company header. Built as
  the second header row.
- The sidebar category labels show in English on Chinese pages. Reversed:
  they are translated (#1718).
