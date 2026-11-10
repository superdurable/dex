import DexExpressiveness.Dex.Syntax

/-!
# Dex core: configurations (spec §3)

One Flow, one partition. The admissible environment starts exactly one Flow
(spec §5.3) and the translation uses no SubFlows, so the system `Σ` of spec
§3.4 reduces to one clock, at most one run, and the tasks handed to Workers.
-/

namespace DexExpressiveness.Dex

variable (σ : Signature)

/-- The phase `φ` of a Step execution (spec §3.1). -/
inductive Phase
  /-- `WF`: WaitFor is pending or running. -/
  | waitFor
  /-- `WT`: the wait is registered and not satisfied. -/
  | waiting
  /-- `EX`: Execute is pending or running. -/
  | execute
  /-- `CO`: completed. -/
  | completed
  /-- `CA`: canceled. -/
  | canceled
  deriving DecidableEq, Repr

/-- Whether a Step is active: `φ ∈ {WF, WT, EX}` (spec §3.1). -/
def Phase.isActive : Phase → Bool
  | .waitFor | .waiting | .execute => true
  | .completed | .canceled => false

/-- `ip : P | R` of an invocation (spec §3.1). A running invocation names the
task that runs it, so a Complete rule can check "the same task" (spec §4.4). -/
inductive InvocationState
  /-- `P`: pending, waiting for [STEP-START]. -/
  | pending
  /-- `R`: running as the task with this ID. -/
  | running (taskId : Nat)
  deriving DecidableEq, Repr

/-- The current invocation `Inv` of a Step (spec §3.1), without lock keys and
the pending sequence number, which only Attribute locks read. -/
structure Invocation where
  /-- The method `m`. -/
  method : Method
  /-- Pending or running. -/
  state : InvocationState
  deriving DecidableEq, Repr

/-- The registered wait `WaitState` of a Step (spec §3.1), without child IDs. -/
structure WaitState where
  /-- The wait `W`. -/
  wait : Wait σ
  /-- `fireAt : [t]`, the fire time of each timer condition, in microseconds. -/
  fireAt : List Nat
  /-- `tdone : [Bool]`, which timer conditions have fired. -/
  timersDone : List Bool
  /-- `sws`: "the `seq` at which the wait was registered. It orders waiters." -/
  registeredSeq : Nat

/-- A Step execution `StepRec` (spec §3.1), without locals. -/
structure StepRecord where
  /-- `e = T-n`. -/
  id : StepExecId σ
  /-- `in : Val?`. -/
  input : Option σ.Val
  /-- `o : Opts`. -/
  options : StepOptions σ
  /-- `from`. -/
  origin : Origin σ
  /-- `φ`. -/
  phase : Phase
  /-- `inv : Inv?`. -/
  invocation : Option Invocation
  /-- `w : WaitState?`. -/
  waitState : Option (WaitState σ)
  /-- `cr : CR?`. -/
  conditionResults : Option (ConditionResults σ)

/-- The status of a closed run that the restricted model can reach (spec §3.2:
`status ∈ {Completed, Failed, Canceled, Terminated}`). Canceled and Terminated
need StopFlow or a Flow timeout, which spec §5.3 excludes. -/
inductive CloseStatus
  /-- `Completed`. -/
  | completed
  /-- `Failed(STEP_DECISION_FAILING_FLOW, s)`: the only failure the model
  reaches (spec §4.2 `decide`, case `FF(s)`). -/
  | failed (message : String)
  deriving DecidableEq, Repr

/-- `st : Running | Closed(status)` (spec §3.2). -/
inductive RunStatus
  /-- `Running`. -/
  | running
  /-- `Closed(status)`. -/
  | closed (status : CloseStatus)
  deriving DecidableEq, Repr

/-- A wait timer ID `wait(e, j)` (spec §4.2 `register`, §4.5). -/
structure TimerId where
  /-- The waiting Step execution `e`. -/
  step : StepExecId σ
  /-- The index `j` of the timer condition in its wait. -/
  index : Nat
  deriving DecidableEq

