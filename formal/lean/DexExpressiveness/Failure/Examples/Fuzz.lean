import DexExpressiveness.Failure.Examples.CrashRetryZombie
import DexExpressiveness.Examples.LockstepChecks

/-!
# An inductiveness fuzz of the failure simulation

Test tooling, not part of the theorem. The step lemmas of
`Failure/Statements.lean` are stated for every configuration that satisfies
`Invariant`, not only for reachable ones, so `Invariant` must be inductive
and strong enough for `IsStepSimulated`. This file tests both:

1. **Walks.** It walks pseudo-random DexF runs of translated programs. Each
   walk mixes progress labels with failure labels: [CRASH], [RECOVER],
   [RECOVER-INTERRUPT], [ATTEMPT-LOST] (also before the start),
   [ATTEMPT-RETURNED], [ATTEMPT-HEARTBEAT], [START-RETRY], [ATTEMPT-EXHAUSTED]
   rarely, and internal commits, with a budget of failures per walk. At every
   step it checks `IsStepSimulated` (except for [ATTEMPT-EXHAUSTED], which H1
   excludes) and the mirror of `Invariant` after the step.
2. **Perturbations.** It takes every `period`-th configuration of the walks
   and changes it in one place (`perturbations`): a task's counter, phase,
   attempt, or place; a ghost history's `h`, `v`, `par`, or outcome; an
   attempt state; a retry timer; an interrupt; `up`; a stamp; a counter. It
   keeps a changed copy only when the mirror of `Invariant` accepts it, and
   from every kept copy it takes every enabled candidate label with the same
   checks.

A failure is a configuration that the invariant accepts and that breaks a
step: a gap in `Invariant`, or a step that α does not simulate. Passing is
evidence, not proof.
-/

namespace DexExpressiveness.DexF.Examples

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples (nextRandom pick)

/-- The DexF form of an L request: StartFlow gets request ID 0. -/
def failureRequestOf (request : L.Request) : Request σ :=
  match encodeRequest request with
  | .startFlow stepType input => .startFlow 0 stepType input
  | .publish chan value => .publish chan value

/-- The requests a walk gives outcomes to, in order: a start and three inputs. -/
def fuzzRequests : List (Request σ) :=
  DexExpressiveness.Examples.LockstepChecks.requests.map failureRequestOf

/-! ## Candidate labels -/

/-- The Worker, completion, and drop labels of one real task, with both
values of every classification flag. -/
def taskCandidates (task : Task σ) : List (Action σ) :=
  [true, false].flatMap (fun flag =>
    [.workerRun task.base.id flag, .workerEffect task.base.id flag,
     .workerReturn task.base.id flag, .workerAbandon task.base.id flag,
     .resultStale task.base.id flag]) ++
  [.stepComplete task.base.id]

/-- The progress labels to try: ticks, the next request, Step starts, Worker
and completion steps, timer fires. -/
def progressCandidates (config : Config σ) (nextRequest : Option (Request σ)) :
    List (Action σ) :=
  let steps : List (Dex.StepRecord σ) := match config.run with
    | none => []
    | some run => run.base.steps
  let dueCount := match config.run with
    | none => 0
    | some run => (run.dueTimers config.clock).length
  [.tick 1, .tick 1000000] ++
  (match nextRequest with
   | some request => [.request request]
   | none => []) ++
  steps.flatMap (fun record => [.stepStart record.id false, .stepStart record.id true]) ++
  config.realTasks.flatMap taskCandidates ++
  (List.range dueCount).flatMap (fun index => [.timerFire (index + 1) true, .timerFire (index + 1) false])

/-- The failure labels to try. -/
def failureCandidates (config : Config σ) : List (Action σ) :=
  let steps : List (Dex.StepRecord σ) := match config.run with
    | none => []
    | some run => run.base.steps
  [.crash, .recover, .internalCommit, .tick 3,
   .startRetry 0 StepType.start (DexVal.value (.int 3)),
   .engineReject (.publish (Chan.external 7) (DexVal.value (.int 50)))] ++
  steps.flatMap (fun record =>
    [.attemptFailed record.id (.retryAfter 3), .attemptReturned record.id,
     .attemptHeartbeat record.id DexVal.token]) ++
  config.interrupts.map (fun interrupt => .recoverInterrupt interrupt (.retryAfter 3))

