import DexExpressiveness.Examples.LockstepChecks
import DexExpressiveness.Examples.ForgedHandleInput

/-!
# Executable checks of the simulation state on pseudo-random runs

This file is test tooling, not part of the theorem. `checkLInvariant`,
`checkRelated`, and `checkDexInvariant` are Boolean mirrors of `L.Invariant`
(`L/Invariant.lean`), `Related` (`Simulation/Relation.lean`), and
`ConfigInvariant`, `TasksAreDistinct`, and `TimersAreDistinct`
(`Invariants.lean`). Together they mirror `IsSimulationState`. They return
the names of the clauses that fail. The clauses that quantify over every name
are checked for the names that occur on either side.

A mirror must accept exactly what its clause accepts on the checked names. A
stricter mirror hides gaps from `Examples/Inductiveness.lean`: the `timers`
mirror once checked every binding of the timer list, where `Related.timers`
reads only the first binding of a timer ID.

Each `#guard` runs an L program and its translated Flow in lockstep
(`Examples/Lockstep.lean`), in both directions, and checks the predicates in
every configuration pair the run reaches. A failure means that a clause is
wrong for a reachable pair, so the step simulations could not be proved with
it. Passing does not show that the predicates are inductive:
`Examples/Inductiveness.lean` tests that, and the work packages prove it.
-/

namespace DexExpressiveness.Examples

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation

/-- The name of a clause when it fails. -/
def failsUnless (name : String) (holds : Bool) : List String :=
  if holds then [] else [name]

/-! ## `L.Invariant` -/

/-- The index that follows `shorter` in `longer`, when `shorter` is a proper prefix. -/
def nextIndexAfter (shorter longer : Tid) : Option Nat :=
  if shorter.isPrefixOf longer then longer[shorter.length]? else none

/-- No `tid ++ [index]` with `freshCounter ≤ index` is a prefix of `other`. -/
def isFreshFor (tid : Tid) (freshCounter : Nat) (other : Tid) : Bool :=
  match nextIndexAfter tid other with
  | some index => decide (index < freshCounter)
  | none => true

/-- Mirror of `Config.IsExistingMutex`. -/
def isExistingMutex (config : L.Config) : MutexId → Bool
  | .static _ => true
  | .dynamic creator index => config.mutexOwners.contains (.dynamic creator index)

/-- Mirror of `Config.IsValidVal`. -/
def isValidVal (config : L.Config) : Val → Bool
  | .mutex mutex => isExistingMutex config mutex
  | .cv cv => isExistingMutex config cv.mutex
  | _ => true

/-- Mirror of `Config.IsValidInstr`. -/
def isValidInstr (config : L.Config) : Instr → Bool
  | .source _ => true
  | .park cv => isExistingMutex config cv.mutex
  | .relock mutex => isExistingMutex config mutex

/-- Mirror of `Config.IsValidFrame`. -/
def isValidFrame (config : L.Config) : Frame → Bool
  | .call _ savedLocals returnCode =>
    savedLocals.all (fun entry => isValidVal config entry.2) &&
    returnCode.all (isValidInstr config)
  | .handler _ handlerCode afterCode =>
    handlerCode.all (isValidInstr config) && afterCode.all (isValidInstr config)

/-- Mirror of `Config.IsValidPhase`. -/
def isValidPhase (config : L.Config) : Phase → Bool
  | .running loaded => isValidVal config loaded
  | .blocked (.lock mutex) _ => isExistingMutex config mutex
  | .blocked (.park cv) _ => isExistingMutex config cv.mutex
  | .woken (.received message) => isValidVal config message
  | .resuming (.received message) => isValidVal config message
  | _ => true

/-- Mirror of `Config.IsValidThread`. -/
def isValidThread (config : L.Config) (thread : L.Thread) : Bool :=
  thread.core.locals.all (fun entry => isValidVal config entry.2) &&
  thread.core.stack.all (isValidFrame config) &&
  thread.code.all (isValidInstr config) &&
  isValidPhase config thread.phase

