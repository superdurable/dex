import DexExpressiveness.Failure.Proofs.WPF4Complete
import DexExpressiveness.Failure.Proofs.WPF4TimerFire
import DexExpressiveness.Failure.Proofs.WPF4StepStart

/-!
# WP-F4: the commit rules, from the step

Each theorem takes `step program config action = some (events, next)` for one
action and proves `Invariant program next` with the commit lemmas.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- The run that [FLOW-START] starts from. -/
def startRun (requestId : Nat) : Run σ :=
  { base := Dex.Run.empty σ, attempts := [], retryTimers := [], startRequestId := requestId }

/-- Before the start, the configuration with the empty run satisfies the
invariants. -/
theorem invariant_withEmptyRun {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (noRun : config.run = none) (requestId : Nat) :
    Invariant program { config with run := some (startRun requestId) } := by
  obtain ⟨noReal, noInterrupts, _, noInvocations, noCount, noIndex, _, _⟩ :=
    invariant.notStarted noRun
  have noTasks : config.tasks = [] := (List.append_eq_nil_iff.mp noReal).1
  refine
    { realTaskIdsNodup := invariant.realTaskIdsNodup
      realTaskIdsBelow := invariant.realTaskIdsBelow
      interruptsNotInTasks := invariant.interruptsNotInTasks
      interruptsBelow := invariant.interruptsBelow
      downIsEmpty := invariant.downIsEmpty
      lastNormalCommitAtMost := invariant.lastNormalCommitAtMost
      notStarted := fun isNone => by cases isNone
      ghostKeysNodup := invariant.ghostKeysNodup
      virtualTaskIdsNodup := invariant.virtualTaskIdsNodup
      virtualTaskIdsBelow := invariant.virtualTaskIdsBelow
      historiesConsistent := invariant.historiesConsistent
      realTasksFollowHistory := invariant.realTasksFollowHistory
      liveTasksPassFence := ?_
      runInvariant := ?_ }
  · intro task _ isTask
    have isConfigTask : task ∈ config.tasks := isTask
    rw [noTasks] at isConfigTask
    cases isConfigTask
  · intro run isRun
    cases isRun
    have noFind : ∀ key, config.ghost.findInvocation? key = none := by
      intro key
      unfold Ghost.findInvocation?
      rw [noInvocations]
      rfl
    exact
      { stepIdsNodup := List.nodup_nil
        stepNumbersCounted := fun _ isRecord => by cases isRecord
        invocationMatchesPhase := fun _ isRecord => by cases isRecord
        closedRunIsQuiet := fun _ _ isRecord => by cases isRecord
        historiesHaveSteps := fun entry isEntry => by
          have isConfigEntry : entry ∈ config.ghost.invocations := isEntry
          rw [noInvocations] at isConfigEntry
          cases isConfigEntry
        retryTimersHaveHistories := fun _ isEntry => by cases isEntry
        retryTimersNotAhead := fun _ _ _ _ isEntry => by cases isEntry
        waitTimersNodup := List.nodup_nil
        retryTimersNodup := List.nodup_nil
        attemptKeysNodup := List.nodup_nil
        attemptsHaveInvocations := fun _ isEntry => by cases isEntry
        normalIndexValues := by
          show config.ghost.normalIndex.map _ = List.range' 1 config.ghost.normalCount
          rw [noIndex, noCount]
          rfl
        normalIndexKeysIncrease := by
          show (config.ghost.normalIndex.map _).Pairwise _
          rw [noIndex]
          exact List.Pairwise.nil
        normalIndexKeysTaken := fun entry isEntry => by
          have isConfigEntry : entry ∈ config.ghost.normalIndex := isEntry
          rw [noIndex] at isConfigEntry
          cases isConfigEntry
        waitSeqsIndexed := fun _ _ isRecord => by cases isRecord
        attemptsMatch := fun _ isRecord => by cases isRecord
        liveIsCurrent := fun key history isFound => by
          have isConfigFound : config.ghost.findInvocation? key = some history := isFound
          rw [noFind] at isConfigFound
          cases isConfigFound
        runningTaskIdsBelow := fun _ _ _ isRecord => by cases isRecord
        runningTaskMatches := fun _ _ _ _ isRecord => by cases isRecord
        runningIsTracked := fun _ _ _ isRecord => by cases isRecord }

/-- The commit of [FLOW-START] keeps the invariants. -/
theorem invariant_flowStartCommit {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (noRun : config.run = none)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true) (requestId : Nat)
    (stepType : σ.StepType) (input : σ.Val) :
    Invariant program (config.commitRun (startRun requestId) true none [Obs.start stepType input]
      (fun current =>
        { current with
          base := current.base.newStep stepType (some input)
            { skipWaitFor := (program.failureFreeFlow.stepDef stepType).waitFor.isNone }
            .flowStart })).2 := by
  have started := invariant_withEmptyRun invariant noRun requestId
  have evolve := stepsEvolve_newStep (bumpedRun (startRun (σ := σ) requestId)).base stepType
    (some input) { skipWaitFor := (program.failureFreeFlow.stepDef stepType).waitFor.isNone }
    .flowStart (fun _ isRecord => by cases isRecord)
  rw [commitRun_eq]
  exact invariant_dexCommit started rfl canCommit isUp { config with run := some (startRun requestId) }
    rfl rfl rfl rfl (fun _ isTask => isTask) invariant.realTaskIdsNodup (fun _ isTask => isTask)
    (fun _ isInterrupt => isInterrupt) _ none true none evolve (fun _ isNone => by cases isNone)
    (closedQuiet_of_evolve evolve rfl) (fun _ => Or.inl rfl)
    (fun _ _ isAttempt => by cases isAttempt) (fun _ isEntry => isEntry) List.nodup_nil
    (fun _ _ isNone => by cases isNone) (fun _ isTask isNot => absurd isTask isNot)
    (fun _ isInterrupt isNot => absurd isInterrupt isNot)

/-- A client request keeps the invariants: [FLOW-START], [PUBLISH], and the
rejection of a publication. -/
theorem invariant_request (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (request : Request σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config (.request request) = some (events, next)) :
    Invariant program next := by
  cases request with
  | startFlow requestId stepType input =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · rename_i noRun
      split at isStep
      · rename_i canServe
        obtain ⟨isUp, canCommit⟩ := canServeCommit_parts canServe
        simp only [Option.some.injEq] at isStep
        have isNext := congrArg Prod.snd isStep
        subst isNext
        exact invariant_flowStartCommit invariant noRun canCommit isUp requestId stepType input
      · cases isStep
  | publish chan value =>
    simp only [step] at isStep
    split at isStep
    · rename_i isUp
      split at isStep
      · simp only [Option.some.injEq, Prod.mk.injEq] at isStep
        obtain ⟨-, rfl⟩ := isStep
        exact invariant
      · rename_i run isRun
        split at isStep
        · simp only [Option.some.injEq, Prod.mk.injEq] at isStep
          obtain ⟨-, rfl⟩ := isStep
          exact invariant
        · split at isStep
          · rename_i canCommit
            simp only [Option.some.injEq] at isStep
            have isNext := congrArg Prod.snd isStep
            subst isNext
            exact invariant_publishCommit invariant isRun canCommit isUp chan value
          · cases isStep
    · cases isStep

/-- [STEP-START] keeps the invariants. -/
theorem invariant_stepStart (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ) (isSnapshotReused : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.stepStart stepId isSnapshotReused) = some (events, next)) :
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
        · rename_i method hasPending
          split at isStep
          · cases isStep
          · rename_i isGuard
            simp only [Option.some.injEq] at isStep
            have isNext := congrArg Prod.snd isStep
            subst isNext
            obtain ⟨isRecord, isId⟩ := mem_of_findStep? isFound
            subst isId
            simp only [Bool.or_eq_true, not_or, bne_iff_ne, ne_eq, Decidable.not_not,
              Bool.not_eq_true', decide_eq_false_iff_not] at isGuard
            obtain ⟨⟨notBackoff, sameMethod⟩, isReuse⟩ := isGuard
            exact invariant_stepStartGeneral invariant isRun canCommit isUp record isRecord method
              hasPending attempt isAttempt (by simpa using notBackoff)
              (attempt.snapshot.getD (run.base.snapshot record.options method))
              (fun found isFoundSnapshot => by rw [isFoundSnapshot]; rfl)
              { attempt with
                snapshot := some (attempt.snapshot.getD (run.base.snapshot record.options method))
                firstAttemptTimestamp := some (attempt.context config.clock).firstAttemptTimestamp }
              rfl rfl rfl rfl
              { base :=
                  { id := config.nextTaskId, step := record.id, method := method,
                    context := { stepExecutionId := record.id, fromStepExecutionId := record.origin },
                    input := record.input,
                    snapshot := attempt.snapshot.getD (run.base.snapshot record.options method),
                    conditionResults := record.conditionResults, phase := .started }
                attemptContext := attempt.context config.clock
                emittedCount := 0 }
              rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl (!isSnapshotReused) isSnapshotReused isReuse
        · cases isStep
      · cases isStep
  · cases isStep

end DexExpressiveness.DexF.WPF4
