import DexExpressiveness.Invariants
import DexExpressiveness.TraceSets

/-!
# The Stage 2 proof plan: simulation relation and key lemmas

`Theorems.lean` proves the two statements from four inclusions. The two of
safety are proved in `FiniteTraces.lean` from the step simulations of this
file; the two of liveness are at the end of this file. This file states the lemmas the proofs are expected to use,
each ending in `sorry`, so that the plan can be reviewed with the statements. The README lists every
`sorry`.

The plan is a step-for-step correspondence. L was given exactly one
clock-stamped action per Dex commit of the translated program
(spec §8.1 "Clock and timing model"; `L/Semantics.lean`), so:

- every L label is simulated by one Dex commit or Worker effect, preceded by
  the Worker steps that run and return the handler;
- every Dex action is simulated by one L label, except running and returning
  a handler, which L does not see.

`Related` is the candidate invariant that links the two configurations. It is
a draft: Stage 2 is expected to strengthen it (for example with the freshness
of thread IDs), and `dex_step_simulated` and `l_step_simulated` then change
with it. `Examples/Lockstep.lean` tests the
step correspondence on pseudo-random schedules.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## The candidate simulation relation -/

/-- The thread a Step input belongs to. The start Step runs `main`. -/
def threadOfInput : Option DexVal → Tid
  | some (.thread core) => core.tid
  | some (.timedThread core _ _) => core.tid
  | _ => []

/-- The effects a task has not emitted yet. -/
def pendingEffects (flow : Dex.FlowDef signature) (task : Dex.Task signature) : List Val :=
  match task.phase with
  | .started => (task.compute flow).1
  | .running remainingEffects _ => remainingEffects
  | .returned _ => []

/-- The kind and Channel conditions of the wait a blocked thread's Step has
registered (spec §8.3). A timed operation also has its one timer. -/
def expectedWaitShape (self : Tid) : BlockedOn → Dex.WaitKind × List Chan
  | .lock mutex => (.all, [Chan.mutexToken mutex])
  | .recv chan => (.any, [Chan.external chan, Chan.cancelRequest self])
  | .join thread => (.any, [Chan.joinDone thread, Chan.cancelRequest self])
  | .park cv => (.all, [Chan.cvWake cv self])
  | .timed _ _ none => (.any, [Chan.cancelRequest self])
  | .timed _ _ (some chan) => (.any, [Chan.external chan, Chan.cancelRequest self])

/-- The Step that carries a thread has the Step type, input, and options the
translation gives it (spec §8.2): the start Step while `main` boots, the
`Timed` Step numbered by the thread's timer number after Enter, and otherwise
the program point of the thread's code, with the load selection of
`moveToPoint`. -/
def StepCarriesThread (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) : Prop :=
  match thread.phase, thread.timer with
  | .bootPending, _ =>
    record.id.stepType = StepType.start ∧
    ∃ input, record.input = some (DexVal.value input) ∧
      thread.core.locals = [(program.mainParam, input)]
  | .bootRunning, _ =>
    record.id.stepType = StepType.start ∧
    ∃ input, record.input = some (DexVal.value input) ∧
      thread.core.locals = [(program.mainParam, input)]
  | _, some (number, seconds) =>
    record.id = { stepType := StepType.timed, number := number } ∧
    record.input = some (DexVal.timedThread thread.core thread.code seconds)
  | _, none =>
    record.id.stepType = StepType.point thread.code ∧
    record.input = some (DexVal.thread thread.core) ∧
    record.options.executeLoadMaps = executeLoadMaps thread.core thread.code

/-- The Step's current invocation runs as a task with method `method` that
satisfies `Holds`. -/
def HasRunningTask (config : Dex.Config signature) (record : Dex.StepRecord signature)
    (method : Dex.Method) (Holds : Dex.Task signature → Prop) : Prop :=
  ∃ task, task ∈ config.tasks ∧
    record.invocation = some { method := method, state := .running task.id } ∧
    task.step = record.id ∧ task.method = method ∧ task.input = record.input ∧ Holds task

/-- How a thread's wait was decided, read from condition results. -/
def decodeOutcome (thread : L.Thread) (results : Option (Dex.ConditionResults signature)) :
    WakeOutcome :=
  if thread.timer.isSome then decodeTimedOutcome thread.code results
  else decodeBlockedOutcome thread.code results

