import DexExpressiveness.Simulation.Completion.Data

/-!
# `effects` of a translated Execute and the L action it simulates

One lemma per atomic L action that publishes or writes (spec §8.3):

| L action | Dex effects | Lemma |
| --- | --- | --- |
| none | none | `wp3b_effect_none` |
| write a shared variable, change a waiter list | Attribute writes | `wp3b_effect_writes` |
| create a mutex | one token, no waiter | `wp3b_effect_token` |
| release a mutex (`unlock`, `wait`) | one token | `wp3b_effect_release` |
| wake one dequeued waiter (`signal`) | one message on `cv/w` | `wp3b_effect_wakeCvWaiter` |
| set a cancel flag | one message on `cancel/t` | `wp3b_effect_cancel` |
| end a thread | one message on `join/t` | `wp3b_effect_endThread` |

Each keeps `MidState`, the relation with the committing thread left out.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The Dex configuration with another run. -/
def withRun (config : Dex.Config signature) (run : Dex.Run signature) : Dex.Config signature :=
  { config with run := some run }

/-- The run after `effects` with one publication and no write. -/
abbrev publishedRun (run : Dex.Run signature) (chan : Chan) (message : DexVal) :
    Dex.Run signature :=
  run.applyEffects { publications := [(chan, message)] }

/-- The committing thread is neither changed nor removed. -/
def OwnKept (lConfig lNext : L.Config) (tid : Tid) : Prop :=
  (∀ own, own ∈ lConfig.threads → own.core.tid = tid → own ∈ lNext.threads) ∧
  (∀ own, own ∈ lNext.threads → own.core.tid = tid → own ∈ lConfig.threads)

/-- The same threads keep the committing thread. -/
theorem wp3b_ownKept_of_eq (lConfig lNext : L.Config) (tid : Tid)
    (sameThreads : lNext.threads = lConfig.threads) : OwnKept lConfig lNext tid :=
  ⟨fun own isOwn _ => by rw [sameThreads]; exact isOwn,
   fun own isOwn _ => by rw [sameThreads] at isOwn; exact isOwn⟩

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}
  {run : Dex.Run signature} {tid : Tid} {stepId : Dex.StepExecId signature}

/-- A wake keeps the committing thread, which is not blocked. -/
theorem wp3b_ownKept_of_wake (mid : MidThreads program lConfig dexConfig run tid stepId)
    (lNext : L.Config) (woken : L.Thread → Option WakeOutcome)
    (threadsNext : lNext.threads = lConfig.threads.map (wakeThreadBy woken))
    (wokenWereBlocked : ∀ thread, thread ∈ lConfig.threads → ∀ outcome,
      woken thread = some outcome → ∃ on since, thread.phase = .blocked on since) :
    OwnKept lConfig lNext tid := by
  have ownIsKept : ∀ own, own ∈ lConfig.threads → own.core.tid = tid →
      wakeThreadBy woken own = own := by
    intro own isOwn ownTid
    cases wokenCase : woken own with
    | none => simp [wakeThreadBy, wokenCase]
    | some outcome =>
      obtain ⟨on, since, isBlocked⟩ := wokenWereBlocked own isOwn outcome wokenCase
      exact absurd isBlocked (mid.ownThread own isOwn ownTid on since)
  have sameCore : ∀ thread, (wakeThreadBy woken thread).core = thread.core := by
    intro thread
    unfold wakeThreadBy
    split <;> rfl
  constructor
  · intro own isOwn ownTid
    rw [threadsNext]
    exact List.mem_map.mpr ⟨own, isOwn, ownIsKept own isOwn ownTid⟩
  · intro own isOwn ownTid
    rw [threadsNext] at isOwn
    obtain ⟨original, isOriginal, rfl⟩ := List.mem_map.mp isOwn
    rw [sameCore] at ownTid
    rw [ownIsKept original isOriginal ownTid]
    exact isOriginal

