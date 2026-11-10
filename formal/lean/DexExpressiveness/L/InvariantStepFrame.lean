import DexExpressiveness.L.InvariantStepLocal

/-!
# Helper lemmas for work package 1: the invariant under one change

Every L transition is a short sequence of changes to a configuration. Each
lemma here keeps `L.Invariant` across one kind of change, stated for any
`next` configuration that differs from `config` in the named fields only:

- `Invariant.of_sameFields`: only the clock, the stamp, or the late emits change;
- `Invariant.changeFields`: the thread list stays; shared variables, input
  queues, mutex owners, the joinable list, or the counters change;
- `Invariant.replaceThread`: one thread changes, and it may create a mutex;
- `Invariant.removeThread`: one thread ends;
- `Invariant.appendThread`: a forked thread starts;
- `Invariant.closeProgram`, `Invariant.begun`: the program ends or begins.
-/

namespace DexExpressiveness.L

/-- A member of a thread list with one thread replaced is the new thread or an
old thread with another ID. -/
theorem mem_replaceThread {threads : List Thread} {tid : Tid} {newThread member : Thread}
    (isMember : member ∈ threads.map (fun candidate =>
      if candidate.core.tid = tid then newThread else candidate)) :
    member = newThread ∨ (member ∈ threads ∧ member.core.tid ≠ tid) := by
  obtain ⟨candidate, isCandidate, isImage⟩ := List.mem_map.mp isMember
  by_cases isTid : candidate.core.tid = tid
  · simp only [isTid, ↓reduceIte] at isImage
    exact Or.inl isImage.symm
  · simp only [isTid, ↓reduceIte] at isImage
    rw [← isImage]
    exact Or.inr ⟨isCandidate, isTid⟩

namespace Invariant

variable {program : Program} {config : Config}

/-- Two live threads with the same ID are the same thread. -/
theorem eq_of_tid_eq (invariant : Invariant program config) {first second : Thread}
    (isFirstMember : first ∈ config.threads) (isSecondMember : second ∈ config.threads)
    (sameTid : first.core.tid = second.core.tid) : first = second :=
  eq_of_nodup_map (fun thread : Thread => thread.core.tid) config.threads
    invariant.threadIdsNodup isFirstMember isSecondMember sameTid

/-- A configuration with a live thread is running. -/
theorem status_of_mem (invariant : Invariant program config) {thread : Thread}
    (isMember : thread ∈ config.threads) : config.status = .running := by
  cases isStatus : config.status with
  | notStarted =>
    rw [(invariant.notStartedIsInitial isStatus).1] at isMember
    cases isMember
  | running => rfl
  | closed =>
    rw [invariant.closedHasNoThread isStatus] at isMember
    cases isMember

/-- While some other thread lives, no thread boots. -/
theorem not_boot_of_ne (invariant : Invariant program config) {thread other : Thread}
    (isMember : thread ∈ config.threads) (isOtherMember : other ∈ config.threads)
    (isOther : other.core.tid ≠ thread.core.tid) :
    ¬ (other.phase = .bootPending ∨ other.phase = .bootRunning) := by
  intro isBoot
  have isOnly := (invariant.bootIsInitial other isOtherMember isBoot).1
  rw [isOnly] at isMember
  rw [List.mem_singleton.mp isMember] at isOther
  exact isOther rfl

/-- Only the clock, the stamp, or the late emits changed. -/
theorem of_sameFields (invariant : Invariant program config) {next : Config}
    (status_eq : next.status = config.status) (threads_eq : next.threads = config.threads)
    (shared_eq : next.shared = config.shared)
    (mutexOwners_eq : next.mutexOwners = config.mutexOwners)
    (cvQueues_eq : next.cvQueues = config.cvQueues) (cvWakes_eq : next.cvWakes = config.cvWakes)
    (inputQueues_eq : next.inputQueues = config.inputQueues)
    (cancelFlags_eq : next.cancelFlags = config.cancelFlags)
    (joinable_eq : next.joinable = config.joinable)
    (blockCounter_eq : next.blockCounter = config.blockCounter)
    (timerCounter_eq : next.timerCounter = config.timerCounter) : Invariant program next := by
  have isExtended : config.IsExtendedBy next := Config.IsExtendedBy.of_eq mutexOwners_eq
  exact
    { notStartedIsInitial := by
        rw [status_eq, threads_eq, shared_eq, mutexOwners_eq, cvQueues_eq, cvWakes_eq,
          inputQueues_eq, cancelFlags_eq, joinable_eq, timerCounter_eq]
        exact invariant.notStartedIsInitial
      closedHasNoThread := by
        rw [status_eq, threads_eq]
        exact invariant.closedHasNoThread
      threadIdsNodup := by
        rw [threads_eq]
        exact invariant.threadIdsNodup
      threadIdsFresh := by
        rw [threads_eq]
        exact invariant.threadIdsFresh
      joinableNodup := by
        rw [joinable_eq]
        exact invariant.joinableNodup
      joinableFresh := by
        rw [threads_eq, joinable_eq]
        exact invariant.joinableFresh
      mutexesFresh := by
        rw [threads_eq, mutexOwners_eq]
        exact invariant.mutexesFresh
      handlesExist := by
        rw [threads_eq, shared_eq, inputQueues_eq]
        exact ⟨fun thread isMember =>
            isExtended.isValidThread (invariant.handlesExist.1 thread isMember),
          fun entry isMember => isExtended.isValidVal (invariant.handlesExist.2.1 entry isMember),
          fun entry isMember message isMessage =>
            isExtended.isValidVal (invariant.handlesExist.2.2 entry isMember message isMessage)⟩
      bootIsInitial := by
        rw [threads_eq]
        exact invariant.bootIsInitial
      sinceBelowCounter := by
        rw [threads_eq, blockCounter_eq]
        exact invariant.sinceBelowCounter
      sinceInjective := by
        rw [threads_eq]
        exact invariant.sinceInjective
      timerNumbersCounted := by
        rw [threads_eq, timerCounter_eq]
        exact invariant.timerNumbersCounted
      timerNumbersInjective := by
        rw [threads_eq]
        exact invariant.timerNumbersInjective
      phasesFitCode := by
        rw [threads_eq]
        exact invariant.phasesFitCode
      runningHoldsRequiredMutex := by
        rw [threads_eq, mutexOwners_eq]
        exact invariant.runningHoldsRequiredMutex }