/-- The phase of a thread matches the state of the Step that carries it and
of that Step's task (spec §8.2: issue at [STEP-START], `out(a)` at
[WORKER-EFFECT], continuation at [STEP-COMPLETE-EXECUTE]). -/
def PhaseMatchesStep (program : Program) (lConfig : L.Config) (config : Dex.Config signature)
    (thread : L.Thread) (record : Dex.StepRecord signature) : Prop :=
  let waitsFirst : Bool := thread.timer.isSome || isBlockingHead thread.code
  match thread.phase with
  | .bootPending => record.invocation = some { method := .execute, state := .pending }
  | .bootRunning =>
    HasRunningTask config record .execute (fun task => pendingEffects ⟦program⟧ task = [])
  | .ready =>
    record.conditionResults = none ∧
    record.invocation =
      some { method := if waitsFirst then .waitFor else .execute, state := .pending }
  | .running loaded =>
    HasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task = [] ∧ task.conditionResults = none ∧
      (match beginKind thread.core thread.code with
        | .load source => readShared task.snapshot source = loaded
        | _ => True) ∧
      -- A statement that requires a mutex runs while the thread holds it, so the
      -- waiter list its Execute read at Start is still current (spec §8.3:
      -- "`waiters/cv` is read and written only under `m`").
      (match (completeStatement program thread.core thread.code loaded).intent with
        | .unlock mutex => lConfig.mutexOwners.get? mutex = some (some thread.core.tid)
        | .cvWait cv =>
          lConfig.mutexOwners.get? cv.mutex = some (some thread.core.tid) ∧
          readWaiters task.snapshot cv = (lConfig.cvQueues.get? cv).getD []
        | .signal cv =>
          lConfig.mutexOwners.get? cv.mutex = some (some thread.core.tid) ∧
          readWaiters task.snapshot cv = (lConfig.cvQueues.get? cv).getD []
        | .broadcast cv =>
          lConfig.mutexOwners.get? cv.mutex = some (some thread.core.tid) ∧
          readWaiters task.snapshot cv = (lConfig.cvQueues.get? cv).getD []
        | _ => True))
  | .emitting effect =>
    HasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task = [effect] ∧ task.conditionResults = none)
  | .emitted =>
    HasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task = [] ∧ task.conditionResults = none ∧
      ∃ effect, beginKind thread.core thread.code = .emit effect)
  | .waitStarted => HasRunningTask config record .waitFor (fun _ => True)
  | .blocked on _ =>
    record.phase = .waiting ∧ record.invocation = none ∧
    ∃ waitState, record.waitState = some waitState ∧
      waitState.wait.kind = (expectedWaitShape thread.core.tid on).1 ∧
      waitState.wait.channels = (expectedWaitShape thread.core.tid on).2 ∧
      waitState.timersDone = waitState.wait.timerSeconds.map (fun _ => false)
  | .woken outcome =>
    record.phase = .execute ∧
    record.invocation = some { method := .execute, state := .pending } ∧
    decodeOutcome thread record.conditionResults = outcome
  | .resuming outcome =>
    HasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task = [] ∧ decodeOutcome thread task.conditionResults = outcome)
  | .halted => False