/-- Whether the head statement is an `emit` whose operand evaluates, to
`effect` when one is given. -/
def isEmitOf (core : ThreadCore) (code : Code) (effect : Option Val) : Bool :=
  match beginKind core code, effect with
  | .emit actual, some expected => actual == expected
  | .emit _, none => true
  | _, _ => false

/-- Mirror of `Thread.PhaseFitsCode`. -/
def phaseFitsCode (thread : L.Thread) : Bool :=
  let isExecuteOnly := thread.timer.isNone && !isBlockingHead thread.code
  let isWait := thread.timer.isSome || isBlockingHead thread.code
  match thread.phase with
  | .bootPending => true
  | .bootRunning => true
  | .ready => true
  | .halted => true
  | .running _ => isExecuteOnly && !isEmitOf thread.core thread.code none
  | .emitting effect => isExecuteOnly && isEmitOf thread.core thread.code (some effect)
  | .emitted => isExecuteOnly && isEmitOf thread.core thread.code none
  | .waitStarted => isWait
  | .woken _ => isWait
  | .resuming _ => isWait
  | .blocked (.timed number _ chan) _ =>
    (match thread.timer with
      | some (timerNumber, _) => timerNumber == number
      | none => false) &&
    decide (timedChan thread.code = some chan)
  | .blocked on _ =>
    thread.timer.isNone && decide (waitRequest thread.core thread.code = on.toWaitRequest?)

/-- The `since` stamp of a blocked thread. -/
def sinceOf (thread : L.Thread) : Option Nat :=
  match thread.phase with
  | .blocked _ since => some since
  | _ => none

/-- The names of the clauses of `L.Invariant` that fail. -/
def checkLInvariant (program : Program) (config : L.Config) : List String :=
  let threads := config.threads
  let isBooting : L.Thread → Bool := fun thread =>
    match thread.phase with
    | .bootPending => true
    | .bootRunning => true
    | _ => false
  let createdMutexes := config.mutexOwners.filterMap (fun entry =>
    match entry.1 with
    | .dynamic creator index => some (creator, index)
    | .static _ => none)
  failsUnless "notStartedIsInitial" (config.status != .notStarted ||
    (threads.isEmpty && config.shared.isEmpty && config.mutexOwners.isEmpty &&
      config.cvQueues.isEmpty && config.cvWakes.isEmpty && config.inputQueues.isEmpty &&
      config.cancelFlags.isEmpty && config.joinable.isEmpty && config.timerCounter == 0)) ++
  failsUnless "closedHasNoThread" (config.status != .closed || threads.isEmpty) ++
  failsUnless "threadIdsNodup" (decide (threads.map (fun thread => thread.core.tid)).Nodup) ++
  failsUnless "threadIdsFresh" (threads.all (fun first => threads.all (fun second =>
    isFreshFor first.core.tid first.core.freshCounter second.core.tid))) ++
  failsUnless "joinableNodup" (decide config.joinable.Nodup) ++
  failsUnless "joinableFresh" (threads.all (fun thread =>
    !config.joinable.contains thread.core.tid &&
    config.joinable.all (fun ended =>
      isFreshFor thread.core.tid thread.core.freshCounter ended))) ++
  failsUnless "mutexesFresh" (threads.all (fun thread => createdMutexes.all (fun created =>
    (created.1 != thread.core.tid || decide (created.2 < thread.core.freshCounter)) &&
    isFreshFor thread.core.tid thread.core.freshCounter created.1))) ++
  failsUnless "handlesExist" (threads.all (isValidThread config) &&
    config.shared.all (fun entry => isValidVal config entry.2) &&
    config.inputQueues.all (fun entry => entry.2.all (isValidVal config))) ++
  failsUnless "bootIsInitial" (threads.all (fun thread => !isBooting thread ||
    (threads.length == 1 && thread.timer.isNone &&
      decide (thread.code = pushCode program.mainBody []) &&
      decide (thread.core.tid = []) && thread.core.freshCounter == 0 &&
      thread.core.stack.isEmpty &&
      (match thread.core.locals with
        | [(name, _)] => name == program.mainParam
        | _ => false)))) ++
  failsUnless "sinceBelowCounter" (threads.all (fun thread =>
    match sinceOf thread with
    | some since => decide (since < config.blockCounter)
    | none => true)) ++
  failsUnless "sinceInjective" (decide (threads.filterMap sinceOf).Nodup) ++
  failsUnless "timerNumbersCounted" (threads.all (fun thread =>
    match thread.timer with
    | some (number, _) => decide (0 < number) && decide (number ≤ config.timerCounter)
    | none => true)) ++
  failsUnless "timerNumbersInjective" (decide (threads.filterMap (fun thread =>
    thread.timer.map (fun timer => timer.1))).Nodup) ++
  failsUnless "phasesFitCode" (threads.all phaseFitsCode) ++
  failsUnless "runningHoldsRequiredMutex" (threads.all (fun thread =>
    match thread.phase with
    | .running _ =>
      (match requiredMutex thread.core thread.code with
        | some mutex => decide (config.mutexOwners.get? mutex = some (some thread.core.tid))
        | none => true)
    | _ => true))

