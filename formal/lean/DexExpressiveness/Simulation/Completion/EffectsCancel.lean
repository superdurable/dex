import DexExpressiveness.Simulation.Completion.Effects

/-!
# `effects` of `cancel(t)` and of the end of a thread

- `wp3b_effect_cancel`: `cancel(t)` sets the flag and publishes to
  `cancel/t`; a thread blocked at a cancellation point observes it at once
  (spec §8.1, §8.3).
- `wp3b_effect_join_some`, `wp3b_effect_join_none`: the last Step of a thread
  publishes to `join/t`; the first thread blocked in `join(t)` continues, or
  the thread stays joinable.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}
  {run : Dex.Run signature} {tid : Tid} {stepId : Dex.StepExecId signature}

/-- The configuration with the cancel flag of `target` set. -/
def flagThread (config : L.Config) (target : Tid) : L.Config :=
  { config with
    cancelFlags :=
      if config.cancelFlags.contains target then config.cancelFlags
      else config.cancelFlags ++ [target] }

/-- The flags after `flagThread`. -/
theorem wp3b_contains_flagThread (config : L.Config) (target other : Tid) :
    (flagThread config target).cancelFlags.contains other = true ↔
      config.cancelFlags.contains other = true ∨ other = target := by
  unfold flagThread
  by_cases isFlagged : config.cancelFlags.contains target = true
  · simp only [isFlagged, ↓reduceIte]
    constructor
    · exact Or.inl
    · rintro (isOld | isTarget)
      · exact isOld
      · rw [isTarget]
        exact isFlagged
  · simp only [isFlagged, Bool.false_eq_true, ↓reduceIte]
    simp