/-- The rare exhaustion labels to try. -/
def exhaustionCandidates (config : Config σ) : List (Action σ) :=
  match config.run with
  | none => []
  | some run => run.base.steps.map (fun record => .attemptFailed record.id (.exhausted .maximumAttempts))

/-- The labels enabled in a configuration. -/
def enabledLabels (program : Program σ) (config : Config σ) (candidates : List (Action σ)) :
    List (Action σ) :=
  candidates.filter (fun label => (step program config label).isSome)

/-! ## Checks of one step -/

/-- The kind of a label, for coverage. -/
def labelKind (config next : Config σ) : Action σ → String
  | .tick _ => "tick"
  | .request (.startFlow _ _ _) => "startFlow"
  | .request (.publish _ _) => "publish"
  | .startRetry _ _ _ => "startRetry"
  | .engineReject _ => "engineReject"
  | .stepStart _ isSnapshotReused => if isSnapshotReused then "stepStart reused" else "stepStart"
  | .workerRun _ isFirst => if isFirst then "workerRun first" else "workerRun repeat"
  | .workerEffect _ isFirst => if isFirst then "workerEffect first" else "workerEffect repeat"
  | .workerReturn _ isFirst => if isFirst then "workerReturn first" else "workerReturn repeat"
  | .workerAbandon _ isLast => if isLast then "workerAbandon last" else "workerAbandon"
  | .stepComplete taskId =>
    if (config.findZombie? taskId).isSome then "stepComplete zombie" else "stepComplete"
  | .resultStale taskId isLast =>
    (if isLast then "resultStale last" else "resultStale") ++
      (if (config.findZombie? taskId).isSome then " zombie" else "")
  | .timerFire _ isWaitFired => if isWaitFired then "timerFire wait" else "timerFire retry"
  | .attemptFailed _ decision => if decision.isExhausted then "attemptExhausted" else "attemptLost"
  | .attemptReturned _ => "attemptReturned"
  | .attemptHeartbeat _ _ => "attemptHeartbeat"
  | .crash => "crash"
  | .recover => "recover"
  | .recoverInterrupt _ _ =>
    if next.run.map (fun run => run.base.seq) == config.run.map (fun run => run.base.seq) then
      "recoverInterrupt ignored"
    else "recoverInterrupt applied"
  | .internalCommit => "internalCommit"

/-- The failures of one enabled step: the simulation and the progress rule
(not for [ATTEMPT-EXHAUSTED]), and the invariant after it. -/
def stepFailures (program : Program σ) (config : Config σ) (label : Action σ) : List String :=
  match step program config label with
  | none => []
  | some (_, next) =>
    (if label.isExhausting || checkStepSimulated program config label then []
     else [s!"{describeAction label}: not simulated"]) ++
    (if label.isExhausting || checkProgressMatches program config label then []
     else [s!"{describeAction label}: progress Boolean differs from the transition map"]) ++
    (checkInvariant program next).map (fun clause => s!"{describeAction label}: {clause}")

/-! ## Walks -/

/-- The result of the walks: failures, sampled configurations, and the count
of steps by kind. -/
structure WalkResult where
  /-- The failures found. -/
  failures : List String
  /-- The configurations sampled for perturbation. -/
  samples : List (Config σ)
  /-- The steps taken, by kind. -/
  steps : AssocList String Nat

/-- Count one more of `key`. -/
def countOne (counts : AssocList String Nat) (key : String) : AssocList String Nat :=
  counts.set key ((counts.get? key).getD 0 + 1)

/-- Whether a label is a clock tick. -/
def isTick : Action σ → Bool
  | .tick _ => true
  | _ => false

/-- Whether nothing but ticks and internal commits can happen any more: the
Flow is closed, no real task and no interrupt is left, and no retry timer is
armed. -/
def isClosedAndQuiet (config : Config σ) : Bool :=
  config.realTasks.isEmpty && config.interrupts.isEmpty &&
    match config.run with
    | none => false
    | some run =>
      run.retryTimers.isEmpty &&
        match run.base.status with
        | .closed _ => true
        | .running => false

