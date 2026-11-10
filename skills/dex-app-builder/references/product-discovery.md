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
- what authenticates users and maps roles to permissions in front of a
  deployed Dex Web, such as an identity provider or reverse proxy;
- whether any custom process UI is needed beyond Dex Web.

## Stack checkpoint

Classify the repository before you propose a language. An effectively empty
repository gets the stack in [workspace bootstrap](workspace-bootstrap.md): a
Go backend on an exact released Dex Go SDK paired with the installed `dexcli`,
plus a React and Vite frontend only for a confirmed custom UI. Do not offer
TypeScript, Node, or another SDK language as an equivalent default.

Only an explicit user request can replace the Go backend for a new
application. Before you accept it, explain that App Builder, strict FDG 2.0,
and Dex Web management metadata require Go. A non-Go backend becomes a
standalone `dex-sdk` project. Python and TypeScript projects may use official
Connectors where the catalog lists their language. Java and Rust have no
Connector SDK.

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
  artifacts.

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
call between Flows adds history to both Flows. Every RPC, including a
read-only one, is a separate engine operation with its own latency and cost.
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

| Candidate work | Same business lifecycle | Parallel-Step or batching option | Observed complexity or concurrent Step count | Explicit SubFlow confirmation | Decision and rejected alternatives |
| --- | --- | --- | --- | --- | --- |
| product-specific | yes or no | concrete option | measured requirement, not speculation | required before a SubFlow | Step, independent top-level Flow, or confirmed SubFlow |

New applications default to no SubFlows. Use static or dynamic parallel
Steps, Channels for joins or quorum, batching, typed RPCs, and ordinary Go
helpers first. Provider abstraction, research stages, model calls, code reuse,
an independent retry policy, and ordinary bounded fan-out never justify a
SubFlow.

Propose a SubFlow only when all of these hold:

1. An existing design or running system gives evidence, not a worry about
   future complexity.
2. The main graph is already impractical to review, evolve, or operate, or a
   fan-out needs more than about 200 concurrent Step executions. That number
   is an architecture-review threshold, not a Server limit. Crossing it
   permits a proposal; it never selects a SubFlow on its own.
3. Parallel Steps, batching, Channel coordination, RPCs, and code abstraction
   were evaluated and rejected with reasons.
4. Child identity, input and output, parent completion, cancellation,
   failure, retry, duplicate submission, and concurrency are defined.
5. The user explicitly confirms the SubFlow design.

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

## Management UI capability mapping

Process products usually need a management interface. Design it through
Dex Web first. For each management operation, define its indexed Attributes,
Summary RPC fields, Display RPC fields, Action RPC input and metadata,
condition, and permission. Only then decide whether a custom surface is
needed.

| Management need | Dex Web capability |
| --- | --- |
| List and filter business records | Model each record as a Run. Expose search criteria as indexed Attributes and extra list fields from the Summary RPC. |
| Inspect one record | Return detail fields and UI slots from the Display RPC. |
| Perform a business operation | Register an RPC with Action metadata, a typed input form, a current-state condition, and one permission. |
| Find assigned or actionable work | Work Queue discovers Runs from permission history. Opening a Run rechecks current Action eligibility. |
| Change simple business fields | Declare editable scalar Attributes in the Display RPC. |
| Inspect progress and failures | Use the Run timeline, Step graph, execution details, and recovery information. |

Record this mapping as a discovery artifact. Keep each fully covered need in
Dex Web, even when another need requires Custom UI. For an uncovered need,
name the exact missing interaction and its user. A general wish for an admin
portal, dashboard, management backend, or branded shell is not a capability
gap.

## UI-mode decision

Decide the UI mode only after the management mapping is complete and the
Summary, Display, Action, indexed Attribute, and permission contracts are
designed. [UI workflow](ui-workflow.md) has the two modes.

- **No custom UI** when those surfaces serve operators and maintainers. The
  application needs no frontend of its own. Still confirm what supplies
  authentication, role-to-permission mapping, tenant isolation, and the
  trusted reverse proxy in front of a deployed Dex Web.
- **Custom UI** only for a recorded Dex Web gap, such as a participant
  journey, a different navigation model, domain visualization, or a complex
  interaction Dex Web lacks. Record which requirement forces it.

## Connector decision

Create a Connector capability matrix for every Trigger, Query, Mutation, and
integration UI. Follow the discovery procedure in
[Connector architecture](connector-architecture.md#released-capability-discovery).

- Start from the published catalog at
  `https://connectors.superdurable.io/catalog.yaml`.
- For a public external product, match each need to an exact released
  capability, not a Connector name. Verify the full contract in the
  component tag's immutable `connector.yaml`.
- Require `go` in the Connector's `languages` map and in the capability's
  own `languages` list. A capability without `go` is a gap.
- Record the catalog URL, Connector ID, capability kind and name, component
  tag, immutable manifest URL, and the reuse or gap decision.

If the catalog or the release manifest cannot be verified, stop
Connector-dependent implementation and report the blocker. A missing
Connector, operation, Trigger, or UI unit for a product with a documented API
or official SDK is a `dex-connector-contributor` work item and a production
release blocker. It is never permission to install the provider SDK or write
local integration code.

Mark a service internal only when the organization owns and controls it.
Then follow the
[internal connector library decision](connector-architecture.md#internal-connector-library-decision).

If the process begins from Slack, email, a webhook, or another external
source, model it as a Connector Trigger. The application decides whether the
event starts a Flow or invokes a typed RPC.

## Confirmation artifact

Before code, provide:

1. the actor, role, and permission matrix, with every non-`admin` role
   justified;
2. a numbered lifecycle with decisions and terminal outcomes;
3. the data-lifecycle and execution-shape matrices, including every top-level
   Flow, every candidate collapsed into a Step, RPC, or helper, and the full
   gate for any proposed SubFlow;
4. the management UI capability mapping, with the proposed Summary, Display,
   Action, indexed Attribute, Work Queue, and permission contracts;
5. the **No custom UI** or **Custom UI** mode and any recorded Dex Web gap;
6. the proposed Flow, Step, state, message, Timer, RPC, and Connector
   boundaries;
7. the storage matrix and any evidence-backed external-store gap;
8. Connector reuse, contribution, internal-library decisions, and release
   status;
9. unresolved tradeoffs.

Existing implementation authority and stated requirements count as
confirmation of routine defaults. When a consequential choice remains open,
ask for explicit confirmation. A casual discussion reply is not approval to
implement.
