# Dex - Durable Execution(D-EX), Dead Simple. More Power.

**Durable Execution** is an abstraction of the design patterns every reliable backend needs: multi-step orchestration, durable timers, retries, human-in-the-loop coordination, and production observability. Traditional databases and storage only persist data with passive read/write APIs—they do not give you atomic step transitions, worker dispatch, or failure handling out of the box. Engineers end up rebuilding the same queue-and-poll infrastructure on every project.

**Dex** is a Durable Execution platform built on a small set of [durable primitives](https://docs.superdurable.io/primitives)—Steps, Attributes, Channels, Timers, and RPCs. You write a Flow in ordinary code, run Workers that host it, and use the Client to start and interact with Flow instances. Dex Server dispatches Step and RPC work to your Workers. Step transitions are atomic; waits and timers survive restarts; retries and compensation are declared on the Step. Unlike replay-based workflow engines, Dex does not split your logic into deterministic workflow code and separate activities—Worker handlers are ordinary code, and Attribute data lives in a blob store you can sync to databases you already run.

Learn more: [What is Durable Execution?](https://docs.superdurable.io/intro/what-is-durable-execution) · [Why Dex?](https://docs.superdurable.io/intro/what-is-dex)

<img width="676" height="607" alt="arch" src="https://github.com/user-attachments/assets/720e38a8-b151-4251-aa8a-5b62ae64a7f4" />

## Quick start

See [Quick start](https://docs.superdurable.io/quick-start) on docs.superdurable.io.