/-- One pseudo-random walk of at most `fuel` steps; it stops early once the
Flow is closed and quiet. While the failure budget lasts, a failure label is
taken with probability about one in three, half of them a failure of an
attempt, and an exhaustion about one in three hundred. Otherwise a progress
label other than a tick is preferred two times in three. -/
def walk (program : Program σ) (period : Nat) :
    Nat → Nat → Nat → Nat → List (Request σ) → Config σ → WalkResult → WalkResult
  | 0, _, _, _, _, _, result => result
  | fuel + 1, seed, index, budget, requests, config, result =>
    let progress := enabledLabels program config (progressCandidates config requests.head?)
    let moves := progress.filter (fun label => !isTick label)
    let failures := enabledLabels program config (failureCandidates config)
    let attemptFailures := failures.filter (fun label =>
      match label with
      | .attemptFailed _ _ | .attemptReturned _ | .recoverInterrupt _ _ => true
      | _ => false)
    let exhaustions := enabledLabels program config (exhaustionCandidates config)
    let roll := (seed / 65536) % 300
    let choice :=
      if isClosedAndQuiet config then none
      else if 0 < budget && roll = 0 && !exhaustions.isEmpty then
        pick (nextRandom seed) exhaustions
      else if 0 < budget && roll % 6 = 1 && !attemptFailures.isEmpty then
        pick (nextRandom seed) attemptFailures
      else if 0 < budget && roll % 6 = 4 && !failures.isEmpty then
        pick (nextRandom seed) failures
      else if !moves.isEmpty && roll % 3 != 2 then pick (nextRandom seed) moves
      else if progress.isEmpty then pick (nextRandom seed) failures
      else if roll % 7 = 0 then some (.tick 1000000)
      else some (.tick 1)
    match choice with
    | none => result
    | some label =>
      match step program config label with
      | none => result
      | some (_, next) =>
        let isFailure := label.simulationKind = .failure && !(match label with
          | .tick _ => true | _ => false)
        let remaining := match label with
          | .request _ => requests.tail
          | _ => requests
        let stepResult : WalkResult :=
          { failures := result.failures ++
              (stepFailures program config label).map (fun failure => s!"step {index}: {failure}")
            samples := if index % period = 0 then result.samples ++ [next] else result.samples
            steps := countOne result.steps (labelKind config next label) }
        walk program period fuel (nextRandom (nextRandom seed)) (index + 1)
          (if isFailure then budget - 1 else budget) remaining next stepResult

/-- `seeds` walks of `fuel` steps each, with a budget of `budget` failures. -/
def walks (program : Program σ) (fuel period budget seeds : Nat) : WalkResult :=
  (List.range seeds).foldl (fun result seed =>
    let walked := walk program period fuel (nextRandom (seed + 1)) 0 budget fuzzRequests
      (Config.initial σ) { failures := [], samples := [], steps := result.steps }
    { failures := result.failures ++ walked.failures.map (fun failure => s!"seed {seed}, {failure}")
      samples := result.samples ++ walked.samples
      steps := walked.steps })
    { failures := [], samples := [], steps := [] }

/-! ## Perturbations -/

/-- Changed copies of one real task. -/
def taskVariants (program : Program σ) (config : Config σ) (task : Task σ) :
    List (String × Task σ) :=
  let computed := match config.ghost.argumentsOf task.key with
    | some arguments => some (arguments.compute program.failureFreeFlow task.key)
    | none => none
  [("task.emittedCount+1", { task with emittedCount := task.emittedCount + 1 })] ++
  (if task.emittedCount = 0 then []
   else [("task.emittedCount-1", { task with emittedCount := task.emittedCount - 1 })]) ++
  [("task.phase.started", { task with base := { task.base with phase := .started } }),
   ("task.attempt+1",
    { task with attemptContext := { task.attemptContext with attempt := task.attemptContext.attempt + 1 } })] ++
  (match computed with
   | some (effects, response) =>
     [("task.phase.running", { task with base := { task.base with phase := .running effects response } }),
      ("task.phase.returned", { task with base := { task.base with phase := .returned response } })]
   | none => []) ++
  [("task.snapshot.empty", { task with base := { task.base with snapshot := { attributes := [] } } })]