/-- `effects` with nothing to write or publish changes nothing. -/
theorem wp3b_effect_none (mid : MidThreads program lConfig dexConfig run tid stepId) :
    run.applyEffects {} = run :=
  wp3b_publish_noWaiter mid [] (fun _ _ _ _ _ _ _ => by simp)

/-- Writes then publications. -/
theorem wp3b_applyEffects_writes (run : Dex.Run signature) (writes : List (Key × Option DexVal))
    (publications : List (Chan × DexVal)) :
    run.applyEffects { writes := writes, publications := publications } =
      (run.applyWrites writes).applyEffects { publications := publications } := rfl

/-- Attribute writes: the caller proves the two Attribute clauses. -/
theorem wp3b_effect_writes (mid : MidState program lConfig dexConfig run tid stepId)
    (lNext : L.Config) (writes : List (Key × Option DexVal))
    (sameThreads : lNext.threads = lConfig.threads)
    (sameCounter : lNext.timerCounter = lConfig.timerCounter)
    (sameInput : lNext.inputQueues = lConfig.inputQueues)
    (sameJoinable : lNext.joinable = lConfig.joinable)
    (sameFlags : lNext.cancelFlags = lConfig.cancelFlags)
    (sameWakes : lNext.cvWakes = lConfig.cvWakes)
    (sameOwners : lNext.mutexOwners = lConfig.mutexOwners)
    (sameQueues : ∀ thread, thread ∈ lConfig.threads → thread.core.tid ≠ tid →
      ∀ loaded, thread.phase = .running loaded → ∀ cv,
        cvOperand thread.core thread.code = some cv →
        lNext.cvQueues.get? cv = lConfig.cvQueues.get? cv)
    (shared : ∀ name,
      readShared { attributes := (run.applyWrites writes).attributes } name = lNext.readShared name)
    (cvQueues : ∀ cv, readWaiters { attributes := (run.applyWrites writes).attributes } cv =
      (lNext.cvQueues.get? cv).getD []) :
    MidState program lNext (withRun dexConfig (run.applyWrites writes)) (run.applyWrites writes)
      tid stepId := by
  obtain ⟨sameSteps, sameCounters, sameTimers, sameChannels, sameSeq, _, _⟩ :=
    Dex.Run.applyWrites_frame writes run
  have samePending : ∀ chan, (run.applyWrites writes).pendingMessages chan =
      run.pendingMessages chan := by
    intro chan
    simp [Dex.Run.pendingMessages, sameChannels]
  refine ⟨?_, mid.data.writes lNext writes sameThreads sameCounter sameInput sameJoinable sameFlags
    sameWakes shared cvQueues, ?_⟩
  · apply mid.threads.frame lNext (withRun dexConfig (run.applyWrites writes))
      (run.applyWrites writes) rfl rfl sameQueues sameThreads sameSteps sameTimers
    rw [sameSeq]
    exact mid.threads.runInvariant.transfer (by rw [sameSteps]) sameCounters
      (fun record waitState isWaiting =>
        ⟨record, ⟨by rw [← sameSteps]; exact isWaiting.1, isWaiting.2.1, isWaiting.2.2⟩, rfl⟩)
      (fun entry isEntry => by rw [sameTimers] at isEntry; exact isEntry)
      (fun chan isEmpty => by rw [samePending]; exact isEmpty)
  · intro mutex
    rw [samePending, sameOwners]
    exact mid.tokens mutex

