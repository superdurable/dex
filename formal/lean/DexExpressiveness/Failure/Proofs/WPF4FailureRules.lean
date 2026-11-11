import DexExpressiveness.Failure.Proofs.WPF4SingleInvariant

/-!
# WP-F4: [ATTEMPT-LOST] with RetryAt, [ATTEMPT-RETURNED], [ATTEMPT-HEARTBEAT]

Each is an instance of `invariant_singleRecord`. The RetryAt branch of
`failAttempt` serves [ATTEMPT-LOST] (Running and before-start) and an applied
[RECOVER-INTERRUPT]; the `interrupts` argument is the interrupt list after the
removal that [RECOVER-INTERRUPT] makes.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- `canServeCommit` gives `up` and `canCommit`. -/
theorem canServeCommit_parts {config : Config σ} (canServe : config.canServeCommit = true) :
    config.isUp = true ∧ config.canCommit = true := by
  unfold Config.canServeCommit at canServe
  simpa using canServe

/-- The attempt state of a Step with an invocation has its method and a
positive number. -/
theorem attempt_of_invocation {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    {record : Dex.StepRecord σ} (isRecord : record ∈ run.base.steps) {invocation : Dex.Invocation}
    (hasInvocation : record.invocation = some invocation) {attempt : Attempt σ}
    (isAttempt : run.findAttempt? record.id = some attempt) :
    attempt.method = invocation.method ∧ 1 ≤ attempt.number := by
  have isMatching := (invariant.runInvariant run isRun).attemptsMatch record isRecord
  unfold AttemptMatchesRecord at isMatching
  rw [hasInvocation] at isMatching
  obtain ⟨found, _, isFound, _, _, sameMethod, isPositive, _⟩ := isMatching
  rw [isAttempt] at isFound
  cases isFound
  exact ⟨sameMethod, isPositive⟩

/-- The RetryAt branch of `failAttempt` keeps the invariants. -/
theorem invariant_failAttempt_retry {program : Program σ} {config : Config σ} {run : Run σ}
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
    (noLostPending : lostTaskId = none → oldState = .pending) (backoff : Nat) :
    Invariant program ({ config with interrupts := interrupts }.failAttempt run stepId method
      attempt cause lostTaskId (.retryAfter backoff)).2 := by
  have runInvariant := invariant.runInvariant run isRun
  obtain ⟨isRecord, isId⟩ := mem_of_findStep? isFound
  subst isId
  obtain ⟨sameMethod, _⟩ := attempt_of_invocation invariant isRun isRecord hasInvocation isAttempt
  let newAttempt : Attempt σ :=
    { attempt with isBackoff := true, number := attempt.number + 1, lastFailure := some cause }
  let timer : RetryTimerId σ :=
    { step := record.id, method := method, attempt := attempt.number + 1 }
  have retryFrom : ∀ entry, entry ∈ run.retryTimers.set timer (config.clock + backoff) →
      entry ∈ run.retryTimers ∨ (entry.1.step = record.id ∧ entry.1.method = method) := by
    intro entry isMember
    rcases AssocList.mem_set _ _ _ _ isMember with isOld | isNew
    · exact Or.inl isOld
    · right
      rw [isNew]
      exact ⟨rfl, rfl⟩
  have common := fun (pre : Config σ) (preRun : pre.run = config.run)
      (preGhost : pre.ghost = config.ghost) (preNextTaskId : pre.nextTaskId = config.nextTaskId)
      (preUp : pre.isUp = config.isUp)
      (realSub : ∀ task, task ∈ pre.realTasks → task ∈ config.realTasks)
      (realNodup : (pre.realTasks.map (fun task => task.base.id)).Nodup)
      (tasksSub : ∀ task, task ∈ pre.tasks → task ∈ config.tasks)
      (preInterruptsSub : ∀ interrupt, interrupt ∈ pre.interrupts → interrupt ∈ config.interrupts)
      (tasksKept : ∀ task, task ∈ config.tasks → task ∉ pre.tasks →
        oldState = .running task.base.id)
      (removedRunning : ∀ taskId, oldState = .running taskId → Dex.InvocationState.pending = .pending →
        ∀ task, task ∈ pre.tasks → task.base.id ≠ taskId)
      (preInterruptsKept : ∀ interrupt, interrupt ∈ config.interrupts →
        interrupt ∉ pre.interrupts → interrupt.step = record.id) =>
    invariant_singleRecord invariant isRun canCommit isUp pre preRun preGhost preNextTaskId preUp
      realSub realNodup tasksSub preInterruptsSub record isRecord method oldState .pending
      hasInvocation tasksKept removedRunning preInterruptsKept attempt newAttempt isAttempt
      sameMethod (Nat.succ_pos _) rfl (fun _ isRunning => by cases isRunning)
      ({ bumpedRun run with
        base := (bumpedRun run).base.updateStep record.id (fun current =>
          { current with invocation := some { method := method, state := .pending } })
        attempts := (bumpedRun run).attempts.set record.id newAttempt
        retryTimers := (bumpedRun run).retryTimers.set timer (config.clock + backoff) })
      rfl rfl rfl rfl rfl (AssocList.get?_set_self _ _ _)
      (fun _ isOther => AssocList.get?_set_other _ _ _ _ isOther) retryFrom
      (fun found _ isContained => by
        show AssocList.contains (run.retryTimers.set timer _) found = true
        rw [contains_set, isContained, Bool.or_true])
      (AssocList.nodup_keys_set _ _ _ runInvariant.retryTimersNodup)
      (fun _ => ⟨rfl, by
        show AssocList.contains (run.retryTimers.set timer _) timer = true
        rw [contains_set]; simp⟩)
      (fun entry isMember isStep isMethod => by
        rcases AssocList.mem_set _ _ _ _ isMember with isOld | isNew
        · exact Nat.le_succ_of_le (runInvariant.retryTimersNotAhead entry record
            { method := method, state := oldState } attempt isOld isRecord isStep.symm
            hasInvocation isMethod.symm isAttempt)
        · rw [isNew]; exact Nat.le_refl _)
      false
  unfold Config.failAttempt
  cases lostTaskId with
  | none =>
    simp only [commitRun_eq]
    exact common { config with interrupts := interrupts } rfl rfl rfl rfl
      (fun task isTask => isTask) invariant.realTaskIdsNodup (fun task isTask => isTask)
      interruptsSub (fun task isTask isNot => absurd isTask isNot)
      (fun taskId isRunning _ => by rw [noLostPending rfl] at isRunning; cases isRunning)
      interruptsKept
  | some lostId =>
    simp only [commitRun_eq]
    have isPerm := realTasks_moveToZombies { config with interrupts := interrupts } lostId
    exact common (({ config with interrupts := interrupts }).moveToZombies lostId) rfl rfl rfl
      rfl (fun task isTask => isPerm.mem_iff.mp isTask)
      ((isPerm.map _).nodup_iff.mpr invariant.realTaskIdsNodup)
      (fun task isTask => (List.mem_filter.mp isTask).1) interruptsSub
      (fun task isTask isNot => by
        rw [lostMatches lostId rfl]
        by_cases isRemoved : task.base.id = lostId
        · rw [isRemoved]
        · exact absurd (List.mem_filter.mpr ⟨isTask, by simp [isRemoved]⟩) isNot)
      (fun taskId isRunning _ task isTask => by
        rw [lostMatches lostId rfl] at isRunning
        cases isRunning
        have := (List.mem_filter.mp isTask).2
        simpa using this)
      interruptsKept

/-- [ATTEMPT-RETURNED] keeps the invariants. -/
theorem invariant_attemptReturned (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ) (events : List (Obs σ))
    (next : Config σ)
    (isStep : step program config (.attemptReturned stepId) = some (events, next)) :
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
        · rename_i method taskId hasInvocation
          split at isStep
          · cases isStep
          · rename_i isGuard
            simp only [Option.some.injEq] at isStep
            have isNext := congrArg Prod.snd isStep
            simp only [commitRun_eq] at isNext
            subst isNext
            have runInvariant := invariant.runInvariant run isRun
            have notBackoff : attempt.isBackoff = false := by
              simp only [Bool.or_eq_true, not_or] at isGuard
              simpa using isGuard.1
            obtain ⟨isRecord, isId⟩ := mem_of_findStep? isFound
            subst isId
            obtain ⟨sameMethod, isPositive⟩ :=
              attempt_of_invocation invariant isRun isRecord hasInvocation isAttempt
            have isPerm := realTasks_moveToZombies config taskId
            exact invariant_singleRecord invariant isRun canCommit isUp (config.moveToZombies taskId)
              rfl rfl rfl rfl (fun task isTask => isPerm.mem_iff.mp isTask)
              ((isPerm.map _).nodup_iff.mpr invariant.realTaskIdsNodup)
              (fun task isTask => (List.mem_filter.mp isTask).1) (fun _ isMember => isMember)
              record isRecord method (.running taskId) .pending hasInvocation
              (fun task isTask isNot => by
                by_cases isRemoved : task.base.id = taskId
                · rw [isRemoved]
                · exact absurd (List.mem_filter.mpr ⟨isTask, by simp [isRemoved]⟩) isNot)
              (fun runningId isRunning _ task isTask => by
                cases isRunning
                have := (List.mem_filter.mp isTask).2
                simpa using this)
              (fun _ isMember isNot => absurd isMember isNot) attempt attempt isAttempt
              sameMethod isPositive rfl (fun _ isRunning => by cases isRunning)
              { bumpedRun run with
                base := (bumpedRun run).base.updateStep record.id (fun current =>
                  { current with invocation := some { method := method, state := .pending } }) }
              rfl rfl rfl rfl rfl isAttempt (fun _ _ => rfl) (fun entry isMember => Or.inl isMember)
              (fun _ _ isContained => isContained) runInvariant.retryTimersNodup
              (fun isBackoff => by rw [notBackoff] at isBackoff; cases isBackoff)
              (fun entry isMember isStep isMethod =>
                runInvariant.retryTimersNotAhead entry record _ attempt isMember isRecord
                  isStep.symm hasInvocation isMethod.symm isAttempt)
              false
        · cases isStep
      · cases isStep
  · cases isStep

/-- [ATTEMPT-HEARTBEAT] keeps the invariants. -/
theorem invariant_attemptHeartbeat (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ) (value : σ.Val)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.attemptHeartbeat stepId value) = some (events, next)) :
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
        · rename_i method taskId hasInvocation
          split at isStep
          · cases isStep
          · rename_i isGuard
            simp only [Option.some.injEq] at isStep
            have isNext := congrArg Prod.snd isStep
            simp only [commitRun_eq] at isNext
            subst isNext
            have runInvariant := invariant.runInvariant run isRun
            have notBackoff : attempt.isBackoff = false := by
              simp only [Bool.or_eq_true, not_or] at isGuard
              simpa using isGuard.1
            obtain ⟨isRecord, isId⟩ := mem_of_findStep? isFound
            subst isId
            obtain ⟨sameMethod, isPositive⟩ :=
              attempt_of_invocation invariant isRun isRecord hasInvocation isAttempt
            have sameSteps : run.base.steps = run.base.steps.map (fun current =>
                if current.id = record.id then setInvocation method (.running taskId) current
                else current) := by
              have isIdentity : ∀ current, current ∈ run.base.steps →
                  (if current.id = record.id then setInvocation method (.running taskId) current
                    else current) = current := by
                intro current isCurrent
                split
                · rename_i sameId
                  rw [eq_of_id_eq runInvariant.stepIdsNodup isCurrent isRecord sameId]
                  unfold setInvocation
                  rw [← hasInvocation]
                · rfl
              rw [List.map_congr_left isIdentity, List.map_id']
            exact invariant_singleRecord invariant isRun canCommit isUp config rfl rfl rfl rfl
              (fun task isTask => isTask) invariant.realTaskIdsNodup (fun task isTask => isTask)
              (fun _ isMember => isMember) record isRecord method (.running taskId)
              (.running taskId) hasInvocation (fun _ isTask isNot => absurd isTask isNot)
              (fun _ _ isPending => by cases isPending)
              (fun _ isMember isNot => absurd isMember isNot) attempt
              { attempt with lastHeartbeatValue := some value } isAttempt sameMethod isPositive rfl
              (fun runningId isRunning => by
                cases isRunning
                exact ⟨rfl, rfl, rfl, rfl, rfl⟩)
              ((bumpedRun run).setAttempt record.id { attempt with lastHeartbeatValue := some value })
              sameSteps rfl rfl rfl rfl (AssocList.get?_set_self _ _ _)
              (fun _ isOther => AssocList.get?_set_other _ _ _ _ isOther)
              (fun entry isMember => Or.inl isMember)
              (fun _ _ isContained => isContained) runInvariant.retryTimersNodup
              (fun isBackoff => by
                have : attempt.isBackoff = true := isBackoff
                rw [notBackoff] at this; cases this)
              (fun entry isMember isStep isMethod =>
                runInvariant.retryTimersNotAhead entry record _ attempt isMember isRecord
                  isStep.symm hasInvocation isMethod.symm isAttempt)
              false
        · cases isStep
      · cases isStep
  · cases isStep

end DexExpressiveness.DexF.WPF4
