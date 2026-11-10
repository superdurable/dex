import DexExpressiveness.Simulation.Completion.IntentsSync

/-!
# `broadcast(cv)` on the L side

`broadcast` wakes the dequeued waiters one after the other
(`Config.wakeCvWaiter`). `wp3b_broadcast_fold` states the result in closed
form: every thread parked on `cv` whose ID is in the list continues; each
other entry of the list is kept as an early wake.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- Whether a thread waits in the queue of the condition variable for a signal. -/
def parkedOn (cv : CvId) (thread : L.Thread) : Bool :=
  match thread.phase with
  | .blocked (.park parked) _ => decide (parked = cv)
  | _ => false

/-- `parkedOn` as a phase. -/
theorem wp3b_parkedOn_iff (cv : CvId) (thread : L.Thread) :
    parkedOn cv thread = true ↔ ∃ since, thread.phase = .blocked (.park cv) since := by
  unfold parkedOn
  cases phaseCase : thread.phase with
  | blocked on since =>
    cases on with
    | park parked =>
      constructor
      · intro isParked
        have isCv : parked = cv := by simpa using isParked
        exact ⟨since, by rw [isCv]⟩
      · rintro ⟨otherSince, isBlocked⟩
        cases isBlocked
        simp
    | _ => simp
  | _ => simp

open Classical in
/-- 1 when a thread with this ID is parked on `cv` and the ID is in the list:
that thread takes one wake. -/
noncomputable def takenWakes (config : L.Config) (cv : CvId) (waiters : List Tid)
    (waiter : Tid) : Nat :=
  if waiter ∈ waiters ∧
      ∃ thread, thread ∈ config.threads ∧ thread.core.tid = waiter ∧ parkedOn cv thread = true
  then 1 else 0

/-- The two cases of the wake of one dequeued waiter. -/
theorem wp3b_wakeCvWaiter_cases (config : L.Config)
    (idsNodup : (config.threads.map (fun thread => thread.core.tid)).Nodup) (cv : CvId)
    (waiter : Tid) :
    (∃ thread, thread ∈ config.threads ∧ thread.core.tid = waiter ∧
      parkedOn cv thread = true ∧
      config.wakeCvWaiter cv waiter = config.wakeThread waiter .granted) ∨
    ((∀ thread, thread ∈ config.threads → thread.core.tid = waiter →
        parkedOn cv thread = false) ∧
      config.wakeCvWaiter cv waiter =
        { config with cvWakes := config.cvWakes ++ [(cv, waiter)] }) := by
  by_cases isParked : ∃ thread since, config.findThread? waiter = some thread ∧
      thread.phase = .blocked (.park cv) since
  · obtain ⟨thread, since, isFound, isBlocked⟩ := isParked
    obtain ⟨isThread, hasId⟩ := mem_of_findThread? isFound
    left
    exact ⟨thread, isThread, hasId, (wp3b_parkedOn_iff cv thread).mpr ⟨since, isBlocked⟩, by
      simp [L.Config.wakeCvWaiter, isFound, isBlocked]⟩
  · right
    constructor
    · intro thread isThread hasId
      cases parkedCase : parkedOn cv thread with
      | false => rfl
      | true =>
        obtain ⟨since, isBlocked⟩ := (wp3b_parkedOn_iff cv thread).mp parkedCase
        exact absurd ⟨thread, since, by
          rw [← hasId]
          exact findThread?_of_mem_of_nodup config thread idsNodup isThread, isBlocked⟩ isParked
    · cases foundCase : config.findThread? waiter with
      | none => simp [L.Config.wakeCvWaiter, foundCase]
      | some thread =>
        cases phaseCase : thread.phase with
        | blocked on since =>
          cases on with
          | park parkedOn =>
            by_cases isCv : parkedOn = cv
            · subst isCv
              exact absurd ⟨thread, since, foundCase, phaseCase⟩ isParked
            · simp [L.Config.wakeCvWaiter, foundCase, phaseCase, isCv]
          | _ => simp [L.Config.wakeCvWaiter, foundCase, phaseCase]
        | _ => simp [L.Config.wakeCvWaiter, foundCase, phaseCase]