/-- One publication that no blocked thread waits on. -/
theorem wp3b_publish_one_noWaiter (mid : MidThreads program lConfig dexConfig run tid stepId)
    (chan : Chan) (message : DexVal)
    (noWaiter : ∀ thread, thread ∈ lConfig.threads → ∀ on since,
      thread.phase = .blocked on since → chan ∉ (expectedWaitShape thread.core.tid on).2.2)
    (lNext : L.Config) (sameQueues : lNext.cvQueues = lConfig.cvQueues)
    (sameThreads : lNext.threads = lConfig.threads) :
    MidThreads program lNext (withRun dexConfig (publishedRun run chan message))
      (publishedRun run chan message) tid stepId ∧
    (∀ other, (publishedRun run chan message).pendingMessages other =
      if other = chan then run.pendingMessages chan ++ [message]
      else run.pendingMessages other) ∧
    (publishedRun run chan message).attributes = run.attributes ∧
    (publishedRun run chan message).counters = run.counters ∧
    (publishedRun run chan message).status = run.status ∧
    (publishedRun run chan message).outputs = run.outputs := by
  have isPublished : publishedRun run chan message = run.applyPublications [(chan, message)] := by
    apply wp3b_publish_noWaiter mid
    intro thread isThread on since isBlocked named isNamed isChan
    have isSame : named = chan := by simpa using isChan
    rw [isSame] at isNamed
    exact noWaiter thread isThread on since isBlocked isNamed
  obtain ⟨sameSteps, sameCounters, sameTimers, sameSeq, sameStatus, sameAttributes, sameOutputs,
    _⟩ := Dex.Run.applyPublications_frame [(chan, message)] run
  have invariantNext := mid.runInvariant.applyEffects { publications := [(chan, message)] }
  have seqNext := Dex.Run.seq_applyEffects run { publications := [(chan, message)] }
  refine ⟨?_, ?_, ?_, ?_, ?_, ?_⟩
  · apply mid.frame lNext (withRun dexConfig (publishedRun run chan message))
      (publishedRun run chan message) rfl rfl (fun _ _ _ _ _ cv _ => by rw [sameQueues])
      sameThreads
    · rw [isPublished]
      exact sameSteps
    · rw [isPublished]
      exact sameTimers
    · rw [seqNext]
      exact invariantNext
  · intro other
    rw [isPublished]
    exact wp3b_pendingMessages_publish run chan other message
  · rw [isPublished]
    exact sameAttributes
  · rw [isPublished]
    exact sameCounters
  · rw [isPublished]
    exact sameStatus
  · rw [isPublished]
    exact sameOutputs

/-- One publication that goes to the thread that blocked first on the Channel. -/
theorem wp3b_wake_one (mid : MidThreads program lConfig dexConfig run tid stepId)
    (chan : Chan) (message : DexVal) (thread : L.Thread) (on : BlockedOn) (since : Nat)
    (outcome : WakeOutcome) (isThread : thread ∈ lConfig.threads)
    (isBlocked : thread.phase = .blocked on since)
    (isNamed : chan ∈ (expectedWaitShape thread.core.tid on).2.2)
    (isEarliest : ∀ other, other ∈ lConfig.threads → ∀ otherOn otherSince,
      other.phase = .blocked otherOn otherSince →
      chan ∈ (expectedWaitShape other.core.tid otherOn).2.2 → since ≤ otherSince)
    (isDecoded : decodeOutcome thread (some
      { timersDone := List.replicate (expectedWaitShape thread.core.tid on).2.1 false
        channelMessages := (expectedWaitShape thread.core.tid on).2.2.map (fun other =>
          if other = chan then some message else none) }) = outcome)
    (lNext : L.Config) (sameQueues : lNext.cvQueues = lConfig.cvQueues)
    (threadsNext : lNext.threads = (lConfig.wakeThread thread.core.tid outcome).threads) :
    MidThreads program lNext (withRun dexConfig (publishedRun run chan message))
      (publishedRun run chan message) tid stepId ∧
    lNext.threads = lConfig.threads.map (wakeThreadBy (fun candidate =>
      if candidate.core.tid = thread.core.tid then some outcome else none)) ∧
    (∀ candidate, candidate ∈ lConfig.threads → ∀ wokenOutcome,
      (if candidate.core.tid = thread.core.tid then some outcome else none) = some wokenOutcome →
      ∃ candidateOn candidateSince, candidate.phase = .blocked candidateOn candidateSince) ∧
    (∀ other, (publishedRun run chan message).pendingMessages other = run.pendingMessages other) ∧
    (publishedRun run chan message).attributes = run.attributes ∧
    (publishedRun run chan message).counters = run.counters ∧
    (publishedRun run chan message).status = run.status ∧
    (publishedRun run chan message).outputs = run.outputs := by
  obtain ⟨midNext, samePending⟩ := wp3b_publish_wake mid chan message thread on since outcome
    isThread isBlocked isNamed isEarliest isDecoded lNext
    (withRun dexConfig (publishedRun run chan message)) rfl rfl
    (fun _ _ _ _ _ cv _ => by rw [sameQueues]) threadsNext
  obtain ⟨sameAttributes, sameCounters, sameStatus, sameOutputs⟩ :=
    wp3b_applyEffects_frame run { publications := [(chan, message)] }
  refine ⟨midNext, ?_, ?_, samePending, sameAttributes, sameCounters, sameStatus, sameOutputs⟩
  · rw [threadsNext]
    exact wp3b_wakeThread_threads lConfig thread.core.tid outcome
  · intro candidate isCandidate wokenOutcome isWoken
    by_cases isOwn : candidate.core.tid = thread.core.tid
    · have isSame : candidate = thread :=
        eq_of_nodup_map (fun other => other.core.tid) lConfig.threads mid.threadIdsNodup
          isCandidate isThread isOwn
      rw [isSame]
      exact ⟨on, since, isBlocked⟩
    · simp [isOwn] at isWoken

