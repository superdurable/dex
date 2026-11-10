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

end DexExpressiveness