/-- `broadcast` in closed form. -/
theorem wp3b_broadcast_fold (cv : CvId) (waiters : List Tid) (config : L.Config)
    (idsNodup : (config.threads.map (fun thread => thread.core.tid)).Nodup) :
    (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).threads =
      config.threads.map (wakeThreadBy (fun thread =>
        if parkedOn cv thread && waiters.contains thread.core.tid then some .granted
        else none)) ∧
    (∀ waiter,
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).cvWakes.count
          (cv, waiter) + takenWakes config cv waiters waiter =
        config.cvWakes.count (cv, waiter) + waiters.count waiter) ∧
    (∀ otherCv waiter, otherCv ≠ cv →
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).cvWakes.count
          (otherCv, waiter) = config.cvWakes.count (otherCv, waiter)) ∧
    ((waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).shared =
        config.shared ∧
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).cvQueues =
        config.cvQueues ∧
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).inputQueues =
        config.inputQueues ∧
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).mutexOwners =
        config.mutexOwners ∧
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).joinable =
        config.joinable ∧
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).cancelFlags =
        config.cancelFlags) ∧
    (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).timerCounter =
      config.timerCounter ∧
    (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).status =
      config.status ∧
    (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).clock =
      config.clock := by
  induction waiters generalizing config with
  | nil =>
    refine ⟨?_, ?_, fun _ _ _ => rfl, ⟨rfl, rfl, rfl, rfl, rfl, rfl⟩, rfl, rfl, rfl⟩
    · show config.threads = _
      have noThread : wakeThreadBy (fun thread : L.Thread =>
          if parkedOn cv thread && ([] : List Tid).contains thread.core.tid then some .granted
          else none) = id := by
        funext thread
        simp [wakeThreadBy]
      rw [noThread, List.map_id]
    · intro waiter
      simp [takenWakes]
  | cons first rest inductionHypothesis =>
    simp only [List.foldl_cons]
    have sameThread : ∀ left, left ∈ config.threads → ∀ right, right ∈ config.threads →
        left.core.tid = right.core.tid → left = right := fun left isLeft right isRight =>
      eq_of_nodup_map (fun thread => thread.core.tid) config.threads idsNodup isLeft isRight
    rcases wp3b_wakeCvWaiter_cases config idsNodup cv first with
      ⟨parked, isParkedThread, parkedTid, isParked, isNext⟩ | ⟨noParked, isNext⟩
    · -- The waiter has parked: it continues.
      have nextThreads := wp3b_wakeThread_threads config first .granted
      have nextNodup : ((config.wakeThread first .granted).threads.map
          (fun thread => thread.core.tid)).Nodup := by
        rw [nextThreads, List.map_map]
        have sameIds : ((fun thread : L.Thread => thread.core.tid) ∘ wakeThreadBy (fun candidate =>
            if candidate.core.tid = first then some WakeOutcome.granted else none)) =
            fun thread => thread.core.tid := by
          funext thread
          simp only [Function.comp, wakeThreadBy]
          split <;> rfl
        rw [sameIds]
        exact idsNodup
      obtain ⟨restThreads, restTaken, restOther, restData, restCounter, restStatus, restClock⟩ :=
        inductionHypothesis (config.wakeThread first .granted) nextNodup
      rw [isNext]
      have nextParked : ∀ thread, thread ∈ (config.wakeThread first .granted).threads →
          thread.core.tid = first → parkedOn cv thread = false := by
        intro thread isThread hasId
        rw [nextThreads] at isThread
        obtain ⟨original, isOriginal, rfl⟩ := List.mem_map.mp isThread
        have originalTid : original.core.tid = first := by
          simp only [wakeThreadBy] at hasId
          split at hasId <;> exact hasId
        simp [wakeThreadBy, originalTid, parkedOn]
      have otherThreads : ∀ waiter, waiter ≠ first →
          ((∃ thread, thread ∈ (config.wakeThread first .granted).threads ∧
              thread.core.tid = waiter ∧ parkedOn cv thread = true) ↔
            ∃ thread, thread ∈ config.threads ∧ thread.core.tid = waiter ∧
              parkedOn cv thread = true) := by
        intro waiter isOther
        rw [nextThreads]
        constructor
        · rintro ⟨thread, isThread, hasId, isParkedThread⟩
          obtain ⟨original, isOriginal, rfl⟩ := List.mem_map.mp isThread
          by_cases isFirst : original.core.tid = first
          · simp [wakeThreadBy, isFirst, parkedOn] at isParkedThread
          · simp only [wakeThreadBy, isFirst, ↓reduceIte] at hasId isParkedThread
            exact ⟨original, isOriginal, hasId, isParkedThread⟩
        · rintro ⟨thread, isThread, hasId, isParkedThread⟩
          have isNotFirst : thread.core.tid ≠ first := by
            rw [hasId]
            exact isOther
          exact ⟨thread, List.mem_map.mpr ⟨thread, isThread, by
            simp [wakeThreadBy, isNotFirst]⟩, hasId, isParkedThread⟩
      refine ⟨?_, ?_, ?_, restData, restCounter, restStatus, restClock⟩
      · rw [restThreads, nextThreads, List.map_map]
        apply List.map_congr_left
        intro thread isThread
        by_cases isFirst : thread.core.tid = first
        · have isSame := sameThread thread isThread parked isParkedThread
            (isFirst.trans parkedTid.symm)
          subst isSame
          have isWoken : wakeThreadBy (fun candidate =>
              if candidate.core.tid = first then some WakeOutcome.granted else none) thread =
              { thread with phase := .woken .granted } := by
            simp [wakeThreadBy, isFirst]
          have isNotParked : parkedOn cv { thread with phase := .woken .granted } = false := rfl
          simp only [Function.comp]
          rw [isWoken]
          simp [wakeThreadBy, isNotParked, isParked, isFirst]
        · simp [wakeThreadBy, isFirst]
      · intro waiter
        have isRest := restTaken waiter
        have sameWakes : (config.wakeThread first .granted).cvWakes = config.cvWakes := rfl
        rw [sameWakes] at isRest
        by_cases isFirst : waiter = first
        · subst isFirst
          have noneTaken : takenWakes (config.wakeThread waiter .granted) cv rest waiter = 0 := by
            unfold takenWakes
            rw [if_neg]
            rintro ⟨_, thread, isThread, hasId, isParkedThread⟩
            rw [nextParked thread isThread hasId] at isParkedThread
            cases isParkedThread
          have oneTaken : takenWakes config cv (waiter :: rest) waiter = 1 := by
            unfold takenWakes
            rw [if_pos]
            exact ⟨List.mem_cons_self .., parked, isParkedThread, parkedTid, isParked⟩
          rw [noneTaken] at isRest
          rw [oneTaken, List.count_cons_self]
          omega
        · have sameTaken : takenWakes (config.wakeThread first .granted) cv rest waiter =
              takenWakes config cv (first :: rest) waiter := by
            unfold takenWakes
            have sameCondition : (waiter ∈ rest ∧ ∃ thread,
                thread ∈ (config.wakeThread first .granted).threads ∧ thread.core.tid = waiter ∧
                  parkedOn cv thread = true) ↔
                (waiter ∈ first :: rest ∧ ∃ thread, thread ∈ config.threads ∧
                  thread.core.tid = waiter ∧ parkedOn cv thread = true) := by
              rw [otherThreads waiter isFirst]
              simp [isFirst]
            simp only [sameCondition]
          have isOtherHead : ¬ (first = waiter) := fun isSame => isFirst isSame.symm
          rw [sameTaken] at isRest
          rw [List.count_cons, isRest]
          simp [isOtherHead]
      · intro otherCv waiter isOtherCv
        exact restOther otherCv waiter isOtherCv
    · -- The waiter has not parked: the wake waits for it.
      have nextNodup : (({ config with cvWakes := config.cvWakes ++ [(cv, first)] } :
          L.Config).threads.map (fun thread => thread.core.tid)).Nodup := idsNodup
      obtain ⟨restThreads, restTaken, restOther, restData, restCounter, restStatus, restClock⟩ :=
        inductionHypothesis { config with cvWakes := config.cvWakes ++ [(cv, first)] } nextNodup
      rw [isNext]
      refine ⟨?_, ?_, ?_, ?_, restCounter, restStatus, restClock⟩
      · rw [restThreads]
        apply List.map_congr_left
        intro thread isThread
        by_cases isFirst : thread.core.tid = first
        · simp [wakeThreadBy, noParked thread isThread isFirst]
        · simp [wakeThreadBy, isFirst]
      · intro waiter
        have isRest := restTaken waiter
        have nextWakes : (({ config with cvWakes := config.cvWakes ++ [(cv, first)] } :
            L.Config).cvWakes).count (cv, waiter) =
            config.cvWakes.count (cv, waiter) + if first = waiter then 1 else 0 := by
          show (config.cvWakes ++ [(cv, first)]).count (cv, waiter) = _
          rw [List.count_append]
          by_cases isFirst : first = waiter <;> simp [List.count_cons, isFirst]
        rw [nextWakes] at isRest
        by_cases isFirst : first = waiter
        · subst isFirst
          have noneTakenRest : takenWakes
              { config with cvWakes := config.cvWakes ++ [(cv, first)] } cv rest first = 0 := by
            unfold takenWakes
            rw [if_neg]
            rintro ⟨_, thread, isThread, hasId, isParkedThread⟩
            rw [noParked thread isThread hasId] at isParkedThread
            cases isParkedThread
          have noneTaken : takenWakes config cv (first :: rest) first = 0 := by
            unfold takenWakes
            rw [if_neg]
            rintro ⟨_, thread, isThread, hasId, isParkedThread⟩
            rw [noParked thread isThread hasId] at isParkedThread
            cases isParkedThread
          rw [noneTakenRest] at isRest
          rw [noneTaken, List.count_cons_self]
          simp only [↓reduceIte] at isRest
          omega
        · have sameTaken : takenWakes { config with cvWakes := config.cvWakes ++ [(cv, first)] }
              cv rest waiter = takenWakes config cv (first :: rest) waiter := by
            unfold takenWakes
            have isOther : ¬ (waiter = first) := fun isSame => isFirst isSame.symm
            have sameCondition : (waiter ∈ rest ∧ ∃ thread,
                thread ∈ ({ config with cvWakes := config.cvWakes ++ [(cv, first)] } :
                  L.Config).threads ∧ thread.core.tid = waiter ∧ parkedOn cv thread = true) ↔
                (waiter ∈ first :: rest ∧ ∃ thread, thread ∈ config.threads ∧
                  thread.core.tid = waiter ∧ parkedOn cv thread = true) := by
              simp [isOther]
            simp only [sameCondition]
          rw [sameTaken] at isRest
          rw [List.count_cons]
          simp only [isFirst, ↓reduceIte] at isRest
          simp only [show (first == waiter) = false by simp [isFirst]]
          simpa using isRest
      · intro otherCv waiter isOtherCv
        rw [restOther otherCv waiter isOtherCv]
        show (config.cvWakes ++ [(cv, first)]).count (otherCv, waiter) = _
        rw [List.count_append]
        have isOtherPair : ¬ ((cv, first) = (otherCv, waiter)) := by
          intro isSame
          cases isSame
          exact isOtherCv rfl
        simp [List.count_cons, isOtherPair]
      · exact restData

end DexExpressiveness