/-! ## `Related` -/

/-- Equality of Step inputs. -/
def isSameInput : Option DexVal → Option DexVal → Bool
  | none, none => true
  | some (.value first), some (.value second) => first == second
  | some (.exitTag first), some (.exitTag second) => first == second
  | some .token, some .token => true
  | some (.waiterQueue first), some (.waiterQueue second) => first == second
  | some (.thread first), some (.thread second) => decide (first = second)
  | some (.timedThread firstCore firstCode firstSeconds),
    some (.timedThread secondCore secondCode secondSeconds) =>
    decide (firstCore = secondCore) && decide (firstCode = secondCode) &&
    firstSeconds == secondSeconds
  | _, _ => false

/-- Mirror of `StepCarriesThread`. -/
def carriesThread (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) : Bool :=
  let isBootRecord : Bool :=
    decide (record.id.stepType = StepType.start) &&
    (match record.input with
      | some (.value input) => decide (thread.core.locals = [(program.mainParam, input)])
      | _ => false)
  match thread.phase, thread.timer with
  | .bootPending, _ => isBootRecord
  | .bootRunning, _ => isBootRecord
  | _, some (number, seconds) =>
    decide (record.id.stepType = StepType.timed) && record.id.number == number &&
    isSameInput record.input (some (.timedThread thread.core thread.code seconds))
  | _, none =>
    decide (record.id.stepType = StepType.point thread.code) &&
    isSameInput record.input (some (.thread thread.core)) &&
    decide (record.options.executeLoadMaps = executeLoadMaps thread.core thread.code)

/-- Mirror of `HasRunningTask`. -/
def hasRunningTask (config : Dex.Config signature) (record : Dex.StepRecord signature)
    (method : Dex.Method) (holds : Dex.Task signature → Bool) : Bool :=
  config.tasks.any (fun task =>
    decide (record.invocation = some { method := method, state := .running task.id }) &&
    decide (task.step = record.id) && decide (task.method = method) &&
    isSameInput task.input record.input && holds task)

