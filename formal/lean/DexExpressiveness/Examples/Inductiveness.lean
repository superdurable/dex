import DexExpressiveness.Examples.RelatedCheck

/-!
# An inductiveness fuzz of the simulation state

This file is test tooling, not part of the theorem. The step simulations of
`Simulation/*.lean` are proved for every configuration pair that satisfies
`IsSimulationState`, not only for the pairs a run reaches. So
`IsSimulationState` must be inductive: no pair that satisfies it may take a
step that the other side cannot follow into a related pair.

`Examples/RelatedCheck.lean` only visits reachable pairs, which cannot show
that. This file also visits unreachable ones:

1. Run an L program and its translated Flow in lockstep, in both directions,
   and collect the configuration pairs on the way.
2. Change each pair a little (`perturbations`): one field of one side, or
   matching fields of both sides.
3. Keep a changed pair only when the Boolean mirror of `IsSimulationState`
   (`checkState`) accepts it.
4. From every kept pair, take every enabled L label and its simulating Dex
   actions (`simulatingActions`), and every enabled admissible Dex action and its
   simulating L labels (`lLabelsFor`). The other side must be enabled, the
   events and stamps must agree, and `checkState` must accept the result.
5. From every kept pair also walk a few pseudo-random steps in each
   direction, with the same checks.

A failure is a pair that `IsSimulationState` accepts and that breaks a step
simulation: a gap in `Related`, `L.Invariant`, or `ConfigInvariant`. Passing
is evidence, not proof. The Dex-driven direction skips the pairs in which a
thread is about to break a mutex requirement, as `dex_step_simulated` does.
-/

namespace DexExpressiveness.Examples

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation

/-- A configuration pair: an L configuration and a Dex configuration. -/
abbrev ConfigPair := L.Config × Dex.Config signature

/-! ## Changing one element of a list -/

/-- Every list that differs from the given one in one element, which one of
`changes` replaces. -/
def replaceEach {α : Type} (changes : α → List α) : List α → List (List α)
  | [] => []
  | head :: rest =>
    (changes head).map (fun changed => changed :: rest) ++
    (replaceEach changes rest).map (fun changedRest => head :: changedRest)

/-- As `replaceEach`, for changes that carry the name of their perturbation. -/
def replaceEachNamed {α : Type} (changes : α → List (String × α)) :
    List α → List (String × List α)
  | [] => []
  | head :: rest =>
    (changes head).map (fun changed => (changed.1, changed.2 :: rest)) ++
    (replaceEachNamed changes rest).map (fun changedRest =>
      (changedRest.1, head :: changedRest.2))

/-- Every list that lacks one element of the given one. -/
def dropEach {α : Type} : List α → List (List α)
  | [] => []
  | head :: rest => rest :: (dropEach rest).map (fun shorter => head :: shorter)

/-- Give every changed value the name of its perturbation. -/
def named {α : Type} (name : String) (changed : List α) : List (String × α) :=
  changed.map (fun value => (name, value))

/-! ## Perturbations of the L configuration -/

/-- The thread IDs a perturbation may name: the live threads, the next two
children of each live thread, and the ended threads. -/
def threadCandidates (config : L.Config) : List Tid :=
  config.threads.map (fun thread => thread.core.tid) ++
  config.threads.flatMap (fun thread =>
    [thread.core.tid ++ [thread.core.freshCounter],
     thread.core.tid ++ [thread.core.freshCounter + 1]]) ++
  config.joinable

/-- Changed copies of one thread: another phase, another counter, another
timer, a forged handle in the locals, or less code. -/
def threadVariants (config : L.Config) (thread : L.Thread) : List (String × L.Thread) :=
  let phases : List Phase :=
    [.bootPending, .bootRunning, .ready, .running .null, .emitting .null, .emitted, .waitStarted,
     .blocked (.lock (.static 0)) 0, .blocked (.recv 7) config.blockCounter,
     .woken .granted, .woken .canceled, .woken .invalid, .resuming .granted, .halted]
  let tid := thread.core.tid
  let counter := thread.core.freshCounter
  named "L.thread.phase" ((phases.filter (fun phase => phase != thread.phase)).map (fun phase =>
    { thread with phase := phase })) ++
  named "L.thread.freshCounter"
    ([{ thread with core := { thread.core with freshCounter := counter + 1 } }] ++
     (if counter = 0 then []
      else [{ thread with core := { thread.core with freshCounter := counter - 1 } }])) ++
  named "L.thread.timer"
    ((if thread.timer.isSome then [{ thread with timer := none }] else []) ++
     [{ thread with timer := some (config.timerCounter + 1, 0) },
      { thread with timer := some (1, 0) }]) ++
  named "L.thread.locals"
    [{ thread with
        core := { thread.core with locals := (99, Val.thread (tid ++ [counter])) :: thread.core.locals } },
     { thread with
        core := { thread.core with locals := (99, Val.thread [9, 9]) :: thread.core.locals } },
     { thread with
        core := { thread.core with
          locals := (99, Val.mutex (.dynamic tid counter)) :: thread.core.locals } }] ++
  named "L.thread.code" (if thread.code.isEmpty then [] else [{ thread with code := thread.code.drop 1 }])

