import DexExpressiveness.Failure.Proofs.WPF1Steps

/-!
# WP-F1: facts for the Complete rules

A Complete rule ends the invocation it commits: after the commit no record
of its Step has a current invocation of its method (`NotCurrentAt`). A
virtual task whose invocation is no longer current is not current in α, so
Dex [WORKER-ABANDON] may drop it, and a list of such steps drops exactly
those tasks.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-! ## No current invocation of the method -/

/-- No record of the Step has a current invocation of the method. -/
def NotCurrentAt (step : Dex.StepExecId σ) (method : Dex.Method) (run : Dex.Run σ) : Prop :=
  ∀ record, record ∈ run.steps → record.id = step →
    record.invocation.map (fun invocation => invocation.method) ≠ some method

theorem NotCurrentAt.of_steps {step : Dex.StepExecId σ} {method : Dex.Method}
    {run other : Dex.Run σ} (notCurrent : NotCurrentAt step method run)
    (sameSteps : other.steps = run.steps) : NotCurrentAt step method other := by
  intro record isMember
  rw [sameSteps] at isMember
  exact notCurrent record isMember

theorem NotCurrentAt.updateStep {step : Dex.StepExecId σ} {method : Dex.Method}
    {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run) (updated : Dex.StepExecId σ)
    (change : Dex.StepRecord σ → Dex.StepRecord σ)
    (keepsId : ∀ record, (change record).id = record.id)
    (isAllowed : ∀ record, record ∈ run.steps → record.id = updated → record.id = step →
      (change record).invocation = record.invocation ∨
        (change record).invocation.map (fun invocation => invocation.method) ≠ some method) :
    NotCurrentAt step method (run.updateStep updated change) := by
  intro record isMember isStep
  obtain ⟨original, isOriginal, isRecord⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMember
  by_cases isUpdated : original.id = updated
  · rw [if_pos isUpdated] at isRecord
    subst isRecord
    rw [keepsId] at isStep
    rcases isAllowed original isOriginal isUpdated isStep with isSame | isOther
    · rw [isSame]
      exact notCurrent original isOriginal isStep
    · exact isOther
  · rw [if_neg isUpdated] at isRecord
    subst isRecord
    exact notCurrent record isOriginal isStep