/-- The thread list stays. The shared variables and input queues hold valid
values, the same mutexes exist, the mutexes that running threads require keep
their owners, the joinable list shrinks, and the counters grow. -/
theorem changeFields (invariant : Invariant program config) {next : Config}
    (isStarted : config.status ≠ .notStarted)
    (status_eq : next.status = config.status) (threads_eq : next.threads = config.threads)
    (owners_contains : ∀ mutex,
      next.mutexOwners.contains mutex = config.mutexOwners.contains mutex)
    (owners_held : ∀ thread, thread ∈ config.threads → ∀ loaded,
      thread.phase = .running loaded → ∀ mutex,
      requiredMutex thread.core thread.code = some mutex →
      next.mutexOwners.get? mutex = config.mutexOwners.get? mutex)
    (shared_valid : ∀ entry, entry ∈ next.shared → config.IsValidVal entry.2)
    (inputs_valid : ∀ entry, entry ∈ next.inputQueues → ∀ message, message ∈ entry.2 →
      config.IsValidVal message)
    (joinable_nodup : next.joinable.Nodup)
    (joinable_sub : ∀ ended, ended ∈ next.joinable → ended ∈ config.joinable)
    (blockCounter_le : config.blockCounter ≤ next.blockCounter)
    (timerCounter_le : config.timerCounter ≤ next.timerCounter) : Invariant program next := by
  have isExtended : config.IsExtendedBy next := fun mutex isContained => by
    rw [owners_contains]
    exact isContained
  exact
    { notStartedIsInitial := by
        intro isNotStarted
        rw [status_eq] at isNotStarted
        exact absurd isNotStarted isStarted
      closedHasNoThread := by
        rw [status_eq, threads_eq]
        exact invariant.closedHasNoThread
      threadIdsNodup := by
        rw [threads_eq]
        exact invariant.threadIdsNodup
      threadIdsFresh := by
        rw [threads_eq]
        exact invariant.threadIdsFresh
      joinableNodup := joinable_nodup
      joinableFresh := by
        rw [threads_eq]
        intro thread isMember
        have isFresh := invariant.joinableFresh thread isMember
        exact ⟨fun isJoinable => isFresh.1 (joinable_sub _ isJoinable),
          fun ended isEnded => isFresh.2 ended (joinable_sub ended isEnded)⟩
      mutexesFresh := by
        rw [threads_eq]
        intro thread isMember creator index isContained
        rw [owners_contains] at isContained
        exact invariant.mutexesFresh thread isMember creator index isContained
      handlesExist := by
        rw [threads_eq]
        exact ⟨fun thread isMember =>
            isExtended.isValidThread (invariant.handlesExist.1 thread isMember),
          fun entry isMember => isExtended.isValidVal (shared_valid entry isMember),
          fun entry isMember message isMessage =>
            isExtended.isValidVal (inputs_valid entry isMember message isMessage)⟩
      bootIsInitial := by
        rw [threads_eq]
        exact invariant.bootIsInitial
      sinceBelowCounter := by
        rw [threads_eq]
        intro thread isMember on since isBlocked
        exact Nat.lt_of_lt_of_le (invariant.sinceBelowCounter thread isMember on since isBlocked)
          blockCounter_le
      sinceInjective := by
        rw [threads_eq]
        exact invariant.sinceInjective
      timerNumbersCounted := by
        rw [threads_eq]
        intro thread isMember number seconds isTimer
        have isCounted := invariant.timerNumbersCounted thread isMember number seconds isTimer
        exact ⟨isCounted.1, Nat.le_trans isCounted.2 timerCounter_le⟩
      timerNumbersInjective := by
        rw [threads_eq]
        exact invariant.timerNumbersInjective
      phasesFitCode := by
        rw [threads_eq]
        exact invariant.phasesFitCode
      runningHoldsRequiredMutex := by
        rw [threads_eq]
        intro thread isMember loaded isRunning mutex isRequired
        rw [owners_held thread isMember loaded isRunning mutex isRequired]
        exact invariant.runningHoldsRequiredMutex thread isMember loaded isRunning mutex
          isRequired }