/-- Changed copies of an L configuration. -/
def lVariants (program : Program) (config : L.Config) : List (String × L.Config) :=
  let tids := config.threads.map (fun thread => thread.core.tid)
  let candidates := threadCandidates config
  let cvs := config.cvQueues.map (fun entry => entry.1) ++ config.cvWakes.map (fun wake => wake.1) ++
    [({ mutex := .static 0, index := 0 } : CvId)]
  named "L.cancelFlags.add" (candidates.map (fun tid =>
    { config with cancelFlags := config.cancelFlags ++ [tid] })) ++
  named "L.cancelFlags.drop" ((dropEach config.cancelFlags).map (fun flags =>
    { config with cancelFlags := flags })) ++
  named "L.joinable.add" (candidates.map (fun tid =>
    { config with joinable := config.joinable ++ [tid] })) ++
  named "L.joinable.drop" ((dropEach config.joinable).map (fun joinable =>
    { config with joinable := joinable })) ++
  named "L.mutexOwners.set" ((replaceEach (fun (entry : MutexId × Option Tid) =>
      ((none :: tids.map some).filter (fun owner => owner != entry.2)).map (fun owner =>
        (entry.1, owner))) config.mutexOwners).map (fun owners =>
    { config with mutexOwners := owners })) ++
  named "L.mutexOwners.drop" ((dropEach config.mutexOwners).map (fun owners =>
    { config with mutexOwners := owners })) ++
  named "L.mutexOwners.add"
    (([(MutexId.static program.staticMutexCount, none),
       (MutexId.static (program.staticMutexCount + 1), some []), (MutexId.static 0, none)] ++
      config.threads.flatMap (fun (thread : L.Thread) =>
        [(MutexId.dynamic thread.core.tid thread.core.freshCounter, none),
         (MutexId.dynamic thread.core.tid 0, none)])).map (fun entry =>
      { config with mutexOwners := config.mutexOwners ++ [entry] })) ++
  named "L.cvQueues.add" (cvs.flatMap (fun cv => (candidates.take 2).map (fun tid =>
    { config with cvQueues := config.cvQueues.set cv ((config.cvQueues.get? cv).getD [] ++ [tid]) }))) ++
  named "L.cvWakes.add" (cvs.flatMap (fun cv => (candidates.take 2).map (fun tid =>
    { config with cvWakes := config.cvWakes ++ [(cv, tid)] }))) ++
  named "L.cvWakes.drop" ((dropEach config.cvWakes).map (fun wakes =>
    { config with cvWakes := wakes })) ++
  named "L.inputQueues.add"
    [{ config with inputQueues := config.inputQueues.set 7 (config.pendingInput 7 ++ [Val.int 1]) },
     { config with inputQueues := config.inputQueues.set 3 (config.pendingInput 3 ++ [Val.thread [9]]) }] ++
  named "L.shared.add"
    [{ config with shared := config.shared.set 99 (Val.int 1) },
     { config with shared := config.shared.set 98 (Val.thread [9, 9]) },
     { config with shared := config.shared.set 97 (Val.mutex (.dynamic [9] 9)) }] ++
  named "L.shared.drop" ((dropEach config.shared).map (fun shared =>
    { config with shared := shared })) ++
  named "L.counters"
    ([{ config with blockCounter := config.blockCounter + 1 },
      { config with timerCounter := config.timerCounter + 1 }] ++
     (if config.blockCounter = 0 then [] else [{ config with blockCounter := config.blockCounter - 1 }]) ++
     (if config.timerCounter = 0 then [] else [{ config with timerCounter := config.timerCounter - 1 }])) ++
  named "L.lateEmits"
    ({ config with lateEmits := config.lateEmits ++ [Val.null] } ::
      (dropEach config.lateEmits).map (fun lateEmits => { config with lateEmits := lateEmits })) ++
  (replaceEachNamed (threadVariants config) config.threads).map (fun variant =>
    (variant.1, { config with threads := variant.2 })) ++
  named "L.threads.drop" ((dropEach config.threads).map (fun threads =>
    { config with threads := threads })) ++
  named "L.threads.duplicate" ((config.threads.take 1).map (fun thread =>
    { config with threads := config.threads ++ [thread] }))