/-- `cancel(t)`: the flag is set and a request is published to `cancel/t`. A
thread blocked in `recv`, `join`, or a timed operation is decided by it. -/
theorem wp3b_effect_cancel (mid : MidState program lConfig dexConfig run tid stepId)
    (target : Tid) :
    MidState program ((flagThread lConfig target).cancelBlockedThread target)
      (withRun dexConfig (publishedRun run (Chan.cancelRequest target) DexVal.token))
      (publishedRun run (Chan.cancelRequest target) DexVal.token) tid stepId ∧
    OwnKept lConfig ((flagThread lConfig target).cancelBlockedThread target) tid := by
  have selfOfChan : ∀ self on, Chan.cancelRequest target ∈ (expectedWaitShape self on).2.2 →
      self = target := by
    intro self on isNamed
    rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;> simp_all [expectedWaitShape]
  by_cases isAtPoint : ∃ thread on since, lConfig.findThread? target = some thread ∧
      thread.phase = .blocked on since ∧
      Chan.cancelRequest target ∈ (expectedWaitShape target on).2.2
  · obtain ⟨thread, on, since, isFound, isBlocked, isNamed⟩ := isAtPoint
    obtain ⟨isThread, hasId⟩ := mem_of_findThread? isFound
    have isFoundFlagged : (flagThread lConfig target).findThread? target = some thread := isFound
    have isNext : (flagThread lConfig target).cancelBlockedThread target =
        (flagThread lConfig target).wakeThread target .canceled := by
      rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;>
        first
          | (simp [expectedWaitShape] at isNamed; done)
          | simp [L.Config.cancelBlockedThread, isFoundFlagged, isBlocked]
    subst hasId
    have fits := mid.threads.blockedFit thread isThread on since isBlocked
    obtain ⟨midNext, threadsNext, wokenWereBlocked, samePending, sameAttributes, sameCounters,
      sameStatus, sameOutputs⟩ := wp3b_wake_one mid.threads (Chan.cancelRequest thread.core.tid)
        DexVal.token thread on since .canceled isThread isBlocked isNamed
        (by
          intro other isOther otherOn otherSince otherBlocked otherNamed
          have isSame : other = thread :=
            eq_of_nodup_map (fun candidate => candidate.core.tid) lConfig.threads
              mid.threads.threadIdsNodup isOther isThread (selfOfChan _ _ otherNamed)
          subst isSame
          rw [isBlocked] at otherBlocked
          cases otherBlocked
          exact Nat.le_refl _)
        (wp3b_decode_canceled thread on since isBlocked fits isNamed DexVal.token)
        ((flagThread lConfig thread.core.tid).cancelBlockedThread thread.core.tid)
        (by rw [isNext]; rfl) (by rw [isNext]; rfl)
    -- First the wake, with the old flags; then the new flag.
    have wokenData : MidData (lConfig.wakeThread thread.core.tid .canceled)
        (publishedRun run (Chan.cancelRequest thread.core.tid) DexVal.token) tid :=
      mid.data.transfer _ _ _ (wp3b_wakeThread_threads lConfig thread.core.tid .canceled)
        wokenWereBlocked sameStatus sameOutputs sameCounters sameAttributes rfl rfl rfl rfl
        (fun chan => samePending _) (Or.inl ⟨fun joined => samePending _, rfl⟩)
        (Or.inl ⟨fun cv waiter => samePending _, rfl⟩) (Or.inl ⟨fun other => samePending _, rfl⟩)
    have wokenFound : (lConfig.wakeThread thread.core.tid .canceled).findThread? thread.core.tid =
        some { thread with phase := .woken .canceled } := by
      rw [wp3b_findThread?_wake lConfig _ _
        (wp3b_wakeThread_threads lConfig thread.core.tid .canceled), isFound]
      simp [wakeThreadBy]
    have isOtherThread : thread.core.tid ≠ tid := fun isOwn =>
      mid.threads.ownThread thread isThread isOwn on since isBlocked
    refine ⟨⟨midNext, ?_, ?_⟩, wp3b_ownKept_of_wake mid.threads _ _ threadsNext wokenWereBlocked⟩
    · rw [isNext]
      exact wokenData.transfer _ _ (fun _ => none)
        (by exact (wp3b_map_wakeThreadBy_none _).symm) (fun _ _ _ isWoken => by cases isWoken)
        rfl rfl rfl rfl rfl rfl rfl rfl (fun chan => rfl) (Or.inl ⟨fun joined => rfl, rfl⟩)
        (Or.inl ⟨fun cv waiter => rfl, rfl⟩)
        (Or.inr ⟨
          (by
            intro other hasRequest
            exact (wp3b_contains_flagThread lConfig thread.core.tid other).mpr
              (Or.inl (wokenData.cancelRequestsAreFlags other hasRequest))),
          (by
            intro other isOther isFlagged
            rcases (wp3b_contains_flagThread lConfig thread.core.tid other).mp isFlagged with
              isOld | isTarget
            · exact wokenData.cancelFlagsAreRequests other isOther isOld
            · rw [isTarget]
              exact Or.inr (Or.inl ⟨_, wokenFound, Or.inl rfl⟩)),
          (by
            intro isFlagged
            rcases (wp3b_contains_flagThread lConfig thread.core.tid tid).mp isFlagged with
              isOld | isTarget
            · exact wokenData.ownFlag isOld
            · exact absurd isTarget.symm isOtherThread)⟩)
    · intro mutex
      rw [samePending, isNext]
      exact mid.tokens mutex
  · have isNext : (flagThread lConfig target).cancelBlockedThread target =
        flagThread lConfig target := by
      cases foundCase : lConfig.findThread? target with
      | none =>
        have notFound : (flagThread lConfig target).findThread? target = none := foundCase
        simp [L.Config.cancelBlockedThread, notFound]
      | some thread =>
        have isFoundFlagged : (flagThread lConfig target).findThread? target = some thread :=
          foundCase
        cases phaseCase : thread.phase with
        | blocked on since =>
          rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;>
            first
              | (simp [L.Config.cancelBlockedThread, isFoundFlagged, phaseCase]; done)
              | (exact absurd ⟨thread, _, since, foundCase, phaseCase,
                  by simp [expectedWaitShape]⟩ isAtPoint)
        | _ => simp [L.Config.cancelBlockedThread, isFoundFlagged, phaseCase]
    obtain ⟨midNext, pendingNext, sameAttributes, sameCounters, sameStatus, sameOutputs⟩ :=
      wp3b_publish_one_noWaiter mid.threads (Chan.cancelRequest target) DexVal.token
        (by
          intro thread isThread on since isBlocked isNamed
          have hasId := selfOfChan _ _ isNamed
          apply isAtPoint
          refine ⟨thread, on, since, ?_, isBlocked, by rw [hasId] at isNamed; exact isNamed⟩
          rw [← hasId]
          exact findThread?_of_mem_of_nodup lConfig thread mid.threads.threadIdsNodup isThread)
        ((flagThread lConfig target).cancelBlockedThread target) (by rw [isNext]; rfl)
        (by rw [isNext]; rfl)
    have otherPending : ∀ other, other ≠ target →
        (publishedRun run (Chan.cancelRequest target) DexVal.token).pendingMessages
          (Chan.cancelRequest other) = run.pendingMessages (Chan.cancelRequest other) := by
      intro other isOther
      rw [pendingNext]
      simp [isOther]
    have targetPending :
        (publishedRun run (Chan.cancelRequest target) DexVal.token).pendingMessages
          (Chan.cancelRequest target) ≠ [] := by
      rw [pendingNext]
      simp
    refine ⟨⟨midNext, ?_, ?_⟩, wp3b_ownKept_of_eq _ _ tid (by rw [isNext]; rfl)⟩
    · rw [isNext]
      exact mid.data.transfer _ _ (fun _ => none)
        (by exact (wp3b_map_wakeThreadBy_none lConfig.threads).symm)
        (fun _ _ _ isWoken => by cases isWoken) sameStatus sameOutputs sameCounters
        sameAttributes rfl rfl rfl rfl (fun chan => by simp [pendingNext])
        (Or.inl ⟨fun joined => by simp [pendingNext], rfl⟩)
        (Or.inl ⟨fun cv waiter => by simp [pendingNext], rfl⟩)
        (Or.inr ⟨
          (by
            intro other hasRequest
            apply (wp3b_contains_flagThread lConfig target other).mpr
            by_cases isTarget : other = target
            · exact Or.inr isTarget
            · rw [otherPending other isTarget] at hasRequest
              exact Or.inl (mid.data.cancelRequestsAreFlags other hasRequest)),
          (by
            intro other isOther isFlagged
            by_cases isTarget : other = target
            · rw [isTarget]
              exact Or.inl targetPending
            · rw [otherPending other isTarget]
              rcases (wp3b_contains_flagThread lConfig target other).mp isFlagged with
                isOld | isSame
              · exact mid.data.cancelFlagsAreRequests other isOther isOld
              · exact absurd isSame isTarget),
          (by
            intro isFlagged
            by_cases isTarget : tid = target
            · rw [isTarget]
              exact Or.inl targetPending
            · rw [otherPending tid isTarget]
              rcases (wp3b_contains_flagThread lConfig target tid).mp isFlagged with
                isOld | isSame
              · exact mid.data.ownFlag isOld
              · exact absurd isSame isTarget)⟩)
    · intro mutex
      rw [pendingNext, isNext]
      simp only [reduceCtorEq, ↓reduceIte]
      exact mid.tokens mutex

