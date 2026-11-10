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

/-- A run followed by one more step. -/
theorem wp3a_exec_snoc {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State)
    (labels : List Label) (label : Label) (events lastEvents : List Event)
    (middle final : State)
    (isFirst : system.exec state labels = some (events, middle))
    (isLast : system.step middle label = some (lastEvents, final)) :
    system.exec state (labels ++ [label]) = some (events ++ lastEvents, final) := by
  induction labels generalizing state events with
  | nil =>
    simp only [TransitionSystem.exec] at isFirst
    cases isFirst
    simp [TransitionSystem.exec, isLast]
  | cons head laterLabels inductionHypothesis =>
    simp only [TransitionSystem.exec, List.cons_append] at isFirst ⊢
    split at isFirst
    · cases isFirst
    · rename_i stepEvents next isStep
      split at isFirst
      · cases isFirst
      · rename_i laterEvents reached isLater
        cases isFirst
        rw [inductionHypothesis next laterEvents isLater]
        simp

/-- The task after its handler returned. -/
def wp3a_returnTask (program : Program) (task : Dex.Task signature)
    (current : Dex.Task signature) : Dex.Task signature :=
  { current with phase := .returned (task.compute ⟦program⟧).2 }

/-- A task that holds no effect returns by `returnTaskActions`, silently. -/
theorem wp3a_exec_returnTask (program : Program) (config : Dex.Config signature)
    (tasksDistinct : TasksAreDistinct config) (task : Dex.Task signature)
    (isMember : task ∈ config.tasks) (isComputed : IsTaskComputed program task)
    (noEffects : pendingEffects ⟦program⟧ task = []) :
    (Dex.system ⟦program⟧).exec config (returnTaskActions task) =
      some ([], config.updateTask task.id (wp3a_returnTask program task)) := by
  have isFound := wp2_findTask?_of_mem tasksDistinct isMember
  unfold pendingEffects at noEffects
  unfold IsTaskComputed at isComputed
  cases phaseCase : task.phase with
  | started =>
    simp only [phaseCase] at noEffects
    let ranTask : Dex.Task signature → Dex.Task signature := fun current =>
      { current with phase := .running (task.compute ⟦program⟧).1 (task.compute ⟦program⟧).2 }
    have isFoundNext : (config.updateTask task.id ranTask).findTask? task.id =
        some { task with phase := .running [] (task.compute ⟦program⟧).2 } := by
      rw [wp2_findTask?_updateTask config task.id task.id ranTask (fun _ => rfl), isFound]
      simp [ranTask, noEffects]
    have isReturned : (config.updateTask task.id ranTask).updateTask task.id
          (wp3a_returnTask program task) =
        config.updateTask task.id (wp3a_returnTask program task) := by
      rw [wp2_updateTask_updateTask config task.id ranTask _ (fun _ => rfl)]
      rfl
    simp only [ranTask] at isFoundNext isReturned
    simp only [returnTaskActions, phaseCase, TransitionSystem.exec, Dex.system, Dex.step,
      isFound, isFoundNext, List.append_nil]
    exact congrArg (fun next => some ([], next)) isReturned
  | running remaining response =>
    simp only [phaseCase] at noEffects isComputed
    subst noEffects
    simp only [returnTaskActions, phaseCase, TransitionSystem.exec, Dex.system, Dex.step,
      isFound, List.append_nil, isComputed.1]
    rfl
  | returned response =>
    simp only [phaseCase] at isComputed
    simp only [returnTaskActions, phaseCase, TransitionSystem.exec]
    have isSame : config.updateTask task.id (wp3a_returnTask program task) = config := by
      have sameTasks : config.tasks.map (fun candidate =>
          if candidate.id = task.id then wp3a_returnTask program task candidate else candidate) =
          config.tasks := by
        conv => rhs; rw [← List.map_id config.tasks]
        apply List.map_congr_left
        intro candidate isCandidate
        by_cases isChanged : candidate.id = task.id
        · have isTask : candidate = task :=
            eq_of_nodup_map (fun current => current.id) config.tasks tasksDistinct.1 isCandidate
              isMember isChanged
          subst isTask
          simp only [↓reduceIte, wp3a_returnTask, id, ← isComputed, ← phaseCase]
        · simp [isChanged]
      simp only [Dex.Config.updateTask, sameTasks]
    rw [isSame]

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
  have isFound := wp2_findTask?_of_mem tasksDistinct isMember
  have isReturned := wp3a_exec_returnTask program config tasksDistinct task isMember isComputed
    noEffects
  have isFoundNext : (config.updateTask task.id (wp3a_returnTask program task)).findTask? task.id =
      some { task with phase := .returned (.waitFor response) } := by
    rw [wp2_findTask?_updateTask config task.id task.id (wp3a_returnTask program task)
      (fun _ => rfl), isFound]
    simp [wp3a_returnTask, isResponse]
  have sameRemoved : (config.updateTask task.id (wp3a_returnTask program task)).removeTask task.id =
      config.removeTask task.id := by
    have sameFilter : List.filter ((fun candidate => !decide (candidate.id = task.id)) ∘
        fun candidate => if candidate.id = task.id then wp3a_returnTask program task candidate
          else candidate) config.tasks =
        List.filter (fun candidate => !decide (candidate.id = task.id)) config.tasks := by
      apply List.filter_congr
      intro candidate _
      by_cases isChanged : candidate.id = task.id <;> simp [isChanged, wp3a_returnTask]
    have isIdentity : ∀ candidate,
        candidate ∈ List.filter (fun candidate => !decide (candidate.id = task.id)) config.tasks →
        (if candidate.id = task.id then wp3a_returnTask program task candidate else candidate) =
          id candidate := by
      intro candidate isCandidate
      have isOther := (List.mem_filter.mp isCandidate).2
      simp only [Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not] at isOther
      simp [isOther]
    simp only [Dex.Config.removeTask, Dex.Config.updateTask, List.filter_map]
    congr 1
    rw [sameFilter, List.map_congr_left isIdentity, List.map_id]
  have isCurrentNext : (config.updateTask task.id (wp3a_returnTask program task)).isTaskCurrent
      { task with phase := .returned (.waitFor response) } = true := isCurrent
  have canCommitNext : (config.updateTask task.id (wp3a_returnTask program task)).canCommit =
      true := canCommit
  have hasRunNext : (config.updateTask task.id (wp3a_returnTask program task)).run = some run :=
    hasRun
  have isLast : (Dex.system ⟦program⟧).step
      (config.updateTask task.id (wp3a_returnTask program task)) (.stepComplete task.id) =
      some (Dex.Config.closeEvents run (wp3a_completedRun run task.step response config.clock),
        wp3a_completedConfig config task
          (wp3a_completedRun run task.step response config.clock)) := by
    simp only [isWaitFor] at isCurrentNext
    simp only [Dex.system, Dex.step, hasRunNext, isFoundNext, isWaitFor, isCurrentNext,
      canCommitNext, Bool.and_self, ↓reduceIte, sameRemoved]
    rfl
  have isExec := wp3a_exec_snoc (Dex.system ⟦program⟧) config _ _ _ _ _ _ isReturned isLast
  simpa using isExec

