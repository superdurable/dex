# Stage 1: product discovery and the business contract

Resolve the business model from the request, retained answers, the existing
host, and its source. A clear request to build the product authorizes routine
implementation choices. Record reasonable defaults and continue. Ask only when
an unresolved business, authorization, or integration decision changes the
outcome. Do not ask users to choose SDK methods, approve compile fixes, or
approve the same scope twice. A short business request need not become a
questionnaire.

## What to resolve

Identify each of these from the supplied intent first:

- the process maintainer;
- managers, operators, approvers, end users, participants, and external
  systems, as actors, before you derive roles;
- each actor's authentication boundary;
- each human Action's stable, granular permission;
- external systems that trigger or join the process, and whether each is a
  public external product or an organization-controlled internal service;
- start triggers, inputs, outputs, deadlines, waits, approvals, retries,
  recovery, audit, search, and sensitive data;
- the business outcome of each run, including results that happen in another
  system, and the facts the business would compare runs by, such as plan,
  region, source, or customer size;
- durable facts, their owning business identity, read and write paths,
  cross-Flow reuse, volume, contention, search, retention, and cleanup;
- candidate top-level Flows, their independent waits, Timers, and terminal
  outcomes, and the RPCs or messages between Flows that each business event
  would cause;
- every management list, detail, action, assignment, edit, progress, and
  recovery need;
- what authenticates users and maps roles to permissions at the
  application's trusted boundary, such as an identity provider or reverse
  proxy;