/-- A token for a mutex that is not unlocked and that no thread waits for:
the mutex becomes unlocked. This is `newMutex`, and a release with no waiter. -/
theorem wp3b_effect_token (mid : MidState program lConfig dexConfig run tid stepId)
    (mutex : MutexId) (isNotUnlocked : lConfig.mutexOwners.get? mutex ≠ some none)
    (noLocker : ∀ thread, thread ∈ lConfig.threads → ∀ since,
      thread.phase ≠ .blocked (.lock mutex) since) :
    MidState program { lConfig with mutexOwners := lConfig.mutexOwners.set mutex none }
      (withRun dexConfig (publishedRun run (Chan.mutexToken mutex) DexVal.token))
      (publishedRun run (Chan.mutexToken mutex) DexVal.token) tid stepId := by
  have wasEmpty : run.pendingMessages (Chan.mutexToken mutex) = [] := by
    apply List.length_eq_zero_iff.mp
    rw [mid.tokens mutex]
    simp [isNotUnlocked]
  obtain ⟨midNext, pendingNext, sameAttributes, sameCounters, sameStatus, sameOutputs⟩ :=
    wp3b_publish_one_noWaiter mid.threads (Chan.mutexToken mutex) DexVal.token
      (by
        intro thread isThread on since isBlocked isNamed
        have isLock : on = .lock mutex := by
          rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;> simp_all [expectedWaitShape]
        rw [isLock] at isBlocked
        exact noLocker thread isThread since isBlocked)
      { lConfig with mutexOwners := lConfig.mutexOwners.set mutex none } rfl rfl
  refine ⟨midNext, ?_, ?_⟩
  · exact mid.data.transfer _ _ (fun _ => none)
      (by exact (wp3b_map_wakeThreadBy_none lConfig.threads).symm)
      (fun _ _ _ isWoken => by cases isWoken) sameStatus sameOutputs sameCounters sameAttributes
      rfl rfl rfl rfl (fun chan => by simp [pendingNext])
      (Or.inl ⟨fun thread => by simp [pendingNext], rfl⟩)
      (Or.inl ⟨fun cv waiter => by simp [pendingNext], rfl⟩)
      (Or.inl ⟨fun thread => by simp [pendingNext], rfl⟩)
  · intro other
    rw [pendingNext]
    by_cases isMutex : other = mutex
    · subst isMutex
      simp [wasEmpty, AssocList.get?_set_self]
    · have isOtherChan : Chan.mutexToken other ≠ Chan.mutexToken mutex := by
        simpa using isMutex
      simp only [isOtherChan, ↓reduceIte]
      show _ = if (lConfig.mutexOwners.set mutex none).get? other = some none then 1 else 0
      rw [AssocList.get?_set_other _ _ _ _ isMutex]
      exact mid.tokens other