/-! ## The three results of `register(e, W)` -/

/-- A response with no effect wakes no Step. -/
theorem wp3a_applyEffects_empty (run : Dex.Run signature) : run.applyEffects {} = run := by
  show ((run.applyWrites []).applyPublications []).wake [] = run
  simp only [Dex.Run.applyWrites, Dex.Run.applyPublications, List.foldl_nil]
  exact wp2_wake_of_no_waiter run [] (fun _ _ _ _ _ _ _ => by simp)

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
  rw [wp3a_applyEffects_empty]
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
  simp only [Dex.Run.registerWait, hasCondition, Bool.false_eq_true, ↓reduceIte]
  exact Dex.Run.evalStep_of_plan_none (wp3a_storedRun run step wait now) step _ _
    (wp3a_findStep?_storedRun run step record isFound wait now) rfl
    (wp2_plan_none_of_quiet wait _ _ isSupported hasChannel (by simp)
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
  simp only [Dex.Run.registerWait, hasCondition, Bool.false_eq_true, ↓reduceIte]
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
  rw [wp2_stepCarriesThread_iff] at carries
  simp only [isWaitStarted, wp2_isBootPhase, Bool.false_eq_true, ↓reduceIte] at carries
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
    (newPhase : Phase) (isNotBoot : wp2_isBootPhase newPhase = false)
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
  obtain ⟨isMember, hasTid⟩ := wp2_mem_of_findThread? isFound
  have memNew : ∀ candidate, candidate ∈ lNext.threads ↔
      (candidate ∈ lConfig.threads ∧ candidate.core.tid ≠ tid) ∨
      candidate = { thread with phase := newPhase } := by
    intro candidate
    rw [sameThreads]
    exact wp2_mem_setPhase lInvariant isFound newPhase candidate
  have sameBoot : wp2_isBootPhase newPhase = wp2_isBootPhase thread.phase := by
    rw [isNotBoot, isWaitStarted]
    rfl
  have findNew : ∀ other, lNext.findThread? other =
      (lConfig.findThread? other).map (fun current =>
        if current.core.tid = tid then { current with phase := newPhase } else current) := by
    intro other
    rw [← wp2_findThread?_setPhase]
    show lNext.threads.find? _ = (lConfig.setPhase tid newPhase).threads.find? _
    rw [sameThreads]
  -- No thread boots: a booting `main` is the only thread.
  have noBoot : ∀ other, other ∈ lConfig.threads → wp2_isBootPhase other.phase = false := by
    intro other isOther
    cases bootCase : wp2_isBootPhase other.phase with
    | false => rfl
    | true =>
      have isBootPhase : other.phase = .bootPending ∨ other.phase = .bootRunning := by
        cases phaseCase : other.phase <;> simp [phaseCase, wp2_isBootPhase] at bootCase ⊢
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
    have otherInput := wp2_threadOfInput_of_mem lInvariant isOther otherCarries
    have threadInput := wp2_threadOfInput_of_mem lInvariant isMember carries
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
    exact ⟨wp2_stepCarriesThread_congr sameId sameInput sameOptions,
      wp2_stepCarriesThread_congr sameId.symm sameInput.symm sameOptions.symm⟩
  -- The active Step that carries the changed thread is `record`.
  have recordOfNew : ∀ candidate, candidate ∈ run.steps → candidate.phase.isActive = true →
      StepCarriesThread program { thread with phase := newPhase }
        (wp3a_changeStep record.id change candidate) → candidate = record := by
    intro candidate isCandidate candidateActive candidateCarries
    have oldCarries : StepCarriesThread program thread candidate := by
      have newCarries := (carriesImage _ candidate isCandidate).mpr candidateCarries
      rw [wp2_stepCarriesThread_iff] at newCarries ⊢
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
        have isBoot : wp2_isBootPhase other.phase = true := by
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
          · have currentTid := (wp2_mem_of_findThread? isCurrent).2
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
          · exact wp2_phaseMatchesStep_transfer sameQueues
              (fun task isTask isTaskStep =>
                keepsTasks task isTask (by rw [isTaskStep]; exact isOtherId))
              otherMatch
        · rw [isNew]
          refine ⟨change record, ?_, changeKeeps.2.2.2, ?_, ?_⟩
          · have isImage := imageMem record isRecord
            rw [imageRecord] at isImage
            exact isImage
          · exact wp2_stepCarriesThread_congr changeKeeps.1 changeKeeps.2.1 changeKeeps.2.2.1
              (wp2_stepCarriesThread_setPhase newPhase sameBoot carries)
          · exact wp2_phaseMatchesStep_transfer sameQueues (fun _ isTask _ => isTask) newMatch
      stepsHaveThreads := fun changed isChanged changedActive => by
        rw [sameSteps] at isChanged
        obtain ⟨candidate, isCandidate, isImage⟩ := List.mem_map.mp isChanged
        rw [← isImage] at changedActive ⊢
        rw [(imageKeeps candidate isCandidate).2.2.2] at changedActive
        obtain ⟨owner, isOwner, ownerNotHalted, ownerCarries⟩ :=
          related.stepsHaveThreads candidate isCandidate changedActive
        by_cases isChangedThread : owner.core.tid = tid
        · have isThread : owner = thread :=
            wp2_thread_eq_of_tid lInvariant isOwner isMember (isChangedThread.trans hasTid.symm)
          rw [isThread] at ownerCarries
          exact ⟨_, (memNew _).mpr (Or.inr rfl), isNotHalted,
            (carriesImage _ candidate isCandidate).mp
              (wp2_stepCarriesThread_setPhase newPhase sameBoot ownerCarries)⟩
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

end DexExpressiveness
