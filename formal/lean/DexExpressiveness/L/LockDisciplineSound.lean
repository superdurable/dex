import DexExpressiveness.L.LockDiscipline

/-!
# The static lock discipline implies well-formedness

`Program.isWellFormed_of_hasStaticLockDiscipline` proves that every program
that `Program.hasStaticLockDiscipline` accepts is well formed (spec §0, §8.1
"Well-formed programs").

The proof is an invariant of reachable configurations,
`Config.HasSoundLockSets`: every live thread has a lock set that it owns and
from which the analysis accepts its code, every frame of its stack is accepted
from the empty lock set, and a thread whose `lock` was granted owns that mutex.
A thread that owns a mutex keeps it until it releases it itself: L releases a
mutex only in `unlock(m)` and `wait(cv, m)` of its owner, and a `lock` takes
only a free mutex. So other threads never shrink a thread's lock set.

The proof reuses `L.Invariant` (`L/Invariant.lean`) for the uniqueness of
thread IDs and for the fit between a blocked thread's phase and its code.
-/

namespace DexExpressiveness.L

/-- The owner map gives the statically known mutex `static index` to this thread. -/
def OwnsStaticMutex (owners : AssocList MutexId (Option Tid)) (tid : Tid) (index : Nat) : Prop :=
  owners.get? (.static index) = some (some tid)

/-- The thread owns every mutex of the lock set. -/
def IsHeldLockSet (owners : AssocList MutexId (Option Tid)) (tid : Tid) (held : Nat) : Prop :=
  ∀ index, held.testBit index = true → OwnsStaticMutex owners tid index

/-- The thread's wait was decided with `granted`, and the thread has not
continued yet. -/
def Phase.IsGranted : Phase → Prop
  | .woken .granted => True
  | .resuming .granted => True
  | _ => False

/-- One thread obeys the lock discipline under the owner map `owners`. -/
structure Thread.HasSoundLockSet (owners : AssocList MutexId (Option Tid)) (thread : Thread) :
    Prop where
  /-- Every frame of the stack is accepted from the empty lock set. -/
  framesDisciplined : AreFramesLockDisciplined thread.core.stack
  /-- The code is accepted from a lock set that the thread owns. -/
  heldLockSet : ∃ held, IsHeldLockSet owners thread.core.tid held ∧
    (checkCode held thread.code).isAccepted = true
  /-- A thread whose `lock` of a statically known mutex was granted owns it. -/
  grantedLockOwned : thread.phase.IsGranted → ∀ index,
    waitRequest thread.core thread.code = some (.lock (.static index)) →
    OwnsStaticMutex owners thread.core.tid index