/-- Mirror of `PhaseMatchesStep`. -/
def phaseMatchesStep (program : Program) (lConfig : L.Config) (config : Dex.Config signature)
    (thread : L.Thread) (record : Dex.StepRecord signature) : Bool :=
  let waitsFirst : Bool := thread.timer.isSome || isBlockingHead thread.code
  let hasNoEffect : Dex.Task signature → Bool := fun task =>
    (pendingEffects ⟦program⟧ task).isEmpty
  let isExecutePhase : Bool := decide (record.phase = .execute)
  match thread.phase with
  | .bootPending =>
    isExecutePhase &&
    decide (record.invocation = some { method := .execute, state := .pending })
  | .bootRunning => isExecutePhase && hasRunningTask config record .execute hasNoEffect
  | .ready =>
    decide (record.phase = (if waitsFirst then .waitFor else .execute)) &&
    record.conditionResults.isNone &&
    decide (record.invocation =
      some { method := if waitsFirst then .waitFor else .execute, state := .pending })
  | .running loaded =>
    isExecutePhase &&
    hasRunningTask config record .execute (fun task =>
      hasNoEffect task && task.conditionResults.isNone &&
      (match beginKind thread.core thread.code with
        | .load source => readShared task.snapshot source == loaded
        | _ => true) &&
      (match cvOperand thread.core thread.code with
        | some cv => readWaiters task.snapshot cv == (lConfig.cvQueues.get? cv).getD []
        | none => true))
  | .emitting effect =>
    isExecutePhase &&
    hasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task == [effect] && task.conditionResults.isNone)
  | .emitted =>
    isExecutePhase &&
    hasRunningTask config record .execute (fun task =>
      hasNoEffect task && task.conditionResults.isNone)
  | .waitStarted =>
    decide (record.phase = .waitFor) && hasRunningTask config record .waitFor hasNoEffect
  | .blocked on _ =>
    decide (record.phase = .waiting) && record.invocation.isNone &&
    (match record.waitState with
      | some waitState =>
        decide (waitState.wait.kind = (expectedWaitShape thread.core.tid on).1) &&
        waitState.wait.timerSeconds.length == (expectedWaitShape thread.core.tid on).2.1 &&
        decide (waitState.wait.channels = (expectedWaitShape thread.core.tid on).2.2) &&
        waitState.timersDone == waitState.wait.timerSeconds.map (fun _ => false)
      | none => false)
  | .woken outcome =>
    decide (record.phase = .execute) &&
    decide (record.invocation = some { method := .execute, state := .pending }) &&
    decide (decodeOutcome thread record.conditionResults = outcome)
  | .resuming outcome =>
    isExecutePhase &&
    hasRunningTask config record .execute (fun task =>
      hasNoEffect task && decide (decodeOutcome thread task.conditionResults = outcome))
  | .halted => false