- the [UI question](#ui-question) and, for each external system, the
  [Connector question](#connector-question).

## Stack checkpoint

Classify the repository before you propose a language. Pick the backend
language with the rule in
[workspace bootstrap](workspace-bootstrap.md#choose-the-language). The backend
uses an exact released Dex SDK for that language, paired with the installed
`dexcli`. Add a React and Vite frontend when the user wants their own UI. For
each Connector the user chose, use the official package that the catalog
lists for the application's language.

## Actor, role, operation, and permission matrix

List actors first, then derive the smallest authorization model. Start with
one `admin` role for trusted maintainers and operators. A requester,
subscriber, participant, or external system is an actor, not automatically a
role.

| Actor | Authentication boundary | Goal and visibility | Actions and permissions | Role | Evidence for another role |
| --- | --- | --- | --- | --- | --- |
| Trusted maintainer or operator | authenticated host or reverse proxy | all required operational state | granular approval, edit, recovery, and configuration permissions | `admin` | none by default |
| Requester or participant | product-specific | only confirmed participant-facing state | typed start, response, or Action when required | none by default | a distinct authenticated membership plus different visibility or operations |
| External system | Connector or integration identity | only the required exchange | typed Trigger, RPC, or event | none | never derive a human role from an integration identity |

- Actors describe participants. Roles group authenticated people with the same
  visibility and operations. Permissions describe single Actions.
- Add a role only when a confirmed, separately authenticated group needs a
  different view or a different set of operations.
- Never create one role per actor, lifecycle stage, Action, or permission.
- Keep Action permissions granular even when `admin` holds all of them.
- Record authentication, authorization, audit, and reason requirements.
  Collapse an unjustified role back into `admin`.

## Lifecycle questions

Confirm:

- the business identity and the source of the unique Flow ID;
- the start trigger and duplicate-start behavior;
- the typed start input and completion output;
- the happy path and every terminal business outcome;
- human decisions, required fields, deadlines, reminders, and escalation;
- retryable provider failures versus business rejections;
- unknown external mutation outcomes and query-first reconciliation;
- cancellation, recovery, cleanup, and operator intervention;
- searchable indexed fields and detailed display fields;
- sensitive data that must stay out of IDs, logs, Streams, and generated
  artifacts;
- whether any data must stay unreadable to Dex Server, its storage, and its
  operators. If so, plan payload encryption, and keep that data out of IDs,
  names, and Attributes the server compares. See
  [payload encryption](core/data-handling.md#sensitive-data-and-payload-encryption).

For each outcome and comparison fact, record how the run will show it: a Step
it enters, an Action, or a scalar Attribute. See
[runs that can be measured](backend-implementation.md#runs-that-can-be-measured-and-improved).

## Flow-boundary decisions

Three shapes are distinct decisions:

| Shape | Identity and lifecycle | Communication |
| --- | --- | --- |
| Parallel Steps | one Flow identity and lifecycle | shared Attributes and Channels |
| Independent top-level Flows | separate starts, owners, and terminal outcomes | typed RPCs or Channel messages on rare events |
| SubFlows | explicit parent-child lifecycle | parent waits for child results |

### Data-lifecycle boundary

Capture this matrix for each candidate:

| Candidate data or process | Authoritative owner | Retention and cleanup | Independent waits, Timers, and terminal outcomes | Pollution if kept in the existing Flow | RPCs or messages between the Flows per frequent event | Top-level Flow decision |
| --- | --- | --- | --- | --- | --- | --- |
| product-specific | stable business owner | product-specific | product-specific | unrelated state the existing Flow would retain | none, or the split is wrong | keep or split, with evidence |

Ask four questions together:

1. Does the data have a different authoritative owner and a different
   retention or cleanup lifecycle?
2. Does the process have its own waits, Timers, and terminal outcomes, such as
   `verified`, `expired`, or `cancelled`?
3. Would keeping it in an existing Flow force that Flow to retain, clean up, or
   coordinate state unrelated to its own lifecycle?
4. Would the split make the Flows exchange RPCs or Channel messages on
   frequent events, such as every user operation, every external callback, or
   every poll?

A yes to question 4 rules the split out, whatever the other answers. Every
call between Flows, including a read-only RPC, adds latency and history to
both Flows.
For example, a separate Flow that tracks another Flow's inactivity needs a
message on every operation. Keep that deadline in the Flow where the
operations happen. Calls between Flows are fine for rare events: a start, a
completion, a cancellation, or an explicit user command.

Create an independent top-level Flow only when questions 1 to 3 are yes and
question 4 is no. Otherwise keep the work in the existing Flow's Steps,
Attributes, or AttributeMaps. Field count, source size, stage count, retry
policy, code reuse, provider neutrality, and ordinary parallelism never
justify a new top-level Flow. Keep long-lived authoritative facts in their
stable owner. Keep temporary validation or failure state in its short-lived
owner, so it never pollutes the authoritative store.

### Execution shape

| Candidate work | Same business lifecycle | Parallel-Step or batching option | Needs its own lifecycle, history, or retries, or a very large fan-out | Decision |
| --- | --- | --- | --- | --- |
| product-specific | yes or no | concrete option | yes or no | Step, independent top-level Flow, or SubFlow |

Prefer parallel Steps inside one Flow: static or dynamic parallel Steps, a
Join for parallel branches, Channels for quorum, batching, typed RPCs, and
ordinary helper functions. Use a SubFlow when the child needs its own
lifecycle, history, or retries, or when one fan-out is very large. Define the
child's input, output, cancellation, and failure handling. Code reuse or
provider abstraction alone is better served by helper functions.

## Storage decision

Default every durable fact to Dex before you propose an external dependency.

| Fact or collection | Owning Flow or business identity | Retention and cleanup | Reads and writes | Volume and contention | Dex primitive and access path | Proven external-store gap |
| --- | --- | --- | --- | --- | --- | --- |
| Process state | the process Flow ID | follows the process | Step and Action updates, status and detail reads | product-specific | typed Attribute, plus an RPC or index when needed | normally none |
| Keyed or growing records | a stable domain or entity owner | explicit record retention and deletion | exact lookup, bounded page, independent mutation | product-specific | partitioned or chunked AttributeMap with exact loads and locks | only a confirmed query or contention limit |
| Shared domain facts | a stable domain or entity Flow | owned independently of callers | reused by several process or API paths | product-specific | one owner with typed application operations | cross-Flow reuse alone is not a gap |

Do not choose PostgreSQL, another database, a cache, an ORM, or a shadow read
model only because data must persist. Dex provides durable Attributes,
AttributeMaps, indexes and search, locks, exact instance loads, blob offload
for large values, and optional Attribute Store sync.

Attribute Store sync still adds an external PostgreSQL database. Use it only
for a confirmed query gap. Prefer its server-managed sync over application
dual writes while Dex stays authoritative. When you choose it, record the
Flow ID format behind any row-key template, because that format becomes a
storage contract. Also record whether keyed AttributeMap records need their
own rows. The [Attribute Store sync reference](https://docs.superdurable.io/reference/server/attribute-store-sync)
has the row-key and table rules.

Require an external store only for a specific unsupported need: complex or
ad-hoc indexes, full-text or vector search, high-concurrency access to one hot
record, relational joins or multi-record transactions, or large analytical
scans. Record the exact operation, scale or SLO, authority, synchronization,
failure, and reconciliation design. "We may need it later" is not evidence.

## UI question

The backend is the core of the product. Ask the user once, in the same
discovery batch as the other open questions, which UI they want:

- **their own UI**, which most products need, such as a participant portal,
  a branded application, or screens for their own users;
- **Dex Web** for operations and management: Runs, Work Queue, Actions,
  edits, history, and diagnostics;
- **both**, often their own UI for end users and Dex Web for operators;
- **neither**, when callers only use the application API.

Skip the question when the request already answers it. Never default to Dex
Web alone. The user's own UI needs no Dex Web gap to justify it. For their
own UI, load [UI workflow](ui-workflow.md). For Dex Web, load
[Dex Web](dex-web-v2.md) and record the management mapping below. When the
user chose both, leave the needs Dex Web covers to Dex Web unless the user
asks for them in their own UI.

With Dex Web deployed, still confirm what supplies authentication,
role-to-permission mapping, tenant isolation, and the trusted reverse proxy in
front of it.

### Dex Web management mapping

Use this mapping only when the user chose Dex Web. For each management
operation it serves, define its indexed Attributes, Summary RPC fields,
Display RPC fields, Action RPC input and metadata, condition, and permission.

| Management need | Dex Web capability |
| --- | --- |
| List and filter business records | Model each record as a Run. Expose search criteria as indexed Attributes and extra list fields from the Summary RPC. |
| Inspect one record | Return detail fields and UI slots from the Display RPC. |
| Perform a business operation | Register an RPC with Action metadata, a typed input form, a current-state condition, and one permission. |
| Find assigned or actionable work | Work Queue discovers Runs from permission history. Opening a Run rechecks current Action eligibility. |
| Change simple business fields | Declare editable scalar Attributes in the Display RPC. |
| Inspect progress and failures | Use the Run timeline, Step graph, execution details, and recovery information. |

Record this mapping as a discovery artifact.

## Connector question

When discovery finds an external system, ask the user once whether they want
to use a released Connector for it. Skip the question when the request
already answers it. Load
[Connector architecture](connector-architecture.md) only when the user wants
one.

When the user declines, the application calls the system from its own Step
code in `Execute`. Each call keeps an idempotency identity and recovery. Its credentials stay out of code and Flow state.

When the user wants a released Connector, create a Connector capability
matrix for every Trigger, Query, Mutation, and integration UI. Follow the
discovery procedure in
[Connector architecture](connector-architecture.md#released-capability-discovery).

- Start from the published catalog at
  `https://connectors.superdurable.io/catalog.yaml`.
- For a public external product, match each need to an exact released
  capability, not a Connector name. Verify the full contract in the
  component tag's immutable `connector.yaml`.
- Require the application's language in the Connector's `languages` map and
  in the capability's own `languages` list. A capability without that
  language is a gap.
- Record the catalog URL, Connector ID, capability kind and name, component
  tag, immutable manifest URL, and the reuse or gap decision.

If the catalog or the release manifest cannot be verified, stop
Connector-dependent implementation and report the blocker. For a system the
user chose a Connector for, a missing Connector, operation, Trigger, or UI
unit for a product with a documented API or official SDK is a
`dex-connector-contributor` work item and a production release blocker. It
is never permission to install the provider SDK or write local integration
code without asking the user again.

Mark a service internal only when the organization owns and controls it.
When the user chose a Connector for an internal service, follow the
[internal connector library decision](connector-architecture.md#internal-connector-library-decision).

If the process begins from Slack, email, a webhook, or another external
source the user chose a Connector for, model it as a Connector Trigger.
Otherwise the application API receives the event: verify the provider
signature or an equivalent proof at that boundary, deduplicate by the stable
provider event ID, and correlate the event to its Flow. Either way, the
application decides whether the event starts a Flow or invokes a typed RPC.

## Confirmation artifact

Before code, provide:

1. the actor, role, and permission matrix, with every non-`admin` role
   justified;
2. a numbered lifecycle with decisions and terminal outcomes;
3. the data-lifecycle and execution-shape matrices, including every top-level
   Flow, every candidate collapsed into a Step, RPC, or helper, and the full
   gate for any proposed SubFlow;
4. the UI answer: the user's own UI, Dex Web, both, or neither, and the
   application API operations the UI or callers need;
5. when the user chose Dex Web, the management mapping, with the proposed
   Summary, Display, Action, indexed Attribute, Work Queue, and permission
   contracts;
6. the proposed Flow, Step, state, message, Timer, RPC, and Connector
   boundaries;
7. the storage matrix and any evidence-backed external-store gap;
8. the Connector answer for each external system and, for each chosen
   Connector, reuse, contribution, internal-library decisions, and release
   status;
9. unresolved tradeoffs.

Existing implementation authority and stated requirements count as
confirmation of routine defaults. When a consequential choice remains open,
ask for explicit confirmation. A casual discussion reply is not approval to
implement.