/-- A release of a mutex: its first blocked locker becomes the owner, or it
becomes unlocked (spec §8.3 "Mutex"). -/
theorem wp3b_effect_release (mid : MidState program lConfig dexConfig run tid stepId)
    (mutex : MutexId) (isNotUnlocked : lConfig.mutexOwners.get? mutex ≠ some none) :
    MidState program (lConfig.releaseMutex mutex)
      (withRun dexConfig (publishedRun run (Chan.mutexToken mutex) DexVal.token))
      (publishedRun run (Chan.mutexToken mutex) DexVal.token) tid stepId ∧
    OwnKept lConfig (lConfig.releaseMutex mutex) tid := by
  suffices isGeneral : ∀ lNext, lNext = lConfig.releaseMutex mutex →
      MidState program lNext
        (withRun dexConfig (publishedRun run (Chan.mutexToken mutex) DexVal.token))
        (publishedRun run (Chan.mutexToken mutex) DexVal.token) tid stepId ∧
      OwnKept lConfig lNext tid from isGeneral _ rfl
  intro lNext isNext
  have lockOfChan : ∀ self on, Chan.mutexToken mutex ∈ (expectedWaitShape self on).2.2 →
      on = .lock mutex := by
    intro self on isNamed
    rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;> simp_all [expectedWaitShape]
  have wasEmpty : run.pendingMessages (Chan.mutexToken mutex) = [] := by
    apply List.length_eq_zero_iff.mp
    rw [mid.tokens mutex]
    simp [isNotUnlocked]
  simp only [L.Config.releaseMutex] at isNext
  split at isNext
  · rename_i next earliestCase
    obtain ⟨thread, on, since, isThread, isNextThread, isBlocked, isWanted, isEarliest⟩ :=
      wp3b_earliestBlocked_some lConfig _ next earliestCase
    have isLock : on = .lock mutex := by
      cases on <;> simp_all
    subst isLock
    subst isNextThread
    have fits := mid.threads.blockedFit thread isThread _ since isBlocked
    unfold Thread.PhaseFitsCode at fits
    simp only [isBlocked, BlockedOn.toWaitRequest?] at fits
    obtain ⟨midNext, threadsNext, wokenWereBlocked, samePending, sameAttributes, sameCounters,
      sameStatus, sameOutputs⟩ := wp3b_wake_one mid.threads (Chan.mutexToken mutex) DexVal.token
        thread (.lock mutex) since .granted isThread isBlocked (by simp [expectedWaitShape])
        (by
          intro other isOther otherOn otherSince otherBlocked otherNamed
          have otherLock := lockOfChan _ _ otherNamed
          subst otherLock
          exact isEarliest other isOther _ otherSince otherBlocked (by simp))
        (by
          simp only [expectedWaitShape, List.map_cons, List.map_nil, ↓reduceIte]
          exact wp3b_decode_lock thread mutex fits.1 fits.2 _ _)
        lNext (by rw [isNext]; rfl) (by rw [isNext]; rfl)
    refine ⟨⟨midNext, ?_, ?_⟩, wp3b_ownKept_of_wake mid.threads lNext _ threadsNext
      wokenWereBlocked⟩
    · exact mid.data.transfer _ _ _ threadsNext wokenWereBlocked sameStatus sameOutputs
        sameCounters sameAttributes (by rw [isNext]; rfl) (by rw [isNext]; rfl)
        (by rw [isNext]; rfl) (by rw [isNext]; rfl) (fun chan => samePending _)
        (Or.inl ⟨fun joined => samePending _, by rw [isNext]; rfl⟩)
        (Or.inl ⟨fun cv waiter => samePending _, by rw [isNext]; rfl⟩)
        (Or.inl ⟨fun target => samePending _, by rw [isNext]; rfl⟩)
    · intro other
      rw [samePending, isNext]
      show _ = if (lConfig.mutexOwners.set mutex (some thread.core.tid)).get? other = some none
        then 1 else 0
      by_cases isMutex : other = mutex
      · subst isMutex
        simp [wasEmpty, AssocList.get?_set_self]
      · rw [AssocList.get?_set_other _ _ _ _ isMutex]
        exact mid.tokens other
  · rename_i earliestCase
    have noLocker : ∀ thread, thread ∈ lConfig.threads → ∀ since,
        thread.phase ≠ .blocked (.lock mutex) since := by
      intro thread isThread since isBlocked
      have isNotWanted := wp3b_earliestBlocked_none lConfig _ earliestCase thread isThread _ since
        isBlocked
      simp at isNotWanted
    rw [isNext]
    exact ⟨wp3b_effect_token mid mutex isNotUnlocked noLocker, wp3b_ownKept_of_eq _ _ tid rfl⟩

