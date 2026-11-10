import DexExpressiveness.Simulation.Completion.Fire
import DexExpressiveness.Simulation.Completion.BroadcastL

/-!
# `broadcast(cv)`: one publication per waiter, one `wake`

`broadcast` publishes to `cv/w` for every waiter `w` of the list and then
wakes them in one `wake` (spec §8.3: "As `signal`, for every waiter in
order"). On the L side the waiters are woken one after the other.
`wp3b_effect_broadcast` shows that both give the same: every thread parked
on `cv` whose ID is in the list continues, and every other entry of the list
stays as an early wake.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The publications of `broadcast(cv)` for the waiters it dequeues. -/
def broadcastPublications (cv : CvId) (waiters : List Tid) : List (Chan × DexVal) :=
  waiters.map (fun waiter => (Chan.cvWake cv waiter, DexVal.token))

/-- The threads that `broadcast(cv)` wakes. -/
def broadcastWoken (cv : CvId) (waiters : List Tid) (thread : L.Thread) : Option WakeOutcome :=
  if parkedOn cv thread && waiters.contains thread.core.tid then some .granted else none

/-- The condition results of the Steps of `steps` that wait. -/
def firedResults (run : Dex.Run signature) (chanOf : Dex.StepExecId signature → Chan)
    (steps : List (Dex.StepExecId signature)) (record : Dex.StepRecord signature) :
    Option (Dex.ConditionResults signature) :=
  if record.id ∈ steps ∧ record.phase = .waiting then
    some { timersDone := []
           channelMessages := [(run.pendingMessages (chanOf record.id)).head?] }
  else none

/-- The pending messages after the publications of `broadcast`. -/
theorem wp3b_pending_broadcast (cv : CvId) (waiters : List Tid) (run : Dex.Run signature) :
    (∀ chan, (∀ waiter, chan ≠ Chan.cvWake cv waiter) →
      (run.applyPublications (broadcastPublications cv waiters)).pendingMessages chan =
        run.pendingMessages chan) ∧
    (∀ waiter,
      ((run.applyPublications (broadcastPublications cv waiters)).pendingMessages
        (Chan.cvWake cv waiter)).length =
        (run.pendingMessages (Chan.cvWake cv waiter)).length + waiters.count waiter) := by
  constructor
  · intro chan isOther
    apply (Dex.Run.applyPublications_frame (broadcastPublications cv waiters) run).2.2.2.2.2.2.2
    intro isPublished
    simp only [broadcastPublications, List.map_map, List.mem_map] at isPublished
    obtain ⟨waiter, _, isChan⟩ := isPublished
    exact isOther waiter isChan.symm
  · intro waiter
    induction waiters generalizing run with
    | nil => rfl
    | cons first rest inductionHypothesis =>
      have isStep : run.applyPublications (broadcastPublications cv (first :: rest)) =
          (run.setPendingMessages (Chan.cvWake cv first)
            (run.pendingMessages (Chan.cvWake cv first) ++ [DexVal.token])).applyPublications
              (broadcastPublications cv rest) := rfl
      rw [isStep, inductionHypothesis]
      by_cases isFirst : waiter = first
      · subst isFirst
        simp only [Dex.Run.pendingMessages, Dex.Run.setPendingMessages, AssocList.get?_set_self,
          Option.getD_some, List.length_append, List.length_singleton, List.count_cons_self]
        omega
      · have isOtherChan : Chan.cvWake cv waiter ≠ Chan.cvWake cv first := by simpa using isFirst
        have isOtherHead : ¬ (first = waiter) := fun isSame => isFirst isSame.symm
        simp [Dex.Run.pendingMessages, Dex.Run.setPendingMessages,
          AssocList.get?_set_other _ _ _ _ isOtherChan, List.count_cons, isOtherHead]

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}
  {run : Dex.Run signature} {tid : Tid} {stepId : Dex.StepExecId signature}

