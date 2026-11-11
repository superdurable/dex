import DexExpressiveness.Dex.Observation

/-!
# DexF: configurations under failures (spec §11.2)

DexF is "the system of Sections 3 and 4 plus the state of Section 11.2 and
the rules of Section 11.3" (spec §11). This file is the state. It keeps the
apply state of the failure-free model unchanged, as a `Dex.Run`, so that
every meta-function of spec §4.2 applies to it verbatim, and adds:

- availability `up` (`Config.isUp`);
- per invocation attempt state: Backoff, the attempt number, and the
  retained snapshot (`Attempt`);
- retry timers (`Run.retryTimers`);
- zombie tasks `Z` (`Config.zombies`) and interrupted attempts `I`
  (`Config.interrupts`);
- the ghost history variables `h`, `v`, `par`, `ps₀`, `n`, `ζ` (`Ghost`).

Retention timers, RunPurge, and the ghost `Rc` are left out; see
`Failure/Rules.lean` and the README section "Failure transparency (issue
1514), Stage 1".

The definitions of this file are fixed for Stage 2.
-/

namespace DexExpressiveness.DexF

variable (σ : Dex.Signature)

/-- An invocation, named by its Step execution and method (spec §11.2:
"`h : (e, m) ⇀ ℕ`"). "Under H2 each pair `(e, m)` has at most one
invocation in its life, so they need no reset." -/
abbrev InvocationKey := Dex.StepExecId σ × Dex.Method

/-- The cause the last failure of an invocation recorded: the
`recoveryError` part of `ctxA` (spec §11.1 H3,
`method_request.rs:464-479`). -/
inductive FailureCause
  /-- [ATTEMPT-LOST] of a started attempt. -/
  | attemptLost
  /-- [ATTEMPT-LOST] before any start of the attempt ("a before-start failure"). -/
  | beforeStart
  /-- [RECOVER-INTERRUPT], the kind `STARTED_ATTEMPT_INTERRUPTED`. -/
  | interrupted
  deriving DecidableEq, Repr

/-- The attempt-varying fields of the handler context, `ctxA = ⟨attempt,
firstAttemptTimestamp, recoveryError, lastHeartbeatValue⟩` (spec §11.1 H3).
The failure-free `Dex.Context` has none of them. DexF hands them to user
code through `Program`, and H3 (`IgnoresAttemptContext`) says user code
ignores them. -/
structure AttemptContext where
  /-- `attempt`: the attempt number, starting at 1. -/
  attempt : Nat
  /-- `firstAttemptTimestamp`: the stamp of the invocation's first start. -/
  firstAttemptTimestamp : Nat
  /-- `recoveryError`: the cause of the last failure, if any. -/
  recoveryError : Option FailureCause
  /-- `lastHeartbeatValue`: the last checkpoint that [ATTEMPT-HEARTBEAT] stored. -/
  lastHeartbeatValue : Option σ.Val

/-- The context of a first attempt: attempt 1, no failure, no heartbeat.
Every flow that DexF compares with Dex is the program at this context
(`Program.failureFreeFlow`). -/
def AttemptContext.first : AttemptContext σ :=
  { attempt := 1, firstAttemptTimestamp := 0, recoveryError := none, lastHeartbeatValue := none }

/-- A DexF program: a Flow definition for each attempt context. "The Step
method functions `U` receive a context `Ctx` that includes the
attempt-varying fields `ctxA`" (spec §11.1 H3). A failure-free program is a
constant function (`Program.ofFlow`).

Lean decision: the spec keeps H3 as an explicit hypothesis rather than a
silent restriction (plan §4.1). `Dex.Context` has no attempt-varying field,
so a plain `Dex.FlowDef` would make H3 hold by construction and leave no room
for a counterexample. The program is therefore parameterized by `ctxA`. -/
abbrev Program := AttemptContext σ → Dex.FlowDef σ

variable {σ} in
/-- The Flow definition that DexF is compared with: the program at the first
attempt context. Under H3 every context gives this Flow definition. -/
def Program.failureFreeFlow (program : Program σ) : Dex.FlowDef σ :=
  program (AttemptContext.first σ)