/-- The names of the clauses of `RunningRelated` that fail. -/
def checkRunningRelated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (run : Dex.Run signature) : List String :=
  let attributes : Dex.Snapshot signature := { attributes := run.attributes }
  let threads := lConfig.threads
  let activeRecords := run.steps.filter (fun record => record.phase.isActive)
  let liveThreads := threads.filter (fun thread => decide (thread.phase ≠ .halted))
  let sharedNames := run.attributes.filterMap (fun entry =>
      match entry.1 with
      | Key.shared name => some name
      | _ => none) ++ lConfig.shared.map (fun entry => entry.1)
  let cvs := run.attributes.filterMap (fun entry =>
      match entry.1 with
      | Key.cvWaiters cv => some cv
      | _ => none) ++ lConfig.cvQueues.map (fun entry => entry.1)
  let chans := run.channels.map (fun entry => entry.1)
  let externalChans := chans.filterMap (fun chan =>
      match chan with
      | Chan.external external => some external
      | _ => none) ++ lConfig.inputQueues.map (fun entry => entry.1)
  let mutexes := chans.filterMap (fun chan =>
      match chan with
      | Chan.mutexToken mutex => some mutex
      | _ => none) ++ lConfig.mutexOwners.map (fun entry => entry.1)
  let joinedThreads := chans.filterMap (fun chan =>
      match chan with
      | Chan.joinDone thread => some thread
      | _ => none) ++ lConfig.joinable
  let canceledThreads := chans.filterMap (fun chan =>
      match chan with
      | Chan.cancelRequest thread => some thread
      | _ => none)
  let wakes := chans.filterMap (fun chan =>
      match chan with
      | Chan.cvWake cv waiter => some (cv, waiter)
      | _ => none) ++ lConfig.cvWakes
  let hasBooted := threads.all (fun thread =>
    decide (thread.phase ≠ .bootPending) && decide (thread.phase ≠ .bootRunning))
  let blockedPairs := threads.filterMap (fun thread =>
    match thread.phase with
    | .blocked _ since =>
      (activeRecords.find? (carriesThread program thread)).bind (fun record =>
        record.waitState.map (fun waitState => (since, waitState.registeredSeq)))
    | _ => none)
  failsUnless "shared" (sharedNames.all (fun name =>
    readShared attributes name == lConfig.readShared name)) ++
  failsUnless "cvQueues" (cvs.all (fun cv =>
    readWaiters attributes cv == (lConfig.cvQueues.get? cv).getD [])) ++
  failsUnless "inputQueues" (externalChans.all (fun chan =>
    (run.pendingMessages (Chan.external chan)).length == (lConfig.pendingInput chan).length &&
    ((run.pendingMessages (Chan.external chan)).zip (lConfig.pendingInput chan)).all
      (fun pair => isSameInput (some pair.1) (some (DexVal.value pair.2))))) ++
  failsUnless "mutexTokens" (!hasBooted || mutexes.all (fun mutex =>
    (run.pendingMessages (Chan.mutexToken mutex)).length ==
      (if lConfig.mutexOwners.get? mutex = some none then 1 else 0))) ++
  failsUnless "mutexesBeforeBoot" (hasBooted ||
    (decide (lConfig.mutexOwners =
        (List.range program.staticMutexCount).map (fun index => (MutexId.static index, none))) &&
      mutexes.all (fun mutex => (run.pendingMessages (Chan.mutexToken mutex)).isEmpty))) ++
  failsUnless "joinTokens" (joinedThreads.all (fun thread =>
    (run.pendingMessages (Chan.joinDone thread)).length ==
      (if lConfig.joinable.contains thread then 1 else 0))) ++
  failsUnless "cancelRequestsAreFlags" (canceledThreads.all (fun thread =>
    (run.pendingMessages (Chan.cancelRequest thread)).isEmpty ||
    lConfig.cancelFlags.contains thread)) ++
  failsUnless "cancelFlagsAreRequests" (lConfig.cancelFlags.all (fun thread =>
    !(run.pendingMessages (Chan.cancelRequest thread)).isEmpty ||
    (match lConfig.findThread? thread with
      | none =>
        threads.all (fun live => isFreshFor live.core.tid live.core.freshCounter thread)
      | some current =>
        decide (current.phase = .woken .canceled) || decide (current.phase = .resuming .canceled)))) ++
  failsUnless "cvWakes" (wakes.all (fun wake =>
    (run.pendingMessages (Chan.cvWake wake.1 wake.2)).length == lConfig.cvWakes.count wake)) ++
  failsUnless "timers" (run.timers.all (fun entry =>
      -- Only the first binding of a key counts, as in `AssocList.get?`.
      match run.timers.get? entry.1 with
      | none => true
      | some fireTime =>
        decide (entry.1.step.stepType = StepType.timed) && entry.1.index == 0 &&
        threads.any (fun thread =>
          match thread.phase with
          | .blocked (.timed number deadline _) _ =>
            number == entry.1.step.number && deadline == fireTime
          | _ => false)) &&
    threads.all (fun thread =>
      match thread.phase with
      | .blocked (.timed number deadline _) _ =>
        decide (run.timers.get?
          (⟨⟨StepType.timed, number⟩, 0⟩ : Dex.TimerId signature) = some deadline)
      | _ => true)) ++
  failsUnless "timerCounter"
    ((run.counters.get? StepType.timed).getD 0 == lConfig.timerCounter) ++
  failsUnless "outputsEmpty" run.outputs.isEmpty ++
  failsUnless "threadsHaveSteps" (liveThreads.all (fun thread =>
    activeRecords.any (fun record =>
      carriesThread program thread record &&
      phaseMatchesStep program lConfig dexConfig thread record))) ++
  failsUnless "stepsHaveThreads" (activeRecords.all (fun record =>
    liveThreads.any (fun thread => carriesThread program thread record))) ++
  failsUnless "activeStepsHaveDistinctThreads"
    (decide (activeRecords.map (fun record => threadOfInput record.input)).Nodup) ++
  failsUnless "invocationsAreActive" (run.steps.all (fun record =>
    record.invocation.isNone || record.phase.isActive)) ++
  failsUnless "blockingOrder" (blockedPairs.all (fun first => blockedPairs.all (fun second =>
    decide (first.1 < second.1) == decide (first.2 < second.2)))) ++
  failsUnless "tasksAreCurrent" (dexConfig.tasks.all dexConfig.isTaskCurrent)