/-- Changed copies of one ghost history. -/
def historyVariants (program : Program σ) (key : InvocationKey σ) (history : InvocationHistory σ) :
    List (String × InvocationHistory σ) :=
  let computed := history.arguments.map (fun arguments =>
    arguments.compute program.failureFreeFlow key)
  [("ghost.h+1", { history with emittedCount := history.emittedCount + 1 })] ++
  (if history.emittedCount = 0 then []
   else [("ghost.h-1", { history with emittedCount := history.emittedCount - 1 })]) ++
  [("ghost.v.notRun", { history with progress := .notRun }),
   ("ghost.par.none", { history with arguments := none })] ++
  (match computed with
   | some (effects, response) =>
     [("ghost.v.ran", { history with progress := .ran effects response }),
      ("ghost.v.returned", { history with progress := .returned response })]
   | none => []) ++
  ([InvocationOutcome.live, .committed, .canceled].filter (fun outcome => outcome != history.outcome)).map
    (fun outcome => ("ghost.outcome", { history with outcome := outcome })) ++
  (match history.arguments with
   | some arguments =>
     [("ghost.par.virtualTaskId+1",
       { history with arguments := some { arguments with virtualTaskId := arguments.virtualTaskId + 1 } })]
   | none => [])

/-- Changed copies of one attempt state. -/
def attemptVariants (attempt : Attempt σ) : List (String × Attempt σ) :=
  [("attempt.isBackoff", { attempt with isBackoff := !attempt.isBackoff }),
   ("attempt.number+1", { attempt with number := attempt.number + 1 }),
   ("attempt.snapshot.none", { attempt with snapshot := none }),
   ("attempt.snapshot.empty", { attempt with snapshot := some { attributes := [] } })]

/-- Replace the element at `position`. -/
def replaceAt {α : Type} (elements : List α) (position : Nat) (element : α) : List α :=
  elements.set position element