/-- One thread changes. The new thread keeps its ID, its counter does not
shrink, and its handles are valid. It may create mutexes named by its own ID
and a counter value it has used up. A new `since` stamp or timer number is
beyond the old counter. -/
theorem replaceThread (invariant : Invariant program config) {next : Config}
    {thread newThread : Thread} (isMember : thread ∈ config.threads)
    (threads_eq : next.threads = config.threads.map (fun candidate =>
      if candidate.core.tid = thread.core.tid then newThread else candidate))
    (status_eq : next.status = config.status) (shared_eq : next.shared = config.shared)
    (inputQueues_eq : next.inputQueues = config.inputQueues)
    (joinable_eq : next.joinable = config.joinable)
    (owners_kept : ∀ mutex, config.mutexOwners.contains mutex = true →
      next.mutexOwners.get? mutex = config.mutexOwners.get? mutex)
    (owners_new : ∀ creator index, next.mutexOwners.contains (.dynamic creator index) = true →
      config.mutexOwners.contains (.dynamic creator index) = true ∨
      (creator = thread.core.tid ∧ index < newThread.core.freshCounter))
    (blockCounter_le : config.blockCounter ≤ next.blockCounter)
    (timerCounter_le : config.timerCounter ≤ next.timerCounter)
    (tid_eq : newThread.core.tid = thread.core.tid)
    (counter_le : thread.core.freshCounter ≤ newThread.core.freshCounter)
    (isValid : next.IsValidThread newThread)
    (boot : newThread.phase = .bootPending ∨ newThread.phase = .bootRunning →
      (thread.phase = .bootPending ∨ thread.phase = .bootRunning) ∧
      newThread.core = thread.core ∧ newThread.code = thread.code ∧
      newThread.timer = thread.timer)
    (since_ok : ∀ on since, newThread.phase = .blocked on since →
      since < next.blockCounter ∧
      ((∃ oldOn, thread.phase = .blocked oldOn since) ∨ config.blockCounter ≤ since))
    (timer_ok : ∀ number seconds, newThread.timer = some (number, seconds) →
      0 < number ∧ number ≤ next.timerCounter ∧
      ((∃ oldSeconds, thread.timer = some (number, oldSeconds)) ∨ config.timerCounter < number))
    (fits : newThread.PhaseFitsCode)
    (holds : ∀ loaded, newThread.phase = .running loaded → ∀ mutex,
      requiredMutex newThread.core newThread.code = some mutex →
      next.mutexOwners.get? mutex = some (some newThread.core.tid)) :
    Invariant program next := by
  have isExtended : config.IsExtendedBy next := fun mutex isContained => by
    have isKept := owners_kept mutex isContained
    simp only [AssocList.contains] at isContained ⊢
    rw [isKept]
    exact isContained
  have memCases : ∀ member, member ∈ next.threads →
      member = newThread ∨ (member ∈ config.threads ∧ member.core.tid ≠ thread.core.tid) :=
    fun member isNextMember => mem_replaceThread (threads_eq ▸ isNextMember)
  have isRunning := invariant.status_of_mem isMember
  exact
    { notStartedIsInitial := by
        intro isNotStarted
        rw [status_eq, isRunning] at isNotStarted
        cases isNotStarted
      closedHasNoThread := by
        intro isClosed
        rw [status_eq, isRunning] at isClosed
        cases isClosed
      threadIdsNodup := by
        have sameIds : next.threads.map (fun thread => thread.core.tid) =
            config.threads.map (fun thread => thread.core.tid) := by
          rw [threads_eq, List.map_map]
          apply List.map_congr_left
          intro candidate _
          simp only [Function.comp]
          split
          · rename_i isTid
            rw [tid_eq, isTid]
          · rfl
        rw [sameIds]
        exact invariant.threadIdsNodup
      threadIdsFresh := by
        intro first isFirstMember second isSecondMember index isLater isPrefix
        rcases memCases first isFirstMember with isNewFirst | ⟨isOldFirst, _⟩ <;>
          rcases memCases second isSecondMember with isNewSecond | ⟨isOldSecond, _⟩
        · rw [isNewFirst, isNewSecond] at isPrefix
          exact not_concat_prefix_self _ _ isPrefix
        · rw [isNewFirst, tid_eq] at isPrefix
          rw [isNewFirst] at isLater
          exact invariant.threadIdsFresh thread isMember second isOldSecond index
            (Nat.le_trans counter_le isLater) isPrefix
        · rw [isNewSecond, tid_eq] at isPrefix
          exact invariant.threadIdsFresh first isOldFirst thread isMember index isLater isPrefix
        · exact invariant.threadIdsFresh first isOldFirst second isOldSecond index isLater isPrefix
      joinableNodup := by
        rw [joinable_eq]
        exact invariant.joinableNodup
      joinableFresh := by
        intro member isNextMember
        rw [joinable_eq]
        rcases memCases member isNextMember with isNew | ⟨isOld, _⟩
        · have isFresh := invariant.joinableFresh thread isMember
          rw [isNew, tid_eq]
          exact ⟨isFresh.1, fun ended isEnded index isLater =>
            isFresh.2 ended isEnded index (Nat.le_trans counter_le isLater)⟩
        · exact invariant.joinableFresh member isOld
      mutexesFresh := by
        intro member isNextMember creator index isContained
        rcases owners_new creator index isContained with isOldMutex | ⟨isCreator, isBelow⟩ <;>
          rcases memCases member isNextMember with isNew | ⟨isOld, isOther⟩
        · have isFresh := invariant.mutexesFresh thread isMember creator index isOldMutex
          rw [isNew, tid_eq]
          exact ⟨fun sameCreator => Nat.lt_of_lt_of_le (isFresh.1 sameCreator) counter_le,
            fun later isLater => isFresh.2 later (Nat.le_trans counter_le isLater)⟩
        · exact invariant.mutexesFresh member isOld creator index isOldMutex
        · rw [isNew, tid_eq, isCreator]
          exact ⟨fun _ => isBelow, fun later _ => not_concat_prefix_self _ _⟩
        · rw [isCreator]
          exact ⟨fun sameTid => absurd sameTid.symm isOther,
            fun later isLater => invariant.threadIdsFresh member isOld thread isMember later isLater⟩
      handlesExist := by
        refine ⟨?_, ?_, ?_⟩
        · intro member isNextMember
          rcases memCases member isNextMember with isNew | ⟨isOld, _⟩
          · rw [isNew]
            exact isValid
          · exact isExtended.isValidThread (invariant.handlesExist.1 member isOld)
        · rw [shared_eq]
          exact fun entry isEntry => isExtended.isValidVal (invariant.handlesExist.2.1 entry isEntry)
        · rw [inputQueues_eq]
          exact fun entry isEntry message isMessage =>
            isExtended.isValidVal (invariant.handlesExist.2.2 entry isEntry message isMessage)
      bootIsInitial := by
        intro member isNextMember isBoot
        rcases memCases member isNextMember with isNew | ⟨isOld, isOther⟩
        · rw [isNew] at isBoot ⊢
          obtain ⟨wasBoot, sameCore, sameCode, sameTimer⟩ := boot isBoot
          obtain ⟨isOnly, noTimer, isCode, input, isCore⟩ :=
            invariant.bootIsInitial thread isMember wasBoot
          refine ⟨?_, by rw [sameTimer]; exact noTimer, by rw [sameCode]; exact isCode, input,
            by rw [sameCore]; exact isCore⟩
          rw [threads_eq, isOnly]
          simp
        · exact absurd isBoot (invariant.not_boot_of_ne isMember isOld isOther)
      sinceBelowCounter := by
        intro member isNextMember on since isBlocked
        rcases memCases member isNextMember with isNew | ⟨isOld, _⟩
        · rw [isNew] at isBlocked
          exact (since_ok on since isBlocked).1
        · exact Nat.lt_of_lt_of_le (invariant.sinceBelowCounter member isOld on since isBlocked)
            blockCounter_le
      sinceInjective := by
        have newOld : ∀ other, other ∈ config.threads → other.core.tid ≠ thread.core.tid →
            ∀ newOn otherOn since, newThread.phase = .blocked newOn since →
            other.phase = .blocked otherOn since → False := by
          intro other isOtherMember isOther newOn otherOn since isNewBlocked isOtherBlocked
          rcases (since_ok newOn since isNewBlocked).2 with ⟨oldOn, wasBlocked⟩ | isBeyond
          · have isSame := invariant.sinceInjective thread isMember other isOtherMember oldOn
              otherOn since wasBlocked isOtherBlocked
            exact isOther (by rw [isSame])
          · have isBelow := invariant.sinceBelowCounter other isOtherMember otherOn since
              isOtherBlocked
            omega
        intro first isFirstMember second isSecondMember firstOn secondOn since isFirstBlocked
          isSecondBlocked
        rcases memCases first isFirstMember with isNewFirst | ⟨isOldFirst, isOtherFirst⟩ <;>
          rcases memCases second isSecondMember with isNewSecond | ⟨isOldSecond, isOtherSecond⟩
        · rw [isNewFirst, isNewSecond]
        · rw [isNewFirst] at isFirstBlocked
          exact (newOld second isOldSecond isOtherSecond _ _ _ isFirstBlocked isSecondBlocked).elim
        · rw [isNewSecond] at isSecondBlocked
          exact (newOld first isOldFirst isOtherFirst _ _ _ isSecondBlocked isFirstBlocked).elim
        · exact invariant.sinceInjective first isOldFirst second isOldSecond firstOn secondOn since
            isFirstBlocked isSecondBlocked
      timerNumbersCounted := by
        intro member isNextMember number seconds isTimer
        rcases memCases member isNextMember with isNew | ⟨isOld, _⟩
        · rw [isNew] at isTimer
          have isCounted := timer_ok number seconds isTimer
          exact ⟨isCounted.1, isCounted.2.1⟩
        · have isCounted := invariant.timerNumbersCounted member isOld number seconds isTimer
          exact ⟨isCounted.1, Nat.le_trans isCounted.2 timerCounter_le⟩
      timerNumbersInjective := by
        have newOld : ∀ other, other ∈ config.threads → other.core.tid ≠ thread.core.tid →
            ∀ number newSeconds otherSeconds, newThread.timer = some (number, newSeconds) →
            other.timer = some (number, otherSeconds) → False := by
          intro other isOtherMember isOther number newSeconds otherSeconds isNewTimer isOtherTimer
          rcases (timer_ok number newSeconds isNewTimer).2.2 with ⟨oldSeconds, wasTimer⟩ | isBeyond
          · have isSame := invariant.timerNumbersInjective thread isMember other isOtherMember
              number oldSeconds otherSeconds wasTimer isOtherTimer
            exact isOther (by rw [isSame])
          · have isCounted := invariant.timerNumbersCounted other isOtherMember number otherSeconds
              isOtherTimer
            omega
        intro first isFirstMember second isSecondMember number firstSeconds secondSeconds
          isFirstTimer isSecondTimer
        rcases memCases first isFirstMember with isNewFirst | ⟨isOldFirst, isOtherFirst⟩ <;>
          rcases memCases second isSecondMember with isNewSecond | ⟨isOldSecond, isOtherSecond⟩
        · rw [isNewFirst, isNewSecond]
        · rw [isNewFirst] at isFirstTimer
          exact (newOld second isOldSecond isOtherSecond _ _ _ isFirstTimer isSecondTimer).elim
        · rw [isNewSecond] at isSecondTimer
          exact (newOld first isOldFirst isOtherFirst _ _ _ isSecondTimer isFirstTimer).elim
        · exact invariant.timerNumbersInjective first isOldFirst second isOldSecond number
            firstSeconds secondSeconds isFirstTimer isSecondTimer
      phasesFitCode := by
        intro member isNextMember
        rcases memCases member isNextMember with isNew | ⟨isOld, _⟩
        · rw [isNew]
          exact fits
        · exact invariant.phasesFitCode member isOld
      runningHoldsRequiredMutex := by
        intro member isNextMember loaded isPhase mutex isRequired
        rcases memCases member isNextMember with isNew | ⟨isOld, _⟩
        · rw [isNew] at isPhase isRequired ⊢
          exact holds loaded isPhase mutex isRequired
        · have isHeld := invariant.runningHoldsRequiredMutex member isOld loaded isPhase mutex
            isRequired
          rw [owners_kept mutex (contains_of_get? _ _ _ isHeld)]
          exact isHeld }