/-- One run `ρ` (spec §3.2), without the pending close `pc`, the graceful
completion flag `gc`, the claimed counts `K`, the Step locks `L`, the child
results `SFR`, and the outbox `Ob`. None of them changes under the restricted
rules. A Channel message is its value: message IDs and enqueue sequence numbers
serve only deletions and ordering, and `Q[c]` is kept in commit order. -/
structure Run where
  /-- `st`. -/
  status : RunStatus
  /-- `outs : [(T, e, Val)]`, the collected Flow outputs; `e` contains `T`. -/
  outputs : List (StepExecId σ × σ.Val)
  /-- `S : StepExecId ⇀ StepRec`, in creation order. -/
  steps : List (StepRecord σ)
  /-- `cnt : StepType → ℕ`; an absent type counts 0. -/
  counters : AssocList σ.StepType Nat
  /-- `A : Key ⇀ Val`. -/
  attributes : AssocList σ.Key σ.Val
  /-- `Q : Chan → [Msg]`, pending messages in commit order; an absent Channel is empty. -/
  channels : AssocList σ.Chan (List σ.Val)
  /-- `Tm : TimerId ⇀ t`, armed timers and their fire times in microseconds. -/
  timers : AssocList (TimerId σ) Nat
  /-- `seq`: the sequence number of the last committed command. -/
  seq : Nat

/-- A handler response held by a task. -/
inductive Response
  /-- A WaitFor response. -/
  | waitFor (response : WaitForResponse σ)
  /-- An Execute response. -/
  | execute (response : ExecuteResponse σ)

/-- `phase` of a task (spec §3.3). -/
inductive TaskPhase
  /-- `Started`: handed to a Worker, the handler has not run. -/
  | started
  /-- `Running(Fx*, Resp)`: the handler ran; these effects are still to be emitted. -/
  | running (remainingEffects : List σ.Fx) (response : Response σ)
  /-- `Returned(Resp)`: every effect was emitted; the response awaits its commit. -/
  | returned (response : Response σ)

/-- An attempt handed to a Worker, `Task` in spec §3.3. It holds the request
the engine builds at [STEP-START]: context, input, snapshot, condition results. -/
structure Task where
  /-- The task ID, fresh at [STEP-START]. -/
  id : Nat
  /-- The Step execution `e`. -/
  step : StepExecId σ
  /-- The method `m`. -/
  method : Method
  /-- The handler context. -/
  context : Context σ
  /-- `S[e].in` at [STEP-START]. -/
  input : Option σ.Val
  /-- `snapshot(e, m)` at [STEP-START]. -/
  snapshot : Snapshot σ
  /-- `S[e].cr` at [STEP-START]. -/
  conditionResults : Option (ConditionResults σ)
  /-- The task phase. -/
  phase : TaskPhase σ

/-- The system `Σ` (spec §3.4) for one Flow in one partition, with the engine
state `Θ` of spec §3.3. -/
structure Config where
  /-- `t_p`: the partition clock in microseconds. -/
  clock : Nat
  /-- The timestamp of the last commit, or `none` before the first. A commit
  needs `clock` strictly above it (spec §4.1: "`t_{p(f)}` strictly increases
  between two commits of partition `p(f)`"). -/
  lastCommitAt : Option Nat
  /-- `R(f)`: the run of the root Flow, or `none` before [FLOW-START]. -/
  run : Option (Run σ)
  /-- `Θ`: the tasks handed to Workers. -/
  tasks : List (Task σ)
  /-- The next fresh task ID. -/
  nextTaskId : Nat

/-- The initial system `Σ₀` with `R = ∅` (spec §5.2). -/
def Config.initial : Config σ :=
  { clock := 0, lastCommitAt := none, run := none, tasks := [], nextTaskId := 0 }

/-- The empty run with `st = Running` (spec §4.3, `ρ₀` before its start Step). -/
def Run.empty : Run σ :=
  { status := .running, outputs := [], steps := [], counters := [], attributes := [],
    channels := [], timers := [], seq := 0 }

end DexExpressiveness.Dex