/-! ## Perturbations of the Dex configuration -/

/-- A token on a Channel. -/
def tokenOn (run : Dex.Run signature) (chan : Chan) : Dex.Run signature :=
  run.setPendingMessages chan (run.pendingMessages chan ++ [DexVal.token])

/-- Changed copies of one Step record: another phase, another invocation, a
wait it does not have, other condition results, other options. -/
def recordVariants (config : Dex.Config signature) (run : Dex.Run signature)
    (record : Dex.StepRecord signature) : List (String × Dex.StepRecord signature) :=
  let phases : List Dex.Phase := [.waitFor, .waiting, .execute, .completed, .canceled]
  let self := threadOfInput record.input
  let taskIds := (config.tasks.filter (fun task => decide (task.step = record.id))).map
    (fun task => task.id) ++ [config.nextTaskId]
  let invocations : List (Option Dex.Invocation) :=
    [none, some { method := .waitFor, state := .pending },
     some { method := .execute, state := .pending }] ++
    taskIds.flatMap (fun taskId =>
      [some { method := .waitFor, state := .running taskId },
       some { method := .execute, state := .running taskId }])
  let waits : List (Dex.WaitState signature) :=
    [{ wait := { kind := .all, timerSeconds := [], channels := [Chan.mutexToken (.static 0)] }
       fireAt := [], timersDone := [], registeredSeq := run.seq },
     { wait := { kind := .any, timerSeconds := []
                 channels := [Chan.external 7, Chan.cancelRequest self] }
       fireAt := [], timersDone := [], registeredSeq := 0 },
     { wait := { kind := .any, timerSeconds := [0], channels := [Chan.cancelRequest self] }
       fireAt := [0], timersDone := [false], registeredSeq := run.seq },
     { wait := { kind := .all, timerSeconds := []
                 channels := [Chan.cvWake { mutex := .static 0, index := 0 } self] }
       fireAt := [], timersDone := [], registeredSeq := run.seq }]
  named "Dex.record.phase" ((phases.filter (fun phase => phase != record.phase)).map (fun phase =>
    { record with phase := phase })) ++
  named "Dex.record.invocation" ((invocations.filter (fun invocation =>
    invocation != record.invocation)).map (fun invocation =>
      { record with invocation := invocation })) ++
  named "Dex.record.waiting" (waits.map (fun waitState =>
    { record with phase := .waiting, waitState := some waitState })) ++
  named "Dex.record.waitState"
    ((if record.waitState.isSome then [{ record with waitState := none }] else []) ++
     waits.map (fun waitState => { record with waitState := some waitState })) ++
  named "Dex.record.conditionResults"
    ((if record.conditionResults.isSome then [{ record with conditionResults := none }] else []) ++
     [{ record with conditionResults := some { timersDone := [true], channelMessages := [] } },
      { record with conditionResults := some { timersDone := [], channelMessages := [some .token] } },
      { record with
          conditionResults := some { timersDone := [], channelMessages := [none, some .token] } }]) ++
  named "Dex.record.options"
    [{ record with options := { record.options with skipWaitFor := !record.options.skipWaitFor } },
     { record with
        options := { record.options with
          executeLoadMaps := [MapSelection.cvWaiters { mutex := .static 0, index := 0 }] } },
     { record with options := { record.options with executeLoadMaps := [] } }] ++
  named "Dex.record.input" (if record.input.isSome then [{ record with input := none }] else [])

/-- Changed copies of one task: another phase with the computed response,
other condition results, or an empty snapshot. -/
def taskVariants (program : Program) (task : Dex.Task signature) :
    List (String × Dex.Task signature) :=
  let computed := task.compute ⟦program⟧
  named "Dex.task.phase"
    [{ task with phase := .started },
     { task with phase := .running [] computed.2 },
     { task with phase := .running [.null] computed.2 },
     { task with phase := .running [.null, .null] computed.2 },
     { task with phase := .running computed.1 computed.2 },
     { task with phase := .returned computed.2 }] ++
  named "Dex.task.conditionResults"
    ((if task.conditionResults.isSome then [{ task with conditionResults := none }] else []) ++
     [{ task with conditionResults := some { timersDone := [true], channelMessages := [] } },
      { task with conditionResults := some { timersDone := [], channelMessages := [some .token] } }]) ++
  named "Dex.task.snapshot" [{ task with snapshot := { attributes := [] } }]