variable {σ} in
/-- The DexF program of a failure-free Flow definition: it ignores `ctxA`. -/
def Program.ofFlow (flow : Dex.FlowDef σ) : Program σ :=
  fun _ => flow

/-- The attempt state of the current invocation of a Step: the fields `ip`,
`a`, and `snap` of `Inv` (spec §11.2) that the failure-free `Dex.Invocation`
lacks, and the inputs of `ctxA`.

"`ip : P | R | B`": `R` is `Dex.InvocationState.running` in the Step record;
`P` and `B` are both `Dex.InvocationState.pending` there, and `isBackoff`
tells them apart. Lean decision: one invocation state lives in one place, so
the Step record keeps `P | R` exactly as in the failure-free model. -/
structure Attempt where
  /-- The method `m`; it equals the method of the Step record's invocation. -/
  method : Dex.Method
  /-- `ip = B`: the attempt failed and its retry timer is armed. -/
  isBackoff : Bool
  /-- `a : ℕ⁺`, the attempt number. "In Backoff it is already the next
  attempt's number." -/
  number : Nat
  /-- `snap : Snap?`. Present while `ip = R`, and "may be present while
  `ip = P` or `ip = B`. That is the **retained snapshot** of a retry." -/
  snapshot : Option (Dex.Snapshot σ)
  /-- The stamp of the invocation's first start, for `ctxA`. -/
  firstAttemptTimestamp : Option Nat
  /-- The cause of the last failure, for `ctxA`. -/
  lastFailure : Option FailureCause
  /-- The checkpoint stored by [ATTEMPT-HEARTBEAT] (`step_results.rs:211-256`), for `ctxA`. -/
  lastHeartbeatValue : Option σ.Val

variable {σ} in
/-- The attempt state of a newly pended invocation: Pending, attempt 1, no
snapshot. -/
def Attempt.fresh (method : Dex.Method) : Attempt σ :=
  { method := method, isBackoff := false, number := 1, snapshot := none,
    firstAttemptTimestamp := none, lastFailure := none, lastHeartbeatValue := none }