/-- Every live thread obeys the lock discipline: the invariant behind
`Program.isWellFormed_of_hasStaticLockDiscipline` (spec §8.1 "Well-formed
programs"). -/
def Config.HasSoundLockSets (config : Config) : Prop :=
  ∀ thread, thread ∈ config.threads → thread.HasSoundLockSet config.mutexOwners

namespace Thread.HasSoundLockSet

variable {owners : AssocList MutexId (Option Tid)} {thread : Thread}

/-- A thread that keeps every mutex it owns keeps the discipline. -/
theorem mono (sound : thread.HasSoundLockSet owners) {next : AssocList MutexId (Option Tid)}
    (keeps : ∀ index, OwnsStaticMutex owners thread.core.tid index →
      OwnsStaticMutex next thread.core.tid index) :
    thread.HasSoundLockSet next := by
  obtain ⟨held, isHeld, isAccepted⟩ := sound.heldLockSet
  exact
    { framesDisciplined := sound.framesDisciplined
      heldLockSet := ⟨held, fun index isSet => keeps index (isHeld index isSet), isAccepted⟩
      grantedLockOwned := fun isGranted index isRequest =>
        keeps index (sound.grantedLockOwned isGranted index isRequest) }

/-- A change of phase keeps the discipline, when a granted `lock` owns its mutex. -/
theorem withPhase (sound : thread.HasSoundLockSet owners) (phase : Phase)
    (granted : phase.IsGranted → ∀ index,
      waitRequest thread.core thread.code = some (.lock (.static index)) →
      OwnsStaticMutex owners thread.core.tid index) :
    ({ thread with phase := phase } : Thread).HasSoundLockSet owners :=
  { framesDisciplined := sound.framesDisciplined
    heldLockSet := sound.heldLockSet
    grantedLockOwned := granted }

end Thread.HasSoundLockSet

/-- The thread at its next statement, ready, with frames and code that the
analysis accepts from a lock set it owns, obeys the discipline. -/
theorem Thread.hasSoundLockSet_of_ready {owners : AssocList MutexId (Option Tid)}
    {thread : Thread} (isReady : thread.phase = .ready)
    (framesDisciplined : AreFramesLockDisciplined thread.core.stack) (held : Nat)
    (isHeld : IsHeldLockSet owners thread.core.tid held)
    (isAccepted : (checkCode held thread.code).isAccepted = true) :
    thread.HasSoundLockSet owners :=
  { framesDisciplined := framesDisciplined
    heldLockSet := ⟨held, isHeld, isAccepted⟩
    grantedLockOwned := fun isGranted => by rw [isReady] at isGranted; exact isGranted.elim }

/-- The thread that `earliestBlocked` returns is live and blocked on a wanted wait. -/
theorem Config.earliestBlocked_some_wanted {config : Config} {isWanted : BlockedOn → Bool}
    {tid : Tid} (isEarliest : config.earliestBlocked isWanted = some tid) :
    ∃ thread, thread ∈ config.threads ∧ thread.core.tid = tid ∧
      ∃ on since, thread.phase = .blocked on since ∧ isWanted on = true := by
  simp only [Config.earliestBlocked, Option.map_eq_some_iff] at isEarliest
  obtain ⟨candidate, isHead, isTid⟩ := isEarliest
  have isSorted := List.mem_of_mem_head? (Option.mem_def.mpr isHead)
  rw [mem_sortStable, List.mem_filterMap] at isSorted
  obtain ⟨thread, isMember, isCandidate⟩ := isSorted
  refine ⟨thread, isMember, ?_⟩
  split at isCandidate
  · rename_i on since isBlocked
    split at isCandidate
    · rename_i isWantedOn
      simp only [Option.some.injEq] at isCandidate
      rw [← isCandidate] at isTid
      exact ⟨isTid, on, since, isBlocked, isWantedOn⟩
    · cases isCandidate
  · cases isCandidate

namespace Config.HasSoundLockSets

variable {program : Program} {config : Config}

/-- The threads stay, and every thread keeps the mutexes it owns. -/
theorem of_threads_eq (sound : config.HasSoundLockSets) {next : Config}
    (threads_eq : next.threads = config.threads)
    (keeps : ∀ thread, thread ∈ config.threads → ∀ index,
      OwnsStaticMutex config.mutexOwners thread.core.tid index →
      OwnsStaticMutex next.mutexOwners thread.core.tid index) :
    next.HasSoundLockSets := by
  intro thread isMember
  rw [threads_eq] at isMember
  exact (sound thread isMember).mono (keeps thread isMember)

/-- Neither the threads nor the owner map changed. -/
theorem of_same (sound : config.HasSoundLockSets) {next : Config}
    (threads_eq : next.threads = config.threads)
    (mutexOwners_eq : next.mutexOwners = config.mutexOwners) : next.HasSoundLockSets :=
  sound.of_threads_eq threads_eq (fun _ _ index isOwned => by
    simp only [OwnsStaticMutex, mutexOwners_eq]
    exact isOwned)

/-- A change of one thread's phase keeps the discipline, when a granted
`lock` owns its mutex. -/
theorem setPhase (sound : config.HasSoundLockSets) (tid : Tid) (phase : Phase)
    (granted : ∀ thread, thread ∈ config.threads → thread.core.tid = tid → phase.IsGranted →
      ∀ index, waitRequest thread.core thread.code = some (.lock (.static index)) →
      OwnsStaticMutex config.mutexOwners tid index) :
    (config.setPhase tid phase).HasSoundLockSets := by
  intro member isMember
  simp only [Config.setPhase, Config.updateThread, List.mem_map] at isMember
  obtain ⟨candidate, isCandidate, isImage⟩ := isMember
  split at isImage
  · rename_i isTid
    rw [← isImage]
    refine (sound candidate isCandidate).withPhase phase ?_
    intro isGranted index isRequest
    rw [isTid]
    exact granted candidate isCandidate isTid isGranted index isRequest
  · rw [← isImage]
    exact sound candidate isCandidate

/-- A change of one thread's phase to a phase that is not granted keeps the
discipline. -/
theorem setPhase_ungranted (sound : config.HasSoundLockSets) (tid : Tid) (phase : Phase)
    (notGranted : ¬ phase.IsGranted) : (config.setPhase tid phase).HasSoundLockSets :=
  sound.setPhase tid phase (fun _ _ _ isGranted => absurd isGranted notGranted)

/-- A change of one thread's phase in a configuration with the same threads
and owner map keeps the discipline, when a granted `lock` owns its mutex. -/
theorem setPhase_of_same (sound : config.HasSoundLockSets) {base : Config}
    (threads_eq : base.threads = config.threads)
    (mutexOwners_eq : base.mutexOwners = config.mutexOwners) (tid : Tid) (phase : Phase)
    (granted : ∀ thread, thread ∈ config.threads → thread.core.tid = tid → phase.IsGranted →
      ∀ index, waitRequest thread.core thread.code = some (.lock (.static index)) →
      OwnsStaticMutex config.mutexOwners tid index) :
    (base.setPhase tid phase).HasSoundLockSets :=
  (sound.of_same threads_eq mutexOwners_eq).setPhase tid phase (by
    rw [threads_eq, mutexOwners_eq]
    exact granted)

/-- Blocking a thread keeps the discipline. -/
theorem blockThread (sound : config.HasSoundLockSets) (tid : Tid) (on : BlockedOn) :
    (config.blockThread tid on).HasSoundLockSets :=
  (sound.setPhase_ungranted tid (.blocked on config.blockCounter) id).of_same rfl rfl

/-- The stamp of an action keeps the discipline. -/
theorem stamp (sound : config.HasSoundLockSets) : config.stamp.HasSoundLockSets :=
  sound.of_same rfl rfl

/-- A thread that waits for something other than a `lock` needs no owned mutex
when it is granted. -/
theorem granted_of_waitRequest {thread : Thread} {request : WaitRequest}
    (isRequest : waitRequest thread.core thread.code = some request)
    (notLock : ∀ mutex, request ≠ .lock mutex) (owners : AssocList MutexId (Option Tid))
    (tid : Tid) : ∀ index, waitRequest thread.core thread.code = some (.lock (.static index)) →
      OwnsStaticMutex owners tid index := by
  intro index isLock
  rw [isRequest, Option.some.injEq] at isLock
  exact absurd isLock (notLock _)

/-- Releasing a mutex keeps the discipline, when its owner `releaser` is ready
and obeys the discipline without that mutex. -/
theorem releaseMutex (invariant : Invariant program config) (mutex : MutexId) (releaser : Tid)
    (isHeld : config.mutexOwners.get? mutex = some (some releaser))
    (soundOthers : ∀ thread, thread ∈ config.threads → thread.core.tid ≠ releaser →
      thread.HasSoundLockSet config.mutexOwners)
    (soundReleaser : ∀ thread, thread ∈ config.threads → thread.core.tid = releaser →
      thread.phase = .ready ∧ ∀ owners : AssocList MutexId (Option Tid),
        (∀ index, OwnsStaticMutex config.mutexOwners releaser index →
          mutex = .static index ∨ OwnsStaticMutex owners releaser index) →
        thread.HasSoundLockSet owners) :
    (config.releaseMutex mutex).HasSoundLockSets := by
  have keeps : ∀ (owner : Option Tid) tid index,
      OwnsStaticMutex config.mutexOwners tid index →
      (tid = releaser ∧ mutex = .static index) ∨
        OwnsStaticMutex (config.mutexOwners.set mutex owner) tid index := by
    intro owner tid index isOwned
    by_cases isSame : MutexId.static index = mutex
    · left
      simp only [OwnsStaticMutex, isSame, isHeld, Option.some.injEq] at isOwned
      exact ⟨isOwned.symm, isSame.symm⟩
    · right
      simp only [OwnsStaticMutex]
      rw [AssocList.get?_set_other _ _ _ _ isSame]
      exact isOwned
  have soundUnder : ∀ owner : Option Tid, ∀ thread, thread ∈ config.threads →
      thread.HasSoundLockSet (config.mutexOwners.set mutex owner) := by
    intro owner thread isMember
    by_cases isReleaser : thread.core.tid = releaser
    · refine (soundReleaser thread isMember isReleaser).2 _ ?_
      intro index isOwned
      rcases keeps owner releaser index isOwned with isReleased | isKept
      · exact Or.inl isReleased.2
      · exact Or.inr isKept
    · refine (soundOthers thread isMember isReleaser).mono ?_
      intro index isOwned
      rcases keeps owner _ index isOwned with isReleased | isKept
      · exact absurd isReleased.1 isReleaser
      · exact isKept
  simp only [Config.releaseMutex]
  split
  · rename_i next isEarliest
    obtain ⟨waiter, isWaiterMember, isWaiterTid, on, since, isBlocked, isWanted⟩ :=
      Config.earliestBlocked_some_wanted isEarliest
    refine Config.HasSoundLockSets.setPhase (config := { config with
      mutexOwners := config.mutexOwners.set mutex (some next) })
      (soundUnder (some next)) next (.woken .granted) ?_
    intro thread isMember isTid _ index isRequest
    have isWaiter : thread = waiter :=
      invariant.eq_of_tid_eq isMember isWaiterMember (by rw [isTid, isWaiterTid])
    have fits := invariant.phasesFitCode waiter isWaiterMember
    rw [isWaiter] at isRequest
    cases on with
    | lock wanted =>
      simp only [decide_eq_true_eq] at isWanted
      simp only [Thread.PhaseFitsCode, isBlocked, BlockedOn.toWaitRequest?] at fits
      rw [fits.2, Option.some.injEq, WaitRequest.lock.injEq] at isRequest
      simp only [OwnsStaticMutex]
      rw [← isRequest, isWanted]
      exact AssocList.get?_set_self _ _ _
    | recv chan => cases isWanted
    | join target => cases isWanted
    | park cv => cases isWanted
    | timed number deadline chan => cases isWanted
  · exact soundUnder none

/-- Waking a waiter of a condition variable keeps the discipline. -/
theorem wakeCvWaiter (invariant : Invariant program config) (sound : config.HasSoundLockSets)
    (cv : CvId) (waiter : Tid) : (config.wakeCvWaiter cv waiter).HasSoundLockSets := by
  have queued : ({ config with cvWakes := config.cvWakes ++ [(cv, waiter)] } : Config).HasSoundLockSets :=
    sound.of_same rfl rfl
  simp only [Config.wakeCvWaiter]
  split
  · rename_i found isFound
    obtain ⟨isFoundMember, isFoundTid⟩ := Config.mem_of_findThread? isFound
    split
    · rename_i parkedOn since isPhase
      split
      · refine sound.setPhase waiter (.woken .granted) ?_
        intro thread isMember isTid _
        have isFoundThread : thread = found :=
          invariant.eq_of_tid_eq isMember isFoundMember (by rw [isTid, isFoundTid])
        have fits := invariant.phasesFitCode found isFoundMember
        simp only [Thread.PhaseFitsCode, isPhase, BlockedOn.toWaitRequest?] at fits
        rw [isFoundThread]
        exact granted_of_waitRequest fits.2 (fun _ isLock => by cases isLock) _ _
      · exact queued
    · exact queued
  · exact queued

/-- A broadcast keeps the discipline. -/
theorem foldl_wakeCvWaiter (cv : CvId) (waiters : List Tid) :
    ∀ config : Config, Invariant program config → config.status ≠ .notStarted →
      config.HasSoundLockSets →
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).HasSoundLockSets := by
  induction waiters with
  | nil =>
    intro config _ _ sound
    exact sound
  | cons waiter laterWaiters inductionHypothesis =>
    intro config invariant isStarted sound
    obtain ⟨woken, sameStatus⟩ := invariant.wakeCvWaiter isStarted cv waiter
    exact inductionHypothesis _ woken (by rw [sameStatus]; exact isStarted)
      (sound.wakeCvWaiter invariant cv waiter)

