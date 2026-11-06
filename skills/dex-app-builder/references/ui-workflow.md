# Stage 2: the application surface

Use this stage only after discovery completes the management UI capability
mapping in [product discovery](product-discovery.md#management-ui-capability-mapping).
Design Summary RPC, Display RPC, Action RPC, indexed Attribute, Work Queue,
and permission coverage first. Then choose exactly one mode.

| Mode | When | The application ships |
| --- | --- | --- |
| No custom UI | Dex Web covers management and no participant UI is requested | the Go Worker, plus a minimal HTTP server only for confirmed integration ingress |
| Custom UI | the mapping names an interaction Dex Web v2 cannot provide | the Go Worker, an application API from a generated contract, and a frontend for that interaction only |

When Dex Web covers management and nobody asks for a participant UI, select No
custom UI without asking. A request for an admin portal, dashboard, or
management backend is not a reason for Custom UI. "The project needs a
backend" is not a capability gap either.

## No custom UI

Dex Web v2 is the only process-management UI: `dexcli dev` locally, and the
standalone Dex Web behind the deployment's authenticated proxy. Define indexed
Attributes, `GetDexSummary`, `GetDexDisplay`, editable fields, human-action
Steps, and Action RPCs with conditions and permissions, so Run and Work Queue
modes cover the confirmed process. [Dex Web v2](dex-web-v2.md) and
[Go FDG authoring](fdg-authoring.md) describe those contracts.

Do not build any custom management surface or operation:

- approval, rejection, retry, escalation, status, display, list, search, or
  detail routes;
- Action or Attribute proxy endpoints;
- dashboards, forms, or queues;
- mock lifecycle state, mock controls, fixtures, or their tests.

Remove such scaffolding when it exists only as unused sample code. When an
existing repository already contains a non-business frontend shell, keep it
non-business. Do not keep speculative endpoints.

Keep a webhook only when it is confirmed integration ingress. Keep only that
HTTP operation and the verification and correlation code it needs. Do not
grow it into a second management backend. An external provider's webhook
belongs to its dedicated Connector Trigger. A generic HTTP Connector is only
for an organization-controlled internal system.

Verify through Dex Web that Runs, Summary and Display fields, Work Queue
discovery, and Actions cover the process, and that the application exposes no
management route. If custom behavior is requested later, enter the Custom UI
workflow before you implement it.

## Custom UI

Enter this mode only for the unsupported interaction that the mapping names.
Keep every operation Dex Web v2 covers on its native Run, Work Queue, detail,
Action, edit, timeline, and graph surfaces. Valid gaps include a participant
portal, a different navigation model, domain visualization, or a complex
interaction Dex Web does not provide.

### 1. Resolve material interaction choices

A clear implementation request already authorizes its stated pages, fields,
and behavior. Implement those directly. Use the optional static checkpoint
below only when a consequential interaction choice is still open. Ask only
about that choice.

Agree only on the information architecture the backend design needs:

- role-specific entry pages and ordinary link navigation;
- the list, detail, create, action, and completion pages;
- labels, inputs, buttons, and placeholder regions on each page;
- which Dex Web v2 surfaces stay available to maintainers.

The checkpoint is a low-fidelity static wireframe in the application's
frontend. A new frontend defaults to a React and Vite application in its own
directory. The wireframe follows these rules:

- React renders static markup only: no hooks or application state, no API or
  generated-client calls, no local storage, timers, mock server, mock
  lifecycle, mock controls, or provider behavior.
- No images, generated art, custom icons, animation, branding, gradients, or
  decorative effects. CSS stays neutral and minimal.
- Inputs and buttons are inert. Use ordinary links only to show navigation
  between directly openable page URLs.
- Do not model loading, validation, success, failure, or recovery yet.

Run only the frontend development server on a stable local port. Verify that
every page URL renders. Give the user a page inventory with each name,
purpose, and direct URL. Ask about the open page set, navigation, fields, or
actions. Do not ask for approval of colors, typography, imagery, responsive
refinements, or dynamic states.

### 2. Contract and backend design

Design the Flow, Connector capabilities, and application HTTP API contract
together. Map every approved UI action to an application operation backed by a
Flow start, a typed RPC, a query, or confirmed ingress. Define authentication
and permission enforcement, idempotency, asynchronous status, validation,
response and error shapes, retry, and terminal outcomes.

- The API contract describes the business boundary. It never exposes Dex
  Steps, Channels, Attributes, connection credentials, or other execution
  internals.
- Write it as an OpenAPI document, or in the project's existing contract
  format.
- Generate the Go server interfaces and the TypeScript client from it before
  you implement handlers. The Go backend implements the generated
  interfaces. The browser later uses the generated client.
- Never hand-write parallel transport types or hand-edit generated code.
  Follow the project's convention for committing or regenerating generated
  output.

Implement and verify the Go Flows and the required Connectors before you
return to dynamic frontend work. A missing public Connector capability follows
the Connector Contributor workflow, with its authorization and release
requirements.

### 3. Integration and durable verification

Implement the Go boundary and the UI against the same generated contract.
Replace the wireframe's inert controls with generated client calls. Add the
confirmed loading, validation, empty, success, failure, retry, recovery, and
terminal behavior. The browser calls the application API. It never imports a
Dex client or touches Dex primitives directly. Missing private credentials
defer real execution, not source implementation.

Follow a real-dependency policy:

- Exercise loading, validation, failure, retry, and terminal states through
  real APIs. Record a coverage gap for any case you cannot run.
- No component mocks, intercepted API responses, fake providers,
  application-level mock API, or mock controls.
- Prove waits, RPCs, retries, Worker replacement, provider effects, and
  terminal behavior through the real Dex and Connector path on a local
  `dexcli dev` stack.

Application UI controls never provide authorization. A permission selector
filters work; it grants nothing. Enforce the identity-to-permission mapping at
the trusted application boundary. Keep credentials and provider secrets on the
server.

### 4. Visual polish

Only after the real end-to-end journey passes may the UI add imagery, custom
icons, branding, animation, refined typography, decorative styling, and
responsive tuning. Do not generate or source visual assets before this point.
Keep the generated client boundary and the confirmed behavior. Then rerun the
real journey, accessibility checks, and the production build.
