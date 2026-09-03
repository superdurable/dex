# Dex - Durable Execution(D-EX)

**Durable Execution** provides programming model that makes an application's execution durable. This includes local state and control flow such as branches and loops, as well as parallel execution and coordination, waiting for timers or external events, error handling, and remote procedure invocations. The application logic is expressed directly in ordinary code, while the platform reliably restores and resumes the execution after failures and restarts.

**Dex** provides such a structural programming model with only a few concepts as [durable primitives](https://docs.superdurable.io/primitives). You use Dex to write a Flow filled with ordinary code: durable Steps, Attributes, RPCs, and durable conditions using Channels and Timers. Then you run Workers hosting your Flow. The Client calls Dex Server to start and interact with Flow instances. Dex Server dispatches Step and RPC invocation tasks to your Workers.

Unlike replay-based durable execution engines, Dex does not split your logic into deterministic workflow code and separate activities—Worker handlers are ordinary code, and Attribute data lives in a blob store you can sync to databases you already run.

<img width="921" height="664" alt="dex-arch2" src="https://github.com/user-attachments/assets/fe91af8a-58e7-4688-9f01-057a25db7bc4" />


Learn more: [What is Durable Execution?](https://docs.superdurable.io/intro/what-is-durable-execution) · [Why Dex?](https://docs.superdurable.io/intro/what-is-dex)

AI coding assistants can use the repository's [Dex Developer skill](skills/dex-developer/SKILL.md) to build, test, and operate Dex applications through Dex's public programming model.

## Quick start

See [Quick start](https://docs.superdurable.io/quick-start) on docs.superdurable.io.
