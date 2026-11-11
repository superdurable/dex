import DexExpressiveness.Failure.Proofs.WPF3Basic
import DexExpressiveness.Dex.RunInvariant

/-!
# WP-F3: how the meta-functions change the Step table

The facts about one commit that the effect history needs (spec §11.4,
"First occurrences come in order 1, 2, 3, …"): Step execution IDs are only
appended and stay fresh; a Step's phase only moves forward in the order
`WF < WT < EX < CO = CA`; an invocation that stays current keeps the input,
condition results, and origin of its Step; a new invocation is pended only on
a new Step or as an Execute that moves a Step from `WF` or `WT` to `EX`; and
an invocation that stops being current leaves its Step past its method's
phase. So an invocation `(e, m)` is pended at most once in its life.

`WPF3RunEvolves before after` collects these facts for two Step tables;
each meta-function of spec §4.2 satisfies it, and it composes.
`WPF3RunShape` is the part of the run invariant they need: unique IDs, fresh
counters, and an invocation's method matching its Step's phase. Everything
here is proved.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Phases -/

/-- The order of phases: `WF < WT < EX < CO = CA`. -/
def wpf3PhaseRank : Dex.Phase → Nat
  | .waitFor => 0
  | .waiting => 1
  | .execute => 2
  | .completed => 3
  | .canceled => 3

/-- The phase in which a method is pending or running. -/
def wpf3PhaseOf : Dex.Method → Dex.Phase
  | .waitFor => .waitFor
  | .execute => .execute

/-- The Step is past the phase of the method, so it never pends the method again. -/
def wpf3IsBeyond (phase : Dex.Phase) (method : Dex.Method) : Prop :=
  wpf3PhaseRank (wpf3PhaseOf method) < wpf3PhaseRank phase

theorem wpf3_isBeyond_mono {phase later : Dex.Phase} {method : Dex.Method}
    (isBeyond : wpf3IsBeyond phase method) (grows : wpf3PhaseRank phase ≤ wpf3PhaseRank later) :
    wpf3IsBeyond later method :=
  Nat.lt_of_lt_of_le isBeyond grows

/-! ## The relation -/

/-- How one Step record may change in a commit. -/
structure WPF3RecordEvolves (record next : Dex.StepRecord σ) : Prop where
  sameId : next.id = record.id
  rankGrows : wpf3PhaseRank record.phase ≤ wpf3PhaseRank next.phase
  invocationOrigin : ∀ invocation, next.invocation = some invocation →
    (∃ previous, record.invocation = some previous ∧ previous.method = invocation.method ∧
      next.input = record.input ∧ next.conditionResults = record.conditionResults ∧
      next.origin = record.origin) ∨
    (invocation.method = .execute ∧ wpf3PhaseRank record.phase ≤ 1)
  invocationExit : ∀ invocation, record.invocation = some invocation →
    (∃ current, next.invocation = some current ∧ current.method = invocation.method) ∨
    wpf3IsBeyond next.phase invocation.method

theorem WPF3RecordEvolves.refl (record : Dex.StepRecord σ) : WPF3RecordEvolves record record where
  sameId := rfl
  rankGrows := Nat.le_refl _
  invocationOrigin := fun invocation isInvocation =>
    Or.inl ⟨invocation, isInvocation, rfl, rfl, rfl, rfl⟩
  invocationExit := fun invocation isInvocation => Or.inl ⟨invocation, isInvocation, rfl⟩

