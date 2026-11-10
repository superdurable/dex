import DexExpressiveness.Simulation.RelationLemmas
import DexExpressiveness.Properties

/-!
# Shared lemmas of the step simulations

Facts that several step simulations use: the status and the clock of related
configurations, the lookup of threads and tasks, the thread that blocked
first, which Step carries a thread, and what `PhaseMatchesStep` reads. The
work packages proved them separately; this file holds one copy of each.
Everything here is proved.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Clocks and status -/

/-- A stamped L action may happen exactly when a Dex commit may. -/
theorem Related.canCommit_of_canStamp {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig)
    (canStamp : lConfig.canStamp = true) : dexConfig.canCommit = true := by
  unfold L.Config.canStamp at canStamp
  unfold Dex.Config.canCommit
  rw [← isRelated.stamp, ← isRelated.clock]
  exact canStamp

/-- A stamped L action may happen when a Dex commit may. -/
theorem Related.canStamp_of_canCommit {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig)
    (canCommit : dexConfig.canCommit = true) : lConfig.canStamp = true := by
  unfold Dex.Config.canCommit at canCommit
  unfold L.Config.canStamp
  rw [isRelated.stamp, isRelated.clock]
  exact canCommit

/-- A configuration with a thread is running, so the Flow runs. -/
theorem IsSimulationState.run_of_thread {program : Program} {lConfig : L.Config}
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

/-- The Flow of a running program runs. -/
theorem Related.run_of_running {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig)
    (isRunningL : lConfig.status = .running) :
    ∃ run, dexConfig.run = some run ∧ run.status = .running := by
  have isStatus := isRelated.status
  cases runCase : dexConfig.run with
  | none => simp [isRunningL, runCase] at isStatus
  | some run =>
    simp only [isRunningL, runCase] at isStatus
    exact ⟨run, rfl, isStatus⟩

/-- The configurations after the close: the Flow is closed. -/
theorem Related.run_of_closed {program : Program} {lConfig : L.Config}
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

/-- When the Flow runs, the L program runs. -/
theorem Related.status_of_running_run {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) : lConfig.status = .running := by
  have isStatus := isRelated.status
  rw [hasRun] at isStatus
  cases lStatus : lConfig.status with
  | notStarted => simp [lStatus] at isStatus
  | running => rfl
  | closed => simp [lStatus, isRunning] at isStatus

/-- When the Flow is closed, the L program is closed. -/
theorem Related.status_of_closed_run (program : Program) (lConfig : L.Config) (config : Dex.Config signature)
    (isRelated : Related program lConfig config) (run : Dex.Run signature)
    (status : Dex.CloseStatus) (hasRun : config.run = some run)
    (isClosed : run.status = .closed status) : lConfig.status = .closed := by
  have isStatus := isRelated.status
  rw [hasRun] at isStatus
  cases lStatus : lConfig.status with
  | notStarted => simp [lStatus] at isStatus
  | running => simp [lStatus, isClosed] at isStatus
  | closed => rfl

/-- Before the Flow starts, the L program has not started. -/
theorem Related.status_of_no_run {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig)
    (hasNoRun : dexConfig.run = none) : lConfig.status = .notStarted := by
  have isStatus := isRelated.status
  rw [hasNoRun] at isStatus
  cases lStatus : lConfig.status with
  | notStarted => rfl
  | running => simp [lStatus] at isStatus
  | closed => simp [lStatus] at isStatus

/-! ## Threads -/

/-- What the thread lookup finds is a thread of the configuration. -/
theorem mem_of_findThread? {config : L.Config} {tid : Tid} {thread : L.Thread}
    (isFound : config.findThread? tid = some thread) :
    thread ∈ config.threads ∧ thread.core.tid = tid :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- Two threads of an invariant configuration with the same ID are the same. -/
