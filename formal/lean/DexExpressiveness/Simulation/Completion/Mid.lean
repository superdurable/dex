import DexExpressiveness.Simulation.Frame

/-!
# The relation while an Execute commits

[STEP-COMPLETE-EXECUTE] of the Step `stepId` that carries thread `tid` runs
in three parts: `S[e].inv := ⊥`, then `effects`, then `decide`. Between the
parts the thread has no Step that matches its phase. `MidThreads` is the
thread-and-Step part of `RunningRelated` with that thread and that Step left
out. It also carries the facts of the two invariants that the wake-up
arguments need, so that they need not be transported separately.

`MidThreads.wake` is the frame lemma for `wake`: when the L side wakes some
blocked threads and the Dex side fires the waits of their Steps, with
matching outcomes, the relation is kept.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The thread-and-Step part of the relation while the Execute of thread
`tid`, carried by Step `stepId`, commits. -/
structure MidThreads (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (run : Dex.Run signature) (tid : Tid) (stepId : Dex.StepExecId signature) : Prop where
  /-- The run is the configuration's run. -/
  hasRun : dexConfig.run = some run
  /-- As `RunningRelated.timers`. -/
  timers : ∀ timer fireTime, run.timers.get? timer = some fireTime ↔
    ∃ thread chan since, thread ∈ lConfig.threads ∧
      thread.phase = .blocked (.timed timer.step.number fireTime chan) since ∧
      timer.step.stepType = StepType.timed ∧ timer.index = 0
  /-- As `RunningRelated.threadsHaveSteps`, for the other threads. -/
  threadsHaveSteps : ∀ thread, thread ∈ lConfig.threads → thread.core.tid ≠ tid →
    thread.phase ≠ .halted →
    ∃ record, record ∈ run.steps ∧ record.phase.isActive = true ∧
      StepCarriesThread program thread record ∧
      PhaseMatchesStep program lConfig dexConfig thread record
  /-- As `RunningRelated.stepsHaveThreads`, for the other Steps. -/
  stepsHaveThreads : ∀ record, record ∈ run.steps → record.phase.isActive = true →
    record.id ≠ stepId →
    ∃ thread, thread ∈ lConfig.threads ∧ thread.core.tid ≠ tid ∧ thread.phase ≠ .halted ∧
      StepCarriesThread program thread record
  /-- As `RunningRelated.activeStepsHaveDistinctThreads`. -/
  activeStepsHaveDistinctThreads : ∀ first, first ∈ run.steps → ∀ second, second ∈ run.steps →
    first.phase.isActive = true → second.phase.isActive = true →
    threadOfInput first.input = threadOfInput second.input → first = second
  /-- As `RunningRelated.invocationsAreActive`. -/
  invocationsAreActive : ∀ record, record ∈ run.steps → record.invocation ≠ none →
    record.phase.isActive = true
  /-- As `RunningRelated.blockingOrder`. -/
  blockingOrder : ∀ first second firstRecord secondRecord firstOn firstSince secondOn secondSince
      firstWait secondWait,
    first ∈ lConfig.threads → second ∈ lConfig.threads →
    firstRecord ∈ run.steps → secondRecord ∈ run.steps →
    firstRecord.phase.isActive = true → secondRecord.phase.isActive = true →
    StepCarriesThread program first firstRecord → StepCarriesThread program second secondRecord →
    first.phase = .blocked firstOn firstSince → second.phase = .blocked secondOn secondSince →
    firstRecord.waitState = some firstWait → secondRecord.waitState = some secondWait →
    (firstSince < secondSince ↔ firstWait.registeredSeq < secondWait.registeredSeq)
  /-- As `RunningRelated.tasksAreCurrent`. -/
  tasksAreCurrent : ∀ task, task ∈ dexConfig.tasks → dexConfig.isTaskCurrent task = true
  /-- The run invariant. -/
  runInvariant : Dex.Run.Invariant IsTimedTimer (run.seq + 1) run
  /-- A Step in phase `WT` has no invocation. -/
  waitingHasNoInvocation : ∀ record, record ∈ run.steps → record.phase = .waiting →
    record.invocation = none
  /-- The committing Step does not wait, has no invocation, and has the
  thread's ID in its input. -/
  ownStep : ∀ record, record ∈ run.steps → record.id = stepId →
    record.phase ≠ .waiting ∧ record.invocation = none ∧ threadOfInput record.input = tid
  /-- The committing thread is not blocked. -/
  ownThread : ∀ thread, thread ∈ lConfig.threads → thread.core.tid = tid →
    ∀ on since, thread.phase ≠ .blocked on since
  /-- No thread is in the start action. -/
  noBoot : ∀ thread, thread ∈ lConfig.threads →
    thread.phase ≠ .bootPending ∧ thread.phase ≠ .bootRunning
  /-- Thread IDs are unique. -/
  threadIdsNodup : (lConfig.threads.map (fun thread => thread.core.tid)).Nodup
  /-- The phase of a blocked thread fits its code. -/
  blockedFit : ∀ thread, thread ∈ lConfig.threads → ∀ on since,
    thread.phase = .blocked on since → thread.PhaseFitsCode
  /-- Two blocked threads never share a `since` stamp. -/
  sinceInjective : ∀ first, first ∈ lConfig.threads → ∀ second, second ∈ lConfig.threads →
    ∀ firstOn secondOn since, first.phase = .blocked firstOn since →
      second.phase = .blocked secondOn since → first = second

namespace MidThreads

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}
  {run : Dex.Run signature} {tid : Tid} {stepId : Dex.StepExecId signature}