theorem WPF3RecordEvolves.trans {first second third : Dex.StepRecord σ}
    (firstEvolves : WPF3RecordEvolves first second)
    (secondEvolves : WPF3RecordEvolves second third) : WPF3RecordEvolves first third where
  sameId := secondEvolves.sameId.trans firstEvolves.sameId
  rankGrows := Nat.le_trans firstEvolves.rankGrows secondEvolves.rankGrows
  invocationOrigin := by
    intro invocation isInvocation
    rcases secondEvolves.invocationOrigin invocation isInvocation with
      ⟨middle, isMiddle, sameMethod, sameInput, sameResults, sameOrigin⟩ | ⟨isExecute, isEarly⟩
    · rcases firstEvolves.invocationOrigin middle isMiddle with
        ⟨previous, isPrevious, samePreviousMethod, sameFirstInput, sameFirstResults,
          sameFirstOrigin⟩ | ⟨isMiddleExecute, isFirstEarly⟩
      · exact Or.inl ⟨previous, isPrevious, samePreviousMethod.trans sameMethod,
          sameInput.trans sameFirstInput, sameResults.trans sameFirstResults,
          sameOrigin.trans sameFirstOrigin⟩
      · exact Or.inr ⟨sameMethod.symm.trans isMiddleExecute, isFirstEarly⟩
    · exact Or.inr ⟨isExecute, Nat.le_trans firstEvolves.rankGrows isEarly⟩
  invocationExit := by
    intro invocation isInvocation
    rcases firstEvolves.invocationExit invocation isInvocation with
      ⟨middle, isMiddle, sameMethod⟩ | isBeyond
    · rcases secondEvolves.invocationExit middle isMiddle with
        ⟨current, isCurrent, sameCurrentMethod⟩ | isMiddleBeyond
      · exact Or.inl ⟨current, isCurrent, sameCurrentMethod.trans sameMethod⟩
      · rw [← sameMethod]; exact Or.inr isMiddleBeyond
    · exact Or.inr (wpf3_isBeyond_mono isBeyond secondEvolves.rankGrows)

/-- How the Step table may change in a commit. -/
structure WPF3RunEvolves (before after : Dex.Run σ) : Prop where
  keepsIds : ∀ record, record ∈ before.steps → ∃ next, next ∈ after.steps ∧ next.id = record.id
  evolves : ∀ next, next ∈ after.steps →
    (∃ record, record ∈ before.steps ∧ WPF3RecordEvolves record next) ∨
    (∀ record, record ∈ before.steps → record.id ≠ next.id)

theorem WPF3RunEvolves.of_steps_eq {before after : Dex.Run σ}
    (sameSteps : after.steps = before.steps) : WPF3RunEvolves before after where
  keepsIds := fun record isMember => ⟨record, sameSteps ▸ isMember, rfl⟩
  evolves := fun next isMember =>
    Or.inl ⟨next, sameSteps ▸ isMember, WPF3RecordEvolves.refl next⟩

theorem WPF3RunEvolves.refl (run : Dex.Run σ) : WPF3RunEvolves run run :=
  WPF3RunEvolves.of_steps_eq rfl

theorem WPF3RunEvolves.trans {first second third : Dex.Run σ}
    (firstEvolves : WPF3RunEvolves first second) (secondEvolves : WPF3RunEvolves second third) :
    WPF3RunEvolves first third where
  keepsIds := by
    intro record isMember
    obtain ⟨middle, isMiddleMember, isMiddleId⟩ := firstEvolves.keepsIds record isMember
    obtain ⟨next, isNextMember, isNextId⟩ := secondEvolves.keepsIds middle isMiddleMember
    exact ⟨next, isNextMember, isNextId.trans isMiddleId⟩
  evolves := by
    intro next isMember
    rcases secondEvolves.evolves next isMember with ⟨middle, isMiddleMember, middleEvolves⟩ |
      isNewInMiddle
    · rcases firstEvolves.evolves middle isMiddleMember with
        ⟨record, isRecordMember, recordEvolves⟩ | isNewInFirst
      · exact Or.inl ⟨record, isRecordMember, recordEvolves.trans middleEvolves⟩
      · refine Or.inr (fun record isRecordMember isSameId => ?_)
        exact isNewInFirst record isRecordMember (isSameId.trans middleEvolves.sameId)
    · refine Or.inr (fun record isRecordMember isSameId => ?_)
      obtain ⟨middle, isMiddleMember, isMiddleId⟩ := firstEvolves.keepsIds record isRecordMember
      exact isNewInMiddle middle isMiddleMember (isMiddleId.trans isSameId)

/-- The part of the run invariant that the relation needs. -/
structure WPF3RunShape (run : Dex.Run σ) : Prop where
  idsNodup : (run.steps.map (fun record => record.id)).Nodup
  numbersCounted : ∀ record, record ∈ run.steps →
    record.id.number ≤ (run.counters.get? record.id.stepType).getD 0
  methodPhase : ∀ record, record ∈ run.steps → ∀ invocation, record.invocation = some invocation →
    record.phase = wpf3PhaseOf invocation.method

/-- Both facts for one function application. -/
def WPF3Evolution (before after : Dex.Run σ) : Prop :=
  WPF3RunEvolves before after ∧ WPF3RunShape after