theorem thread_eq_of_tid {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {first second : L.Thread}
    (isFirst : first ∈ config.threads) (isSecond : second ∈ config.threads)
    (sameTid : first.core.tid = second.core.tid) : first = second :=
  eq_of_nodup_map (fun thread => thread.core.tid) config.threads invariant.threadIdsNodup
    isFirst isSecond sameTid

/-- No thread has the ID exactly when the lookup finds nothing. -/
theorem findThread?_eq_none_iff (config : L.Config) (tid : Tid) :
    config.findThread? tid = none ↔ ∀ thread, thread ∈ config.threads → thread.core.tid ≠ tid := by
  unfold L.Config.findThread?
  simp [List.find?_eq_none]

/-- With unique thread IDs, a thread is what the lookup of its ID finds. -/
theorem findThread?_of_mem_of_nodup (config : L.Config) (thread : L.Thread)
    (idsNodup : (config.threads.map (fun candidate => candidate.core.tid)).Nodup)
    (isMember : thread ∈ config.threads) : config.findThread? thread.core.tid = some thread := by
  cases foundCase : config.findThread? thread.core.tid with
  | none => exact absurd rfl ((findThread?_eq_none_iff config _).mp foundCase thread isMember)
  | some found =>
    obtain ⟨isFoundMember, sameId⟩ := mem_of_findThread? foundCase
    rw [eq_of_nodup_map (fun candidate => candidate.core.tid) config.threads idsNodup
      isFoundMember isMember sameId]

/-- The lookup of an ID depends only on which threads have that ID. -/
theorem findThread?_congr (first second : L.Config) (tid : Tid)
    (secondNodup : (second.threads.map (fun candidate => candidate.core.tid)).Nodup)
    (sameThreads : ∀ thread, thread.core.tid = tid →
      (thread ∈ first.threads ↔ thread ∈ second.threads)) :
    first.findThread? tid = second.findThread? tid := by
  cases foundCase : first.findThread? tid with
  | none =>
    symm
    apply (findThread?_eq_none_iff second tid).mpr
    intro thread isMember hasId
    exact (findThread?_eq_none_iff first tid).mp foundCase thread
      ((sameThreads thread hasId).mpr isMember) hasId
  | some found =>
    obtain ⟨isMember, hasId⟩ := mem_of_findThread? foundCase
    rw [← hasId]
    exact (findThread?_of_mem_of_nodup second found secondNodup
      ((sameThreads found hasId).mp isMember)).symm

/-- The threads after an update of one thread. -/
theorem mem_updateThread (config : L.Config) (tid : Tid) (change : L.Thread → L.Thread)
    (thread : L.Thread) :
    thread ∈ (config.updateThread tid change).threads ↔
      ∃ original, original ∈ config.threads ∧
        thread = if original.core.tid = tid then change original else original := by
  simp only [L.Config.updateThread, List.mem_map]
  constructor
  · rintro ⟨original, isMember, isThread⟩
    exact ⟨original, isMember, isThread.symm⟩
  · rintro ⟨original, isMember, isThread⟩
    exact ⟨original, isMember, isThread.symm⟩

/-- An update that keeps thread IDs keeps the list of thread IDs. -/
theorem tids_updateThread (config : L.Config) (tid : Tid) (change : L.Thread → L.Thread)
    (keepsId : ∀ candidate, (change candidate).core.tid = candidate.core.tid) :
    (config.updateThread tid change).threads.map (fun thread => thread.core.tid) =
      config.threads.map (fun thread => thread.core.tid) := by
  simp only [L.Config.updateThread, List.map_map]
  apply List.map_congr_left
  intro candidate _
  by_cases isThread : candidate.core.tid = tid <;> simp [isThread, keepsId]

/-- Setting the phase of one thread: the lookup of any thread. -/
theorem findThread?_setPhase (config : L.Config) (tid other : Tid) (phase : Phase) :
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
theorem mem_setPhase {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {tid : Tid} {thread : L.Thread}
    (isFound : config.findThread? tid = some thread) (phase : Phase) (candidate : L.Thread) :
    candidate ∈ (config.setPhase tid phase).threads ↔
      (candidate ∈ config.threads ∧ candidate.core.tid ≠ tid) ∨
      candidate = { thread with phase := phase } := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  simp only [L.Config.setPhase, L.Config.updateThread, List.mem_map]
  constructor
  · rintro ⟨original, isOriginal, isCandidate⟩
    by_cases isChanged : original.core.tid = tid
    · have isThread : original = thread :=
        thread_eq_of_tid invariant isOriginal isMember (isChanged.trans hasTid.symm)
      subst isThread
      simp only [isChanged, ↓reduceIte] at isCandidate
      exact Or.inr isCandidate.symm
    · simp only [isChanged, ↓reduceIte] at isCandidate
      subst isCandidate
      exact Or.inl ⟨isOriginal, isChanged⟩
  · rintro (⟨isOld, isOther⟩ | isNew)
    · exact ⟨candidate, isOld, by simp [isOther]⟩
    · exact ⟨thread, isMember, by simp [hasTid, isNew]⟩

/-- With unique thread IDs, the lookup of a thread's ID finds the thread. -/
theorem findThread?_of_mem {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {thread : L.Thread}
    (isMember : thread ∈ config.threads) : config.findThread? thread.core.tid = some thread := by
  cases foundCase : config.findThread? thread.core.tid with
  | none =>
    have isRejected := List.find?_eq_none.mp foundCase thread isMember
    simp at isRejected
  | some found =>
    obtain ⟨isFoundMember, sameTid⟩ := mem_of_findThread? foundCase
    rw [thread_eq_of_tid invariant isFoundMember isMember sameTid]

/-! ## Tasks -/

/-- The task lookup after a change that keeps task IDs. -/
theorem findTask?_updateTask (config : Dex.Config signature) (taskId other : Nat)
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
theorem updateTask_updateTask (config : Dex.Config signature) (taskId : Nat)
    (first second : Dex.Task signature → Dex.Task signature)
    (keepsId : ∀ task, (first task).id = task.id) :
    (config.updateTask taskId first).updateTask taskId second =
      config.updateTask taskId (fun task => second (first task)) := by
  simp only [Dex.Config.updateTask, List.map_map]
  congr 1
  apply List.map_congr_left
  intro task _
  by_cases isChanged : task.id = taskId <;> simp [isChanged, keepsId]

/-- Whether a task is current does not depend on the other tasks. -/
theorem isTaskCurrent_congr (config other : Dex.Config signature)
    (task changed : Dex.Task signature) (sameRun : other.run = config.run)
    (sameStep : changed.step = task.step) (sameMethod : changed.method = task.method)
    (sameId : changed.id = task.id) :
    other.isTaskCurrent changed = config.isTaskCurrent task := by
  unfold Dex.Config.isTaskCurrent
  rw [sameRun, sameStep, sameMethod, sameId]

/-- The task after its handler returned. -/
def returnedTask (program : Program) (task : Dex.Task signature)
    (current : Dex.Task signature) : Dex.Task signature :=
  { current with phase := .returned (task.compute ⟦program⟧).2 }

/-- A task that holds no effect returns by `returnTaskActions`, silently. -/
theorem exec_returnTaskActions (program : Program) (config : Dex.Config signature)
    (tasksDistinct : TasksAreDistinct config) (task : Dex.Task signature)
    (isMember : task ∈ config.tasks) (isComputed : IsTaskComputed program task)
    (noEffects : pendingEffects ⟦program⟧ task = []) :
    (Dex.system ⟦program⟧).exec config (returnTaskActions task) =
      some ([], config.updateTask task.id (returnedTask program task)) := by
  have isFound := findTask?_of_mem tasksDistinct isMember
  unfold pendingEffects at noEffects
  unfold IsTaskComputed at isComputed
  cases phaseCase : task.phase with
  | started =>
    simp only [phaseCase] at noEffects
    let ranTask : Dex.Task signature → Dex.Task signature := fun current =>
      { current with phase := .running (task.compute ⟦program⟧).1 (task.compute ⟦program⟧).2 }
    have isFoundNext : (config.updateTask task.id ranTask).findTask? task.id =
        some { task with phase := .running [] (task.compute ⟦program⟧).2 } := by
      rw [findTask?_updateTask config task.id task.id ranTask (fun _ => rfl), isFound]
      simp [ranTask, noEffects]
    have isReturned : (config.updateTask task.id ranTask).updateTask task.id
          (returnedTask program task) =
        config.updateTask task.id (returnedTask program task) := by
      rw [updateTask_updateTask config task.id ranTask _ (fun _ => rfl)]
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
    have isSame : config.updateTask task.id (returnedTask program task) = config := by
      have sameTasks : config.tasks.map (fun candidate =>
          if candidate.id = task.id then returnedTask program task candidate else candidate) =
          config.tasks := by
        conv => rhs; rw [← List.map_id config.tasks]
        apply List.map_congr_left
        intro candidate isCandidate
        by_cases isChanged : candidate.id = task.id
        · have isTask : candidate = task :=
            eq_of_nodup_map (fun current => current.id) config.tasks tasksDistinct.1 isCandidate
              isMember isChanged
          subst isTask
          simp only [↓reduceIte, returnedTask, id, ← isComputed, ← phaseCase]
        · simp [isChanged]
      simp only [Dex.Config.updateTask, sameTasks]
    rw [isSame]

/-- A response with no effect wakes no Step. -/
theorem applyEffects_empty (run : Dex.Run signature) : run.applyEffects {} = run := by
  have noWaiters : run.waitersOn [] = [] := by
    apply List.eq_nil_iff_forall_not_mem.mpr
    intro step isWaiter
    simp only [Dex.Run.waitersOn, List.mem_map] at isWaiter
    obtain ⟨pair, isSorted, _⟩ := isWaiter
    obtain ⟨record, _, isPair⟩ := List.mem_filterMap.mp ((mem_sortStable _ _ _).mp isSorted)
    cases phaseCase : record.phase <;> simp only [phaseCase] at isPair <;> try (cases isPair)
    cases waitCase : record.waitState with
    | none => simp [waitCase] at isPair
    | some waitState => simp [waitCase] at isPair
  show (run.wake []) = run
  unfold Dex.Run.wake
  rw [noWaiters]
  rfl

/-! ## The thread that blocked first -/

/-- The blocked threads in a wanted wait, each with its `since` stamp. -/
def blockedCandidates (config : L.Config) (isWanted : BlockedOn → Bool) : List (Nat × Tid) :=
  config.threads.filterMap (fun candidate =>
    match candidate.phase with
    | .blocked on since => if isWanted on then some (since, candidate.core.tid) else none
    | _ => none)

/-- `earliestBlocked` is the first candidate in `since` order. -/
theorem earliestBlocked_eq (config : L.Config) (isWanted : BlockedOn → Bool) :
    config.earliestBlocked isWanted =
      (sortStable (fun first second => decide (first.1 ≤ second.1))
        (blockedCandidates config isWanted)).head?.map (fun candidate => candidate.2) := rfl

/-- No thread is blocked in a wanted wait: `earliestBlocked` finds none. -/
theorem earliestBlocked_eq_none (config : L.Config) (isWanted : BlockedOn → Bool)
    (noneBlocked : ∀ thread, thread ∈ config.threads → ∀ on since,
      thread.phase = .blocked on since → isWanted on = false) :
    config.earliestBlocked isWanted = none := by
  have noCandidate : blockedCandidates config isWanted = [] := by
    apply List.filterMap_eq_nil_iff.mpr
    intro thread isMember
    cases phaseCase : thread.phase <;> simp
    rename_i on since
    exact noneBlocked thread isMember on since phaseCase
  rw [earliestBlocked_eq, noCandidate]
  rfl

/-- The thread whose `since` is the least among the wanted blocked threads is
the one `earliestBlocked` finds. -/
theorem earliestBlocked_eq_some {program : Program} (config : L.Config)
    (invariant : L.Invariant program config) (isWanted : BlockedOn → Bool)
    (thread : L.Thread) (isMember : thread ∈ config.threads) (on : BlockedOn) (since : Nat)
    (isBlocked : thread.phase = .blocked on since) (isWantedOn : isWanted on = true)
    (isEarliest : ∀ other, other ∈ config.threads → ∀ otherOn otherSince,
      other.phase = .blocked otherOn otherSince → isWanted otherOn = true →
      since ≤ otherSince) :
    config.earliestBlocked isWanted = some thread.core.tid := by
  have isHead := head?_sortStable_of_least
    (fun (first second : Nat × Tid) => decide (first.1 ≤ second.1)) (since, thread.core.tid)
    (blockedCandidates config isWanted)
    (List.mem_filterMap.mpr ⟨thread, isMember, by simp [isBlocked, isWantedOn]⟩)
    (by simp)
    (by
      intro other isOther
      obtain ⟨owner, isOwner, isCandidate⟩ := List.mem_filterMap.mp isOther
      cases phaseCase : owner.phase <;> simp [phaseCase] at isCandidate
      rename_i otherOn otherSince
      obtain ⟨isOtherWanted, isOtherPair⟩ := isCandidate
      subst isOtherPair
      by_cases sameSince : otherSince = since
      · subst sameSince
        have isThread : owner = thread :=
          invariant.sinceInjective owner isOwner thread isMember otherOn on otherSince
            phaseCase isBlocked
        rw [isThread]
        exact Or.inl rfl
      · have isAtMost := isEarliest owner isOwner otherOn otherSince phaseCase isOtherWanted
        refine Or.inr ⟨by simpa using isAtMost, ?_⟩
        simp only [decide_eq_false_iff_not, Nat.not_le]
        omega)
  rw [earliestBlocked_eq, isHead]
  rfl

/-- Among the wanted blocked threads, one blocked first. -/
theorem exists_earliestBlocked (config : L.Config) (isWanted : BlockedOn → Bool)
    (thread : L.Thread) (isMember : thread ∈ config.threads) (on : BlockedOn) (since : Nat)
    (isBlocked : thread.phase = .blocked on since) (isWantedOn : isWanted on = true) :
    ∃ earliest earliestOn earliestSince, earliest ∈ config.threads ∧
      earliest.phase = .blocked earliestOn earliestSince ∧ isWanted earliestOn = true ∧
      ∀ other, other ∈ config.threads → ∀ otherOn otherSince,
        other.phase = .blocked otherOn otherSince → isWanted otherOn = true →
        earliestSince ≤ otherSince := by
  induction since using Nat.strongRecOn generalizing thread on with
  | ind since inductionHypothesis =>
    by_cases hasEarlier : ∃ other otherOn otherSince, other ∈ config.threads ∧
        other.phase = .blocked otherOn otherSince ∧ isWanted otherOn = true ∧ otherSince < since
    · obtain ⟨other, otherOn, otherSince, isOther, isOtherBlocked, isOtherWanted, isEarlier⟩ :=
        hasEarlier
      exact inductionHypothesis otherSince isEarlier other isOther otherOn isOtherBlocked
        isOtherWanted
    · refine ⟨thread, on, since, isMember, isBlocked, isWantedOn, ?_⟩
      intro other isOther otherOn otherSince isOtherBlocked isOtherWanted
      apply Nat.le_of_not_lt
      intro isEarlier
      exact hasEarlier ⟨other, otherOn, otherSince, isOther, isOtherBlocked, isOtherWanted,
        isEarlier⟩

/-- No thread is blocked on a wanted wait when `earliestBlocked` finds none. -/
theorem not_isWanted_of_earliestBlocked_none (config : L.Config) (isWanted : BlockedOn → Bool)
    (isNone : config.earliestBlocked isWanted = none) (thread : L.Thread)
    (isMember : thread ∈ config.threads) (on : BlockedOn) (since : Nat)
    (isBlocked : thread.phase = .blocked on since) : isWanted on = false := by
  unfold L.Config.earliestBlocked at isNone
  simp only [Option.map_eq_none_iff] at isNone
  cases wantedCase : isWanted on with
  | false => rfl
  | true =>
    exfalso
    refine not_mem_of_head?_sortStable_none _ _ isNone (since, thread.core.tid) ?_
    refine List.mem_filterMap.mpr ⟨thread, isMember, ?_⟩
    simp [isBlocked, wantedCase]

/-- The thread `earliestBlocked` finds is blocked on a wanted wait, and no
thread blocked on a wanted wait has a smaller `since`. -/
theorem exists_of_earliestBlocked_some (config : L.Config) (isWanted : BlockedOn → Bool) (next : Tid)
    (isSome : config.earliestBlocked isWanted = some next) :
    ∃ thread on since, thread ∈ config.threads ∧ thread.core.tid = next ∧
      thread.phase = .blocked on since ∧ isWanted on = true ∧
      ∀ other, other ∈ config.threads → ∀ otherOn otherSince,
        other.phase = .blocked otherOn otherSince → isWanted otherOn = true →
        since ≤ otherSince := by
  unfold L.Config.earliestBlocked at isSome
  simp only [Option.map_eq_some_iff] at isSome
  obtain ⟨pair, isHead, isNext⟩ := isSome
  have isLeast := le_of_head?_sortStable
    (fun (first second : Nat × Tid) => decide (first.1 ≤ second.1))
    (fun element => by simp)
    (fun first second third isFirst isSecond => by
      simp only [decide_eq_true_eq] at isFirst isSecond ⊢
      omega)
    (fun first second => by
      simp only [decide_eq_true_eq]
      omega)
    _ pair isHead
  have isSorted : pair ∈ sortStable (fun (first second : Nat × Tid) => decide (first.1 ≤ second.1))
      (config.threads.filterMap (fun candidate =>
        match candidate.phase with
        | .blocked on since => if isWanted on then some (since, candidate.core.tid) else none
        | _ => none)) := List.mem_of_mem_head? isHead
  obtain ⟨thread, isMember, isCandidate⟩ :=
    List.mem_filterMap.mp ((mem_sortStable _ _ _).mp isSorted)
  cases phaseCase : thread.phase <;> simp only [phaseCase] at isCandidate <;>
    try (cases isCandidate)
  rename_i on since
  split at isCandidate
  · rename_i isWantedOn
    cases isCandidate
    refine ⟨thread, on, since, isMember, isNext, phaseCase, isWantedOn, ?_⟩
    intro other isOtherMember otherOn otherSince isOtherBlocked isOtherWanted
    have isAtMost := isLeast (otherSince, other.core.tid)
      (List.mem_filterMap.mpr ⟨other, isOtherMember, by simp [isOtherBlocked, isOtherWanted]⟩)
    simpa using isAtMost
  · cases isCandidate

/-! ## Which Step carries a thread -/

/-- `main` before its first statement: the phases whose Step is the start Step. -/
def isBootPhase : Phase → Bool
  | .bootPending => true
  | .bootRunning => true
  | _ => false

/-- A phase is a boot phase exactly when it is `bootPending` or `bootRunning`. -/
theorem isBootPhase_eq_false (phase : Phase) :
    isBootPhase phase = false ↔ phase ≠ .bootPending ∧ phase ≠ .bootRunning := by
  cases phase <;> simp [isBootPhase]

/-- `StepCarriesThread` by cases on "boots or not" and on the timer. -/
theorem stepCarriesThread_iff (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) :
    StepCarriesThread program thread record ↔
      if isBootPhase thread.phase then
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
    simp [StepCarriesThread, isBootPhase]

/-- `StepCarriesThread` reads only the ID, the input, and the options of the Step. -/
theorem stepCarriesThread_congr {program : Program} {thread : L.Thread}
    {record changed : Dex.StepRecord signature} (sameId : changed.id = record.id)
    (sameInput : changed.input = record.input) (sameOptions : changed.options = record.options)
    (carries : StepCarriesThread program thread record) :
    StepCarriesThread program thread changed := by
  rw [stepCarriesThread_iff] at carries ⊢
  rw [sameId, sameInput, sameOptions]
  exact carries

/-- A phase change that does not cross the end of the boot keeps the Step. -/
theorem stepCarriesThread_setPhase {program : Program} {thread : L.Thread}
    {record : Dex.StepRecord signature} (phase : Phase)
    (sameBoot : isBootPhase phase = isBootPhase thread.phase)
    (carries : StepCarriesThread program thread record) :
    StepCarriesThread program { thread with phase := phase } record := by
  rw [stepCarriesThread_iff] at carries ⊢
  simp only [sameBoot]
  exact carries

/-- The Step that carries a thread has the thread's ID in its input. `main`
boots with the empty ID. -/
theorem threadOfInput_of_carries {program : Program} {thread : L.Thread}
    {record : Dex.StepRecord signature} (carries : StepCarriesThread program thread record)
    (bootIsMain : isBootPhase thread.phase = true → thread.core.tid = []) :
    threadOfInput record.input = thread.core.tid := by
  rw [stepCarriesThread_iff] at carries
  cases bootCase : isBootPhase thread.phase with
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
theorem threadOfInput_of_mem {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {thread : L.Thread}
    {record : Dex.StepRecord signature} (isMember : thread ∈ config.threads)
    (carries : StepCarriesThread program thread record) :
    threadOfInput record.input = thread.core.tid := by
  apply threadOfInput_of_carries carries
  intro isBoot
  have isBootPhase : thread.phase = .bootPending ∨ thread.phase = .bootRunning := by
    cases phaseCase : thread.phase <;> simp [phaseCase, isBootPhase] at isBoot ⊢
  obtain ⟨_, _, _, input, isCore⟩ := invariant.bootIsInitial thread isMember isBootPhase
  rw [isCore]

/-- Two threads carried by the same active Step are the same thread. -/
theorem thread_eq_of_carries {program : Program} {lConfig : L.Config}
    (lInvariant : L.Invariant program lConfig) {first second : L.Thread}
    {record : Dex.StepRecord signature} (isFirst : first ∈ lConfig.threads)
    (isSecond : second ∈ lConfig.threads) (firstCarries : StepCarriesThread program first record)
    (secondCarries : StepCarriesThread program second record) : first = second :=
  thread_eq_of_tid lInvariant isFirst isSecond
    ((threadOfInput_of_mem lInvariant isFirst firstCarries).symm.trans
      (threadOfInput_of_mem lInvariant isSecond secondCarries))

/-- Outside the start action, a Step carries a thread by its type, input, and options. -/
theorem stepCarriesThread_iff_of_booted (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (isNotPending : thread.phase ≠ .bootPending)
    (isNotRunning : thread.phase ≠ .bootRunning) :
    StepCarriesThread program thread record ↔
      match thread.timer with
      | some (number, seconds) =>
        record.id = { stepType := StepType.timed, number := number } ∧
        record.input = some (DexVal.timedThread thread.core thread.code seconds)
      | none =>
        record.id.stepType = StepType.point thread.code ∧
        record.input = some (DexVal.thread thread.core) ∧
        record.options.executeLoadMaps = executeLoadMaps thread.core thread.code := by
  unfold StepCarriesThread
  cases phaseCase : thread.phase <;> cases timerCase : thread.timer <;> simp_all

/-- A Step that carries a thread outside the start action has the thread's
ID in its input. -/
theorem threadOfInput_of_carries_of_booted (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (isNotPending : thread.phase ≠ .bootPending)
    (isNotRunning : thread.phase ≠ .bootRunning)
    (carries : StepCarriesThread program thread record) :
    threadOfInput record.input = thread.core.tid := by
  have shape := (stepCarriesThread_iff_of_booted program thread record isNotPending isNotRunning).mp carries
  cases timerCase : thread.timer with
  | none =>
    simp only [timerCase] at shape
    simp [threadOfInput, shape.2.1]
  | some timer =>
    obtain ⟨number, seconds⟩ := timer
    simp only [timerCase] at shape
    simp [threadOfInput, shape.2]

/-- Carrying a thread depends on the thread's state, code, and timer, and on
the Step's ID, input, and options. -/
theorem stepCarriesThread_congr_of_booted (program : Program) (thread changed : L.Thread)
    (record changedRecord : Dex.StepRecord signature)
    (isNotPending : thread.phase ≠ .bootPending) (isNotRunning : thread.phase ≠ .bootRunning)
    (changedNotPending : changed.phase ≠ .bootPending)
    (changedNotRunning : changed.phase ≠ .bootRunning)
    (sameCore : changed.core = thread.core) (sameCode : changed.code = thread.code)
    (sameTimer : changed.timer = thread.timer) (sameId : changedRecord.id = record.id)
    (sameInput : changedRecord.input = record.input)
    (sameOptions : changedRecord.options = record.options)
    (carries : StepCarriesThread program thread record) :
    StepCarriesThread program changed changedRecord := by
  rw [stepCarriesThread_iff_of_booted program changed changedRecord changedNotPending changedNotRunning]
  rw [stepCarriesThread_iff_of_booted program thread record isNotPending isNotRunning] at carries
  rw [sameCore, sameCode, sameTimer, sameId, sameInput, sameOptions]
  exact carries

/-- While a thread is outside the start action, no thread is in it. -/
theorem noBoot_of_other (program : Program) (lConfig : L.Config)
    (lInvariant : L.Invariant program lConfig) (thread other : L.Thread)
    (isThread : thread ∈ lConfig.threads) (isOther : other ∈ lConfig.threads)
    (isDistinct : other ≠ thread) :
    other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning := by
  have notBoot : ¬ (other.phase = .bootPending ∨ other.phase = .bootRunning) := by
    intro isBoot
    obtain ⟨isOnly, _⟩ := lInvariant.bootIsInitial other isOther isBoot
    rw [isOnly] at isThread
    have isSame : thread = other := by simpa using isThread
    exact isDistinct isSame.symm
  exact ⟨fun isPending => notBoot (Or.inl isPending), fun isRunning => notBoot (Or.inr isRunning)⟩

/-- No thread is in the start action while some thread is outside it. -/
theorem noBoot_of_booted (program : Program) (lConfig : L.Config)
    (lInvariant : L.Invariant program lConfig) (thread : L.Thread)
    (isThread : thread ∈ lConfig.threads)
    (isNotBoot : thread.phase ≠ .bootPending ∧ thread.phase ≠ .bootRunning) :
    ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning := by
  intro other isOther
  by_cases isSame : other = thread
  · rw [isSame]
    exact isNotBoot
  · exact noBoot_of_other program lConfig lInvariant thread other isThread isOther isSame

/-- No thread boots while another thread has begun a wait. -/
theorem noBoot_of_waitStarted {program : Program} {lConfig : L.Config}
    (lInvariant : L.Invariant program lConfig) {tid : Tid} {thread : L.Thread}
    (isFound : lConfig.findThread? tid = some thread)
    (isWaitStarted : thread.phase = .waitStarted) :
    ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning := by
  obtain ⟨isMember, _⟩ := mem_of_findThread? isFound
  intro other isOther
  rw [← isBootPhase_eq_false]
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

/-! ## The phase of a thread and its Step -/

/-- A running task that the next configuration keeps still runs the Step. -/
theorem hasRunningTask_transfer {config next : Dex.Config signature}
    {record : Dex.StepRecord signature} {method : Dex.Method}
    {Holds : Dex.Task signature → Prop}
    (keepsTasks : ∀ task, task ∈ config.tasks → task.step = record.id → task ∈ next.tasks)
    (hasTask : HasRunningTask config record method Holds) :
    HasRunningTask next record method Holds := by
  obtain ⟨task, isMember, rest⟩ := hasTask
  exact ⟨task, keepsTasks task isMember rest.2.1, rest⟩

/-- A running task that satisfies more satisfies less. -/
theorem hasRunningTask_imp {config : Dex.Config signature}
    {record : Dex.StepRecord signature} {method : Dex.Method}
    {Holds Weaker : Dex.Task signature → Prop} (isWeaker : ∀ task, Holds task → Weaker task)
    (hasTask : HasRunningTask config record method Holds) :
    HasRunningTask config record method Weaker := by
  obtain ⟨task, isMember, hasInvocation, isStep, isMethod, sameInput, holds⟩ := hasTask
  exact ⟨task, isMember, hasInvocation, isStep, isMethod, sameInput, isWeaker task holds⟩

/-- The phase match depends on the Dex configuration only through the tasks
of the Step, and on the L configuration only through the waiter list that a
running `wait`, `signal`, or `broadcast` read. -/
theorem phaseMatchesStep_congr (program : Program) (lConfig lChanged : L.Config)
    (dexConfig dexChanged : Dex.Config signature) (thread : L.Thread)
    (record : Dex.StepRecord signature)
    (tasksKept : ∀ task, task ∈ dexConfig.tasks → task.step = record.id →
      task ∈ dexChanged.tasks)
    (sameQueues : ∀ loaded, thread.phase = .running loaded → ∀ cv,
      cvOperand thread.core thread.code = some cv →
      lChanged.cvQueues.get? cv = lConfig.cvQueues.get? cv)
    (isMatch : PhaseMatchesStep program lConfig dexConfig thread record) :
    PhaseMatchesStep program lChanged dexChanged thread record := by
  have keepsTask : ∀ method (Holds : Dex.Task signature → Prop),
      HasRunningTask dexConfig record method Holds →
      HasRunningTask dexChanged record method Holds := by
    rintro method Holds ⟨task, isMember, hasInvocation, hasStep, rest⟩
    exact ⟨task, tasksKept task isMember hasStep, hasInvocation, hasStep, rest⟩
  unfold PhaseMatchesStep at isMatch ⊢
  cases phaseCase : thread.phase with
  | running loaded =>
    simp only [phaseCase] at isMatch ⊢
    obtain ⟨isPhase, task, isMember, hasInvocation, hasStep, hasMethod, hasInput, noEffects,
      noResults, isLoaded, sameWaiters⟩ := isMatch
    refine ⟨isPhase, task, tasksKept task isMember hasStep, hasInvocation, hasStep, hasMethod,
      hasInput, noEffects, noResults, isLoaded, ?_⟩
    intro cv isOperand
    rw [sameQueues loaded phaseCase cv isOperand]
    exact sameWaiters cv isOperand
  | bootPending => simpa only [phaseCase] using isMatch
  | bootRunning => simp only [phaseCase] at isMatch ⊢; exact ⟨isMatch.1, keepsTask _ _ isMatch.2⟩
  | ready => simpa only [phaseCase] using isMatch
  | emitting effect =>
    simp only [phaseCase] at isMatch ⊢; exact ⟨isMatch.1, keepsTask _ _ isMatch.2⟩
  | emitted => simp only [phaseCase] at isMatch ⊢; exact ⟨isMatch.1, keepsTask _ _ isMatch.2⟩
  | waitStarted => simp only [phaseCase] at isMatch ⊢; exact ⟨isMatch.1, keepsTask _ _ isMatch.2⟩
  | blocked on since => simpa only [phaseCase] using isMatch
  | woken outcome => simpa only [phaseCase] using isMatch
  | resuming outcome =>
    simp only [phaseCase] at isMatch ⊢; exact ⟨isMatch.1, keepsTask _ _ isMatch.2⟩
  | halted => simp only [phaseCase] at isMatch

/-- `PhaseMatchesStep` reads the L configuration only through the waiter
lists, and the Dex configuration only through the tasks of the Step. -/
theorem phaseMatchesStep_transfer {program : Program} {lConfig lNext : L.Config}
    {config next : Dex.Config signature} {thread : L.Thread}
    {record : Dex.StepRecord signature} (sameQueues : lNext.cvQueues = lConfig.cvQueues)
    (keepsTasks : ∀ task, task ∈ config.tasks → task.step = record.id → task ∈ next.tasks)
    (isMatch : PhaseMatchesStep program lConfig config thread record) :
    PhaseMatchesStep program lNext next thread record :=
  phaseMatchesStep_congr program lConfig lNext config next thread record keepsTasks
    (fun _ _ _ _ => by rw [sameQueues]) isMatch

/-- A Step that waits matches only a blocked thread. -/
theorem blocked_of_phaseMatchesStep (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (thread : L.Thread) (record : Dex.StepRecord signature)
    (isMatch : PhaseMatchesStep program lConfig dexConfig thread record)
    (isWaiting : record.phase = .waiting) :
    ∃ on since, thread.phase = .blocked on since := by
  cases phaseCase : thread.phase <;> simp only [PhaseMatchesStep, phaseCase] at isMatch
  all_goals first
    | exact ⟨_, _, rfl⟩
    | (have isPhase := isMatch.1
       rw [isWaiting] at isPhase
       first
         | cases isPhase
         | (split at isPhase <;> cases isPhase))

/-- A Step with an invocation, or outside `WT`, does not match a blocked thread. -/
theorem waiting_of_phaseMatchesStep (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (thread : L.Thread) (record : Dex.StepRecord signature)
    (on : BlockedOn) (since : Nat) (isBlocked : thread.phase = .blocked on since)
    (isMatch : PhaseMatchesStep program lConfig dexConfig thread record) :
    record.phase = .waiting ∧ record.invocation = none ∧
    ∃ waitState, record.waitState = some waitState ∧
      waitState.wait.kind = (expectedWaitShape thread.core.tid on).1 ∧
      waitState.wait.timerSeconds.length = (expectedWaitShape thread.core.tid on).2.1 ∧
      waitState.wait.channels = (expectedWaitShape thread.core.tid on).2.2 ∧
      waitState.timersDone = waitState.wait.timerSeconds.map (fun _ => false) := by
  unfold PhaseMatchesStep at isMatch
  simpa only [isBlocked] using isMatch

end DexExpressiveness