/-- `broadcast(cv)`: every waiter of the list that has parked continues; the
wake of every other waiter of the list waits for it. -/
theorem wp3b_effect_broadcast (mid : MidState program lConfig dexConfig run tid stepId)
    (cv : CvId) (waiters : List Tid) :
    MidState program
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) lConfig)
      (withRun dexConfig
        (run.applyEffects { publications := broadcastPublications cv waiters }))
      (run.applyEffects { publications := broadcastPublications cv waiters }) tid stepId ∧
    OwnKept lConfig
      (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) lConfig) tid := by
  obtain ⟨publishedSteps, publishedCounters, publishedTimers, _, _, _, _, _⟩ :=
    Dex.Run.applyPublications_frame (broadcastPublications cv waiters) run
  obtain ⟨pendingOther, pendingWake⟩ := wp3b_pending_broadcast cv waiters run
  have idsNodup := mid.threads.runInvariant.idsNodup
  have sameRecord : ∀ first, first ∈ run.steps → ∀ second, second ∈ run.steps →
      first.id = second.id → first = second := fun first isFirst second isSecond sameId =>
    eq_of_nodup_map (fun candidate => candidate.id) run.steps idsNodup isFirst isSecond sameId
  have sameCarrier : ∀ first, first ∈ lConfig.threads → ∀ second, second ∈ lConfig.threads →
      ∀ record, StepCarriesThread program first record →
      StepCarriesThread program second record → first = second := by
    intro first isFirst second isSecond record firstCarries secondCarries
    apply eq_of_nodup_map (fun candidate => candidate.core.tid) lConfig.threads
      mid.threads.threadIdsNodup isFirst isSecond
    show first.core.tid = second.core.tid
    rw [← threadOfInput_of_carries_of_booted program first record (mid.threads.noBoot first isFirst).1
        (mid.threads.noBoot first isFirst).2 firstCarries,
      threadOfInput_of_carries_of_booted program second record (mid.threads.noBoot second isSecond).1
        (mid.threads.noBoot second isSecond).2 secondCarries]
  have parkOfChan : ∀ self on waiter, Chan.cvWake cv waiter ∈ (expectedWaitShape self on).2.2 →
      on = .park cv ∧ self = waiter := by
    intro self on waiter isNamed
    rcases on with _ | _ | _ | _ | ⟨_, _, _ | _⟩ <;> simp_all [expectedWaitShape]
  let chanOf : Dex.StepExecId signature → Chan := fun step =>
    match run.findStep? step with
    | some record =>
      match record.waitState with
      | some waitState => waitState.wait.channels.headD (Chan.external 0)
      | none => Chan.external 0
    | none => Chan.external 0
  -- A waiting Step that `wake` evaluates carries a thread parked on `cv`.
  have parkedOfWaiter : ∀ record, record ∈ run.steps →
      record.id ∈ (run.applyPublications (broadcastPublications cv waiters)).waitersOn
        ((broadcastPublications cv waiters).map (fun publication => publication.1)) →
      record.phase = .waiting →
      ∃ thread since waitState, thread ∈ lConfig.threads ∧
        thread.phase = .blocked (.park cv) since ∧ thread.core.tid ∈ waiters ∧
        StepCarriesThread program thread record ∧ record.waitState = some waitState ∧
        waitState.wait =
          { kind := .all, timerSeconds := [], channels := [Chan.cvWake cv thread.core.tid] } ∧
        waitState.timersDone = [] ∧ chanOf record.id = Chan.cvWake cv thread.core.tid := by
    intro record isRecord isWaiter isWaiting
    simp only [Dex.Run.waitersOn, List.mem_map] at isWaiter
    obtain ⟨pair, isSorted, isPair⟩ := isWaiter
    obtain ⟨other, isOther, isCandidate⟩ :=
      List.mem_filterMap.mp ((mem_sortStable _ _ _).mp isSorted)
    rw [publishedSteps] at isOther
    cases phaseCase : other.phase <;> simp only [phaseCase] at isCandidate <;>
      try (cases isCandidate)
    cases waitCase : other.waitState with
    | none => simp [waitCase] at isCandidate
    | some otherWait =>
      simp only [waitCase] at isCandidate
      split at isCandidate
      · rename_i namesChanged
        have isOtherPair : (otherWait.registeredSeq, other.id) = pair :=
          Option.some.inj isCandidate
        have sameId : other.id = record.id := by rw [← isPair, ← isOtherPair]
        have isSame := sameRecord other isOther record isRecord sameId
        subst isSame
        obtain ⟨chan, isChan, isChanged⟩ := List.any_eq_true.mp namesChanged
        have isPublished : ∃ waiter, waiter ∈ waiters ∧ Chan.cvWake cv waiter = chan := by
          simpa [broadcastPublications] using isChanged
        obtain ⟨waiter, isWaiterMember, isWake⟩ := isPublished
        obtain ⟨thread, on, since, isThread, isBlocked, carries, isMatch⟩ :=
          mid.threads.blockedOfWaiting other isOther isWaiting
        obtain ⟨_, _, found, foundWait, hasKind, hasLength, hasChannels, hasDone⟩ :=
          waiting_of_phaseMatchesStep program lConfig dexConfig thread other on since isBlocked isMatch
        rw [waitCase] at foundWait
        cases foundWait
        rw [hasChannels, ← isWake] at isChan
        obtain ⟨isPark, hasTid⟩ := parkOfChan _ _ _ isChan
        subst isPark
        subst hasTid
        have isWait : otherWait.wait =
            { kind := .all, timerSeconds := []
              channels := [Chan.cvWake cv thread.core.tid] } := by
          rcases waitShape : otherWait.wait with ⟨kind, timerSeconds, channels⟩
          rw [waitShape] at hasKind hasLength hasChannels
          simp only [expectedWaitShape] at hasKind hasLength hasChannels
          rw [hasKind, hasChannels, List.length_eq_zero_iff.mp hasLength]
        refine ⟨thread, since, otherWait, isThread, isBlocked, isWaiterMember, carries, waitCase,
          isWait, ?_, ?_⟩
        · rw [hasDone, isWait]
          rfl
        · have isFound : run.findStep? other.id = some other :=
            Dex.Run.findStep?_of_mem_nodup run other isOther idsNodup
          simp [chanOf, isFound, waitCase, isWait]
      · cases isCandidate
  -- The Step of a thread parked on `cv` whose ID is in the list is evaluated.
  have waiterOfParked : ∀ thread, thread ∈ lConfig.threads → ∀ since,
      thread.phase = .blocked (.park cv) since → thread.core.tid ∈ waiters →
      ∀ record, record ∈ run.steps → record.phase.isActive = true →
      StepCarriesThread program thread record →
      record.id ∈ (run.applyPublications (broadcastPublications cv waiters)).waitersOn
        ((broadcastPublications cv waiters).map (fun publication => publication.1)) ∧
      record.phase = .waiting := by
    intro thread isThread since isBlocked isWaiterMember record isRecord isActive carries
    obtain ⟨found, waitState, isFound, foundActive, foundCarries, isWaiting, hasWait, _, _,
      hasChannels, _⟩ := mid.threads.stepOfBlocked thread isThread _ since isBlocked
    have isSame := mid.threads.stepOfThread_unique thread isThread record found isRecord isFound
      isActive foundActive carries foundCarries
    subst isSame
    refine ⟨?_, isWaiting⟩
    apply Dex.Run.mem_waitersOn _ _ record waitState
      ⟨by rw [publishedSteps]; exact isRecord, isWaiting, hasWait⟩ (Chan.cvWake cv thread.core.tid)
    · rw [hasChannels]
      simp [expectedWaitShape]
    · exact List.mem_map.mpr ⟨(Chan.cvWake cv thread.core.tid, DexVal.token),
        List.mem_map.mpr ⟨thread.core.tid, isWaiterMember, rfl⟩, rfl⟩
  have firesAll : FiresAll chanOf
      ((run.applyPublications (broadcastPublications cv waiters)).waitersOn
        ((broadcastPublications cv waiters).map (fun publication => publication.1)))
      (run.applyPublications (broadcastPublications cv waiters)) :=
    { idsNodup := by rw [publishedSteps]; exact idsNodup
      fires := by
        intro record isRecord isWaiter isWaiting
        rw [publishedSteps] at isRecord
        obtain ⟨thread, since, waitState, _, _, isWaiterMember, _, hasWait, isWait, noneDone,
          hasChan⟩ := parkedOfWaiter record isRecord isWaiter isWaiting
        refine ⟨waitState, hasWait, by rw [hasChan]; exact isWait, noneDone, ?_⟩
        rw [hasChan]
        intro isEmpty
        have hasLength := pendingWake thread.core.tid
        rw [isEmpty] at hasLength
        have isCounted : 0 < waiters.count thread.core.tid := List.count_pos_iff.mpr isWaiterMember
        simp only [List.length_nil] at hasLength
        omega
      isInjective := by
        intro first second isFirst isSecond firstWaiter secondWaiter firstWaiting secondWaiting
          sameChan
        rw [publishedSteps] at isFirst isSecond
        obtain ⟨firstThread, _, _, isFirstThread, _, _, firstCarries, _, _, _, firstChan⟩ :=
          parkedOfWaiter first isFirst firstWaiter firstWaiting
        obtain ⟨secondThread, _, _, isSecondThread, _, _, secondCarries, _, _, _, secondChan⟩ :=
          parkedOfWaiter second isSecond secondWaiter secondWaiting
        rw [firstChan, secondChan] at sameChan
        have sameTid : firstThread.core.tid = secondThread.core.tid := by simpa using sameChan
        have isSameThread := eq_of_nodup_map (fun candidate => candidate.core.tid) lConfig.threads
          mid.threads.threadIdsNodup isFirstThread isSecondThread sameTid
        subst isSameThread
        rw [mid.threads.stepOfThread_unique firstThread isFirstThread first second isFirst isSecond
          (by rw [firstWaiting]; rfl) (by rw [secondWaiting]; rfl) firstCarries secondCarries]
      noTimers := by
        intro timer record isRecord hasId isWaiter isWaiting
        rw [publishedSteps] at isRecord
        rw [publishedTimers]
        obtain ⟨thread, since, _, isThread, isBlocked, _, carries, _⟩ :=
          parkedOfWaiter record isRecord isWaiter isWaiting
        cases armedCase : run.timers.get? timer with
        | none => rfl
        | some fireTime =>
          exfalso
          obtain ⟨timed, chan, timedSince, isTimed, timedBlocked, isTimedStep, _⟩ :=
            (mid.threads.timers timer fireTime).mp armedCase
          obtain ⟨timedRecord, isTimedRecord, timedId, _, timedCarries⟩ :=
            mid.threads.timedStep timed isTimed timer fireTime chan timedSince timedBlocked
              isTimedStep
          have isSameRecord := sameRecord timedRecord isTimedRecord record isRecord
            (timedId.trans hasId.symm)
          subst isSameRecord
          have isSameThread := sameCarrier timed isTimed thread isThread timedRecord timedCarries
            carries
          subst isSameThread
          rw [isBlocked] at timedBlocked
          cases timedBlocked }
  obtain ⟨firedSteps, keptPending, firedPending, sameTimers⟩ :=
    wp3b_foldl_evalStep_fire chanOf _ _ firesAll
  have isWake : run.applyEffects { publications := broadcastPublications cv waiters } =
      ((run.applyPublications (broadcastPublications cv waiters)).waitersOn
        ((broadcastPublications cv waiters).map (fun publication => publication.1))).foldl
        Dex.Run.evalStep (run.applyPublications (broadcastPublications cv waiters)) := rfl
  obtain ⟨lThreads, lTaken, lOther, lSame, lCounter, _, _⟩ :=
    wp3b_broadcast_fold cv waiters lConfig mid.threads.threadIdsNodup
  obtain ⟨sameAttributes, sameCounters, sameStatus, sameOutputs⟩ :=
    Dex.Run.applyEffects_keeps_data run { publications := broadcastPublications cv waiters }
  have wokenWereBlocked : ∀ thread, thread ∈ lConfig.threads → ∀ outcome,
      broadcastWoken cv waiters thread = some outcome →
      ∃ on since, thread.phase = .blocked on since := by
    intro thread _ outcome isWoken
    have isParked : parkedOn cv thread = true := by
      unfold broadcastWoken at isWoken
      split at isWoken
      · rename_i isBoth
        exact (Bool.and_eq_true _ _ ▸ isBoth).1
      · cases isWoken
    obtain ⟨since, isBlocked⟩ := (wp3b_parkedOn_iff cv thread).mp isParked
    exact ⟨_, since, isBlocked⟩
  -- The pending messages of the Channels that `broadcast` does not publish to.
  have otherKept : ∀ chan, (∀ waiter, chan ≠ Chan.cvWake cv waiter) →
      (run.applyEffects { publications := broadcastPublications cv waiters }).pendingMessages
        chan = run.pendingMessages chan := by
    intro chan isOther
    rw [isWake, keptPending chan (by
      intro record isRecord isWaiter isWaiting sameChan
      rw [publishedSteps] at isRecord
      obtain ⟨thread, _, _, _, _, _, _, _, _, _, hasChan⟩ :=
        parkedOfWaiter record isRecord isWaiter isWaiting
      exact isOther thread.core.tid (by rw [← sameChan, hasChan])), pendingOther chan isOther]
  refine ⟨⟨?_, ?_, ?_⟩, wp3b_ownKept_of_wake mid.threads _ _ lThreads wokenWereBlocked⟩
  · apply mid.threads.wake (broadcastWoken cv waiters)
      (firedResults (run.applyPublications (broadcastPublications cv waiters)) chanOf
        ((run.applyPublications (broadcastPublications cv waiters)).waitersOn
          ((broadcastPublications cv waiters).map (fun publication => publication.1))))
      _ (withRun dexConfig (run.applyEffects { publications := broadcastPublications cv waiters }))
      (run.applyEffects { publications := broadcastPublications cv waiters }) rfl rfl
      (fun _ _ _ _ _ otherCv _ => by rw [lSame.2.1]) lThreads
    · rw [isWake, firedSteps, publishedSteps]
      apply List.map_congr_left
      intro record _
      unfold wakeRecordBy firedResults
      split <;> rfl
    · exact wokenWereBlocked
    · intro record _ conditionResults isFired
      unfold firedResults at isFired
      split at isFired
      · rename_i isBoth
        exact isBoth.2
      · cases isFired
    · intro thread isThread record isRecord isActive carries on since isBlocked
      by_cases isTarget : parkedOn cv thread = true ∧ thread.core.tid ∈ waiters
      · right
        obtain ⟨parkedSince, isParked⟩ := (wp3b_parkedOn_iff cv thread).mp isTarget.1
        obtain ⟨isWaiter, isWaiting⟩ := waiterOfParked thread isThread parkedSince isParked
          isTarget.2 record isRecord isActive carries
        obtain ⟨_, hasWait, _, _, hasPending⟩ := firesAll.fires record
          (by rw [publishedSteps]; exact isRecord) isWaiter isWaiting
        have fits := mid.threads.blockedFit thread isThread _ parkedSince isParked
        unfold Thread.PhaseFitsCode at fits
        simp only [isParked, BlockedOn.toWaitRequest?] at fits
        cases pendingCase : (run.applyPublications (broadcastPublications cv waiters)).pendingMessages
            (chanOf record.id) with
        | nil => exact absurd pendingCase hasPending
        | cons message remaining =>
          refine ⟨.granted, { timersDone := [], channelMessages := [some message] }, ?_, ?_, ?_⟩
          · simp [broadcastWoken, isTarget.1, isTarget.2]
          · simp [firedResults, isWaiter, isWaiting, pendingCase]
          · exact wp3b_decode_park thread cv fits.1 fits.2 [] message
      · left
        constructor
        · unfold broadcastWoken
          rw [if_neg]
          intro isBoth
          apply isTarget
          have isSplit := Bool.and_eq_true _ _ ▸ isBoth
          exact ⟨isSplit.1, by simpa using isSplit.2⟩
        · unfold firedResults
          rw [if_neg]
          rintro ⟨isWaiter, isWaiting⟩
          obtain ⟨parked, parkedSince, _, isParkedThread, isParked, isWaiterMember, parkedCarries,
            _⟩ := parkedOfWaiter record isRecord isWaiter isWaiting
          have isSameThread := sameCarrier parked isParkedThread thread isThread record
            parkedCarries carries
          subst isSameThread
          exact isTarget ⟨(wp3b_parkedOn_iff cv parked).mpr ⟨parkedSince, isParked⟩, isWaiterMember⟩
    · intro timer _
      rw [isWake, sameTimers, publishedTimers]
    · intro timer record isRecord hasId isFired
      have isBoth : record.id ∈ (run.applyPublications
          (broadcastPublications cv waiters)).waitersOn
          ((broadcastPublications cv waiters).map (fun publication => publication.1)) ∧
          record.phase = .waiting := by
        unfold firedResults at isFired
        split at isFired
        · rename_i isBoth
          exact isBoth
        · exact absurd rfl isFired
      rw [isWake, sameTimers]
      exact firesAll.noTimers timer record (by rw [publishedSteps]; exact isRecord) hasId isBoth.1
        isBoth.2
    · have kept := mid.threads.runInvariant.applyEffects
        { publications := broadcastPublications cv waiters }
      rw [Dex.Run.seq_applyEffects]
      exact kept
  · exact mid.data.transfer _ _ (broadcastWoken cv waiters) lThreads wokenWereBlocked sameStatus
      sameOutputs sameCounters sameAttributes lCounter lSame.1 lSame.2.1 lSame.2.2.1
      (fun chan => otherKept _ (by simp))
      (Or.inl ⟨fun joined => otherKept _ (by simp), lSame.2.2.2.2.1⟩)
      (Or.inr (by
        intro otherCv waiter
        by_cases isCv : otherCv = cv
        · subst isCv
          have published := pendingWake waiter
          have taken := lTaken waiter
          have related := mid.data.cvWakes otherCv waiter
          by_cases isTaken : waiter ∈ waiters ∧ ∃ thread, thread ∈ lConfig.threads ∧
              thread.core.tid = waiter ∧ parkedOn otherCv thread = true
          · obtain ⟨isWaiterMember, thread, isThread, hasTid, isParked⟩ := isTaken
            have oneTaken : takenWakes lConfig otherCv waiters waiter = 1 := by
              unfold takenWakes
              rw [if_pos]
              exact ⟨isWaiterMember, thread, isThread, hasTid, isParked⟩
            obtain ⟨since, isBlocked⟩ := (wp3b_parkedOn_iff otherCv thread).mp isParked
            obtain ⟨record, _, isRecord, isActive, carries, _⟩ :=
              mid.threads.stepOfBlocked thread isThread _ since isBlocked
            obtain ⟨isWaiter, isWaiting⟩ := waiterOfParked thread isThread since isBlocked
              (by rw [hasTid]; exact isWaiterMember) record isRecord isActive carries
            obtain ⟨parked, _, _, isParkedThread, _, _, parkedCarries, _, _, _, hasChan⟩ :=
              parkedOfWaiter record isRecord isWaiter isWaiting
            have isSameThread := sameCarrier parked isParkedThread thread isThread record
              parkedCarries carries
            subst isSameThread
            have isFired := firedPending record (by rw [publishedSteps]; exact isRecord) isWaiter
              isWaiting
            rw [hasChan, hasTid] at isFired
            rw [isWake, isFired, List.length_tail]
            have isCounted : 0 < waiters.count waiter := List.count_pos_iff.mpr isWaiterMember
            omega
          · have noneTaken : takenWakes lConfig otherCv waiters waiter = 0 := by
              unfold takenWakes
              rw [if_neg isTaken]
            have isKept := keptPending (Chan.cvWake otherCv waiter) (by
              intro record isRecord isWaiter isWaiting sameChan
              rw [publishedSteps] at isRecord
              obtain ⟨parked, parkedSince, _, isParkedThread, isParked, isWaiterMember, _, _, _, _,
                hasChan⟩ := parkedOfWaiter record isRecord isWaiter isWaiting
              rw [hasChan] at sameChan
              have hasTid : parked.core.tid = waiter := by simpa using sameChan
              exact isTaken ⟨by rw [← hasTid]; exact isWaiterMember, parked, isParkedThread, hasTid,
                (wp3b_parkedOn_iff otherCv parked).mpr ⟨parkedSince, isParked⟩⟩)
            rw [isWake, isKept]
            omega
        · have isOtherChan : ∀ other, Chan.cvWake otherCv waiter ≠ Chan.cvWake cv other := by
            intro other isSame
            apply isCv
            have isPair : otherCv = cv ∧ waiter = other := by simpa using isSame
            exact isPair.1
          rw [otherKept _ isOtherChan, lOther otherCv waiter isCv]
          exact mid.data.cvWakes otherCv waiter))
      (Or.inl ⟨fun target => otherKept _ (by simp), lSame.2.2.2.2.2⟩)
  · intro mutex
    rw [otherKept _ (by simp), lSame.2.2.2.1]
    exact mid.tokens mutex

end DexExpressiveness
