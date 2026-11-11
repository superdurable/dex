import DexExpressiveness.Failure.Composite.WPF5BackwardStart

/-!
# WP-F5: Dex [TIMER-FIRE] from α of a run without failures

Without retry timers the due timers of DexF are the due wait timers of α, in
the same order, so DexF [TIMER-FIRE] of the same count fires a wait timer and
is a normal commit.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

theorem wpf5_insertSorted_map {α β : Type} (isAtMost : α → α → Bool)
    (isImageAtMost : β → β → Bool) (image : α → β)
    (isCompatible : ∀ first second, isImageAtMost (image first) (image second) = isAtMost first second)
    (element : α) (elements : List α) :
    insertSorted isImageAtMost (image element) (elements.map image) =
      (insertSorted isAtMost element elements).map image := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, insertSorted, isCompatible]
    split
    · rfl
    · simp [inductionHypothesis]

theorem wpf5_sortStable_map {α β : Type} (isAtMost : α → α → Bool)
    (isImageAtMost : β → β → Bool) (image : α → β)
    (isCompatible : ∀ first second, isImageAtMost (image first) (image second) = isAtMost first second)
    (elements : List α) :
    sortStable isImageAtMost (elements.map image) = (sortStable isAtMost elements).map image := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, sortStable]
    rw [inductionHypothesis, wpf5_insertSorted_map isAtMost isImageAtMost image isCompatible]

/-- Without retry timers, the due timers of DexF are the due wait timers. -/
theorem wpf5_dueTimers (run : Run σ) (ghost : Ghost σ) (hasNoRetryTimers : run.retryTimers = [])
    (clock : Nat) :
    run.dueTimers clock = ((run.abstract ghost).dueTimers clock).map DueTimer.wait := by
  simp only [Run.dueTimers, Dex.Run.dueTimers, hasNoRetryTimers, List.filter_nil, List.map_nil,
    List.append_nil, Run.abstract]
  rw [wpf5_sortStable_map Dex.Run.isTimerEntryAtMost isDueEntryAtMost
    (fun entry => (DueTimer.wait entry.1, entry.2)) (fun _ _ => rfl)]
  simp

theorem wpf5_fireDueTimer_retryTimers (run : Run σ) (hasNoRetryTimers : run.retryTimers = [])
    (timer : DueTimer σ) : (run.fireDueTimer timer).retryTimers = [] := by
  cases timer with
  | wait timer => exact hasNoRetryTimers
  | retry timer =>
    simp [Run.fireDueTimer, Run.fireRetryTimer, hasNoRetryTimers, AssocList.contains,
      AssocList.get?]

theorem wpf5_foldl_fireDueTimer_retryTimers (timers : List (DueTimer σ)) :
    ∀ run : Run σ, run.retryTimers = [] → (timers.foldl Run.fireDueTimer run).retryTimers = [] := by
  induction timers with
  | nil => intro run hasNoRetryTimers; exact hasNoRetryTimers
  | cons timer laterTimers inductionHypothesis =>
    intro run hasNoRetryTimers
    exact inductionHypothesis _ (wpf5_fireDueTimer_retryTimers run hasNoRetryTimers timer)

theorem wpf5_free_timerFire (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (count : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.timerFire count) =
      some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.timerFire count) dexEvents dexNext := by
  cases hasRun : config.run with
  | none => simp [Dex.step, abstractConfig, hasRun] at isDexStep
  | some run =>
    have hasNoRetryTimers := shape.noRetryTimers run hasRun
    obtain ⟨isPositive, isWithin, canCommit⟩ : 0 < count ∧
        count ≤ ((run.abstract config.ghost).dueTimers config.clock).length ∧
        config.canCommit = true := by
      simp only [Dex.step, abstractConfig, hasRun, Option.map_some] at isDexStep
      split at isDexStep
      · rename_i isEnabled
        simp only [Bool.and_eq_true, decide_eq_true_eq] at isEnabled
        exact ⟨isEnabled.1.1, isEnabled.1.2, by rw [← wpf5_canCommit config shape]; exact isEnabled.2⟩
      · cases isDexStep
    have isDue := wpf5_dueTimers run config.ghost hasNoRetryTimers config.clock
    have isTakeLength : (((run.abstract config.ghost).dueTimers config.clock).take count).length =
        count := by
      simp [List.length_take, Nat.min_eq_left isWithin]
    have firesWait : (((run.dueTimers config.clock).take count).any DueTimer.isWait) = true := by
      rw [isDue, ← List.map_take]
      cases isTake : ((run.abstract config.ghost).dueTimers config.clock).take count with
      | nil =>
        rw [isTake] at isTakeLength
        simp at isTakeLength
        omega
      | cons first rest => simp [DueTimer.isWait]
    have isServe := wpf5_canServeCommit config shape
    have isStep : step program config (.timerFire count true) =
        some (config.commitRun run true none [] (fun current =>
          ((run.dueTimers config.clock).take count).foldl Run.fireDueTimer current)) := by
      have isLength : count ≤ (run.dueTimers config.clock).length := by
        rw [isDue, List.length_map]
        exact isWithin
      simp [step, isServe, canCommit, hasRun, isPositive, isLength, firesWait]
    have isMap : dexActionsOf program config (.timerFire count true) = [.timerFire count] := by
      simp only [dexActionsOf, if_true, Config.firedWaitTimerCount, hasRun, isDue,
        ← List.map_take, List.filter_map]
      have isAllWait : (List.filter (DueTimer.isWait ∘ DueTimer.wait)
          (((run.abstract config.ghost).dueTimers config.clock).take count)) =
          ((run.abstract config.ghost).dueTimers config.clock).take count := by
        apply List.filter_eq_self.mpr
        intro _ _
        rfl
      rw [List.length_map, isAllWait, isTakeLength]
    exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
      isDexStep _ _ _ isStep _ isMap rfl
      (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _ isStep)
      (wpf5_shape_commitRun config shape run hasNoRetryTimers [] _
        (fun current isEmpty => wpf5_foldl_fireDueTimer_retryTimers _ current isEmpty))
      rfl rfl rfl

end DexExpressiveness.DexF
