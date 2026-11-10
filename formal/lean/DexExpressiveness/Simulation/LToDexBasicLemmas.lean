import DexExpressiveness.Simulation.Relation
import DexExpressiveness.Properties

/-!
# Helper lemmas of work package 2 (L to Dex, basic steps)

Clock facts, lookup facts, and the frame lemmas that `Simulation/LToDexBasic.lean`
uses: what stays true of `RunningRelated` when one Step record, one task, or
one thread's phase changes. Every lemma is prefixed `wp2_`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Clocks and status -/

/-- A stamped L action may happen exactly when a Dex commit may. -/
theorem wp2_canCommit_of_canStamp {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig)
    (canStamp : lConfig.canStamp = true) : dexConfig.canCommit = true := by
  unfold L.Config.canStamp at canStamp
  unfold Dex.Config.canCommit
  rw [← isRelated.stamp, ← isRelated.clock]
  exact canStamp

/-- A configuration with a thread is running, so the Flow runs. -/
theorem wp2_run_of_thread {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig)
    (thread : L.Thread) (isMember : thread ∈ lConfig.threads) :
    lConfig.status = .running ∧ ∃ run, dexConfig.run = some run ∧ run.status = .running := by
  have isStatus := state.isRelated.status
  cases statusCase : lConfig.status with
  | notStarted =>
    have noThread := (state.lInvariant.notStartedIsInitial statusCase).1
    rw [noThread] at isMember
    cases isMember
  | closed =>
    have noThread := state.lInvariant.closedHasNoThread statusCase
    rw [noThread] at isMember
    cases isMember
  | running =>
    cases runCase : dexConfig.run with
    | none => simp [statusCase, runCase] at isStatus
    | some run =>
      simp only [statusCase, runCase] at isStatus
      exact ⟨rfl, run, rfl, isStatus⟩

/-- Stamping an L configuration changes nothing that `RunningRelated` reads. -/
theorem wp2_runningRelated_stamp {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run) :
    RunningRelated program lConfig.stamp dexConfig run :=
  { shared := related.shared
    cvQueues := related.cvQueues
    inputQueues := related.inputQueues
    mutexTokens := related.mutexTokens
    joinTokens := related.joinTokens
    cancelRequestsAreFlags := related.cancelRequestsAreFlags
    cancelFlagsAreRequests := related.cancelFlagsAreRequests
    cvWakes := related.cvWakes
    timers := related.timers
    timerCounter := related.timerCounter
    outputsEmpty := related.outputsEmpty
    threadsHaveSteps := related.threadsHaveSteps
    stepsHaveThreads := related.stepsHaveThreads
    activeStepsHaveDistinctThreads := related.activeStepsHaveDistinctThreads
    invocationsAreActive := related.invocationsAreActive
    blockingOrder := related.blockingOrder
    tasksAreCurrent := related.tasksAreCurrent }

/-! ## Threads -/

/-- What the thread lookup finds is a thread of the configuration. -/
theorem wp2_mem_of_findThread? {config : L.Config} {tid : Tid} {thread : L.Thread}
    (isFound : config.findThread? tid = some thread) :
    thread ∈ config.threads ∧ thread.core.tid = tid :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- Two threads of an invariant configuration with the same ID are the same. -/
