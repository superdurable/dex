import DexExpressiveness.Simulation
import DexExpressiveness.Examples.Schedule

/-!
# Lockstep runs of an L program and its translation

This file is test tooling, not part of the theorem. It runs an L program and
its translated Flow side by side under one schedule and checks, after every
step, that

- the L events equal the projection `π` of the Dex events, and
- the L clock stamp equals the Dex commit timestamp.

`dexActionsFor` turns one L label into the Dex actions that are meant to
simulate it, and `lLabelsFor` turns one Dex action into the L labels that are
meant to simulate it. They are the step correspondence that the simulation
lemmas of `Simulation.lean` state. Running them on pseudo-random schedules
tests that correspondence before anyone proves it.
-/

namespace DexExpressiveness.Examples

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation

/-- The Worker steps that bring a task to the point where it has emitted
nothing more and can return: run the handler if it has not run. -/
def runIfStarted (task : Dex.Task signature) : List (Dex.Action signature) :=
  match task.phase with
  | .started => [.workerRun task.id]
  | _ => []

/-- The Worker steps that bring a task to `Returned`, for a task with no
effect left to emit. -/
def returnTask (task : Dex.Task signature) : List (Dex.Action signature) :=
  match task.phase with
  | .started => [.workerRun task.id, .workerReturn task.id]
  | .running _ _ => [.workerReturn task.id]
  | .returned _ => []

/-- The active Step of a thread in the Dex run, with its running task, if any. -/
def stepOfThread (config : Dex.Config signature) (thread : Tid) :
    Option (Dex.StepRecord signature × Option (Dex.Task signature)) :=
  match config.run with
  | none => none
  | some run =>
    match run.steps.find? (fun record =>
        record.phase.isActive && decide (threadOfInput record.input = thread)) with
    | none => none
    | some record =>
      match record.invocation with
      | some { method := _, state := .running taskId } => some (record, config.findTask? taskId)
      | _ => some (record, none)

/-- The Dex actions that simulate one L label, given both configurations
before the step. -/
def dexActionsFor (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature) :
    L.Label → List (Dex.Action signature)
  | .tick duration => [.tick duration]
  | .request request => [.request (encodeRequest request)]
  | .fire count => [.timerFire count]
  | .thread thread =>
    match lConfig.findThread? thread, stepOfThread dexConfig thread with
    | some current, some (record, task) =>
      match current.phase, task with
      | .bootPending, _ => [.stepStart record.id]
      | .ready, _ => [.stepStart record.id]
      | .woken _, _ => [.stepStart record.id]
      | .emitting _, some running => runIfStarted running ++ [.workerEffect running.id]
      | _, some running => returnTask running ++ [.stepComplete running.id]
      | _, none => []
    | _, _ => []
  | .lateDeliver index =>
    match lConfig.lateEmits[index]? with
    | none => []
    | some effect =>
      match dexConfig.tasks.find? (fun task => (pendingEffects ⟦program⟧ task).contains effect) with
      | some task => runIfStarted task ++ [.workerEffect task.id]
      | none => []
  | .lateDrop index =>
    match lConfig.lateEmits[index]? with
    | none => []
    | some effect =>
      match dexConfig.tasks.find? (fun task => (pendingEffects ⟦program⟧ task).contains effect) with
      | some task => [.workerAbandon task.id]
      | none => []