/-- The Channels a perturbation may publish to. -/
def chanCandidates (program : Program) (lConfig : L.Config) (run : Dex.Run signature) : List Chan :=
  let candidates := threadCandidates lConfig
  run.channels.map (fun entry => entry.1) ++
  [Chan.mutexToken (.static 0), Chan.mutexToken (.static program.staticMutexCount), Chan.external 7] ++
  lConfig.mutexOwners.map (fun entry => Chan.mutexToken entry.1) ++
  candidates.flatMap (fun tid =>
    [Chan.cancelRequest tid, Chan.joinDone tid,
     Chan.cvWake { mutex := .static 0, index := 0 } tid])

/-- Changed copies of a run. -/
def runVariants (program : Program) (lConfig : L.Config) (config : Dex.Config signature)
    (run : Dex.Run signature) : List (String × Dex.Run signature) :=
  let timedCount := (run.counters.get? StepType.timed).getD 0
  (replaceEachNamed (recordVariants config run) run.steps).map (fun variant =>
    (variant.1, { run with steps := variant.2 })) ++
  named "Dex.records.drop" ((dropEach run.steps).map (fun steps => { run with steps := steps })) ++
  named "Dex.records.duplicate" ((run.steps.take 1).map (fun record =>
    { run with steps := run.steps ++ [record] })) ++
  named "Dex.channels.add" ((chanCandidates program lConfig run).map (tokenOn run)) ++
  named "Dex.channels.drop" (run.channels.filterMap (fun entry =>
    match entry.2 with
    | [] => none
    | _ :: remaining => some (run.setPendingMessages entry.1 remaining))) ++
  named "Dex.timers.duplicate" (run.timers.flatMap (fun entry =>
    [{ run with timers := run.timers ++ [(entry.1, 0)] },
     { run with timers := run.timers ++ [entry] },
     { run with timers := (entry.1, 0) :: run.timers }])) ++
  named "Dex.timers.add"
    [{ run with timers := run.timers ++ [(⟨⟨StepType.timed, timedCount⟩, 0⟩, 0)] },
     { run with timers := run.timers ++ [(⟨⟨StepType.timed, timedCount + 1⟩, 0⟩, 0)] },
     { run with timers := run.timers ++ [(⟨⟨StepType.timed, 1⟩, 1⟩, 0)] }] ++
  named "Dex.timers.drop" ((dropEach run.timers).map (fun timers => { run with timers := timers })) ++
  named "Dex.counters"
    ([{ run with counters := run.counters.set StepType.timed (timedCount + 1) },
      { run with seq := run.seq + 1 }] ++
     (if timedCount = 0 then [] else [{ run with counters := run.counters.set StepType.timed (timedCount - 1) }]) ++
     (if run.seq = 0 then [] else [{ run with seq := run.seq - 1 }])) ++
  named "Dex.attributes.drop" ((dropEach run.attributes).map (fun attributes =>
    { run with attributes := attributes })) ++
  named "Dex.attributes.add"
    [{ run with attributes := run.attributes.set (Key.shared 99) (DexVal.value (.int 1)) },
     { run with
        attributes := run.attributes.set (Key.cvWaiters { mutex := .static 0, index := 0 })
          (DexVal.waiterQueue [[9]]) }] ++
  named "Dex.outputs" [{ run with outputs := run.outputs ++ [(⟨StepType.start, 1⟩, DexVal.token)] }]

/-- Changed copies of a Dex configuration. -/
def dexVariants (program : Program) (lConfig : L.Config) (config : Dex.Config signature) :
    List (String × Dex.Config signature) :=
  (match config.run with
    | none => []
    | some run => (runVariants program lConfig config run).map (fun variant =>
        (variant.1, { config with run := some variant.2 }))) ++
  (replaceEachNamed (taskVariants program) config.tasks).map (fun variant =>
    (variant.1, { config with tasks := variant.2 })) ++
  named "Dex.tasks.drop" ((dropEach config.tasks).map (fun tasks => { config with tasks := tasks })) ++
  named "Dex.tasks.twin" (config.tasks.flatMap (fun task =>
    let computed := task.compute ⟦program⟧
    [{ config with
        tasks := config.tasks ++ [{ task with id := config.nextTaskId, phase := .started }]
        nextTaskId := config.nextTaskId + 1 },
     { config with
        tasks := config.tasks ++ [{ task with id := config.nextTaskId, phase := .returned computed.2 }]
        nextTaskId := config.nextTaskId + 1 },
     { config with tasks := config.tasks ++ [task] }])) ++
  named "Dex.nextTaskId" [{ config with nextTaskId := config.nextTaskId + 1 }]

