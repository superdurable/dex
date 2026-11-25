# The application's own UI

Load this reference when the user wants their own UI, which most products
need. Discovery asks once, as in
[product discovery](product-discovery.md#ui-question). The user's UI needs no
Dex Web gap to justify it. The UI calls the application API that the backend
stage designs. It never calls Dex directly.

When the user also uses Dex Web for operations and management, leave its
Runs, Work Queue, Actions, edits, timeline, and graph to it. Build those in the
user's UI only when the user asks for them. [Dex Web](dex-web-v2.md) has its
contracts.

When the user wants no UI of their own, the application ships the Worker and
the application API its callers need. Do not add UI scaffolding then.

## Build the UI

### 1. Resolve material interaction choices

A clear implementation request already authorizes its stated pages, fields,
and behavior. Implement those directly. Use the optional static checkpoint
below only when a consequential interaction choice is still open. Ask only
about that choice.

Agree only on the information architecture the backend design needs:

- role-specific entry pages and ordinary link navigation;
- the list, detail, create, action, and completion pages;
- labels, inputs, buttons, and placeholder regions on each page;
- when the user also uses Dex Web, which of its surfaces maintainers keep.

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

Design the Flow, any Connector capabilities, and the application HTTP API
contract together, with the rules in
[backend implementation](backend-implementation.md#application-api-contract).
Map every approved UI action to an operation in that contract. Generate the
TypeScript client from the contract with the server interfaces. The browser
later uses only that generated client.

Implement and verify the Flows, and any Connectors the user chose, before you
return to dynamic frontend work. A missing capability of a chosen public
Connector follows the Connector Contributor workflow, with its authorization
and release requirements.

### 3. Integration and durable verification

Implement the backend boundary and the UI against the same generated contract.
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
