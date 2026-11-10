import DexExpressiveness.Dex.RunInvariant

/-!
# Armed timers have distinct IDs

`Tm : TimerId ⇀ t` is a finite map (spec §3.2), kept as an association list.
`Due_f` reads every binding of the list (`Run.dueTimers`), so a second binding
of one timer ID would fire twice. This file proves that no meta-function of
spec §4.2 creates one: `register` arms a timer with `M[k ↦ v]`, which replaces
an existing binding, and every other meta-function only removes timers.

Everything here is proved, and nothing depends on the translation.
-/

namespace DexExpressiveness

namespace AssocList

variable {κ ν : Type} [DecidableEq κ]

/-- `M` without `k` is a sublist of `M`. -/
theorem erase_sublist (map : AssocList κ ν) (key : κ) : (erase map key).Sublist map := by
  induction map with
  | nil => exact List.Sublist.refl _
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · simp only [erase, isKey, ↓reduceIte]
      exact inductionHypothesis.cons _
    · simp only [erase, isKey, ↓reduceIte]
      exact inductionHypothesis.cons_cons _

/-- `M[k ↦ v]` keeps the keys distinct. -/
theorem nodup_keys_set (map : AssocList κ ν) (key : κ) (value : ν)
    (keysNodup : (map.map (fun entry => entry.1)).Nodup) :
    ((set map key value).map (fun entry => entry.1)).Nodup := by
  induction map with
  | nil => simp [set]
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    simp only [List.map_cons, List.nodup_cons] at keysNodup
    by_cases isKey : headKey = key
    · simp only [set, isKey, ↓reduceIte, List.map_cons, List.nodup_cons]
      rw [← isKey]
      exact keysNodup
    · simp only [set, isKey, ↓reduceIte, List.map_cons, List.nodup_cons]
      refine ⟨?_, inductionHypothesis keysNodup.2⟩
      intro isNewKey
      obtain ⟨entry, isMember, isEntryKey⟩ := List.mem_map.mp isNewKey
      rcases mem_set rest key value entry isMember with isOld | isNew
      · exact keysNodup.1 (List.mem_map.mpr ⟨entry, isOld, isEntryKey⟩)
      · rw [isNew] at isEntryKey
        exact isKey isEntryKey.symm

end AssocList

namespace Dex

variable {σ : Signature}

namespace Run

/-- The armed timers have distinct IDs: `Tm` is a finite map (spec §3.2). -/
def HasDistinctTimers (run : Run σ) : Prop :=
  (run.timers.map (fun entry => entry.1)).Nodup

/-- A run whose timers are a sublist of timers with distinct IDs has timers
with distinct IDs. -/
theorem HasDistinctTimers.of_sublist {run after : Run σ} (timersDistinct : run.HasDistinctTimers)
    (isSublist : after.timers.Sublist run.timers) : after.HasDistinctTimers :=
  List.Nodup.sublist (isSublist.map _) timersDistinct

/-- `eval(e)` only disarms timers. -/
theorem timers_evalStep_sublist (run : Run σ) (step : StepExecId σ) :
    (run.evalStep step).timers.Sublist run.timers := by
  rcases evalStep_shape run step with isSame | ⟨chans, consumeFlags, change, _, isEval⟩
  · rw [isSame]
    exact List.Sublist.refl _
  · rw [isEval]
    have sameTimers := (consumeMessages_frame chans consumeFlags (run.disarmTimersOf step)).2.2.1
    show (consumeMessages chans consumeFlags (run.disarmTimersOf step)).1.timers.Sublist run.timers
    rw [sameTimers]
    exact List.filter_sublist

/-- Evaluating Steps only disarms timers. -/
theorem timers_foldl_evalStep_sublist (steps : List (StepExecId σ)) (run : Run σ) :
    (steps.foldl Run.evalStep run).timers.Sublist run.timers := by
  induction steps generalizing run with
  | nil => exact List.Sublist.refl _
  | cons step laterSteps inductionHypothesis =>
    exact (inductionHypothesis _).trans (timers_evalStep_sublist run step)

/-- `effects` only disarms timers. -/
theorem timers_applyEffects_sublist (run : Run σ) (effects : Effects σ) :
    (run.applyEffects effects).timers.Sublist run.timers := by
  unfold applyEffects wake
  refine (timers_foldl_evalStep_sublist _ _).trans ?_
  rw [(applyPublications_frame _ _).2.2.1, (applyWrites_frame _ _).2.2.1]
  exact List.Sublist.refl _