/-- The names of the clauses of `Related` that fail. -/
def checkRelated (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature) :
    List String :=
  failsUnless "clock" (lConfig.clock == dexConfig.clock) ++
  failsUnless "stamp" (lConfig.lastStampAt == dexConfig.lastCommitAt) ++
  (match lConfig.status, dexConfig.run with
    | .notStarted, none => failsUnless "notStarted" dexConfig.tasks.isEmpty
    | .running, some run =>
      failsUnless "status" (decide (run.status = .running)) ++
      checkRunningRelated program lConfig dexConfig run
    | .closed, some run =>
      failsUnless "status" (decide (run.status ≠ .running)) ++
      failsUnless "lateEmits"
        ((dexConfig.tasks.flatMap (pendingEffects ⟦program⟧)).isPerm lConfig.lateEmits) ++
      failsUnless "tasksAreStale" (dexConfig.tasks.all (fun task => !dexConfig.isTaskCurrent task)) ++
      failsUnless "noInvocation" (run.steps.all (fun record => record.invocation.isNone)) ++
      failsUnless "noTimer" run.timers.isEmpty
    | _, _ => ["status"])

/-! ## `ConfigInvariant`, `TasksAreDistinct`, and `TimersAreDistinct` -/

/-- Mirror of `Dex.IsSupportedWait`. -/
def isSupportedWait (wait : Dex.Wait signature) : Bool :=
  wait.hasNoCondition || decide (wait.kind = .any) ||
  (decide (wait.kind = .all) && wait.timerSeconds.isEmpty && wait.channels.length == 1)

/-- The waiting Steps of a run with their wait states: mirror of `Dex.Run.IsWaiting`. -/
def waitersOf (run : Dex.Run signature) :
    List (Dex.StepRecord signature × Dex.WaitState signature) :=
  run.steps.filterMap (fun record =>
    match record.phase, record.waitState with
    | .waiting, some waitState => some (record, waitState)
    | _, _ => none)

/-- Mirror of `IsTaskComputed`, without the equality of responses. A response
has no decidable equality here; every change that the fuzz of
`Examples/Inductiveness.lean` makes to a task keeps the computed response. -/
def isTaskComputed (program : Program) (task : Dex.Task signature) : Bool :=
  match task.phase with
  | .running remainingEffects _ => remainingEffects.isSuffixOf (task.compute ⟦program⟧).1
  | _ => true

/-- The names of the clauses of `ConfigInvariant`, `TasksAreDistinct`, and
`TimersAreDistinct` that fail. -/
def checkDexInvariant (program : Program) (config : Dex.Config signature) : List String :=
  (match config.run with
    | none => []
    | some run =>
      let waiters := waitersOf run
      failsUnless "idsNodup" (decide (run.steps.map (fun record => record.id)).Nodup) ++
      failsUnless "numbersCounted" (run.steps.all (fun record =>
        decide (record.id.number ≤ (run.counters.get? record.id.stepType).getD 0))) ++
      failsUnless "waitsSupported" (waiters.all (fun waiter =>
        isSupportedWait waiter.2.wait && !waiter.2.timersDone.any id &&
        waiter.2.timersDone.length == waiter.2.wait.timerSeconds.length &&
        decide (waiter.2.registeredSeq < run.seq + 1))) ++
      failsUnless "seqInjective" (waiters.all (fun first => waiters.all (fun second =>
        first.2.registeredSeq != second.2.registeredSeq || decide (first.1.id = second.1.id)))) ++
      failsUnless "timersAllowed" (run.timers.all (fun entry =>
        decide (entry.1.step.stepType = StepType.timed) && entry.1.index == 0)) ++
      failsUnless "waitersQuiet" (waiters.all (fun waiter =>
        waiter.2.wait.channels.all (fun chan => (run.pendingMessages chan).isEmpty)))) ++
  failsUnless "tasksComputed" (config.tasks.all (isTaskComputed program)) ++
  failsUnless "tasksDistinct" (decide (config.tasks.map (fun task => task.id)).Nodup &&
    config.tasks.all (fun task => decide (task.id < config.nextTaskId))) ++
  failsUnless "timersDistinct" (match config.run with
    | none => true
    | some run => decide (run.timers.map (fun entry => entry.1)).Nodup)

