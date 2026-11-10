import DexExpressiveness.Failure.Proofs.WPF4Rules

/-!
# WP-F4: every DexF step keeps the invariants

`invariant_step_proof` proves the statement `invariant_step` of
`Failure/Statements/Leaf.lean`. It dispatches on the action to one theorem
per rule.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- `failAttempt` keeps the invariants, for either decision. -/
theorem invariant_failAttempt {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true)
    (interrupts : List (Interrupt σ))
    (interruptsSub : ∀ interrupt, interrupt ∈ interrupts → interrupt ∈ config.interrupts)
    (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isFound : run.base.findStep? stepId = some record)
    (method : Dex.Method) (oldState : Dex.InvocationState)
    (hasInvocation : record.invocation = some { method := method, state := oldState })
    (attempt : Attempt σ) (isAttempt : run.findAttempt? stepId = some attempt)
    (interruptsKept : ∀ interrupt, interrupt ∈ config.interrupts → interrupt ∉ interrupts →
      interrupt.step = stepId)
    (cause : FailureCause) (lostTaskId : Option Nat)
    (lostMatches : ∀ taskId, lostTaskId = some taskId → oldState = .running taskId)
    (noLostPending : lostTaskId = none → oldState = .pending) (decision : RetryDecision) :
    Invariant program ({ config with interrupts := interrupts }.failAttempt run stepId method
      attempt cause lostTaskId decision).2 := by
  cases decision with
  | retryAfter backoff =>
    exact invariant_failAttempt_retry invariant isRun canCommit isUp interrupts interruptsSub stepId
      record isFound method oldState hasInvocation attempt isAttempt interruptsKept cause lostTaskId
      lostMatches noLostPending backoff
  | exhausted reason =>
    exact invariant_failAttempt_exhausted invariant isRun canCommit isUp interrupts interruptsSub
      stepId record isFound method oldState hasInvocation attempt cause lostTaskId reason

/-- [ATTEMPT-LOST] and [ATTEMPT-EXHAUSTED] keep the invariants. -/
theorem invariant_attemptFailed (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ) (decision : RetryDecision)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.attemptFailed stepId decision) = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · rename_i canServe
    obtain ⟨isUp, canCommit⟩ := canServeCommit_parts canServe
    split at isStep
    · cases isStep
    · rename_i run isRun
      split at isStep
      · rename_i record attempt isFound isAttempt
        split at isStep
        · rename_i method taskId hasRunning
          split at isStep
          · cases isStep
          · simp only [Option.some.injEq] at isStep
            have isNext := congrArg Prod.snd isStep
            subst isNext
            exact invariant_failAttempt invariant isRun canCommit isUp config.interrupts
              (fun _ isMember => isMember) stepId record isFound method _ hasRunning attempt
              isAttempt (fun _ isMember isNot => absurd isMember isNot) .attemptLost (some taskId)
              (fun _ isSome => by cases isSome; rfl) (fun isNone => by cases isNone) decision
        · rename_i method hasPending
          split at isStep
          · cases isStep
          · simp only [Option.some.injEq] at isStep
            have isNext := congrArg Prod.snd isStep
            subst isNext
            exact invariant_failAttempt invariant isRun canCommit isUp config.interrupts
              (fun _ isMember => isMember) stepId record isFound method _ hasPending attempt
              isAttempt (fun _ isMember isNot => absurd isMember isNot) .beforeStart none
              (fun _ isSome => by cases isSome) (fun _ => rfl) decision
        · cases isStep
      · cases isStep
  · cases isStep

