import DexExpressiveness.Liveness.LToDexRun
import DexExpressiveness.Simulation.Frame

/-!
# Liveness, work package L6a: helpers for F1, F2, and F3 of the Dex run

`Liveness/LToDexFair.lean` proves `DexRunOfLRun.timeDiverges`, `.timersFire`,
and `.stepsStart` from the lemmas of this file. Three groups:

1. Inside a block. Every action of a block but at most one is a Worker step
   (`fairActions_workerSteps`), and a Worker step keeps the run, the clock, the
   last commit timestamp, and the next task ID (`Dex.step_workerStep_frame`).
   So inside a block the run and the clock are those at the block's start or
   those at its end (`DexRunOfLRun.run_clock_inside_block`, and the sharper
   `DexRunOfLRun.exists_mainOffset`).
2. Along the whole Dex run. The input of a Step never changes, except that
   `finish` drops it (`Dex.Run.KeepsStepInputs`,
   `Dex.keepsStepInputs_of_isRun`). A Step that carries a thread at two block
   boundaries therefore carries the same thread at both.
3. At a block boundary. A pending invocation belongs to the Step of a thread
   whose next action has not begun (`IsSimulationState.pendingThread`), and an
   armed timer to the Step of a thread blocked on it
   (`IsSimulationState.timedThread`).

F2 and F3 are then proved by contradiction: a timer that stayed armed and
due, or an invocation that stayed pending, would stay so at every later block
boundary, where it is an L timer of one thread, or one pending thread, and
the L assumption says that this fails.
-/

namespace DexExpressiveness

namespace Dex

variable {σ : Signature}

/-! ## The input of a Step never changes -/

namespace Run

/-- Every Step of `before` is a Step of `after`, with the same input or with
no input: only `finish` changes the input of a Step, and it drops it. -/
def KeepsStepInputs (before after : Run σ) : Prop :=
  ∀ step record, before.findStep? step = some record →
    ∃ changed, after.findStep? step = some changed ∧
      (changed.input = record.input ∨ changed.input = none)

theorem KeepsStepInputs.refl (run : Run σ) : run.KeepsStepInputs run :=
  fun _ record isFound => ⟨record, isFound, Or.inl rfl⟩

theorem KeepsStepInputs.trans {first second third : Run σ}
    (firstKeeps : first.KeepsStepInputs second) (secondKeeps : second.KeepsStepInputs third) :
    first.KeepsStepInputs third := by
  intro step record isFound
  obtain ⟨middle, isMiddle, middleInput⟩ := firstKeeps step record isFound
  obtain ⟨last, isLast, lastInput⟩ := secondKeeps step middle isMiddle
  refine ⟨last, isLast, ?_⟩
  rcases lastInput with isSame | isNone
  · rw [isSame]
    exact middleInput
  · exact Or.inr isNone

/-- A change that leaves the Step table as it is keeps the inputs. -/
theorem KeepsStepInputs.of_steps_eq {before after : Run σ}
    (sameSteps : after.steps = before.steps) : before.KeepsStepInputs after := by
  intro step record isFound
  refine ⟨record, ?_, Or.inl rfl⟩
  unfold findStep? at isFound ⊢
  rw [sameSteps]
  exact isFound

/-- An update of one Step that keeps its ID and keeps or drops its input. -/
theorem keepsStepInputs_updateStep (run : Run σ) (step : StepExecId σ)
    (change : StepRecord σ → StepRecord σ)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id)
    (keepsInput : ∀ candidate,
      (change candidate).input = candidate.input ∨ (change candidate).input = none) :
    run.KeepsStepInputs (run.updateStep step change) := by
  intro other record isFound
  by_cases isStep : other = step
  · subst isStep
    exact ⟨change record,
      findStep?_updateStep_self run other change record isFound (keepsId record),
      keepsInput record⟩
  · refine ⟨record, ?_, Or.inl rfl⟩
    rw [findStep?_updateStep_other run step other change keepsId isStep]
    exact isFound

/-- A new Step is appended, so every older Step is found as before. -/
theorem keepsStepInputs_newStep (run : Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : StepOptions σ) (origin : Origin σ) :
    run.KeepsStepInputs (run.newStep stepType input options origin) := by
  intro step record isFound
  refine ⟨record, ?_, Or.inl rfl⟩
  unfold findStep? at isFound ⊢
  simp [newStep, List.find?_append, isFound]

/-- Folding a change that keeps the inputs keeps them. -/
theorem keepsStepInputs_foldl {α : Type} (apply : Run σ → α → Run σ)
    (keeps : ∀ (run : Run σ) (element : α), run.KeepsStepInputs (apply run element)) (elements : List α)
    (run : Run σ) : run.KeepsStepInputs (elements.foldl apply run) := by
  induction elements generalizing run with
  | nil => exact .refl run
  | cons element laterElements inductionHypothesis =>
    exact (keeps run element).trans (inductionHypothesis (apply run element))