/-- Delivering a cancel keeps the discipline. -/
theorem cancelBlockedThread (sound : config.HasSoundLockSets) (target : Tid) :
    (config.cancelBlockedThread target).HasSoundLockSets := by
  simp only [Config.cancelBlockedThread]
  split
  · split
    all_goals first
      | exact sound
      | exact sound.setPhase_ungranted target (.woken .canceled) id
  · exact sound

/-- Firing timers keeps the discipline. -/
theorem fireTimers (count : Nat) :
    ∀ (config fired : Config), config.HasSoundLockSets → config.fireTimers count = some fired →
      fired.HasSoundLockSets := by
  induction count with
  | zero =>
    intro config fired sound isFired
    simp only [Config.fireTimers, Option.some.injEq] at isFired
    rw [← isFired]
    exact sound
  | succ count inductionHypothesis =>
    intro config fired sound isFired
    simp only [Config.fireTimers] at isFired
    split at isFired
    · cases isFired
    · rename_i tid _
      exact inductionHypothesis _ fired (sound.setPhase_ungranted tid (.woken .fired) id) isFired

/-- The arrival of an external message keeps the discipline. -/
theorem deliverInput (sound : config.HasSoundLockSets) (chan : ExtChan) (value : Val) :
    (config.deliverInput chan value).HasSoundLockSets := by
  simp only [Config.deliverInput]
  split
  · rename_i receiver _
    exact sound.setPhase_ungranted receiver (.woken (.received value)) id
  · exact sound.of_same rfl rfl

