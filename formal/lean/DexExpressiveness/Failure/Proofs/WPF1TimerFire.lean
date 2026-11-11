import DexExpressiveness.Failure.Proofs.WPF1Steps

/-!
# WP-F1: [TIMER-FIRE] with wait timers

A [TIMER-FIRE] that fires a wait timer is a normal commit. Its retry timers
touch neither the base run nor attempt shapes, so the base change is the
failure-free [TIMER-FIRE] of the fired wait timers.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

theorem fireRetryTimer_base (run : Run σ) (timer : RetryTimerId σ) :
    (run.fireRetryTimer timer).base = run.base := by
  unfold Run.fireRetryTimer
  split
  · dsimp only
    split
    · split <;> rfl
    · rfl
  · rfl

theorem fireRetryTimer_attemptShape (run : Run σ) (timer : RetryTimerId σ)
    (step : Dex.StepExecId σ) :
    ((run.fireRetryTimer timer).findAttempt? step).map attemptShape =
      (run.findAttempt? step).map attemptShape := by
  unfold Run.fireRetryTimer
  split
  · dsimp only
    split
    · rename_i record attempt _ hasAttempt
      split
      · simp only [Run.setAttempt, Run.findAttempt?]
        by_cases isStep : step = timer.step
        · subst isStep
          rw [AssocList.get?_set_self]
          rw [hasAttempt]
          rfl
        · rw [AssocList.get?_set_other _ _ _ _ isStep]
      · rfl
    · rfl
  · rfl

theorem base_foldl_fireDueTimer (timers : List (DueTimer σ)) (run : Run σ) :
    (timers.foldl Run.fireDueTimer run).base =
      (timers.filterMap waitTimerOf?).foldl Dex.Run.fireTimer run.base := by
  induction timers generalizing run with
  | nil => rfl
  | cons timer laterTimers inductionHypothesis =>
    cases timer with
    | wait timer =>
      simp only [List.foldl_cons, List.filterMap_cons, waitTimerOf?]
      exact inductionHypothesis _
    | retry timer =>
      simp only [List.foldl_cons, List.filterMap_cons, waitTimerOf?]
      rw [inductionHypothesis, Run.fireDueTimer, fireRetryTimer_base]

theorem attemptShape_foldl_fireDueTimer (timers : List (DueTimer σ)) (run : Run σ)
    (step : Dex.StepExecId σ) :
    ((timers.foldl Run.fireDueTimer run).findAttempt? step).map attemptShape =
      (run.findAttempt? step).map attemptShape := by
  induction timers generalizing run with
  | nil => rfl
  | cons timer laterTimers inductionHypothesis =>
    rw [List.foldl_cons, inductionHypothesis]
    cases timer with
    | wait timer => rfl
    | retry timer => exact fireRetryTimer_attemptShape run timer step

/-- Every live invocation stays current through the fired wait timers. -/
theorem timerFire_staysCurrent {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (runInvariant : RunInvariant config run)
    (waits : List (Dex.TimerId σ)) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) (isLive : entry.2.outcome = .live) :
    (currentKeys (waits.foldl Dex.Run.fireTimer (bumpedRun run).base)).contains
      entry.1 = true := by
  obtain ⟨record, invocation, isRecord, isId, hasInvocation, isMethod⟩ :=
    live_isCurrent invariant runInvariant entry isMember isLive
  have nonWaiting := nonWaitingAt_of_invocation runInvariant record isRecord invocation
    hasInvocation
  have isKept := (keeps_foldl_fireTimer waits (bumpedRun run).base record.id nonWaiting).2
    record isRecord rfl
  have isCurrent :
      entry.1 ∈ currentKeys (waits.foldl Dex.Run.fireTimer (bumpedRun run).base) := by
    apply (mem_currentKeys _ _).mpr
    refine ⟨record, invocation, isKept, hasInvocation, ?_⟩
    rw [isId, isMethod]
  simpa using isCurrent