/-- Arming the timers of a wait keeps the timer IDs distinct. -/
theorem nodup_keys_armTimers (step : StepExecId σ) (fireAt : List Nat) (index : Nat)
    (timers : AssocList (TimerId σ) Nat)
    (keysNodup : (timers.map (fun entry => entry.1)).Nodup) :
    ((armTimers step fireAt index timers).map (fun entry => entry.1)).Nodup := by
  induction fireAt generalizing index timers with
  | nil => exact keysNodup
  | cons fireTime laterFireTimes inductionHypothesis =>
    exact inductionHypothesis _ _ (AssocList.nodup_keys_set timers _ fireTime keysNodup)

/-- `register(e, W)` keeps the timer IDs distinct. -/
theorem HasDistinctTimers.registerWait {run : Run σ} (timersDistinct : run.HasDistinctTimers)
    (step : StepExecId σ) (wait : Wait σ) (now : Nat) :
    (run.registerWait step wait now).HasDistinctTimers := by
  unfold Run.registerWait
  split
  · exact timersDistinct
  · exact HasDistinctTimers.of_sublist
      (run := { run with
        timers := armTimers step
          (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0 run.timers })
      (nodup_keys_armTimers step _ 0 run.timers timersDistinct)
      (timers_evalStep_sublist _ step)

/-- `finish(e, φ')` only disarms timers. -/
theorem timers_finishStep_sublist (run : Run σ) (step : StepExecId σ) (finalPhase : Phase) :
    (run.finishStep step finalPhase).timers.Sublist run.timers :=
  List.filter_sublist

/-- Canceling a list of Steps only disarms timers. -/
theorem timers_foldl_finishStep_sublist (steps : List (StepExecId σ)) (run : Run σ) :
    (steps.foldl (fun current step => current.finishStep step .canceled) run).timers.Sublist
      run.timers := by
  induction steps generalizing run with
  | nil => exact List.Sublist.refl _
  | cons step laterSteps inductionHypothesis =>
    exact (inductionHypothesis _).trans (timers_finishStep_sublist run step .canceled)

/-- `close(κ)` only disarms timers. -/
theorem timers_closeRun_sublist (run : Run σ) (status : CloseStatus) :
    (run.closeRun status).timers.Sublist run.timers := by
  unfold Run.closeRun
  split
  · exact List.Sublist.refl _
  · exact timers_foldl_finishStep_sublist _ run

/-- Moves arm no timer. -/
theorem timers_foldl_newStep (moves : List (Move σ)) (origin : Origin σ) (run : Run σ) :
    (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run).timers = run.timers := by
  induction moves generalizing run with
  | nil => rfl
  | cons move laterMoves inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    rfl

/-- `decide(e, D)` only disarms timers. -/
theorem timers_applyDecision_sublist (run : Run σ) (step : StepExecId σ)
    (decision : Decision σ) : (run.applyDecision step decision).timers.Sublist run.timers := by
  have finished := timers_finishStep_sublist run step .completed
  simp only [Run.applyDecision]
  split
  · refine (timers_closeRun_sublist _ _).trans ?_
    unfold Run.collectOutput
    split <;> exact finished
  · exact (timers_closeRun_sublist _ _).trans finished
  · exact finished
  · rw [timers_foldl_newStep]
    exact finished

/-- The effect of [TIMER-FIRE] for one timer only disarms timers. -/
theorem timers_fireTimer_sublist (run : Run σ) (timer : TimerId σ) :
    (run.fireTimer timer).timers.Sublist run.timers := by
  have erased : ({ run with timers := run.timers.erase timer } : Run σ).timers.Sublist
      run.timers := AssocList.erase_sublist run.timers timer
  unfold Run.fireTimer
  split
  · simp only
    split
    · exact erased
    · split
      · split
        · exact erased
        · exact (timers_evalStep_sublist _ _).trans erased
      · exact erased
  · exact List.Sublist.refl _

/-- Firing a prefix of the due timers only disarms timers. -/
theorem timers_foldl_fireTimer_sublist (timers : List (TimerId σ)) (run : Run σ) :
    (timers.foldl Run.fireTimer run).timers.Sublist run.timers := by
  induction timers generalizing run with
  | nil => exact List.Sublist.refl _
  | cons timer laterTimers inductionHypothesis =>
    exact (inductionHypothesis _).trans (timers_fireTimer_sublist run timer)

end Run

end Dex

end DexExpressiveness