/-- Every other active Step carries a thread whose phase it matches. -/
theorem threadOfStep (mid : MidThreads program lConfig dexConfig run tid stepId)
    (record : Dex.StepRecord signature) (isMember : record ∈ run.steps)
    (isActive : record.phase.isActive = true) (isOther : record.id ≠ stepId) :
    ∃ thread, thread ∈ lConfig.threads ∧ thread.core.tid ≠ tid ∧ thread.phase ≠ .halted ∧
      StepCarriesThread program thread record ∧
      PhaseMatchesStep program lConfig dexConfig thread record := by
  obtain ⟨thread, isThread, isOtherThread, isLive, carries⟩ :=
    mid.stepsHaveThreads record isMember isActive isOther
  obtain ⟨found, isFound, foundActive, foundCarries, isMatch⟩ :=
    mid.threadsHaveSteps thread isThread isOtherThread isLive
  have sameThread : threadOfInput record.input = threadOfInput found.input := by
    rw [threadOfInput_of_carries_of_booted program thread record (mid.noBoot thread isThread).1
        (mid.noBoot thread isThread).2 carries,
      threadOfInput_of_carries_of_booted program thread found (mid.noBoot thread isThread).1
        (mid.noBoot thread isThread).2 foundCarries]
  have isSame := mid.activeStepsHaveDistinctThreads record isMember found isFound isActive
    foundActive sameThread
  subst isSame
  exact ⟨thread, isThread, isOtherThread, isLive, carries, isMatch⟩

/-- Two active Steps that carry the same thread are the same Step. -/
theorem stepOfThread_unique (mid : MidThreads program lConfig dexConfig run tid stepId)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads)
    (first second : Dex.StepRecord signature) (isFirst : first ∈ run.steps)
    (isSecond : second ∈ run.steps) (firstActive : first.phase.isActive = true)
    (secondActive : second.phase.isActive = true)
    (firstCarries : StepCarriesThread program thread first)
    (secondCarries : StepCarriesThread program thread second) : first = second := by
  apply mid.activeStepsHaveDistinctThreads first isFirst second isSecond firstActive secondActive
  rw [threadOfInput_of_carries_of_booted program thread first (mid.noBoot thread isThread).1
      (mid.noBoot thread isThread).2 firstCarries,
    threadOfInput_of_carries_of_booted program thread second (mid.noBoot thread isThread).1
      (mid.noBoot thread isThread).2 secondCarries]

/-- A blocked thread has a waiting Step with the wait its statement asks for. -/
theorem stepOfBlocked (mid : MidThreads program lConfig dexConfig run tid stepId)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads) (on : BlockedOn) (since : Nat)
    (isBlocked : thread.phase = .blocked on since) :
    ∃ record waitState, record ∈ run.steps ∧ record.phase.isActive = true ∧
      StepCarriesThread program thread record ∧ record.phase = .waiting ∧
      record.waitState = some waitState ∧
      waitState.wait.kind = (expectedWaitShape thread.core.tid on).1 ∧
      waitState.wait.timerSeconds.length = (expectedWaitShape thread.core.tid on).2.1 ∧
      waitState.wait.channels = (expectedWaitShape thread.core.tid on).2.2 ∧
      waitState.timersDone = waitState.wait.timerSeconds.map (fun _ => false) := by
  have isOther : thread.core.tid ≠ tid := fun isOwn =>
    mid.ownThread thread isThread isOwn on since isBlocked
  obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
    mid.threadsHaveSteps thread isThread isOther (by simp [isBlocked])
  obtain ⟨isWaiting, _, waitState, hasWait, shape⟩ :=
    waiting_of_phaseMatchesStep program lConfig dexConfig thread record on since isBlocked isMatch
  exact ⟨record, waitState, isRecord, isActive, carries, isWaiting, hasWait, shape⟩

/-- A waiting Step carries a blocked thread. -/
theorem blockedOfWaiting (mid : MidThreads program lConfig dexConfig run tid stepId)
    (record : Dex.StepRecord signature) (isMember : record ∈ run.steps)
    (isWaiting : record.phase = .waiting) :
    ∃ thread on since, thread ∈ lConfig.threads ∧ thread.phase = .blocked on since ∧
      StepCarriesThread program thread record ∧
      PhaseMatchesStep program lConfig dexConfig thread record := by
  have isOther : record.id ≠ stepId := fun isOwn =>
    (mid.ownStep record isMember isOwn).1 isWaiting
  obtain ⟨thread, isThread, _, _, carries, isMatch⟩ :=
    mid.threadOfStep record isMember (by simp [isWaiting, Dex.Phase.isActive]) isOther
  obtain ⟨on, since, isBlocked⟩ := blocked_of_phaseMatchesStep program lConfig dexConfig thread record
    isMatch isWaiting
  exact ⟨thread, on, since, isThread, isBlocked, carries, isMatch⟩