/-- The part of the relation that holds while the program runs. -/
structure RunningRelated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (run : Dex.Run signature) : Prop where
  /-- Shared variables are Attributes (spec §8.2). -/
  shared : ∀ name, readShared { attributes := run.attributes } name = lConfig.readShared name
  /-- `waiters/cv` is the condition variable's queue (spec §8.3). -/
  cvQueues : ∀ cv,
    readWaiters { attributes := run.attributes } cv = (lConfig.cvQueues.get? cv).getD []
  /-- An external Channel holds the queued external messages (spec §8.1, "External input"). -/
  inputQueues : ∀ chan,
    run.pendingMessages (Chan.external chan) = (lConfig.pendingInput chan).map DexVal.value
  /-- A mutex's token Channel holds one token exactly while the mutex is
  unlocked (spec §8.3 "Mutex"), once the start Step has published the tokens. -/
  mutexTokens : (∀ thread, thread ∈ lConfig.threads →
      thread.phase ≠ .bootPending ∧ thread.phase ≠ .bootRunning) →
    ∀ mutex, (run.pendingMessages (Chan.mutexToken mutex)).length =
      if lConfig.mutexOwners.get? mutex = some none then 1 else 0
  /-- `join/t` holds one message exactly while `t` is ended and not joined. -/
  joinTokens : ∀ thread, (run.pendingMessages (Chan.joinDone thread)).length =
    if lConfig.joinable.contains thread then 1 else 0
  /-- A pending cancel request means the flag is set. -/
  cancelRequestsAreFlags : ∀ thread,
    run.pendingMessages (Chan.cancelRequest thread) ≠ [] → lConfig.cancelFlags.contains thread = true
  /-- A set flag is a pending cancel request, unless the thread is gone or has observed it. -/
  cancelFlagsAreRequests : ∀ thread, lConfig.cancelFlags.contains thread = true →
    run.pendingMessages (Chan.cancelRequest thread) ≠ [] ∨
    lConfig.findThread? thread = none ∨
    ∃ current, lConfig.findThread? thread = some current ∧
      (current.phase = .woken .canceled ∨ current.phase = .resuming .canceled)
  /-- `cv/w` holds the wakes of a waiter that has not parked yet. -/
  cvWakes : ∀ cv waiter, (run.pendingMessages (Chan.cvWake cv waiter)).length =
    lConfig.cvWakes.count (cv, waiter)
  /-- The armed Dex timers are the armed L timers: same number, same deadline (spec §8.2). -/
  timers : ∀ timer fireTime, run.timers.get? timer = some fireTime ↔
    ∃ thread chan since, thread ∈ lConfig.threads ∧
      thread.phase = .blocked (.timed timer.step.number fireTime chan) since ∧
      timer.step.stepType = StepType.timed ∧ timer.index = 0
  /-- The timer counter is the `Timed` Step counter (spec §8.2: "the timer
  number `n` is its Step number"). -/
  timerCounter : (run.counters.get? StepType.timed).getD 0 = lConfig.timerCounter
  /-- Every thread that has not halted is carried by one active Step. -/
  threadsHaveSteps : ∀ thread, thread ∈ lConfig.threads → thread.phase ≠ .halted →
    ∃ record, record ∈ run.steps ∧ record.phase.isActive = true ∧
      StepCarriesThread program thread record ∧
      PhaseMatchesStep program lConfig dexConfig thread record
  /-- Every active Step carries one thread. -/
  stepsHaveThreads : ∀ record, record ∈ run.steps → record.phase.isActive = true →
    ∃ thread, thread ∈ lConfig.threads ∧ thread.phase ≠ .halted ∧
      StepCarriesThread program thread record
  /-- Step execution IDs are unique, and the per-type counters are at or above
  every number in use, so `new` creates a fresh ID (spec §3.1). -/
  stepIdsAreDistinct : run.steps.Pairwise (fun first second => first.id ≠ second.id)
  /-- See `stepIdsAreDistinct`. -/
  stepNumbersAreCounted : ∀ record, record ∈ run.steps →
    record.id.number ≤ (run.counters.get? record.id.stepType).getD 0
  /-- Thread IDs are unique. -/
  threadIdsAreDistinct : ∀ first, first ∈ lConfig.threads → ∀ second, second ∈ lConfig.threads →
    first.core.tid = second.core.tid → first = second
  /-- Blocked threads are ordered as their Steps' waits (spec §7 item 4: FIFO
  by registration). -/
  blockingOrder : ∀ first second firstRecord secondRecord firstOn firstSince secondOn secondSince
      firstWait secondWait,
    first ∈ lConfig.threads → second ∈ lConfig.threads →
    firstRecord ∈ run.steps → secondRecord ∈ run.steps →
    StepCarriesThread program first firstRecord → StepCarriesThread program second secondRecord →
    first.phase = .blocked firstOn firstSince → second.phase = .blocked secondOn secondSince →
    firstRecord.waitState = some firstWait → secondRecord.waitState = some secondWait →
    (firstSince < secondSince ↔ firstWait.registeredSeq < secondWait.registeredSeq)
  /-- While the Flow runs no Step is canceled, so every task is current. -/
  tasksAreCurrent : ∀ task, task ∈ dexConfig.tasks → dexConfig.isTaskCurrent task = true

/-- The candidate simulation relation between an L configuration and a Dex
configuration of the translated Flow. -/
structure Related (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) : Prop where
  /-- The clocks agree (spec §8.1, "Clock and timing model"). -/
  clock : lConfig.clock = dexConfig.clock
  /-- The last L clock stamp is the last Dex commit timestamp: one stamped L
  action per Dex commit. -/
  stamp : lConfig.lastStampAt = dexConfig.lastCommitAt
  /-- Not started, running, and closed correspond. -/
  status :
    match lConfig.status, dexConfig.run with
    | .notStarted, none => True
    | .running, some run => run.status = .running
    | .closed, some run => ∃ status, run.status = .closed status
    | _, _ => False
  /-- Task IDs are unique and below the next fresh ID. -/
  taskIdsAreDistinct : dexConfig.tasks.Pairwise (fun first second => first.id ≠ second.id) ∧
    ∀ task, task ∈ dexConfig.tasks → task.id < dexConfig.nextTaskId
  /-- While the program runs, the data and the threads correspond. -/
  running : ∀ run, dexConfig.run = some run → run.status = .running →
    RunningRelated program lConfig dexConfig run
  /-- After the close, the late emits are the effects that the remaining
  tasks have not emitted (spec §8.1: "issued emits may still deliver"), and no
  task is current. -/
  closed : lConfig.status = .closed →
    (dexConfig.tasks.flatMap (pendingEffects ⟦program⟧)).Perm lConfig.lateEmits ∧
    ∀ task, task ∈ dexConfig.tasks → dexConfig.isTaskCurrent task = false