theorem wpf3_evolution_trans {first second third : Dex.Run σ}
    (firstStep : WPF3Evolution first second) (secondStep : WPF3Evolution second third) :
    WPF3Evolution first third :=
  ⟨firstStep.1.trans secondStep.1, secondStep.2⟩

theorem wpf3_evolution_of_frame {before after : Dex.Run σ} (shape : WPF3RunShape before)
    (sameSteps : after.steps = before.steps) (sameCounters : after.counters = before.counters) :
    WPF3Evolution before after := by
  refine ⟨WPF3RunEvolves.of_steps_eq sameSteps, ?_, ?_, ?_⟩
  · rw [sameSteps]; exact shape.idsNodup
  · rw [sameSteps, sameCounters]; exact shape.numbersCounted
  · rw [sameSteps]; exact shape.methodPhase

/-! ## Updating one Step -/

theorem wpf3_evolution_updateStep (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (change : Dex.StepRecord σ → Dex.StepRecord σ) (shape : WPF3RunShape run)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id)
    (changedEvolves : ∀ record, record ∈ run.steps → record.id = step →
      WPF3RecordEvolves record (change record))
    (changedMatches : ∀ record, record ∈ run.steps → record.id = step →
      ∀ invocation, (change record).invocation = some invocation →
        (change record).phase = wpf3PhaseOf invocation.method) :
    WPF3Evolution run (run.updateStep step change) := by
  refine ⟨⟨?_, ?_⟩, ?_, ?_, ?_⟩
  · intro record isMember
    refine ⟨if record.id = step then change record else record,
      (Dex.Run.mem_updateStep run step change _).mpr ⟨record, isMember, rfl⟩, ?_⟩
    split <;> simp [keepsId]
  · intro next isMember
    obtain ⟨original, isOriginalMember, isNext⟩ :=
      (Dex.Run.mem_updateStep run step change next).mp isMember
    refine Or.inl ⟨original, isOriginalMember, ?_⟩
    split at isNext
    · rename_i isStep
      rw [isNext]
      exact changedEvolves original isOriginalMember isStep
    · rw [isNext]
      exact WPF3RecordEvolves.refl original
  · rw [Dex.Run.ids_updateStep run step change keepsId]
    exact shape.idsNodup
  · intro next isMember
    obtain ⟨original, isOriginalMember, isNext⟩ :=
      (Dex.Run.mem_updateStep run step change next).mp isMember
    have isSameId : next.id = original.id := by
      rw [isNext]; split <;> simp [keepsId]
    rw [isSameId]
    exact shape.numbersCounted original isOriginalMember
  · intro next isMember invocation isInvocation
    obtain ⟨original, isOriginalMember, isNext⟩ :=
      (Dex.Run.mem_updateStep run step change next).mp isMember
    split at isNext
    · rename_i isStep
      subst isNext
      exact changedMatches original isOriginalMember isStep invocation isInvocation
    · rw [isNext] at isInvocation ⊢
      exact shape.methodPhase original isOriginalMember invocation isInvocation

/-- An update that keeps the phase, the inputs, and the method of the invocation. -/
theorem wpf3_recordEvolves_of_sameMethod (record next : Dex.StepRecord σ)
    (sameId : next.id = record.id) (samePhase : next.phase = record.phase)
    (sameInput : next.input = record.input)
    (sameResults : next.conditionResults = record.conditionResults)
    (sameOrigin : next.origin = record.origin)
    (sameMethod : next.invocation.map (fun invocation => invocation.method) =
      record.invocation.map (fun invocation => invocation.method)) :
    WPF3RecordEvolves record next where
  sameId := sameId
  rankGrows := by rw [samePhase]; exact Nat.le_refl _
  invocationOrigin := by
    intro invocation isInvocation
    rw [isInvocation] at sameMethod
    cases isPrevious : record.invocation with
    | none => rw [isPrevious] at sameMethod; cases sameMethod
    | some previous =>
      rw [isPrevious] at sameMethod
      simp only [Option.map_some, Option.some.injEq] at sameMethod
      exact Or.inl ⟨previous, rfl, sameMethod.symm, sameInput, sameResults, sameOrigin⟩
  invocationExit := by
    intro invocation isInvocation
    rw [isInvocation] at sameMethod
    cases isCurrent : next.invocation with
    | none => rw [isCurrent] at sameMethod; cases sameMethod
    | some current =>
      rw [isCurrent] at sameMethod
      simp only [Option.map_some, Option.some.injEq] at sameMethod
      exact Or.inl ⟨current, rfl, sameMethod⟩