/-- A retry timer `retry(e, m, a)` (spec §11.2: "`Tm` also holds retry timers
`retry(e, m, a)`, armed when attempt `a − 1` fails"). -/
structure RetryTimerId where
  /-- The Step execution `e`. -/
  step : Dex.StepExecId σ
  /-- The method `m`. -/
  method : Dex.Method
  /-- The attempt number `a` that the timer starts. -/
  attempt : Nat
  deriving DecidableEq

/-- One run under failures: the apply state `R(f)` of spec §11.2.

- `base` is the failure-free run of spec §3.2, unchanged in shape. Its
  `seq` is the real run `seq`, which failure commands also take.
- `attempts` holds the attempt state of each Step's current invocation.
- `retryTimers` holds the retry timers of `Tm`; the wait timers stay in
  `base.timers`.
- `startRequestId` is the request ID that the Flow directory recorded for the
  start (`flow_start.rs:91-125`). -/
structure Run where
  /-- The failure-free part of the apply state. -/
  base : Dex.Run σ
  /-- The attempt state of the current invocation of each Step, by Step execution. -/
  attempts : AssocList (Dex.StepExecId σ) (Attempt σ)
  /-- The armed retry timers and their fire times in microseconds. -/
  retryTimers : AssocList (RetryTimerId σ) Nat
  /-- The request ID of the StartFlow request that started the run. -/
  startRequestId : Nat

/-- A task: an attempt handed to a Worker (spec §11.2 `Task`). `base` holds
the fields of the failure-free task (`f`, `e`, `m`, `tid`, `input`, `snap`,
`cr`, and the phase). Locals are not modeled, as in the failure-free model. -/
structure Task where
  /-- The failure-free task: ID, Step execution, method, context, input,
  snapshot, condition results, phase. -/
  base : Dex.Task σ
  /-- `ctxA`; its `attempt` is the task's attempt number `a`. -/
  attemptContext : AttemptContext σ
  /-- "`j`: effects emitted so far (ghost)". -/
  emittedCount : Nat

/-- An interrupted attempt `(e, m, a, tid)` of `I` (spec §11.2: "`I` : set of
`(e, m, a, tid)`, interrupted attempts, engine memory"). -/
structure Interrupt where
  /-- The Step execution `e`. -/
  step : Dex.StepExecId σ
  /-- The method `m`. -/
  method : Dex.Method
  /-- The attempt number `a`. -/
  attempt : Nat
  /-- The task ID of the running attempt. -/
  taskId : Nat
  deriving DecidableEq

/-- "`v : (e, m) ⇀ NotRun | Ran(Fx*, Resp) | Returned(Resp)` -- ghost:
furthest phase any attempt reached" (spec §11.2). -/
inductive VirtualProgress
  /-- `NotRun`: no attempt has run the handler. -/
  | notRun
  /-- `Ran(fx⃗, resp)`: some attempt ran the handler and computed this. -/
  | ran (effects : List σ.Fx) (response : Dex.Response σ)
  /-- `Returned(resp)`: some attempt returned. -/
  | returned (response : Dex.Response σ)

variable {σ} in
/-- Whether the progress is `NotRun`. -/
def VirtualProgress.isNotRun : VirtualProgress σ → Bool
  | .notRun => true
  | _ => false

variable {σ} in
/-- Whether the progress is `Returned`. -/
def VirtualProgress.isReturned : VirtualProgress σ → Bool
  | .returned _ => true
  | _ => false

/-- "`par : (e, m) ⇀ ⟨input, snap, locals, cr⟩` -- ghost: the arguments of
`U` at the first start" (spec §11.2), without locals.

Lean decision: it also keeps the handler context, which is the same for
every attempt, and the task ID of the virtual task. The failure-free
`Dex.Invocation` names the task that runs it, so α must give the virtual
task an ID; it is the ID that the corresponding Dex [STEP-START] mints. -/
structure Arguments where
  /-- The handler context. -/
  context : Dex.Context σ
  /-- `S[e].in` at the first start. -/
  input : Option σ.Val
  /-- The snapshot of the first start. -/
  snapshot : Dex.Snapshot σ
  /-- `S[e].cr` at the first start. -/
  conditionResults : Option (Dex.ConditionResults σ)
  /-- The ID of the virtual task in α. -/
  virtualTaskId : Nat

/-- How an invocation ended. Lean decision: spec §11.6 tells "an invocation
that a cancel or a close ended" from "an invocation whose result committed",
and α needs the difference, but the configuration alone does not keep it
after `finish` clears the invocation. This ghost keeps it. -/
inductive InvocationOutcome
  /-- The invocation is the current invocation of its Step. -/
  | live
  /-- A Complete rule committed its result. -/
  | committed
  /-- A cancel or a close ended it. -/
  | canceled
  deriving DecidableEq, Repr

/-- The ghost history of one invocation `(e, m)` (spec §11.2). -/
structure InvocationHistory where
  /-- `ps₀(e, m) := ζ(s)`, "`s` the run seq of the invocation's first pend".
  α does not read it: the Lean `Dex.Invocation` has no `ps` (spec §11.6: "In
  the fragment, `ps` of an invocation orders only lock queues ... So α may
  also ignore it there"). -/
  pendedAt : Nat
  /-- `h(e, m)`: "largest effect index any attempt emitted". -/
  emittedCount : Nat
  /-- `v(e, m)`. -/
  progress : VirtualProgress σ
  /-- `par(e, m)`, set by the first [STEP-START] that computes a snapshot. -/
  arguments : Option (Arguments σ)
  /-- Whether the invocation is current, committed, or canceled. -/
  outcome : InvocationOutcome

variable {σ} in
/-- The history of an invocation at its first pend, by the normal command
with normal index `pendedAt`. -/
def InvocationHistory.fresh (pendedAt : Nat) : InvocationHistory σ :=
  { pendedAt := pendedAt, emittedCount := 0, progress := .notRun, arguments := none,
    outcome := .live }

/-- The ghost history variables of `Σ_F` (spec §11.2). "No rule reads them;
only α does." -/
structure Ghost where
  /-- `h`, `v`, `ps₀`, `par`, and the outcome, per invocation. -/
  invocations : AssocList (InvocationKey σ) (InvocationHistory σ)
  /-- `n`: "number of applied normal commands of the run". -/
  normalCount : Nat
  /-- `ζ`: "real seq of each applied normal command ↦ its index". -/
  normalIndex : AssocList Nat Nat
  /-- The stamp of the last normal commit. Lean decision: spec §11.6 says "α
  keeps the stamp of every normal commit"; this is that stamp. -/
  lastNormalCommitAt : Option Nat
  /-- The next virtual task ID: the number of first starts so far. -/
  nextVirtualTaskId : Nat

/-- The ghost of the initial configuration. -/
def Ghost.initial : Ghost σ :=
  { invocations := [], normalCount := 0, normalIndex := [], lastNormalCommitAt := none,
    nextVirtualTaskId := 0 }

/-- The system `Σ_F = ⟨τc, P, R, η, N, Z, I, h, v, ps₀, n, ζ, Rc, par, up⟩`
(spec §11.2) for one Flow in one partition. `Rc` is left out with RunPurge.
The engine memory `η` that DexF keeps is `Θ` (`tasks`) and `I`
(`interrupts`). -/
structure Config where
  /-- `t_p`: the partition clock in microseconds. -/
  clock : Nat
  /-- The stamp of the last log commit, or `none` before the first. Every log
  commit, applied or not, needs `clock` above it (spec §11.3: "the stamp
  advances the partition clock, whether apply accepts the command or not"). -/
  lastCommitAt : Option Nat
  /-- `R(f)`: the run of the root Flow, or `none` before [FLOW-START]. -/
  run : Option (Run σ)
  /-- `Θ`: the tasks the serving engine handed to Workers. -/
  tasks : List (Task σ)
  /-- `Z`: "zombie tasks: Workers the server gave up". -/
  zombies : List (Task σ)
  /-- The next fresh task ID. "`tid` is a fresh task ID that the engine minted." -/
  nextTaskId : Nat
  /-- `I`: interrupted attempts, rebuilt by [RECOVER]. -/
  interrupts : List (Interrupt σ)
  /-- `up`: "the server is serving". -/
  isUp : Bool
  /-- The ghost history variables. -/
  ghost : Ghost σ

/-- The initial configuration: no run, the server up, nothing in flight. -/
def Config.initial : Config σ :=
  { clock := 0, lastCommitAt := none, run := none, tasks := [], zombies := [], nextTaskId := 0,
    interrupts := [], isUp := true, ghost := Ghost.initial σ }

variable {σ}

/-- The real tasks: `Θ` followed by `Z`. -/
def Config.realTasks (config : Config σ) : List (Task σ) :=
  config.tasks ++ config.zombies

/-- The invocation a task runs. -/
def Task.key (task : Task σ) : InvocationKey σ :=
  (task.base.step, task.base.method)

/-- The history of an invocation, if it was ever pended. -/
def Ghost.findInvocation? (ghost : Ghost σ) (key : InvocationKey σ) :
    Option (InvocationHistory σ) :=
  ghost.invocations.get? key

/-- `h(e, m)`, `0` for an invocation never pended. -/
def Ghost.emittedCountOf (ghost : Ghost σ) (key : InvocationKey σ) : Nat :=
  ((ghost.findInvocation? key).map (fun history => history.emittedCount)).getD 0

/-- `par(e, m)`, if set. -/
def Ghost.argumentsOf (ghost : Ghost σ) (key : InvocationKey σ) : Option (Arguments σ) :=
  (ghost.findInvocation? key).bind (fun history => history.arguments)

/-- Change the history of an invocation that has one. -/
def Ghost.updateInvocation (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) : Ghost σ :=
  match ghost.findInvocation? key with
  | none => ghost
  | some history => { ghost with invocations := ghost.invocations.set key (change history) }

/-- `ζ(s)`, `0` outside its domain. -/
def Ghost.normalIndexOf (ghost : Ghost σ) (seq : Nat) : Nat :=
  (ghost.normalIndex.get? seq).getD 0

end DexExpressiveness.DexF
