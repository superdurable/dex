# Connector

A Connector integrates a Flow with an external provider. It packages typed operations, event ingress, authorization, and optional configuration UI, without putting provider credentials in durable Flow state. Official Connectors are released separately from Dex.

- Connectors support Go, Python, and TypeScript. Java and Rust have none; do not invent a Connector API or loader for them.
- Support is per Connector and per capability. Check the [Connector catalog](https://connectors.superdurable.io/catalog.yaml) for what each language package offers.
- Use only released official Connector modules. Never invent a Connector, an operation, or a generated type.
- To add or change an official Connector, use the dex-connector-contributor skill.

## Capabilities

| Capability | What it does |
| --- | --- |
| Auth | Authorizes a logical connection and keeps credentials outside the Flow. |
| Trigger | Turns a provider event into a typed event that starts a Flow or invokes a typed RPC. |
| Query | Reads provider state from a Connector Step. |
| Mutation | Changes provider state with explicit idempotency and uncertainty handling. |
| UI | Supplies configuration units that Dex Web composes for a Step or Trigger binding. |

## Operations are Connector Steps

An operation-specific factory creates a Connector Step. The application maps the current Step input to the provider input and wires each result branch to a Step.

- The provider call runs in Execute and inherits Dex retry, timeout, durability, and recovery.
- The result holds the operation output, a receipt, the selected branch, and a safe failure classification. It does not carry the previous Step input. Persist business context in an Attribute, or pass it through an application Step first.
- A Mutation uses a stable idempotency key derived from the Step execution.
- Each operation has an effect class. A Query is a read and changes nothing when repeated. A Mutation the Connector marks safe to repeat is an idempotent write: a repeat with the same idempotency key makes no second effect. Every other Mutation is a non-idempotent write and can make a second effect when repeated.
- When a Mutation's outcome is unknown, the Connector selects an uncertainty branch. Wire it to a Step that queries and reconciles. Never resend blindly.
- Only the happy-path branch must be wired. Selecting an unwired optional branch fails the Flow, which keeps the provider outcome visible for recovery.
- Use the operation-specific generated factories and a static connection name on every operation config.

## Triggers

A Trigger is a long-running event source outside the Flow. It delivers each event at least once, with the provider event ID and occurrence time.

- The application supplies the final filter, resolves the Flow ID, and maps the event to typed input.
- It chooses to start a Flow or invoke a typed RPC on an existing Flow. A Trigger binding does not store an RPC name; the application registers the RPC definition the Trigger runner uses.
- A root start uses the provider event ID as its start Request ID and a deterministic Flow ID.
- An RPC target needs its own bounded duplicate handling in application state, plus its own locks.
- Local Trigger runners persist a matched event before acknowledging it and replay pending deliveries after a restart.
- A connection's credentials and each Trigger binding's matchers are separate. One connection can serve several Flows, each with its own binding filters.

## Credentials and connections

Flow definitions refer to a logical connection name, never to credentials.

- Credentials never enter Flow input, Attributes, results, receipts, Streams, logs, browser messages, or generated artifacts.
- Treat Connector secrets as secret values at the application boundary.
- Connection settings are read when the application constructs the named connection. A settings change needs an application restart.
- Credentials are resolved before each provider call. Reauthorization needs no restart.

### Local Connector credentials

In local development, Dex Web inside `dexcli dev` can configure exact published official Connector modules. See the [Dex Web local Connector setup reference](https://docs.superdurable.io/reference/web/local-connector-setup) for which connections it can configure.

- For a Flow's connection, the Flow must use operation-specific factories, a static connection name, and static Trigger binding names. Generic factories, local replacements, unpublished versions, or version conflicts stay visible but are not configurable.
- Dex Web writes a plaintext development store, `connections.json`, under the user's Dex directory or the directory passed to `dexcli dev`. The file and directory permissions are restrictive and Dex rejects wider ones.
- The application reads it through its Connector SDK's project configuration loader, pointed at the file by an environment variable. Run one application per directory.
- Never write your own reader for `connections.json`. If the installed Connector SDK cannot read it, report that as a compatibility blocker.
- Deleting a stored credential does not revoke the provider grant.
- Never commit the file, copy it into an image or a deployed volume, or use it as a production secret store.

### Deployed Connector configuration

Dex Server and Dex Web provide no deployed Connector configuration, OAuth flow, or credential store. Dex Web's Connector setup runs only in loopback `dexcli dev`.

- A deployed application loads its connections with the Connector SDK's project configuration loader at startup. The loader reads a secret-free configuration document from storage that a hosting platform or operator writes. The application never writes it.
- Each generated project connection decodes its settings once and resolves credentials on every call.
- Decide with the user who writes the configuration, where it lives, and how the application reaches it, before handoff.
- Prove with the real application startup that every connection resolves its settings and credentials.
- The exact loader names and environment variables are in the Connector SDK documentation in the [Dex Connectors Library](https://github.com/superdurable/dex-connectors-library).

## Self-check

- Is every Connector a released official module, used through its operation-specific factory?
- Is the uncertainty branch of every Mutation wired to reconciliation?
- Does business context survive the Connector Step through an Attribute or an application Step?
- Does every Trigger start use a deterministic Flow ID and the provider event ID as Request ID?
- Are credentials absent from Flow state, logs, and responses?