theorem timerFire_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (count : Nat)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.timerFire count true) = some (events, next)) :
    IsStepSimulated program config (.timerFire count true) events next := by
  unfold IsStepSimulated
  simp only [dexActionsOf, ↓reduceIte, exec_singleton]
  simp only [step] at isStep
  split at isStep
  · rename_i canServe
    split at isStep
    · cases isStep
    · rename_i run hasRun
      have runInvariant := invariant.runInvariant run hasRun
      have absRun : (abstractConfig config).run = some (Run.abstract config.ghost run) := by
        simp [abstractConfig, hasRun]
      split at isStep
      · rename_i premises
        simp only [Option.some.injEq] at isStep
        have canCommit : config.canCommit = true := by
          simp only [Config.canServeCommit, Bool.and_eq_true] at canServe
          exact canServe.2
        have absCommit := canCommit_abstract invariant canCommit
        simp only [Bool.and_eq_true, decide_eq_true_eq, beq_iff_eq] at premises
        obtain ⟨⟨isPositive, isAtMost⟩, anyWait⟩ := premises
        have isWaits := take_dueTimers_filterMap_waitTimerOf run config.clock count
        have lengthWaits := length_filterMap_waitTimerOf ((run.dueTimers config.clock).take count)
        have firedWaitCount : config.firedWaitTimerCount count =
            (((run.dueTimers config.clock).take count).filter DueTimer.isWait).length := by
          simp only [Config.firedWaitTimerCount, hasRun]
        have isPositiveWaits :
            0 < (((run.dueTimers config.clock).take count).filter DueTimer.isWait).length := by
          obtain ⟨timer, isFired, isWait⟩ := List.any_eq_true.mp anyWait.symm
          exact List.length_pos_of_mem (List.mem_filter.mpr ⟨isFired, isWait⟩)
        have isAtMostWaits :
            (((run.dueTimers config.clock).take count).filter DueTimer.isWait).length ≤
              (run.base.dueTimers config.clock).length := by
          have lengths := congrArg List.length isWaits
          rw [lengthWaits, List.length_take] at lengths
          omega
        have absDue : (Run.abstract config.ghost run).dueTimers (abstractConfig config).clock =
            run.base.dueTimers config.clock := rfl
        rw [firedWaitCount]
        simp only [Dex.step, absRun, absDue, isPositiveWaits, isAtMostWaits, absCommit,
          decide_true, Bool.and_self, ↓reduceIte, Dex.Config.commit]
        rw [← isWaits]
        obtain ⟨isEvents, isConfig⟩ := commit_simulated config run runInvariant none []
          (fun current => ((run.dueTimers config.clock).take count).foldl Run.fireDueTimer current)
          (fun base => (((run.dueTimers config.clock).take count).filterMap waitTimerOf?).foldl
            Dex.Run.fireTimer base)
          (base_foldl_fireDueTimer _ _)
          (fun step => attemptShape_foldl_fireDueTimer _ _ step)
          (foldl_fireTimer_relabelRun _ _ _ _ (isWaitFresh_bumpedRun runInvariant))
          (Dex.Run.seq_foldl_fireTimer _ _)
          (by
            rw [ids_foldl_fireTimer]
            exact runInvariant.stepIdsNodup)
        obtain ⟨isEventsEq, isNextEq⟩ := Prod.ext_iff.mp isStep
        subst isEventsEq isNextEq
        rw [isEvents, isConfig]
        obtain ⟨extra, isInvocations, isFresh⟩ := reconcile_invocations
          (countedGhost config.ghost (run.base.seq + 1) config.clock) none
          (((run.dueTimers config.clock).take count).foldl Run.fireDueTimer (bumpedRun run)).base
        have tasksEq : (config.commitRun run true none [] (fun current =>
            ((run.dueTimers config.clock).take count).foldl Run.fireDueTimer
              current)).2.virtualTasks = config.virtualTasks := by
          rw [commitRun_normal]
          refine virtualTasks_of_extra config _ ?_ extra ?_ ?_
          · rfl
          · rw [← map_markHistory_of_current config.ghost.invocations none
              (currentKeys (((run.dueTimers config.clock).take count).foldl Run.fireDueTimer
                (bumpedRun run)).base)
              (fun entry isMember isLive => by
                rw [base_foldl_fireDueTimer]
                exact timerFire_staysCurrent invariant runInvariant _ entry isMember isLive)]
            exact isInvocations
          · intro entry isMember
            rw [isFresh entry isMember]
            rfl
        rw [tasksEq]
        have absSeq : (Run.abstract config.ghost run).seq = config.ghost.normalCount := rfl
        simp [eraseStepEvents, Obs.toDex, absSeq]
        exact ⟨rfl, rfl, rfl⟩
      · cases isStep
  · cases isStep

end DexExpressiveness.DexF.WPF1