/-- Changed copies of a configuration, each with the name of its change. -/
def perturbations (program : Program σ) (config : Config σ) : List (String × Config σ) :=
  let ghost := config.ghost
  let tasks := (List.range config.tasks.length).flatMap (fun position =>
    match config.tasks[position]? with
    | some task =>
      (taskVariants program config task).map (fun variant =>
        (variant.1, { config with tasks := replaceAt config.tasks position variant.2 })) ++
      [("task.toZombies", { config with
          tasks := config.tasks.eraseIdx position, zombies := config.zombies ++ [task] }),
       ("task.drop", { config with tasks := config.tasks.eraseIdx position }),
       ("task.duplicate", { config with tasks := config.tasks ++ [task] })]
    | none => [])
  let zombies := (List.range config.zombies.length).flatMap (fun position =>
    match config.zombies[position]? with
    | some task =>
      (taskVariants program config task).map (fun variant =>
        ("zombie." ++ variant.1, { config with zombies := replaceAt config.zombies position variant.2 })) ++
      [("zombie.toTasks", { config with
          zombies := config.zombies.eraseIdx position, tasks := config.tasks ++ [task] }),
       ("zombie.drop", { config with zombies := config.zombies.eraseIdx position })]
    | none => [])
  let histories := (List.range ghost.invocations.length).flatMap (fun position =>
    match ghost.invocations[position]? with
    | some entry =>
      (historyVariants program entry.1 entry.2).map (fun variant =>
        (variant.1, { config with
          ghost := { ghost with invocations := replaceAt ghost.invocations position (entry.1, variant.2) } }))
    | none => [])
  let globals : List (String × Config σ) :=
    [("ghost.n+1", { config with ghost := { ghost with normalCount := ghost.normalCount + 1 } }),
     ("ghost.lastNormalCommitAt.last", { config with
        ghost := { ghost with lastNormalCommitAt := config.lastCommitAt } }),
     ("ghost.nextVirtualTaskId+1", { config with
        ghost := { ghost with nextVirtualTaskId := ghost.nextVirtualTaskId + 1 } }),
     ("ghost.zeta.dropLast", { config with
        ghost := { ghost with normalIndex := ghost.normalIndex.dropLast } }),
     ("isUp", { config with isUp := !config.isUp }),
     ("lastCommitAt.none", { config with lastCommitAt := none }),
     ("nextTaskId+1", { config with nextTaskId := config.nextTaskId + 1 })] ++
    (if config.nextTaskId = 0 then []
     else [("nextTaskId-1", { config with nextTaskId := config.nextTaskId - 1 })]) ++
    (if ghost.nextVirtualTaskId = 0 then []
     else [("ghost.nextVirtualTaskId-1", { config with
        ghost := { ghost with nextVirtualTaskId := ghost.nextVirtualTaskId - 1 } })])
  let interrupts : List (String × Config σ) :=
    (List.range config.interrupts.length).map (fun position =>
      ("interrupt.drop", { config with interrupts := config.interrupts.eraseIdx position })) ++
    (match config.run with
     | none => []
     | some run =>
       run.base.steps.filterMap (fun record =>
         match record.invocation, run.findAttempt? record.id with
         | some invocation, some attempt =>
           some ("interrupt.add", { config with
             interrupts := config.interrupts ++
               [{ step := record.id, method := invocation.method, attempt := attempt.number
                  taskId := match invocation.state with
                    | .running taskId => taskId
                    | .pending => config.nextTaskId }] })
         | _, _ => none))
  let runs : List (String × Config σ) :=
    match config.run with
    | none => []
    | some run =>
      let withRun := fun (changed : Run σ) => { config with run := some changed }
      (List.range run.attempts.length).flatMap (fun position =>
        match run.attempts[position]? with
        | some entry =>
          (attemptVariants entry.2).map (fun variant =>
            (variant.1, withRun { run with attempts := replaceAt run.attempts position (entry.1, variant.2) }))
        | none => []) ++
      (List.range run.retryTimers.length).map (fun position =>
        ("retryTimer.drop", withRun { run with retryTimers := run.retryTimers.eraseIdx position })) ++
      run.base.steps.filterMap (fun record =>
        match record.invocation, run.findAttempt? record.id with
        | some invocation, some attempt =>
          some ("retryTimer.add", withRun { run with
            retryTimers := run.retryTimers.set
              { step := record.id, method := invocation.method, attempt := attempt.number } config.clock })
        | _, _ => none) ++
      run.base.steps.filterMap (fun record =>
        match record.invocation, run.findAttempt? record.id with
        | some invocation, some attempt =>
          some ("retryTimer.addAhead", withRun { run with
            retryTimers := run.retryTimers.set
              { step := record.id, method := invocation.method, attempt := attempt.number + 1 }
              config.clock })
        | _, _ => none) ++
      [("base.counters.reset", withRun { run with base := { run.base with counters := [] } }),
       ("base.counters.decrement", withRun { run with
          base := { run.base with
            counters := run.base.counters.map (fun entry => (entry.1, entry.2 - 1)) } })] ++
      run.base.steps.flatMap (fun record =>
        ([Dex.Phase.waitFor, .waiting, .execute, .completed, .canceled].filter
            (fun phase => phase != record.phase)).map (fun phase =>
          ("step.phase", withRun { run with
            base := run.base.updateStep record.id (fun current => { current with phase := phase }) }))) ++
      run.base.steps.filterMap (fun record =>
        if (ghost.findInvocation? (record.id, .execute)).isSome then none
        else
          some ("ghost.history.addExecute", { config with
            ghost := { ghost with
              invocations := ghost.invocations ++
                [((record.id, .execute),
                  { InvocationHistory.fresh ghost.normalCount with outcome := .canceled })] } })) ++
      run.base.steps.filterMap (fun record =>
        match record.invocation with
        | some { method := method, state := .running _ } =>
          some ("invocation.pending", withRun { run with
            base := run.base.updateStep record.id (fun current =>
              { current with invocation := some { method := method, state := .pending } }) })
        | some { method := method, state := .pending } =>
          some ("invocation.running", withRun { run with
            base := run.base.updateStep record.id (fun current =>
              { current with invocation := some { method := method, state := .running config.nextTaskId } }) })
        | none => none)
  tasks ++ zombies ++ histories ++ globals ++ interrupts ++ runs

/-- The result of the perturbation checks. -/
structure PerturbationResult where
  /-- The failures found. -/
  failures : List String
  /-- The changed copies tried. -/
  tried : Nat
  /-- The changed copies kept, by perturbation. -/
  kept : AssocList String Nat
  /-- The single steps checked from kept copies. -/
  checkedSteps : Nat

/-- Check every enabled candidate step of every kept changed copy of every
sample. -/
def perturbationChecks (program : Program σ) (samples : List (Config σ)) : PerturbationResult :=
  samples.foldl (fun result sample =>
    (perturbations program sample).foldl (fun current named =>
      if decide (named.2 = sample) then current
      else if (checkInvariant program named.2).isEmpty then
        let labels := enabledLabels program named.2
          (progressCandidates named.2 (fuzzRequests.head?) ++
            [.request (.publish (Chan.external 7) (DexVal.value (.int 50)))] ++
            failureCandidates named.2 ++ exhaustionCandidates named.2)
        let failures := labels.flatMap (fun label =>
          (stepFailures program named.2 label).map (fun failure => s!"{named.1}: {failure}"))
        { failures := current.failures ++ failures
          tried := current.tried + 1
          kept := countOne current.kept named.1
          checkedSteps := current.checkedSteps + labels.length }
      else { current with tried := current.tried + 1 }) result)
    { failures := [], tried := 0, kept := [], checkedSteps := 0 }

