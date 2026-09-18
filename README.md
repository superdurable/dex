# Dex - Durable Execution(D-EX)

> ⚠️ **Pre-launch:** Dex has not formally launched yet. You can use it for testing, but breaking changes may be introduced before the official launch.

**Durable Execution** provides programming model that makes an application's execution durable. This includes local state and control flow such as branches and loops, as well as parallel execution and coordination, waiting for timers or external events, error handling, and remote procedure invocations. The application logic is expressed directly in ordinary code, while the platform reliably restores and resumes the execution after failures and restarts.

**Dex** provides such a structural programming model with only a few concepts as [durable primitives](https://docs.superdurable.io/primitives). You use Dex to write a Flow filled with ordinary code: durable Steps, Attributes, RPCs, and durable conditions using Channels and Timers. Then you run Workers hosting your Flow. The Client calls Dex Server to start and interact with Flow instances. Dex Server dispatches Step and RPC invocation tasks to your Workers.

Unlike replay-based durable execution engines, Dex does not split your logic into deterministic workflow code and separate activities—Worker handlers are ordinary code, and Attribute data lives in a blob store you can sync to databases you already run.

<img width="901" height="719" alt="dex-arch3" src="https://github.com/user-attachments/assets/4b70a5ec-8c94-4f13-acc4-8f7958245bda" />


Learn more: [What is Durable Execution?](https://docs.superdurable.io/intro/what-is-durable-execution) · [Why Dex?](https://docs.superdurable.io/intro/what-is-dex)

AI coding assistants can use the official [Dex Developer skill](https://docs.superdurable.io/build-with-ai/dex-developer-skill) to build, test, and operate Dex applications through Dex's public programming model. Its source is maintained in [superdurable/skill-dex-developer](https://github.com/superdurable/skill-dex-developer).

## Community

Join the [SuperDurable Dex community on Slack](https://join.slack.com/t/superdurableworkspace/shared_invite/zt-4aby5e0b6-6rNT9zN6BzbroHZpGyia0A) to ask questions, share feedback, and connect with other Dex users and contributors.

## Quick start

See [Quick start](https://docs.superdurable.io/quick-start) on docs.superdurable.io.
