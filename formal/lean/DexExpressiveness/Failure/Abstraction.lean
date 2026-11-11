import DexExpressiveness.Failure.Rules

/-!
# The abstraction map α (spec §11.6)

"`α` maps a DexF state to a Dex state. The proof shows that every failure
transition maps to a stutter, every other transition maps to a Dex
transition with the same events, and the first occurrence of each `fx`
index maps to a Dex [WORKER-EFFECT]."

α is a function of the configuration. "What α forgets": attempt numbers,
task IDs, heartbeat checkpoints, last failures, and retry bookkeeping; every
retry timer; `Z`, `I`; `up`; and the ghosts, after it has used them.

The definitions of this file are fixed for Stage 2.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- The virtual task's handler call, as the failure-free model makes it:
`(fx⃗(e, m), resp) := U(par(e, m))` at the failure-free flow (spec §11.4:
"Every attempt of `(e, m)` computes the same effect list `fx⃗(e, m)` and the
same response"). -/
def Arguments.toTask (arguments : Arguments σ) (key : InvocationKey σ)
    (phase : Dex.TaskPhase σ) : Dex.Task σ :=
  { id := arguments.virtualTaskId, step := key.1, method := key.2, context := arguments.context,
    input := arguments.input, snapshot := arguments.snapshot,
    conditionResults := arguments.conditionResults, phase := phase }

/-- `fx⃗(e, m)` and the response that every attempt of `(e, m)` computes,
under H3. -/
def Arguments.compute (flow : Dex.FlowDef σ) (key : InvocationKey σ) (arguments : Arguments σ) :
    List σ.Fx × Dex.Response σ :=
  (arguments.toTask key .started).compute flow

/-- The phase of the virtual task, from the ghosts only (spec §11.6):

- "`v(e, m) = NotRun`: phase `Started`";
- "`v(e, m) = Ran(fx⃗, resp)`: phase `Running(fx⃗[h(e, m)+1..], resp)`, having
  emitted `fx⃗[1..h(e, m)]`";
- "`v(e, m) = Returned(resp)`: phase `Returned(resp)`". -/
def InvocationHistory.virtualPhase (history : InvocationHistory σ) : Dex.TaskPhase σ :=
  match history.progress with
  | .notRun => .started
  | .ran effects response => .running (effects.drop history.emittedCount) response
  | .returned response => .returned response

/-- Whether α keeps a virtual task for an invocation (spec §11.6):

- a current invocation that holds a snapshot ("`ip = R`, or `ip = B` or
  `ip = P` with a retained snapshot") has one; it has started, so `par` is
  set, and conversely a current invocation with `par` holds its snapshot
  (invariant I4);
- "An invocation that a cancel or a close ended, while some real task of
  `(e, m)` is still in `Θ` or `Z`" keeps one;
- "An invocation whose result committed" has none. -/
def Config.hasVirtualTask (config : Config σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) : Bool :=
  history.arguments.isSome &&
    match history.outcome with
    | .live => true
    | .committed => false
    | .canceled => config.realTasks.any (fun task => decide (task.key = key))

/-- The virtual tasks of α, in ID order, which is the order in which the
failure-free model appends tasks to `Θ`. -/
def Config.virtualTasks (config : Config σ) : List (Dex.Task σ) :=
  sortStable (fun first second => decide (first.id ≤ second.id))
    (config.ghost.invocations.filterMap (fun entry =>
      match entry.2.arguments with
      | some arguments =>
        if config.hasVirtualTask entry.1 entry.2 then
          some (arguments.toTask entry.1 entry.2.virtualPhase)
        else none
      | none => none))

/-- The invocation of a Step under α (spec §11.6, "Invocations"): "α decides
by whether the invocation holds a snapshot, not by its phase." With a
snapshot it is running as its virtual task; without one it is pending. -/
def Run.abstractInvocation (run : Run σ) (ghost : Ghost σ) (record : Dex.StepRecord σ) :
    Option Dex.Invocation :=
  match record.invocation with
  | none => none
  | some invocation =>
    let holdsSnapshot :=
      match run.findAttempt? record.id with
      | some attempt => attempt.snapshot.isSome
      | none => false
    match holdsSnapshot, ghost.argumentsOf (record.id, invocation.method) with
    | true, some arguments =>
      some { method := invocation.method, state := .running arguments.virtualTaskId }
    | _, _ => some { method := invocation.method, state := .pending }

/-- The apply state under α (spec §11.6, "`seq`-derived values"): the run's
`seq` is `n`, every wait's `sws` is relabeled by `ζ`, and every invocation is
abstracted. The fields that only failures write (attempt states, retry
timers, the start request ID) are forgotten. Every other field is equal. The
enqueue sequence numbers, `ps`, and the created `seq` of spec §11.6 have no
counterpart in the Lean model. -/
def Run.abstract (ghost : Ghost σ) (run : Run σ) : Dex.Run σ :=
  { run.base with
    seq := ghost.normalCount
    steps := run.base.steps.map (fun record =>
      { record with
        invocation := run.abstractInvocation ghost record
        waitState := record.waitState.map (fun waitState =>
          { waitState with registeredSeq := ghost.normalIndexOf waitState.registeredSeq }) }) }

/-- α : DexF.Config → Dex.Config (spec §11.6). The clock is kept; the last
commit stamp is the stamp of the last normal commit ("α keeps the stamp of
every normal commit. Failure commits use stamps between them, and Dex covers
that time with [TICK]"); `Θ` is the list of virtual tasks; the next task ID
is the next virtual task ID. -/
def abstractConfig (config : Config σ) : Dex.Config σ :=
  { clock := config.clock
    lastCommitAt := config.ghost.lastNormalCommitAt
    run := config.run.map (Run.abstract config.ghost)
    tasks := config.virtualTasks
    nextTaskId := config.ghost.nextVirtualTaskId }

end DexExpressiveness.DexF