/-- The end of a thread with a thread blocked in `join` on it: the first such
thread continues. -/
theorem wp3b_effect_join_some (mid : MidState program lConfig dexConfig run tid stepId)
    (joined : Tid) (isWanted : BlockedOn → Bool)
    (wantedSpec : ∀ on, isWanted on = true ↔ on = .join joined) (joiner : Tid)
    (earliestCase : lConfig.earliestBlocked isWanted = some joiner) :
    MidState program (lConfig.wakeThread joiner .granted)
      (withRun dexConfig (publishedRun run (Chan.joinDone joined) DexVal.token))
      (publishedRun run (Chan.joinDone joined) DexVal.token) tid stepId ∧
    OwnKept lConfig (lConfig.wakeThread joiner .granted) tid := by
  have joinOfChan : ∀ self on, Chan.joinDone joined ∈ (expectedWaitShape self on).2.2 →
      on = .join joined := by
    intro self on isNamed
    rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;> simp_all [expectedWaitShape]
  obtain ⟨thread, on, since, isThread, isJoiner, isBlocked, isWantedOn, isEarliest⟩ :=
    exists_of_earliestBlocked_some lConfig isWanted joiner earliestCase
  have isJoin := (wantedSpec on).mp isWantedOn
  subst isJoin
  subst isJoiner
  have fits := mid.threads.blockedFit thread isThread _ since isBlocked
  unfold Thread.PhaseFitsCode at fits
  simp only [isBlocked, BlockedOn.toWaitRequest?] at fits
  obtain ⟨midNext, threadsNext, wokenWereBlocked, samePending, sameAttributes, sameCounters,
    sameStatus, sameOutputs⟩ := wp3b_wake_one mid.threads (Chan.joinDone joined) DexVal.token
      thread (.join joined) since .granted isThread isBlocked (by simp [expectedWaitShape])
      (by
        intro other isOther otherOn otherSince otherBlocked otherNamed
        have otherJoin := joinOfChan _ _ otherNamed
        subst otherJoin
        exact isEarliest other isOther _ otherSince otherBlocked ((wantedSpec _).mpr rfl))
      (by
        simp only [expectedWaitShape, List.map_cons, List.map_nil, ↓reduceIte, reduceCtorEq]
        exact wp3b_decode_join thread joined fits.1 fits.2 _ _)
      (lConfig.wakeThread thread.core.tid .granted) rfl rfl
  refine ⟨⟨midNext, ?_, ?_⟩, wp3b_ownKept_of_wake mid.threads _ _ threadsNext wokenWereBlocked⟩
  · exact mid.data.transfer _ _ _ threadsNext wokenWereBlocked sameStatus sameOutputs
      sameCounters sameAttributes rfl rfl rfl rfl (fun chan => samePending _)
      (Or.inl ⟨fun other => samePending _, rfl⟩) (Or.inl ⟨fun cv waiter => samePending _, rfl⟩)
      (Or.inl ⟨fun target => samePending _, rfl⟩)
  · intro mutex
    rw [samePending]
    exact mid.tokens mutex