/-- One thread ends. Its ID may become joinable. -/
theorem removeThread (invariant : Invariant program config) {next : Config} (tid : Tid)
    (isStarted : config.status ≠ .notStarted)
    (threads_eq : next.threads =
      config.threads.filter (fun candidate => !decide (candidate.core.tid = tid)))
    (status_eq : next.status = config.status) (shared_eq : next.shared = config.shared)
    (mutexOwners_eq : next.mutexOwners = config.mutexOwners)
    (inputQueues_eq : next.inputQueues = config.inputQueues)
    (joinable_nodup : next.joinable.Nodup)
    (joinable_mem : ∀ ended, ended ∈ next.joinable → ended ∈ config.joinable ∨
      (ended = tid ∧ ∃ thread, thread ∈ config.threads ∧ thread.core.tid = tid))
    (blockCounter_eq : next.blockCounter = config.blockCounter)
    (timerCounter_eq : next.timerCounter = config.timerCounter) : Invariant program next := by
  have isExtended : config.IsExtendedBy next := Config.IsExtendedBy.of_eq mutexOwners_eq
  have memCases : ∀ member, member ∈ next.threads →
      member ∈ config.threads ∧ member.core.tid ≠ tid := by
    intro member isNextMember
    rw [threads_eq] at isNextMember
    simpa using isNextMember
  exact
    { notStartedIsInitial := by
        intro isNotStarted
        rw [status_eq] at isNotStarted
        exact absurd isNotStarted isStarted
      closedHasNoThread := by
        intro isClosed
        rw [status_eq] at isClosed
        rw [threads_eq, invariant.closedHasNoThread isClosed]
        rfl
      threadIdsNodup := by
        rw [threads_eq]
        exact List.Nodup.sublist (List.Sublist.map _ List.filter_sublist) invariant.threadIdsNodup
      threadIdsFresh := by
        intro first isFirstMember second isSecondMember
        exact invariant.threadIdsFresh first (memCases first isFirstMember).1 second
          (memCases second isSecondMember).1
      joinableNodup := joinable_nodup
      joinableFresh := by
        intro member isNextMember
        obtain ⟨isOld, isOther⟩ := memCases member isNextMember
        have isFresh := invariant.joinableFresh member isOld
        refine ⟨?_, ?_⟩
        · intro isJoinable
          rcases joinable_mem _ isJoinable with wasJoinable | ⟨isEnded, _⟩
          · exact isFresh.1 wasJoinable
          · exact isOther isEnded
        · intro ended isJoinable index isLater
          rcases joinable_mem _ isJoinable with wasJoinable | ⟨isEnded, endedThread, isEndedMember,
            isEndedTid⟩
          · exact isFresh.2 ended wasJoinable index isLater
          · rw [isEnded, ← isEndedTid]
            exact invariant.threadIdsFresh member isOld endedThread isEndedMember index isLater
      mutexesFresh := by
        intro member isNextMember
        rw [mutexOwners_eq]
        exact invariant.mutexesFresh member (memCases member isNextMember).1
      handlesExist := by
        rw [shared_eq, inputQueues_eq]
        exact ⟨fun member isNextMember => isExtended.isValidThread
            (invariant.handlesExist.1 member (memCases member isNextMember).1),
          fun entry isEntry => isExtended.isValidVal (invariant.handlesExist.2.1 entry isEntry),
          fun entry isEntry message isMessage =>
            isExtended.isValidVal (invariant.handlesExist.2.2 entry isEntry message isMessage)⟩
      bootIsInitial := by
        intro member isNextMember isBoot
        obtain ⟨isOld, isOther⟩ := memCases member isNextMember
        obtain ⟨isOnly, noTimer, isCode, isCore⟩ := invariant.bootIsInitial member isOld isBoot
        refine ⟨?_, noTimer, isCode, isCore⟩
        rw [threads_eq, isOnly]
        simp [isOther]
      sinceBelowCounter := by
        intro member isNextMember
        rw [blockCounter_eq]
        exact invariant.sinceBelowCounter member (memCases member isNextMember).1
      sinceInjective := by
        intro first isFirstMember second isSecondMember
        exact invariant.sinceInjective first (memCases first isFirstMember).1 second
          (memCases second isSecondMember).1
      timerNumbersCounted := by
        intro member isNextMember
        rw [timerCounter_eq]
        exact invariant.timerNumbersCounted member (memCases member isNextMember).1
      timerNumbersInjective := by
        intro first isFirstMember second isSecondMember
        exact invariant.timerNumbersInjective first (memCases first isFirstMember).1 second
          (memCases second isSecondMember).1
      phasesFitCode := fun member isNextMember =>
        invariant.phasesFitCode member (memCases member isNextMember).1
      runningHoldsRequiredMutex := by
        intro member isNextMember
        rw [mutexOwners_eq]
        exact invariant.runningHoldsRequiredMutex member (memCases member isNextMember).1 }

