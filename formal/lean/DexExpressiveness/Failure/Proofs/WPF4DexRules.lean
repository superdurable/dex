import DexExpressiveness.Failure.Proofs.WPF4DexInvariant

/-!
# WP-F4: [PUBLISH] and the Exhausted branch of a failure

Both are instances of `invariant_dexCommit`.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- A kept Step table is quiet when closed, if the old one was. -/
theorem closedQuiet_of_keep {registered : Option Nat} {old new : Dex.Run σ}
    (keep : StepsKeep registered old new)
    (oldQuiet : (∃ status, old.status = .closed status) →
      ∀ record, record ∈ old.steps → IsQuietRecord record) :
    (∃ status, new.status = .closed status) →
      ∀ record, record ∈ new.steps → IsQuietRecord record := by
  intro isClosed record isRecord
  rcases keep.status with sameStatus | ⟨_, allQuiet⟩
  · obtain ⟨original, isOriginal, isStep⟩ := keep.backward record isRecord
    exact isStep.quietKept (oldQuiet (sameStatus ▸ isClosed) original isOriginal)
  · exact allQuiet record isRecord

/-- An evolved Step table of a running run is quiet when closed. -/
theorem closedQuiet_of_evolve {registered : Option Nat} {old new : Dex.Run σ}
    (evolve : StepsEvolve registered old new) (isRunning : old.status = .running) :
    (∃ status, new.status = .closed status) →
      ∀ record, record ∈ new.steps → IsQuietRecord record := by
  intro isClosed record isRecord
  rcases evolve.status with sameStatus | ⟨_, allQuiet⟩
  · obtain ⟨status, isClosedStatus⟩ := isClosed
    rw [sameStatus, isRunning] at isClosedStatus
    cases isClosedStatus
  · exact allQuiet record isRecord

/-- A Backoff attempt state has its retry timer armed. -/
theorem backoffArmed_of_invariant {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run) :
    ∀ step attempt, run.attempts.get? step = some attempt → attempt.isBackoff = true →
      run.retryTimers.contains
        { step := step, method := attempt.method, attempt := attempt.number } = true := by
  have runInvariant := invariant.runInvariant run isRun
  intro step attempt isAttempt isBackoff
  obtain ⟨record, isRecord, isId, hasInvocation⟩ :=
    runInvariant.attemptsHaveInvocations _ (mem_of_get? _ _ _ isAttempt)
  simp only at isId
  have isMatching := runInvariant.attemptsMatch record isRecord
  unfold AttemptMatchesRecord at isMatching
  cases hInvocation : record.invocation with
  | none => rw [hInvocation] at hasInvocation; cases hasInvocation
  | some invocation =>
    rw [hInvocation] at isMatching
    obtain ⟨found, _, isFound, _, _, sameMethod, _, _, _, backoffClause⟩ := isMatching
    unfold Run.findAttempt? at isFound
    rw [isId, isAttempt] at isFound
    cases isFound
    obtain ⟨_, isContained⟩ := backoffClause isBackoff
    rw [sameMethod, ← isId]
    exact isContained

/-- An inactive Step has no invocation. -/
theorem inactiveQuiet_of_invariant {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run) :
    ∀ record, record ∈ run.base.steps → record.phase.isActive = false →
      record.invocation = none := by
  intro record isRecord isInactive
  have phaseMatches := (invariant.runInvariant run isRun).invocationMatchesPhase record isRecord
  cases hInvocation : record.invocation with
  | none => rfl
  | some invocation =>
    rw [hInvocation] at phaseMatches
    simp only at phaseMatches
    rw [phaseMatches] at isInactive
    cases hMethod : invocation.method <;> simp [hMethod, Dex.Phase.isActive] at isInactive

