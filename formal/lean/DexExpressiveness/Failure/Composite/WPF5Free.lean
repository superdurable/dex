import DexExpressiveness.Failure.Composite.WPF5BackwardComplete

/-!
# WP-F5: every Dex run is a DexF run without failures

The Lemma (no failures) of spec §11.5, `Tr_free ⊆ Tr_fail`, by induction on the
Dex run: each Dex step is matched by one DexF step (`wpf5_free_step`), which
keeps `WPF5FreeShape`, and `invariant_step` keeps the invariants. The DexF
run gives the start request ID 0, takes no failure transition, and so has no
retry.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- Every Dex step from α of a configuration of the shape is matched. -/
theorem wpf5_free_step (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config)
    (dexAction : Dex.Action σ) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) dexAction =
      some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config dexAction dexEvents dexNext := by
  cases dexAction with
  | tick duration =>
    exact wpf5_free_tick program ignoresAttemptContext config invariant shape duration _ _ isDexStep
  | request request =>
    cases request with
    | startFlow stepType input =>
      exact wpf5_free_startFlow program ignoresAttemptContext config invariant shape stepType input
        _ _ isDexStep
    | publish chan value =>
      exact wpf5_free_publish program ignoresAttemptContext config invariant shape chan value
        _ _ isDexStep
  | stepStart stepId =>
    exact wpf5_free_stepStart program ignoresAttemptContext config invariant shape stepId _ _
      isDexStep
  | workerRun taskId =>
    exact wpf5_free_workerRun program ignoresAttemptContext config invariant shape taskId _ _
      isDexStep
  | workerEffect taskId =>
    exact wpf5_free_workerEffect program ignoresAttemptContext config invariant shape taskId _ _
      isDexStep
  | workerReturn taskId =>
    exact wpf5_free_workerReturn program ignoresAttemptContext config invariant shape taskId _ _
      isDexStep
  | workerAbandon taskId =>
    exact wpf5_free_workerAbandon program ignoresAttemptContext config invariant shape taskId _ _
      isDexStep
  | stepComplete taskId =>
    exact wpf5_free_stepComplete program ignoresAttemptContext config invariant shape taskId _ _
      isDexStep
  | stepCompleteStale taskId =>
    exact wpf5_free_stepCompleteStale program ignoresAttemptContext config invariant shape taskId
      _ _ isDexStep
  | timerFire count =>
    exact wpf5_free_timerFire program ignoresAttemptContext config invariant shape count _ _
      isDexStep

theorem wpf5_shape_initial : WPF5FreeShape (Config.initial σ) :=
  { isUp := rfl
    noZombies := rfl
    noInterrupts := rfl
    lastCommitIsNormal := rfl
    noRetryTimers := by intro run isRun; cases isRun
    nextTaskIds := rfl
    tasksMatch := by intro task isTask; cases isTask
    liveHaveTasks := by intro key history isHistory; cases isHistory
    taskKeysNodup := List.nodup_nil }

