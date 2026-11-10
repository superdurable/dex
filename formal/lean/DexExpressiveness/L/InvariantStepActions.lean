import DexExpressiveness.L.InvariantStepFrame

/-!
# Helper lemmas for work package 1: the atomic actions

Each operation of `L/Semantics.lean` keeps `L.Invariant`: `setPhase`,
`wakeThread`, `blockThread`, `releaseMutex`, `wakeCvWaiter`,
`cancelBlockedThread`, `applyIntent`, `registerWait`, `deliverInput`, and
`fireTimers`. `L/InvariantStep.lean` puts them together.
-/

namespace DexExpressiveness.L

namespace Config

/-- The thread that `findThread?` returns is live and has the wanted ID. -/
theorem mem_of_findThread? {config : Config} {tid : Tid} {thread : Thread}
    (isFound : config.findThread? tid = some thread) :
    thread ∈ config.threads ∧ thread.core.tid = tid :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- The thread that `earliestBlocked` returns is live and blocked. -/
theorem earliestBlocked_some {config : Config} {isWanted : BlockedOn → Bool} {tid : Tid}
    (isEarliest : config.earliestBlocked isWanted = some tid) :
    ∃ thread, thread ∈ config.threads ∧ thread.core.tid = tid ∧
      ∃ on since, thread.phase = .blocked on since := by
  simp only [earliestBlocked, Option.map_eq_some_iff] at isEarliest
  obtain ⟨candidate, isHead, isTid⟩ := isEarliest
  have isSorted := List.mem_of_mem_head? (Option.mem_def.mpr isHead)
  rw [mem_sortStable, List.mem_filterMap] at isSorted
  obtain ⟨thread, isMember, isCandidate⟩ := isSorted
  refine ⟨thread, isMember, ?_⟩
  split at isCandidate
  · rename_i on since isBlocked
    split at isCandidate
    · simp only [Option.some.injEq] at isCandidate
      rw [← isCandidate] at isTid
      exact ⟨isTid, on, since, isBlocked⟩
    · cases isCandidate
  · cases isCandidate

/-- The thread that `leastDueTimerThread` returns is live and blocked. -/
theorem leastDueTimerThread_some {config : Config} {tid : Tid}
    (isLeast : config.leastDueTimerThread = some tid) :
    ∃ thread, thread ∈ config.threads ∧ thread.core.tid = tid ∧
      ∃ on since, thread.phase = .blocked on since := by
  simp only [leastDueTimerThread, Option.map_eq_some_iff] at isLeast
  obtain ⟨candidate, isHead, isTid⟩ := isLeast
  have isSorted := List.mem_of_mem_head? (Option.mem_def.mpr isHead)
  rw [mem_sortStable, List.mem_filterMap] at isSorted
  obtain ⟨thread, isMember, isCandidate⟩ := isSorted
  refine ⟨thread, isMember, ?_⟩
  split at isCandidate
  · rename_i number deadline chan since isBlocked
    split at isCandidate
    · simp only [Option.some.injEq] at isCandidate
      rw [← isCandidate] at isTid
      exact ⟨isTid, _, since, isBlocked⟩
    · cases isCandidate
  · cases isCandidate

end Config

/-- What `Invariant.applyIntent` needs from the result of a thread-local step. -/
structure IsSoundResult (config : Config) (thread : Thread) (result : StatementResult) :
    Prop where
  /-- The thread ID stays. -/
  tid_eq : result.core.tid = thread.core.tid
  /-- The counter does not shrink. -/
  counter_le : thread.core.freshCounter ≤ result.core.freshCounter
  /-- A forked thread is named by the old counter, which is then used up. -/
  fork : ∀ child childCode, result.intent = .fork child childCode →
    child.tid = thread.core.tid ++ [thread.core.freshCounter] ∧
    result.core.freshCounter = thread.core.freshCounter + 1
  /-- A created mutex is named by the old counter, which is then used up. -/
  newMutex : ∀ mutex, result.intent = .newMutex mutex →
    mutex = .dynamic thread.core.tid thread.core.freshCounter ∧
    result.core.freshCounter = thread.core.freshCounter + 1
  /-- The result holds valid handles only, once the created mutex exists. -/
  isValid : ∀ witness : Config, config.IsExtendedBy witness →
    (∀ mutex, result.intent = .newMutex mutex → witness.IsExistingMutex mutex) →
    witness.IsValidResult result
  /-- The thread holds the mutex it unlocks. -/
  unlock : ∀ mutex, result.intent = .unlock mutex →
    config.mutexOwners.get? mutex = some (some thread.core.tid)
  /-- The thread holds the mutex of the condition variable it waits on. -/
  cvWait : ∀ cv, result.intent = .cvWait cv →
    config.mutexOwners.get? cv.mutex = some (some thread.core.tid)