/-- A forked thread starts. Its ID is fresh: no live thread, joinable thread,
or mutex creator extends it, and it is not a next child of another thread. -/
theorem appendThread (invariant : Invariant program config) {next : Config} (child : Thread)
    (isRunning : config.status = .running)
    (threads_eq : next.threads = config.threads ++ [child])
    (status_eq : next.status = config.status) (shared_eq : next.shared = config.shared)
    (mutexOwners_eq : next.mutexOwners = config.mutexOwners)
    (inputQueues_eq : next.inputQueues = config.inputQueues)
    (joinable_eq : next.joinable = config.joinable)
    (blockCounter_eq : next.blockCounter = config.blockCounter)
    (timerCounter_eq : next.timerCounter = config.timerCounter)
    (noBoot : ∀ thread, thread ∈ config.threads →
      ¬ (thread.phase = .bootPending ∨ thread.phase = .bootRunning))
    (fresh_threads : ∀ thread, thread ∈ config.threads → ¬ child.core.tid <+: thread.core.tid)
    (fresh_parents : ∀ thread, thread ∈ config.threads → ∀ index,
      thread.core.freshCounter ≤ index → ¬ (thread.core.tid ++ [index]) <+: child.core.tid)
    (fresh_joinable : ∀ ended, ended ∈ config.joinable → ¬ child.core.tid <+: ended)
    (fresh_mutexes : ∀ creator index,
      config.mutexOwners.contains (.dynamic creator index) = true → ¬ child.core.tid <+: creator)
    (isValid : config.IsValidState child.core child.code)
    (timer_eq : child.timer = none) (phase_eq : child.phase = .ready) :
    Invariant program next := by
  have isExtended : config.IsExtendedBy next := Config.IsExtendedBy.of_eq mutexOwners_eq
  have memCases : ∀ member, member ∈ next.threads → member ∈ config.threads ∨ member = child := by
    intro member isNextMember
    rw [threads_eq] at isNextMember
    simpa using isNextMember
  exact
    { notStartedIsInitial := by
        intro isNotStarted
        rw [status_eq, isRunning] at isNotStarted
        cases isNotStarted
      closedHasNoThread := by
        intro isClosed
        rw [status_eq, isRunning] at isClosed
        cases isClosed
      threadIdsNodup := by
        rw [threads_eq, List.map_append, List.nodup_append]
        refine ⟨invariant.threadIdsNodup, by simp, ?_⟩
        intro tid isTid other isOtherTid
        obtain ⟨thread, isMember, isThreadTid⟩ := List.mem_map.mp isTid
        simp only [List.map_cons, List.map_nil, List.mem_singleton] at isOtherTid
        intro sameTid
        apply fresh_threads thread isMember
        rw [isThreadTid, sameTid, isOtherTid]
        exact List.prefix_refl _
      threadIdsFresh := by
        intro first isFirstMember second isSecondMember index isLater isPrefix
        rcases memCases first isFirstMember with isOldFirst | isChildFirst <;>
          rcases memCases second isSecondMember with isOldSecond | isChildSecond
        · exact invariant.threadIdsFresh first isOldFirst second isOldSecond index isLater isPrefix
        · rw [isChildSecond] at isPrefix
          exact fresh_parents first isOldFirst index isLater isPrefix
        · rw [isChildFirst] at isPrefix
          exact fresh_threads second isOldSecond (prefix_of_concat_prefix isPrefix)
        · rw [isChildFirst, isChildSecond] at isPrefix
          exact not_concat_prefix_self _ _ isPrefix
      joinableNodup := by
        rw [joinable_eq]
        exact invariant.joinableNodup
      joinableFresh := by
        intro member isNextMember
        rw [joinable_eq]
        rcases memCases member isNextMember with isOld | isChild
        · exact invariant.joinableFresh member isOld
        · rw [isChild]
          exact ⟨fun isJoinable => fresh_joinable _ isJoinable (List.prefix_refl _),
            fun ended isEnded index _ isPrefix =>
              fresh_joinable ended isEnded (prefix_of_concat_prefix isPrefix)⟩
      mutexesFresh := by
        intro member isNextMember creator index isContained
        rw [mutexOwners_eq] at isContained
        rcases memCases member isNextMember with isOld | isChild
        · exact invariant.mutexesFresh member isOld creator index isContained
        · rw [isChild]
          have isFresh := fresh_mutexes creator index isContained
          exact ⟨fun sameCreator => absurd (sameCreator ▸ List.prefix_refl _) isFresh,
            fun later _ isPrefix => isFresh (prefix_of_concat_prefix isPrefix)⟩
      handlesExist := by
        rw [shared_eq, inputQueues_eq]
        refine ⟨?_, fun entry isEntry => isExtended.isValidVal (invariant.handlesExist.2.1 entry isEntry),
          fun entry isEntry message isMessage =>
            isExtended.isValidVal (invariant.handlesExist.2.2 entry isEntry message isMessage)⟩
        intro member isNextMember
        rcases memCases member isNextMember with isOld | isChild
        · exact isExtended.isValidThread (invariant.handlesExist.1 member isOld)
        · rw [isChild]
          have childValid := isExtended.isValidState isValid
          refine ⟨childValid.1, childValid.2.1, childValid.2.2, ?_⟩
          rw [phase_eq]
          trivial
      bootIsInitial := by
        intro member isNextMember isBoot
        rcases memCases member isNextMember with isOld | isChild
        · exact absurd isBoot (noBoot member isOld)
        · rw [isChild, phase_eq] at isBoot
          rcases isBoot with isBoot | isBoot <;> cases isBoot
      sinceBelowCounter := by
        intro member isNextMember on since isBlocked
        rw [blockCounter_eq]
        rcases memCases member isNextMember with isOld | isChild
        · exact invariant.sinceBelowCounter member isOld on since isBlocked
        · rw [isChild, phase_eq] at isBlocked
          cases isBlocked
      sinceInjective := by
        intro first isFirstMember second isSecondMember firstOn secondOn since isFirstBlocked
          isSecondBlocked
        rcases memCases first isFirstMember with isOldFirst | isChildFirst
        · rcases memCases second isSecondMember with isOldSecond | isChildSecond
          · exact invariant.sinceInjective first isOldFirst second isOldSecond firstOn secondOn
              since isFirstBlocked isSecondBlocked
          · rw [isChildSecond, phase_eq] at isSecondBlocked
            cases isSecondBlocked
        · rw [isChildFirst, phase_eq] at isFirstBlocked
          cases isFirstBlocked
      timerNumbersCounted := by
        intro member isNextMember number seconds isTimer
        rw [timerCounter_eq]
        rcases memCases member isNextMember with isOld | isChild
        · exact invariant.timerNumbersCounted member isOld number seconds isTimer
        · rw [isChild, timer_eq] at isTimer
          cases isTimer
      timerNumbersInjective := by
        intro first isFirstMember second isSecondMember number firstSeconds secondSeconds
          isFirstTimer isSecondTimer
        rcases memCases first isFirstMember with isOldFirst | isChildFirst
        · rcases memCases second isSecondMember with isOldSecond | isChildSecond
          · exact invariant.timerNumbersInjective first isOldFirst second isOldSecond number
              firstSeconds secondSeconds isFirstTimer isSecondTimer
          · rw [isChildSecond, timer_eq] at isSecondTimer
            cases isSecondTimer
        · rw [isChildFirst, timer_eq] at isFirstTimer
          cases isFirstTimer
      phasesFitCode := by
        intro member isNextMember
        rcases memCases member isNextMember with isOld | isChild
        · exact invariant.phasesFitCode member isOld
        · rw [isChild]
          simp [Thread.PhaseFitsCode, phase_eq]
      runningHoldsRequiredMutex := by
        intro member isNextMember loaded isPhase
        rw [mutexOwners_eq]
        rcases memCases member isNextMember with isOld | isChild
        · exact invariant.runningHoldsRequiredMutex member isOld loaded isPhase
        · rw [isChild, phase_eq] at isPhase
          cases isPhase }