/-- The initial configurations are related. -/
theorem related_initial (program : Program) :
    Related program (L.system program).init (Dex.system ⟦program⟧).init :=
  { clock := rfl
    stamp := rfl
    status := trivial
    taskIdsAreDistinct := ⟨List.Pairwise.nil, fun task isMember => by cases isMember⟩
    running := fun run hasRun => by cases hasRun
    closed := fun isClosed => by cases isClosed }

/-- A Dex action that an admissible run can take from this configuration: a
start of the entry Step with an encoded program input, or, once the Flow has
started, a publication of an encoded message to an external Channel (spec §5.3). -/
def IsAdmissibleAction (config : Dex.Config signature) : Dex.Action signature → Prop
  | .request (.startFlow stepType input) =>
    stepType = StepType.start ∧ ∃ value, input = DexVal.value value
  | .request (.publish chan message) =>
    config.run ≠ none ∧
    ∃ externalChan value, chan = Chan.external externalChan ∧ message = DexVal.value value
  | _ => True

/-- Dex to L: every Dex action from a configuration related to a reachable L
configuration is simulated by at most one L label with the same projected
events, and the results are related. (Running and returning a handler are
simulated by no L label.) The L configuration is reachable, so no thread of
it is about to break a mutex requirement (`Program.IsWellFormed`). -/
theorem dex_step_simulated (program : Program) (isWellFormed : program.IsWellFormed)
    (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (isRelated : Related program lConfig dexConfig)
    (isReachable : (L.system program).Reachable lConfig)
    (action : Dex.Action signature) (isAdmissible : IsAdmissibleAction dexConfig action)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig action = some (dexEvents, dexNext)) :
    ∃ labels lNext,
      labels.length ≤ 1 ∧
      (L.system program).exec lConfig labels = some (dexEvents.filterMap project, lNext) ∧
      Related program lNext dexNext := by
  sorry

/-- L to Dex: every L label from related configurations is simulated by a
finite sequence of Dex actions with the same projected events, and the
results are related. The sequence is the Worker steps that run and return the
handler, then one commit or one Worker effect. Its only request, if any, is
the encoded request of the L label. Well-formedness is not needed here: L
gives no action to a thread that would break a mutex requirement. -/
theorem l_step_simulated (program : Program)
    (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (isRelated : Related program lConfig dexConfig)
    (label : L.Label) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig label = some (lEvents, lNext)) :
    ∃ actions dexEvents dexNext,
      (Dex.system ⟦program⟧).exec dexConfig actions = some (dexEvents, dexNext) ∧
      dexEvents.filterMap project = lEvents ∧
      actions.filterMap (Dex.system ⟦program⟧).requestOf =
        ((L.system program).requestOf label).toList.map encodeRequest ∧
      Related program lNext dexNext := by
  sorry

/-! ## The two inclusions of liveness

The two inclusions of safety are proved in `FiniteTraces.lean` from the two
step simulations above. -/

/-- Liveness, Dex to L: every fair maximal Dex trace with its marker, for an
admissible word, is one of L. Needs the transfer of F1 to F6 and of the marker
along the simulation, and the removal of the handler steps L does not see. -/
theorem fairMaximalTrace_dex_to_l (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker)
    (isDexTrace : IsProjectedDexFairMaximalTrace program word trace marker) :
    IsLFairMaximalTrace program word trace marker := by
  sorry

/-- Liveness, L to Dex: every fair maximal L trace with its marker, for an
admissible word, is one of the translated Flow. Needs the same transfers in
the other direction, and a fair schedule of the inserted handler steps. -/
theorem fairMaximalTrace_l_to_dex (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker)
    (isLTrace : IsLFairMaximalTrace program word trace marker) :
    IsProjectedDexFairMaximalTrace program word trace marker := by
  sorry

end DexExpressiveness