/-- Performing the atomic action of a statement keeps the discipline, when the
thread's new frames and code obey it from a lock set that the thread owns
without the mutex it releases, and a forked thread obeys it too. -/
theorem applyIntent (invariant : Invariant program config) (sound : config.HasSoundLockSets)
    {thread : Thread} (isMember : thread ∈ config.threads) (result : StatementResult)
    (isSound : IsSoundResult config thread result)
    (framesDisciplined : AreFramesLockDisciplined result.core.stack)
    (continues : ∃ after, (∀ index, after.testBit index = true →
        OwnsStaticMutex config.mutexOwners thread.core.tid index ∧
        result.intent.releasedStaticIndex? ≠ some index) ∧
      (checkCode after result.code).isAccepted = true)
    (forkDisciplined : ∀ child childCode, result.intent = .fork child childCode →
      child.stack = [] ∧ (checkCode 0 childCode).isAccepted = true) :
    (config.applyIntent thread.core.tid result).2.HasSoundLockSets := by
  have isRunning := invariant.status_of_mem isMember
  have isStarted : config.status ≠ .notStarted := by
    rw [isRunning]
    intro isStatus
    cases isStatus
  have continuedStarted : (config.continueThread thread.core.tid result).status ≠ .notStarted :=
    isStarted
  obtain ⟨after, isAfter, isAccepted⟩ := continues
  have newSound : ∀ newThread : Thread, newThread.core = result.core →
      newThread.code = result.code → newThread.phase = .ready →
      ∀ owners : AssocList MutexId (Option Tid),
        (∀ index, OwnsStaticMutex config.mutexOwners thread.core.tid index →
          result.intent.releasedStaticIndex? = some index ∨
          OwnsStaticMutex owners thread.core.tid index) →
        newThread.HasSoundLockSet owners := by
    intro newThread isCore isCode isReady owners keeps
    have isTid : newThread.core.tid = thread.core.tid := by rw [isCore]; exact isSound.tid_eq
    refine Thread.hasSoundLockSet_of_ready isReady (by rw [isCore]; exact framesDisciplined)
      after ?_ (by rw [isCode]; exact isAccepted)
    intro index isSet
    rw [isTid]
    rcases keeps index (isAfter index isSet).1 with isReleased | isKept
    · exact absurd isReleased (isAfter index isSet).2
    · exact isKept
  have continuedSound : (config.continueThread thread.core.tid result).HasSoundLockSets := by
    intro member isNextMember
    rcases invariant.mem_continueThread isMember result isNextMember with isNew | ⟨isOld, _⟩
    · rw [isNew]
      exact newSound _ rfl rfl rfl config.mutexOwners (fun _ isOwned => Or.inr isOwned)
    · exact sound member isOld
  have releaseSound : ∀ (released : Config) (mutex : MutexId),
      Invariant program released →
      released.threads = (config.continueThread thread.core.tid result).threads →
      released.mutexOwners = config.mutexOwners →
      config.mutexOwners.get? mutex = some (some thread.core.tid) →
      (∀ index, mutex = .static index → result.intent.releasedStaticIndex? = some index) →
      (released.releaseMutex mutex).HasSoundLockSets := by
    intro released mutex releasedInvariant threads_eq mutexOwners_eq isHeld isReleased
    refine Config.HasSoundLockSets.releaseMutex releasedInvariant mutex thread.core.tid
      (by rw [mutexOwners_eq]; exact isHeld) ?_ ?_
    · intro member isNextMember isOther
      rw [threads_eq] at isNextMember
      rw [mutexOwners_eq]
      rcases invariant.mem_continueThread isMember result isNextMember with isNew | ⟨isOld, _⟩
      · rw [isNew] at isOther
        exact absurd isSound.tid_eq isOther
      · exact sound member isOld
    · intro member isNextMember isReleaser
      rw [threads_eq] at isNextMember
      rcases invariant.mem_continueThread isMember result isNextMember with
        isNew | ⟨_, isOther⟩
      · rw [isNew]
        refine ⟨rfl, fun owners keeps => newSound _ rfl rfl rfl owners ?_⟩
        intro index isOwned
        rw [mutexOwners_eq] at keeps
        rcases keeps index isOwned with isStatic | isKept
        · exact Or.inl (isReleased index isStatic)
        · exact Or.inr isKept
      · exact absurd isReleaser isOther
  cases isIntent : result.intent with
  | silent =>
    simp only [Config.applyIntent, isIntent]
    exact continuedSound
  | store target value =>
    simp only [Config.applyIntent, isIntent]
    exact continuedSound.of_same rfl rfl
  | fork child childCode =>
    simp only [Config.applyIntent, isIntent]
    obtain ⟨isChildStack, isChildAccepted⟩ := forkDisciplined child childCode isIntent
    intro member isNextMember
    rcases List.mem_append.mp isNextMember with isOld | isChild
    · exact continuedSound member isOld
    · rw [List.mem_singleton.mp isChild]
      refine Thread.hasSoundLockSet_of_ready rfl ?_ 0 (fun index isSet => by simp at isSet)
        isChildAccepted
      intro frame isFrame
      simp only [isChildStack] at isFrame
      cases isFrame
  | newMutex mutex =>
    simp only [Config.applyIntent, isIntent]
    obtain ⟨isMutex, _⟩ := isSound.newMutex mutex isIntent
    refine continuedSound.of_threads_eq rfl ?_
    intro member _ index isOwned
    simp only [OwnsStaticMutex] at isOwned ⊢
    rw [AssocList.get?_set_other _ _ _ _ (by rw [isMutex]; intro isSame; cases isSame)]
    exact isOwned
  | unlock mutex =>
    simp only [Config.applyIntent, isIntent]
    exact releaseSound _ mutex
      (invariant.continueThread isMember result isSound
        (by rw [isIntent]; intro created isNew; cases isNew))
      rfl rfl (isSound.unlock mutex isIntent)
      (fun index isStatic => by rw [isIntent, isStatic]; rfl)
  | cvWait cv =>
    simp only [Config.applyIntent, isIntent]
    exact releaseSound _ cv.mutex
      ((invariant.continueThread isMember result isSound
        (by rw [isIntent]; intro created isNew; cases isNew)).setCvQueues continuedStarted
        (config.cvQueues.set cv ((config.cvQueues.get? cv).getD [] ++ [thread.core.tid])))
      rfl rfl (isSound.cvWait cv isIntent)
      (fun index isStatic => by
        rw [isIntent]
        obtain ⟨cvMutex, cvIndex⟩ := cv
        simp only at isStatic
        subst isStatic
        rfl)
  | signal cv =>
    simp only [Config.applyIntent, isIntent]
    have continued := invariant.continueThread isMember result isSound
      (by rw [isIntent]; intro created isNew; cases isNew)
    split
    · exact continuedSound
    · rename_i waiter laterWaiters _
      exact Config.HasSoundLockSets.wakeCvWaiter
        (continued.setCvQueues continuedStarted (config.cvQueues.set cv laterWaiters))
        (continuedSound.of_same rfl rfl) cv waiter
  | broadcast cv =>
    simp only [Config.applyIntent, isIntent]
    exact foldl_wakeCvWaiter cv ((config.cvQueues.get? cv).getD []) _
      ((invariant.continueThread isMember result isSound
        (by rw [isIntent]; intro created isNew; cases isNew)).setCvQueues continuedStarted
        (config.cvQueues.set cv [])) continuedStarted (continuedSound.of_same rfl rfl)
  | cancel target =>
    simp only [Config.applyIntent, isIntent]
    refine Config.HasSoundLockSets.cancelBlockedThread ?_ target
    exact continuedSound.of_same rfl rfl
  | enterTimed seconds =>
    simp only [Config.applyIntent, isIntent]
    intro member isNextMember
    have isUpdated := invariant.threads_updateThread isMember (fun current =>
      { current with core := result.core, code := result.code
                     timer := some (config.timerCounter + 1, seconds), phase := .ready })
    simp only at isNextMember
    rw [isUpdated] at isNextMember
    rcases mem_replaceThread isNextMember with isNew | ⟨isOld, _⟩
    · rw [isNew]
      exact newSound _ rfl rfl rfl config.mutexOwners (fun _ isOwned => Or.inr isOwned)
    · exact sound member isOld
  | endThread =>
    simp only [Config.applyIntent, isIntent]
    have removedSound : ({ config with
        threads := config.threads.filter
          (fun candidate => !decide (candidate.core.tid = thread.core.tid)) } :
        Config).HasSoundLockSets := by
      intro member isRemovedMember
      exact sound member (List.mem_filter.mp isRemovedMember).1
    split
    · rename_i joiner isEarliest
      obtain ⟨waiter, isWaiterMember, isWaiterTid, on, since, isBlocked, isWanted⟩ :=
        Config.earliestBlocked_some_wanted isEarliest
      refine removedSound.setPhase joiner (.woken .granted) ?_
      intro member isRemovedMember isTid _
      have isWaiter : member = waiter :=
        invariant.eq_of_tid_eq (List.mem_filter.mp isRemovedMember).1
          (List.mem_filter.mp isWaiterMember).1 (by rw [isTid, isWaiterTid])
      have fits := invariant.phasesFitCode waiter (List.mem_filter.mp isWaiterMember).1
      rw [isWaiter]
      cases on with
      | join target =>
        simp only [Thread.PhaseFitsCode, isBlocked, BlockedOn.toWaitRequest?] at fits
        exact granted_of_waitRequest fits.2 (fun _ isLock => by cases isLock) _ _
      | lock wanted => cases isWanted
      | recv chan => cases isWanted
      | park cv => cases isWanted
      | timed number deadline chan => cases isWanted
    · exact removedSound.of_same rfl rfl
  | exit value =>
    simp only [Config.applyIntent, isIntent]
    intro member isNextMember
    cases isNextMember
  | fail message =>
    simp only [Config.applyIntent, isIntent]
    intro member isNextMember
    cases isNextMember
  | halt =>
    simp only [Config.applyIntent, isIntent]
    exact sound.setPhase_ungranted _ .halted id