/-- The end of a thread that no thread waits for: it becomes joinable. -/
theorem wp3b_effect_join_none (mid : MidState program lConfig dexConfig run tid stepId)
    (joined : Tid) (isNotJoinable : lConfig.joinable.contains joined = false)
    (isWanted : BlockedOn → Bool) (wantedSpec : ∀ on, isWanted on = true ↔ on = .join joined)
    (earliestCase : lConfig.earliestBlocked isWanted = none) :
    MidState program { lConfig with joinable := lConfig.joinable ++ [joined] }
      (withRun dexConfig (publishedRun run (Chan.joinDone joined) DexVal.token))
      (publishedRun run (Chan.joinDone joined) DexVal.token) tid stepId := by
  have joinOfChan : ∀ self on, Chan.joinDone joined ∈ (expectedWaitShape self on).2.2 →
      on = .join joined := by
    intro self on isNamed
    rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;> simp_all [expectedWaitShape]
  have wasEmpty : run.pendingMessages (Chan.joinDone joined) = [] := by
    apply List.length_eq_zero_iff.mp
    have isNotMember : joined ∉ lConfig.joinable := by simpa using isNotJoinable
    rw [mid.data.joinTokens joined]
    simp [isNotMember]
  obtain ⟨midNext, pendingNext, sameAttributes, sameCounters, sameStatus, sameOutputs⟩ :=
    wp3b_publish_one_noWaiter mid.threads (Chan.joinDone joined) DexVal.token
      (by
        intro thread isThread on since isBlocked isNamed
        have isJoin := joinOfChan _ _ isNamed
        have isNotWanted := not_isWanted_of_earliestBlocked_none lConfig isWanted earliestCase thread isThread
          on since isBlocked
        rw [(wantedSpec on).mpr isJoin] at isNotWanted
        cases isNotWanted)
      { lConfig with joinable := lConfig.joinable ++ [joined] } rfl rfl
  refine ⟨midNext, ?_, ?_⟩
  · exact mid.data.transfer _ _ (fun _ => none)
      (by exact (wp3b_map_wakeThreadBy_none lConfig.threads).symm)
      (fun _ _ _ isWoken => by cases isWoken) sameStatus sameOutputs sameCounters sameAttributes
      rfl rfl rfl rfl (fun chan => by simp [pendingNext])
      (Or.inr (by
        intro other
        rw [pendingNext]
        show _ = if (lConfig.joinable ++ [joined]).contains other then 1 else 0
        by_cases isJoined : other = joined
        · subst isJoined
          simp [wasEmpty]
        · have sameContains : (lConfig.joinable ++ [joined]).contains other =
              lConfig.joinable.contains other := by
            simp [isJoined]
          simp only [Chan.joinDone.injEq, isJoined, ↓reduceIte, sameContains]
          exact mid.data.joinTokens other))
      (Or.inl ⟨fun cv waiter => by simp [pendingNext], rfl⟩)
      (Or.inl ⟨fun target => by simp [pendingNext], rfl⟩)
  · intro mutex
    rw [pendingNext]
    simp only [reduceCtorEq, ↓reduceIte]
    exact mid.tokens mutex

end DexExpressiveness