/-- The Step of a thread that is blocked in a timed operation is the Step of
its timer. -/
theorem timedStep (mid : MidThreads program lConfig dexConfig run tid stepId)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads) (timer : Dex.TimerId signature)
    (fireTime : Nat) (chan : Option ExtChan) (since : Nat)
    (isBlocked : thread.phase = .blocked (.timed timer.step.number fireTime chan) since)
    (isTimed : timer.step.stepType = StepType.timed) :
    ∃ record, record ∈ run.steps ∧ record.id = timer.step ∧ record.phase.isActive = true ∧
      StepCarriesThread program thread record := by
  obtain ⟨record, waitState, isRecord, isActive, carries, _⟩ :=
    mid.stepOfBlocked thread isThread _ since isBlocked
  refine ⟨record, isRecord, ?_, isActive, carries⟩
  have fits := mid.blockedFit thread isThread _ since isBlocked
  unfold Thread.PhaseFitsCode at fits
  simp only [isBlocked] at fits
  obtain ⟨⟨seconds, hasTimer⟩, _⟩ := fits
  have shape := (stepCarriesThread_iff_of_booted program thread record (mid.noBoot thread isThread).1
    (mid.noBoot thread isThread).2).mp carries
  simp only [hasTimer] at shape
  rw [shape.1, ← isTimed]

/-- The committing Step carries no other thread. -/
theorem ne_ownStep (mid : MidThreads program lConfig dexConfig run tid stepId)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads) (isOther : thread.core.tid ≠ tid)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (carries : StepCarriesThread program thread record) : record.id ≠ stepId := by
  intro isOwn
  apply isOther
  rw [← (mid.ownStep record isRecord isOwn).2.2]
  exact (threadOfInput_of_carries_of_booted program thread record (mid.noBoot thread isThread).1
    (mid.noBoot thread isThread).2 carries).symm

end MidThreads

/-- A thread after the wake of the threads that `woken` selects. -/
def wakeThreadBy (woken : L.Thread → Option WakeOutcome) (thread : L.Thread) : L.Thread :=
  match woken thread with
  | some outcome => { thread with phase := .woken outcome }
  | none => thread

/-- A Step after `eval` fired the waits that `results` selects. -/
def wakeRecordBy
    (results : Dex.StepRecord signature → Option (Dex.ConditionResults signature))
    (record : Dex.StepRecord signature) : Dex.StepRecord signature :=
  match results record with
  | some conditionResults =>
    { record with
      conditionResults := some conditionResults
      phase := .execute
      invocation := some { method := .execute, state := .pending } }
  | none => record