theorem keepsStepInputs_evalStep (run : Run σ) (step : StepExecId σ) :
    run.KeepsStepInputs (run.evalStep step) := by
  unfold evalStep
  split
  · exact .refl run
  · split
    · split
      · exact .refl run
      · refine KeepsStepInputs.trans ?_
          (keepsStepInputs_updateStep _ _ _ (fun _ => rfl) (fun _ => Or.inl rfl))
        exact .of_steps_eq (by rw [consumeMessages_steps]; rfl)
    · exact .refl run

theorem keepsStepInputs_wake (run : Run σ) (changed : List σ.Chan) :
    run.KeepsStepInputs (run.wake changed) :=
  keepsStepInputs_foldl evalStep keepsStepInputs_evalStep _ run

theorem keepsStepInputs_applyEffects (run : Run σ) (effects : Effects σ) :
    run.KeepsStepInputs (run.applyEffects effects) := by
  unfold applyEffects
  refine KeepsStepInputs.trans (.of_steps_eq ?_) (keepsStepInputs_wake _ _)
  rw [(applyPublications_frame _ _).1, (applyWrites_frame _ _).1]

theorem keepsStepInputs_registerWait (run : Run σ) (step : StepExecId σ) (wait : Wait σ)
    (now : Nat) : run.KeepsStepInputs (run.registerWait step wait now) := by
  unfold registerWait
  split
  · exact keepsStepInputs_updateStep _ _ _ (fun _ => rfl) (fun _ => Or.inl rfl)
  · refine KeepsStepInputs.trans ?_ (keepsStepInputs_evalStep _ _)
    exact KeepsStepInputs.trans (second := { run with
        timers := armTimers step
          (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0 run.timers })
      (.of_steps_eq rfl)
      (keepsStepInputs_updateStep _ _ _ (fun _ => rfl) (fun _ => Or.inl rfl))

/-- `finish` drops the input of its Step and keeps every other. -/
theorem keepsStepInputs_finishStep (run : Run σ) (step : StepExecId σ) (finalPhase : Phase) :
    run.KeepsStepInputs (run.finishStep step finalPhase) := by
  unfold finishStep
  exact KeepsStepInputs.trans (second := run.disarmTimersOf step) (.of_steps_eq rfl)
    (keepsStepInputs_updateStep _ _ _ (fun _ => rfl) (fun _ => Or.inr rfl))

theorem keepsStepInputs_collectOutput (run : Run σ) (step : StepExecId σ)
    (output : Option σ.Val) : run.KeepsStepInputs (run.collectOutput step output) := by
  cases output with
  | none => exact .refl run
  | some value => exact .of_steps_eq rfl

theorem keepsStepInputs_closeRun (run : Run σ) (status : CloseStatus) :
    run.KeepsStepInputs (run.closeRun status) := by
  unfold closeRun
  split
  · exact .refl run
  · refine KeepsStepInputs.trans
      (keepsStepInputs_foldl (fun current step => current.finishStep step .canceled)
        (fun current step => keepsStepInputs_finishStep current step .canceled) _ run)
      (.of_steps_eq rfl)

theorem keepsStepInputs_applyDecision (run : Run σ) (step : StepExecId σ)
    (decision : Decision σ) : run.KeepsStepInputs (run.applyDecision step decision) := by
  unfold applyDecision
  refine KeepsStepInputs.trans (keepsStepInputs_finishStep run step .completed) ?_
  split
  · exact (keepsStepInputs_collectOutput _ _ _).trans (keepsStepInputs_closeRun _ _)
  · exact keepsStepInputs_closeRun _ _
  · exact .refl _
  · exact keepsStepInputs_foldl
      (fun current (move : Move σ) =>
        current.newStep move.stepType move.input move.options (.step step))
      (fun current move => keepsStepInputs_newStep current _ _ _ _) _ _

theorem keepsStepInputs_fireTimer (run : Run σ) (timer : TimerId σ) :
    run.KeepsStepInputs (run.fireTimer timer) := by
  unfold fireTimer
  split
  · have disarmedKeeps :
        run.KeepsStepInputs { run with timers := run.timers.erase timer } := .of_steps_eq rfl
    refine disarmedKeeps.trans ?_
    dsimp only
    split
    · exact .refl _
    · split
      · split
        · exact .refl _
        · refine KeepsStepInputs.trans ?_ (keepsStepInputs_evalStep _ _)
          exact keepsStepInputs_updateStep _ _ _ (fun _ => rfl) (fun _ => Or.inl rfl)
      · exact .refl _
  · exact .refl run

end Run

/-- Taking the next sequence number keeps the Step table. -/
theorem keepsStepInputs_bumpSeq (run : Run σ) :
    run.KeepsStepInputs { run with seq := run.seq + 1 } :=
  .of_steps_eq rfl

/-- One transition keeps the run, and in it the input of every Step, except
that `finish` drops the input of the Steps it ends. -/
theorem step_keepsStepInputs (flow : FlowDef σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step flow config action = some (events, next)) (run : Run σ)
    (hasRun : config.run = some run) :
    ∃ after, next.run = some after ∧ run.KeepsStepInputs after := by
  cases action with
  | tick duration =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
      exact ⟨run, hasRun, .refl run⟩
    · cases isStep
  | request request =>
    cases request with
    | startFlow stepType input => simp [step, hasRun] at isStep
    | publish chan value =>
      simp only [step, hasRun] at isStep
      split at isStep
      · cases isStep
        exact ⟨run, hasRun, .refl run⟩
      · split at isStep
        · cases isStep
          refine ⟨_, rfl, (keepsStepInputs_bumpSeq run).trans ?_⟩
          refine Run.KeepsStepInputs.trans ?_ (Run.keepsStepInputs_wake _ _)
          exact .of_steps_eq (Run.applyPublications_frame _ _).1
        · cases isStep
  | stepStart stepId =>
    simp only [step, hasRun] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep
        · cases isStep
          refine ⟨_, rfl, (keepsStepInputs_bumpSeq run).trans ?_⟩
          exact Run.keepsStepInputs_updateStep _ _ _ (fun _ => rfl) (fun _ => Or.inl rfl)
        · cases isStep
      · cases isStep
  | workerRun taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨run, hasRun, .refl run⟩
      · cases isStep
  | workerEffect taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨run, hasRun, .refl run⟩
      · cases isStep
  | workerReturn taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨run, hasRun, .refl run⟩
      · cases isStep
  | workerAbandon taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
      · cases isStep
        exact ⟨run, hasRun, .refl run⟩
  | stepComplete taskId =>
    simp only [step] at isStep
    split at isStep
    · rename_i matchedRun task hasMatchedRun isFound
      rw [hasRun] at hasMatchedRun
      cases hasMatchedRun
      split at isStep
      · split at isStep
        · cases isStep
          refine ⟨_, rfl, (keepsStepInputs_bumpSeq _).trans ?_⟩
          refine Run.KeepsStepInputs.trans ?_ (Run.keepsStepInputs_registerWait _ _ _ _)
          refine Run.KeepsStepInputs.trans ?_ (Run.keepsStepInputs_applyEffects _ _)
          exact Run.keepsStepInputs_updateStep _ _ _ (fun _ => rfl) (fun _ => Or.inl rfl)
        · cases isStep
          refine ⟨_, rfl, (keepsStepInputs_bumpSeq _).trans ?_⟩
          refine Run.KeepsStepInputs.trans ?_ (Run.keepsStepInputs_applyDecision _ _ _)
          refine Run.KeepsStepInputs.trans ?_ (Run.keepsStepInputs_applyEffects _ _)
          exact Run.keepsStepInputs_updateStep _ _ _ (fun _ => rfl) (fun _ => Or.inl rfl)
        · cases isStep
      · cases isStep
    · cases isStep
  | stepCompleteStale taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep
        · cases isStep
        · cases isStep
          exact ⟨run, hasRun, .refl run⟩
      · cases isStep
  | timerFire count =>
    simp only [step, hasRun] at isStep
    split at isStep
    · cases isStep
      refine ⟨_, rfl, (keepsStepInputs_bumpSeq run).trans ?_⟩
      exact Run.keepsStepInputs_foldl Run.fireTimer Run.keepsStepInputs_fireTimer _ _
    · cases isStep

/-- Along a run, the run of the Flow stays, and the input of every Step
stays or is dropped. -/
theorem keepsStepInputs_of_isRun {flow : FlowDef σ}
    {dexRun : InfiniteRun (Config σ) (Action σ)} (isRun : (system flow).IsRun dexRun)
    (first later : Nat) (isLater : first ≤ later) (run : Run σ)
    (hasRun : (dexRun.states first).run = some run) :
    ∃ after, (dexRun.states later).run = some after ∧ run.KeepsStepInputs after := by
  obtain ⟨distance, isDistance⟩ := Nat.exists_eq_add_of_le isLater
  subst isDistance
  induction distance with
  | zero => exact ⟨run, hasRun, .refl run⟩
  | succ distance inductionHypothesis =>
    obtain ⟨middle, hasMiddle, middleKeeps⟩ := inductionHypothesis (Nat.le_add_right _ _)
    obtain ⟨events, isStep⟩ := isRun.2 (first + distance)
    obtain ⟨after, hasAfter, afterKeeps⟩ :=
      step_keepsStepInputs flow _ _ events _ isStep middle hasMiddle
    exact ⟨after, hasAfter, middleKeeps.trans afterKeeps⟩

end Dex

end DexExpressiveness