/-! ## Perturbations of both sides -/

/-- Changed copies of a configuration pair in which both sides change in a
matching way. -/
def pairVariants (program : Program) (pair : ConfigPair) : List (String × ConfigPair) :=
  let lConfig := pair.1
  let dexConfig := pair.2
  let candidates := threadCandidates lConfig
  let withRun : (Dex.Run signature → Dex.Run signature) → Dex.Config signature := fun change =>
    { dexConfig with run := dexConfig.run.map change }
  let cv : CvId := { mutex := .static 0, index := 0 }
  named "both.cancel" (candidates.map (fun tid =>
    ({ lConfig with cancelFlags := lConfig.cancelFlags ++ [tid] },
      withRun (fun run => tokenOn run (Chan.cancelRequest tid))))) ++
  named "both.join" (candidates.map (fun tid =>
    ({ lConfig with joinable := lConfig.joinable ++ [tid] },
      withRun (fun run => tokenOn run (Chan.joinDone tid))))) ++
  named "both.mutex.add"
    (([MutexId.static program.staticMutexCount] ++
      lConfig.threads.map (fun thread =>
        MutexId.dynamic thread.core.tid thread.core.freshCounter)).map (fun mutex =>
      ({ lConfig with mutexOwners := lConfig.mutexOwners ++ [(mutex, none)] },
        withRun (fun run => tokenOn run (Chan.mutexToken mutex))))) ++
  named "both.mutex.unlock" (lConfig.mutexOwners.filterMap (fun entry =>
    match entry.2 with
    | some _ =>
      some ({ lConfig with mutexOwners := lConfig.mutexOwners.set entry.1 none },
        withRun (fun run => tokenOn run (Chan.mutexToken entry.1)))
    | none => none)) ++
  named "both.input"
    [({ lConfig with inputQueues := lConfig.inputQueues.set 7 (lConfig.pendingInput 7 ++ [.int 1]) },
      withRun (fun run => run.setPendingMessages (Chan.external 7)
        (run.pendingMessages (Chan.external 7) ++ [DexVal.value (.int 1)])))] ++
  named "both.cvWake" ((candidates.take 3).map (fun tid =>
    ({ lConfig with cvWakes := lConfig.cvWakes ++ [(cv, tid)] },
      withRun (fun run => tokenOn run (Chan.cvWake cv tid))))) ++
  named "both.cvQueue" ((candidates.take 3).map (fun tid =>
    ({ lConfig with cvQueues := lConfig.cvQueues.set cv ((lConfig.cvQueues.get? cv).getD [] ++ [tid]) },
      withRun (fun run =>
        { run with
          attributes := run.attributes.set (Key.cvWaiters cv)
            (DexVal.waiterQueue ((lConfig.cvQueues.get? cv).getD [] ++ [tid])) })))) ++
  named "both.shared"
    [({ lConfig with shared := lConfig.shared.set 99 (.int 1) },
      withRun (fun run =>
        { run with attributes := run.attributes.set (Key.shared 99) (DexVal.value (.int 1)) }))] ++
  named "both.timerCounter"
    [({ lConfig with timerCounter := lConfig.timerCounter + 1 },
      withRun (fun run =>
        { run with counters := run.counters.set StepType.timed (lConfig.timerCounter + 1) }))] ++
  named "both.clock"
    [({ lConfig with clock := lConfig.clock + 1 }, { dexConfig with clock := dexConfig.clock + 1 }),
     ({ lConfig with lastStampAt := none }, { dexConfig with lastCommitAt := none })] ++
  named "both.lateEffect" ((replaceEach (fun task =>
      [{ task with
          phase := .running (.null :: pendingEffects ⟦program⟧ task) (task.compute ⟦program⟧).2 }])
      dexConfig.tasks).map (fun tasks =>
    ({ lConfig with lateEmits := lConfig.lateEmits ++ [.null] }, { dexConfig with tasks := tasks }))) ++
  -- A task that holds its computed effects again, with the matching late emits.
  named "both.lateEmit.again" ((replaceEach (fun task =>
      [{ task with phase := .started },
       { task with phase := .running (task.compute ⟦program⟧).1 (task.compute ⟦program⟧).2 }])
      dexConfig.tasks).filterMap (fun tasks =>
    let before := dexConfig.tasks.flatMap (pendingEffects ⟦program⟧)
    let after := tasks.flatMap (pendingEffects ⟦program⟧)
    if after.length ≤ before.length then none
    else some ({ lConfig with lateEmits := lConfig.lateEmits ++ after.drop before.length },
      { dexConfig with tasks := tasks }))) ++
  -- A second task with the same effects, with the matching late emits.
  named "both.lateEmit.twin" (dexConfig.tasks.filterMap (fun task =>
    if (task.compute ⟦program⟧).1.isEmpty then none
    else some ({ lConfig with lateEmits := lConfig.lateEmits ++ (task.compute ⟦program⟧).1 },
      { dexConfig with
        tasks := dexConfig.tasks ++ [{ task with id := dexConfig.nextTaskId, phase := .started }]
        nextTaskId := dexConfig.nextTaskId + 1 }))) ++
  named "both.lateEffects" ((replaceEach (fun task =>
      [{ task with
          phase := .running (.null :: .null :: pendingEffects ⟦program⟧ task)
            (task.compute ⟦program⟧).2 }])
      dexConfig.tasks).map (fun tasks =>
    ({ lConfig with lateEmits := lConfig.lateEmits ++ [.null, .null] },
      { dexConfig with tasks := tasks })))