/-- Registering the wait of a blocking statement or timed operation keeps the
discipline. A `lock` that takes a free mutex makes the thread its owner. -/
theorem registerWait (invariant : Invariant program config) (sound : config.HasSoundLockSets)
    {thread : Thread} (isMember : thread ∈ config.threads) :
    (config.registerWait thread).HasSoundLockSets := by
  have grantedOf : ∀ request : WaitRequest,
      waitRequest thread.core thread.code = some request → (∀ mutex, request ≠ .lock mutex) →
      ∀ member, member ∈ config.threads → member.core.tid = thread.core.tid →
      ∀ index, waitRequest member.core member.code = some (.lock (.static index)) →
      OwnsStaticMutex config.mutexOwners thread.core.tid index := by
    intro request isRequest notLock member isMemberMember isTid
    rw [invariant.eq_of_tid_eq isMemberMember isMember isTid]
    exact granted_of_waitRequest isRequest notLock _ _
  simp only [Config.registerWait]
  split
  · split
    · split
      · exact sound.setPhase_ungranted _ (.woken .canceled) id
      · exact sound.blockThread _ _
    · split
      · rename_i message laterMessages _
        exact sound.setPhase_of_same rfl rfl _ (.woken (.received message))
          (fun _ _ _ isGranted => absurd isGranted id)
      · split
        · exact sound.setPhase_ungranted _ (.woken .canceled) id
        · exact sound.blockThread _ _
    · exact sound.setPhase_ungranted _ (.woken .invalid) id
  · split
    · rename_i mutex isRequest
      split
      · rename_i isOwner
        have ownedSound : ({ config with
            mutexOwners := config.mutexOwners.set mutex (some thread.core.tid) } :
            Config).HasSoundLockSets := by
          refine sound.of_threads_eq rfl ?_
          intro member _ index isOwned
          simp only [OwnsStaticMutex] at isOwned ⊢
          by_cases isSame : MutexId.static index = mutex
          · rw [isSame, isOwner] at isOwned
            cases isOwned
          · rw [AssocList.get?_set_other _ _ _ _ isSame]
            exact isOwned
        refine ownedSound.setPhase _ (.woken .granted) ?_
        intro member isMemberMember isTid _ index isLock
        rw [invariant.eq_of_tid_eq isMemberMember isMember isTid, isRequest, Option.some.injEq,
          WaitRequest.lock.injEq] at isLock
        simp only [OwnsStaticMutex]
        rw [← isLock]
        exact AssocList.get?_set_self _ _ _
      · exact sound.blockThread _ _
    · rename_i chan isRequest
      split
      · rename_i message laterMessages _
        exact sound.setPhase_of_same rfl rfl _ (.woken (.received message))
          (fun _ _ _ isGranted => absurd isGranted id)
      · split
        · exact sound.setPhase_ungranted _ (.woken .canceled) id
        · exact sound.blockThread _ _
    · rename_i target isRequest
      split
      · refine sound.setPhase_of_same rfl rfl _ (.woken .granted) ?_
        intro member isMemberMember isTid _
        exact grantedOf _ isRequest (fun _ isLock => by cases isLock) member isMemberMember isTid
      · split
        · exact sound.setPhase_ungranted _ (.woken .canceled) id
        · exact sound.blockThread _ _
    · rename_i cv isRequest
      split
      · refine sound.setPhase_of_same rfl rfl _ (.woken .granted) ?_
        intro member isMemberMember isTid _
        exact grantedOf _ isRequest (fun _ isLock => by cases isLock) member isMemberMember isTid
      · exact sound.blockThread _ _
    · exact sound.setPhase_ungranted _ (.woken .invalid) id