/-- The L labels that simulate one Dex action, given both configurations
before the step. Running and returning a handler have no L counterpart. -/
def lLabelsFor (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature) :
    Dex.Action signature → List L.Label
  | .tick duration => [.tick duration]
  | .request (.startFlow _ (.value input)) =>
    ((Literal.ofVal? input).map (fun literal => L.Label.request (.start literal))).toList
  | .request (.publish (.external chan) (.value value)) =>
    ((Literal.ofVal? value).map (fun literal => L.Label.request (.input chan literal))).toList
  | .request _ => []
  | .timerFire count => [.fire count]
  | .stepStart stepId =>
    match dexConfig.run with
    | none => []
    | some run =>
      match run.findStep? stepId with
      | some record => [.thread (threadOfInput record.input)]
      | none => []
  | .workerRun _ => []
  | .workerReturn _ => []
  | .stepComplete taskId =>
    match dexConfig.findTask? taskId with
    | some task => [.thread (threadOfInput task.input)]
    | none => []
  | .workerEffect taskId =>
    match dexConfig.findTask? taskId with
    | none => []
    | some task =>
      if dexConfig.isTaskCurrent task then [.thread (threadOfInput task.input)]
      else
        match pendingEffects ⟦program⟧ task with
        | effect :: _ => [.lateDeliver (lConfig.lateEmits.idxOf effect)]
        | [] => []
  | .workerAbandon taskId =>
    match dexConfig.findTask? taskId with
    | none => []
    | some task =>
      match pendingEffects ⟦program⟧ task with
      | effect :: _ => [.lateDrop (lConfig.lateEmits.idxOf effect)]
      | [] => []
  | .stepCompleteStale _ => []

/-- The Dex schedule that simulates an L schedule label by label, from the
given pair of configurations. It stops at the first label that either side
cannot follow. -/
def simulateLSchedule (program : Program) :
    List L.Label → L.Config × Dex.Config signature → List (Dex.Action signature)
  | [], _ => []
  | label :: laterLabels, state =>
    let actions := dexActionsFor program state.1 state.2 label
    match (L.system program).step state.1 label,
        (Dex.system ⟦program⟧).exec state.2 actions with
    | some (_, lNext), some (_, dexNext) =>
      actions ++ simulateLSchedule program laterLabels (lNext, dexNext)
    | _, _ => []

/-- The Dex schedule that simulates an L schedule from the initial configurations. -/
def dexScheduleOf (program : Program) (labels : List L.Label) : List (Dex.Action signature) :=
  simulateLSchedule program labels ((L.system program).init, (Dex.system ⟦program⟧).init)

/-- A lockstep state: both configurations, or the description of the first mismatch. -/
abbrev Lockstep := Except String (L.Config × Dex.Config signature)

/-- Check that both sides produced the same events and the same clock stamp. -/
def checkAligned (context : String) (lResult : Option (List Event × L.Config))
    (dexResult : Option (List (Dex.Obs signature) × Dex.Config signature)) : Lockstep :=
  match lResult, dexResult with
  | some (lEvents, lConfig), some (dexEvents, dexConfig) =>
    if lEvents != dexEvents.filterMap project then
      .error s!"{context}: events differ: L {repr lEvents}, Dex {repr (dexEvents.filterMap project)}"
    else if lConfig.lastStampAt != dexConfig.lastCommitAt then
      .error s!"{context}: stamps differ: L {repr lConfig.lastStampAt}, Dex {repr dexConfig.lastCommitAt}"
    else if lConfig.clock != dexConfig.clock then .error s!"{context}: clocks differ"
    else .ok (lConfig, dexConfig)
  | none, some _ => .error s!"{context}: the L side is not enabled"
  | some _, none => .error s!"{context}: the Dex side is not enabled"
  | none, none => .error s!"{context}: neither side is enabled"

/-- Run one L label on the L side and its simulating actions on the Dex side. -/
def stepFromL (program : Program) (state : L.Config × Dex.Config signature) (label : L.Label) :
    Lockstep :=
  checkAligned "L-driven step" ((L.system program).exec state.1 [label])
    ((Dex.system ⟦program⟧).exec state.2 (dexActionsFor program state.1 state.2 label))

/-- Run one Dex action on the Dex side and its simulating labels on the L side. -/
def stepFromDex (program : Program) (state : L.Config × Dex.Config signature)
    (action : Dex.Action signature) : Lockstep :=
  checkAligned "Dex-driven step"
    ((L.system program).exec state.1 (lLabelsFor program state.1 state.2 action))
    ((Dex.system ⟦program⟧).exec state.2 [action])