/-- The frame lemma for `wake`: the L side wakes the blocked threads that
`woken` selects, the Dex side fires the waits that `results` selects, and the
two agree on every blocked thread and its Step. -/
theorem MidThreads.wake {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature} {tid : Tid}
    {stepId : Dex.StepExecId signature}
    (mid : MidThreads program lConfig dexConfig run tid stepId)
    (woken : L.Thread → Option WakeOutcome)
    (results : Dex.StepRecord signature → Option (Dex.ConditionResults signature))
    (lNext : L.Config) (dexNext : Dex.Config signature) (runNext : Dex.Run signature)
    (hasRunNext : dexNext.run = some runNext)
    (sameTasks : dexNext.tasks = dexConfig.tasks)
    (sameQueues : ∀ thread, thread ∈ lConfig.threads → thread.core.tid ≠ tid →
      ∀ loaded, thread.phase = .running loaded → ∀ cv,
        cvOperand thread.core thread.code = some cv →
        lNext.cvQueues.get? cv = lConfig.cvQueues.get? cv)
    (threadsNext : lNext.threads = lConfig.threads.map (wakeThreadBy woken))
    (stepsNext : runNext.steps = run.steps.map (wakeRecordBy results))
    (wokenWereBlocked : ∀ thread, thread ∈ lConfig.threads → ∀ outcome,
      woken thread = some outcome → ∃ on since, thread.phase = .blocked on since)
    (resultsWereWaiting : ∀ record, record ∈ run.steps → ∀ conditionResults,
      results record = some conditionResults → record.phase = .waiting)
    (agree : ∀ thread, thread ∈ lConfig.threads → ∀ record, record ∈ run.steps →
      record.phase.isActive = true → StepCarriesThread program thread record →
      ∀ on since, thread.phase = .blocked on since →
      (woken thread = none ∧ results record = none) ∨
      ∃ outcome conditionResults, woken thread = some outcome ∧
        results record = some conditionResults ∧
        decodeOutcome thread (some conditionResults) = outcome)
    (timersKept : ∀ timer,
      (∀ record, record ∈ run.steps → record.id = timer.step → results record = none) →
      runNext.timers.get? timer = run.timers.get? timer)
    (timersDisarmed : ∀ timer record, record ∈ run.steps → record.id = timer.step →
      results record ≠ none → runNext.timers.get? timer = none)
    (invariantNext : Dex.Run.Invariant IsTimedTimer (runNext.seq + 1) runNext) :
    MidThreads program lNext dexNext runNext tid stepId := by
  have threadFacts : ∀ thread, (wakeThreadBy woken thread).core = thread.core ∧
      (wakeThreadBy woken thread).code = thread.code ∧
      (wakeThreadBy woken thread).timer = thread.timer := by
    intro thread
    unfold wakeThreadBy
    split <;> simp
  have recordFacts : ∀ record, (wakeRecordBy results record).id = record.id ∧
      (wakeRecordBy results record).input = record.input ∧
      (wakeRecordBy results record).options = record.options ∧
      (wakeRecordBy results record).waitState = record.waitState := by
    intro record
    unfold wakeRecordBy
    split <;> simp
  have keptThread : ∀ thread, woken thread = none → wakeThreadBy woken thread = thread := by
    intro thread isKept
    simp [wakeThreadBy, isKept]
  have keptRecord : ∀ record, results record = none → wakeRecordBy results record = record := by
    intro record isKept
    simp [wakeRecordBy, isKept]
  have unblockedKept : ∀ thread, thread ∈ lConfig.threads →
      (∀ on since, thread.phase ≠ .blocked on since) → woken thread = none := by
    intro thread isThread isNotBlocked
    cases wokenCase : woken thread with
    | none => rfl
    | some outcome =>
      obtain ⟨on, since, isBlocked⟩ := wokenWereBlocked thread isThread outcome wokenCase
      exact absurd isBlocked (isNotBlocked on since)
  have blockedKept : ∀ thread on since,
      (wakeThreadBy woken thread).phase = .blocked on since → woken thread = none := by
    intro thread on since isBlocked
    cases wokenCase : woken thread with
    | none => rfl
    | some outcome => simp [wakeThreadBy, wokenCase] at isBlocked
  have notWaitingKept : ∀ record, record ∈ run.steps → record.phase ≠ .waiting →
      results record = none := by
    intro record isRecord isNotWaiting
    cases resultCase : results record with
    | none => rfl
    | some conditionResults =>
      exact absurd (resultsWereWaiting record isRecord conditionResults resultCase) isNotWaiting
  have activeBefore : ∀ record, record ∈ run.steps →
      (wakeRecordBy results record).phase.isActive = true → record.phase.isActive = true := by
    intro record isRecord isActive
    cases resultCase : results record with
    | none => rwa [keptRecord record resultCase] at isActive
    | some conditionResults =>
      rw [resultsWereWaiting record isRecord conditionResults resultCase]
      rfl
  have noBootNext : ∀ thread, thread ∈ lConfig.threads →
      (wakeThreadBy woken thread).phase ≠ .bootPending ∧
      (wakeThreadBy woken thread).phase ≠ .bootRunning := by
    intro thread isThread
    cases wokenCase : woken thread with
    | none =>
      rw [keptThread thread wokenCase]
      exact mid.noBoot thread isThread
    | some outcome => simp [wakeThreadBy, wokenCase]
  have carriesBack : ∀ thread, thread ∈ lConfig.threads → ∀ record,
      StepCarriesThread program thread (wakeRecordBy results record) →
      StepCarriesThread program thread record := by
    intro thread isThread record carries
    exact stepCarriesThread_congr_of_booted program thread thread _ record (mid.noBoot thread isThread).1
      (mid.noBoot thread isThread).2 (mid.noBoot thread isThread).1 (mid.noBoot thread isThread).2
      rfl rfl rfl (recordFacts record).1.symm (recordFacts record).2.1.symm
      (recordFacts record).2.2.1.symm carries
  -- A thread and its Step change together.
  have pair : ∀ thread, thread ∈ lConfig.threads → thread.core.tid ≠ tid →
      thread.phase ≠ .halted → ∀ record, record ∈ run.steps → record.phase.isActive = true →
      StepCarriesThread program thread record →
      PhaseMatchesStep program lConfig dexConfig thread record →
      StepCarriesThread program (wakeThreadBy woken thread) (wakeRecordBy results record) ∧
      PhaseMatchesStep program lNext dexNext (wakeThreadBy woken thread)
        (wakeRecordBy results record) ∧
      (wakeRecordBy results record).phase.isActive = true ∧
      (wakeThreadBy woken thread).phase ≠ .halted := by
    intro thread isThread isOther isLive record isRecord isActive carries isMatch
    have unchanged : woken thread = none → results record = none →
        StepCarriesThread program (wakeThreadBy woken thread) (wakeRecordBy results record) ∧
        PhaseMatchesStep program lNext dexNext (wakeThreadBy woken thread)
          (wakeRecordBy results record) ∧
        (wakeRecordBy results record).phase.isActive = true ∧
        (wakeThreadBy woken thread).phase ≠ .halted := by
      intro threadKept recordKept
      rw [keptThread thread threadKept, keptRecord record recordKept]
      refine ⟨carries, ?_, isActive, isLive⟩
      exact phaseMatchesStep_congr program lConfig lNext dexConfig dexNext thread record
        (fun task isMember _ => by rw [sameTasks]; exact isMember)
        (fun loaded isRunning cv isOperand =>
          sameQueues thread isThread isOther loaded isRunning cv isOperand) isMatch
    by_cases isBlocked : ∃ on since, thread.phase = .blocked on since
    · obtain ⟨on, since, isBlocked⟩ := isBlocked
      rcases agree thread isThread record isRecord isActive carries on since isBlocked with
        ⟨threadKept, recordKept⟩ | ⟨outcome, conditionResults, isWoken, isFired, isDecoded⟩
      · exact unchanged threadKept recordKept
      · have isThreadNext : wakeThreadBy woken thread = { thread with phase := .woken outcome } := by
          simp [wakeThreadBy, isWoken]
        have isRecordNext : wakeRecordBy results record =
            { record with
              conditionResults := some conditionResults
              phase := .execute
              invocation := some { method := .execute, state := .pending } } := by
          simp [wakeRecordBy, isFired]
        rw [isThreadNext, isRecordNext]
        refine ⟨?_, ?_, rfl, by simp⟩
        · exact stepCarriesThread_congr_of_booted program thread _ record _ (mid.noBoot thread isThread).1
            (mid.noBoot thread isThread).2 (by simp) (by simp) rfl rfl rfl rfl rfl rfl carries
        · simp only [PhaseMatchesStep, true_and]
          exact isDecoded
    · have isNotBlocked : ∀ on since, thread.phase ≠ .blocked on since :=
        fun on since isBlockedOn => isBlocked ⟨on, since, isBlockedOn⟩
      have isNotWaiting : record.phase ≠ .waiting := by
        intro isWaiting
        obtain ⟨on, since, isBlockedOn⟩ := blocked_of_phaseMatchesStep program lConfig dexConfig
          thread record isMatch isWaiting
        exact isNotBlocked on since isBlockedOn
      exact unchanged (unblockedKept thread isThread isNotBlocked)
        (notWaitingKept record isRecord isNotWaiting)
  -- The Step of a thread that is blocked in a timed operation is the Step of its timer.
  have timedRecord : ∀ thread, thread ∈ lConfig.threads → ∀ (timer : Dex.TimerId signature)
      fireTime chan since,
      thread.phase = .blocked (.timed timer.step.number fireTime chan) since →
      timer.step.stepType = StepType.timed →
      ∃ record, record ∈ run.steps ∧ record.id = timer.step ∧ record.phase.isActive = true ∧
        StepCarriesThread program thread record := by
    intro thread isThread timer fireTime chan since isBlocked isTimed
    obtain ⟨record, waitState, isRecord, isActive, carries, _⟩ :=
      mid.stepOfBlocked thread isThread _ since isBlocked
    refine ⟨record, isRecord, ?_, isActive, carries⟩
    have fits := mid.blockedFit thread isThread _ since isBlocked
    unfold Thread.PhaseFitsCode at fits
    simp only [isBlocked] at fits
    obtain ⟨⟨seconds, hasTimer⟩, _⟩ := fits
    have shape := (stepCarriesThread_iff_of_booted program thread record (mid.noBoot thread isThread).1
      (mid.noBoot thread isThread).2).mp carries
    simp only [hasTimer] at shape
    rw [shape.1, ← isTimed]
  exact
    { hasRun := hasRunNext
      timers := by
        intro timer fireTime
        constructor
        · intro isArmed
          by_cases isDisarmed : ∃ record, record ∈ run.steps ∧ record.id = timer.step ∧
              results record ≠ none
          · obtain ⟨record, isRecord, hasId, isFired⟩ := isDisarmed
            rw [timersDisarmed timer record isRecord hasId isFired] at isArmed
            cases isArmed
          · have allKept : ∀ record, record ∈ run.steps → record.id = timer.step →
                results record = none := by
              intro record isRecord hasId
              cases resultCase : results record with
              | none => rfl
              | some conditionResults =>
                exact absurd ⟨record, isRecord, hasId, by simp [resultCase]⟩ isDisarmed
            rw [timersKept timer allKept] at isArmed
            obtain ⟨thread, chan, since, isThread, isBlocked, isTimed, isIndex⟩ :=
              (mid.timers timer fireTime).mp isArmed
            obtain ⟨record, isRecord, hasId, isActive, carries⟩ :=
              timedRecord thread isThread timer fireTime chan since isBlocked isTimed
            have threadKept : woken thread = none := by
              rcases agree thread isThread record isRecord isActive carries _ since isBlocked with
                ⟨threadKept, _⟩ | ⟨outcome, conditionResults, _, isFired, _⟩
              · exact threadKept
              · rw [allKept record isRecord hasId] at isFired
                cases isFired
            refine ⟨thread, chan, since, ?_, isBlocked, isTimed, isIndex⟩
            rw [threadsNext]
            exact List.mem_map.mpr ⟨thread, isThread, keptThread thread threadKept⟩
        · rintro ⟨threadNext, chan, since, isThreadNext, isBlocked, isTimed, isIndex⟩
          rw [threadsNext] at isThreadNext
          obtain ⟨thread, isThread, rfl⟩ := List.mem_map.mp isThreadNext
          have threadKept := blockedKept thread _ since isBlocked
          rw [keptThread thread threadKept] at isBlocked
          obtain ⟨record, isRecord, hasId, isActive, carries⟩ :=
            timedRecord thread isThread timer fireTime chan since isBlocked isTimed
          have allKept : ∀ other, other ∈ run.steps → other.id = timer.step →
              results other = none := by
            intro other isOther otherId
            have isSame : other = record :=
              eq_of_nodup_map (fun candidate => candidate.id) run.steps
                mid.runInvariant.idsNodup isOther isRecord (otherId.trans hasId.symm)
            rw [isSame]
            rcases agree thread isThread record isRecord isActive carries _ since isBlocked with
              ⟨_, recordKept⟩ | ⟨outcome, conditionResults, isWoken, _, _⟩
            · exact recordKept
            · rw [threadKept] at isWoken
              cases isWoken
          rw [timersKept timer allKept]
          exact (mid.timers timer fireTime).mpr ⟨thread, chan, since, isThread, isBlocked, isTimed,
            isIndex⟩
      threadsHaveSteps := by
        intro threadNext isThreadNext isOther isLive
        rw [threadsNext] at isThreadNext
        obtain ⟨thread, isThread, rfl⟩ := List.mem_map.mp isThreadNext
        rw [(threadFacts thread).1] at isOther
        have wasLive : thread.phase ≠ .halted := by
          intro isHalted
          have threadKept := unblockedKept thread isThread (by simp [isHalted])
          rw [keptThread thread threadKept] at isLive
          exact isLive isHalted
        obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
          mid.threadsHaveSteps thread isThread isOther wasLive
        obtain ⟨carriesNext, isMatchNext, isActiveNext, _⟩ :=
          pair thread isThread isOther wasLive record isRecord isActive carries isMatch
        exact ⟨wakeRecordBy results record, by
          rw [stepsNext]
          exact List.mem_map.mpr ⟨record, isRecord, rfl⟩, isActiveNext, carriesNext, isMatchNext⟩
      stepsHaveThreads := by
        intro recordNext isRecordNext isActiveNext isOther
        rw [stepsNext] at isRecordNext
        obtain ⟨record, isRecord, rfl⟩ := List.mem_map.mp isRecordNext
        rw [(recordFacts record).1] at isOther
        obtain ⟨thread, isThread, isOtherThread, isLive, carries, isMatch⟩ :=
          mid.threadOfStep record isRecord (activeBefore record isRecord isActiveNext) isOther
        obtain ⟨carriesNext, _, _, isLiveNext⟩ :=
          pair thread isThread isOtherThread isLive record isRecord
            (activeBefore record isRecord isActiveNext) carries isMatch
        refine ⟨wakeThreadBy woken thread, ?_, ?_, isLiveNext, carriesNext⟩
        · rw [threadsNext]
          exact List.mem_map.mpr ⟨thread, isThread, rfl⟩
        · rw [(threadFacts thread).1]
          exact isOtherThread
      activeStepsHaveDistinctThreads := by
        intro firstNext isFirstNext secondNext isSecondNext firstActive secondActive sameThread
        rw [stepsNext] at isFirstNext isSecondNext
        obtain ⟨first, isFirst, rfl⟩ := List.mem_map.mp isFirstNext
        obtain ⟨second, isSecond, rfl⟩ := List.mem_map.mp isSecondNext
        rw [(recordFacts first).2.1, (recordFacts second).2.1] at sameThread
        rw [mid.activeStepsHaveDistinctThreads first isFirst second isSecond
          (activeBefore first isFirst firstActive) (activeBefore second isSecond secondActive)
          sameThread]
      invocationsAreActive := by
        intro recordNext isRecordNext hasInvocation
        rw [stepsNext] at isRecordNext
        obtain ⟨record, isRecord, rfl⟩ := List.mem_map.mp isRecordNext
        cases resultCase : results record with
        | none =>
          rw [keptRecord record resultCase] at hasInvocation ⊢
          exact mid.invocationsAreActive record isRecord hasInvocation
        | some conditionResults => simp [wakeRecordBy, resultCase, Dex.Phase.isActive]
      blockingOrder := by
        intro firstNext secondNext firstRecordNext secondRecordNext firstOn firstSince secondOn
          secondSince firstWait secondWait isFirstNext isSecondNext isFirstRecordNext
          isSecondRecordNext firstActive secondActive firstCarries secondCarries firstBlocked
          secondBlocked firstHasWait secondHasWait
        rw [threadsNext] at isFirstNext isSecondNext
        rw [stepsNext] at isFirstRecordNext isSecondRecordNext
        obtain ⟨first, isFirst, rfl⟩ := List.mem_map.mp isFirstNext
        obtain ⟨second, isSecond, rfl⟩ := List.mem_map.mp isSecondNext
        obtain ⟨firstRecord, isFirstRecord, rfl⟩ := List.mem_map.mp isFirstRecordNext
        obtain ⟨secondRecord, isSecondRecord, rfl⟩ := List.mem_map.mp isSecondRecordNext
        have firstKept := keptThread first (blockedKept first _ _ firstBlocked)
        have secondKept := keptThread second (blockedKept second _ _ secondBlocked)
        rw [firstKept] at firstBlocked firstCarries
        rw [secondKept] at secondBlocked secondCarries
        rw [(recordFacts firstRecord).2.2.2] at firstHasWait
        rw [(recordFacts secondRecord).2.2.2] at secondHasWait
        exact mid.blockingOrder first second firstRecord secondRecord firstOn firstSince secondOn
          secondSince firstWait secondWait isFirst isSecond isFirstRecord isSecondRecord
          (activeBefore firstRecord isFirstRecord firstActive)
          (activeBefore secondRecord isSecondRecord secondActive)
          (carriesBack first isFirst firstRecord firstCarries)
          (carriesBack second isSecond secondRecord secondCarries) firstBlocked secondBlocked
          firstHasWait secondHasWait
      tasksAreCurrent := by
        intro task isTask
        rw [sameTasks] at isTask
        have wasCurrent := mid.tasksAreCurrent task isTask
        unfold Dex.Config.isTaskCurrent at wasCurrent ⊢
        rw [mid.hasRun] at wasCurrent
        rw [hasRunNext]
        simp only at wasCurrent ⊢
        cases foundCase : run.findStep? task.step with
        | none => simp [foundCase] at wasCurrent
        | some record =>
          simp only [foundCase, decide_eq_true_eq] at wasCurrent
          have isRecord := (Dex.Run.mem_of_findStep? run task.step record foundCase).1
          have recordKept : results record = none := by
            apply notWaitingKept record isRecord
            intro isWaiting
            rw [mid.waitingHasNoInvocation record isRecord isWaiting] at wasCurrent
            cases wasCurrent
          have foundNext : runNext.findStep? task.step = some record := by
            unfold Dex.Run.findStep? at foundCase ⊢
            rw [stepsNext, List.find?_map]
            have samePredicate :
                ((fun candidate : Dex.StepRecord signature => decide (candidate.id = task.step)) ∘
                  wakeRecordBy results) = fun candidate => decide (candidate.id = task.step) := by
              funext candidate
              simp [(recordFacts candidate).1]
            rw [samePredicate, foundCase]
            simp [keptRecord record recordKept]
          simp [foundNext, wasCurrent]
      runInvariant := invariantNext
      waitingHasNoInvocation := by
        intro recordNext isRecordNext isWaiting
        rw [stepsNext] at isRecordNext
        obtain ⟨record, isRecord, rfl⟩ := List.mem_map.mp isRecordNext
        cases resultCase : results record with
        | none =>
          rw [keptRecord record resultCase] at isWaiting ⊢
          exact mid.waitingHasNoInvocation record isRecord isWaiting
        | some conditionResults => simp [wakeRecordBy, resultCase] at isWaiting
      ownStep := by
        intro recordNext isRecordNext isOwn
        rw [stepsNext] at isRecordNext
        obtain ⟨record, isRecord, rfl⟩ := List.mem_map.mp isRecordNext
        rw [(recordFacts record).1] at isOwn
        have own := mid.ownStep record isRecord isOwn
        rw [keptRecord record (notWaitingKept record isRecord own.1)]
        exact own
      ownThread := by
        intro threadNext isThreadNext isOwn on since
        rw [threadsNext] at isThreadNext
        obtain ⟨thread, isThread, rfl⟩ := List.mem_map.mp isThreadNext
        rw [(threadFacts thread).1] at isOwn
        rw [keptThread thread (unblockedKept thread isThread (mid.ownThread thread isThread isOwn))]
        exact mid.ownThread thread isThread isOwn on since
      noBoot := by
        intro threadNext isThreadNext
        rw [threadsNext] at isThreadNext
        obtain ⟨thread, isThread, rfl⟩ := List.mem_map.mp isThreadNext
        exact noBootNext thread isThread
      threadIdsNodup := by
        rw [threadsNext, List.map_map]
        have sameIds : ((fun thread : L.Thread => thread.core.tid) ∘ wakeThreadBy woken) =
            fun thread => thread.core.tid := by
          funext thread
          simp [(threadFacts thread).1]
        rw [sameIds]
        exact mid.threadIdsNodup
      blockedFit := by
        intro threadNext isThreadNext on since isBlocked
        rw [threadsNext] at isThreadNext
        obtain ⟨thread, isThread, rfl⟩ := List.mem_map.mp isThreadNext
        have threadKept := keptThread thread (blockedKept thread on since isBlocked)
        rw [threadKept] at isBlocked ⊢
        exact mid.blockedFit thread isThread on since isBlocked
      sinceInjective := by
        intro firstNext isFirstNext secondNext isSecondNext firstOn secondOn since firstBlocked
          secondBlocked
        rw [threadsNext] at isFirstNext isSecondNext
        obtain ⟨first, isFirst, rfl⟩ := List.mem_map.mp isFirstNext
        obtain ⟨second, isSecond, rfl⟩ := List.mem_map.mp isSecondNext
        have firstKept := keptThread first (blockedKept first _ _ firstBlocked)
        have secondKept := keptThread second (blockedKept second _ _ secondBlocked)
        rw [firstKept] at firstBlocked ⊢
        rw [secondKept] at secondBlocked ⊢
        exact mid.sinceInjective first isFirst second isSecond firstOn secondOn since firstBlocked
          secondBlocked }