/-- The start of a disciplined program obeys the discipline. -/
theorem begun (isDisciplined : program.hasStaticLockDiscipline = true) (input : Val) :
    (config.begun program input).HasSoundLockSets := by
  intro thread isMember
  simp only [Config.begun, List.mem_singleton] at isMember
  rw [isMember]
  refine
    { framesDisciplined := fun frame isFrame => by cases isFrame
      heldLockSet := ⟨0, fun index isSet => by simp at isSet, ?_⟩
      grantedLockOwned := fun isGranted => isGranted.elim }
  simp only [Program.hasStaticLockDiscipline, Bool.and_eq_true] at isDisciplined
  show (checkCode 0 (pushCode program.mainBody [])).isAccepted = true
  rw [checkCode_pushCode]
  have isMainAccepted := isDisciplined.1
  revert isMainAccepted
  cases checkStmt 0 program.mainBody <;> simp [LockSetOutcome.andThen, checkCode]

/-- The next action of one thread keeps the discipline. -/
theorem stepThread (isDisciplined : program.hasStaticLockDiscipline = true)
    (invariant : Invariant program config) (sound : config.HasSoundLockSets) {thread : Thread}
    (isMember : thread ∈ config.threads) {events : List Event} {next : Config}
    (isStep : L.stepThread program config thread = some (events, next)) :
    next.HasSoundLockSets := by
  have threadSound := sound thread isMember
  obtain ⟨held, isHeld, isAccepted⟩ := threadSound.heldLockSet
  have phaseValid := (invariant.handlesExist.1 thread isMember).2.2.2
  have completeSound : ∀ loaded, IsSoundResult config thread
      (completeStatement program thread.core thread.code loaded) →
      (config.applyIntent thread.core.tid
        (completeStatement program thread.core thread.code loaded)).2.HasSoundLockSets := by
    intro loaded isSound
    have isLocal := lockDisciplined_completeStatement isDisciplined thread.core thread.code loaded
      held threadSound.framesDisciplined isAccepted
    obtain ⟨after, isAfter, isAfterAccepted⟩ := isLocal.continues
    exact sound.applyIntent invariant isMember _ isSound isLocal.framesDisciplined
      ⟨after, fun index isSet =>
        ⟨isHeld index (isAfter index isSet).1, (isAfter index isSet).2⟩, isAfterAccepted⟩
      isLocal.forkDisciplined
  cases isPhase : thread.phase with
  | bootPending =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · cases isStep
      exact (sound.setPhase_ungranted _ .bootRunning id).stamp
    · cases isStep
  | bootRunning =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · cases isStep
      exact (sound.setPhase_ungranted _ .ready id).stamp
    · cases isStep
  | ready =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · split at isStep
      · cases isStep
        exact (sound.setPhase_ungranted _ .waitStarted id).stamp
      · cases isStep
    · split at isStep
      · split at isStep
        · split at isStep
          · cases isStep
            exact (sound.setPhase_ungranted _ (.running .null) id).stamp
          · cases isStep
        · split at isStep
          · cases isStep
            exact (sound.setPhase_ungranted _ (.running _) id).stamp
          · cases isStep
        · split at isStep
          · cases isStep
            exact (sound.setPhase_ungranted _ (.emitting _) id).stamp
          · cases isStep
      · cases isStep
  | running loaded =>
    simp only [L.stepThread, isPhase] at isStep
    rw [isPhase] at phaseValid
    split at isStep
    · cases isStep
      exact (completeSound loaded (invariant.isSoundResult_completeStatement isMember loaded
        phaseValid (invariant.runningHoldsRequiredMutex thread isMember loaded isPhase))).stamp
    · cases isStep
  | emitting effect =>
    simp only [L.stepThread, isPhase, Option.some.injEq, Prod.mk.injEq] at isStep
    rw [← isStep.2]
    exact sound.setPhase_ungranted _ .emitted id
  | emitted =>
    simp only [L.stepThread, isPhase] at isStep
    have fits := invariant.phasesFitCode thread isMember
    simp only [Thread.PhaseFitsCode, isPhase] at fits
    obtain ⟨_, _, effect, isKind⟩ := fits
    split at isStep
    · cases isStep
      exact (completeSound .null (invariant.isSoundResult_completeStatement isMember .null trivial
        (fun mutex isRequired =>
          absurd isKind (beginKind_of_requiredMutex _ _ mutex isRequired effect)))).stamp
    · cases isStep
  | waitStarted =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · cases isStep
      exact (sound.registerWait invariant isMember).stamp
    · cases isStep
  | blocked on since => simp [L.stepThread, isPhase] at isStep
  | woken outcome =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · cases isStep
      refine (sound.setPhase _ (.resuming outcome) ?_).stamp
      intro member isMemberMember isTid isGranted index isRequest
      rw [invariant.eq_of_tid_eq isMemberMember isMember isTid] at isRequest
      have wasGranted : thread.phase.IsGranted := by
        rw [isPhase]
        cases outcome <;> first | exact isGranted | exact isGranted.elim
      exact threadSound.grantedLockOwned wasGranted index isRequest
    · cases isStep
  | resuming outcome =>
    simp only [L.stepThread, isPhase] at isStep
    rw [isPhase] at phaseValid
    split at isStep
    · cases isStep
      have isLocal : IsLockDisciplinedResume thread.core thread.code outcome held
          (if thread.timer.isSome then resumeTimed thread.core thread.code outcome
           else resumeBlocked thread.core thread.code outcome) := by
        split
        · exact lockDisciplined_resumeTimed _ _ outcome held threadSound.framesDisciplined
            isAccepted
        · exact lockDisciplined_resumeBlocked _ _ outcome held threadSound.framesDisciplined
            isAccepted
      have isQuiet : ∀ result : StatementResult,
          result = (if thread.timer.isSome then resumeTimed thread.core thread.code outcome
            else resumeBlocked thread.core thread.code outcome) →
          result.intent = .silent ∨ result.intent = .endThread ∨ result.intent = .halt := by
        intro result isResult
        rw [isResult]
        split
        · exact (resumeTimed_shape _ _ outcome).2.2
        · exact (resumeBlocked_shape _ _ outcome).2.2
      obtain ⟨framesDisciplined, after, isAfter, isAfterAccepted⟩ := isLocal
      refine (sound.applyIntent invariant isMember _
        (invariant.isSoundResult_resume isMember outcome phaseValid) framesDisciplined
        ⟨after, fun index isSet => ⟨?_, ?_⟩, isAfterAccepted⟩ ?_).stamp
      · rcases isAfter index isSet with isOld | ⟨isGrantedOutcome, isRequest⟩
        · exact isHeld index isOld
        · refine threadSound.grantedLockOwned ?_ index isRequest
          rw [isPhase, isGrantedOutcome]
          trivial
      · rcases isQuiet _ rfl with isIntent | isIntent | isIntent <;>
          (rw [isIntent]; intro isReleased; cases isReleased)
      · intro child childCode isFork
        rcases isQuiet _ rfl with isIntent | isIntent | isIntent <;>
          (rw [isIntent] at isFork; cases isFork)
    · cases isStep
  | halted => simp [L.stepThread, isPhase] at isStep

