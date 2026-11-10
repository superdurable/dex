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

/-! ## Worker steps -/

/-- A Worker step: it acts on one task of `Θ` and commits nothing (spec §4.4
[WORKER-EFFECT], [WORKER-ABANDON], [STEP-COMPLETE-STALE]). -/
def Action.isWorkerStep : Action σ → Bool
  | .workerRun _ => true
  | .workerEffect _ => true
  | .workerReturn _ => true
  | .workerAbandon _ => true
  | .stepCompleteStale _ => true
  | _ => false

/-- A Worker step keeps the run, the clock, the timestamp of the last commit,
and the next task ID: it changes the phase of one task or removes one task. -/
theorem step_workerStep_frame (flow : FlowDef σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step flow config action = some (events, next))
    (isWorker : action.isWorkerStep = true) :
    next.run = config.run ∧ next.clock = config.clock ∧
    next.lastCommitAt = config.lastCommitAt ∧ next.nextTaskId = config.nextTaskId := by
  cases action with
  | workerRun taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨rfl, rfl, rfl, rfl⟩
      · cases isStep
  | workerEffect taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨rfl, rfl, rfl, rfl⟩
      · cases isStep
  | workerReturn taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨rfl, rfl, rfl, rfl⟩
      · cases isStep
  | workerAbandon taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
      · cases isStep
        exact ⟨rfl, rfl, rfl, rfl⟩
  | stepCompleteStale taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep
        · cases isStep
        · cases isStep
          exact ⟨rfl, rfl, rfl, rfl⟩
      · cases isStep
  | _ => simp [Action.isWorkerStep] at isWorker

end Dex

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Inside a block: all actions but one are Worker steps -/

/-- The Worker step that runs a handler is a Worker step. -/
theorem runTaskActions_isWorkerStep (task : Dex.Task signature) :
    ∀ action, action ∈ runTaskActions task → action.isWorkerStep = true := by
  unfold runTaskActions
  split <;> simp [Dex.Action.isWorkerStep]

/-- The Worker steps that return a handler are Worker steps. -/
theorem returnTaskActions_isWorkerStep (task : Dex.Task signature) :
    ∀ action, action ∈ returnTaskActions task → action.isWorkerStep = true := by
  unfold returnTaskActions
  split <;> simp [Dex.Action.isWorkerStep]

/-- The cleanup of a block is [WORKER-ABANDON] steps. -/
theorem cleanupActions_isWorkerStep (program : Program) (config : Dex.Config signature) :
    ∀ action, action ∈ cleanupActions program config → action.isWorkerStep = true := by
  intro action isMember
  obtain ⟨task, _, isAction⟩ := List.mem_map.mp isMember
  rw [← isAction]
  rfl

/-- The simulating actions of a label are Worker steps followed by one more
action, or there are none. -/
theorem simulatingActions_shape (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) :
    ∃ workerActions, (∀ action, action ∈ workerActions → action.isWorkerStep = true) ∧
      (simulatingActions program lConfig dexConfig label = [] ∨
        ∃ main, simulatingActions program lConfig dexConfig label = workerActions ++ [main]) := by
  have noAction : ∀ action : Dex.Action signature,
      action ∈ ([] : List (Dex.Action signature)) → action.isWorkerStep = true :=
    fun _ isMember => nomatch isMember
  cases label with
  | tick duration => exact ⟨[], noAction, Or.inr ⟨_, rfl⟩⟩
  | request request => exact ⟨[], noAction, Or.inr ⟨_, rfl⟩⟩
  | fire count => exact ⟨[], noAction, Or.inr ⟨_, rfl⟩⟩
  | thread thread =>
    simp only [simulatingActions]
    split
    · split
      · exact ⟨[], noAction, Or.inr ⟨_, rfl⟩⟩
      · exact ⟨[], noAction, Or.inr ⟨_, rfl⟩⟩
      · exact ⟨[], noAction, Or.inr ⟨_, rfl⟩⟩
      · exact ⟨_, runTaskActions_isWorkerStep _, Or.inr ⟨_, rfl⟩⟩
      · exact ⟨_, returnTaskActions_isWorkerStep _, Or.inr ⟨_, rfl⟩⟩
      · exact ⟨[], noAction, Or.inl rfl⟩
    · exact ⟨[], noAction, Or.inl rfl⟩
  | lateDeliver index =>
    simp only [simulatingActions]
    split
    · exact ⟨[], noAction, Or.inl rfl⟩
    · split
      · exact ⟨_, runTaskActions_isWorkerStep _, Or.inr ⟨_, rfl⟩⟩
      · exact ⟨[], noAction, Or.inl rfl⟩
  | lateDrop index =>
    simp only [simulatingActions]
    split
    · exact ⟨[], noAction, Or.inl rfl⟩
    · split
      · exact ⟨[], noAction, Or.inr ⟨_, rfl⟩⟩
      · exact ⟨[], noAction, Or.inl rfl⟩