/-- The named changed copies of a configuration pair. -/
def perturbations (program : Program) (pair : ConfigPair) : List (String × ConfigPair) :=
  (lVariants program pair.1).map (fun variant => (variant.1, (variant.2, pair.2))) ++
  (dexVariants program pair.1 pair.2).map (fun variant => (variant.1, (pair.1, variant.2))) ++
  pairVariants program pair

/-! ## The steps to try -/

/-- The requests the fuzz offers. -/
def fuzzRequests : List L.Request :=
  [.start (.int 3), .input 7 (.int 50), .input 3 (.int 9)]

/-- The L labels the fuzz tries. -/
def fuzzLLabels (config : L.Config) : List L.Label :=
  config.threads.map (fun thread => L.Label.thread thread.core.tid) ++
  [L.Label.fire 1, L.Label.fire 2, L.Label.fire 3, L.Label.lateDeliver 0, L.Label.lateDeliver 1,
   L.Label.lateDeliver 2, L.Label.lateDrop 0, L.Label.lateDrop 1, L.Label.lateDrop 2,
   L.Label.tick 1, L.Label.tick 1000000] ++
  fuzzRequests.map L.Label.request

/-- The Dex actions the fuzz tries: those an admissible run can take. -/
def fuzzDexActions (config : Dex.Config signature) : List (Dex.Action signature) :=
  dexCandidates false config ++
  [Dex.Action.timerFire 2, Dex.Action.timerFire 3, Dex.Action.tick 1, Dex.Action.tick 1000000] ++
  (fuzzRequests.filter (fun request =>
    match request with
    | .start _ => true
    | .input _ _ => config.run.isSome)).map (fun request =>
      Dex.Action.request (encodeRequest request))

/-- A short name of an L label. -/
def describeLabel : L.Label → String
  | .tick duration => s!"tick {duration}"
  | .request (.start _) => "start"
  | .request (.input chan _) => s!"input {chan}"
  | .thread thread => s!"thread {thread}"
  | .fire count => s!"fire {count}"
  | .lateDeliver index => s!"lateDeliver {index}"
  | .lateDrop index => s!"lateDrop {index}"

/-- A short name of a Dex action. -/
def describeAction : Dex.Action signature → String
  | .tick duration => s!"tick {duration}"
  | .request (.startFlow _ _) => "startFlow"
  | .request (.publish _ _) => "publish"
  | .stepStart step => s!"stepStart {step.number}"
  | .workerRun taskId => s!"workerRun {taskId}"
  | .workerEffect taskId => s!"workerEffect {taskId}"
  | .workerReturn taskId => s!"workerReturn {taskId}"
  | .workerAbandon taskId => s!"workerAbandon {taskId}"
  | .stepComplete taskId => s!"stepComplete {taskId}"
  | .stepCompleteStale taskId => s!"stepCompleteStale {taskId}"
  | .timerFire count => s!"timerFire {count}"

/-- The enabled L labels among those the fuzz tries. -/
def enabledLLabels (program : Program) (config : L.Config) : List L.Label :=
  (fuzzLLabels config).filter (fun label => ((L.system program).step config label).isSome)

/-- The enabled Dex actions among those the fuzz tries. -/
def enabledDexActions (program : Program) (config : Dex.Config signature) :
    List (Dex.Action signature) :=
  (fuzzDexActions config).filter (fun action =>
    ((Dex.system ⟦program⟧).step config action).isSome)