/-- The mutex tokens: a token Channel holds one token exactly while its
mutex is unlocked. This is `RunningRelated.mutexTokens` once the start Step
has completed. -/
def TokensRelated (lConfig : L.Config) (run : Dex.Run signature) : Prop :=
  ∀ mutex, (run.pendingMessages (Chan.mutexToken mutex)).length =
    if lConfig.mutexOwners.get? mutex = some none then 1 else 0

/-- The data part of the relation while the Execute of thread `tid` commits:
the clauses of `RunningRelated` about Attributes, Channels, and counters. The
cancel flag of the committing thread is treated apart, because its phase
changes. -/
structure MidData (lConfig : L.Config) (run : Dex.Run signature) (tid : Tid) : Prop where
  /-- The run is running. -/
  isRunning : run.status = .running
  /-- As `RunningRelated.shared`. -/
  shared : ∀ name, readShared { attributes := run.attributes } name = lConfig.readShared name
  /-- As `RunningRelated.cvQueues`. -/
  cvQueues : ∀ cv,
    readWaiters { attributes := run.attributes } cv = (lConfig.cvQueues.get? cv).getD []
  /-- As `RunningRelated.inputQueues`. -/
  inputQueues : ∀ chan,
    run.pendingMessages (Chan.external chan) = (lConfig.pendingInput chan).map DexVal.value
  /-- As `RunningRelated.joinTokens`. -/
  joinTokens : ∀ thread, (run.pendingMessages (Chan.joinDone thread)).length =
    if lConfig.joinable.contains thread then 1 else 0
  /-- As `RunningRelated.cancelRequestsAreFlags`. -/
  cancelRequestsAreFlags : ∀ thread,
    run.pendingMessages (Chan.cancelRequest thread) ≠ [] →
      lConfig.cancelFlags.contains thread = true
  /-- As `RunningRelated.cancelFlagsAreRequests`, for the other threads. -/
  cancelFlagsAreRequests : ∀ thread, thread ≠ tid → lConfig.cancelFlags.contains thread = true →
    run.pendingMessages (Chan.cancelRequest thread) ≠ [] ∨
    (∃ current, lConfig.findThread? thread = some current ∧
      (current.phase = .woken .canceled ∨ current.phase = .resuming .canceled)) ∨
    (lConfig.findThread? thread = none ∧
      ∀ live, live ∈ lConfig.threads → ∀ index, live.core.freshCounter ≤ index →
        ¬ (live.core.tid ++ [index]) <+: thread)
  /-- The committing thread is no child that a live thread can still fork. -/
  ownIsNoChild : ∀ live, live ∈ lConfig.threads → ∀ index, live.core.freshCounter ≤ index →
    ¬ (live.core.tid ++ [index]) <+: tid
  /-- The cancel flag of the committing thread is a pending request, unless
  the thread ends with this Execute. -/
  ownFlag : lConfig.cancelFlags.contains tid = true →
    run.pendingMessages (Chan.cancelRequest tid) ≠ [] ∨ lConfig.findThread? tid = none
  /-- As `RunningRelated.cvWakes`. -/
  cvWakes : ∀ cv waiter, (run.pendingMessages (Chan.cvWake cv waiter)).length =
    lConfig.cvWakes.count (cv, waiter)
  /-- As `RunningRelated.timerCounter`. -/
  timerCounter : (run.counters.get? StepType.timed).getD 0 = lConfig.timerCounter
  /-- As `RunningRelated.outputsEmpty`. -/
  outputsEmpty : run.outputs = []

/-- Two L configurations have the same shared state. Threads and the timer
counter may differ. -/
structure SameData (first second : L.Config) : Prop where
  /-- The shared variables. -/
  shared : second.shared = first.shared
  /-- The condition variable queues. -/
  cvQueues : second.cvQueues = first.cvQueues
  /-- The external channels. -/
  inputQueues : second.inputQueues = first.inputQueues
  /-- The mutexes. -/
  mutexOwners : second.mutexOwners = first.mutexOwners
  /-- The joinable threads. -/
  joinable : second.joinable = first.joinable
  /-- The cancel flags. -/
  cancelFlags : second.cancelFlags = first.cancelFlags
  /-- The early wakes. -/
  cvWakes : second.cvWakes = first.cvWakes

end DexExpressiveness