/-- The next value of a linear congruential generator. -/
def nextRandom (seed : Nat) : Nat :=
  (seed * 1103515245 + 12345) % 2147483648

/-- Pick a pseudo-random element. -/
def pick {α : Type} (seed : Nat) (choices : List α) : Option α :=
  if choices.isEmpty then none else choices[(seed / 65536) % choices.length]?

/-- The L labels that could be enabled in a configuration. -/
def allLLabels (inputs : List L.Request) (config : L.Config) : List L.Label :=
  config.threads.map (fun thread => L.Label.thread thread.core.tid) ++
  [L.Label.fire 1, L.Label.fire 2, L.Label.lateDeliver 0, L.Label.lateDrop 0,
   L.Label.lateDeliver 1, L.Label.tick 1, L.Label.tick 1000000] ++
  (inputs.take 1).map L.Label.request

/-- The Dex actions that could be enabled in a configuration. -/
def allDexActions (inputs : List L.Request) (config : Dex.Config signature) :
    List (Dex.Action signature) :=
  (dexCandidates false config) ++
  [Dex.Action.timerFire 2, Dex.Action.tick 1, Dex.Action.tick 1000000] ++
  (inputs.take 1).map (fun request => Dex.Action.request (encodeRequest request))

/-- Drive both sides from pseudo-random L labels. `inputs` are the requests
still to attempt, in order. Returns the L trace, or the first mismatch. -/
def randomFromL (program : Program) :
    Nat → Nat → List L.Request → L.Config × Dex.Config signature → List Event →
      Except String (List Event)
  | 0, _, _, _, trace => .ok trace
  | fuel + 1, seed, inputs, state, trace =>
    let enabled := (allLLabels inputs state.1).filter (fun label =>
      ((L.system program).step state.1 label).isSome)
    match pick seed enabled with
    | none => .ok trace
    | some label =>
      let events := (((L.system program).step state.1 label).map (fun result => result.1)).getD []
      let remainingInputs :=
        match label with
        | .request _ => inputs.drop 1
        | _ => inputs
      match stepFromL program state label with
      | .error message => .error message
      | .ok next =>
        randomFromL program fuel (nextRandom seed) remainingInputs next (trace ++ events)

/-- Drive both sides from pseudo-random Dex actions. Returns the projected
Dex trace, or the first mismatch. -/
def randomFromDex (program : Program) :
    Nat → Nat → List L.Request → L.Config × Dex.Config signature → List Event →
      Except String (List Event)
  | 0, _, _, _, trace => .ok trace
  | fuel + 1, seed, inputs, state, trace =>
    let enabled := (allDexActions inputs state.2).filter (fun action =>
      ((Dex.system ⟦program⟧).step state.2 action).isSome)
    match pick seed enabled with
    | none => .ok trace
    | some action =>
      let events :=
        (((Dex.system ⟦program⟧).step state.2 action).map (fun result => result.1)).getD []
      let remainingInputs :=
        match action with
        | .request _ => inputs.drop 1
        | _ => inputs
      match stepFromDex program state action with
      | .error message => .error message
      | .ok next =>
        randomFromDex program fuel (nextRandom seed) remainingInputs next
          (trace ++ events.filterMap project)

/-- Run `seeds` pseudo-random schedules in both directions. Returns the
mismatches found; an empty list means every run stayed aligned. -/
def lockstepMismatches (program : Program) (inputs : List L.Request) (fuel seeds : Nat) :
    List String :=
  (List.range seeds).flatMap (fun seed =>
    let initial := ((L.system program).init, (Dex.system ⟦program⟧).init)
    let fromL :=
      match randomFromL program fuel (nextRandom (seed + 1)) inputs initial [] with
      | .error message => [s!"seed {seed}, {message}"]
      | .ok _ => []
    let fromDex :=
      match randomFromDex program fuel (nextRandom (seed + 1)) inputs initial [] with
      | .error message => [s!"seed {seed}, {message}"]
      | .ok _ => []
    fromL ++ fromDex)

end DexExpressiveness.Examples