/-- A run with an invocation is running. -/
theorem running_of_invocation {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    {record : Dex.StepRecord σ} (isRecord : record ∈ run.base.steps) {invocation : Dex.Invocation}
    (hasInvocation : record.invocation = some invocation) : run.base.status = .running := by
  cases isStatus : run.base.status with
  | running => rfl
  | closed status =>
    have := ((invariant.runInvariant run isRun).closedRunIsQuiet ⟨status, isStatus⟩ record
      isRecord).2
    rw [hasInvocation] at this
    cases this

/-- The commit of [PUBLISH] keeps the invariants. -/
theorem invariant_publishCommit {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true) (chan : σ.Chan)
    (value : σ.Val) :
    Invariant program (config.commitRun run true none [Obs.pub chan value] (fun current =>
      { current with
        base := (current.base.applyPublications [(chan, value)]).wake [chan] })).2 := by
  rw [commitRun_eq]
  have runInvariant := invariant.runInvariant run isRun
  have keep := stepsKeep_publish (bumpedRun run).base [(chan, value)] [chan]
    runInvariant.stepIdsNodup
  exact invariant_dexCommit invariant isRun canCommit isUp config rfl rfl rfl rfl
    (fun _ isTask => isTask) invariant.realTaskIdsNodup (fun _ isTask => isTask)
    (fun _ isInterrupt => isInterrupt) _ none true none keep.toEvolve
    (fun _ isNone => by cases isNone) (closedQuiet_of_keep keep runInvariant.closedRunIsQuiet)
    (fun _ => Or.inl rfl) (backoffArmed_of_invariant (run := run) invariant isRun) (fun _ isEntry => isEntry)
    runInvariant.retryTimersNodup (fun _ _ isNone => by cases isNone)
    (fun _ isTask isNot => absurd isTask isNot) (fun _ isInterrupt isNot => absurd isInterrupt isNot)

/-- The Exhausted branch of `failAttempt` keeps the invariants. -/
theorem invariant_failAttempt_exhausted {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true)
    (interrupts : List (Interrupt σ))
    (interruptsSub : ∀ interrupt, interrupt ∈ interrupts → interrupt ∈ config.interrupts)
    (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isFound : run.base.findStep? stepId = some record)
    (method : Dex.Method) (oldState : Dex.InvocationState)
    (hasInvocation : record.invocation = some { method := method, state := oldState })
    (attempt : Attempt σ) (cause : FailureCause) (lostTaskId : Option Nat)
    (reason : ExhaustionReason) :
    Invariant program ({ config with interrupts := interrupts }.failAttempt run stepId method
      attempt cause lostTaskId (.exhausted reason)).2 := by
  have runInvariant := invariant.runInvariant run isRun
  obtain ⟨isRecord, _⟩ := mem_of_findStep? isFound
  have isRunning := running_of_invocation invariant isRun isRecord hasInvocation
  have inactiveQuiet := inactiveQuiet_of_invariant invariant isRun
  have keep := stepsKeep_closeRun (bumpedRun run).base (.failed exhaustionMessage) inactiveQuiet
  have allQuiet := (closeRun_quiet (bumpedRun run).base (.failed exhaustionMessage) isRunning
    inactiveQuiet).2
  have noRunning : ∀ changedRecord method taskId,
      changedRecord ∈ ((bumpedRun run).base.closeRun (.failed exhaustionMessage)).steps →
      changedRecord.invocation ≠ some { method := method, state := .running taskId } := by
    intro changedRecord method taskId isChanged hasRunning
    rw [(allQuiet changedRecord isChanged).2] at hasRunning
    cases hasRunning
  have common := fun (pre : Config σ) (preRun : pre.run = config.run)
      (preGhost : pre.ghost = config.ghost) (preNextTaskId : pre.nextTaskId = config.nextTaskId)
      (preUp : pre.isUp = config.isUp)
      (realSub : ∀ task, task ∈ pre.realTasks → task ∈ config.realTasks)
      (realNodup : (pre.realTasks.map (fun task => task.base.id)).Nodup)
      (tasksSub : ∀ task, task ∈ pre.tasks → task ∈ config.tasks)
      (preInterruptsSub : ∀ interrupt, interrupt ∈ pre.interrupts →
        interrupt ∈ config.interrupts) =>
    invariant_dexCommit invariant isRun canCommit isUp pre preRun preGhost preNextTaskId preUp
      realSub realNodup tasksSub preInterruptsSub
      { bumpedRun run with base := (bumpedRun run).base.closeRun (.failed exhaustionMessage) }
      none false none keep.toEvolve (fun _ isNone => by cases isNone)
      (closedQuiet_of_keep keep runInvariant.closedRunIsQuiet) (fun _ => Or.inl rfl)
      (backoffArmed_of_invariant (run := run) invariant isRun) (fun _ isEntry => isEntry)
      runInvariant.retryTimersNodup (fun _ _ isNone => by cases isNone)
      (fun _ _ _ changedRecord method isChanged => noRunning changedRecord method _ isChanged)
      (fun _ _ _ changedRecord method taskId isChanged _ =>
        noRunning changedRecord method taskId isChanged)
  unfold Config.failAttempt
  cases lostTaskId with
  | none =>
    simp only [commitRun_eq]
    exact common { config with interrupts := interrupts } rfl rfl rfl rfl
      (fun task isTask => isTask) invariant.realTaskIdsNodup (fun task isTask => isTask)
      interruptsSub
  | some lostId =>
    simp only [commitRun_eq]
    have isPerm := realTasks_moveToZombies { config with interrupts := interrupts } lostId
    exact common (({ config with interrupts := interrupts }).moveToZombies lostId) rfl rfl rfl
      rfl (fun task isTask => isPerm.mem_iff.mp isTask)
      ((isPerm.map _).nodup_iff.mpr invariant.realTaskIdsNodup)
      (fun task isTask => (List.mem_filter.mp isTask).1) interruptsSub

end DexExpressiveness.DexF.WPF4