/-- The first failure of one step in lockstep: the other side is not
enabled, the events or stamps differ, or `checkState` rejects the result. -/
def checkedStep (program : Program) (context : String) (result : Lockstep) :
    Except String ConfigPair :=
  match result with
  | .error message => .error s!"{context}: {message}"
  | .ok next =>
    match checkState program next with
    | failure :: _ => .error s!"{context}: {failure}"
    | [] => .ok next

/-- The failures of every single step from a pair, in both directions. -/
def singleStepFailures (program : Program) (pair : ConfigPair) : List String :=
  (enabledLLabels program pair.1).filterMap (fun label =>
    match checkedStep program s!"L {describeLabel label}" (stepFromL program pair label) with
    | .error message => some message
    | .ok _ => none) ++
  (if breaksMutexRequirement pair.1 then []
   else (enabledDexActions program pair.2).filterMap (fun action =>
    match checkedStep program s!"Dex {describeAction action}" (stepFromDex program pair action) with
    | .error message => some message
    | .ok _ => none))

/-- The first failure of a pseudo-random walk driven by L labels. -/
def walkFromL (program : Program) : Nat → Nat → ConfigPair → List String
  | 0, _, _ => []
  | fuel + 1, seed, pair =>
    match pick seed (enabledLLabels program pair.1) with
    | none => []
    | some label =>
      match checkedStep program s!"L walk {describeLabel label}" (stepFromL program pair label) with
      | .error message => [message]
      | .ok next => walkFromL program fuel (nextRandom seed) next

/-- The first failure of a pseudo-random walk driven by Dex actions. It stops
when a thread is about to break a mutex requirement. -/
def walkFromDex (program : Program) : Nat → Nat → ConfigPair → List String
  | 0, _, _ => []
  | fuel + 1, seed, pair =>
    if breaksMutexRequirement pair.1 then []
    else
      match pick seed (enabledDexActions program pair.2) with
      | none => []
      | some action =>
        match checkedStep program s!"Dex walk {describeAction action}"
            (stepFromDex program pair action) with
        | .error message => [message]
        | .ok next => walkFromDex program fuel (nextRandom seed) next

/-! ## Reachable pairs -/

/-- The pairs of a pseudo-random lockstep run. `isDexDriven` picks the side
that chooses the steps. The run stops at the first step that fails. -/
def reachablePairs (program : Program) (isDexDriven : Bool) : Nat → Nat → ConfigPair → List ConfigPair
  | 0, _, pair => [pair]
  | fuel + 1, seed, pair =>
    let next : Option ConfigPair :=
      if isDexDriven then
        (pick seed (enabledDexActions program pair.2)).bind (fun action =>
          (stepFromDex program pair action).toOption)
      else
        (pick seed (enabledLLabels program pair.1)).bind (fun label =>
          (stepFromL program pair label).toOption)
    match next with
    | none => [pair]
    | some reached => pair :: reachablePairs program isDexDriven fuel (nextRandom seed) reached

/-- Every `period`-th element of a list, from index `offset`. -/
def everyNth {α : Type} (period offset : Nat) (list : List α) : List α :=
  (list.zipIdx.filter (fun entry => entry.2 % period == offset % period)).map (fun entry => entry.1)

/-! ## The fuzz -/

/-- The pairs the fuzz changes: every `period`-th pair of one pseudo-random
run in each direction. -/
def sampledPairs (program : Program) (fuel period seed : Nat) : List ConfigPair :=
  let initial : ConfigPair := ((L.system program).init, (Dex.system ⟦program⟧).init)
  everyNth period seed (reachablePairs program false fuel (nextRandom (seed + 1)) initial) ++
  everyNth period seed (reachablePairs program true (2 * fuel) (nextRandom (seed + 1)) initial)

/-- The changed copies of a pair that `checkState` accepts. -/
def keptPerturbations (program : Program) (pair : ConfigPair) : List (String × ConfigPair) :=
  (perturbations program pair).filter (fun variant => (checkState program variant.2).isEmpty)

/-- The failures of the changed copies of one pair: for each copy that
`checkState` accepts, the first failure among every single step and one short
walk in each direction. -/
def perturbedFailures (program : Program) (walkLength seed : Nat) (pair : ConfigPair) :
    List String :=
  (keptPerturbations program pair).flatMap (fun variant =>
    ((singleStepFailures program variant.2 ++
      walkFromL program walkLength (nextRandom seed) variant.2 ++
      walkFromDex program walkLength (nextRandom (seed + 1)) variant.2).take 1).map
      (fun failure => s!"{variant.1}: {failure}"))

