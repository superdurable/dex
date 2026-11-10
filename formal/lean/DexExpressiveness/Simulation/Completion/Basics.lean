import DexExpressiveness.Simulation.Relation

/-!
# Basic facts for the completion steps

Lists and maps, thread lookup, the earliest blocked thread, and what
`StepCarriesThread` and `PhaseMatchesStep` depend on.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Sorting -/

/-- The head after an insertion is the new element or the old head. -/
theorem wp3b_head?_insertSorted {α : Type} (isAtMost : α → α → Bool) (newElement : α)
    (elements : List α) :
    (insertSorted isAtMost newElement elements).head? =
      some (match elements.head? with
        | none => newElement
        | some head => if isAtMost newElement head then newElement else head) := by
  cases elements with
  | nil => rfl
  | cons head rest =>
    by_cases isBefore : isAtMost newElement head = true <;> simp [insertSorted, isBefore]

/-- The head of a sorted list is at most every element. -/
theorem wp3b_head?_sortStable_le {α : Type} (isAtMost : α → α → Bool)
    (isReflexive : ∀ element, isAtMost element element = true)
    (isTransitive : ∀ first second third, isAtMost first second = true →
      isAtMost second third = true → isAtMost first third = true)
    (isTotal : ∀ first second, isAtMost first second = true ∨ isAtMost second first = true)
    (elements : List α) (least : α)
    (isHead : (sortStable isAtMost elements).head? = some least) :
    ∀ other, other ∈ elements → isAtMost least other = true := by
  induction elements generalizing least with
  | nil => intro other isMember; cases isMember
  | cons head rest inductionHypothesis =>
    simp only [sortStable] at isHead
    rw [wp3b_head?_insertSorted] at isHead
    cases restCase : (sortStable isAtMost rest).head? with
    | none =>
      simp only [restCase, Option.some.injEq] at isHead
      subst isHead
      intro other isMember
      rcases List.mem_cons.mp isMember with isSame | isInRest
      · subst isSame
        exact isReflexive _
      · have isSorted : other ∈ sortStable isAtMost rest := (mem_sortStable _ _ _).mpr isInRest
        cases sortedCase : sortStable isAtMost rest with
        | nil => rw [sortedCase] at isSorted; cases isSorted
        | cons first later => rw [sortedCase] at restCase; simp at restCase
    | some restLeast =>
      simp only [restCase, Option.some.injEq] at isHead
      have restIsLeast := inductionHypothesis restLeast restCase
      intro other isMember
      by_cases isBefore : isAtMost head restLeast = true
      · simp only [isBefore, ↓reduceIte] at isHead
        subst isHead
        rcases List.mem_cons.mp isMember with isSame | isInRest
        · subst isSame
          exact isReflexive _
        · exact isTransitive _ _ _ isBefore (restIsLeast other isInRest)
      · simp only [isBefore, Bool.false_eq_true, ↓reduceIte] at isHead
        subst isHead
        rcases List.mem_cons.mp isMember with isSame | isInRest
        · subst isSame
          rcases isTotal other restLeast with isFirst | isSecond
          · exact absurd isFirst isBefore
          · exact isSecond
        · exact restIsLeast other isInRest

/-- A sorted list with no head has no element. -/
theorem wp3b_not_mem_of_head?_sortStable_none {α : Type} (isAtMost : α → α → Bool)
    (elements : List α) (isNone : (sortStable isAtMost elements).head? = none) (element : α) :
    element ∉ elements := by
  intro isMember
  have isSorted : element ∈ sortStable isAtMost elements := (mem_sortStable _ _ _).mpr isMember
  cases sortedCase : sortStable isAtMost elements with
  | nil => rw [sortedCase] at isSorted; cases isSorted
  | cons first later => rw [sortedCase] at isNone; simp at isNone

/-! ## Maps -/

namespace AssocList

variable {κ ν : Type} [DecidableEq κ]