theorem wp2_thread_eq_of_tid {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {first second : L.Thread}
    (isFirst : first ∈ config.threads) (isSecond : second ∈ config.threads)
    (sameTid : first.core.tid = second.core.tid) : first = second :=
  eq_of_nodup_map (fun thread => thread.core.tid) config.threads invariant.threadIdsNodup
    isFirst isSecond sameTid

/-- Setting the phase of one thread: the lookup of any thread. -/
theorem wp2_findThread?_setPhase (config : L.Config) (tid other : Tid) (phase : Phase) :
    (config.setPhase tid phase).findThread? other =
      (config.findThread? other).map (fun current =>
        if current.core.tid = tid then { current with phase := phase } else current) := by
  simp only [L.Config.findThread?, L.Config.setPhase, L.Config.updateThread, List.find?_map]
  congr 1
  apply congrArg (fun predicate => config.threads.find? predicate)
  funext candidate
  by_cases isChanged : candidate.core.tid = tid <;> simp [isChanged]

/-- The threads after setting the phase of a found thread: the other threads,
and the found thread with its new phase. -/
theorem wp2_mem_setPhase {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {tid : Tid} {thread : L.Thread}
    (isFound : config.findThread? tid = some thread) (phase : Phase) (candidate : L.Thread) :
    candidate ∈ (config.setPhase tid phase).threads ↔
      (candidate ∈ config.threads ∧ candidate.core.tid ≠ tid) ∨
      candidate = { thread with phase := phase } := by
  obtain ⟨isMember, hasTid⟩ := wp2_mem_of_findThread? isFound
  simp only [L.Config.setPhase, L.Config.updateThread, List.mem_map]
  constructor
  · rintro ⟨original, isOriginal, isCandidate⟩
    by_cases isChanged : original.core.tid = tid
    · have isThread : original = thread :=
        wp2_thread_eq_of_tid invariant isOriginal isMember (isChanged.trans hasTid.symm)
      subst isThread
      simp only [isChanged, ↓reduceIte] at isCandidate
      exact Or.inr isCandidate.symm
    · simp only [isChanged, ↓reduceIte] at isCandidate
      subst isCandidate
      exact Or.inl ⟨isOriginal, isChanged⟩
  · rintro (⟨isOld, isOther⟩ | isNew)
    · exact ⟨candidate, isOld, by simp [isOther]⟩
    · exact ⟨thread, isMember, by simp [hasTid, isNew]⟩

/-! ## Which Step carries a thread -/

/-- `main` before its first statement: the phases whose Step is the start Step. -/
def wp2_isBootPhase : Phase → Bool
  | .bootPending => true
  | .bootRunning => true
  | _ => false

/-- A phase is a boot phase exactly when it is `bootPending` or `bootRunning`. -/
theorem wp2_isBootPhase_eq_false (phase : Phase) :
    wp2_isBootPhase phase = false ↔ phase ≠ .bootPending ∧ phase ≠ .bootRunning := by
  cases phase <;> simp [wp2_isBootPhase]

/-- `StepCarriesThread` by cases on "boots or not" and on the timer. -/
theorem wp2_stepCarriesThread_iff (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) :
    StepCarriesThread program thread record ↔
      if wp2_isBootPhase thread.phase then
        record.id.stepType = StepType.start ∧
        ∃ input, record.input = some (DexVal.value input) ∧
          thread.core.locals = [(program.mainParam, input)]
      else
        match thread.timer with
        | some (number, seconds) =>
          record.id = { stepType := StepType.timed, number := number } ∧
          record.input = some (DexVal.timedThread thread.core thread.code seconds)
        | none =>
          record.id.stepType = StepType.point thread.code ∧
          record.input = some (DexVal.thread thread.core) ∧
          record.options.executeLoadMaps = executeLoadMaps thread.core thread.code := by
  obtain ⟨core, code, timer, phase⟩ := thread
  cases phase <;> rcases timer with _ | ⟨number, seconds⟩ <;>
    simp [StepCarriesThread, wp2_isBootPhase]

/-- `StepCarriesThread` reads only the ID, the input, and the options of the Step. -/
theorem wp2_stepCarriesThread_congr {program : Program} {thread : L.Thread}
    {record changed : Dex.StepRecord signature} (sameId : changed.id = record.id)
    (sameInput : changed.input = record.input) (sameOptions : changed.options = record.options)
    (carries : StepCarriesThread program thread record) :
    StepCarriesThread program thread changed := by
  rw [wp2_stepCarriesThread_iff] at carries ⊢
  rw [sameId, sameInput, sameOptions]
  exact carries

/-- A phase change that does not cross the end of the boot keeps the Step. -/
theorem wp2_stepCarriesThread_setPhase {program : Program} {thread : L.Thread}
    {record : Dex.StepRecord signature} (phase : Phase)
    (sameBoot : wp2_isBootPhase phase = wp2_isBootPhase thread.phase)
    (carries : StepCarriesThread program thread record) :
    StepCarriesThread program { thread with phase := phase } record := by
  rw [wp2_stepCarriesThread_iff] at carries ⊢
  simp only [sameBoot]
  exact carries

/-- The Step that carries a thread has the thread's ID in its input. `main`
boots with the empty ID. -/
theorem wp2_threadOfInput_of_carries {program : Program} {thread : L.Thread}
    {record : Dex.StepRecord signature} (carries : StepCarriesThread program thread record)
    (bootIsMain : wp2_isBootPhase thread.phase = true → thread.core.tid = []) :
    threadOfInput record.input = thread.core.tid := by
  rw [wp2_stepCarriesThread_iff] at carries
  cases bootCase : wp2_isBootPhase thread.phase with
  | true =>
    simp only [bootCase, ↓reduceIte] at carries
    obtain ⟨_, input, hasInput, _⟩ := carries
    rw [hasInput, bootIsMain bootCase]
    rfl
  | false =>
    simp only [bootCase] at carries
    cases timerCase : thread.timer with
    | none =>
      simp only [timerCase] at carries
      rw [carries.2.1]
      rfl
    | some timer =>
      obtain ⟨number, seconds⟩ := timer
      simp only [timerCase] at carries
      rw [carries.2]
      rfl

/-- In an invariant configuration the Step that carries a thread has the
thread's ID in its input. -/
theorem wp2_threadOfInput_of_mem {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {thread : L.Thread}
    {record : Dex.StepRecord signature} (isMember : thread ∈ config.threads)
    (carries : StepCarriesThread program thread record) :
    threadOfInput record.input = thread.core.tid := by
  apply wp2_threadOfInput_of_carries carries
  intro isBoot
  have isBootPhase : thread.phase = .bootPending ∨ thread.phase = .bootRunning := by
    cases phaseCase : thread.phase <;> simp [phaseCase, wp2_isBootPhase] at isBoot ⊢
  obtain ⟨_, _, _, input, isCore⟩ := invariant.bootIsInitial thread isMember isBootPhase
  rw [isCore]

/-! ## Tasks -/

/-- A running task that the next configuration keeps still runs the Step. -/
theorem wp2_hasRunningTask_transfer {config next : Dex.Config signature}
    {record : Dex.StepRecord signature} {method : Dex.Method}
    {Holds : Dex.Task signature → Prop}
    (keepsTasks : ∀ task, task ∈ config.tasks → task.step = record.id → task ∈ next.tasks)
    (hasTask : HasRunningTask config record method Holds) :
    HasRunningTask next record method Holds := by
  obtain ⟨task, isMember, rest⟩ := hasTask
  exact ⟨task, keepsTasks task isMember rest.2.1, rest⟩

/-- `PhaseMatchesStep` reads the L configuration only through the waiter
lists, and the Dex configuration only through the tasks of the Step. -/
theorem wp2_phaseMatchesStep_transfer {program : Program} {lConfig lNext : L.Config}
    {config next : Dex.Config signature} {thread : L.Thread}
    {record : Dex.StepRecord signature} (sameQueues : lNext.cvQueues = lConfig.cvQueues)
    (keepsTasks : ∀ task, task ∈ config.tasks → task.step = record.id → task ∈ next.tasks)
    (isMatch : PhaseMatchesStep program lConfig config thread record) :
    PhaseMatchesStep program lNext next thread record := by
  obtain ⟨core, code, timer, phase⟩ := thread
  cases phase <;> simp only [PhaseMatchesStep] at isMatch ⊢
  all_goals first
    | exact isMatch
    | (rw [sameQueues]; exact wp2_hasRunningTask_transfer keepsTasks isMatch)
    | exact wp2_hasRunningTask_transfer keepsTasks isMatch

/-! ## The frame of a phase change

One thread changes its phase, without blocking, unblocking, halting, or
leaving the boot. On the Dex side the Step table changes only in the
invocation and the condition results of the Step that carries the thread,
and the tasks of every other Step stay. Then `RunningRelated` holds again,
given the new match of the changed thread with its Step. -/

/-- The frame lemma of a phase change. `image` is the change of the Step
table; it is the identity outside the Step that carries the thread. -/
theorem wp2_runningRelated_setPhase {program : Program} {lConfig : L.Config}
    {dexConfig dexNext : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (lInvariant : L.Invariant program lConfig)
    (idsNodup : (run.steps.map (fun candidate => candidate.id)).Nodup)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    {record : Dex.StepRecord signature} (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true)
    (carries : StepCarriesThread program thread record)
    (newPhase : Phase)
    (sameBoot : wp2_isBootPhase newPhase = wp2_isBootPhase thread.phase)
    (wasNotBlocked : ∀ on since, thread.phase ≠ .blocked on since)
    (isNotBlocked : ∀ on since, newPhase ≠ .blocked on since)
    (isNotHalted : newPhase ≠ .halted)
    (keepsCanceled : thread.phase = .woken .canceled ∨ thread.phase = .resuming .canceled →
      newPhase = .woken .canceled ∨ newPhase = .resuming .canceled)
    (image : Dex.StepRecord signature → Dex.StepRecord signature)
    (sameSteps : newRun.steps = run.steps.map image)
    (imageKeeps : ∀ candidate, (image candidate).id = candidate.id ∧
      (image candidate).input = candidate.input ∧ (image candidate).options = candidate.options ∧
      (image candidate).phase = candidate.phase ∧
      (image candidate).waitState = candidate.waitState)
    (imageOther : ∀ candidate, candidate.id ≠ record.id → image candidate = candidate)
    (sameAttributes : newRun.attributes = run.attributes)
    (sameChannels : newRun.channels = run.channels)
    (sameTimers : newRun.timers = run.timers)
    (sameCounters : newRun.counters = run.counters)
    (sameOutputs : newRun.outputs = run.outputs)
    (keepsTasks : ∀ task, task ∈ dexConfig.tasks → task.step ≠ record.id → task ∈ dexNext.tasks)
    (newMatch : PhaseMatchesStep program lConfig dexNext { thread with phase := newPhase }
      (image record))
    (tasksAreCurrent : ∀ task, task ∈ dexNext.tasks → dexNext.isTaskCurrent task = true) :
    RunningRelated program (lConfig.setPhase tid newPhase) dexNext newRun := by
  obtain ⟨isMember, hasTid⟩ := wp2_mem_of_findThread? isFound
  have memNew := wp2_mem_setPhase lInvariant isFound newPhase
  have samePending : ∀ chan, newRun.pendingMessages chan = run.pendingMessages chan := by
    intro chan
    simp [Dex.Run.pendingMessages, sameChannels]
  -- A Step of another thread is not the Step of the changed thread.
  have otherStep : ∀ other otherRecord, other ∈ lConfig.threads → other.core.tid ≠ tid →
      otherRecord ∈ run.steps → StepCarriesThread program other otherRecord →
      otherRecord.id ≠ record.id := by
    intro other otherRecord isOther otherTid isOtherRecord otherCarries sameId
    have sameRecord : otherRecord = record :=
      eq_of_nodup_map (fun candidate => candidate.id) run.steps idsNodup isOtherRecord
        isRecord sameId
    subst sameRecord
    have otherInput := wp2_threadOfInput_of_mem lInvariant isOther otherCarries
    have threadInput := wp2_threadOfInput_of_mem lInvariant isMember carries
    exact otherTid ((otherInput.symm.trans threadInput).trans hasTid)
  have imageMem : ∀ candidate, candidate ∈ run.steps → image candidate ∈ newRun.steps := by
    intro candidate isCandidate
    rw [sameSteps]
    exact List.mem_map.mpr ⟨candidate, isCandidate, rfl⟩
  have carriesImage : ∀ (other : L.Thread) (candidate : Dex.StepRecord signature),
      StepCarriesThread program other candidate ↔
        StepCarriesThread program other (image candidate) := by
    intro other candidate
    obtain ⟨sameId, sameInput, sameOptions, _, _⟩ := imageKeeps candidate
    exact ⟨wp2_stepCarriesThread_congr sameId sameInput sameOptions,
      wp2_stepCarriesThread_congr sameId.symm sameInput.symm sameOptions.symm⟩
  exact
    { shared := fun name => by
        rw [sameAttributes]
        exact related.shared name
      cvQueues := fun cv => by
        rw [sameAttributes]
        exact related.cvQueues cv
      inputQueues := fun chan => by
        rw [samePending]
        exact related.inputQueues chan
      mutexTokens := fun areBooted mutex => by
        rw [samePending]
        apply related.mutexTokens
        intro other isOther
        by_cases isChanged : other.core.tid = tid
        · have isThread : other = thread :=
            wp2_thread_eq_of_tid lInvariant isOther isMember (isChanged.trans hasTid.symm)
          subst isThread
          have isBooted := areBooted _ ((memNew _).mpr (Or.inr rfl))
          rw [← wp2_isBootPhase_eq_false] at isBooted ⊢
          rw [← sameBoot]
          exact isBooted
        · exact areBooted other ((memNew other).mpr (Or.inl ⟨isOther, isChanged⟩))
      joinTokens := fun joined => by
        rw [samePending]
        exact related.joinTokens joined
      cancelRequestsAreFlags := fun target isPending => by
        rw [samePending] at isPending
        exact related.cancelRequestsAreFlags target isPending
      cancelFlagsAreRequests := fun target isFlagged => by
        rw [samePending, wp2_findThread?_setPhase]
        rcases related.cancelFlagsAreRequests target isFlagged with
          isPending | isGone | ⟨current, isCurrent, isCanceled⟩
        · exact Or.inl isPending
        · exact Or.inr (Or.inl (by rw [isGone]; rfl))
        · refine Or.inr (Or.inr ⟨_, by rw [isCurrent]; rfl, ?_⟩)
          by_cases isChanged : current.core.tid = tid
          · have isThread : current = thread :=
              wp2_thread_eq_of_tid lInvariant (wp2_mem_of_findThread? isCurrent).1 isMember
                (isChanged.trans hasTid.symm)
            subst isThread
            simp only [isChanged, ↓reduceIte]
            exact keepsCanceled isCanceled
          · simp only [isChanged, ↓reduceIte]
            exact isCanceled
      cvWakes := fun cv waiter => by
        rw [samePending]
        exact related.cvWakes cv waiter
      timers := fun timer fireTime => by
        rw [sameTimers, related.timers]
        constructor
        · rintro ⟨owner, chan, since, isOwner, isBlocked, rest⟩
          refine ⟨owner, chan, since, (memNew owner).mpr (Or.inl ⟨isOwner, ?_⟩), isBlocked, rest⟩
          intro isChanged
          have isThread : owner = thread :=
            wp2_thread_eq_of_tid lInvariant isOwner isMember (isChanged.trans hasTid.symm)
          subst isThread
          exact wasNotBlocked _ _ isBlocked
        · rintro ⟨owner, chan, since, isOwner, isBlocked, rest⟩
          rcases (memNew owner).mp isOwner with ⟨isOld, _⟩ | isNew
          · exact ⟨owner, chan, since, isOld, isBlocked, rest⟩
          · subst isNew
            exact (isNotBlocked _ _ isBlocked).elim
      timerCounter := by
        rw [sameCounters]
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
          · exact wp2_phaseMatchesStep_transfer rfl
              (fun task isTask isTaskStep =>
                keepsTasks task isTask (by rw [isTaskStep]; exact isOtherId))
              otherMatch
        · subst isNew
          refine ⟨image record, imageMem record isRecord, ?_, ?_, ?_⟩
          · rw [(imageKeeps record).2.2.2.1]
            exact isActive
          · exact (carriesImage _ record).mp (wp2_stepCarriesThread_setPhase newPhase sameBoot carries)
          · exact wp2_phaseMatchesStep_transfer rfl (fun _ isTask _ => isTask) newMatch
      stepsHaveThreads := fun changed isChanged changedActive => by
        rw [sameSteps] at isChanged
        obtain ⟨candidate, isCandidate, isImage⟩ := List.mem_map.mp isChanged
        subst isImage
        rw [(imageKeeps candidate).2.2.2.1] at changedActive
        obtain ⟨owner, isOwner, ownerNotHalted, ownerCarries⟩ :=
          related.stepsHaveThreads candidate isCandidate changedActive
        by_cases isChangedThread : owner.core.tid = tid
        · have isThread : owner = thread :=
            wp2_thread_eq_of_tid lInvariant isOwner isMember (isChangedThread.trans hasTid.symm)
          subst isThread
          exact ⟨_, (memNew _).mpr (Or.inr rfl), isNotHalted,
            (carriesImage _ candidate).mp
              (wp2_stepCarriesThread_setPhase newPhase sameBoot ownerCarries)⟩
        · exact ⟨owner, (memNew owner).mpr (Or.inl ⟨isOwner, isChangedThread⟩), ownerNotHalted,
            (carriesImage owner candidate).mp ownerCarries⟩
      activeStepsHaveDistinctThreads := fun first isFirst second isSecond firstActive
          secondActive sameThread => by
        rw [sameSteps] at isFirst isSecond
        obtain ⟨firstOld, isFirstOld, isFirstImage⟩ := List.mem_map.mp isFirst
        obtain ⟨secondOld, isSecondOld, isSecondImage⟩ := List.mem_map.mp isSecond
        subst isFirstImage
        subst isSecondImage
        rw [(imageKeeps firstOld).2.2.2.1] at firstActive
        rw [(imageKeeps secondOld).2.2.2.1] at secondActive
        rw [(imageKeeps firstOld).2.1, (imageKeeps secondOld).2.1] at sameThread
        rw [related.activeStepsHaveDistinctThreads firstOld isFirstOld secondOld isSecondOld
          firstActive secondActive sameThread]
      invocationsAreActive := fun changed isChanged hasInvocation => by
        rw [sameSteps] at isChanged
        obtain ⟨candidate, isCandidate, isImage⟩ := List.mem_map.mp isChanged
        subst isImage
        rw [(imageKeeps candidate).2.2.2.1]
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
        subst isFirstImage
        subst isSecondImage
        rw [(imageKeeps firstOld).2.2.2.1] at firstActive
        rw [(imageKeeps secondOld).2.2.2.1] at secondActive
        rw [(imageKeeps firstOld).2.2.2.2] at firstHasWait
        rw [(imageKeeps secondOld).2.2.2.2] at secondHasWait
        have isFirstThread : first ∈ lConfig.threads := by
          rcases (memNew first).mp isFirst with ⟨isOld, _⟩ | isNew
          · exact isOld
          · subst isNew
            exact (isNotBlocked _ _ firstBlocked).elim
        have isSecondThread : second ∈ lConfig.threads := by
          rcases (memNew second).mp isSecond with ⟨isOld, _⟩ | isNew
          · exact isOld
          · subst isNew
            exact (isNotBlocked _ _ secondBlocked).elim
        exact related.blockingOrder first second firstOld secondOld firstOn firstSince secondOn
          secondSince firstWait secondWait isFirstThread isSecondThread isFirstOld isSecondOld
          firstActive secondActive ((carriesImage first firstOld).mpr firstCarries)
          ((carriesImage second secondOld).mpr secondCarries) firstBlocked secondBlocked
          firstHasWait secondHasWait
      tasksAreCurrent := tasksAreCurrent }

/-! ## Task lookup -/

/-- With unique task IDs, the lookup of a task's ID finds the task. -/
theorem wp2_findTask?_of_mem {config : Dex.Config signature}
    (tasksDistinct : TasksAreDistinct config) {task : Dex.Task signature}
    (isMember : task ∈ config.tasks) : config.findTask? task.id = some task := by
  cases foundCase : config.findTask? task.id with
  | none =>
    have isRejected := List.find?_eq_none.mp foundCase task isMember
    simp at isRejected
  | some found =>
    rw [eq_of_isFound config task.id task found tasksDistinct isMember rfl foundCase]

/-- The task lookup after a change that keeps task IDs. -/
theorem wp2_findTask?_updateTask (config : Dex.Config signature) (taskId other : Nat)
    (change : Dex.Task signature → Dex.Task signature)
    (keepsId : ∀ task, (change task).id = task.id) :
    (config.updateTask taskId change).findTask? other =
      (config.findTask? other).map (fun task => if task.id = taskId then change task else task) := by
  simp only [Dex.Config.findTask?, Dex.Config.updateTask, List.find?_map]
  congr 1
  apply congrArg (fun predicate => config.tasks.find? predicate)
  funext candidate
  by_cases isChanged : candidate.id = taskId <;> simp [isChanged, keepsId]

/-- Two changes of the same task are one change. -/
theorem wp2_updateTask_updateTask (config : Dex.Config signature) (taskId : Nat)
    (first second : Dex.Task signature → Dex.Task signature)
    (keepsId : ∀ task, (first task).id = task.id) :
    (config.updateTask taskId first).updateTask taskId second =
      config.updateTask taskId (fun task => second (first task)) := by
  simp only [Dex.Config.updateTask, List.map_map]
  congr 1
  apply List.map_congr_left
  intro task _
  by_cases isChanged : task.id = taskId <;> simp [isChanged, keepsId]

/-! ## [WORKER-EFFECT] of the one effect a task holds -/

/-- The Worker steps that deliver the effect of a task: run the handler if
it has not run, then emit the effect. -/
def wp2_deliverActions (task : Dex.Task signature) : List (Dex.Action signature) :=
  match task.phase with
  | .started => [.workerRun task.id, .workerEffect task.id]
  | _ => [.workerEffect task.id]

/-- The task after its one effect was emitted. -/
def wp2_deliveredTask (program : Program) (task : Dex.Task signature) : Dex.Task signature :=
  { task with phase := .running [] (task.compute ⟦program⟧).2 }

/-- A task that holds exactly one effect emits it by `wp2_deliverActions`. -/
theorem wp2_exec_deliver (program : Program) (config : Dex.Config signature)
    (tasksDistinct : TasksAreDistinct config) (task : Dex.Task signature)
    (isMember : task ∈ config.tasks) (isComputed : IsTaskComputed program task) (effect : Val)
    (hasEffect : pendingEffects ⟦program⟧ task = [effect]) :
    (Dex.system ⟦program⟧).exec config (wp2_deliverActions task) =
      some ([Dex.Obs.fx task.step task.method effect],
        config.updateTask task.id (fun _ => wp2_deliveredTask program task)) := by
  have isFound := wp2_findTask?_of_mem tasksDistinct isMember
  have sameUpdate : ∀ change : Dex.Task signature → Dex.Task signature, change task = wp2_deliveredTask program task →
      config.updateTask task.id change =
        config.updateTask task.id (fun _ => wp2_deliveredTask program task) := by
    intro change isDelivered
    simp only [Dex.Config.updateTask]
    congr 1
    apply List.map_congr_left
    intro candidate isCandidate
    by_cases isChanged : candidate.id = task.id
    · have isTask : candidate = task :=
        eq_of_nodup_map (fun current => current.id) config.tasks tasksDistinct.1 isCandidate
          isMember isChanged
      simp [isTask, isDelivered]
    · simp [isChanged]
  unfold pendingEffects at hasEffect
  unfold IsTaskComputed at isComputed
  cases phaseCase : task.phase with
  | started =>
    simp only [phaseCase] at hasEffect
    let ranTask : Dex.Task signature → Dex.Task signature := fun current =>
      { current with phase := .running (task.compute ⟦program⟧).1 (task.compute ⟦program⟧).2 }
    let emittedTask : Dex.Task signature → Dex.Task signature := fun current =>
      { current with phase := .running [] (task.compute ⟦program⟧).2 }
    have isFoundNext : (config.updateTask task.id ranTask).findTask? task.id =
        some { task with phase := .running [effect] (task.compute ⟦program⟧).2 } := by
      rw [wp2_findTask?_updateTask config task.id task.id ranTask (fun _ => rfl), isFound]
      simp [ranTask, hasEffect]
    have isDelivered : (config.updateTask task.id ranTask).updateTask task.id emittedTask =
        config.updateTask task.id (fun _ => wp2_deliveredTask program task) := by
      rw [wp2_updateTask_updateTask config task.id ranTask emittedTask (fun _ => rfl)]
      exact sameUpdate _ rfl
    simp only [ranTask] at isFoundNext
    simp only [wp2_deliverActions, phaseCase, TransitionSystem.exec, Dex.system, Dex.step,
      isFound, isFoundNext, List.nil_append, List.append_nil]
    exact congrArg (fun next => some ([Dex.Obs.fx task.step task.method effect], next))
      isDelivered
  | running remaining response =>
    simp only [phaseCase] at hasEffect isComputed
    subst hasEffect
    simp only [wp2_deliverActions, phaseCase, TransitionSystem.exec, Dex.system, Dex.step,
      isFound]
    rw [sameUpdate _ (by simp [wp2_deliveredTask, isComputed])]
    rfl
  | returned response =>
    simp [phaseCase] at hasEffect

/-- The Worker steps of a delivery attempt no request. -/
theorem wp2_deliverActions_requests (program : Program) (task : Dex.Task signature) :
    (wp2_deliverActions task).filterMap (Dex.system ⟦program⟧).requestOf = [] := by
  unfold wp2_deliverActions
  split <;> rfl

/-! ## [STEP-START] -/

/-- The task that [STEP-START] hands to a Worker. -/
def wp2_startedTask (config : Dex.Config signature) (run : Dex.Run signature)
    (record : Dex.StepRecord signature) (method : Dex.Method) : Dex.Task signature :=
  { id := config.nextTaskId
    step := record.id
    method := method
    context := { stepExecutionId := record.id, fromStepExecutionId := record.origin }
    input := record.input
    snapshot := run.snapshot record.options method
    conditionResults := record.conditionResults
    phase := .started }

/-- The Step record after [STEP-START]. -/
def wp2_startedRecord (config : Dex.Config signature) (record : Dex.StepRecord signature)
    (method : Dex.Method) : Dex.StepRecord signature :=
  { record with invocation := some { method := method, state := .running config.nextTaskId } }

/-- The run after [STEP-START]. -/
def wp2_startedRun (config : Dex.Config signature) (run : Dex.Run signature)
    (record : Dex.StepRecord signature) (method : Dex.Method) : Dex.Run signature :=
  { run with seq := run.seq + 1 }.updateStep record.id (fun current =>
    { current with invocation := some { method := method, state := .running config.nextTaskId } })

/-- The configuration after [STEP-START]. -/
def wp2_startedConfig (config : Dex.Config signature) (run : Dex.Run signature)
    (record : Dex.StepRecord signature) (method : Dex.Method) : Dex.Config signature :=
  { config with
    run := some (wp2_startedRun config run record method)
    lastCommitAt := some config.clock
    tasks := config.tasks ++ [wp2_startedTask config run record method]
    nextTaskId := config.nextTaskId + 1 }

/-- [STEP-START] of a pending invocation, as one explicit action. -/
theorem wp2_exec_stepStart (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (run : Dex.Run signature) (hasRun : config.run = some run) (isRunning : run.status = .running)
    (record : Dex.StepRecord signature) (isFound : run.findStep? record.id = some record)
    (method : Dex.Method)
    (isPending : record.invocation = some { method := method, state := .pending })
    (canCommit : config.canCommit = true) :
    (Dex.system flow).exec config [.stepStart record.id] =
      some ([], wp2_startedConfig config run record method) := by
  simp [TransitionSystem.exec, Dex.system, Dex.step, hasRun, isFound, isPending, canCommit,
    Dex.Config.commit, Dex.Config.closeEvents, Dex.Run.updateStep, isRunning,
    wp2_startedConfig, wp2_startedRun, wp2_startedTask]

/-- The started task runs the started Step. -/
theorem wp2_hasRunningTask_started (config : Dex.Config signature) (run : Dex.Run signature)
    (record : Dex.StepRecord signature) (method : Dex.Method)
    (Holds : Dex.Task signature → Prop)
    (holds : Holds (wp2_startedTask config run record method)) :
    HasRunningTask (wp2_startedConfig config run record method)
      (wp2_startedRecord config record method) method Holds :=
  ⟨wp2_startedTask config run record method, by simp [wp2_startedConfig], rfl, rfl, rfl, rfl,
    holds⟩

/-- A running task that satisfies more satisfies less. -/
theorem wp2_hasRunningTask_imp {config : Dex.Config signature}
    {record : Dex.StepRecord signature} {method : Dex.Method}
    {Holds Weaker : Dex.Task signature → Prop} (isWeaker : ∀ task, Holds task → Weaker task)
    (hasTask : HasRunningTask config record method Holds) :
    HasRunningTask config record method Weaker := by
  obtain ⟨task, isMember, hasInvocation, isStep, isMethod, sameInput, holds⟩ := hasTask
  exact ⟨task, isMember, hasInvocation, isStep, isMethod, sameInput, isWeaker task holds⟩

/-- The begin of an action: after [STEP-START] of the Step that carries the
thread, the configurations are related again, given the new match of the
thread with its started Step. -/
theorem wp2_stepStart_related {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    {run : Dex.Run signature} (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running)
    {record : Dex.StepRecord signature} (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true)
    (carries : StepCarriesThread program thread record) (method : Dex.Method)
    (isPending : record.invocation = some { method := method, state := .pending })
    (newPhase : Phase)
    (sameBoot : wp2_isBootPhase newPhase = wp2_isBootPhase thread.phase)
    (wasNotBlocked : ∀ on since, thread.phase ≠ .blocked on since)
    (isNotBlocked : ∀ on since, newPhase ≠ .blocked on since)
    (isNotHalted : newPhase ≠ .halted)
    (keepsCanceled : thread.phase = .woken .canceled ∨ thread.phase = .resuming .canceled →
      newPhase = .woken .canceled ∨ newPhase = .resuming .canceled)
    (newMatch : PhaseMatchesStep program lConfig (wp2_startedConfig dexConfig run record method)
      { thread with phase := newPhase } (wp2_startedRecord dexConfig record method)) :
    Related program (lConfig.setPhase tid newPhase).stamp
      (wp2_startedConfig dexConfig run record method) := by
  obtain ⟨isMember, hasTid⟩ := wp2_mem_of_findThread? isFound
  obtain ⟨isRunningL, _⟩ := wp2_run_of_thread state thread isMember
  have related := state.isRelated.running run hasRun isRunning
  have idsNodup := (state.dexInvariant.runInvariant run hasRun).idsNodup
  have isFoundRecord : run.findStep? record.id = some record :=
    Dex.Run.findStep?_of_mem_nodup run record isRecord idsNodup
  let image : Dex.StepRecord signature → Dex.StepRecord signature := fun candidate =>
    if candidate.id = record.id then
      { candidate with
        invocation := some { method := method, state := .running dexConfig.nextTaskId } }
    else candidate
  have frame : RunningRelated program (lConfig.setPhase tid newPhase)
      (wp2_startedConfig dexConfig run record method)
      (wp2_startedRun dexConfig run record method) := by
    refine wp2_runningRelated_setPhase related state.lInvariant idsNodup isFound isRecord
      isActive carries newPhase sameBoot wasNotBlocked isNotBlocked isNotHalted keepsCanceled
      image rfl ?_ ?_ rfl rfl rfl rfl rfl ?_ ?_ ?_
    · intro candidate
      by_cases isChanged : candidate.id = record.id <;> simp [image, isChanged]
    · intro candidate isOther
      simp [image, isOther]
    · intro task isTask _
      exact List.mem_append_left _ isTask
    · have isImage : image record = wp2_startedRecord dexConfig record method := by
        simp [image, wp2_startedRecord]
      rw [isImage]
      exact newMatch
    · intro task isTask
      rcases List.mem_append.mp isTask with isOld | isNew
      · have wasCurrent := related.tasksAreCurrent task isOld
        simp only [Dex.Config.isTaskCurrent, hasRun] at wasCurrent
        by_cases sameStep : task.step = record.id
        · simp [sameStep, isFoundRecord, isPending] at wasCurrent
        · have sameFind : (wp2_startedRun dexConfig run record method).findStep? task.step =
              run.findStep? task.step :=
            Dex.Run.findStep?_updateStep_other _ _ _ _ (fun _ => rfl) sameStep
          simp only [Dex.Config.isTaskCurrent, wp2_startedConfig, sameFind]
          exact wasCurrent
      · have isTaskNew : task = wp2_startedTask dexConfig run record method := by
          simpa using isNew
        have isFoundNew : (wp2_startedRun dexConfig run record method).findStep? record.id =
            some (wp2_startedRecord dexConfig record method) :=
          Dex.Run.findStep?_updateStep_self _ _ _ record isFoundRecord rfl
        simp [Dex.Config.isTaskCurrent, wp2_startedConfig, isTaskNew, wp2_startedTask,
          isFoundNew, wp2_startedRecord]
  exact
    { clock := state.isRelated.clock
      stamp := congrArg some state.isRelated.clock
      status := by
        simp only [L.Config.stamp, L.Config.setPhase, L.Config.updateThread, isRunningL,
          wp2_startedConfig]
        exact isRunning
      notStarted := fun hasNoRun => by simp [wp2_startedConfig] at hasNoRun
      running := fun newRun hasNewRun _ => by
        obtain rfl : wp2_startedRun dexConfig run record method = newRun :=
          Option.some.inj hasNewRun
        exact wp2_runningRelated_stamp frame
      closed := fun newRun status hasNewRun isClosed => by
        obtain rfl : wp2_startedRun dexConfig run record method = newRun :=
          Option.some.inj hasNewRun
        have isClosedRun : run.status = .closed status := isClosed
        rw [isRunning] at isClosedRun
        cases isClosedRun }

/-! ## The external effects of the translated handlers (spec §8.2: "Each
`emit` is the only effect of its own Execute") -/

/-- No WaitFor of a translated Flow has an external effect. -/
theorem wp2_compute_waitFor_effects (program : Program) (task : Dex.Task signature)
    (isWaitFor : task.method = .waitFor) : (task.compute ⟦program⟧).1 = [] := by
  unfold Dex.Task.compute
  simp only [isWaitFor]
  cases task.step.stepType with
  | start => simp [translate, startStepDef]
  | point code =>
    simp only [translate, pointStepDef]
    split
    · rename_i waitFor isDefined
      split at isDefined
      · cases isDefined
        dsimp only
        split <;> rfl
      · cases isDefined
    · rfl
  | timed =>
    simp only [translate, timedStepDef]
    split
    · split <;> rfl
    · rfl

/-- The Execute of the start Step has no external effect. -/
theorem wp2_startStep_effects (program : Program) (context : Dex.Context signature)
    (input : Option DexVal) (snapshot : Dex.Snapshot signature)
    (results : Option (Dex.ConditionResults signature)) :
    ((startStepDef program).execute context input snapshot results).1 = [] := by
  simp only [startStepDef]
  split <;> rfl

/-- The Execute of the `Timed` Step has no external effect. -/
theorem wp2_timedStep_effects (context : Dex.Context signature)
    (input : Option DexVal) (snapshot : Dex.Snapshot signature)
    (results : Option (Dex.ConditionResults signature)) :
    (timedStepDef.execute context input snapshot results).1 = [] := by
  simp only [timedStepDef]
  split <;> rfl

/-- The Execute of a blocking program point has no external effect. -/
theorem wp2_pointStep_effects_of_blocking (program : Program) (code : Code)
    (isBlocking : isBlockingHead code = true) (context : Dex.Context signature)
    (input : Option DexVal) (snapshot : Dex.Snapshot signature)
    (results : Option (Dex.ConditionResults signature)) :
    ((pointStepDef program code).execute context input snapshot results).1 = [] := by
  simp only [pointStepDef, isBlocking]
  split <;> rfl

/-- The Execute of a program point that does not block has the effect of an
`emit` head statement, and no other. -/
theorem wp2_pointStep_effects (program : Program) (core : ThreadCore) (code : Code)
    (isNotBlocking : isBlockingHead code = false) (context : Dex.Context signature)
    (snapshot : Dex.Snapshot signature) (results : Option (Dex.ConditionResults signature)) :
    ((pointStepDef program code).execute context (some (DexVal.thread core)) snapshot results).1 =
      match beginKind core code with
      | .emit effect => [effect]
      | _ => [] := by
  simp only [pointStepDef, isNotBlocking]
  cases beginKind core code <;> rfl

/-- The effects a started Execute task holds are those of its handler. -/
theorem wp2_pendingEffects_started_execute (program : Program) (config : Dex.Config signature)
    (run : Dex.Run signature) (record : Dex.StepRecord signature) :
    pendingEffects ⟦program⟧ (wp2_startedTask config run record .execute) =
      ((⟦program⟧.stepDef record.id.stepType).execute
        { stepExecutionId := record.id, fromStepExecutionId := record.origin } record.input
        (run.snapshot record.options .execute) record.conditionResults).1 := rfl

/-- The Execute snapshot of a Step with the load selection of `moveToPoint`
holds the waiter list its head statement reads. -/
theorem wp2_readWaiters_snapshot (run : Dex.Run signature) (core : ThreadCore) (code : Code)
    (options : Dex.StepOptions signature)
    (hasSelection : options.executeLoadMaps = executeLoadMaps core code) (cv : CvId)
    (isOperand : cvOperand core code = some cv) :
    readWaiters (run.snapshot options .execute) cv =
      readWaiters { attributes := run.attributes } cv := by
  unfold readWaiters Dex.Run.snapshot
  rw [AssocList.get?_filter_of_kept (Dex.Run.isLoadedKey options .execute) run.attributes
    (Key.cvWaiters cv)
    (by simp [Dex.Run.isLoadedKey, hasSelection, executeLoadMaps, isOperand,
      Dex.StepOptions.loadMaps])]

/-- Every handler of a translated Flow has at most one external effect. -/
theorem wp2_compute_effects_length (program : Program) (task : Dex.Task signature) :
    (task.compute ⟦program⟧).1.length ≤ 1 := by
  cases methodCase : task.method with
  | waitFor => simp [wp2_compute_waitFor_effects program task methodCase]
  | execute =>
    unfold Dex.Task.compute
    simp only [methodCase]
    cases task.step.stepType with
    | start => simp [translate, wp2_startStep_effects]
    | timed => simp [translate, wp2_timedStep_effects]
    | point code =>
      simp only [translate, pointStepDef]
      split
      · split
        · simp
        · split <;> simp
      · simp

/-! ## After the close: the task that holds a late emit -/

/-- A list with at most one element that has an element is that singleton. -/
theorem wp2_eq_singleton_of_mem {α : Type} {element : α} {elements : List α}
    (isMember : element ∈ elements) (isShort : elements.length ≤ 1) :
    elements = [element] := by
  match elements, isMember, isShort with
  | [only], isOnly, _ => simp [List.mem_singleton.mp isOnly]
  | _ :: _ :: _, _, isTooLong => simp at isTooLong

/-- A list is its element at `index` followed by the rest, up to order. -/
theorem wp2_perm_removeAt {α : Type} (elements : List α) (index : Nat) (element : α)
    (isAt : elements[index]? = some element) :
    elements.Perm (element :: removeAt elements index) := by
  induction elements generalizing index with
  | nil => simp at isAt
  | cons head rest inductionHypothesis =>
    cases index with
    | zero =>
      simp only [List.getElem?_cons_zero, Option.some.injEq] at isAt
      subst isAt
      exact List.Perm.refl _
    | succ previous =>
      simp only [List.getElem?_cons_succ] at isAt
      exact ((inductionHypothesis previous isAt).cons head).trans (List.Perm.swap _ _ _)

/-- With unique task IDs, the task list splits around a task, and no other
task has its ID. -/
theorem wp2_split_task {config : Dex.Config signature} (tasksDistinct : TasksAreDistinct config)
    {task : Dex.Task signature} (isMember : task ∈ config.tasks) :
    ∃ before after, config.tasks = before ++ task :: after ∧
      (∀ other, other ∈ before → other.id ≠ task.id) ∧
      (∀ other, other ∈ after → other.id ≠ task.id) := by
  obtain ⟨before, after, isSplit⟩ := List.append_of_mem isMember
  have idsNodup := tasksDistinct.1
  rw [isSplit] at idsNodup
  simp only [List.map_append, List.map_cons, List.nodup_append, List.nodup_cons, List.mem_map,
    List.mem_cons] at idsNodup
  refine ⟨before, after, isSplit, ?_, ?_⟩
  · intro other isOther sameId
    exact idsNodup.2.2 other.id ⟨other, isOther, rfl⟩ task.id (Or.inl rfl) sameId
  · intro other isOther sameId
    exact idsNodup.2.1.1 ⟨other, isOther, sameId⟩

/-- The configurations after the close: the Flow is closed. -/
theorem wp2_run_of_closed {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig)
    (isClosed : lConfig.status = .closed) :
    ∃ run status, dexConfig.run = some run ∧ run.status = .closed status := by
  have isStatus := isRelated.status
  cases runCase : dexConfig.run with
  | none => simp [isClosed, runCase] at isStatus
  | some run =>
    simp only [isClosed, runCase] at isStatus
    obtain ⟨status, hasStatus⟩ := isStatus
    exact ⟨run, status, rfl, hasStatus⟩

/-- A late emit is the one effect of some task. Without that task, the
remaining tasks hold the remaining late emits. The hypothesis
`singleEffects` is not part of `IsSimulationState` today; see the README. -/
theorem wp2_late_task {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    {run : Dex.Run signature} {status : Dex.CloseStatus} (hasRun : dexConfig.run = some run)
    (isClosedRun : run.status = .closed status) (index : Nat) (effect : Val)
    (isLate : lConfig.lateEmits[index]? = some effect) :
    ∃ task before after, task ∈ dexConfig.tasks ∧
      pendingEffects ⟦program⟧ task = [effect] ∧
      dexConfig.tasks = before ++ task :: after ∧
      (∀ other, other ∈ before → other.id ≠ task.id) ∧
      (∀ other, other ∈ after → other.id ≠ task.id) ∧
      ((before ++ after).flatMap (pendingEffects ⟦program⟧)).Perm
        (removeAt lConfig.lateEmits index) := by
  have closedRelated := state.isRelated.closed run status hasRun isClosedRun
  have isHeld : effect ∈ dexConfig.tasks.flatMap (pendingEffects ⟦program⟧) :=
    closedRelated.lateEmits.mem_iff.mpr (List.mem_of_getElem? isLate)
  obtain ⟨task, isTask, holdsEffect⟩ := List.mem_flatMap.mp isHeld
  have hasEffect := wp2_eq_singleton_of_mem holdsEffect (singleEffects task isTask)
  obtain ⟨before, after, isSplit, beforeOther, afterOther⟩ :=
    wp2_split_task state.tasksDistinct isTask
  refine ⟨task, before, after, isTask, hasEffect, isSplit, beforeOther, afterOther, ?_⟩
  have isPerm := closedRelated.lateEmits
  rw [isSplit] at isPerm
  simp only [List.flatMap_append, List.flatMap_cons, hasEffect, List.singleton_append] at isPerm
  have isMiddle : (effect :: (before ++ after).flatMap (pendingEffects ⟦program⟧)).Perm
      (effect :: removeAt lConfig.lateEmits index) := by
    rw [List.flatMap_append]
    exact (List.perm_middle.symm.trans isPerm).trans
      (wp2_perm_removeAt lConfig.lateEmits index effect isLate)
  exact isMiddle.cons_inv

/-- Removing a task whose ID no other task has leaves the other tasks. -/
theorem wp2_removeTask_of_split (config : Dex.Config signature) (task : Dex.Task signature)
    (before after : List (Dex.Task signature)) (isSplit : config.tasks = before ++ task :: after)
    (beforeOther : ∀ other, other ∈ before → other.id ≠ task.id)
    (afterOther : ∀ other, other ∈ after → other.id ≠ task.id) :
    (config.removeTask task.id).tasks = before ++ after := by
  simp only [Dex.Config.removeTask, isSplit, List.filter_append, List.filter_cons,
    decide_true, Bool.not_true, Bool.false_eq_true, ↓reduceIte]
  congr 1
  · exact List.filter_eq_self.mpr (fun other isOther => by simp [beforeOther other isOther])
  · exact List.filter_eq_self.mpr (fun other isOther => by simp [afterOther other isOther])

/-- Changing a task whose ID no other task has leaves the other tasks. -/
theorem wp2_updateTask_of_split (config : Dex.Config signature) (task : Dex.Task signature)
    (change : Dex.Task signature → Dex.Task signature)
    (before after : List (Dex.Task signature)) (isSplit : config.tasks = before ++ task :: after)
    (beforeOther : ∀ other, other ∈ before → other.id ≠ task.id)
    (afterOther : ∀ other, other ∈ after → other.id ≠ task.id) :
    (config.updateTask task.id change).tasks = before ++ change task :: after := by
  simp only [Dex.Config.updateTask, isSplit, List.map_append, List.map_cons, ↓reduceIte]
  congr 1
  · exact (List.map_congr_left (fun other isOther => by simp [beforeOther other isOther])).trans
      (List.map_id _)
  · congr 1
    exact (List.map_congr_left (fun other isOther => by simp [afterOther other isOther])).trans
      (List.map_id _)

end DexExpressiveness