/-- A Dex run from α of a configuration of the shape is matched by a DexF run
without failures. -/
theorem wpf5_free_exec (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) :
    ∀ (dexLabels : List (Dex.Action σ)) (config : Config σ) (dexEvents : List (Dex.Obs σ))
      (dexFinal : Dex.Config σ),
      Invariant program config → WPF5FreeShape config →
      (Dex.system program.failureFreeFlow).exec (abstractConfig config) dexLabels =
        some (dexEvents, dexFinal) →
      ∃ labels events final,
        (system program).exec config labels = some (events, final) ∧
        events.map Obs.toDex = dexEvents ∧
        labels.filterMap Action.requestOf =
          (dexLabels.filterMap (Dex.system program.failureFreeFlow).requestOf).map
            wpf5LiftRequest ∧
        HasNoExhaustion labels ∧ HasNoNonRetryableFailure labels := by
  intro dexLabels
  induction dexLabels with
  | nil =>
    intro config dexEvents dexFinal _ _ isExec
    simp only [TransitionSystem.exec, Option.some.injEq, Prod.mk.injEq] at isExec
    obtain ⟨isEvents, _⟩ := isExec
    subst isEvents
    refine ⟨[], [], config, rfl, rfl, rfl, ?_, ?_⟩ <;> intro label isMember <;> cases isMember
  | cons dexAction laterDexActions inductionHypothesis =>
    intro config dexEvents dexFinal invariant shape isExec
    obtain ⟨stepEvents, dexNext, laterEvents, isDexStep, isLater, isEvents⟩ :=
      (Dex.system program.failureFreeFlow).exec_cons _ dexAction laterDexActions dexEvents dexFinal
        isExec
    obtain ⟨action, events, next, isStep, isMapped, isNext, nextShape, isRequest, isNotExhausting,
      isNotNonRetryable⟩ :=
      wpf5_free_step program ignoresAttemptContext config invariant shape dexAction stepEvents
        dexNext isDexStep
    have nextInvariant := invariant_step program ignoresAttemptContext config invariant action
      events next isStep
    rw [← isNext] at isLater
    obtain ⟨laterLabels, laterFailureEvents, final, isLaterExec, isLaterMapped, isLaterRequests,
      laterHasNoExhaustion, laterHasNoNonRetryable⟩ :=
      inductionHypothesis next laterEvents dexFinal nextInvariant nextShape isLater
    refine ⟨action :: laterLabels, events ++ laterFailureEvents, final, ?_, ?_, ?_, ?_, ?_⟩
    · exact (system program).exec_append config [action] laterLabels events laterFailureEvents
        next final (by simp [TransitionSystem.exec, system, isStep]) isLaterExec
    · rw [List.map_append, isMapped, isLaterMapped, isEvents]
    · rw [List.filterMap_cons, List.filterMap_cons, isRequest, isLaterRequests]
      cases (Dex.system program.failureFreeFlow).requestOf dexAction <;> rfl
    · intro label isMember
      rcases List.mem_cons.mp isMember with isHead | isTail
      · rw [isHead]
        exact isNotExhausting
      · exact laterHasNoExhaustion label isTail
    · intro label isMember
      rcases List.mem_cons.mp isMember with isHead | isTail
      · rw [isHead]
        exact isNotNonRetryable
      · exact laterHasNoNonRetryable label isTail

theorem wpf5_toDex_liftRequest (request : Dex.Request σ) :
    (wpf5LiftRequest request).toDex = request := by
  cases request <;> rfl

/-- `w↓` of the lifted requests of an admissible Dex prefix is the prefix. -/
theorem wpf5_eraseRetries_lift (environment : Dex.Environment σ)
    (requests : List (Dex.Request σ)) (isAdmissible : environment.IsAdmissiblePrefix requests) :
    eraseRetries (requests.map wpf5LiftRequest) = requests ∧
      IsAdmissibleOutcomePrefix environment (requests.map wpf5LiftRequest) := by
  rcases isAdmissible with isEmpty | ⟨input, laterRequests, isRequests, isInput, isLater⟩
  · subst isEmpty
    exact ⟨rfl, .inl rfl⟩
  · subst isRequests
    have isKept : (laterRequests.map wpf5LiftRequest).filter
        (fun request => !(Request.startFlow 0 environment.entryStep input).isRetryOf request) =
        laterRequests.map wpf5LiftRequest := by
      apply List.filter_eq_self.mpr
      intro request isMember
      simp only [List.mem_map] at isMember
      obtain ⟨original, isOriginal, isLifted⟩ := isMember
      obtain ⟨chan, value, isPublish, _⟩ := isLater original isOriginal
      subst isPublish isLifted
      rfl
    refine ⟨?_, .inr ⟨0, input, laterRequests.map wpf5LiftRequest, rfl, isInput, ?_⟩⟩
    · simp only [List.map_cons, eraseRetries, wpf5LiftRequest, Request.toDex]
      rw [isKept, List.map_map]
      congr 1
      conv => rhs; rw [← List.map_id laterRequests]
      apply List.map_congr_left
      intro request _
      exact wpf5_toDex_liftRequest request
    · intro request isMember
      simp only [List.mem_map] at isMember
      obtain ⟨original, isOriginal, isLifted⟩ := isMember
      obtain ⟨chan, value, isPublish, isExternal⟩ := isLater original isOriginal
      subst isPublish isLifted
      exact .inr ⟨chan, value, rfl, isExternal⟩

end DexExpressiveness.DexF