/-- A hand-written Flow that the translation cannot produce, to exercise
effect lists longer than one, WaitFor effects, wait and retry timers in one
index, concurrent Steps, and a close that cancels a running Step:

- the start Step emits `1, 2, 3`, writes a shared variable, and moves to two
  Steps, `Timed` and a program point;
- `Timed` waits for one second or a message on external Channel 7, emitting
  `4` in WaitFor, then emits `5, 6` and completes the Flow;
- the program point emits `7, 8`, publishes to external Channel 7, and ends. -/
def multiEffectFlow (offset : Int) : Dex.FlowDef σ :=
  { stepDef := fun stepType =>
      match stepType with
      | StepType.start =>
        { waitFor := none
          execute := fun _ _ _ _ =>
            ([.int (1 + offset), .int 2, .int 3],
              { effects := { writes := [(Key.shared 0, some (DexVal.value (.int 9)))] }
                decision :=
                  { moves :=
                      [{ stepType := StepType.timed, input := none, options := { skipWaitFor := false } },
                       { stepType := StepType.point [], input := none, options := { skipWaitFor := true } }]
                    close := none } }) }
      | StepType.timed =>
        { waitFor := some (fun _ _ _ =>
            ([.int 4],
              { effects := {}
                wait := { kind := .any, timerSeconds := [1], channels := [Chan.external 7] } }))
          execute := fun _ _ _ _ =>
            ([.int 5, .int 6],
              { effects := {}
                decision := { moves := [], close := some (.forceComplete (some (DexVal.exitTag (.int 0)))) } }) }
      | StepType.point _ =>
        { waitFor := none
          execute := fun _ _ _ _ =>
            ([.int 7, .int 8],
              { effects := { publications := [(Chan.external 7, DexVal.value (.int 1))] }
                decision := { moves := [], close := some .deadEnd } }) } }

/-- A planted fault: a program that breaks H3, because its start Step emits a
different first effect from the second attempt on. The fuzz must report
failures for it. -/
def attemptSensitiveProgram : Program σ :=
  fun context => if context.attempt ≤ 1 then multiEffectFlow 0 else multiEffectFlow 100

/-- The example programs the fuzz runs on: the hand-written Flow and the
translations of example L programs. -/
def failureFuzzFlows : List (String × Program σ) :=
  ("multiEffect", Program.ofFlow (multiEffectFlow 0)) ::
    failureFuzzPrograms.map (fun named => (named.1, Program.ofFlow ⟦named.2⟧))
where
  /-- The translated example programs. -/
  failureFuzzPrograms : List (String × L.Program) :=
  [("emit", emitProgram), ("Mutex", DexExpressiveness.Examples.Mutex.program),
   ("TimedSelect", DexExpressiveness.Examples.TimedSelect.program),
   ("recursion", DexExpressiveness.Examples.LockstepChecks.recursionProgram),
   ("exception", DexExpressiveness.Examples.LockstepChecks.exceptionProgram),
   ("broadcast", DexExpressiveness.Examples.LockstepChecks.broadcastProgram),
   ("signal", DexExpressiveness.Examples.LockstepChecks.signalProgram),
   ("selectRace", DexExpressiveness.Examples.LockstepChecks.selectRaceProgram),
   ("lateEmit", DexExpressiveness.Examples.LockstepChecks.lateEmitProgram),
   ("twoLateEmits", DexExpressiveness.Examples.LockstepChecks.twoLateEmitsProgram),
   ("cancel", DexExpressiveness.Examples.LockstepChecks.cancelProgram)]

#guard (walks (Program.ofFlow ⟦emitProgram⟧) 60 10 6 2).failures.isEmpty
#guard (perturbationChecks (Program.ofFlow ⟦emitProgram⟧)
  ((walks (Program.ofFlow ⟦emitProgram⟧) 40 20 4 1).samples)).failures.isEmpty

end DexExpressiveness.DexF.Examples