/-- Lookup in a map filtered by a predicate on keys. -/
theorem wp3b_get?_filter (isKept : κ → Bool) (map : AssocList κ ν) (wanted : κ) :
    get? (map.filter (fun entry => isKept entry.1)) wanted =
      if isKept wanted then get? map wanted else none := by
  induction map with
  | nil => simp [get?]
  | cons entry rest inductionHypothesis =>
    obtain ⟨key, value⟩ := entry
    by_cases isWanted : key = wanted
    · subst isWanted
      by_cases keepsKey : isKept key = true
      · simp [keepsKey, get?]
      · simp [keepsKey, inductionHypothesis]
    · by_cases keepsKey : isKept key = true
      · simp [keepsKey, get?, isWanted, inductionHypothesis]
      · simp [keepsKey, get?, isWanted, inductionHypothesis]

/-- The key of an entry is bound. -/
theorem wp3b_get?_isSome_of_mem (map : AssocList κ ν) (entry : κ × ν) (isMember : entry ∈ map) :
    ∃ value, get? map entry.1 = some value := by
  induction map with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨key, value⟩ := head
    by_cases isKey : key = entry.1
    · exact ⟨value, by simp [get?, isKey]⟩
    · rcases List.mem_cons.mp isMember with isHead | isInRest
      · subst isHead
        exact absurd rfl isKey
      · obtain ⟨found, isFound⟩ := inductionHypothesis isInRest
        exact ⟨found, by simp [get?, isKey, isFound]⟩

end AssocList

/-! ## Threads -/

/-- What the thread lookup finds is a thread of the configuration with that ID. -/
theorem wp3b_findThread?_some (config : L.Config) (tid : Tid) (thread : L.Thread)
    (isFound : config.findThread? tid = some thread) :
    thread ∈ config.threads ∧ thread.core.tid = tid :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- No thread has the ID exactly when the lookup finds nothing. -/
theorem wp3b_findThread?_none (config : L.Config) (tid : Tid) :
    config.findThread? tid = none ↔ ∀ thread, thread ∈ config.threads → thread.core.tid ≠ tid := by
  unfold L.Config.findThread?
  simp [List.find?_eq_none]

/-- With unique thread IDs, a thread is what the lookup of its ID finds. -/
theorem wp3b_findThread?_of_mem (config : L.Config) (thread : L.Thread)
    (idsNodup : (config.threads.map (fun candidate => candidate.core.tid)).Nodup)
    (isMember : thread ∈ config.threads) : config.findThread? thread.core.tid = some thread := by
  cases foundCase : config.findThread? thread.core.tid with
  | none => exact absurd rfl ((wp3b_findThread?_none config _).mp foundCase thread isMember)
  | some found =>
    obtain ⟨isFoundMember, sameId⟩ := wp3b_findThread?_some config _ found foundCase
    rw [eq_of_nodup_map (fun candidate => candidate.core.tid) config.threads idsNodup
      isFoundMember isMember sameId]

/-- The lookup of an ID depends only on which threads have that ID. -/
theorem wp3b_findThread?_congr (first second : L.Config) (tid : Tid)
    (secondNodup : (second.threads.map (fun candidate => candidate.core.tid)).Nodup)
    (sameThreads : ∀ thread, thread.core.tid = tid →
      (thread ∈ first.threads ↔ thread ∈ second.threads)) :
    first.findThread? tid = second.findThread? tid := by
  cases foundCase : first.findThread? tid with
  | none =>
    symm
    apply (wp3b_findThread?_none second tid).mpr
    intro thread isMember hasId
    exact (wp3b_findThread?_none first tid).mp foundCase thread
      ((sameThreads thread hasId).mpr isMember) hasId
  | some found =>
    obtain ⟨isMember, hasId⟩ := wp3b_findThread?_some first tid found foundCase
    rw [← hasId]
    exact (wp3b_findThread?_of_mem second found secondNodup
      ((sameThreads found hasId).mp isMember)).symm

