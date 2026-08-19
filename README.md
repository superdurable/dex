# Dex - Durable Execution(D-EX)

**Durable Execution** is an abstraction of the design patterns every reliable backend needs: multi-step orchestration, durable timers, retries, human-in-the-loop coordination, and production observability. Traditional databases and storage only persist data with passive read/write APIs—they do not give you atomic step transitions, worker dispatch, or failure handling out of the box. Engineers end up rebuilding the same queue-and-poll infrastructure on every project.

<img width="676" height="607" alt="arch" src="https://github.com/user-attachments/assets/720e38a8-b151-4251-aa8a-5b62ae64a7f4" />

**Dex** provides structural programming model with only a few concepts as [durable primitives](https://docs.superdurable.io/primitives). You use Dex to write a Flow filled with ordinary code: durable Steps, Attributes, RPCs, and durable conditions using Channels and Timers. Then you run Workers hosting your Flow. The Client calls Dex Server to start and interact with Flow instances. Dex Server dispatches Step and RPC invocation tasks to your Workers.

Unlike replay-based durable execution engines, Dex does not split your logic into deterministic workflow code and separate activities—Worker handlers are ordinary code, and Attribute data lives in a blob store you can sync to databases you already run.
<img width="913" height="414" alt="dex-arch" src="https://github.com/user-attachments/assets/8613f6d7-810e-4ef3-a614-cf0a93eb72be" />

Learn more: [What is Durable Execution?](https://docs.superdurable.io/intro/what-is-durable-execution) · [Why Dex?](https://docs.superdurable.io/intro/what-is-dex)


## Quick start

See [Quick start](https://docs.superdurable.io/quick-start) on docs.superdurable.io.

If Dex is useful to you, please [star us on GitHub](https://github.com/superdurable/dex) to follow releases.