/-- In Worker steps, one more action, and Worker steps again, every action
but the one in the middle is a Worker step. -/
theorem isWorkerStep_of_getElem?_append
    {workerActions laterActions : List (Dex.Action signature)} {main : Dex.Action signature}
    (areWorkers : ∀ action, action ∈ workerActions → action.isWorkerStep = true)
    (laterAreWorkers : ∀ action, action ∈ laterActions → action.isWorkerStep = true)
    {offset : Nat} {action : Dex.Action signature}
    (isAction : (workerActions ++ main :: laterActions)[offset]? = some action)
    (isOther : offset ≠ workerActions.length) : action.isWorkerStep = true := by
  by_cases isBefore : offset < workerActions.length
  · rw [List.getElem?_append_left isBefore] at isAction
    exact areWorkers action (List.mem_of_getElem? isAction)
  · rw [List.getElem?_append_right (by omega)] at isAction
    obtain ⟨distance, isDistance⟩ : ∃ distance, offset - workerActions.length = distance + 1 :=
      ⟨offset - workerActions.length - 1, by omega⟩
    rw [isDistance, List.getElem?_cons_succ] at isAction
    exact laterAreWorkers action (List.mem_of_getElem? isAction)

/-- Every action of a block but at most one is a Worker step: the block is
the Worker steps that run and return a handler, then the action that
simulates the label, then the [WORKER-ABANDON] steps of the cleanup. -/
theorem fairActions_workerSteps (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) :
    ∃ mainOffset, ∀ (offset : Nat) (action : Dex.Action signature),
      (fairActions program lConfig dexConfig label)[offset]? = some action →
      offset ≠ mainOffset → action.isWorkerStep = true := by
  obtain ⟨workerActions, areWorkers, isEmpty | ⟨main, isShape⟩⟩ :=
    simulatingActions_shape program lConfig dexConfig label
  · refine ⟨0, fun offset action isAction _ => ?_⟩
    have isMember := List.mem_of_getElem? isAction
    simp only [fairActions, isEmpty] at isMember
    split at isMember
    · exact cleanupActions_isWorkerStep program _ action (by simpa using isMember)
    · cases isMember
  · refine ⟨workerActions.length, fun offset action isAction isOther => ?_⟩
    simp only [fairActions, isShape] at isAction
    split at isAction
    · rw [List.append_assoc] at isAction
      exact isWorkerStep_of_getElem?_append areWorkers (cleanupActions_isWorkerStep program _)
        isAction isOther
    · exact isWorkerStep_of_getElem?_append (laterActions := []) areWorkers
        (fun _ isMember => nomatch isMember) isAction isOther

/-! ## Inside a block: the run and the clock are those at its start or its end -/