/-- The threads after an update of one thread. -/
theorem wp3b_mem_updateThread (config : L.Config) (tid : Tid) (change : L.Thread → L.Thread)
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
theorem wp3b_tids_updateThread (config : L.Config) (tid : Tid) (change : L.Thread → L.Thread)
    (keepsId : ∀ candidate, (change candidate).core.tid = candidate.core.tid) :
    (config.updateThread tid change).threads.map (fun thread => thread.core.tid) =
      config.threads.map (fun thread => thread.core.tid) := by
  simp only [L.Config.updateThread, List.map_map]
  apply List.map_congr_left
  intro candidate _
  by_cases isThread : candidate.core.tid = tid <;> simp [isThread, keepsId]

/-! ## The earliest blocked thread -/

/-- No thread is blocked on a wanted wait when `earliestBlocked` finds none. -/
theorem wp3b_earliestBlocked_none (config : L.Config) (isWanted : BlockedOn → Bool)
    (isNone : config.earliestBlocked isWanted = none) (thread : L.Thread)
    (isMember : thread ∈ config.threads) (on : BlockedOn) (since : Nat)
    (isBlocked : thread.phase = .blocked on since) : isWanted on = false := by
  unfold L.Config.earliestBlocked at isNone
  simp only [Option.map_eq_none_iff] at isNone
  cases wantedCase : isWanted on with
  | false => rfl
  | true =>
    exfalso
    refine wp3b_not_mem_of_head?_sortStable_none _ _ isNone (since, thread.core.tid) ?_
    refine List.mem_filterMap.mpr ⟨thread, isMember, ?_⟩
    simp [isBlocked, wantedCase]

/-- The thread `earliestBlocked` finds is blocked on a wanted wait, and no
thread blocked on a wanted wait has a smaller `since`. -/
theorem wp3b_earliestBlocked_some (config : L.Config) (isWanted : BlockedOn → Bool) (next : Tid)
    (isSome : config.earliestBlocked isWanted = some next) :
    ∃ thread on since, thread ∈ config.threads ∧ thread.core.tid = next ∧
      thread.phase = .blocked on since ∧ isWanted on = true ∧
      ∀ other, other ∈ config.threads → ∀ otherOn otherSince,
        other.phase = .blocked otherOn otherSince → isWanted otherOn = true →
        since ≤ otherSince := by
  unfold L.Config.earliestBlocked at isSome
  simp only [Option.map_eq_some_iff] at isSome
  obtain ⟨pair, isHead, isNext⟩ := isSome
  have isLeast := wp3b_head?_sortStable_le
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

/-! ## `StepCarriesThread` -/

/-- Outside the start action, a Step carries a thread by its type, input, and options. -/
theorem wp3b_carries_iff (program : Program) (thread : L.Thread)
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
theorem wp3b_threadOfInput_of_carries (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (isNotPending : thread.phase ≠ .bootPending)
    (isNotRunning : thread.phase ≠ .bootRunning)
    (carries : StepCarriesThread program thread record) :
    threadOfInput record.input = thread.core.tid := by
  have shape := (wp3b_carries_iff program thread record isNotPending isNotRunning).mp carries
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
theorem wp3b_carries_congr (program : Program) (thread changed : L.Thread)
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
  rw [wp3b_carries_iff program changed changedRecord changedNotPending changedNotRunning]
  rw [wp3b_carries_iff program thread record isNotPending isNotRunning] at carries
  rw [sameCore, sameCode, sameTimer, sameId, sameInput, sameOptions]
  exact carries

/-! ## `PhaseMatchesStep` -/

/-- The phase match depends on the Dex configuration only through the tasks
of the Step, and on the L configuration only through the waiter list that a
running `wait`, `signal`, or `broadcast` read. -/
theorem wp3b_phaseMatches_congr (program : Program) (lConfig lChanged : L.Config)
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

/-- A Step that waits matches only a blocked thread. -/
theorem wp3b_blocked_of_waiting (program : Program) (lConfig : L.Config)
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
theorem wp3b_waiting_of_blocked (program : Program) (lConfig : L.Config)
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