theorem notCurrentAt_updateStep_none (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (method : Dex.Method) :
    NotCurrentAt step method
      (run.updateStep step (fun record => { record with invocation := none })) := by
  intro record isMember isStep
  obtain ⟨original, _, isRecord⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMember
  by_cases isUpdated : original.id = step
  · rw [if_pos isUpdated] at isRecord
    subst isRecord
    simp
  · rw [if_neg isUpdated] at isRecord
    subst isRecord
    exact absurd isStep isUpdated

theorem NonWaitingAt.updateStep {step : Dex.StepExecId σ} {run : Dex.Run σ}
    (nonWaiting : NonWaitingAt step run) (updated : Dex.StepExecId σ)
    (change : Dex.StepRecord σ → Dex.StepRecord σ)
    (keepsId : ∀ record, (change record).id = record.id)
    (keepsPhase : ∀ record, (change record).phase = record.phase) :
    NonWaitingAt step (run.updateStep updated change) := by
  intro record isMember isStep
  obtain ⟨original, isOriginal, isRecord⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMember
  by_cases isUpdated : original.id = updated
  · rw [if_pos isUpdated] at isRecord
    subst isRecord
    rw [keepsId] at isStep
    rw [keepsPhase]
    exact nonWaiting original isOriginal isStep
  · rw [if_neg isUpdated] at isRecord
    subst isRecord
    exact nonWaiting record isOriginal isStep

theorem NotCurrentAt.evalStep {step : Dex.StepExecId σ} {method : Dex.Method}
    {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run) (evaluated : Dex.StepExecId σ)
    (isAllowed : method = .waitFor ∨ NonWaitingAt step run) :
    NotCurrentAt step method (run.evalStep evaluated) := by
  rcases Dex.Run.evalStep_cases run evaluated with ⟨isSame, _⟩ |
    ⟨record, waitState, consumeFlags, isFound, isWaiting, _, isEval⟩
  · rw [isSame]
    exact notCurrent
  · rw [isEval]
    have sameSteps : (Dex.Run.consumeMessages waitState.wait.channels consumeFlags
        (run.disarmTimersOf evaluated)).1.steps = run.steps := by
      rw [(Dex.Run.consumeMessages_frame _ _ _).1]
      rfl
    apply NotCurrentAt.updateStep (notCurrent.of_steps sameSteps) evaluated
      (fun current =>
        { current with
          conditionResults := some
            { timersDone := waitState.timersDone
              channelMessages := (Dex.Run.consumeMessages waitState.wait.channels consumeFlags
                (run.disarmTimersOf evaluated)).2 }
          phase := .execute
          invocation := some { method := .execute, state := .pending } })
      (fun _ => rfl)
    intro other _ isEvaluated isStep
    right
    rcases isAllowed with isWaitFor | nonWaiting
    · subst isWaitFor
      simp
    · obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? run evaluated record isFound
      exact absurd isWaiting (nonWaiting record isMember (isId.trans (isEvaluated.symm.trans isStep)))

theorem NotCurrentAt.foldl_evalStep {step : Dex.StepExecId σ} {method : Dex.Method}
    (steps : List (Dex.StepExecId σ)) {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run)
    (isAllowed : method = .waitFor ∨ NonWaitingAt step run) :
    NotCurrentAt step method (steps.foldl Dex.Run.evalStep run) := by
  induction steps generalizing run with
  | nil => exact notCurrent
  | cons evaluated laterSteps inductionHypothesis =>
    apply inductionHypothesis (notCurrent.evalStep evaluated isAllowed)
    rcases isAllowed with isWaitFor | nonWaiting
    · exact Or.inl isWaitFor
    · exact Or.inr (keeps_evalStep run evaluated step nonWaiting).1

theorem NotCurrentAt.applyEffects {step : Dex.StepExecId σ} {method : Dex.Method}
    {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run) (effects : Dex.Effects σ)
    (isAllowed : method = .waitFor ∨ NonWaitingAt step run) :
    NotCurrentAt step method (run.applyEffects effects) := by
  have sameSteps : ((run.applyWrites effects.writes).applyPublications effects.publications).steps =
      run.steps := by
    rw [(Dex.Run.applyPublications_frame _ _).1, (Dex.Run.applyWrites_frame _ _).1]
  apply NotCurrentAt.foldl_evalStep _ (notCurrent.of_steps sameSteps)
  rcases isAllowed with isWaitFor | nonWaiting
  · exact Or.inl isWaitFor
  · right
    intro record isMember
    rw [sameSteps] at isMember
    exact nonWaiting record isMember

theorem NotCurrentAt.registerWait {step : Dex.StepExecId σ} {run : Dex.Run σ}
    (notCurrent : NotCurrentAt step .waitFor run) (registered : Dex.StepExecId σ)
    (wait : Dex.Wait σ) (now : Nat) :
    NotCurrentAt step .waitFor (run.registerWait registered wait now) := by
  unfold Dex.Run.registerWait
  split
  · refine NotCurrentAt.updateStep notCurrent registered _ ?_ ?_
    · intro _
      rfl
    · intro _ _ _ _
      right
      simp
  · dsimp only
    apply NotCurrentAt.evalStep _ registered (Or.inl rfl)
    refine NotCurrentAt.updateStep (notCurrent.of_steps rfl) registered _ ?_ ?_
    · intro _
      rfl
    · intro _ _ _ _
      left
      rfl

theorem NotCurrentAt.finishStep {step : Dex.StepExecId σ} {method : Dex.Method}
    {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run) (finished : Dex.StepExecId σ)
    (finalPhase : Dex.Phase) : NotCurrentAt step method (run.finishStep finished finalPhase) := by
  refine NotCurrentAt.updateStep (notCurrent.of_steps rfl) finished _ ?_ ?_
  · intro _
    rfl
  · intro _ _ _ _
    right
    simp

theorem NotCurrentAt.closeRun {step : Dex.StepExecId σ} {method : Dex.Method}
    {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run) (status : Dex.CloseStatus) :
    NotCurrentAt step method (run.closeRun status) := by
  unfold Dex.Run.closeRun
  split
  · exact notCurrent
  · dsimp only
    have folded : ∀ (steps : List (Dex.StepExecId σ)) (current : Dex.Run σ),
        NotCurrentAt step method current →
        NotCurrentAt step method
          (steps.foldl (fun current finished => current.finishStep finished .canceled) current) := by
      intro steps
      induction steps with
      | nil => intro current isNotCurrent; exact isNotCurrent
      | cons finished laterSteps inductionHypothesis =>
        intro current isNotCurrent
        exact inductionHypothesis _ (isNotCurrent.finishStep finished .canceled)
    exact (folded _ run notCurrent).of_steps rfl

theorem NotCurrentAt.newStep {step : Dex.StepExecId σ} {method : Dex.Method}
    {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run) (stepType : σ.StepType)
    (input : Option σ.Val) (options : Dex.StepOptions σ) (origin : Dex.Origin σ)
    (isCounted : step.number ≤ (run.counters.get? step.stepType).getD 0) :
    NotCurrentAt step method (run.newStep stepType input options origin) ∧
      step.number ≤ ((run.newStep stepType input options origin).counters.get?
        step.stepType).getD 0 := by
  constructor
  · intro record isMember isStep
    simp only [Dex.Run.newStep, List.mem_append, List.mem_singleton] at isMember
    rcases isMember with isOld | isNew
    · exact notCurrent record isOld isStep
    · subst isNew
      simp only at isStep
      subst isStep
      simp only at isCounted
      omega
  · simp only [Dex.Run.newStep]
    by_cases isType : step.stepType = stepType
    · rw [isType, AssocList.get?_set_self]
      rw [isType] at isCounted
      simp only [Option.getD_some]
      omega
    · rw [AssocList.get?_set_other _ _ _ _ isType]
      exact isCounted

theorem NotCurrentAt.applyDecision {step : Dex.StepExecId σ} {method : Dex.Method}
    {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run) (decided : Dex.StepExecId σ)
    (decision : Dex.Decision σ)
    (isCounted : step.number ≤ (run.counters.get? step.stepType).getD 0) :
    NotCurrentAt step method (run.applyDecision decided decision) := by
  have finished := notCurrent.finishStep decided .completed
  unfold Dex.Run.applyDecision
  dsimp only
  split
  · apply NotCurrentAt.closeRun
    cases ‹Option σ.Val› <;> exact finished
  · exact finished.closeRun _
  · exact finished
  · have folded : ∀ (moves : List (Dex.Move σ)) (current : Dex.Run σ),
        NotCurrentAt step method current →
        step.number ≤ (current.counters.get? step.stepType).getD 0 →
        NotCurrentAt step method (moves.foldl (fun current move =>
          current.newStep move.stepType move.input move.options (.step decided)) current) := by
      intro moves
      induction moves with
      | nil => intro current isNotCurrent _; exact isNotCurrent
      | cons move laterMoves inductionHypothesis =>
        intro current isNotCurrent isCurrentCounted
        obtain ⟨nextNotCurrent, nextCounted⟩ :=
          isNotCurrent.newStep move.stepType move.input move.options (.step decided) isCurrentCounted
        exact inductionHypothesis _ nextNotCurrent nextCounted
    exact folded _ _ finished isCounted

theorem not_mem_currentKeys_of_notCurrentAt {step : Dex.StepExecId σ} {method : Dex.Method}
    {run : Dex.Run σ} (notCurrent : NotCurrentAt step method run) :
    (step, method) ∉ currentKeys run := by
  intro isCurrent
  obtain ⟨record, invocation, isMember, hasInvocation, isKey⟩ :=
    (mem_currentKeys _ _).mp isCurrent
  simp only [Prod.mk.injEq] at isKey
  apply notCurrent record isMember isKey.1.symm
  rw [hasInvocation, isKey.2]
  rfl

/-! ## α of a Step that is no longer current -/

theorem abstractInvocation_method (run : Run σ) (ghost : Ghost σ) (record : Dex.StepRecord σ) :
    (run.abstractInvocation ghost record).map (fun invocation => invocation.method) =
      record.invocation.map (fun invocation => invocation.method) := by
  unfold Run.abstractInvocation
  cases record.invocation with
  | none => rfl
  | some invocation =>
    dsimp only
    split <;> rfl

theorem findStep?_abstract (ghost : Ghost σ) (run : Run σ) (step : Dex.StepExecId σ) :
    (Run.abstract ghost run).findStep? step =
      (run.base.findStep? step).map (fun record =>
        { record with
          invocation := run.abstractInvocation ghost record
          waitState := record.waitState.map (fun waitState =>
            { waitState with registeredSeq := ghost.normalIndexOf waitState.registeredSeq }) }) := by
  simp only [Dex.Run.findStep?, Run.abstract, List.find?_map]
  rfl

/-- A task whose invocation is not current in the run is not current in α. -/
theorem isTaskCurrent_eq_false (config : Dex.Config σ) (ghost : Ghost σ) (run : Run σ)
    (isRun : config.run = some (Run.abstract ghost run)) (task : Dex.Task σ)
    (notCurrent : (task.step, task.method) ∉ currentKeys run.base) :
    config.isTaskCurrent task = false := by
  unfold Dex.Config.isTaskCurrent
  rw [isRun]
  dsimp only
  rw [findStep?_abstract]
  cases isFound : run.base.findStep? task.step with
  | none => rfl
  | some record =>
    apply decide_eq_false
    intro isCurrent
    obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? _ _ _ isFound
    have isMethod := abstractInvocation_method run ghost record
    dsimp only at isCurrent
    rw [isCurrent] at isMethod
    cases hasInvocation : record.invocation with
    | none => rw [hasInvocation] at isMethod; cases isMethod
    | some invocation =>
      rw [hasInvocation] at isMethod
      simp only [Option.map_some, Option.some.injEq] at isMethod
      apply notCurrent
      apply (mem_currentKeys _ _).mpr
      exact ⟨record, invocation, isMember, hasInvocation, by rw [isId, isMethod]⟩

/-! ## Looking up and dropping virtual tasks -/

theorem find?_of_pairwise (tasks : List (Dex.Task σ))
    (isSorted : tasks.Pairwise (fun first second => first.id < second.id)) (task : Dex.Task σ)
    (isMember : task ∈ tasks) :
    tasks.find? (fun candidate => decide (candidate.id = task.id)) = some task := by
  induction tasks with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    rw [List.pairwise_cons] at isSorted
    rw [List.find?_cons]
    by_cases isHead : head.id = task.id
    · rw [decide_eq_true isHead]
      rcases List.mem_cons.mp isMember with isSame | inRest
      · rw [isSame]
      · have := isSorted.1 task inRest
        omega
    · rw [decide_eq_false isHead]
      rcases List.mem_cons.mp isMember with isSame | inRest
      · exact absurd (by rw [isSame]) isHead
      · exact inductionHypothesis isSorted.2 inRest

theorem exec_workerAbandons (flow : Dex.FlowDef σ) (ids : List Nat) (config : Dex.Config σ)
    (idsNodup : ids.Nodup)
    (present : ∀ id, id ∈ ids → ∃ task, task ∈ config.tasks ∧ task.id = id)
    (notCurrent : ∀ task, task ∈ config.tasks → task.id ∈ ids → config.isTaskCurrent task = false) :
    (Dex.system flow).exec config (ids.map Dex.Action.workerAbandon) =
      some ([], { config with tasks := config.tasks.filter (fun task => !decide (task.id ∈ ids)) }) := by
  induction ids generalizing config with
  | nil =>
    have keepAll : config.tasks.filter (fun task => !decide (task.id ∈ ([] : List Nat))) =
        config.tasks := List.filter_eq_self.mpr (fun _ _ => by simp)
    rw [keepAll]
    rfl
  | cons id laterIds inductionHypothesis =>
    rw [List.nodup_cons] at idsNodup
    obtain ⟨presentTask, isPresent, presentId⟩ := present id (List.mem_cons_self ..)
    cases isFound : config.findTask? id with
    | none =>
      have := List.find?_eq_none.mp isFound presentTask isPresent
      simp [presentId] at this
    | some found =>
      have foundMember : found ∈ config.tasks := List.mem_of_find?_eq_some isFound
      have foundId : found.id = id := by simpa using List.find?_some isFound
      have isStep : Dex.step flow config (.workerAbandon id) = some ([], config.removeTask id) := by
        simp only [Dex.step, isFound,
          notCurrent found foundMember (by rw [foundId]; exact List.mem_cons_self ..)]
        rfl
      rw [List.map_cons, exec_cons flow config _ _ [] _ isStep]
      rw [inductionHypothesis (config.removeTask id) idsNodup.2]
      · simp only [Option.map_some, List.nil_append, Dex.Config.removeTask, List.filter_filter]
        congr 3
        apply List.filter_congr
        intro task _
        by_cases isId : task.id = id
        · simp [isId]
        · simp [isId]
      · intro other isOther
        obtain ⟨otherTask, isOtherPresent, otherId⟩ := present other (List.mem_cons_of_mem _ isOther)
        refine ⟨otherTask, ?_, otherId⟩
        simp only [Dex.Config.removeTask, List.mem_filter]
        refine ⟨isOtherPresent, ?_⟩
        have : other ≠ id := fun isSame => idsNodup.1 (isSame ▸ isOther)
        simp [otherId, this]
      · intro task isMember isLater
        simp only [Dex.Config.removeTask, List.mem_filter] at isMember
        have := notCurrent task isMember.1 (List.mem_cons_of_mem _ isLater)
        exact this

end DexExpressiveness.DexF.WPF1