/-- Every transition of a disciplined program keeps the discipline. -/
theorem step (isDisciplined : program.hasStaticLockDiscipline = true) (label : Label)
    (events : List Event) (next : Config) (invariant : Invariant program config)
    (sound : config.HasSoundLockSets)
    (isStep : L.step program config label = some (events, next)) : next.HasSoundLockSets := by
  cases label with
  | tick duration =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
      exact sound.of_same rfl rfl
    · cases isStep
  | request request =>
    cases request with
    | start input =>
      simp only [L.step] at isStep
      split at isStep
      · split at isStep
        · cases isStep
          exact (Config.HasSoundLockSets.begun isDisciplined input.toVal).stamp
        · cases isStep
      · cases isStep
    | input chan value =>
      simp only [L.step] at isStep
      split at isStep
      · cases isStep
      · split at isStep
        · cases isStep
          exact (sound.deliverInput chan value.toVal).stamp
        · cases isStep
      · cases isStep
        exact sound
  | thread tid =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
    · rename_i current isFound
      exact Config.HasSoundLockSets.stepThread isDisciplined invariant sound
        (Config.mem_of_findThread? isFound).1 isStep
  | fire count =>
    simp only [L.step] at isStep
    split at isStep
    · simp only [Option.map_eq_some_iff, Prod.mk.injEq] at isStep
      obtain ⟨fired, isFired, _, isNext⟩ := isStep
      rw [← isNext]
      exact (Config.HasSoundLockSets.fireTimers count config fired sound isFired).stamp
    · cases isStep
  | lateDeliver index =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
      exact sound.of_same rfl rfl
    · cases isStep
  | lateDrop index =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
      exact sound.of_same rfl rfl
    · cases isStep