/-- Mirror of `L.BreaksMutexRequirement`. -/
def breaksMutexRequirement (config : L.Config) : Bool :=
  config.threads.any (fun thread =>
    decide (thread.phase = .ready) && thread.timer.isNone &&
    (match requiredMutex thread.core thread.code with
      | some mutex => decide (config.mutexOwners.get? mutex ≠ some (some thread.core.tid))
      | none => false))

/-- The clauses of `IsSimulationState` that fail in a configuration pair:
those of `L.Invariant`, `Related`, `ConfigInvariant`, `TasksAreDistinct`, and
`TimersAreDistinct`. -/
def checkState (program : Program) (state : L.Config × Dex.Config signature) : List String :=
  (checkLInvariant program state.1).map (fun name => "L.Invariant." ++ name) ++
  (checkRelated program state.1 state.2).map (fun name => "Related." ++ name) ++
  (checkDexInvariant program state.2).map (fun name => "ConfigInvariant." ++ name)

/-- Drive both sides from pseudo-random L labels and check every state.
Returns the first failure, if any. -/
def checkedFromL (program : Program) :
    Nat → Nat → List L.Request → L.Config × Dex.Config signature → List String
  | 0, _, _, state => (checkState program state).take 1
  | fuel + 1, seed, inputs, state =>
    match checkState program state with
    | failure :: _ => [failure]
    | [] =>
      let enabled := (allLLabels inputs state.1).filter (fun label =>
        ((L.system program).step state.1 label).isSome)
      match pick seed enabled with
      | none => []
      | some label =>
        let remainingInputs :=
          match label with
          | .request _ => inputs.drop 1
          | _ => inputs
        match stepFromL program state label with
        | .error message => [message]
        | .ok next => checkedFromL program fuel (nextRandom seed) remainingInputs next

/-- Drive both sides from pseudo-random Dex actions and check every state. -/
def checkedFromDex (program : Program) :
    Nat → Nat → List L.Request → L.Config × Dex.Config signature → List String
  | 0, _, _, state => (checkState program state).take 1
  | fuel + 1, seed, inputs, state =>
    match checkState program state with
    | failure :: _ => [failure]
    | [] =>
      let enabled := (allDexActions inputs state.2).filter (fun action =>
        ((Dex.system ⟦program⟧).step state.2 action).isSome)
      match pick seed enabled with
      | none => []
      | some action =>
        let remainingInputs :=
          match action with
          | .request _ => inputs.drop 1
          | _ => inputs
        match stepFromDex program state action with
        | .error message => [message]
        | .ok next => checkedFromDex program fuel (nextRandom seed) remainingInputs next

/-- Run `seeds` pseudo-random schedules in both directions. Returns the
failures found; an empty list means that `L.Invariant` and `Related` held in
every configuration pair. -/
def relatedFailures (program : Program) (inputs : List L.Request) (fuel seeds : Nat) :
    List String :=
  (List.range seeds).flatMap (fun seed =>
    let initial := ((L.system program).init, (Dex.system ⟦program⟧).init)
    (checkedFromL program fuel (nextRandom (seed + 1)) inputs initial).map
      (fun failure => s!"seed {seed}, L-driven: {failure}") ++
    (checkedFromDex program fuel (nextRandom (seed + 1)) inputs initial).map
      (fun failure => s!"seed {seed}, Dex-driven: {failure}"))

open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures Mutex.program requests 300 6 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures TimedSelect.program requests 500 6 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures ForgedHandleInput.program requests 300 3 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures recursionProgram requests 400 3 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures exceptionProgram requests 300 3 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures lostUpdateProgram requests 600 6 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures broadcastProgram requests 700 6 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures signalProgram requests 700 6 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures selectRaceProgram requests 800 10 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures lateEmitProgram requests 200 8 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures twoLateEmitsProgram requests 200 8 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures newMutexProgram requests 200 3 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures cancelProgram requests 300 6 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard relatedFailures typeErrorProgram requests 200 3 == []

end DexExpressiveness.Examples