/-- A result that asks for no action on shared state, except the end or the
halt of the thread, is sound when its state and code hold valid handles. -/
theorem IsSoundResult.of_quiet {config : Config} {thread : Thread} {result : StatementResult}
    (tid_eq : result.core.tid = thread.core.tid)
    (counter_eq : result.core.freshCounter = thread.core.freshCounter)
    (isQuiet : result.intent = .silent ∨ result.intent = .endThread ∨ result.intent = .halt)
    (isValid : ∀ witness : Config, config.IsExtendedBy witness →
      witness.IsValidState result.core result.code) :
    IsSoundResult config thread result :=
  { tid_eq := tid_eq
    counter_le := Nat.le_of_eq counter_eq.symm
    fork := fun child childCode isIntent => by
      rcases isQuiet with isQuiet | isQuiet | isQuiet <;> rw [isQuiet] at isIntent <;>
        cases isIntent
    newMutex := fun mutex isIntent => by
      rcases isQuiet with isQuiet | isQuiet | isQuiet <;> rw [isQuiet] at isIntent <;>
        cases isIntent
    isValid := fun witness isExtended _ =>
      ⟨isValid witness isExtended,
        (fun target value isIntent => by
          rcases isQuiet with isQuiet | isQuiet | isQuiet <;> rw [isQuiet] at isIntent <;>
            cases isIntent),
        (fun child childCode isIntent => by
          rcases isQuiet with isQuiet | isQuiet | isQuiet <;> rw [isQuiet] at isIntent <;>
            cases isIntent)⟩
    unlock := fun mutex isIntent => by
      rcases isQuiet with isQuiet | isQuiet | isQuiet <;> rw [isQuiet] at isIntent <;>
        cases isIntent
    cvWait := fun cv isIntent => by
      rcases isQuiet with isQuiet | isQuiet | isQuiet <;> rw [isQuiet] at isIntent <;>
        cases isIntent }

namespace Invariant

variable {program : Program} {config : Config}