/-- Every finite run of a disciplined program keeps the discipline. -/
theorem of_exec (isDisciplined : program.hasStaticLockDiscipline = true) (labels : List Label) :
    ∀ (config : Config) (events : List Event) (final : Config),
      Invariant program config → config.HasSoundLockSets →
      (system program).exec config labels = some (events, final) → final.HasSoundLockSets := by
  induction labels with
  | nil =>
    intro config events final _ sound isExec
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    exact sound
  | cons label laterLabels inductionHypothesis =>
    intro config events final invariant sound isExec
    simp only [TransitionSystem.exec] at isExec
    split at isExec
    · cases isExec
    · rename_i stepEvents next isStep
      split at isExec
      · cases isExec
      · rename_i laterEvents reached isLater
        cases isExec
        exact inductionHypothesis next laterEvents _
          (Invariant.step program config label stepEvents next invariant isStep)
          (Config.HasSoundLockSets.step isDisciplined label stepEvents next invariant sound isStep)
          isLater

/-- Every reachable configuration of a disciplined program obeys the discipline. -/
theorem of_reachable (isDisciplined : program.hasStaticLockDiscipline = true) (config : Config)
    (isReachable : (system program).Reachable config) : config.HasSoundLockSets := by
  obtain ⟨labels, events, isExec⟩ := isReachable
  exact Config.HasSoundLockSets.of_exec isDisciplined labels _ events config
    (Invariant.initial program) (fun thread isMember => by cases isMember) isExec

end Config.HasSoundLockSets

/-- A program that obeys the static lock discipline is well formed (spec §0,
§8.1 "Well-formed programs"): no reachable configuration has a thread about to
call `unlock`, `wait`, `signal`, or `broadcast` without holding the mutex. -/
theorem Program.isWellFormed_of_hasStaticLockDiscipline (program : Program)
    (isDisciplined : program.hasStaticLockDiscipline = true) : program.IsWellFormed := by
  intro config isReachable isBreaking
  obtain ⟨thread, isMember, _, _, mutex, isRequired, notOwned⟩ := isBreaking
  have threadSound :=
    Config.HasSoundLockSets.of_reachable isDisciplined config isReachable thread isMember
  obtain ⟨held, isHeld, isAccepted⟩ := threadSound.heldLockSet
  obtain ⟨index, isMutex, isSet⟩ := requiredMutex_mem_of_isAccepted isAccepted isRequired
  rw [isMutex] at notOwned
  exact notOwned (isHeld index isSet)

end DexExpressiveness.L