/-- Changing only the invocation's state, with the same method, is an evolution. -/
theorem wpf3_evolution_setInvocation (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (shape : WPF3RunShape run) (method : Dex.Method) (state : Dex.InvocationState)
    (isCurrentMethod : ∀ record, record ∈ run.steps → record.id = step →
      record.invocation.map (fun invocation => invocation.method) = some method) :
    WPF3Evolution run (run.updateStep step (fun record =>
      { record with invocation := some { method := method, state := state } })) := by
  refine wpf3_evolution_updateStep run step _ shape (by intro; rfl) ?_ ?_
  · intro record isMember isStep
    exact wpf3_recordEvolves_of_sameMethod _ _ rfl rfl rfl rfl rfl
      (by rw [isCurrentMethod record isMember isStep]; rfl)
  · intro record isMember isStep invocation isInvocation
    simp only [Option.some.injEq] at isInvocation
    subst isInvocation
    have isRecordMethod := isCurrentMethod record isMember isStep
    cases isRecordInvocation : record.invocation with
    | none => rw [isRecordInvocation] at isRecordMethod; cases isRecordMethod
    | some recordInvocation =>
      rw [isRecordInvocation] at isRecordMethod
      simp only [Option.map_some, Option.some.injEq] at isRecordMethod
      rw [← isRecordMethod]
      exact shape.methodPhase record isMember recordInvocation isRecordInvocation

/-! ## Creating a Step -/

theorem wpf3_evolution_newStep (run : Dex.Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : Dex.StepOptions σ) (origin : Dex.Origin σ) (shape : WPF3RunShape run) :
    WPF3Evolution run (run.newStep stepType input options origin) := by
  have isFresh : ∀ record, record ∈ run.steps →
      record.id ≠ { stepType := stepType, number := (run.counters.get? stepType).getD 0 + 1 } := by
    intro record isMember isSame
    have isCounted := shape.numbersCounted record isMember
    rw [isSame] at isCounted
    simp only at isCounted
    omega
  refine ⟨⟨?_, ?_⟩, ?_, ?_, ?_⟩
  · intro record isMember
    exact ⟨record, List.mem_append_left _ isMember, rfl⟩
  · intro next isMember
    rcases List.mem_append.mp isMember with isOld | isNew
    · exact Or.inl ⟨next, isOld, WPF3RecordEvolves.refl next⟩
    · rw [List.mem_singleton] at isNew
      subst isNew
      exact Or.inr (fun record isRecordMember => isFresh record isRecordMember)
  · simp only [Dex.Run.newStep, List.map_append, List.map_cons, List.map_nil]
    rw [List.nodup_append]
    refine ⟨shape.idsNodup, by simp, ?_⟩
    intro first isFirst second isSecond isSame
    rw [List.mem_singleton] at isSecond
    obtain ⟨record, isMember, isRecordId⟩ := List.mem_map.mp isFirst
    rw [← isRecordId, isSecond] at isSame
    exact isFresh record isMember isSame
  · intro next isMember
    simp only [Dex.Run.newStep] at isMember ⊢
    rcases List.mem_append.mp isMember with isOld | isNew
    · have isCounted := shape.numbersCounted next isOld
      by_cases isType : next.id.stepType = stepType
      · rw [isType] at isCounted ⊢
        rw [AssocList.get?_set_self]
        simp only [Option.getD_some]
        omega
      · rw [AssocList.get?_set_other _ _ _ _ isType]
        exact isCounted
    · rw [List.mem_singleton] at isNew
      subst isNew
      simp [AssocList.get?_set_self]
  · intro next isMember invocation isInvocation
    simp only [Dex.Run.newStep] at isMember
    rcases List.mem_append.mp isMember with isOld | isNew
    · exact shape.methodPhase next isOld invocation isInvocation
    · rw [List.mem_singleton] at isNew
      subst isNew
      simp only [Option.some.injEq] at isInvocation
      subst isInvocation
      cases options.skipWaitFor <;> rfl

theorem wpf3_evolution_foldl_newStep (moves : List (Dex.Move σ)) (origin : Dex.Origin σ)
    (run : Dex.Run σ) (shape : WPF3RunShape run) :
    WPF3Evolution run (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run) := by
  induction moves generalizing run with
  | nil => exact ⟨WPF3RunEvolves.refl run, shape⟩
  | cons move laterMoves inductionHypothesis =>
    have firstStep := wpf3_evolution_newStep run move.stepType move.input move.options origin shape
    exact wpf3_evolution_trans firstStep (inductionHypothesis _ firstStep.2)

/-! ## `eval`, `wake`, and `effects` -/

theorem wpf3_evolution_evalStep (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (shape : WPF3RunShape run) : WPF3Evolution run (run.evalStep step) := by
  rcases Dex.Run.evalStep_cases run step with ⟨isSame, _⟩ |
    ⟨record, waitState, consumeFlags, isFound, isWaiting, _, isEval⟩
  · rw [isSame]; exact ⟨WPF3RunEvolves.refl run, shape⟩
  · rw [isEval]
    obtain ⟨sameSteps, sameCounters, _⟩ :=
      Dex.Run.consumeMessages_frame waitState.wait.channels consumeFlags (run.disarmTimersOf step)
    have consumed : WPF3Evolution run
        (Dex.Run.consumeMessages waitState.wait.channels consumeFlags (run.disarmTimersOf step)).1 :=
      wpf3_evolution_of_frame shape (by rw [sameSteps]; rfl) (by rw [sameCounters]; rfl)
    refine wpf3_evolution_trans consumed (wpf3_evolution_updateStep _ _ _ consumed.2
      (fun _ => rfl) ?_ ?_)
    · intro other isMember isStep
      rw [sameSteps] at isMember
      have isOther : other = record := by
        have isOtherFound := Dex.Run.findStep?_of_mem_nodup run other isMember shape.idsNodup
        rw [isStep, isFound] at isOtherFound
        exact (Option.some.inj isOtherFound).symm
      subst isOther
      have hasNoInvocation : other.invocation = none := by
        cases isInvocation : other.invocation with
        | none => rfl
        | some invocation =>
          have isPhase := shape.methodPhase other isMember invocation isInvocation
          rw [isWaiting] at isPhase
          obtain ⟨method, _⟩ := invocation
          cases method <;> simp [wpf3PhaseOf] at isPhase
      refine ⟨rfl, ?_, ?_, ?_⟩
      · rw [isWaiting]; simp [wpf3PhaseRank]
      · intro invocation isInvocation
        simp only [Option.some.injEq] at isInvocation
        subst isInvocation
        exact Or.inr ⟨rfl, by rw [isWaiting]; simp [wpf3PhaseRank]⟩
      · intro invocation isInvocation
        rw [hasNoInvocation] at isInvocation
        cases isInvocation
    · intro other _ _ invocation isInvocation
      simp only [Option.some.injEq] at isInvocation
      subst isInvocation
      rfl

theorem wpf3_evolution_foldl_evalStep (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ)
    (shape : WPF3RunShape run) : WPF3Evolution run (steps.foldl Dex.Run.evalStep run) := by
  induction steps generalizing run with
  | nil => exact ⟨WPF3RunEvolves.refl run, shape⟩
  | cons step laterSteps inductionHypothesis =>
    have firstStep := wpf3_evolution_evalStep run step shape
    exact wpf3_evolution_trans firstStep (inductionHypothesis _ firstStep.2)

theorem wpf3_evolution_wake (run : Dex.Run σ) (changed : List σ.Chan)
    (shape : WPF3RunShape run) : WPF3Evolution run (run.wake changed) :=
  wpf3_evolution_foldl_evalStep _ run shape

theorem wpf3_evolution_applyPublications (run : Dex.Run σ)
    (publications : List (σ.Chan × σ.Val)) (shape : WPF3RunShape run) :
    WPF3Evolution run (run.applyPublications publications) :=
  wpf3_evolution_of_frame shape (Dex.Run.applyPublications_frame publications run).1
    (Dex.Run.applyPublications_frame publications run).2.1

theorem wpf3_evolution_applyEffects (run : Dex.Run σ) (effects : Dex.Effects σ)
    (shape : WPF3RunShape run) : WPF3Evolution run (run.applyEffects effects) := by
  have written : WPF3Evolution run (run.applyWrites effects.writes) :=
    wpf3_evolution_of_frame shape (Dex.Run.applyWrites_frame effects.writes run).1
      (Dex.Run.applyWrites_frame effects.writes run).2.1
  have published := wpf3_evolution_applyPublications _ effects.publications written.2
  exact wpf3_evolution_trans (wpf3_evolution_trans written published)
    (wpf3_evolution_wake _ _ published.2)

/-! ## `finish`, `close`, and `decide` -/

theorem wpf3_evolution_finishStep (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (finalPhase : Dex.Phase) (isFinal : wpf3PhaseRank finalPhase = 3)
    (shape : WPF3RunShape run) : WPF3Evolution run (run.finishStep step finalPhase) := by
  have disarmed : WPF3Evolution run (run.disarmTimersOf step) :=
    wpf3_evolution_of_frame shape rfl rfl
  refine wpf3_evolution_trans disarmed (wpf3_evolution_updateStep _ _ _ disarmed.2
    (fun _ => rfl) ?_ ?_)
  · intro record _ _
    refine ⟨rfl, ?_, ?_, ?_⟩
    · show _ ≤ wpf3PhaseRank finalPhase
      rw [isFinal]
      cases record.phase <;> simp [wpf3PhaseRank]
    · intro invocation isInvocation; cases isInvocation
    · intro invocation _
      refine Or.inr ?_
      show wpf3PhaseRank (wpf3PhaseOf invocation.method) < wpf3PhaseRank finalPhase
      rw [isFinal]
      cases invocation.method <;> simp [wpf3PhaseOf, wpf3PhaseRank]
  · intro record _ _ invocation isInvocation
    cases isInvocation

theorem wpf3_evolution_foldl_finishStep (steps : List (Dex.StepExecId σ)) (finalPhase : Dex.Phase)
    (isFinal : wpf3PhaseRank finalPhase = 3) (run : Dex.Run σ) (shape : WPF3RunShape run) :
    WPF3Evolution run (steps.foldl (fun current step => current.finishStep step finalPhase) run) := by
  induction steps generalizing run with
  | nil => exact ⟨WPF3RunEvolves.refl run, shape⟩
  | cons step laterSteps inductionHypothesis =>
    have firstStep := wpf3_evolution_finishStep run step finalPhase isFinal shape
    exact wpf3_evolution_trans firstStep (inductionHypothesis _ firstStep.2)

theorem wpf3_evolution_closeRun (run : Dex.Run σ) (status : Dex.CloseStatus)
    (shape : WPF3RunShape run) : WPF3Evolution run (run.closeRun status) := by
  unfold Dex.Run.closeRun
  split
  · exact ⟨WPF3RunEvolves.refl run, shape⟩
  · have canceled := wpf3_evolution_foldl_finishStep
      ((run.steps.filter (fun record => record.phase.isActive)).map (fun record => record.id))
      .canceled rfl run shape
    exact wpf3_evolution_trans canceled (wpf3_evolution_of_frame canceled.2 rfl rfl)

theorem wpf3_evolution_collectOutput (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (output : Option σ.Val) (shape : WPF3RunShape run) :
    WPF3Evolution run (run.collectOutput step output) := by
  unfold Dex.Run.collectOutput
  split
  · exact wpf3_evolution_of_frame shape rfl rfl
  · exact ⟨WPF3RunEvolves.refl run, shape⟩

theorem wpf3_evolution_applyDecision (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (shape : WPF3RunShape run) :
    WPF3Evolution run (run.applyDecision step decision) := by
  have finished := wpf3_evolution_finishStep run step .completed rfl shape
  unfold Dex.Run.applyDecision
  split
  · rename_i output _
    have collected := wpf3_evolution_collectOutput _ step output finished.2
    exact wpf3_evolution_trans (wpf3_evolution_trans finished collected)
      (wpf3_evolution_closeRun _ _ collected.2)
  · exact wpf3_evolution_trans finished (wpf3_evolution_closeRun _ _ finished.2)
  · exact finished
  · exact wpf3_evolution_trans finished
      (wpf3_evolution_foldl_newStep decision.moves (.step step) _ finished.2)

theorem wpf3_evolution_updateStep_evalStep (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (change : Dex.StepRecord σ → Dex.StepRecord σ) (shape : WPF3RunShape run)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id)
    (changedEvolves : ∀ record, record ∈ run.steps → record.id = step →
      WPF3RecordEvolves record (change record))
    (changedMatches : ∀ record, record ∈ run.steps → record.id = step →
      ∀ invocation, (change record).invocation = some invocation →
        (change record).phase = wpf3PhaseOf invocation.method) :
    WPF3Evolution run ((run.updateStep step change).evalStep step) := by
  have updated := wpf3_evolution_updateStep run step change shape keepsId changedEvolves
    changedMatches
  exact wpf3_evolution_trans updated (wpf3_evolution_evalStep _ step updated.2)

/-! ## `register` -/

theorem wpf3_evolution_registerWait (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (wait : Dex.Wait σ) (now : Nat) (shape : WPF3RunShape run)
    (isWaitingForWait : ∀ record, record ∈ run.steps → record.id = step →
      record.phase = .waitFor ∧ record.invocation = none) :
    WPF3Evolution run (run.registerWait step wait now) := by
  unfold Dex.Run.registerWait
  split
  · refine wpf3_evolution_updateStep run step _ shape (by intro; rfl) ?_ ?_
    · intro record isMember isStep
      obtain ⟨isPhase, hasNoInvocation⟩ := isWaitingForWait record isMember isStep
      refine ⟨rfl, ?_, ?_, ?_⟩
      · rw [isPhase]; simp [wpf3PhaseRank]
      · intro invocation isInvocation
        simp only [Option.some.injEq] at isInvocation
        subst isInvocation
        exact Or.inr ⟨rfl, by rw [isPhase]; simp [wpf3PhaseRank]⟩
      · intro invocation isInvocation
        rw [hasNoInvocation] at isInvocation; cases isInvocation
    · intro record _ _ invocation isInvocation
      simp only [Option.some.injEq] at isInvocation
      subst isInvocation
      rfl
  · dsimp only
    refine wpf3_evolution_trans (by exact wpf3_evolution_of_frame shape rfl rfl)
      (wpf3_evolution_updateStep_evalStep _ step _
        (by exact ⟨shape.idsNodup, shape.numbersCounted, shape.methodPhase⟩) (by intro; rfl) ?_ ?_)
    · intro record isMember isStep
      obtain ⟨isPhase, hasNoInvocation⟩ := isWaitingForWait record isMember isStep
      refine ⟨rfl, ?_, ?_, ?_⟩
      · rw [isPhase]; simp [wpf3PhaseRank]
      · intro invocation isInvocation
        rw [hasNoInvocation] at isInvocation; cases isInvocation
      · intro invocation isInvocation
        rw [hasNoInvocation] at isInvocation; cases isInvocation
    · intro record isMember isStep invocation isInvocation
      rw [(isWaitingForWait record isMember isStep).2] at isInvocation
      cases isInvocation

/-! ## Timers -/

theorem wpf3_evolution_fireTimer (run : Dex.Run σ) (timer : Dex.TimerId σ)
    (shape : WPF3RunShape run) : WPF3Evolution run (run.fireTimer timer) := by
  unfold Dex.Run.fireTimer
  split
  · have disarmed : WPF3Evolution run { run with timers := run.timers.erase timer } :=
      wpf3_evolution_of_frame shape rfl rfl
    dsimp only
    split
    · exact disarmed
    · split
      · split
        · exact disarmed
        · exact wpf3_evolution_trans disarmed (wpf3_evolution_updateStep_evalStep _ timer.step _
            disarmed.2 (by intro; rfl)
            (by
              intro record _ _
              exact wpf3_recordEvolves_of_sameMethod record _ rfl rfl rfl rfl rfl rfl)
            (by
              intro record isMember _ invocation isInvocation
              exact disarmed.2.methodPhase record isMember invocation isInvocation))
      · exact disarmed
  · exact ⟨WPF3RunEvolves.refl run, shape⟩

theorem wpf3_evolution_foldl_fireTimer (timers : List (Dex.TimerId σ)) (run : Dex.Run σ)
    (shape : WPF3RunShape run) : WPF3Evolution run (timers.foldl Dex.Run.fireTimer run) := by
  induction timers generalizing run with
  | nil => exact ⟨WPF3RunEvolves.refl run, shape⟩
  | cons timer laterTimers inductionHypothesis =>
    have firstStep := wpf3_evolution_fireTimer run timer shape
    exact wpf3_evolution_trans firstStep (inductionHypothesis _ firstStep.2)

end DexExpressiveness.DexF
