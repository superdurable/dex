---
section: Added
---
- **Client.getFlowState** reads the open Step executions of the current or latest Flow run and returns a **FlowState**. Each **ActiveStepExecution** reports its Step type, execution number, phase, the open WaitFor or Execute call as a **StepMethodInvocation** (pending, running, or retry backoff, with its attempt), and the latest failed attempt as a **StepMethodFailureInfo** (attempt, error type, message, and stack trace). Use it to tell a Flow that is still running from one whose active Step is failing and retrying. [Reference](/reference/typescript/client#api-names).