/-- The end of the program keeps the invariant: no thread is left. -/
theorem closeProgram (invariant : Invariant program config) :
    Invariant program config.closeProgram :=
  { notStartedIsInitial := fun isNotStarted => by cases isNotStarted
    closedHasNoThread := fun _ => rfl
    threadIdsNodup := List.nodup_nil
    threadIdsFresh := fun first isMember => by cases isMember
    joinableNodup := invariant.joinableNodup
    joinableFresh := fun thread isMember => by cases isMember
    mutexesFresh := fun thread isMember => by cases isMember
    handlesExist := ⟨(fun thread isMember => by cases isMember),
      (fun entry isMember => invariant.handlesExist.2.1 entry isMember),
      (fun entry isMember => invariant.handlesExist.2.2 entry isMember)⟩
    bootIsInitial := fun thread isMember => by cases isMember
    sinceBelowCounter := fun thread isMember => by cases isMember
    sinceInjective := fun first isMember => by cases isMember
    timerNumbersCounted := fun thread isMember => by cases isMember
    timerNumbersInjective := fun first isMember => by cases isMember
    phasesFitCode := fun thread isMember => by cases isMember
    runningHoldsRequiredMutex := fun thread isMember => by cases isMember }

/-- `begin(v)` keeps the invariant: `main` is the only thread, it holds the
program input, which is a literal, and only static mutexes exist. -/
theorem begun (invariant : Invariant program config) (isNotStarted : config.status = .notStarted)
    (input : Literal) : Invariant program (config.begun program input.toVal) := by
  obtain ⟨_, isShared, _, _, _, isInputs, _, isJoinable, isTimerCounter⟩ :=
    invariant.notStartedIsInitial isNotStarted
  have noDynamic : ∀ creator index,
      AssocList.contains ((List.range program.staticMutexCount).map
        (fun index => (MutexId.static index, (none : Option Tid)))) (.dynamic creator index) =
        false := by
    intro creator index
    induction List.range program.staticMutexCount with
    | nil => rfl
    | cons head rest inductionHypothesis =>
      simpa [AssocList.contains, AssocList.get?] using inductionHypothesis
  have memMain : ∀ thread, thread ∈ (config.begun program input.toVal).threads →
      thread = { core := { tid := [], freshCounter := 0,
                           locals := [(program.mainParam, input.toVal)], stack := [] }
                 code := pushCode program.mainBody []
                 timer := none
                 phase := .bootPending } :=
    fun thread isMember => List.mem_singleton.mp isMember
  exact
    { notStartedIsInitial := fun isStatus => by cases isStatus
      closedHasNoThread := fun isStatus => by cases isStatus
      threadIdsNodup := by simp [Config.begun]
      threadIdsFresh := by
        intro first isFirstMember second isSecondMember index _ isPrefix
        rw [memMain first isFirstMember, memMain second isSecondMember] at isPrefix
        exact not_concat_prefix_self _ _ isPrefix
      joinableNodup := by
        show config.joinable.Nodup
        exact invariant.joinableNodup
      joinableFresh := by
        intro thread isMember
        show thread.core.tid ∉ config.joinable ∧ ∀ ended, ended ∈ config.joinable → _
        rw [isJoinable]
        exact ⟨(fun isEnded => nomatch isEnded), (fun ended isEnded => nomatch isEnded)⟩
      mutexesFresh := by
        intro thread isMember creator index isContained
        have isStatic : AssocList.contains ((List.range program.staticMutexCount).map
            (fun index => (MutexId.static index, (none : Option Tid))))
            (.dynamic creator index) = true := isContained
        rw [noDynamic creator index] at isStatic
        cases isStatic
      handlesExist := by
        refine ⟨?_, ?_, ?_⟩
        · intro thread isMember
          rw [memMain thread isMember]
          refine ⟨?_, (fun frame isFrame => nomatch isFrame), ?_, trivial⟩
          · intro entry isEntry
            rw [List.mem_singleton.mp isEntry]
            exact Config.isValidVal_literal _ input
          · exact Config.isValidInstr_pushCode _ _ _ (fun _ isInstr => nomatch isInstr)
        · intro entry isEntry
          rw [show (config.begun program input.toVal).shared = config.shared from rfl, isShared]
            at isEntry
          cases isEntry
        · intro entry isEntry
          rw [show (config.begun program input.toVal).inputQueues = config.inputQueues from rfl,
            isInputs] at isEntry
          cases isEntry
      bootIsInitial := by
        intro thread isMember _
        rw [memMain thread isMember]
        exact ⟨rfl, rfl, rfl, input.toVal, rfl⟩
      sinceBelowCounter := by
        intro thread isMember on since isBlocked
        rw [memMain thread isMember] at isBlocked
        cases isBlocked
      sinceInjective := by
        intro first isFirstMember second isSecondMember
        rw [memMain first isFirstMember, memMain second isSecondMember]
        exact fun _ _ _ _ _ => rfl
      timerNumbersCounted := by
        intro thread isMember number seconds isTimer
        rw [memMain thread isMember] at isTimer
        cases isTimer
      timerNumbersInjective := by
        intro first isFirstMember second isSecondMember
        rw [memMain first isFirstMember, memMain second isSecondMember]
        exact fun _ _ _ _ _ => rfl
      phasesFitCode := by
        intro thread isMember
        rw [memMain thread isMember]
        trivial
      runningHoldsRequiredMutex := by
        intro thread isMember loaded isPhase
        rw [memMain thread isMember] at isPhase
        cases isPhase }

end Invariant

end DexExpressiveness.L