/-- Run the fuzz on `seeds` pseudo-random runs in each direction, taking every
`period`-th pair. Returns the failures, without repetitions; an empty list
means that no accepted pair broke a step. -/
def inductivenessFailures (program : Program) (fuel period walkLength seeds : Nat) :
    List String :=
  ((List.range seeds).flatMap (fun seed =>
    (sampledPairs program fuel period seed).flatMap
      (perturbedFailures program walkLength seed))).eraseDups

/-! ## Coverage -/

/-- The name of a thread phase. -/
def phaseName : Phase → String
  | .bootPending => "bootPending"
  | .bootRunning => "bootRunning"
  | .ready => "ready"
  | .running _ => "running"
  | .emitting _ => "emitting"
  | .emitted => "emitted"
  | .waitStarted => "waitStarted"
  | .blocked _ _ => "blocked"
  | .woken _ => "woken"
  | .resuming _ => "resuming"
  | .halted => "halted"

/-- The case of the L-to-Dex step lemmas that an L label falls in: the kind
of the label and, for a thread, its phase. -/
def labelKind (config : L.Config) : L.Label → String
  | .tick _ => "tick"
  | .request (.start _) => "start"
  | .request (.input _ _) => if config.status = .closed then "input (refused)" else "input"
  | .thread thread =>
    match config.findThread? thread with
    | some current => "thread " ++ phaseName current.phase
    | none => "thread"
  | .fire _ => "fire"
  | .lateDeliver _ => "lateDeliver"
  | .lateDrop _ => "lateDrop"

/-- The kind of a Dex action. -/
def actionKind (config : Dex.Config signature) : Dex.Action signature → String
  | .tick _ => "tick"
  | .request (.startFlow _ _) => "startFlow"
  | .request (.publish _ _) => "publish"
  | .stepStart _ => "stepStart"
  | .workerRun _ => "workerRun"
  | .workerEffect taskId =>
    match config.findTask? taskId with
    | some task => if config.isTaskCurrent task then "workerEffect" else "workerEffect (stale)"
    | none => "workerEffect"
  | .workerReturn _ => "workerReturn"
  | .workerAbandon _ => "workerAbandon"
  | .stepComplete _ => "stepComplete"
  | .stepCompleteStale _ => "stepCompleteStale"
  | .timerFire _ => "timerFire"

/-- The kinds of the single steps that the fuzz checks from one pair. -/
def checkedStepKinds (program : Program) (pair : ConfigPair) : List String :=
  (enabledLLabels program pair.1).map (fun label => "L " ++ labelKind pair.1 label) ++
  (if breaksMutexRequirement pair.1 then []
   else (enabledDexActions program pair.2).map (fun action => "Dex " ++ actionKind pair.2 action))

/-- How often each element occurs. -/
def countOccurrences (elements : List String) : AssocList String Nat :=
  elements.foldl (fun counts element => counts.set element ((counts.get? element).getD 0 + 1)) []

/-- What one fuzz run covers. -/
structure FuzzCoverage where
  /-- The number of reachable pairs that were changed. -/
  sampledPairs : Nat
  /-- The number of changed copies. -/
  triedPerturbations : Nat
  /-- The number of changed copies that `checkState` accepts, by perturbation. -/
  keptByPerturbation : AssocList String Nat
  /-- The number of single steps checked from accepted copies, by kind. -/
  checkedSteps : AssocList String Nat

/-- The coverage of the fuzz run with the same arguments. -/
def fuzzCoverage (program : Program) (fuel period seeds : Nat) : FuzzCoverage :=
  let pairs := (List.range seeds).flatMap (fun seed => sampledPairs program fuel period seed)
  let kept := pairs.flatMap (keptPerturbations program)
  { sampledPairs := pairs.length
    triedPerturbations := (pairs.map (fun pair => (perturbations program pair).length)).sum
    keptByPerturbation := countOccurrences (kept.map (fun variant => variant.1))
    checkedSteps := countOccurrences (kept.flatMap (fun variant =>
      checkedStepKinds program variant.2)) }

/-! ## The checks

The checks run in the Lean interpreter when the file is built, so they take
few pairs. `lake exe inductiveness-fuzz` runs the same fuzz compiled, on many
more pairs, and prints the coverage; the README gives its last result. -/

open DexExpressiveness.Examples.LockstepChecks in
#guard inductivenessFailures Mutex.program 60 12 3 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard inductivenessFailures TimedSelect.program 60 12 3 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard inductivenessFailures signalProgram 80 16 3 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard inductivenessFailures selectRaceProgram 80 16 3 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard inductivenessFailures lateEmitProgram 40 8 3 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard inductivenessFailures cancelProgram 60 12 3 1 == []

end DexExpressiveness.Examples