/-- `updateThread` replaces the one thread with the given ID. -/
theorem threads_updateThread (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (change : Thread → Thread) :
    (config.updateThread thread.core.tid change).threads =
      config.threads.map (fun candidate =>
        if candidate.core.tid = thread.core.tid then change thread else candidate) := by
  simp only [Config.updateThread]
  apply List.map_congr_left
  intro candidate isCandidate
  split
  · rename_i isTid
    rw [invariant.eq_of_tid_eq isCandidate isMember isTid]
  · rfl

/-- The stamp of an action keeps the invariant. -/
theorem stamp (invariant : Invariant program config) : Invariant program config.stamp :=
  invariant.of_sameFields rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl

/-- Only condition variable queues, early wakes, or cancel flags changed. -/
theorem of_sameCore (invariant : Invariant program config) {next : Config}
    (isStarted : config.status ≠ .notStarted)
    (status_eq : next.status = config.status) (threads_eq : next.threads = config.threads)
    (shared_eq : next.shared = config.shared)
    (mutexOwners_eq : next.mutexOwners = config.mutexOwners)
    (inputQueues_eq : next.inputQueues = config.inputQueues)
    (joinable_eq : next.joinable = config.joinable)
    (blockCounter_eq : next.blockCounter = config.blockCounter)
    (timerCounter_eq : next.timerCounter = config.timerCounter) : Invariant program next :=
  invariant.changeFields isStarted status_eq threads_eq
    (by intro mutex; rw [mutexOwners_eq])
    (by intro _ _ _ _ mutex _; rw [mutexOwners_eq])
    (by rw [shared_eq]; exact invariant.handlesExist.2.1)
    (by rw [inputQueues_eq]; exact invariant.handlesExist.2.2)
    (by rw [joinable_eq]; exact invariant.joinableNodup)
    (by rw [joinable_eq]; exact fun _ isEnded => isEnded)
    (Nat.le_of_eq blockCounter_eq.symm) (Nat.le_of_eq timerCounter_eq.symm)

/-- A shared write of a valid value keeps the invariant. -/
theorem setShared (invariant : Invariant program config) (isStarted : config.status ≠ .notStarted)
    (target : Global) (value : Val) (valueValid : config.IsValidVal value) :
    Invariant program { config with shared := config.shared.set target value } :=
  invariant.changeFields isStarted rfl rfl (fun _ => rfl) (fun _ _ _ _ _ _ => rfl)
    (Config.isValidVal_of_mem_set config _ _ _ invariant.handlesExist.2.1 valueValid)
    invariant.handlesExist.2.2 invariant.joinableNodup (fun _ isEnded => isEnded)
    (Nat.le_refl _) (Nat.le_refl _)

/-- The queued messages of a channel are valid. -/
theorem pendingInput_valid (invariant : Invariant program config) (chan : ExtChan) :
    ∀ message, message ∈ config.pendingInput chan → config.IsValidVal message := by
  intro message isMessage
  simp only [Config.pendingInput] at isMessage
  cases isQueue : AssocList.get? config.inputQueues chan with
  | none =>
    rw [isQueue] at isMessage
    cases isMessage
  | some queue =>
    rw [isQueue] at isMessage
    exact invariant.handlesExist.2.2 _ (mem_of_get? _ _ _ isQueue) message isMessage

/-- Replacing the queue of a channel by valid messages keeps the invariant. -/
theorem setInputQueue (invariant : Invariant program config)
    (isStarted : config.status ≠ .notStarted) (chan : ExtChan) (messages : List Val)
    (messagesValid : ∀ message, message ∈ messages → config.IsValidVal message) :
    Invariant program { config with inputQueues := config.inputQueues.set chan messages } := by
  refine invariant.changeFields isStarted rfl rfl (fun _ => rfl) (fun _ _ _ _ _ _ => rfl)
    invariant.handlesExist.2.1 ?_ invariant.joinableNodup (fun _ isEnded => isEnded)
    (Nat.le_refl _) (Nat.le_refl _)
  intro entry isEntry message isMessage
  rcases AssocList.mem_set config.inputQueues chan messages entry isEntry with isOld | isNew
  · exact invariant.handlesExist.2.2 entry isOld message isMessage
  · rw [isNew] at isMessage
    exact messagesValid message isMessage

/-- Changing the owner of an existing mutex keeps the invariant, when no
running thread requires that mutex. -/
theorem setMutexOwner (invariant : Invariant program config)
    (isStarted : config.status ≠ .notStarted) (mutex : MutexId) (owner : Option Tid)
    (isExisting : config.mutexOwners.contains mutex = true)
    (notRequired : ∀ thread, thread ∈ config.threads → ∀ loaded,
      thread.phase = .running loaded → requiredMutex thread.core thread.code ≠ some mutex) :
    Invariant program { config with mutexOwners := config.mutexOwners.set mutex owner } := by
  refine invariant.changeFields isStarted rfl rfl
    (fun other => contains_set_of_existing _ _ _ _ isExisting) ?_
    invariant.handlesExist.2.1 invariant.handlesExist.2.2 invariant.joinableNodup
    (fun _ isEnded => isEnded) (Nat.le_refl _) (Nat.le_refl _)
  intro thread isMember loaded isPhase required isRequired
  have isOther : required ≠ mutex := by
    intro isSame
    rw [isSame] at isRequired
    exact notRequired thread isMember loaded isPhase isRequired
  exact AssocList.get?_set_other _ _ _ _ isOther

/-- A change of one thread's phase keeps the invariant. -/
theorem setPhase (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (phase : Phase)
    (isValid : config.IsValidPhase phase)
    (boot : phase = .bootPending ∨ phase = .bootRunning →
      thread.phase = .bootPending ∨ thread.phase = .bootRunning)
    (notBlocked : ∀ on since, phase ≠ .blocked on since)
    (fits : ({ thread with phase := phase } : Thread).PhaseFitsCode)
    (holds : ∀ loaded, phase = .running loaded → ∀ mutex,
      requiredMutex thread.core thread.code = some mutex →
      config.mutexOwners.get? mutex = some (some thread.core.tid)) :
    Invariant program (config.setPhase thread.core.tid phase) := by
  have threadValid := invariant.handlesExist.1 thread isMember
  exact invariant.replaceThread (newThread := { thread with phase := phase }) isMember
    (invariant.threads_updateThread isMember _) rfl rfl rfl rfl (fun _ _ => rfl)
    (fun _ _ isContained => Or.inl isContained) (Nat.le_refl _) (Nat.le_refl _) rfl
    (Nat.le_refl _) ⟨threadValid.1, threadValid.2.1, threadValid.2.2.1, isValid⟩
    (fun isBoot => ⟨boot isBoot, rfl, rfl, rfl⟩)
    (fun on since isBlocked => absurd isBlocked (notBlocked on since))
    (fun number seconds isTimer => by
      have isCounted := invariant.timerNumbersCounted thread isMember number seconds isTimer
      exact ⟨isCounted.1, isCounted.2, Or.inl ⟨seconds, isTimer⟩⟩)
    fits holds

/-- Deciding the wait of a thread keeps the invariant. -/
theorem wakeThread (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (outcome : WakeOutcome)
    (isWaiting : thread.phase = .waitStarted ∨ ∃ on since, thread.phase = .blocked on since)
    (isValid : config.IsValidPhase (.woken outcome)) :
    Invariant program (config.wakeThread thread.core.tid outcome) := by
  refine invariant.setPhase isMember (.woken outcome) isValid
    (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
    (fun on since isBlocked => by cases isBlocked) ?_ (fun loaded isPhase => by cases isPhase)
  show thread.timer.isSome = true ∨ isBlockingHead thread.code = true
  have fits := invariant.phasesFitCode thread isMember
  rcases isWaiting with isPhase | ⟨on, since, isPhase⟩
  · simpa only [Thread.PhaseFitsCode, isPhase] using fits
  · exact thread.timer_or_blockingHead_of_blocked on since fits isPhase

/-- Blocking a thread with the next `since` stamp keeps the invariant. -/
theorem blockThread (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (on : BlockedOn)
    (isValid : config.IsValidPhase (.blocked on config.blockCounter))
    (fits : ({ thread with phase := .blocked on config.blockCounter } : Thread).PhaseFitsCode) :
    Invariant program (config.blockThread thread.core.tid on) := by
  have threadValid := invariant.handlesExist.1 thread isMember
  refine invariant.replaceThread
    (newThread := { thread with phase := .blocked on config.blockCounter }) isMember
    (invariant.threads_updateThread isMember _) rfl rfl rfl rfl (fun _ _ => rfl)
    (fun _ _ isContained => Or.inl isContained) (Nat.le_succ _) (Nat.le_refl _) rfl
    (Nat.le_refl _) ⟨threadValid.1, threadValid.2.1, threadValid.2.2.1, isValid⟩
    (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
    ?_
    (fun number seconds isTimer => by
      have isCounted := invariant.timerNumbersCounted thread isMember number seconds isTimer
      exact ⟨isCounted.1, isCounted.2, Or.inl ⟨seconds, isTimer⟩⟩)
    fits (fun loaded isPhase => by cases isPhase)
  intro newOn since isBlocked
  cases isBlocked
  exact ⟨Nat.lt_succ_self _, Or.inr (Nat.le_refl _)⟩

/-- Releasing an existing mutex keeps the invariant, when no running thread
requires it. -/
theorem releaseMutex (invariant : Invariant program config)
    (isStarted : config.status ≠ .notStarted) (mutex : MutexId)
    (isExisting : config.mutexOwners.contains mutex = true)
    (notRequired : ∀ thread, thread ∈ config.threads → ∀ loaded,
      thread.phase = .running loaded → requiredMutex thread.core thread.code ≠ some mutex) :
    Invariant program (config.releaseMutex mutex) := by
  simp only [Config.releaseMutex]
  split
  · rename_i next isEarliest
    obtain ⟨waiter, isWaiterMember, isTid, on, since, isBlocked⟩ :=
      Config.earliestBlocked_some isEarliest
    rw [← isTid]
    exact (invariant.setMutexOwner isStarted mutex _ isExisting notRequired).wakeThread
      (thread := waiter) isWaiterMember .granted (Or.inr ⟨on, since, isBlocked⟩) trivial
  · exact invariant.setMutexOwner isStarted mutex none isExisting notRequired

/-- Waking a waiter of a condition variable keeps the invariant. -/
theorem wakeCvWaiter (invariant : Invariant program config)
    (isStarted : config.status ≠ .notStarted) (cv : CvId) (waiter : Tid) :
    Invariant program (config.wakeCvWaiter cv waiter) ∧
      (config.wakeCvWaiter cv waiter).status = config.status := by
  have queued : Invariant program { config with cvWakes := config.cvWakes ++ [(cv, waiter)] } :=
    invariant.of_sameCore isStarted rfl rfl rfl rfl rfl rfl rfl rfl
  simp only [Config.wakeCvWaiter]
  split
  · rename_i thread isFound
    obtain ⟨isMember, isTid⟩ := Config.mem_of_findThread? isFound
    split
    · rename_i parkedOn since isPhase
      split
      · rw [← isTid]
        exact ⟨invariant.wakeThread isMember .granted (Or.inr ⟨_, _, isPhase⟩) trivial, rfl⟩
      · exact ⟨queued, rfl⟩
    · exact ⟨queued, rfl⟩
  · exact ⟨queued, rfl⟩

/-- A broadcast keeps the invariant. -/
theorem foldl_wakeCvWaiter (cv : CvId) (waiters : List Tid) :
    ∀ config : Config, Invariant program config → config.status ≠ .notStarted →
      Invariant program
        (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config) := by
  induction waiters with
  | nil =>
    intro config invariant _
    exact invariant
  | cons waiter laterWaiters inductionHypothesis =>
    intro config invariant isStarted
    obtain ⟨woken, sameStatus⟩ := invariant.wakeCvWaiter isStarted cv waiter
    exact inductionHypothesis _ woken (by rw [sameStatus]; exact isStarted)

/-- Delivering a cancel to a blocked thread keeps the invariant. -/
theorem cancelBlockedThread (invariant : Invariant program config) (target : Tid) :
    Invariant program (config.cancelBlockedThread target) := by
  simp only [Config.cancelBlockedThread]
  split
  · rename_i thread isFound
    obtain ⟨isMember, isTid⟩ := Config.mem_of_findThread? isFound
    split
    all_goals first
      | exact invariant
      | (rename_i isPhase
         rw [← isTid]
         exact invariant.wakeThread isMember .canceled (Or.inr ⟨_, _, isPhase⟩) trivial)
  · exact invariant

/-- The result of completing a statement is sound, when the thread holds the
mutex that the statement requires. -/
theorem isSoundResult_completeStatement (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (loaded : Val) (loadedValid : config.IsValidVal loaded)
    (holds : ∀ mutex, requiredMutex thread.core thread.code = some mutex →
      config.mutexOwners.get? mutex = some (some thread.core.tid)) :
    IsSoundResult config thread (completeStatement program thread.core thread.code loaded) := by
  obtain ⟨tid_eq, counter_le, fork, newMutex⟩ :=
    completeStatement_shape program thread.core thread.code loaded
  exact
    { tid_eq := tid_eq
      counter_le := counter_le
      fork := fork
      newMutex := newMutex
      isValid := fun witness isExtended createdExists =>
        Config.isValidResult_completeStatement program witness thread.core thread.code loaded
          (isExtended.isValidState (invariant.handlesExist.1 thread isMember).isValidState)
          (isExtended.isValidVal loadedValid) createdExists
      unlock := fun mutex isIntent => by
        apply holds
        rw [requiredMutex_eq_of_intent program thread.core thread.code loaded, isIntent]
      cvWait := fun cv isIntent => by
        apply holds
        rw [requiredMutex_eq_of_intent program thread.core thread.code loaded, isIntent] }

/-- The result of continuing after a wait is sound. -/
theorem isSoundResult_resume (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (outcome : WakeOutcome)
    (outcomeValid : config.IsValidPhase (.resuming outcome)) :
    IsSoundResult config thread
      (if thread.timer.isSome then resumeTimed thread.core thread.code outcome
       else resumeBlocked thread.core thread.code outcome) := by
  have stateValid := (invariant.handlesExist.1 thread isMember).isValidState
  split
  · obtain ⟨tid_eq, counter_eq, isQuiet⟩ := resumeTimed_shape thread.core thread.code outcome
    exact IsSoundResult.of_quiet tid_eq counter_eq isQuiet (fun witness isExtended =>
      Config.isValidState_resumeTimed witness _ _ outcome (isExtended.isValidState stateValid)
        (isExtended.isValidPhase outcomeValid))
  · obtain ⟨tid_eq, counter_eq, isQuiet⟩ := resumeBlocked_shape thread.core thread.code outcome
    exact IsSoundResult.of_quiet tid_eq counter_eq isQuiet (fun witness isExtended =>
      Config.isValidState_resumeBlocked witness _ _ outcome (isExtended.isValidState stateValid)
        (isExtended.isValidPhase outcomeValid))

/-- Only the condition variable queues changed. -/
theorem setCvQueues (invariant : Invariant program config)
    (isStarted : config.status ≠ .notStarted) (queues : AssocList CvId (List Tid)) :
    Invariant program { config with cvQueues := queues } :=
  invariant.of_sameCore isStarted rfl rfl rfl rfl rfl rfl rfl rfl

/-- Only the cancel flags changed. -/
theorem setCancelFlags (invariant : Invariant program config)
    (isStarted : config.status ≠ .notStarted) (flags : List Tid) :
    Invariant program { config with cancelFlags := flags } :=
  invariant.of_sameCore isStarted rfl rfl rfl rfl rfl rfl rfl rfl

/-- Only the early wakes changed. -/
theorem setCvWakes (invariant : Invariant program config)
    (isStarted : config.status ≠ .notStarted) (wakes : List (CvId × Tid)) :
    Invariant program { config with cvWakes := wakes } :=
  invariant.of_sameCore isStarted rfl rfl rfl rfl rfl rfl rfl rfl

/-- Removing a joinable thread keeps the invariant. -/
theorem eraseJoinable (invariant : Invariant program config)
    (isStarted : config.status ≠ .notStarted) (target : Tid) :
    Invariant program { config with joinable := config.joinable.erase target } :=
  invariant.changeFields isStarted rfl rfl (fun _ => rfl) (fun _ _ _ _ _ _ => rfl)
    invariant.handlesExist.2.1 invariant.handlesExist.2.2 (invariant.joinableNodup.erase target)
    (fun _ isEnded => List.mem_of_mem_erase isEnded) (Nat.le_refl _) (Nat.le_refl _)

end Invariant

/-- The thread after a statement completed: at its next statement, ready. -/
def Thread.continueWith (thread : Thread) (result : StatementResult) : Thread :=
  { thread with core := result.core, code := result.code, timer := none, phase := .ready }

/-- The configuration after a statement completed, before its atomic action:
`continued` of `Config.applyIntent`. -/
def Config.continueThread (config : Config) (thread : Tid) (result : StatementResult) : Config :=
  config.updateThread thread (fun current =>
    { current with core := result.core, code := result.code, timer := none, phase := .ready })

namespace Invariant

variable {program : Program} {config : Config}

/-- A thread of the continued configuration is the continued thread or
another old thread. -/
theorem mem_continueThread (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (result : StatementResult) {member : Thread}
    (isNextMember : member ∈ (config.continueThread thread.core.tid result).threads) :
    member = thread.continueWith result ∨
      (member ∈ config.threads ∧ member.core.tid ≠ thread.core.tid) := by
  rw [Config.continueThread, invariant.threads_updateThread isMember] at isNextMember
  exact mem_replaceThread isNextMember

/-- Moving a thread to its next statement keeps the invariant, unless the
statement creates a mutex. -/
theorem continueThread (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (result : StatementResult)
    (isSound : IsSoundResult config thread result)
    (notNew : ∀ mutex, result.intent ≠ .newMutex mutex) :
    Invariant program (config.continueThread thread.core.tid result) := by
  have resultValid := (isSound.isValid (config.continueThread thread.core.tid result)
    (fun _ isContained => isContained) (fun mutex isNew => absurd isNew (notNew mutex))).1
  exact invariant.replaceThread (newThread := thread.continueWith result) isMember
    (invariant.threads_updateThread isMember _) rfl rfl rfl rfl (fun _ _ => rfl)
    (fun _ _ isContained => Or.inl isContained) (Nat.le_refl _) (Nat.le_refl _) isSound.tid_eq
    isSound.counter_le ⟨resultValid.1, resultValid.2.1, resultValid.2.2, trivial⟩
    (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
    (fun on since isBlocked => by cases isBlocked)
    (fun number seconds isTimer => by cases isTimer)
    trivial (fun loaded isPhase => by cases isPhase)

/-- Performing the atomic action of a sound result keeps the invariant. -/
theorem applyIntent (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) (result : StatementResult)
    (isSound : IsSoundResult config thread result) :
    Invariant program (config.applyIntent thread.core.tid result).2 := by
  have isRunning := invariant.status_of_mem isMember
  have isStarted : config.status ≠ .notStarted := by
    rw [isRunning]
    intro isStatus
    cases isStatus
  have continuedStarted : (config.continueThread thread.core.tid result).status ≠ .notStarted :=
    isStarted
  have notRequired : ∀ mutex, config.mutexOwners.get? mutex = some (some thread.core.tid) →
      ∀ member, member ∈ (config.continueThread thread.core.tid result).threads → ∀ loaded,
      member.phase = .running loaded → requiredMutex member.core member.code ≠ some mutex := by
    intro mutex isHeld member isNextMember loaded isPhase isRequired
    rcases invariant.mem_continueThread isMember result isNextMember with isNew | ⟨isOld, isOther⟩
    · rw [isNew] at isPhase
      cases isPhase
    · have isHeldByMember := invariant.runningHoldsRequiredMutex member isOld loaded isPhase mutex
        isRequired
      rw [isHeld] at isHeldByMember
      simp only [Option.some.injEq] at isHeldByMember
      exact isOther isHeldByMember.symm
  cases isIntent : result.intent with
  | silent =>
    simp only [Config.applyIntent, isIntent]
    exact invariant.continueThread isMember result isSound
      (by rw [isIntent]; intro mutex isNew; cases isNew)
  | store target value =>
    simp only [Config.applyIntent, isIntent]
    have notNew : ∀ mutex, result.intent ≠ .newMutex mutex := by
      rw [isIntent]; intro mutex isNew; cases isNew
    exact (invariant.continueThread isMember result isSound notNew).setShared continuedStarted
      target value ((isSound.isValid (config.continueThread thread.core.tid result)
        (fun _ isContained => isContained)
        (fun mutex isNew => absurd isNew (notNew mutex))).2.1 target value isIntent)
  | fork child childCode =>
    simp only [Config.applyIntent, isIntent]
    have notNew : ∀ mutex, result.intent ≠ .newMutex mutex := by
      rw [isIntent]; intro mutex isNew; cases isNew
    obtain ⟨isChildTid, isCounter⟩ := isSound.fork child childCode isIntent
    have childValid := (isSound.isValid (config.continueThread thread.core.tid result)
      (fun _ isContained => isContained)
      (fun mutex isNew => absurd isNew (notNew mutex))).2.2 child childCode isIntent
    have continuedTid : (thread.continueWith result).core.tid = thread.core.tid := isSound.tid_eq
    have continuedCounter : (thread.continueWith result).core.freshCounter =
        thread.core.freshCounter + 1 := isCounter
    refine (invariant.continueThread isMember result isSound notNew).appendThread
      { core := child, code := childCode, timer := none, phase := .ready } isRunning rfl rfl rfl
      rfl rfl rfl rfl rfl ?_ ?_ ?_ ?_ ?_ childValid rfl rfl
    · intro member isNextMember
      rcases invariant.mem_continueThread isMember result isNextMember with isNew | ⟨isOld, isOther⟩
      · rw [isNew]
        intro isBoot
        rcases isBoot with isBoot | isBoot <;> cases isBoot
      · exact invariant.not_boot_of_ne isMember isOld isOther
    · intro member isNextMember isPrefix
      have isChildPrefix : (thread.core.tid ++ [thread.core.freshCounter]) <+: member.core.tid := by
        rw [← isChildTid]
        exact isPrefix
      rcases invariant.mem_continueThread isMember result isNextMember with isNew | ⟨isOld, _⟩
      · rw [isNew, continuedTid] at isChildPrefix
        exact not_concat_prefix_self _ _ isChildPrefix
      · exact invariant.threadIdsFresh thread isMember member isOld _ (Nat.le_refl _) isChildPrefix
    · intro member isNextMember index isLater isPrefix
      have isChildPrefix : (member.core.tid ++ [index]) <+:
          (thread.core.tid ++ [thread.core.freshCounter]) := by
        rw [← isChildTid]
        exact isPrefix
      rw [List.prefix_concat_iff] at isChildPrefix
      rcases invariant.mem_continueThread isMember result isNextMember with isNew | ⟨isOld, isOther⟩
      · rw [isNew, continuedTid] at isChildPrefix
        rw [isNew, continuedCounter] at isLater
        rcases isChildPrefix with isSame | isShorter
        · have sameIndex := (concat_inj isSame).2
          omega
        · exact not_concat_prefix_self _ _ isShorter
      · rcases isChildPrefix with isSame | isShorter
        · exact isOther (concat_inj isSame).1
        · exact invariant.threadIdsFresh member isOld thread isMember index isLater isShorter
    · intro ended isEnded isPrefix
      have isChildPrefix : (thread.core.tid ++ [thread.core.freshCounter]) <+: ended := by
        rw [← isChildTid]
        exact isPrefix
      exact (invariant.joinableFresh thread isMember).2 ended isEnded _ (Nat.le_refl _)
        isChildPrefix
    · intro creator index isContained isPrefix
      have isChildPrefix : (thread.core.tid ++ [thread.core.freshCounter]) <+: creator := by
        rw [← isChildTid]
        exact isPrefix
      exact (invariant.mutexesFresh thread isMember creator index isContained).2 _ (Nat.le_refl _)
        isChildPrefix
  | newMutex mutex =>
    simp only [Config.applyIntent, isIntent]
    obtain ⟨isMutex, isCounter⟩ := isSound.newMutex mutex isIntent
    have notContained : config.mutexOwners.contains mutex ≠ true := by
      intro isContained
      rw [isMutex] at isContained
      have isBelow := (invariant.mutexesFresh thread isMember _ _ isContained).1 rfl
      omega
    have resultValid := (isSound.isValid
      { config.continueThread thread.core.tid result with
        mutexOwners := config.mutexOwners.set mutex none }
      (fun other isContained => contains_set_of_contains _ _ _ _ isContained)
      (fun created isCreated => by
        rw [isIntent] at isCreated
        cases isCreated
        rw [isMutex]
        exact contains_set_self _ _ _)).1
    exact invariant.replaceThread (newThread := thread.continueWith result) isMember
      (invariant.threads_updateThread isMember _) rfl rfl rfl rfl
      (fun other isContained => AssocList.get?_set_other _ _ _ _ (by
        intro isSame
        rw [isSame] at isContained
        exact notContained isContained))
      (fun creator index isContained => by
        rcases (contains_set_iff _ _ _ _).mp isContained with isNew | isOld
        · rw [isMutex] at isNew
          simp only [MutexId.dynamic.injEq] at isNew
          refine Or.inr ⟨isNew.1, ?_⟩
          show index < result.core.freshCounter
          omega
        · exact Or.inl isOld)
      (Nat.le_refl _) (Nat.le_refl _) isSound.tid_eq isSound.counter_le
      ⟨resultValid.1, resultValid.2.1, resultValid.2.2, trivial⟩
      (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
      (fun on since isBlocked => by cases isBlocked)
      (fun number seconds isTimer => by cases isTimer)
      trivial (fun loaded isPhase => by cases isPhase)
  | unlock mutex =>
    simp only [Config.applyIntent, isIntent]
    have isHeld := isSound.unlock mutex isIntent
    exact (invariant.continueThread isMember result isSound
      (by rw [isIntent]; intro created isNew; cases isNew)).releaseMutex continuedStarted mutex
      (contains_of_get? _ _ _ isHeld) (notRequired mutex isHeld)
  | cvWait cv =>
    simp only [Config.applyIntent, isIntent]
    have isHeld := isSound.cvWait cv isIntent
    exact ((invariant.continueThread isMember result isSound
      (by rw [isIntent]; intro created isNew; cases isNew)).setCvQueues continuedStarted
        (config.cvQueues.set cv ((config.cvQueues.get? cv).getD [] ++ [thread.core.tid]))
        ).releaseMutex continuedStarted cv.mutex (contains_of_get? _ _ _ isHeld)
      (notRequired cv.mutex isHeld)
  | signal cv =>
    simp only [Config.applyIntent, isIntent]
    have continued := invariant.continueThread isMember result isSound
      (by rw [isIntent]; intro created isNew; cases isNew)
    split
    · exact continued
    · rename_i waiter laterWaiters _
      exact ((continued.setCvQueues continuedStarted
        (config.cvQueues.set cv laterWaiters)).wakeCvWaiter continuedStarted cv waiter).1
  | broadcast cv =>
    simp only [Config.applyIntent, isIntent]
    exact foldl_wakeCvWaiter cv ((config.cvQueues.get? cv).getD []) _
      ((invariant.continueThread isMember result isSound
        (by rw [isIntent]; intro created isNew; cases isNew)).setCvQueues continuedStarted
        (config.cvQueues.set cv [])) continuedStarted
  | cancel target =>
    simp only [Config.applyIntent, isIntent]
    exact ((invariant.continueThread isMember result isSound
      (by rw [isIntent]; intro created isNew; cases isNew)).setCancelFlags continuedStarted
        (if config.cancelFlags.contains target then config.cancelFlags
         else config.cancelFlags ++ [target])).cancelBlockedThread target
  | enterTimed seconds =>
    simp only [Config.applyIntent, isIntent]
    have resultValid := (isSound.isValid config (Config.IsExtendedBy.refl config)
      (fun mutex isNew => by rw [isIntent] at isNew; cases isNew)).1
    exact invariant.replaceThread
      (newThread := { thread with
        core := result.core, code := result.code
        timer := some (config.timerCounter + 1, seconds), phase := .ready }) isMember
      (invariant.threads_updateThread isMember _) rfl rfl rfl rfl (fun _ _ => rfl)
      (fun _ _ isContained => Or.inl isContained) (Nat.le_refl _) (Nat.le_succ _) isSound.tid_eq
      isSound.counter_le ⟨resultValid.1, resultValid.2.1, resultValid.2.2, trivial⟩
      (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
      (fun on since isBlocked => by cases isBlocked)
      (fun number timerSeconds isTimer => by
        cases isTimer
        exact ⟨Nat.succ_pos _, Nat.le_refl _, Or.inr (Nat.lt_succ_self _)⟩)
      trivial (fun loaded isPhase => by cases isPhase)
  | endThread =>
    simp only [Config.applyIntent, isIntent]
    have removed : Invariant program
        { config with
          threads := config.threads.filter
            (fun candidate => !decide (candidate.core.tid = thread.core.tid)) } :=
      invariant.removeThread thread.core.tid isStarted rfl rfl rfl rfl rfl invariant.joinableNodup
        (fun _ isEnded => Or.inl isEnded) rfl rfl
    split
    · rename_i joiner isEarliest
      obtain ⟨waiter, isWaiterMember, isTid, on, since, isBlocked⟩ :=
        Config.earliestBlocked_some isEarliest
      rw [← isTid]
      exact removed.wakeThread isWaiterMember .granted (Or.inr ⟨on, since, isBlocked⟩) trivial
    · refine invariant.removeThread thread.core.tid isStarted rfl rfl rfl rfl rfl ?_ ?_ rfl rfl
      · show (config.joinable ++ [thread.core.tid]).Nodup
        rw [List.nodup_append]
        refine ⟨invariant.joinableNodup, by simp, ?_⟩
        intro ended isEnded other isOther sameTid
        rw [List.mem_singleton] at isOther
        rw [sameTid, isOther] at isEnded
        exact (invariant.joinableFresh thread isMember).1 isEnded
      · intro ended isEnded
        rcases List.mem_append.mp isEnded with isOld | isNew
        · exact Or.inl isOld
        · exact Or.inr ⟨List.mem_singleton.mp isNew, thread, isMember, rfl⟩
  | exit value =>
    simp only [Config.applyIntent, isIntent]
    exact invariant.closeProgram
  | fail message =>
    simp only [Config.applyIntent, isIntent]
    exact invariant.closeProgram
  | halt =>
    simp only [Config.applyIntent, isIntent]
    exact invariant.setPhase isMember .halted trivial
      (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
      (fun on since isBlocked => by cases isBlocked) trivial
      (fun loaded isPhase => by cases isPhase)

end Invariant

end DexExpressiveness.L