/-- The wake of one dequeued waiter of a condition variable: the waiter
continues if it has parked; otherwise the wake waits for it (spec §8.3
`signal`). -/
theorem wp3b_effect_wakeCvWaiter (mid : MidState program lConfig dexConfig run tid stepId)
    (cv : CvId) (waiter : Tid) :
    MidState program (lConfig.wakeCvWaiter cv waiter)
      (withRun dexConfig (publishedRun run (Chan.cvWake cv waiter) DexVal.token))
      (publishedRun run (Chan.cvWake cv waiter) DexVal.token) tid stepId ∧
    OwnKept lConfig (lConfig.wakeCvWaiter cv waiter) tid := by
  have parkOfChan : ∀ self on, Chan.cvWake cv waiter ∈ (expectedWaitShape self on).2.2 →
      on = .park cv ∧ self = waiter := by
    intro self on isNamed
    rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;> simp_all [expectedWaitShape]
  by_cases isParked : ∃ thread since, lConfig.findThread? waiter = some thread ∧
      thread.phase = .blocked (.park cv) since
  · obtain ⟨thread, since, isFound, isBlocked⟩ := isParked
    obtain ⟨isThread, hasId⟩ := wp3b_findThread?_some lConfig waiter thread isFound
    have isNext : lConfig.wakeCvWaiter cv waiter = lConfig.wakeThread thread.core.tid .granted := by
      simp [L.Config.wakeCvWaiter, isFound, isBlocked, hasId]
    have fits := mid.threads.blockedFit thread isThread _ since isBlocked
    unfold Thread.PhaseFitsCode at fits
    simp only [isBlocked, BlockedOn.toWaitRequest?] at fits
    obtain ⟨midNext, threadsNext, wokenWereBlocked, samePending, sameAttributes, sameCounters,
      sameStatus, sameOutputs⟩ := wp3b_wake_one mid.threads (Chan.cvWake cv waiter) DexVal.token
        thread (.park cv) since .granted isThread isBlocked (by simp [expectedWaitShape, hasId])
        (by
          intro other isOther otherOn otherSince otherBlocked otherNamed
          obtain ⟨otherPark, otherId⟩ := parkOfChan _ _ otherNamed
          have isSame : other = thread :=
            eq_of_nodup_map (fun candidate => candidate.core.tid) lConfig.threads
              mid.threads.threadIdsNodup isOther isThread (otherId.trans hasId.symm)
          subst isSame
          rw [isBlocked] at otherBlocked
          cases otherBlocked
          exact Nat.le_refl _)
        (by
          simp only [expectedWaitShape, List.map_cons, List.map_nil, hasId, ↓reduceIte]
          exact wp3b_decode_park thread cv fits.1 fits.2 _ _)
        (lConfig.wakeCvWaiter cv waiter) (by rw [isNext]; rfl) (by rw [isNext])
    refine ⟨⟨midNext, ?_, ?_⟩, wp3b_ownKept_of_wake mid.threads _ _ threadsNext wokenWereBlocked⟩
    · exact mid.data.transfer _ _ _ threadsNext wokenWereBlocked sameStatus sameOutputs
        sameCounters sameAttributes (by rw [isNext]; rfl) (by rw [isNext]; rfl)
        (by rw [isNext]; rfl) (by rw [isNext]; rfl) (fun chan => samePending _)
        (Or.inl ⟨fun joined => samePending _, by rw [isNext]; rfl⟩)
        (Or.inl ⟨fun otherCv otherWaiter => samePending _, by rw [isNext]; rfl⟩)
        (Or.inl ⟨fun target => samePending _, by rw [isNext]; rfl⟩)
    · intro mutex
      rw [samePending, isNext]
      exact mid.tokens mutex
  · have isNext : lConfig.wakeCvWaiter cv waiter =
        { lConfig with cvWakes := lConfig.cvWakes ++ [(cv, waiter)] } := by
      cases foundCase : lConfig.findThread? waiter with
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
    obtain ⟨midNext, pendingNext, sameAttributes, sameCounters, sameStatus, sameOutputs⟩ :=
      wp3b_publish_one_noWaiter mid.threads (Chan.cvWake cv waiter) DexVal.token
        (by
          intro thread isThread on since isBlocked isNamed
          obtain ⟨isPark, hasId⟩ := parkOfChan _ _ isNamed
          subst isPark
          apply isParked
          refine ⟨thread, since, ?_, isBlocked⟩
          rw [← hasId]
          exact wp3b_findThread?_of_mem lConfig thread mid.threads.threadIdsNodup isThread)
        (lConfig.wakeCvWaiter cv waiter) (by rw [isNext]) (by rw [isNext])
    refine ⟨⟨midNext, ?_, ?_⟩, wp3b_ownKept_of_eq _ _ tid (by rw [isNext])⟩
    · exact mid.data.transfer _ _ (fun _ => none)
        (by rw [isNext]; exact (wp3b_map_wakeThreadBy_none lConfig.threads).symm)
        (fun _ _ _ isWoken => by cases isWoken) sameStatus sameOutputs sameCounters
        sameAttributes (by rw [isNext]) (by rw [isNext]) (by rw [isNext]) (by rw [isNext])
        (fun chan => by simp [pendingNext])
        (Or.inl ⟨fun joined => by simp [pendingNext], by rw [isNext]⟩)
        (Or.inr (by
          intro otherCv otherWaiter
          rw [pendingNext, isNext]
          show _ = (lConfig.cvWakes ++ [(cv, waiter)]).count (otherCv, otherWaiter)
          rw [List.count_append]
          by_cases isSame : Chan.cvWake otherCv otherWaiter = Chan.cvWake cv waiter
          · obtain ⟨sameCv, sameWaiter⟩ : otherCv = cv ∧ otherWaiter = waiter := by
              simpa using isSame
            subst sameCv
            subst sameWaiter
            simp [mid.data.cvWakes]
          · have isOtherPair : ¬ ((cv, waiter) = (otherCv, otherWaiter)) := by
              intro samePair
              apply isSame
              cases samePair
              rfl
            simp [isSame, mid.data.cvWakes, List.count_cons, isOtherPair]))
        (Or.inl ⟨fun target => by simp [pendingNext], by rw [isNext]⟩)
    · intro mutex
      rw [pendingNext, isNext]
      simp only [reduceCtorEq, ↓reduceIte]
      exact mid.tokens mutex

end DexExpressiveness