/-- The guard of [RECOVER-INTERRUPT] holds for the interrupt of a running
invocation. -/
theorem interruptGuard_of_running {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    {record : Dex.StepRecord σ} (isRecord : record ∈ run.base.steps) {method : Dex.Method}
    {taskId : Nat} (hasRunning : record.invocation = some { method := method, state := .running taskId })
    {attempt : Attempt σ} (isAttempt : run.findAttempt? record.id = some attempt) :
    (decide (record.invocation = some { method := method, state := .running taskId }) &&
      decide (attempt.method = method) && !attempt.isBackoff &&
      decide (attempt.number = attempt.number)) = true := by
  have isMatching := (invariant.runInvariant run isRun).attemptsMatch record isRecord
  unfold AttemptMatchesRecord at isMatching
  rw [hasRunning] at isMatching
  obtain ⟨found, _, isFound, _, _, sameMethod, _, isRunningClause, _⟩ := isMatching
  rw [isAttempt] at isFound
  cases isFound
  have notBackoff := (isRunningClause taskId rfl).1
  simp [hasRunning, sameMethod, notBackoff]

/-- [RECOVER-INTERRUPT] keeps the invariants. -/
theorem invariant_recoverInterrupt (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (interrupt : Interrupt σ) (decision : RetryDecision)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.recoverInterrupt interrupt decision) = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · rename_i guard
    simp only [Bool.and_eq_true] at guard
    obtain ⟨canServe, _⟩ := guard
    obtain ⟨isUp, canCommit⟩ := canServeCommit_parts canServe
    have ignored : (∀ run, config.run = some run → ∀ record method taskId attempt,
        record ∈ run.base.steps →
        record.invocation = some { method := method, state := .running taskId } →
        run.findAttempt? record.id = some attempt →
        ({ step := record.id, method := method, attempt := attempt.number, taskId := taskId } :
          Interrupt σ) ≠ interrupt) →
        Invariant program { config with interrupts := config.interrupts.erase interrupt }.stampOnly :=
      fun notTracking => invariant_stampOnly (invariant_eraseInterrupt invariant interrupt notTracking)
        canCommit
    split at isStep
    · rename_i noRun
      simp only [Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨-, rfl⟩ := isStep
      apply ignored
      intro run isRun
      rw [noRun] at isRun
      cases isRun
    · rename_i run isRun
      have idsNodup := (invariant.runInvariant run isRun).stepIdsNodup
      split at isStep
      · rename_i record attempt isFound isAttempt
        split at isStep
        · rename_i isGuard
          simp only [Option.some.injEq] at isStep
          have isNext := congrArg Prod.snd isStep
          subst isNext
          simp only [Bool.and_eq_true, decide_eq_true_eq] at isGuard
          obtain ⟨⟨⟨hasRunning, _⟩, _⟩, _⟩ := isGuard
          refine invariant_failAttempt invariant isRun canCommit isUp
            (config.interrupts.erase interrupt) (fun _ isMember => List.mem_of_mem_erase isMember)
            interrupt.step record isFound interrupt.method _ hasRunning attempt isAttempt ?_
            .interrupted (some interrupt.taskId) (fun _ isSome => by cases isSome; rfl)
            (fun isNone => by cases isNone) decision
          intro other isOther isNotKept
          by_cases isSame : other = interrupt
          · rw [isSame]
          · exact absurd ((List.mem_erase_of_ne isSame).mpr isOther) isNotKept
        · rename_i isGuard
          simp only [Option.some.injEq, Prod.mk.injEq] at isStep
          obtain ⟨-, rfl⟩ := isStep
          apply ignored
          intro otherRun isOtherRun tracked method taskId trackedAttempt isTracked hasRunning
            isTrackedAttempt isSame
          rw [isRun] at isOtherRun
          cases isOtherRun
          subst isSame
          have isRecordFound := findStep?_of_mem idsNodup isTracked
          rw [isRecordFound] at isFound
          cases isFound
          rw [isTrackedAttempt] at isAttempt
          cases isAttempt
          exact isGuard (interruptGuard_of_running invariant isRun isTracked hasRunning
            isTrackedAttempt)
      · rename_i notBoth
        simp only [Option.some.injEq, Prod.mk.injEq] at isStep
        obtain ⟨-, rfl⟩ := isStep
        apply ignored
        intro otherRun isOtherRun tracked method taskId trackedAttempt isTracked _
          isTrackedAttempt isSame
        rw [isRun] at isOtherRun
        cases isOtherRun
        subst isSame
        exact notBoth tracked trackedAttempt (findStep?_of_mem idsNodup isTracked) isTrackedAttempt
  · cases isStep

/-- The Complete rules keep the invariants. -/
theorem invariant_stepComplete (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.stepComplete taskId) = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · rename_i canServe
    obtain ⟨isUp, canCommit⟩ := canServeCommit_parts canServe
    split at isStep
    · rename_i run task isRun isFound
      split at isStep
      · rename_i isPassed
        split at isStep
        · rename_i response isMethod isPhase
          simp only [Option.some.injEq] at isStep
          have isNext := congrArg Prod.snd isStep
          subst isNext
          exact invariant_completeWaitFor invariant isRun canCommit isUp taskId task isFound isPassed
            isMethod response isPhase
        · rename_i response isMethod isPhase
          simp only [Option.some.injEq] at isStep
          have isNext := congrArg Prod.snd isStep
          subst isNext
          exact invariant_completeExecute invariant isRun canCommit isUp taskId task isFound
            isPassed isMethod response isPhase
        · cases isStep
      · cases isStep
    · cases isStep
  · cases isStep

/-- [RESULT-STALE] keeps the invariants. -/
theorem invariant_resultStale (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isLastOfCanceled : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.resultStale taskId isLastOfCanceled) = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · rename_i canServe
    obtain ⟨_, canCommit⟩ := canServeCommit_parts canServe
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep
      · split at isStep
        · cases isStep
        · rename_i isGuard
          simp only [Option.some.injEq, Prod.mk.injEq] at isStep
          obtain ⟨-, rfl⟩ := isStep
          have notPassed : config.isFencePassed task = false := by
            simp only [Bool.or_eq_true, not_or] at isGuard
            simpa using isGuard.1
          obtain ⟨isTask, isTaskId⟩ := mem_of_findRealTask? isFound
          refine invariant_stampOnly (config := config.removeRealTask taskId) ?_ canCommit
          refine invariant_removeRealTask invariant taskId ?_
          intro other isOther isId
          rw [task_eq_of_id_eq invariant.realTaskIdsNodup (List.mem_append_left _ isOther) isTask
            (by rw [isId, isTaskId])]
          exact notPassed
      · cases isStep
  · cases isStep

/-- [TIMER-FIRE] with [RETRY-FIRE] keeps the invariants. -/
theorem invariant_timerFire (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (count : Nat) (isWaitTimerFired : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.timerFire count isWaitTimerFired) = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · rename_i canServe
    obtain ⟨isUp, canCommit⟩ := canServeCommit_parts canServe
    split at isStep
    · cases isStep
    · rename_i run isRun
      split at isStep
      · simp only [Option.some.injEq] at isStep
        have isNext := congrArg Prod.snd isStep
        subst isNext
        exact invariant_timerFireCommit invariant isRun canCommit isUp _ _
      · cases isStep
  · cases isStep

/-- [START-RETRY] keeps the invariants. -/
theorem invariant_startRetry (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (requestId : Nat) (stepType : σ.StepType)
    (input : σ.Val) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.startRetry requestId stepType input) = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · cases isStep
  · split at isStep
    · rename_i guard
      simp only [Bool.and_eq_true] at guard
      simp only [Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨-, rfl⟩ := isStep
      exact invariant_stampOnly invariant (canServeCommit_parts guard.1).2
    · cases isStep

/-- [ENGINE-REJECT] keeps the invariants. -/
theorem invariant_engineReject (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (request : Request σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config (.engineReject request) = some (events, next)) :
    Invariant program next := by
  cases request with
  | startFlow => simp [step] at isStep
  | publish chan value =>
    simp only [step] at isStep
    split at isStep
    · simp only [Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨-, rfl⟩ := isStep
      exact invariant
    · cases isStep

/-- An internal commit keeps the invariants. -/
theorem invariant_internalCommit (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config .internalCommit = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · rename_i canServe
    simp only [Option.some.injEq, Prod.mk.injEq] at isStep
    obtain ⟨-, rfl⟩ := isStep
    exact invariant_stampOnly invariant (canServeCommit_parts canServe).2
  · cases isStep

end DexExpressiveness.DexF.WPF4

namespace DexExpressiveness.DexF

open DexExpressiveness

variable {σ : Dex.Signature}

/-- **WP-F4.** Every DexF step keeps the invariants, for a program that
satisfies H3. The proof of `invariant_step`. -/
theorem invariant_step_proof (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next)) :
    Invariant program next := by
  cases action with
  | tick duration => exact WPF4.invariant_tick program config invariant duration events next isStep
  | request request => exact WPF4.invariant_request program config invariant request events next isStep
  | startRetry requestId stepType input =>
    exact WPF4.invariant_startRetry program config invariant requestId stepType input events next isStep
  | engineReject request =>
    exact WPF4.invariant_engineReject program config invariant request events next isStep
  | stepStart stepId isSnapshotReused =>
    exact WPF4.invariant_stepStart program config invariant stepId isSnapshotReused events next isStep
  | workerRun taskId isFirstRun =>
    exact WPF4.invariant_workerRun program ignoresAttemptContext config invariant taskId isFirstRun
      events next isStep
  | workerEffect taskId isFirstOccurrence =>
    exact WPF4.invariant_workerEffect program config invariant taskId isFirstOccurrence events next
      isStep
  | workerReturn taskId isFirstReturn =>
    exact WPF4.invariant_workerReturn program config invariant taskId isFirstReturn events next isStep
  | workerAbandon taskId isLastOfCanceled =>
    exact WPF4.invariant_workerAbandon program config invariant taskId isLastOfCanceled events next
      isStep
  | stepComplete taskId =>
    exact WPF4.invariant_stepComplete program config invariant taskId events next isStep
  | resultStale taskId isLastOfCanceled =>
    exact WPF4.invariant_resultStale program config invariant taskId isLastOfCanceled events next
      isStep
  | timerFire count isWaitTimerFired =>
    exact WPF4.invariant_timerFire program config invariant count isWaitTimerFired events next isStep
  | attemptFailed stepId decision =>
    exact WPF4.invariant_attemptFailed program config invariant stepId decision events next isStep
  | attemptReturned stepId =>
    exact WPF4.invariant_attemptReturned program config invariant stepId events next isStep
  | attemptHeartbeat stepId value =>
    exact WPF4.invariant_attemptHeartbeat program config invariant stepId value events next isStep
  | crash => exact WPF4.invariant_crash program config invariant events next isStep
  | recover => exact WPF4.invariant_recover program config invariant events next isStep
  | recoverInterrupt interrupt decision =>
    exact WPF4.invariant_recoverInterrupt program config invariant interrupt decision events next
      isStep
  | internalCommit => exact WPF4.invariant_internalCommit program config invariant events next isStep

end DexExpressiveness.DexF
