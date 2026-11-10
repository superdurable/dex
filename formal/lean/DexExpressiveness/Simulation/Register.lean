import DexExpressiveness.Simulation.LToDexBasicWake

/-!
# L to Dex: the registration of a wait (work package 3a)

Helper lemmas for `l_thread_register_simulated`: the Dex actions that commit
a WaitFor, the three results of `register(e, W)`, and the frame of the
relation.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## The Dex actions of a WaitFor commit -/

/-- The run after [STEP-COMPLETE-WAITFOR] of `step` with response `response`. -/
def wp3a_completedRun (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (response : Dex.WaitForResponse signature) (now : Nat) : Dex.Run signature :=
  ((({ run with seq := run.seq + 1 } : Dex.Run signature).updateStep step
    (fun record => { record with invocation := none })).applyEffects response.effects).registerWait
      step response.wait now

/-- The configuration after [STEP-COMPLETE-WAITFOR] of `task`. -/
def wp3a_completedConfig (config : Dex.Config signature) (task : Dex.Task signature)
    (after : Dex.Run signature) : Dex.Config signature :=
  { config.removeTask task.id with run := some after, lastCommitAt := some config.clock }

/-- A WaitFor task with no effect left commits by its simulating actions. -/
theorem wp3a_exec_completeWaitFor (program : Program) (config : Dex.Config signature)
    (tasksDistinct : TasksAreDistinct config) (task : Dex.Task signature)
    (isMember : task ∈ config.tasks) (isComputed : IsTaskComputed program task)
    (noEffects : pendingEffects ⟦program⟧ task = []) (isWaitFor : task.method = .waitFor)
    (response : Dex.WaitForResponse signature)
    (isResponse : (task.compute ⟦program⟧).2 = .waitFor response)
    (run : Dex.Run signature) (hasRun : config.run = some run)
    (isCurrent : config.isTaskCurrent task = true) (canCommit : config.canCommit = true) :
    (Dex.system ⟦program⟧).exec config (returnTaskActions task ++ [.stepComplete task.id]) =
      some (Dex.Config.closeEvents run (wp3a_completedRun run task.step response config.clock),
        wp3a_completedConfig config task
          (wp3a_completedRun run task.step response config.clock)) := by
  have isFound := findTask?_of_mem tasksDistinct isMember
  have isReturned := exec_returnTaskActions program config tasksDistinct task isMember isComputed
    noEffects
  have isFoundNext : (config.updateTask task.id (returnedTask program task)).findTask? task.id =
      some { task with phase := .returned (.waitFor response) } := by
    rw [findTask?_updateTask config task.id task.id (returnedTask program task)
      (fun _ => rfl), isFound]
    simp [returnedTask, isResponse]
  have sameRemoved : (config.updateTask task.id (returnedTask program task)).removeTask task.id =
      config.removeTask task.id := by
    have sameFilter : List.filter ((fun candidate => !decide (candidate.id = task.id)) ∘
        fun candidate => if candidate.id = task.id then returnedTask program task candidate
          else candidate) config.tasks =
        List.filter (fun candidate => !decide (candidate.id = task.id)) config.tasks := by
      apply List.filter_congr
      intro candidate _
      by_cases isChanged : candidate.id = task.id <;> simp [isChanged, returnedTask]
    have isIdentity : ∀ candidate,
        candidate ∈ List.filter (fun candidate => !decide (candidate.id = task.id)) config.tasks →
        (if candidate.id = task.id then returnedTask program task candidate else candidate) =
          id candidate := by
      intro candidate isCandidate
      have isOther := (List.mem_filter.mp isCandidate).2
      simp only [Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not] at isOther
      simp [isOther]
    simp only [Dex.Config.removeTask, Dex.Config.updateTask, List.filter_map]
    congr 1
    rw [sameFilter, List.map_congr_left isIdentity, List.map_id]
  have isCurrentNext : (config.updateTask task.id (returnedTask program task)).isTaskCurrent
      { task with phase := .returned (.waitFor response) } = true := isCurrent
  have canCommitNext : (config.updateTask task.id (returnedTask program task)).canCommit =
      true := canCommit
  have hasRunNext : (config.updateTask task.id (returnedTask program task)).run = some run :=
    hasRun
  have isLast : (Dex.system ⟦program⟧).step
      (config.updateTask task.id (returnedTask program task)) (.stepComplete task.id) =
      some (Dex.Config.closeEvents run (wp3a_completedRun run task.step response config.clock),
        wp3a_completedConfig config task
          (wp3a_completedRun run task.step response config.clock)) := by
    simp only [isWaitFor] at isCurrentNext
    simp only [Dex.system, Dex.step, hasRunNext, isFoundNext, isWaitFor, isCurrentNext,
      canCommitNext, Bool.and_self, ↓reduceIte, sameRemoved]
    rfl
  have isExec := TransitionSystem.exec_snoc (Dex.system ⟦program⟧) config _ _ _ _ _ _ isReturned isLast
  simpa using isExec

/-! ## The three results of `register(e, W)` -/

/-- The run of a WaitFor commit before `register(e, W)`. -/
def wp3a_preparedRun (run : Dex.Run signature) (step : Dex.StepExecId signature) :
    Dex.Run signature :=
  ({ run with seq := run.seq + 1 } : Dex.Run signature).updateStep step
    (fun record => { record with invocation := none })

/-- A WaitFor commit with no effect is `register(e, W)` on the prepared run. -/
theorem wp3a_completedRun_eq (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (wait : Dex.Wait signature) (now : Nat) :
    wp3a_completedRun run step { effects := {}, wait := wait } now =
      (wp3a_preparedRun run step).registerWait step wait now := by
  show (Dex.Run.applyEffects _ {}).registerWait step wait now = _
  rw [applyEffects_empty]
  rfl

/-- The Step record once its wait is satisfied. -/
def wp3a_toExecute (results : Dex.ConditionResults signature)
    (current : Dex.StepRecord signature) : Dex.StepRecord signature :=
  { current with
    conditionResults := some results
    phase := .execute
    invocation := some { method := .execute, state := .pending } }

/-- The Step record once its wait is stored. -/
def wp3a_toWaiting (wait : Dex.Wait signature) (now seq : Nat)
    (current : Dex.StepRecord signature) : Dex.StepRecord signature :=
  { current with
    phase := .waiting
    waitState := some
      { wait := wait
        fireAt := wait.timerSeconds.map (fun seconds => now + seconds * 1000000)
        timersDone := wait.timerSeconds.map (fun _ => false)
        registeredSeq := seq } }

/-- The run with the wait stored and its timers armed, before `eval(e)`. -/
def wp3a_storedRun (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (wait : Dex.Wait signature) (now : Nat) : Dex.Run signature :=
  ({ run with
    timers := Dex.Run.armTimers step
      (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0 run.timers } :
    Dex.Run signature).updateStep step (wp3a_toWaiting wait now run.seq)

/-- The run after the stored wait consumed the head of one Channel. -/
def wp3a_consumedRun (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (wait : Dex.Wait signature) (now : Nat) (chan : Chan) (remaining : List DexVal)
    (results : Dex.ConditionResults signature) : Dex.Run signature :=
  (((wp3a_storedRun run step wait now).disarmTimersOf step).setPendingMessages chan
    remaining).updateStep step (wp3a_toExecute results)

/-- A wait with no condition is satisfied at once. -/
theorem wp3a_registerWait_empty (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (wait : Dex.Wait signature) (now : Nat) (hasNoCondition : wait.hasNoCondition = true) :
    run.registerWait step wait now =
      run.updateStep step (wp3a_toExecute { timersDone := [], channelMessages := [] }) := by
  simp only [Dex.Run.registerWait, hasNoCondition, ↓reduceIte]
  rfl

/-- The stored run finds the Step with its wait stored. -/
theorem wp3a_findStep?_storedRun (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (record : Dex.StepRecord signature) (isFound : run.findStep? step = some record)
    (wait : Dex.Wait signature) (now : Nat) :
    (wp3a_storedRun run step wait now).findStep? step =
      some (wp3a_toWaiting wait now run.seq record) :=
  Dex.Run.findStep?_updateStep_self _ step _ record isFound rfl

/-- A supported wait on empty Channels is stored, and the Step waits. -/
theorem wp3a_registerWait_blocks (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (record : Dex.StepRecord signature) (isFound : run.findStep? step = some record)
    (wait : Dex.Wait signature) (now : Nat) (isSupported : Dex.IsSupportedWait wait)
    (hasChannel : wait.channels ≠ [])
    (isQuiet : ∀ chan, chan ∈ wait.channels → run.pendingMessages chan = []) :
    run.registerWait step wait now = wp3a_storedRun run step wait now := by
  have hasCondition : wait.hasNoCondition = false := by
    cases channelCase : wait.channels with
    | nil => exact absurd channelCase hasChannel
    | cons chan laterChans => simp [Dex.Wait.hasNoCondition, channelCase]
  simp only [Dex.Run.registerWait, hasCondition, Bool.false_eq_true, Bool.false_eq_true, ↓reduceIte]
  exact Dex.Run.evalStep_of_plan_none (wp3a_storedRun run step wait now) step _ _
    (wp3a_findStep?_storedRun run step record isFound wait now) rfl
    (Dex.Run.plan_none_of_quiet wait _ _ isSupported hasChannel (by simp)
      (fun chan isNamed => by
        show (run.pendingMessages chan).length = 0
        rw [isQuiet chan isNamed]
        rfl))

/-- A stored wait with a plan consumes its messages, and the Step may Execute. -/
theorem wp3a_registerWait_planned (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (record : Dex.StepRecord signature) (isFound : run.findStep? step = some record)
    (wait : Dex.Wait signature) (now : Nat) (hasCondition : wait.hasNoCondition = false)
    (consumeFlags : List Bool)
    (isPlanned : Dex.Run.plan wait (fun chan => (run.pendingMessages chan).length)
      (wait.timerSeconds.map (fun _ => false)) = some consumeFlags) :
    run.registerWait step wait now =
      (Dex.Run.consumeMessages wait.channels consumeFlags
        ((wp3a_storedRun run step wait now).disarmTimersOf step)).1.updateStep step
        (wp3a_toExecute
          { timersDone := wait.timerSeconds.map (fun _ => false)
            channelMessages := (Dex.Run.consumeMessages wait.channels consumeFlags
              ((wp3a_storedRun run step wait now).disarmTimersOf step)).2 }) := by
  simp only [Dex.Run.registerWait, hasCondition, Bool.false_eq_true, Bool.false_eq_true, ↓reduceIte]
  exact Dex.Run.evalStep_of_plan_some (wp3a_storedRun run step wait now) step _ _ consumeFlags
    (wp3a_findStep?_storedRun run step record isFound wait now) rfl rfl isPlanned

/-- AnyOf: the first Channel has a message, so the wait consumes it. -/
theorem wp3a_registerWait_any_first (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (record : Dex.StepRecord signature) (isFound : run.findStep? step = some record)
    (timerSeconds : List Nat) (chan : Chan) (laterChans : List Chan) (now : Nat)
    (message : DexVal) (remaining : List DexVal)
    (hasMessage : run.pendingMessages chan = message :: remaining) :
    run.registerWait step
        { kind := .any, timerSeconds := timerSeconds, channels := chan :: laterChans } now =
      wp3a_consumedRun run step
        { kind := .any, timerSeconds := timerSeconds, channels := chan :: laterChans } now chan
        remaining
        { timersDone := timerSeconds.map (fun _ => false)
          channelMessages := some message :: laterChans.map (fun _ => none) } := by
  have isPlanned : Dex.Run.plan
      ({ kind := .any, timerSeconds := timerSeconds, channels := chan :: laterChans } :
        Dex.Wait signature)
      (fun chan => (run.pendingMessages chan).length) (timerSeconds.map (fun _ => false)) =
      some (true :: laterChans.map (fun _ => false)) := by
    have noTimerDone : (timerSeconds.map (fun _ => false)).any id = false := by simp
    simp only [Dex.Run.plan, Dex.Wait.hasNoCondition, List.isEmpty_cons, Bool.and_false,
      Bool.false_eq_true, ↓reduceIte, noTimerDone]
    exact Dex.Run.planAnyChannel_first _ _ _ (by simp [hasMessage])
  rw [wp3a_registerWait_planned run step record isFound _ now (by simp [Dex.Wait.hasNoCondition])
    _ isPlanned]
  rw [Dex.Run.consumeMessages_first (message := message) (remaining := remaining)
    (hasPending := hasMessage)]
  rfl

/-- AnyOf of two Channels: only the second has a message, so the wait consumes it. -/
theorem wp3a_registerWait_any_second (run : Dex.Run signature)
    (step : Dex.StepExecId signature) (record : Dex.StepRecord signature)
    (isFound : run.findStep? step = some record) (timerSeconds : List Nat)
    (firstChan secondChan : Chan) (now : Nat) (message : DexVal) (remaining : List DexVal)
    (isFirstEmpty : run.pendingMessages firstChan = [])
    (hasMessage : run.pendingMessages secondChan = message :: remaining) :
    run.registerWait step
        { kind := .any, timerSeconds := timerSeconds, channels := [firstChan, secondChan] } now =
      wp3a_consumedRun run step
        { kind := .any, timerSeconds := timerSeconds, channels := [firstChan, secondChan] } now
        secondChan remaining
        { timersDone := timerSeconds.map (fun _ => false)
          channelMessages := [none, some message] } := by
  have isPlanned : Dex.Run.plan
      ({ kind := .any, timerSeconds := timerSeconds, channels := [firstChan, secondChan] } :
        Dex.Wait signature)
      (fun chan => (run.pendingMessages chan).length) (timerSeconds.map (fun _ => false)) =
      some [false, true] := by
    have noTimerDone : (timerSeconds.map (fun _ => false)).any id = false := by simp
    simp [Dex.Run.plan, Dex.Wait.hasNoCondition, noTimerDone, Dex.Run.planAnyChannel,
      isFirstEmpty, hasMessage]
  rw [wp3a_registerWait_planned run step record isFound _ now (by simp [Dex.Wait.hasNoCondition])
    _ isPlanned]
  have isFirstEmptyStored : ((wp3a_storedRun run step
      { kind := .any, timerSeconds := timerSeconds, channels := [firstChan, secondChan] }
      now).disarmTimersOf step).pendingMessages firstChan = [] := isFirstEmpty
  have hasMessageStored : ((wp3a_storedRun run step
      { kind := .any, timerSeconds := timerSeconds, channels := [firstChan, secondChan] }
      now).disarmTimersOf step).pendingMessages secondChan = message :: remaining := hasMessage
  simp only [Dex.Run.consumeMessages, List.headD_cons, List.tail_cons, isFirstEmptyStored,
    hasMessageStored]
  rfl

/-- `Until(c.ForOne())`: the Channel has a message, so the wait consumes it. -/
theorem wp3a_registerWait_all_single (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (record : Dex.StepRecord signature) (isFound : run.findStep? step = some record)
    (chan : Chan) (now : Nat) (message : DexVal) (remaining : List DexVal)
    (hasMessage : run.pendingMessages chan = message :: remaining) :
    run.registerWait step { kind := .all, timerSeconds := [], channels := [chan] } now =
      wp3a_consumedRun run step { kind := .all, timerSeconds := [], channels := [chan] } now chan
        remaining { timersDone := [], channelMessages := [some message] } := by
  have isPlanned : Dex.Run.plan
      ({ kind := .all, timerSeconds := [], channels := [chan] } : Dex.Wait signature)
      (fun chan => (run.pendingMessages chan).length)
      (([] : List Nat).map (fun _ => false)) = some [true] := by
    simp [Dex.Run.plan, Dex.Wait.hasNoCondition, hasMessage]
  rw [wp3a_registerWait_planned run step record isFound _ now (by simp [Dex.Wait.hasNoCondition])
    _ isPlanned]
  have hasMessageStored : ((wp3a_storedRun run step
      { kind := .all, timerSeconds := [], channels := [chan] }
      now).disarmTimersOf step).pendingMessages chan = message :: remaining := hasMessage
  simp only [Dex.Run.consumeMessages, List.headD_cons, hasMessageStored]
  rfl

/-! ## The wait a WaitFor returns -/

/-- The wait that the WaitFor of a thread's Step returns (spec §8.3). -/
def wp3a_wait (thread : L.Thread) : Dex.Wait signature :=
  match thread.timer with
  | some (_, seconds) =>
    match timedChan thread.code with
    | some none =>
      { kind := .any, timerSeconds := [seconds]
        channels := [Chan.cancelRequest thread.core.tid] }
    | some (some chan) =>
      { kind := .any, timerSeconds := [seconds]
        channels := [Chan.external chan, Chan.cancelRequest thread.core.tid] }
    | none => Dex.Wait.empty signature
  | none => encodeWaitRequest thread.core.tid (waitRequest thread.core thread.code)

/-- The WaitFor task of a `waitStarted` thread computes `wp3a_wait`, with no effect. -/
theorem wp3a_compute_waitFor (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (task : Dex.Task signature)
    (isWaitStarted : thread.phase = .waitStarted) (fitsCode : thread.PhaseFitsCode)
    (carries : StepCarriesThread program thread record) (hasStep : task.step = record.id)
    (isWaitFor : task.method = .waitFor) (hasInput : task.input = record.input) :
    (task.compute ⟦program⟧).2 = .waitFor { effects := {}, wait := wp3a_wait thread } := by
  rw [stepCarriesThread_iff] at carries
  simp only [isWaitStarted, isBootPhase, Bool.false_eq_true, ↓reduceIte] at carries
  unfold L.Thread.PhaseFitsCode at fitsCode
  simp only [isWaitStarted] at fitsCode
  cases timerCase : thread.timer with
  | none =>
    simp only [timerCase] at carries
    have isBlocking : isBlockingHead thread.code = true := by simpa [timerCase] using fitsCode
    simp [Dex.Task.compute, isWaitFor, hasStep, carries.1, translate, pointStepDef, isBlocking,
      hasInput, carries.2.1, wp3a_wait, timerCase]
  | some timer =>
    obtain ⟨number, seconds⟩ := timer
    simp only [timerCase] at carries
    simp only [Dex.Task.compute, isWaitFor, hasStep, carries.1, translate, timedStepDef, hasInput,
      carries.2, wp3a_wait, timerCase]
    cases timedChan thread.code with
    | none => rfl
    | some chan => cases chan <;> rfl

/-! ## The frame of a registration -/

/-- The change of the Step table by one Step. -/
def wp3a_changeStep (step : Dex.StepExecId signature)
    (change : Dex.StepRecord signature → Dex.StepRecord signature)
    (candidate : Dex.StepRecord signature) : Dex.StepRecord signature :=
  if candidate.id = step then change candidate else candidate

/-- The Channel clauses of `RunningRelated` after the thread `tid` registered. -/
structure wp3a_ChannelsRelated (lNext : L.Config) (run newRun : Dex.Run signature) (tid : Tid)
    (newPhase : Phase) : Prop where
  /-- External Channels hold the queued input. -/
  inputQueues : ∀ chan,
    newRun.pendingMessages (Chan.external chan) = (lNext.pendingInput chan).map DexVal.value
  /-- One token exactly while the mutex is unlocked. -/
  mutexTokens : ∀ mutex, (newRun.pendingMessages (Chan.mutexToken mutex)).length =
    if lNext.mutexOwners.get? mutex = some none then 1 else 0
  /-- One message exactly while the thread is joinable. -/
  joinTokens : ∀ joined, (newRun.pendingMessages (Chan.joinDone joined)).length =
    if lNext.joinable.contains joined then 1 else 0
  /-- The early wakes of each waiter. -/
  cvWakes : ∀ cv waiter, (newRun.pendingMessages (Chan.cvWake cv waiter)).length =
    lNext.cvWakes.count (cv, waiter)
  /-- The cancel requests of other threads stay. -/
  cancelOther : ∀ other, other ≠ tid →
    newRun.pendingMessages (Chan.cancelRequest other) =
      run.pendingMessages (Chan.cancelRequest other)
  /-- No cancel request of the thread appears. -/
  cancelBefore : newRun.pendingMessages (Chan.cancelRequest tid) ≠ [] →
    run.pendingMessages (Chan.cancelRequest tid) ≠ []
  /-- A cancel request of the thread stays, or the thread observed it. -/
  cancelAfter : run.pendingMessages (Chan.cancelRequest tid) ≠ [] →
    newRun.pendingMessages (Chan.cancelRequest tid) ≠ [] ∨ newPhase = .woken .canceled

/-- The frame lemma of a registration: a `waitStarted` thread becomes woken or
blocked, its Step changes, and one Channel may lose its head message. -/
theorem wp3a_runningRelated_register {program : Program} {lConfig lNext : L.Config}
    {dexConfig dexNext : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (lInvariant : L.Invariant program lConfig)
    (idsNodup : (run.steps.map (fun candidate => candidate.id)).Nodup)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    (isWaitStarted : thread.phase = .waitStarted)
    {record : Dex.StepRecord signature} (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true)
    (carries : StepCarriesThread program thread record)
    (newPhase : Phase) (isNotBoot : isBootPhase newPhase = false)
    (isNotHalted : newPhase ≠ .halted)
    (sameThreads : lNext.threads = (lConfig.setPhase tid newPhase).threads)
    (sameShared : lNext.shared = lConfig.shared)
    (sameQueues : lNext.cvQueues = lConfig.cvQueues)
    (sameFlags : lNext.cancelFlags = lConfig.cancelFlags)
    (sameTimerCounter : lNext.timerCounter = lConfig.timerCounter)
    (change : Dex.StepRecord signature → Dex.StepRecord signature)
    (sameSteps : newRun.steps = run.steps.map (wp3a_changeStep record.id change))
    (changeKeeps : (change record).id = record.id ∧ (change record).input = record.input ∧
      (change record).options = record.options ∧ (change record).phase.isActive = true)
    (sameAttributes : newRun.attributes = run.attributes)
    (sameCounters : newRun.counters = run.counters)
    (sameOutputs : newRun.outputs = run.outputs)
    (channels : wp3a_ChannelsRelated lNext run newRun tid newPhase)
    (timersField : ∀ timer fireTime, newRun.timers.get? timer = some fireTime ↔
      ∃ owner chan since, owner ∈ lNext.threads ∧
        owner.phase = .blocked (.timed timer.step.number fireTime chan) since ∧
        timer.step.stepType = StepType.timed ∧ timer.index = 0)
    (blockedOrder : ∀ on since, newPhase = .blocked on since →
      (∀ other, other ∈ lConfig.threads → ∀ otherOn otherSince,
        other.phase = .blocked otherOn otherSince → otherSince < since) ∧
      ∃ waitState, (change record).waitState = some waitState ∧
        ∀ other, other ∈ run.steps → other.phase = .waiting → ∀ otherWait,
          other.waitState = some otherWait →
          otherWait.registeredSeq < waitState.registeredSeq)
    (keepsTasks : ∀ task, task ∈ dexConfig.tasks → task.step ≠ record.id → task ∈ dexNext.tasks)
    (newMatch : PhaseMatchesStep program lConfig dexNext { thread with phase := newPhase }
      (change record))
    (tasksAreCurrent : ∀ task, task ∈ dexNext.tasks → dexNext.isTaskCurrent task = true) :
    RunningRelated program lNext dexNext newRun := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  have memNew : ∀ candidate, candidate ∈ lNext.threads ↔
      (candidate ∈ lConfig.threads ∧ candidate.core.tid ≠ tid) ∨
      candidate = { thread with phase := newPhase } := by
    intro candidate
    rw [sameThreads]
    exact mem_setPhase lInvariant isFound newPhase candidate
  have sameBoot : isBootPhase newPhase = isBootPhase thread.phase := by
    rw [isNotBoot, isWaitStarted]
    rfl
  have findNew : ∀ other, lNext.findThread? other =
      (lConfig.findThread? other).map (fun current =>
        if current.core.tid = tid then { current with phase := newPhase } else current) := by
    intro other
    rw [← findThread?_setPhase]
    show lNext.threads.find? _ = (lConfig.setPhase tid newPhase).threads.find? _
    rw [sameThreads]
  -- No thread boots: a booting `main` is the only thread.
  have noBoot : ∀ other, other ∈ lConfig.threads → isBootPhase other.phase = false := by
    intro other isOther
    cases bootCase : isBootPhase other.phase with
    | false => rfl
    | true =>
      have isBootPhase : other.phase = .bootPending ∨ other.phase = .bootRunning := by
        cases phaseCase : other.phase <;> simp [phaseCase, isBootPhase] at bootCase ⊢
      have isOnly := (lInvariant.bootIsInitial other isOther isBootPhase).1
      rw [isOnly] at isMember
      have isThread : thread = other := by simpa using isMember
      rw [isThread] at isWaitStarted
      rcases isBootPhase with isPhase | isPhase <;> rw [isPhase] at isWaitStarted <;>
        cases isWaitStarted
  have imageRecord : wp3a_changeStep record.id change record = change record := by
    simp [wp3a_changeStep]
  have imageOther : ∀ candidate, candidate.id ≠ record.id →
      wp3a_changeStep record.id change candidate = candidate := by
    intro candidate isOther
    simp [wp3a_changeStep, isOther]
  have imageKeeps : ∀ candidate, candidate ∈ run.steps →
      (wp3a_changeStep record.id change candidate).id = candidate.id ∧
      (wp3a_changeStep record.id change candidate).input = candidate.input ∧
      (wp3a_changeStep record.id change candidate).options = candidate.options ∧
      (wp3a_changeStep record.id change candidate).phase.isActive =
        candidate.phase.isActive := by
    intro candidate isCandidate
    by_cases isChanged : candidate.id = record.id
    · have sameRecord : candidate = record :=
        eq_of_nodup_map (fun step => step.id) run.steps idsNodup isCandidate isRecord isChanged
      rw [sameRecord, imageRecord]
      exact ⟨changeKeeps.1, changeKeeps.2.1, changeKeeps.2.2.1, by
        rw [changeKeeps.2.2.2, isActive]⟩
    · rw [imageOther candidate isChanged]
      exact ⟨rfl, rfl, rfl, rfl⟩
  -- A Step of another thread is not the Step of the changed thread.
  have otherStep : ∀ other otherRecord, other ∈ lConfig.threads → other.core.tid ≠ tid →
      otherRecord ∈ run.steps → StepCarriesThread program other otherRecord →
      otherRecord.id ≠ record.id := by
    intro other otherRecord isOther otherTid isOtherRecord otherCarries sameId
    have sameRecord : otherRecord = record :=
      eq_of_nodup_map (fun candidate => candidate.id) run.steps idsNodup isOtherRecord
        isRecord sameId
    rw [sameRecord] at otherCarries
    have otherInput := threadOfInput_of_mem lInvariant isOther otherCarries
    have threadInput := threadOfInput_of_mem lInvariant isMember carries
    exact otherTid ((otherInput.symm.trans threadInput).trans hasTid)
  have imageMem : ∀ candidate, candidate ∈ run.steps →
      wp3a_changeStep record.id change candidate ∈ newRun.steps := by
    intro candidate isCandidate
    rw [sameSteps]
    exact List.mem_map.mpr ⟨candidate, isCandidate, rfl⟩
  have carriesImage : ∀ (other : L.Thread) (candidate : Dex.StepRecord signature),
      candidate ∈ run.steps → (StepCarriesThread program other candidate ↔
        StepCarriesThread program other (wp3a_changeStep record.id change candidate)) := by
    intro other candidate isCandidate
    obtain ⟨sameId, sameInput, sameOptions, _⟩ := imageKeeps candidate isCandidate
    exact ⟨stepCarriesThread_congr sameId sameInput sameOptions,
      stepCarriesThread_congr sameId.symm sameInput.symm sameOptions.symm⟩
  -- The active Step that carries the changed thread is `record`.
  have recordOfNew : ∀ candidate, candidate ∈ run.steps → candidate.phase.isActive = true →
      StepCarriesThread program { thread with phase := newPhase }
        (wp3a_changeStep record.id change candidate) → candidate = record := by
    intro candidate isCandidate candidateActive candidateCarries
    have oldCarries : StepCarriesThread program thread candidate := by
      have newCarries := (carriesImage _ candidate isCandidate).mpr candidateCarries
      rw [stepCarriesThread_iff] at newCarries ⊢
      dsimp only at newCarries
      rw [isNotBoot] at newCarries
      rw [← sameBoot, isNotBoot]
      exact newCarries
    exact related.activeStepsHaveDistinctThreads candidate isCandidate record isRecord
      candidateActive isActive (by rw [oldCarries.input_eq carries])
  -- The Step of a blocked thread waits.
  have waitingOfBlocked : ∀ (other : L.Thread) (otherRecord : Dex.StepRecord signature) on since,
      other ∈ lConfig.threads → otherRecord ∈ run.steps → otherRecord.phase.isActive = true →
      StepCarriesThread program other otherRecord → other.phase = .blocked on since →
      otherRecord.phase = .waiting := by
    intro other otherRecord on since isOther isOtherRecord otherActive otherCarries isBlocked
    have phaseMatches := related.phaseMatchesStep_of_carries other isOther
      (by rw [isBlocked]; simp) otherRecord isOtherRecord otherActive otherCarries
    simp only [PhaseMatchesStep, isBlocked] at phaseMatches
    exact phaseMatches.1
  exact
    { shared := fun name => by
        rw [sameAttributes]
        show _ = (lNext.shared.get? name).getD .null
        rw [sameShared]
        exact related.shared name
      cvQueues := fun cv => by
        rw [sameAttributes, sameQueues]
        exact related.cvQueues cv
      inputQueues := channels.inputQueues
      mutexTokens := fun _ => channels.mutexTokens
      mutexesBeforeBoot := fun other isOther isBooting => by
        have isBoot : isBootPhase other.phase = true := by
          rcases isBooting with isPhase | isPhase <;> rw [isPhase] <;> rfl
        rcases (memNew other).mp isOther with ⟨wasMember, _⟩ | isNew
        · rw [noBoot other wasMember] at isBoot
          cases isBoot
        · rw [isNew] at isBoot
          simp only [isNotBoot] at isBoot
          cases isBoot
      joinTokens := channels.joinTokens
      cancelRequestsAreFlags := fun target isPending => by
        rw [sameFlags]
        apply related.cancelRequestsAreFlags
        by_cases isSelf : target = tid
        · rw [isSelf] at isPending ⊢
          exact channels.cancelBefore isPending
        · rw [← channels.cancelOther target isSelf]
          exact isPending
      cancelFlagsAreRequests := fun target isFlagged => by
        rw [sameFlags] at isFlagged
        rw [findNew]
        by_cases isSelf : target = tid
        · rw [isSelf] at isFlagged ⊢
          rcases related.cancelFlagsAreRequests tid isFlagged with
            isPending | ⟨current, isCurrent, isCanceled⟩ | ⟨isGone, _⟩
          · rcases channels.cancelAfter isPending with stillPending | isObserved
            · exact Or.inl stillPending
            · refine Or.inr (Or.inl ⟨{ thread with phase := newPhase }, ?_, Or.inl isObserved⟩)
              rw [isFound]
              simp [hasTid]
          · rw [isFound] at isCurrent
            cases isCurrent
            rw [isWaitStarted] at isCanceled
            rcases isCanceled with isPhase | isPhase <;> cases isPhase
          · rw [isFound] at isGone
            cases isGone
        · rcases related.cancelFlagsAreRequests target isFlagged with
            isPending | ⟨current, isCurrent, isCanceled⟩ | ⟨isGone, isFresh⟩
          · exact Or.inl (by rw [channels.cancelOther target isSelf]; exact isPending)
          · have currentTid := (mem_of_findThread? isCurrent).2
            refine Or.inr (Or.inl ⟨current, ?_, isCanceled⟩)
            rw [isCurrent]
            simp [currentTid, isSelf]
          · refine Or.inr (Or.inr ⟨by rw [isGone]; rfl, ?_⟩)
            intro live isLive
            rcases (memNew live).mp isLive with ⟨wasLive, _⟩ | isNew
            · exact isFresh live wasLive
            · rw [isNew]
              exact isFresh thread isMember
      cvWakes := channels.cvWakes
      timers := timersField
      timerCounter := by
        rw [sameCounters, sameTimerCounter]
        exact related.timerCounter
      outputsEmpty := sameOutputs.trans related.outputsEmpty
      threadsHaveSteps := fun other isOther isNotHaltedOther => by
        rcases (memNew other).mp isOther with ⟨isOld, otherTid⟩ | isNew
        · obtain ⟨otherRecord, isOtherRecord, otherActive, otherCarries, otherMatch⟩ :=
            related.threadsHaveSteps other isOld isNotHaltedOther
          have isOtherId := otherStep other otherRecord isOld otherTid isOtherRecord otherCarries
          refine ⟨otherRecord, ?_, otherActive, otherCarries, ?_⟩
          · have isImage := imageMem otherRecord isOtherRecord
            rw [imageOther otherRecord isOtherId] at isImage
            exact isImage
          · exact phaseMatchesStep_transfer sameQueues
              (fun task isTask isTaskStep =>
                keepsTasks task isTask (by rw [isTaskStep]; exact isOtherId))
              otherMatch
        · rw [isNew]
          refine ⟨change record, ?_, changeKeeps.2.2.2, ?_, ?_⟩
          · have isImage := imageMem record isRecord
            rw [imageRecord] at isImage
            exact isImage
          · exact stepCarriesThread_congr changeKeeps.1 changeKeeps.2.1 changeKeeps.2.2.1
              (stepCarriesThread_setPhase newPhase sameBoot carries)
          · exact phaseMatchesStep_transfer sameQueues (fun _ isTask _ => isTask) newMatch
      stepsHaveThreads := fun changed isChanged changedActive => by
        rw [sameSteps] at isChanged
        obtain ⟨candidate, isCandidate, isImage⟩ := List.mem_map.mp isChanged
        rw [← isImage] at changedActive ⊢
        rw [(imageKeeps candidate isCandidate).2.2.2] at changedActive
        obtain ⟨owner, isOwner, ownerNotHalted, ownerCarries⟩ :=
          related.stepsHaveThreads candidate isCandidate changedActive
        by_cases isChangedThread : owner.core.tid = tid
        · have isThread : owner = thread :=
            thread_eq_of_tid lInvariant isOwner isMember (isChangedThread.trans hasTid.symm)
          rw [isThread] at ownerCarries
          exact ⟨_, (memNew _).mpr (Or.inr rfl), isNotHalted,
            (carriesImage _ candidate isCandidate).mp
              (stepCarriesThread_setPhase newPhase sameBoot ownerCarries)⟩
        · exact ⟨owner, (memNew owner).mpr (Or.inl ⟨isOwner, isChangedThread⟩), ownerNotHalted,
            (carriesImage owner candidate isCandidate).mp ownerCarries⟩
      activeStepsHaveDistinctThreads := fun first isFirst second isSecond firstActive
          secondActive sameThread => by
        rw [sameSteps] at isFirst isSecond
        obtain ⟨firstOld, isFirstOld, isFirstImage⟩ := List.mem_map.mp isFirst
        obtain ⟨secondOld, isSecondOld, isSecondImage⟩ := List.mem_map.mp isSecond
        rw [← isFirstImage] at firstActive sameThread ⊢
        rw [← isSecondImage] at secondActive sameThread ⊢
        rw [(imageKeeps firstOld isFirstOld).2.2.2] at firstActive
        rw [(imageKeeps secondOld isSecondOld).2.2.2] at secondActive
        rw [(imageKeeps firstOld isFirstOld).2.1, (imageKeeps secondOld isSecondOld).2.1]
          at sameThread
        rw [related.activeStepsHaveDistinctThreads firstOld isFirstOld secondOld isSecondOld
          firstActive secondActive sameThread]
      invocationsAreActive := fun changed isChanged hasInvocation => by
        rw [sameSteps] at isChanged
        obtain ⟨candidate, isCandidate, isImage⟩ := List.mem_map.mp isChanged
        rw [← isImage] at hasInvocation ⊢
        rw [(imageKeeps candidate isCandidate).2.2.2]
        by_cases isChangedStep : candidate.id = record.id
        · have sameRecord : candidate = record :=
            eq_of_nodup_map (fun step => step.id) run.steps idsNodup isCandidate isRecord
              isChangedStep
          rw [sameRecord]
          exact isActive
        · rw [imageOther candidate isChangedStep] at hasInvocation
          exact related.invocationsAreActive candidate isCandidate hasInvocation
      blockingOrder := fun first second firstRecord secondRecord firstOn firstSince secondOn
          secondSince firstWait secondWait isFirst isSecond isFirstRecord isSecondRecord
          firstActive secondActive firstCarries secondCarries firstBlocked secondBlocked
          firstHasWait secondHasWait => by
        rw [sameSteps] at isFirstRecord isSecondRecord
        obtain ⟨firstOld, isFirstOld, isFirstImage⟩ := List.mem_map.mp isFirstRecord
        obtain ⟨secondOld, isSecondOld, isSecondImage⟩ := List.mem_map.mp isSecondRecord
        rw [← isFirstImage] at firstActive firstCarries firstHasWait
        rw [← isSecondImage] at secondActive secondCarries secondHasWait
        rw [(imageKeeps firstOld isFirstOld).2.2.2] at firstActive
        rw [(imageKeeps secondOld isSecondOld).2.2.2] at secondActive
        rcases (memNew first).mp isFirst with ⟨isFirstThread, firstTid⟩ | isFirstNew
        · have firstCarriesOld := (carriesImage first firstOld isFirstOld).mpr firstCarries
          rw [imageOther firstOld (otherStep first firstOld isFirstThread firstTid isFirstOld
            firstCarriesOld)] at firstHasWait
          rcases (memNew second).mp isSecond with ⟨isSecondThread, secondTid⟩ | isSecondNew
          · have secondCarriesOld := (carriesImage second secondOld isSecondOld).mpr secondCarries
            rw [imageOther secondOld (otherStep second secondOld isSecondThread secondTid
              isSecondOld secondCarriesOld)] at secondHasWait
            exact related.blockingOrder first second firstOld secondOld firstOn firstSince
              secondOn secondSince firstWait secondWait isFirstThread isSecondThread isFirstOld
              isSecondOld firstActive secondActive firstCarriesOld secondCarriesOld firstBlocked
              secondBlocked firstHasWait secondHasWait
          · rw [isSecondNew] at secondCarries secondBlocked
            have isSecondChanged := recordOfNew secondOld isSecondOld secondActive secondCarries
            rw [isSecondChanged, imageRecord] at secondHasWait
            obtain ⟨sinceBelow, waitState, hasWait, seqBelow⟩ :=
              blockedOrder secondOn secondSince secondBlocked
            rw [hasWait] at secondHasWait
            cases secondHasWait
            have isEarlier := sinceBelow first isFirstThread firstOn firstSince firstBlocked
            have isEarlierSeq := seqBelow firstOld isFirstOld
              (waitingOfBlocked first firstOld firstOn firstSince isFirstThread isFirstOld
                firstActive firstCarriesOld firstBlocked) firstWait firstHasWait
            exact ⟨fun _ => isEarlierSeq, fun _ => isEarlier⟩
        · rw [isFirstNew] at firstCarries firstBlocked
          have isFirstChanged := recordOfNew firstOld isFirstOld firstActive firstCarries
          rw [isFirstChanged, imageRecord] at firstHasWait
          obtain ⟨sinceBelow, waitState, hasWait, seqBelow⟩ :=
            blockedOrder firstOn firstSince firstBlocked
          rw [hasWait] at firstHasWait
          cases firstHasWait
          rcases (memNew second).mp isSecond with ⟨isSecondThread, secondTid⟩ | isSecondNew
          · have secondCarriesOld := (carriesImage second secondOld isSecondOld).mpr secondCarries
            rw [imageOther secondOld (otherStep second secondOld isSecondThread secondTid
              isSecondOld secondCarriesOld)] at secondHasWait
            have isLater := sinceBelow second isSecondThread secondOn secondSince secondBlocked
            have isLaterSeq := seqBelow secondOld isSecondOld
              (waitingOfBlocked second secondOld secondOn secondSince isSecondThread isSecondOld
                secondActive secondCarriesOld secondBlocked) secondWait secondHasWait
            exact ⟨fun isBefore => absurd isBefore (Nat.lt_asymm isLater),
              fun isBefore => absurd isBefore (Nat.lt_asymm isLaterSeq)⟩
          · rw [isSecondNew] at secondCarries secondBlocked
            have isSecondChanged := recordOfNew secondOld isSecondOld secondActive secondCarries
            rw [isSecondChanged, imageRecord, hasWait] at secondHasWait
            cases secondHasWait
            have sameSince : firstSince = secondSince := by
              have samePhase := firstBlocked.symm.trans secondBlocked
              injection samePhase
            rw [sameSince]
            simp only [Nat.lt_irrefl]
      tasksAreCurrent := tasksAreCurrent }

/-! ## From the Dex run after `register(e, W)` to the simulation -/

/-- What a registration step starts from: a `waitStarted` thread, the Step
that carries it, and the WaitFor task of that Step. -/
structure wp3a_Context (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (tid : Tid) (thread : L.Thread)
    (run : Dex.Run signature) (record : Dex.StepRecord signature)
    (task : Dex.Task signature) : Prop where
  /-- The configurations are a simulation state. -/
  state : IsSimulationState program lConfig dexConfig
  /-- The thread that steps. -/
  isFound : lConfig.findThread? tid = some thread
  /-- It began a wait. -/
  isWaitStarted : thread.phase = .waitStarted
  /-- The L step may stamp. -/
  canStamp : lConfig.canStamp = true
  /-- The Flow runs. -/
  hasRun : dexConfig.run = some run
  /-- The run is not closed. -/
  isRunning : run.status = .running
  /-- The Step is in the run. -/
  isRecord : record ∈ run.steps
  /-- The Step is active. -/
  isActive : record.phase.isActive = true
  /-- The Step carries the thread. -/
  carries : StepCarriesThread program thread record
  /-- The task is handed to a Worker. -/
  isTask : task ∈ dexConfig.tasks
  /-- The task runs the WaitFor of the Step. -/
  hasInvocation : record.invocation = some { method := .waitFor, state := .running task.id }
  /-- The task belongs to the Step. -/
  hasStep : task.step = record.id
  /-- The task runs WaitFor. -/
  isWaitFor : task.method = .waitFor
  /-- The task has the Step's input. -/
  hasInput : task.input = record.input
  /-- The task holds no effect. -/
  noEffects : pendingEffects ⟦program⟧ task = []

/-- What a registration leaves unchanged in the L configuration, and its stamp. -/
structure wp3a_LFrame (lConfig lNext : L.Config) (tid : Tid) (newPhase : Phase) : Prop where
  /-- Only the phase of the thread changes. -/
  threads : lNext.threads = (lConfig.setPhase tid newPhase).threads
  /-- The shared variables stay. -/
  shared : lNext.shared = lConfig.shared
  /-- The waiter lists stay. -/
  cvQueues : lNext.cvQueues = lConfig.cvQueues
  /-- The cancel flags stay. -/
  cancelFlags : lNext.cancelFlags = lConfig.cancelFlags
  /-- The timer counter stays. -/
  timerCounter : lNext.timerCounter = lConfig.timerCounter
  /-- The clock stays. -/
  clock : lNext.clock = lConfig.clock
  /-- The step is stamped. -/
  lastStampAt : lNext.lastStampAt = some lConfig.clock
  /-- The status stays. -/
  status : lNext.status = lConfig.status

/-- Composing two changes of the same Step. -/
theorem wp3a_map_changeStep (steps : List (Dex.StepRecord signature))
    (step : Dex.StepExecId signature)
    (first second : Dex.StepRecord signature → Dex.StepRecord signature)
    (keepsId : ∀ candidate, (first candidate).id = candidate.id) :
    (steps.map (wp3a_changeStep step first)).map (wp3a_changeStep step second) =
      steps.map (wp3a_changeStep step (fun candidate => second (first candidate))) := by
  rw [List.map_map]
  apply List.map_congr_left
  intro candidate _
  by_cases isChanged : candidate.id = step <;>
    simp [wp3a_changeStep, isChanged, keepsId]

/-- A registration is simulated, given the run that `register(e, W)` yields
and its relation to the next L configuration. -/
theorem wp3a_simulated_of_run {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {tid : Tid} {thread : L.Thread}
    {run : Dex.Run signature} {record : Dex.StepRecord signature} {task : Dex.Task signature}
    (context : wp3a_Context program lConfig dexConfig tid thread run record task)
    (newPhase : Phase) (isNotBoot : isBootPhase newPhase = false)
    (isNotHalted : newPhase ≠ .halted) (lNext : L.Config)
    (frame : wp3a_LFrame lConfig lNext tid newPhase) (newRun : Dex.Run signature)
    (isNewRun : (wp3a_preparedRun run record.id).registerWait record.id (wp3a_wait thread)
      dexConfig.clock = newRun)
    (change : Dex.StepRecord signature → Dex.StepRecord signature)
    (sameSteps : newRun.steps = run.steps.map (wp3a_changeStep record.id change))
    (keepsId : ∀ candidate, (change candidate).id = candidate.id)
    (changeKeeps : (change record).input = record.input ∧
      (change record).options = record.options ∧ (change record).phase.isActive = true)
    (sameAttributes : newRun.attributes = run.attributes)
    (sameCounters : newRun.counters = run.counters)
    (sameOutputs : newRun.outputs = run.outputs)
    (isRunningNew : newRun.status = .running)
    (channels : wp3a_ChannelsRelated lNext run newRun tid newPhase)
    (timersField : ∀ timer fireTime, newRun.timers.get? timer = some fireTime ↔
      ∃ owner chan since, owner ∈ lNext.threads ∧
        owner.phase = .blocked (.timed timer.step.number fireTime chan) since ∧
        timer.step.stepType = StepType.timed ∧ timer.index = 0)
    (blockedOrder : ∀ on since, newPhase = .blocked on since →
      (∀ other, other ∈ lConfig.threads → ∀ otherOn otherSince,
        other.phase = .blocked otherOn otherSince → otherSince < since) ∧
      ∃ waitState, (change record).waitState = some waitState ∧
        ∀ other, other ∈ run.steps → other.phase = .waiting → ∀ otherWait,
          other.waitState = some otherWait →
          otherWait.registeredSeq < waitState.registeredSeq)
    (newMatch : ∀ dexNext, PhaseMatchesStep program lConfig dexNext
      { thread with phase := newPhase } (change record)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) [] lNext := by
  have state := context.state
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? context.isFound
  have related := state.isRelated.running run context.hasRun context.isRunning
  have idsNodup := (state.dexInvariant.runInvariant run context.hasRun).idsNodup
  have isResponse := wp3a_compute_waitFor program thread record task context.isWaitStarted
    (state.lInvariant.phasesFitCode thread isMember) context.carries context.hasStep
    context.isWaitFor context.hasInput
  have isExec := wp3a_exec_completeWaitFor program dexConfig state.tasksDistinct task
    context.isTask (state.dexInvariant.tasksComputed task context.isTask) context.noEffects
    context.isWaitFor _ isResponse run context.hasRun
    (related.tasksAreCurrent task context.isTask)
    (Related.canCommit_of_canStamp state.isRelated context.canStamp)
  rw [context.hasStep, wp3a_completedRun_eq, isNewRun] at isExec
  have hasActions := simulatingActions_complete related context.hasRun state.tasksDistinct
    context.isFound record context.isRecord context.isActive
    ((threadOfInput_of_mem state.lInvariant isMember context.carries).trans hasTid)
    .waitFor task context.isTask context.hasInvocation
    (Or.inr (Or.inr (Or.inr (Or.inl context.isWaitStarted))))
  have statusL := (IsSimulationState.run_of_thread state thread isMember).1
  -- Every other task stays.
  have taskOfStep : ∀ other, other ∈ dexConfig.tasks → other.id = task.id → other = task :=
    fun other isOther sameId =>
      eq_of_nodup_map (fun current => current.id) dexConfig.tasks state.tasksDistinct.1 isOther
        context.isTask sameId
  refine ⟨_, _, by rw [hasActions]; exact isExec, ?_, ?_⟩
  · simp [Dex.Config.closeEvents, context.isRunning, isRunningNew]
  · exact
      { clock := frame.clock.trans state.isRelated.clock
        stamp := frame.lastStampAt.trans (congrArg some state.isRelated.clock)
        status := by
          rw [frame.status, statusL]
          exact isRunningNew
        notStarted := fun isNone => by cases isNone
        running := fun after isAfter _ => by
          cases isAfter
          refine wp3a_runningRelated_register related state.lInvariant idsNodup context.isFound
            context.isWaitStarted context.isRecord context.isActive context.carries newPhase
            isNotBoot isNotHalted frame.threads frame.shared frame.cvQueues frame.cancelFlags
            frame.timerCounter change sameSteps ⟨keepsId record, changeKeeps⟩ sameAttributes
            sameCounters sameOutputs channels timersField blockedOrder ?_ (newMatch _) ?_
          · intro other isOther isOtherStep
            refine List.mem_filter.mpr ⟨isOther, ?_⟩
            simp only [Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not]
            intro sameId
            rw [taskOfStep other isOther sameId] at isOtherStep
            exact isOtherStep context.hasStep
          · intro other isOther
            obtain ⟨wasMember, isOtherId⟩ := List.mem_filter.mp isOther
            simp only [Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not] at isOtherId
            have wasCurrent := related.tasksAreCurrent other wasMember
            unfold Dex.Config.isTaskCurrent at wasCurrent
            simp only [context.hasRun] at wasCurrent
            show (wp3a_completedConfig dexConfig task newRun).isTaskCurrent other = true
            unfold Dex.Config.isTaskCurrent
            simp only [wp3a_completedConfig]
            cases foundCase : run.findStep? other.step with
            | none => simp [foundCase] at wasCurrent
            | some otherRecord =>
              simp only [foundCase, decide_eq_true_eq] at wasCurrent
              obtain ⟨isOtherRecord, otherStepId⟩ := Dex.Run.mem_of_findStep? run _ _ foundCase
              have isNotRecord : other.step ≠ record.id := by
                intro sameId
                have sameRecord : otherRecord = record :=
                  eq_of_nodup_map (fun candidate => candidate.id) run.steps idsNodup isOtherRecord
                    context.isRecord (otherStepId.trans sameId)
                rw [sameRecord, context.hasInvocation] at wasCurrent
                injection wasCurrent with sameInvocation
                injection sameInvocation with _ sameState
                injection sameState with sameTask
                exact isOtherId sameTask.symm
              have foundNew : newRun.findStep? other.step = some otherRecord := by
                unfold Dex.Run.findStep?
                rw [sameSteps]
                exact (Dex.find?_map_update_other run.steps record.id other.step change
                  keepsId isNotRecord).trans foundCase
              simp [foundNew, wasCurrent]
        closed := fun after status isAfter isClosed => by
          cases isAfter
          rw [isRunningNew] at isClosed
          cases isClosed }

/-! ## The timer clause -/

/-- No armed timer belongs to the Step of a `waitStarted` thread. -/
theorem wp3a_no_timer_of_record {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (lInvariant : L.Invariant program lConfig)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    (isWaitStarted : thread.phase = .waitStarted)
    {record : Dex.StepRecord signature} (carries : StepCarriesThread program thread record)
    (timer : Dex.TimerId signature) (fireTime : Nat)
    (isArmed : run.timers.get? timer = some fireTime) : timer.step ≠ record.id := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  obtain ⟨owner, chan, since, isOwner, isBlocked, isTimed, _⟩ :=
    (related.timers timer fireTime).mp isArmed
  intro sameStep
  have isNotBoot : isBootPhase thread.phase = false := by
    rw [isWaitStarted]
    rfl
  cases timerCase : thread.timer with
  | none =>
    have isPoint := wp2_record_of_no_timer carries isNotBoot timerCase
    rw [← sameStep, isTimed] at isPoint
    cases isPoint
  | some entry =>
    obtain ⟨number, seconds⟩ := entry
    have isRecordId := wp2_record_of_timer carries isNotBoot timerCase
    obtain ⟨⟨ownerSeconds, ownerTimer⟩, _⟩ :=
      wp2_timer_of_blocked_timed lInvariant isOwner isBlocked
    have sameNumber : timer.step.number = number := by rw [sameStep, isRecordId]
    rw [sameNumber] at ownerTimer
    have isThread := lInvariant.timerNumbersInjective owner isOwner thread isMember number
      ownerSeconds seconds ownerTimer timerCase
    rw [isThread, isWaitStarted] at isBlocked
    cases isBlocked

/-- The timer clause when the timers stay and the thread arms none. -/
theorem wp3a_timers_same {program : Program} {lConfig lNext : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (lInvariant : L.Invariant program lConfig)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    (isWaitStarted : thread.phase = .waitStarted) (newPhase : Phase)
    (isNotTimed : ∀ number deadline chan since,
      newPhase ≠ .blocked (.timed number deadline chan) since)
    (sameThreads : lNext.threads = (lConfig.setPhase tid newPhase).threads)
    (newTimers : AssocList (Dex.TimerId signature) Nat)
    (hasTimers : ∀ timer fireTime,
      newTimers.get? timer = some fireTime ↔ run.timers.get? timer = some fireTime) :
    ∀ timer fireTime, newTimers.get? timer = some fireTime ↔
      ∃ owner chan since, owner ∈ lNext.threads ∧
        owner.phase = .blocked (.timed timer.step.number fireTime chan) since ∧
        timer.step.stepType = StepType.timed ∧ timer.index = 0 := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  have memNew := mem_setPhase lInvariant isFound newPhase
  intro timer fireTime
  rw [hasTimers, related.timers, sameThreads]
  constructor
  · rintro ⟨owner, chan, since, isOwner, isBlocked, rest⟩
    refine ⟨owner, chan, since, (memNew owner).mpr (Or.inl ⟨isOwner, ?_⟩), isBlocked, rest⟩
    intro isChanged
    have isThread : owner = thread :=
      thread_eq_of_tid lInvariant isOwner isMember (isChanged.trans hasTid.symm)
    rw [isThread, isWaitStarted] at isBlocked
    cases isBlocked
  · rintro ⟨owner, chan, since, isOwner, isBlocked, rest⟩
    rcases (memNew owner).mp isOwner with ⟨isOld, _⟩ | isNew
    · exact ⟨owner, chan, since, isOld, isBlocked, rest⟩
    · rw [isNew] at isBlocked
      exact (isNotTimed _ _ _ _ isBlocked).elim

/-- Disarming the timers of the Step of a `waitStarted` thread removes none. -/
theorem wp3a_get?_disarmed {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (lInvariant : L.Invariant program lConfig)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    (isWaitStarted : thread.phase = .waitStarted)
    {record : Dex.StepRecord signature} (carries : StepCarriesThread program thread record)
    (timer : Dex.TimerId signature) (fireTime : Nat) :
    AssocList.get? (run.timers.filter (fun entry => !decide (entry.1.step = record.id))) timer =
        some fireTime ↔
      run.timers.get? timer = some fireTime := by
  by_cases isStep : timer.step = record.id
  · constructor
    · intro isKept
      rw [AssocList.get?_filter_of_rejected
        (fun key : Dex.TimerId signature => !decide (key.step = record.id)) run.timers timer
        (by simp [isStep])] at isKept
      cases isKept
    · intro isArmed
      exact absurd isStep (wp3a_no_timer_of_record related lInvariant isFound isWaitStarted
        carries timer fireTime isArmed)
  · rw [AssocList.get?_filter_of_kept
      (fun key : Dex.TimerId signature => !decide (key.step = record.id)) run.timers timer
      (by simp [isStep])]

/-- The timer clause when the thread arms the timer of its `Timed` Step. -/
theorem wp3a_timers_armed {program : Program} {lConfig lNext : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (lInvariant : L.Invariant program lConfig)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    (isWaitStarted : thread.phase = .waitStarted)
    {record : Dex.StepRecord signature} (carries : StepCarriesThread program thread record)
    (number seconds : Nat) (hasTimer : thread.timer = some (number, seconds))
    (fireAt : Nat) (chan : Option ExtChan) (since : Nat)
    (sameThreads : lNext.threads =
      (lConfig.setPhase tid (.blocked (.timed number fireAt chan) since)).threads) :
    ∀ timer fireTime,
      AssocList.get? (run.timers.set { step := record.id, index := 0 } fireAt) timer =
          some fireTime ↔
        ∃ owner ownerChan ownerSince, owner ∈ lNext.threads ∧
          owner.phase = .blocked (.timed timer.step.number fireTime ownerChan) ownerSince ∧
          timer.step.stepType = StepType.timed ∧ timer.index = 0 := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  have memNew := mem_setPhase lInvariant isFound (.blocked (.timed number fireAt chan) since)
  have isNotBoot : isBootPhase thread.phase = false := by
    rw [isWaitStarted]
    rfl
  have isRecordId := wp2_record_of_timer carries isNotBoot hasTimer
  -- No other thread is blocked on the timer number of this thread.
  have notOld : ∀ owner, owner ∈ lConfig.threads → ∀ deadline ownerChan ownerSince,
      owner.phase ≠ .blocked (.timed number deadline ownerChan) ownerSince := by
    intro owner isOwner deadline ownerChan ownerSince isBlocked
    obtain ⟨⟨ownerSeconds, ownerTimer⟩, _⟩ :=
      wp2_timer_of_blocked_timed lInvariant isOwner isBlocked
    have isThread := lInvariant.timerNumbersInjective owner isOwner thread isMember number
      ownerSeconds seconds ownerTimer hasTimer
    rw [isThread, isWaitStarted] at isBlocked
    cases isBlocked
  intro timer fireTime
  rw [sameThreads]
  by_cases isNew : timer = { step := record.id, index := 0 }
  · rw [isNew, AssocList.get?_set_self]
    constructor
    · intro sameTime
      injection sameTime with sameTime
      rw [← sameTime]
      exact ⟨_, chan, since, (memNew _).mpr (Or.inr rfl), by simp [isRecordId],
        by rw [isRecordId], rfl⟩
    · rintro ⟨owner, ownerChan, ownerSince, isOwner, isBlocked, _⟩
      rcases (memNew owner).mp isOwner with ⟨isOld, _⟩ | isNewOwner
      · dsimp only at isBlocked
        rw [isRecordId] at isBlocked
        exact absurd isBlocked (notOld owner isOld _ _ _)
      · rw [isNewOwner] at isBlocked
        injection isBlocked with sameOn
        injection sameOn with _ sameDeadline
        rw [sameDeadline]
  · rw [AssocList.get?_set_other _ _ _ _ isNew, related.timers]
    constructor
    · rintro ⟨owner, ownerChan, ownerSince, isOwner, isBlocked, rest⟩
      refine ⟨owner, ownerChan, ownerSince, (memNew owner).mpr (Or.inl ⟨isOwner, ?_⟩), isBlocked,
        rest⟩
      intro isChanged
      have isThread : owner = thread :=
        thread_eq_of_tid lInvariant isOwner isMember (isChanged.trans hasTid.symm)
      rw [isThread, isWaitStarted] at isBlocked
      cases isBlocked
    · rintro ⟨owner, ownerChan, ownerSince, isOwner, isBlocked, isTimed, isIndex⟩
      rcases (memNew owner).mp isOwner with ⟨isOld, _⟩ | isNewOwner
      · exact ⟨owner, ownerChan, ownerSince, isOld, isBlocked, isTimed, isIndex⟩
      · rw [isNewOwner] at isBlocked
        injection isBlocked with sameOn
        injection sameOn with sameNumber
        exfalso
        apply isNew
        obtain ⟨⟨stepType, stepNumber⟩, index⟩ := timer
        simp only at isTimed isIndex sameNumber
        rw [isRecordId, isTimed, isIndex, sameNumber]

/-! ## The Channel clauses -/

/-- The Channel clauses when no Channel and no L queue changes. -/
theorem wp3a_channels_same {program : Program} {lConfig lNext : L.Config}
    {dexConfig : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (areBooted : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (tid : Tid) (newPhase : Phase)
    (sameInput : lNext.inputQueues = lConfig.inputQueues)
    (sameOwners : lNext.mutexOwners = lConfig.mutexOwners)
    (sameJoinable : lNext.joinable = lConfig.joinable)
    (sameWakes : lNext.cvWakes = lConfig.cvWakes)
    (samePending : ∀ chan, newRun.pendingMessages chan = run.pendingMessages chan) :
    wp3a_ChannelsRelated lNext run newRun tid newPhase :=
  { inputQueues := fun chan => by
      rw [samePending]
      show _ = ((lNext.inputQueues.get? chan).getD []).map _
      rw [sameInput]
      exact related.inputQueues chan
    mutexTokens := fun mutex => by
      rw [samePending, sameOwners]
      exact related.mutexTokens areBooted mutex
    joinTokens := fun joined => by
      rw [samePending, sameJoinable]
      exact related.joinTokens joined
    cvWakes := fun cv waiter => by
      rw [samePending, sameWakes]
      exact related.cvWakes cv waiter
    cancelOther := fun other _ => samePending _
    cancelBefore := fun isPending => by
      rw [samePending] at isPending
      exact isPending
    cancelAfter := fun isPending => Or.inl (by rw [samePending]; exact isPending) }

/-- The Channel clauses when the thread receives the head of an external Channel. -/
theorem wp3a_channels_external {program : Program} {lConfig lNext : L.Config}
    {dexConfig : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (areBooted : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (tid : Tid) (newPhase : Phase) (chan : ExtChan) (later : List Val)
    (nextInput : lNext.inputQueues = lConfig.inputQueues.set chan later)
    (sameOwners : lNext.mutexOwners = lConfig.mutexOwners)
    (sameJoinable : lNext.joinable = lConfig.joinable)
    (sameWakes : lNext.cvWakes = lConfig.cvWakes)
    (isPopped : newRun.pendingMessages (Chan.external chan) = later.map DexVal.value)
    (sameOthers : ∀ other, other ≠ Chan.external chan →
      newRun.pendingMessages other = run.pendingMessages other) :
    wp3a_ChannelsRelated lNext run newRun tid newPhase :=
  { inputQueues := fun other => by
      show _ = ((lNext.inputQueues.get? other).getD []).map _
      rw [nextInput]
      by_cases isChan : other = chan
      · rw [isChan, isPopped, AssocList.get?_set_self]
        rfl
      · rw [sameOthers _ (by simp [isChan]), AssocList.get?_set_other _ _ _ _ isChan]
        exact related.inputQueues other
    mutexTokens := fun mutex => by
      rw [sameOthers _ (by simp), sameOwners]
      exact related.mutexTokens areBooted mutex
    joinTokens := fun joined => by
      rw [sameOthers _ (by simp), sameJoinable]
      exact related.joinTokens joined
    cvWakes := fun cv waiter => by
      rw [sameOthers _ (by simp), sameWakes]
      exact related.cvWakes cv waiter
    cancelOther := fun other _ => sameOthers _ (by simp)
    cancelBefore := fun isPending => by
      rw [sameOthers _ (by simp)] at isPending
      exact isPending
    cancelAfter := fun isPending => Or.inl (by rw [sameOthers _ (by simp)]; exact isPending) }

/-- The Channel clauses when the thread takes the token of a mutex. -/
theorem wp3a_channels_mutex {program : Program} {lConfig lNext : L.Config}
    {dexConfig : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (areBooted : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (tid : Tid) (newPhase : Phase) (mutex : MutexId)
    (sameInput : lNext.inputQueues = lConfig.inputQueues)
    (nextOwners : lNext.mutexOwners = lConfig.mutexOwners.set mutex (some tid))
    (sameJoinable : lNext.joinable = lConfig.joinable)
    (sameWakes : lNext.cvWakes = lConfig.cvWakes)
    (isPopped : newRun.pendingMessages (Chan.mutexToken mutex) = [])
    (sameOthers : ∀ other, other ≠ Chan.mutexToken mutex →
      newRun.pendingMessages other = run.pendingMessages other) :
    wp3a_ChannelsRelated lNext run newRun tid newPhase :=
  { inputQueues := fun chan => by
      rw [sameOthers _ (by simp)]
      show _ = ((lNext.inputQueues.get? chan).getD []).map _
      rw [sameInput]
      exact related.inputQueues chan
    mutexTokens := fun other => by
      rw [nextOwners]
      by_cases isMutex : other = mutex
      · rw [isMutex, isPopped, AssocList.get?_set_self]
        simp
      · rw [sameOthers _ (by simp [isMutex]), AssocList.get?_set_other _ _ _ _ isMutex]
        exact related.mutexTokens areBooted other
    joinTokens := fun joined => by
      rw [sameOthers _ (by simp), sameJoinable]
      exact related.joinTokens joined
    cvWakes := fun cv waiter => by
      rw [sameOthers _ (by simp), sameWakes]
      exact related.cvWakes cv waiter
    cancelOther := fun other _ => sameOthers _ (by simp)
    cancelBefore := fun isPending => by
      rw [sameOthers _ (by simp)] at isPending
      exact isPending
    cancelAfter := fun isPending => Or.inl (by rw [sameOthers _ (by simp)]; exact isPending) }

/-- The Channel clauses when the thread joins an ended thread. -/
theorem wp3a_channels_join {program : Program} {lConfig lNext : L.Config}
    {dexConfig : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (areBooted : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (joinableNodup : lConfig.joinable.Nodup)
    (tid : Tid) (newPhase : Phase) (target : Tid)
    (sameInput : lNext.inputQueues = lConfig.inputQueues)
    (sameOwners : lNext.mutexOwners = lConfig.mutexOwners)
    (nextJoinable : lNext.joinable = lConfig.joinable.erase target)
    (sameWakes : lNext.cvWakes = lConfig.cvWakes)
    (isPopped : newRun.pendingMessages (Chan.joinDone target) = [])
    (sameOthers : ∀ other, other ≠ Chan.joinDone target →
      newRun.pendingMessages other = run.pendingMessages other) :
    wp3a_ChannelsRelated lNext run newRun tid newPhase :=
  { inputQueues := fun chan => by
      rw [sameOthers _ (by simp)]
      show _ = ((lNext.inputQueues.get? chan).getD []).map _
      rw [sameInput]
      exact related.inputQueues chan
    mutexTokens := fun mutex => by
      rw [sameOthers _ (by simp), sameOwners]
      exact related.mutexTokens areBooted mutex
    joinTokens := fun joined => by
      rw [nextJoinable]
      by_cases isTarget : joined = target
      · rw [isTarget, isPopped]
        simp [joinableNodup.not_mem_erase]
      · rw [sameOthers _ (by simp [isTarget]), related.joinTokens joined]
        simp [List.mem_erase_of_ne isTarget]
    cvWakes := fun cv waiter => by
      rw [sameOthers _ (by simp), sameWakes]
      exact related.cvWakes cv waiter
    cancelOther := fun other _ => sameOthers _ (by simp)
    cancelBefore := fun isPending => by
      rw [sameOthers _ (by simp)] at isPending
      exact isPending
    cancelAfter := fun isPending => Or.inl (by rw [sameOthers _ (by simp)]; exact isPending) }

/-- The Channel clauses when the thread finds an early wake of its wait. -/
theorem wp3a_channels_cvWake {program : Program} {lConfig lNext : L.Config}
    {dexConfig : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (areBooted : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (tid : Tid) (newPhase : Phase) (cv : CvId)
    (sameInput : lNext.inputQueues = lConfig.inputQueues)
    (sameOwners : lNext.mutexOwners = lConfig.mutexOwners)
    (sameJoinable : lNext.joinable = lConfig.joinable)
    (nextWakes : lNext.cvWakes = lConfig.cvWakes.erase (cv, tid))
    (message : DexVal) (remaining : List DexVal)
    (hasMessage : run.pendingMessages (Chan.cvWake cv tid) = message :: remaining)
    (isPopped : newRun.pendingMessages (Chan.cvWake cv tid) = remaining)
    (sameOthers : ∀ other, other ≠ Chan.cvWake cv tid →
      newRun.pendingMessages other = run.pendingMessages other) :
    wp3a_ChannelsRelated lNext run newRun tid newPhase :=
  { inputQueues := fun chan => by
      rw [sameOthers _ (by simp)]
      show _ = ((lNext.inputQueues.get? chan).getD []).map _
      rw [sameInput]
      exact related.inputQueues chan
    mutexTokens := fun mutex => by
      rw [sameOthers _ (by simp), sameOwners]
      exact related.mutexTokens areBooted mutex
    joinTokens := fun joined => by
      rw [sameOthers _ (by simp), sameJoinable]
      exact related.joinTokens joined
    cvWakes := fun otherCv waiter => by
      rw [nextWakes]
      by_cases isWake : (otherCv, waiter) = (cv, tid)
      · injection isWake with sameCv sameWaiter
        rw [sameCv, sameWaiter, isPopped, List.count_erase_self]
        have wasCounted := related.cvWakes cv tid
        rw [hasMessage] at wasCounted
        simp only [List.length_cons] at wasCounted
        omega
      · rw [sameOthers _ (by
            intro sameChan
            injection sameChan with sameCv sameWaiter
            exact isWake (by rw [sameCv, sameWaiter])),
          List.count_erase_of_ne isWake]
        exact related.cvWakes otherCv waiter
    cancelOther := fun other _ => sameOthers _ (by simp)
    cancelBefore := fun isPending => by
      rw [sameOthers _ (by simp)] at isPending
      exact isPending
    cancelAfter := fun isPending => Or.inl (by rw [sameOthers _ (by simp)]; exact isPending) }

/-- The Channel clauses when the thread observes its cancel request. -/
theorem wp3a_channels_cancel {program : Program} {lConfig lNext : L.Config}
    {dexConfig : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (areBooted : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (tid : Tid)
    (sameInput : lNext.inputQueues = lConfig.inputQueues)
    (sameOwners : lNext.mutexOwners = lConfig.mutexOwners)
    (sameJoinable : lNext.joinable = lConfig.joinable)
    (sameWakes : lNext.cvWakes = lConfig.cvWakes)
    (message : DexVal) (remaining : List DexVal)
    (hasMessage : run.pendingMessages (Chan.cancelRequest tid) = message :: remaining)
    (sameOthers : ∀ other, other ≠ Chan.cancelRequest tid →
      newRun.pendingMessages other = run.pendingMessages other) :
    wp3a_ChannelsRelated lNext run newRun tid (.woken .canceled) :=
  { inputQueues := fun chan => by
      rw [sameOthers _ (by simp)]
      show _ = ((lNext.inputQueues.get? chan).getD []).map _
      rw [sameInput]
      exact related.inputQueues chan
    mutexTokens := fun mutex => by
      rw [sameOthers _ (by simp), sameOwners]
      exact related.mutexTokens areBooted mutex
    joinTokens := fun joined => by
      rw [sameOthers _ (by simp), sameJoinable]
      exact related.joinTokens joined
    cvWakes := fun cv waiter => by
      rw [sameOthers _ (by simp), sameWakes]
      exact related.cvWakes cv waiter
    cancelOther := fun other isOther => sameOthers _ (by simp [isOther])
    cancelBefore := fun _ => by simp [hasMessage]
    cancelAfter := fun _ => Or.inr rfl }

/-! ## The three kinds of registration -/

/-- The prepared run finds the Step without its invocation. -/
theorem wp3a_findStep?_preparedRun {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {tid : Tid} {thread : L.Thread}
    {run : Dex.Run signature} {record : Dex.StepRecord signature} {task : Dex.Task signature}
    (context : wp3a_Context program lConfig dexConfig tid thread run record task) :
    (wp3a_preparedRun run record.id).findStep? record.id =
      some { record with invocation := none } :=
  Dex.Run.findStep?_updateStep_self ({ run with seq := run.seq + 1 } : Dex.Run signature)
    record.id _ record
    (Dex.Run.findStep?_of_mem_nodup run record context.isRecord
      (context.state.dexInvariant.runInvariant run context.hasRun).idsNodup) rfl

/-- A wait with no condition: the thread is woken with the decoded outcome. -/
theorem wp3a_simulated_empty {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {tid : Tid} {thread : L.Thread}
    {run : Dex.Run signature} {record : Dex.StepRecord signature} {task : Dex.Task signature}
    (context : wp3a_Context program lConfig dexConfig tid thread run record task)
    (hasNoCondition : (wp3a_wait thread).hasNoCondition = true) (outcome : WakeOutcome)
    (isOutcome : decodeOutcome thread (some { timersDone := [], channelMessages := [] }) =
      outcome) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) []
      (lConfig.wakeThread tid outcome).stamp := by
  have related := context.state.isRelated.running run context.hasRun context.isRunning
  have lInvariant := context.state.lInvariant
  exact wp3a_simulated_of_run context (.woken outcome) rfl (by simp) _
    ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ _
    (wp3a_registerWait_empty _ _ _ _ hasNoCondition)
    (fun candidate => wp3a_toExecute { timersDone := [], channelMessages := [] }
      { candidate with invocation := none })
    (wp3a_map_changeStep run.steps record.id _ _ (fun _ => rfl))
    (fun _ => rfl) ⟨rfl, rfl, rfl⟩ rfl rfl rfl context.isRunning
    (wp3a_channels_same related (noBoot_of_waitStarted lInvariant context.isFound context.isWaitStarted)
      tid _ rfl rfl rfl rfl (fun _ => rfl))
    (wp3a_timers_same related lInvariant context.isFound context.isWaitStarted _
      (by intro _ _ _ _ isBlocked; cases isBlocked) rfl _ (fun _ _ => Iff.rfl))
    (fun _ _ isBlocked => by cases isBlocked)
    (fun _ => by
      simp only [PhaseMatchesStep]
      exact ⟨rfl, rfl, isOutcome⟩)

/-- A wait on empty Channels: the thread blocks and its Step waits. -/
theorem wp3a_simulated_blocked {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {tid : Tid} {thread : L.Thread}
    {run : Dex.Run signature} {record : Dex.StepRecord signature} {task : Dex.Task signature}
    (context : wp3a_Context program lConfig dexConfig tid thread run record task)
    (isSupported : Dex.IsSupportedWait (wp3a_wait thread))
    (hasChannel : (wp3a_wait thread).channels ≠ [])
    (isQuiet : ∀ chan, chan ∈ (wp3a_wait thread).channels → run.pendingMessages chan = [])
    (on : BlockedOn)
    (hasKind : (wp3a_wait thread).kind = (expectedWaitShape thread.core.tid on).1)
    (hasTimerCount : (wp3a_wait thread).timerSeconds.length =
      (expectedWaitShape thread.core.tid on).2.1)
    (hasChannels : (wp3a_wait thread).channels = (expectedWaitShape thread.core.tid on).2.2)
    (timerCase :
      ((wp3a_wait thread).timerSeconds = [] ∧
        ∀ number deadline chan, on ≠ .timed number deadline chan) ∨
      ∃ number seconds chan, thread.timer = some (number, seconds) ∧
        (wp3a_wait thread).timerSeconds = [seconds] ∧
        on = .timed number (lConfig.clock + seconds * 1000000) chan) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) []
      (lConfig.blockThread tid on).stamp := by
  have state := context.state
  have related := state.isRelated.running run context.hasRun context.isRunning
  have lInvariant := state.lInvariant
  have runInvariant := state.dexInvariant.runInvariant run context.hasRun
  refine wp3a_simulated_of_run context (.blocked on lConfig.blockCounter) rfl (by simp) _
    ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ _
    (wp3a_registerWait_blocks _ _ _ (wp3a_findStep?_preparedRun context) _ _ isSupported
      hasChannel isQuiet)
    (fun candidate => wp3a_toWaiting (wp3a_wait thread) dexConfig.clock (run.seq + 1)
      { candidate with invocation := none })
    (wp3a_map_changeStep run.steps record.id _ _ (fun _ => rfl))
    (fun _ => rfl) ⟨rfl, rfl, rfl⟩ rfl rfl rfl context.isRunning
    (wp3a_channels_same related (noBoot_of_waitStarted lInvariant context.isFound context.isWaitStarted)
      tid _ rfl rfl rfl rfl (fun _ => rfl))
    ?_
    (fun _ _ isBlocked => by
      injection isBlocked with _ sameSince
      rw [← sameSince]
      exact ⟨fun other isOther otherOn otherSince otherBlocked =>
          lInvariant.sinceBelowCounter other isOther otherOn otherSince otherBlocked,
        _, rfl, fun other isOther isWaiting otherWait hasWait =>
          (runInvariant.waitsSupported other otherWait ⟨isOther, isWaiting, hasWait⟩).2.2.2⟩)
    (fun _ => by
      simp only [PhaseMatchesStep]
      exact ⟨rfl, rfl, _, rfl, hasKind, hasTimerCount, hasChannels, rfl⟩)
  rcases timerCase with ⟨hasNoTimer, isNotTimed⟩ | ⟨number, seconds, chan, hasTimer, hasSeconds, isOn⟩
  · refine wp3a_timers_same related lInvariant context.isFound context.isWaitStarted _
      (by
        intro number deadline chan since isBlocked
        injection isBlocked with sameOn
        exact isNotTimed number deadline chan sameOn) rfl _ (fun timer fireTime => ?_)
    show AssocList.get? (Dex.Run.armTimers record.id
      ((wp3a_wait thread).timerSeconds.map (fun seconds => dexConfig.clock + seconds * 1000000))
      0 run.timers) timer = some fireTime ↔ _
    rw [hasNoTimer]
    rfl
  · intro timer fireTime
    show AssocList.get? (Dex.Run.armTimers record.id
      ((wp3a_wait thread).timerSeconds.map (fun seconds => dexConfig.clock + seconds * 1000000))
      0 run.timers) timer = some fireTime ↔ _
    rw [hasSeconds, ← state.isRelated.clock]
    exact wp3a_timers_armed related lInvariant context.isFound context.isWaitStarted
      context.carries number seconds hasTimer (lConfig.clock + seconds * 1000000) chan
      lConfig.blockCounter (by rw [isOn]; rfl) timer fireTime

/-- A wait that consumes the head of one Channel: the thread is woken with
the decoded outcome. The caller relates the Channels. -/
theorem wp3a_simulated_consumed {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {tid : Tid} {thread : L.Thread}
    {run : Dex.Run signature} {record : Dex.StepRecord signature} {task : Dex.Task signature}
    (context : wp3a_Context program lConfig dexConfig tid thread run record task)
    (chan : Chan) (remaining : List DexVal) (results : Dex.ConditionResults signature)
    (isNewRun : (wp3a_preparedRun run record.id).registerWait record.id (wp3a_wait thread)
        dexConfig.clock =
      wp3a_consumedRun (wp3a_preparedRun run record.id) record.id (wp3a_wait thread)
        dexConfig.clock chan remaining results)
    (outcome : WakeOutcome) (isOutcome : decodeOutcome thread (some results) = outcome)
    (lNext : L.Config) (frame : wp3a_LFrame lConfig lNext tid (.woken outcome))
    (channels : ∀ newRun : Dex.Run signature, newRun.pendingMessages chan = remaining →
      (∀ other, other ≠ chan → newRun.pendingMessages other = run.pendingMessages other) →
      wp3a_ChannelsRelated lNext run newRun tid (.woken outcome)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) [] lNext := by
  have related := context.state.isRelated.running run context.hasRun context.isRunning
  have lInvariant := context.state.lInvariant
  refine wp3a_simulated_of_run context (.woken outcome) rfl (by simp) lNext frame _ isNewRun
    (fun candidate => wp3a_toExecute results
      (wp3a_toWaiting (wp3a_wait thread) dexConfig.clock (run.seq + 1)
        { candidate with invocation := none }))
    ((wp3a_map_changeStep (run.steps.map (wp3a_changeStep record.id
        (fun candidate => { candidate with invocation := none }))) record.id
        (wp3a_toWaiting (wp3a_wait thread) dexConfig.clock (run.seq + 1))
        (wp3a_toExecute results) (fun _ => rfl)).trans
      (wp3a_map_changeStep run.steps record.id
        (fun candidate => { candidate with invocation := none })
        (fun candidate => wp3a_toExecute results
          (wp3a_toWaiting (wp3a_wait thread) dexConfig.clock (run.seq + 1) candidate))
        (fun _ => rfl)))
    (fun _ => rfl) ⟨rfl, rfl, rfl⟩ rfl rfl rfl context.isRunning
    (channels _ ?_ ?_)
    (wp3a_timers_same related lInvariant context.isFound context.isWaitStarted _
      (by intro _ _ _ _ isBlocked; cases isBlocked) frame.threads _ (fun timer fireTime => ?_))
    (fun _ _ isBlocked => by cases isBlocked)
    (fun _ => by
      simp only [PhaseMatchesStep]
      exact ⟨rfl, rfl, isOutcome⟩)
  · simp [wp3a_consumedRun, Dex.Run.pendingMessages, Dex.Run.updateStep,
      Dex.Run.setPendingMessages, AssocList.get?_set_self]
  · intro other isOther
    show (AssocList.get? (AssocList.set run.channels chan remaining) other).getD [] = _
    rw [AssocList.get?_set_other _ _ _ _ isOther]
    rfl
  · show AssocList.get? ((Dex.Run.armTimers record.id
        ((wp3a_wait thread).timerSeconds.map
          (fun seconds => dexConfig.clock + seconds * 1000000)) 0 run.timers).filter
        (fun entry => !decide (entry.1.step = record.id))) timer = some fireTime ↔ _
    rw [Dex.Run.filter_armTimers]
    exact wp3a_get?_disarmed related lInvariant context.isFound context.isWaitStarted
      context.carries timer fireTime

/-! ## Reading the outcome from the condition results -/

/-- `lock` and the re-lock of `wait`: one consumed token means granted. -/
theorem wp3a_decode_lock (core : ThreadCore) (code : Code) (mutex : MutexId)
    (isRequest : waitRequest core code = some (.lock mutex)) (timersDone : List Bool)
    (message : DexVal) :
    decodeBlockedOutcome code
      (some { timersDone := timersDone, channelMessages := [some message] }) = .granted := by
  cases code with
  | nil => simp [waitRequest] at isRequest
  | cons instr rest =>
    cases instr with
    | source statement =>
      cases statement
      all_goals first
        | (simp [decodeBlockedOutcome]; done)
        | (simp [waitRequest] at isRequest; done)
        | (simp only [waitRequest] at isRequest
           split at isRequest <;> cases isRequest)
    | park cv => simp [waitRequest] at isRequest
    | relock held => simp [decodeBlockedOutcome]

/-- The park of `wait`: one consumed wake means granted. -/
theorem wp3a_decode_park (core : ThreadCore) (code : Code) (cv : CvId)
    (isRequest : waitRequest core code = some (.park cv)) (timersDone : List Bool)
    (message : DexVal) :
    decodeBlockedOutcome code
      (some { timersDone := timersDone, channelMessages := [some message] }) = .granted := by
  cases code with
  | nil => simp [waitRequest] at isRequest
  | cons instr rest =>
    cases instr with
    | source statement =>
      cases statement
      all_goals first
        | (simp [waitRequest] at isRequest; done)
        | (simp only [waitRequest] at isRequest
           split at isRequest <;> cases isRequest)
    | park parked => simp [decodeBlockedOutcome]
    | relock held => simp [waitRequest] at isRequest

/-- `recv`: the first condition is the message, the second the cancel request. -/
theorem wp3a_decode_recv (core : ThreadCore) (code : Code) (chan : ExtChan)
    (isRequest : waitRequest core code = some (.recv chan)) (timersDone : List Bool)
    (message : DexVal) :
    (∀ other, decodeBlockedOutcome code
      (some { timersDone := timersDone, channelMessages := [some message, other] }) =
        .received (decodeMessage message)) ∧
    decodeBlockedOutcome code
      (some { timersDone := timersDone, channelMessages := [none, some message] }) =
        .canceled := by
  cases code with
  | nil => simp [waitRequest] at isRequest
  | cons instr rest =>
    cases instr with
    | source statement =>
      cases statement
      all_goals first
        | (simp [decodeBlockedOutcome]; done)
        | (simp [waitRequest] at isRequest; done)
        | (simp only [waitRequest] at isRequest
           split at isRequest <;> cases isRequest)
    | park parked => simp [waitRequest] at isRequest
    | relock held => simp [waitRequest] at isRequest

/-- `join`: the first condition is the join notice, the second the cancel request. -/
theorem wp3a_decode_join (core : ThreadCore) (code : Code) (target : Tid)
    (isRequest : waitRequest core code = some (.join target)) (timersDone : List Bool)
    (message : DexVal) :
    (∀ other, decodeBlockedOutcome code
      (some { timersDone := timersDone, channelMessages := [some message, other] }) =
        .granted) ∧
    decodeBlockedOutcome code
      (some { timersDone := timersDone, channelMessages := [none, some message] }) =
        .canceled := by
  cases code with
  | nil => simp [waitRequest] at isRequest
  | cons instr rest =>
    cases instr with
    | source statement =>
      cases statement
      all_goals first
        | (simp [decodeBlockedOutcome]; done)
        | (simp [waitRequest] at isRequest; done)
        | (simp only [waitRequest] at isRequest
           split at isRequest <;> cases isRequest)
    | park parked => simp [waitRequest] at isRequest
    | relock held => simp [waitRequest] at isRequest

/-- No wait request and no result: the outcome is invalid. -/
theorem wp3a_decode_invalid (code : Code) (timersDone : List Bool) :
    decodeBlockedOutcome code (some { timersDone := timersDone, channelMessages := [] }) =
      .invalid := by
  simp only [decodeBlockedOutcome]

/-- Outside a timed operation the outcome is read as that of a blocking statement. -/
theorem wp3a_decodeOutcome_blocked (thread : L.Thread) (hasNoTimer : thread.timer = none)
    (results : Option (Dex.ConditionResults signature)) :
    decodeOutcome thread results = decodeBlockedOutcome thread.code results := by
  simp [decodeOutcome, hasNoTimer]

/-! ## Every registration -/

/-- The registration of a `waitStarted` thread is simulated, by cases on the
wait and on what `Config.registerWait` finds (spec §8.3). -/
theorem wp3a_registerWait_simulated {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {tid : Tid} {thread : L.Thread}
    {run : Dex.Run signature} {record : Dex.StepRecord signature} {task : Dex.Task signature}
    (context : wp3a_Context program lConfig dexConfig tid thread run record task) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) []
      (lConfig.registerWait thread).stamp := by
  have state := context.state
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? context.isFound
  subst hasTid
  have related := state.isRelated.running run context.hasRun context.isRunning
  have lInvariant := state.lInvariant
  have areBooted := noBoot_of_waitStarted lInvariant context.isFound context.isWaitStarted
  have foundPrepared := wp3a_findStep?_preparedRun context
  have fitsCode := lInvariant.phasesFitCode thread isMember
  unfold L.Thread.PhaseFitsCode at fitsCode
  simp only [context.isWaitStarted] at fitsCode
  -- A set cancel flag of this thread is a pending request, and conversely.
  have cancelPending : lConfig.cancelFlags.contains thread.core.tid = true →
      ∃ message remaining,
        run.pendingMessages (Chan.cancelRequest thread.core.tid) = message :: remaining := by
    intro isFlagged
    rcases related.cancelFlagsAreRequests _ isFlagged with
      isPending | ⟨current, isCurrent, isCanceled⟩ | ⟨isGone, _⟩
    · cases pendingCase : run.pendingMessages (Chan.cancelRequest thread.core.tid) with
      | nil => exact absurd pendingCase isPending
      | cons message remaining => exact ⟨message, remaining, rfl⟩
    · rw [context.isFound] at isCurrent
      cases isCurrent
      rw [context.isWaitStarted] at isCanceled
      rcases isCanceled with isPhase | isPhase <;> cases isPhase
    · rw [context.isFound] at isGone
      cases isGone
  have cancelQuiet : ¬ lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) = [] := by
    intro isNotFlagged
    cases pendingCase : run.pendingMessages (Chan.cancelRequest thread.core.tid) with
    | nil => rfl
    | cons message remaining =>
      exact absurd (related.cancelRequestsAreFlags thread.core.tid (by rw [pendingCase]; simp))
        isNotFlagged
  cases timerCase : thread.timer with
  | some entry =>
    obtain ⟨number, seconds⟩ := entry
    cases chanCase : timedChan thread.code with
    | none =>
      have isNext : lConfig.registerWait thread = lConfig.wakeThread thread.core.tid .invalid := by
        simp [L.Config.registerWait, timerCase, chanCase]
      have isWait : wp3a_wait thread = Dex.Wait.empty signature := by
        simp [wp3a_wait, timerCase, chanCase]
      rw [isNext]
      exact wp3a_simulated_empty context (by rw [isWait]; rfl) .invalid
        (by simp [decodeOutcome, timerCase, decodeTimedOutcome, chanCase])
    | some selected =>
      cases selected with
      | none =>
        have isWait : wp3a_wait thread =
            { kind := .any, timerSeconds := [seconds]
              channels := [Chan.cancelRequest thread.core.tid] } := by
          simp [wp3a_wait, timerCase, chanCase]
        by_cases isCanceled : lConfig.cancelFlags.contains thread.core.tid = true
        · have isNext : lConfig.registerWait thread =
              lConfig.wakeThread thread.core.tid .canceled := by
            simp only [L.Config.registerWait, timerCase, chanCase, isCanceled, ↓reduceIte]
          obtain ⟨message, remaining, hasMessage⟩ := cancelPending isCanceled
          rw [isNext]
          exact wp3a_simulated_consumed context (Chan.cancelRequest thread.core.tid) remaining _
            (by
              rw [isWait]
              exact wp3a_registerWait_any_first _ _ _ foundPrepared [seconds] _ [] _ message
                remaining hasMessage)
            .canceled (by simp [decodeOutcome, timerCase, decodeTimedOutcome, chanCase])
            _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
            (fun _ _ sameOthers => wp3a_channels_cancel related areBooted _ rfl rfl rfl rfl
              message remaining hasMessage sameOthers)
        · have isNext : lConfig.registerWait thread =
              lConfig.blockThread thread.core.tid
                (.timed number (lConfig.clock + seconds * 1000000) none) := by
            simp only [L.Config.registerWait, timerCase, chanCase,
              isCanceled, Bool.false_eq_true, ↓reduceIte]
          rw [isNext]
          exact wp3a_simulated_blocked context (by rw [isWait]; exact Or.inr (Or.inl rfl))
            (by rw [isWait]; simp)
            (by
              rw [isWait]
              intro chan isNamed
              simp only [List.mem_singleton] at isNamed
              rw [isNamed]
              exact cancelQuiet isCanceled)
            _ (by rw [isWait]; rfl) (by rw [isWait]; rfl) (by rw [isWait]; rfl)
            (Or.inr ⟨number, seconds, none, timerCase, by rw [isWait], rfl⟩)
      | some chan =>
        have isWait : wp3a_wait thread =
            { kind := .any, timerSeconds := [seconds]
              channels := [Chan.external chan, Chan.cancelRequest thread.core.tid] } := by
          simp [wp3a_wait, timerCase, chanCase]
        cases inputCase : lConfig.pendingInput chan with
        | cons message later =>
          have isNext : lConfig.registerWait thread =
              { lConfig with inputQueues := lConfig.inputQueues.set chan later }.wakeThread
                thread.core.tid (.received message) := by
            simp [L.Config.registerWait, timerCase, chanCase, inputCase]
          have hasMessage : run.pendingMessages (Chan.external chan) =
              DexVal.value message :: later.map DexVal.value := by
            rw [related.inputQueues, inputCase]
            rfl
          rw [isNext]
          exact wp3a_simulated_consumed context (Chan.external chan) (later.map DexVal.value) _
            (by
              rw [isWait]
              exact wp3a_registerWait_any_first _ _ _ foundPrepared [seconds] _ _ _ _ _
                hasMessage)
            (.received message)
            (by simp [decodeOutcome, timerCase, decodeTimedOutcome, chanCase, decodeMessage])
            _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
            (fun _ isPopped sameOthers => wp3a_channels_external related areBooted _ _ chan later
              rfl rfl rfl rfl isPopped sameOthers)
        | nil =>
          have isInputEmpty : run.pendingMessages (Chan.external chan) = [] := by
            rw [related.inputQueues, inputCase]
            rfl
          by_cases isCanceled : lConfig.cancelFlags.contains thread.core.tid = true
          · have isNext : lConfig.registerWait thread =
                lConfig.wakeThread thread.core.tid .canceled := by
              simp only [L.Config.registerWait, timerCase, chanCase,
                inputCase, isCanceled, ↓reduceIte]
            obtain ⟨message, remaining, hasMessage⟩ := cancelPending isCanceled
            rw [isNext]
            exact wp3a_simulated_consumed context (Chan.cancelRequest thread.core.tid) remaining
              _
              (by
                rw [isWait]
                exact wp3a_registerWait_any_second _ _ _ foundPrepared [seconds] _ _ _ message
                  remaining isInputEmpty hasMessage)
              .canceled (by simp [decodeOutcome, timerCase, decodeTimedOutcome, chanCase])
              _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
              (fun _ _ sameOthers => wp3a_channels_cancel related areBooted _ rfl rfl rfl rfl
                message remaining hasMessage sameOthers)
          · have isNext : lConfig.registerWait thread =
                lConfig.blockThread thread.core.tid
                  (.timed number (lConfig.clock + seconds * 1000000) (some chan)) := by
              simp only [L.Config.registerWait, timerCase, chanCase, inputCase,
                isCanceled, Bool.false_eq_true, ↓reduceIte]
            rw [isNext]
            exact wp3a_simulated_blocked context (by rw [isWait]; exact Or.inr (Or.inl rfl))
              (by rw [isWait]; simp)
              (by
                rw [isWait]
                intro named isNamed
                simp only [List.mem_cons, List.not_mem_nil, or_false] at isNamed
                rcases isNamed with isNamed | isNamed <;> rw [isNamed]
                · exact isInputEmpty
                · exact cancelQuiet isCanceled)
              _ (by rw [isWait]; rfl) (by rw [isWait]; rfl) (by rw [isWait]; rfl)
              (Or.inr ⟨number, seconds, some chan, timerCase, by rw [isWait], rfl⟩)
  | none =>
    cases requestCase : waitRequest thread.core thread.code with
    | none =>
      have isNext : lConfig.registerWait thread = lConfig.wakeThread thread.core.tid .invalid := by
        simp [L.Config.registerWait, timerCase, requestCase]
      have isWait : wp3a_wait thread = Dex.Wait.empty signature := by
        simp [wp3a_wait, timerCase, requestCase, encodeWaitRequest]
      rw [isNext]
      exact wp3a_simulated_empty context (by rw [isWait]; rfl) .invalid
        (by
          rw [wp3a_decodeOutcome_blocked thread timerCase]
          exact wp3a_decode_invalid _ _)
    | some request =>
      cases request with
      | lock mutex =>
        have isWait : wp3a_wait thread =
            { kind := .all, timerSeconds := [], channels := [Chan.mutexToken mutex] } := by
          simp [wp3a_wait, timerCase, requestCase, encodeWaitRequest]
        have hasTokens := related.mutexTokens areBooted mutex
        have blocks : lConfig.mutexOwners.get? mutex ≠ some none →
            lConfig.registerWait thread = lConfig.blockThread thread.core.tid (.lock mutex) →
            IsSimulatedByActions program lConfig dexConfig (.thread thread.core.tid) []
              (lConfig.registerWait thread).stamp := by
          intro isHeld isNext
          simp only [isHeld, ↓reduceIte] at hasTokens
          rw [isNext]
          exact wp3a_simulated_blocked context
            (by rw [isWait]; exact Or.inr (Or.inr ⟨rfl, rfl, _, rfl⟩))
            (by rw [isWait]; simp)
            (by
              rw [isWait]
              intro chan isNamed
              simp only [List.mem_singleton] at isNamed
              rw [isNamed]
              exact List.eq_nil_of_length_eq_zero hasTokens)
            _ (by rw [isWait]; rfl) (by rw [isWait]; rfl) (by rw [isWait]; rfl)
            (Or.inl ⟨by rw [isWait], by intro _ _ _ isTimed; cases isTimed⟩)
        cases ownerCase : lConfig.mutexOwners.get? mutex with
        | none =>
          exact blocks (by rw [ownerCase]; simp)
            (by simp [L.Config.registerWait, timerCase, requestCase, ownerCase])
        | some owner =>
          cases owner with
          | some holder =>
            exact blocks (by rw [ownerCase]; simp)
              (by simp [L.Config.registerWait, timerCase, requestCase, ownerCase])
          | none =>
            have isNext : lConfig.registerWait thread =
                { lConfig with
                  mutexOwners := lConfig.mutexOwners.set mutex (some thread.core.tid)
                }.wakeThread thread.core.tid .granted := by
              simp [L.Config.registerWait, timerCase, requestCase, ownerCase]
            rw [ownerCase] at hasTokens
            simp only [↓reduceIte] at hasTokens
            obtain ⟨message, hasMessage⟩ := List.length_eq_one_iff.mp hasTokens
            rw [isNext]
            exact wp3a_simulated_consumed context (Chan.mutexToken mutex) [] _
              (by
                rw [isWait]
                exact wp3a_registerWait_all_single _ _ _ foundPrepared _ _ message [] hasMessage)
              .granted
              (by simp [decodeOutcome, timerCase, wp3a_decode_lock _ _ _ requestCase])
              _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
              (fun _ isPopped sameOthers => wp3a_channels_mutex related areBooted _ _ mutex
                rfl rfl rfl rfl isPopped sameOthers)
      | recv chan =>
        have isWait : wp3a_wait thread =
            { kind := .any, timerSeconds := []
              channels := [Chan.external chan, Chan.cancelRequest thread.core.tid] } := by
          simp [wp3a_wait, timerCase, requestCase, encodeWaitRequest]
        cases inputCase : lConfig.pendingInput chan with
        | cons message later =>
          have isNext : lConfig.registerWait thread =
              { lConfig with inputQueues := lConfig.inputQueues.set chan later }.wakeThread
                thread.core.tid (.received message) := by
            simp [L.Config.registerWait, timerCase, requestCase, inputCase]
          have hasMessage : run.pendingMessages (Chan.external chan) =
              DexVal.value message :: later.map DexVal.value := by
            rw [related.inputQueues, inputCase]
            rfl
          rw [isNext]
          exact wp3a_simulated_consumed context (Chan.external chan) (later.map DexVal.value) _
            (by
              rw [isWait]
              exact wp3a_registerWait_any_first _ _ _ foundPrepared [] _ _ _ _ _ hasMessage)
            (.received message)
            (by simp [decodeOutcome, timerCase, (wp3a_decode_recv _ _ _ requestCase _ _).1,
              decodeMessage])
            _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
            (fun _ isPopped sameOthers => wp3a_channels_external related areBooted _ _ chan later
              rfl rfl rfl rfl isPopped sameOthers)
        | nil =>
          have isInputEmpty : run.pendingMessages (Chan.external chan) = [] := by
            rw [related.inputQueues, inputCase]
            rfl
          by_cases isCanceled : lConfig.cancelFlags.contains thread.core.tid = true
          · have isNext : lConfig.registerWait thread =
                lConfig.wakeThread thread.core.tid .canceled := by
              simp only [L.Config.registerWait, timerCase, requestCase,
                inputCase, isCanceled, ↓reduceIte]
            obtain ⟨message, remaining, hasMessage⟩ := cancelPending isCanceled
            rw [isNext]
            exact wp3a_simulated_consumed context (Chan.cancelRequest thread.core.tid) remaining
              _
              (by
                rw [isWait]
                exact wp3a_registerWait_any_second _ _ _ foundPrepared [] _ _ _ message
                  remaining isInputEmpty hasMessage)
              .canceled
              (by simp [decodeOutcome, timerCase, (wp3a_decode_recv _ _ _ requestCase _ _).2])
              _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
              (fun _ _ sameOthers => wp3a_channels_cancel related areBooted _ rfl rfl rfl rfl
                message remaining hasMessage sameOthers)
          · have isNext : lConfig.registerWait thread =
                lConfig.blockThread thread.core.tid (.recv chan) := by
              simp only [L.Config.registerWait, timerCase, requestCase, inputCase,
                isCanceled, Bool.false_eq_true, ↓reduceIte]
            rw [isNext]
            exact wp3a_simulated_blocked context (by rw [isWait]; exact Or.inr (Or.inl rfl))
              (by rw [isWait]; simp)
              (by
                rw [isWait]
                intro named isNamed
                simp only [List.mem_cons, List.not_mem_nil, or_false] at isNamed
                rcases isNamed with isNamed | isNamed <;> rw [isNamed]
                · exact isInputEmpty
                · exact cancelQuiet isCanceled)
              _ (by rw [isWait]; rfl) (by rw [isWait]; rfl) (by rw [isWait]; rfl)
              (Or.inl ⟨by rw [isWait], by intro _ _ _ isTimed; cases isTimed⟩)
      | join target =>
        have isWait : wp3a_wait thread =
            { kind := .any, timerSeconds := []
              channels := [Chan.joinDone target, Chan.cancelRequest thread.core.tid] } := by
          simp [wp3a_wait, timerCase, requestCase, encodeWaitRequest]
        have hasTokens := related.joinTokens target
        by_cases isJoinable : lConfig.joinable.contains target = true
        · have isNext : lConfig.registerWait thread =
              { lConfig with joinable := lConfig.joinable.erase target }.wakeThread
                thread.core.tid .granted := by
            simp only [L.Config.registerWait, timerCase, requestCase, isJoinable, ↓reduceIte]
          simp only [isJoinable, ↓reduceIte] at hasTokens
          obtain ⟨message, hasMessage⟩ := List.length_eq_one_iff.mp hasTokens
          rw [isNext]
          exact wp3a_simulated_consumed context (Chan.joinDone target) [] _
            (by
              rw [isWait]
              exact wp3a_registerWait_any_first _ _ _ foundPrepared [] _ _ _ message [] hasMessage)
            .granted
            (by simp [decodeOutcome, timerCase, (wp3a_decode_join _ _ _ requestCase _ _).1])
            _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
            (fun _ isPopped sameOthers => wp3a_channels_join related areBooted
              lInvariant.joinableNodup _ _ target rfl rfl rfl rfl isPopped sameOthers)
        · simp only [isJoinable, Bool.false_eq_true, ↓reduceIte] at hasTokens
          have isJoinEmpty : run.pendingMessages (Chan.joinDone target) = [] :=
            List.eq_nil_of_length_eq_zero hasTokens
          by_cases isCanceled : lConfig.cancelFlags.contains thread.core.tid = true
          · have isNext : lConfig.registerWait thread =
                lConfig.wakeThread thread.core.tid .canceled := by
              simp only [L.Config.registerWait, timerCase, requestCase, isJoinable,
                isCanceled, Bool.false_eq_true, ↓reduceIte]
            obtain ⟨message, remaining, hasMessage⟩ := cancelPending isCanceled
            rw [isNext]
            exact wp3a_simulated_consumed context (Chan.cancelRequest thread.core.tid) remaining
              _
              (by
                rw [isWait]
                exact wp3a_registerWait_any_second _ _ _ foundPrepared [] _ _ _ message
                  remaining isJoinEmpty hasMessage)
              .canceled
              (by simp [decodeOutcome, timerCase, (wp3a_decode_join _ _ _ requestCase _ _).2])
              _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
              (fun _ _ sameOthers => wp3a_channels_cancel related areBooted _ rfl rfl rfl rfl
                message remaining hasMessage sameOthers)
          · have isNext : lConfig.registerWait thread =
                lConfig.blockThread thread.core.tid (.join target) := by
              simp only [L.Config.registerWait, timerCase, requestCase, isJoinable,
                isCanceled, Bool.false_eq_true, ↓reduceIte]
            rw [isNext]
            exact wp3a_simulated_blocked context (by rw [isWait]; exact Or.inr (Or.inl rfl))
              (by rw [isWait]; simp)
              (by
                rw [isWait]
                intro named isNamed
                simp only [List.mem_cons, List.not_mem_nil, or_false] at isNamed
                rcases isNamed with isNamed | isNamed <;> rw [isNamed]
                · exact isJoinEmpty
                · exact cancelQuiet isCanceled)
              _ (by rw [isWait]; rfl) (by rw [isWait]; rfl) (by rw [isWait]; rfl)
              (Or.inl ⟨by rw [isWait], by intro _ _ _ isTimed; cases isTimed⟩)
      | park cv =>
        have isWait : wp3a_wait thread =
            { kind := .all, timerSeconds := []
              channels := [Chan.cvWake cv thread.core.tid] } := by
          simp [wp3a_wait, timerCase, requestCase, encodeWaitRequest]
        have hasWakes := related.cvWakes cv thread.core.tid
        by_cases hasWake : lConfig.cvWakes.contains (cv, thread.core.tid) = true
        · have isNext : lConfig.registerWait thread =
              { lConfig with cvWakes := lConfig.cvWakes.erase (cv, thread.core.tid) }.wakeThread
                thread.core.tid .granted := by
            simp only [L.Config.registerWait, timerCase, requestCase, hasWake, ↓reduceIte]
          have isCounted : 0 < lConfig.cvWakes.count (cv, thread.core.tid) :=
            List.count_pos_iff.mpr (by simpa using hasWake)
          cases pendingCase : run.pendingMessages (Chan.cvWake cv thread.core.tid) with
          | nil =>
            rw [pendingCase] at hasWakes
            simp only [List.length_nil] at hasWakes
            omega
          | cons message remaining =>
            rw [isNext]
            exact wp3a_simulated_consumed context (Chan.cvWake cv thread.core.tid) remaining _
              (by
                rw [isWait]
                exact wp3a_registerWait_all_single _ _ _ foundPrepared _ _ message remaining
                  pendingCase)
              .granted
              (by simp [decodeOutcome, timerCase, wp3a_decode_park _ _ _ requestCase])
              _ ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
              (fun _ isPopped sameOthers => wp3a_channels_cvWake related areBooted _ _ cv
                rfl rfl rfl rfl message remaining pendingCase isPopped sameOthers)
        · have isNext : lConfig.registerWait thread =
              lConfig.blockThread thread.core.tid (.park cv) := by
            simp only [L.Config.registerWait, timerCase, requestCase,
              hasWake, Bool.false_eq_true, ↓reduceIte]
          have isNotCounted : lConfig.cvWakes.count (cv, thread.core.tid) = 0 :=
            List.count_eq_zero.mpr (by simpa using hasWake)
          rw [isNotCounted] at hasWakes
          rw [isNext]
          exact wp3a_simulated_blocked context
            (by rw [isWait]; exact Or.inr (Or.inr ⟨rfl, rfl, _, rfl⟩))
            (by rw [isWait]; simp)
            (by
              rw [isWait]
              intro chan isNamed
              simp only [List.mem_singleton] at isNamed
              rw [isNamed]
              exact List.eq_nil_of_length_eq_zero hasWakes)
            _ (by rw [isWait]; rfl) (by rw [isWait]; rfl) (by rw [isWait]; rfl)
            (Or.inl ⟨by rw [isWait], by intro _ _ _ isTimed; cases isTimed⟩)

end DexExpressiveness