variable {program : Program} {lRun : InfiniteRun L.Config L.Label}
  {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
  {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}

/-- Each block has a main offset: every action of the block at another offset
is a Worker step. Up to the main offset the run and the clock are those at
the block's start, and after it they are those at the block's end. -/
theorem DexRunOfLRun.exists_mainOffset
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) (index : Nat) :
    ∃ mainOffset,
      (∀ offset, offset < (dexBlocks.blocks index).length → offset ≠ mainOffset →
        (dexRun.labels (boundary index + offset)).isWorkerStep = true) ∧
      (∀ offset, offset ≤ (dexBlocks.blocks index).length → offset ≤ mainOffset →
        (dexRun.states (boundary index + offset)).run = (dexBlocks.states index).run ∧
        (dexRun.states (boundary index + offset)).clock = (dexBlocks.states index).clock) ∧
      (∀ offset, offset ≤ (dexBlocks.blocks index).length → mainOffset < offset →
        (dexRun.states (boundary index + offset)).run = (dexBlocks.states (index + 1)).run ∧
        (dexRun.states (boundary index + offset)).clock =
          (dexBlocks.states (index + 1)).clock) := by
  obtain ⟨mainOffset, areWorkers⟩ := fairActions_workerSteps program (lRun.states index)
    (dexBlocks.states index) (lRun.labels index)
  rw [← built.follows.isFair index] at areWorkers
  have labelsAreWorkers : ∀ offset, offset < (dexBlocks.blocks index).length →
      offset ≠ mainOffset → (dexRun.labels (boundary index + offset)).isWorkerStep = true := by
    intro offset isInside isOther
    have isLabel : (dexBlocks.blocks index)[offset]? = some (dexBlocks.blocks index)[offset] :=
      List.getElem?_eq_getElem isInside
    rw [built.isFlattened.labelInBlock index offset _ isLabel]
    exact areWorkers offset _ isLabel isOther
  have stepKeeps : ∀ offset, offset < (dexBlocks.blocks index).length → offset ≠ mainOffset →
      (dexRun.states (boundary index + offset + 1)).run =
        (dexRun.states (boundary index + offset)).run ∧
      (dexRun.states (boundary index + offset + 1)).clock =
        (dexRun.states (boundary index + offset)).clock := by
    intro offset isInside isOther
    obtain ⟨events, isStep⟩ := built.isDexRun.2 (boundary index + offset)
    obtain ⟨sameRun, sameClock, _⟩ := Dex.step_workerStep_frame _ _ _ events _ isStep
      (labelsAreWorkers offset isInside isOther)
    exact ⟨sameRun, sameClock⟩
  refine ⟨mainOffset, labelsAreWorkers, ?_, ?_⟩
  · intro offset
    induction offset with
    | zero =>
      intro _ _
      rw [Nat.add_zero, built.isFlattened.stateAtBoundary index]
      exact ⟨rfl, rfl⟩
    | succ offset inductionHypothesis =>
      intro isInside isBefore
      obtain ⟨sameRun, sameClock⟩ := stepKeeps offset (by omega) (by omega)
      obtain ⟨startRun, startClock⟩ := inductionHypothesis (by omega) (by omega)
      exact ⟨sameRun.trans startRun, sameClock.trans startClock⟩
  · have atEnd : dexRun.states (boundary index + (dexBlocks.blocks index).length) =
        dexBlocks.states (index + 1) := by
      rw [← built.isFlattened.nextBoundary index, built.isFlattened.stateAtBoundary (index + 1)]
    have fromEnd : ∀ distance offset, offset + distance = (dexBlocks.blocks index).length →
        mainOffset < offset →
        (dexRun.states (boundary index + offset)).run = (dexBlocks.states (index + 1)).run ∧
        (dexRun.states (boundary index + offset)).clock =
          (dexBlocks.states (index + 1)).clock := by
      intro distance
      induction distance with
      | zero =>
        intro offset isOffset _
        rw [Nat.add_zero] at isOffset
        rw [isOffset, atEnd]
        exact ⟨rfl, rfl⟩
      | succ distance inductionHypothesis =>
        intro offset isOffset isAfter
        obtain ⟨sameRun, sameClock⟩ := stepKeeps offset (by omega) (by omega)
        obtain ⟨endRun, endClock⟩ := inductionHypothesis (offset + 1) (by omega) (by omega)
        exact ⟨sameRun.symm.trans endRun, sameClock.symm.trans endClock⟩
    intro offset isInside isAfter
    exact fromEnd ((dexBlocks.blocks index).length - offset) offset (by omega) isAfter

/-- Inside a block, the run and the clock are those at the block's start or
those at the block's end. -/
theorem DexRunOfLRun.run_clock_inside_block
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) (index offset : Nat)
    (isInside : offset ≤ (dexBlocks.blocks index).length) :
    ((dexRun.states (boundary index + offset)).run = (dexBlocks.states index).run ∧
      (dexRun.states (boundary index + offset)).clock = (dexBlocks.states index).clock) ∨
    ((dexRun.states (boundary index + offset)).run = (dexBlocks.states (index + 1)).run ∧
      (dexRun.states (boundary index + offset)).clock =
        (dexBlocks.states (index + 1)).clock) := by
  obtain ⟨mainOffset, _, beforeMain, afterMain⟩ := built.exists_mainOffset index
  by_cases isBefore : offset ≤ mainOffset
  · exact Or.inl (beforeMain offset isInside isBefore)
  · exact Or.inr (afterMain offset isInside (by omega))

end DexExpressiveness
