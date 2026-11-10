import DexExpressiveness.Failure.Proofs.WPF3RunEvolution

/-!
# WP-F3: the Step table under a Complete rule

A Complete rule clears the invocation of its Step, applies the data effects,
and then registers the wait (WaitFor) or applies the decision (Execute). The
first part alone is no evolution, because the Step keeps its phase without an
invocation; the whole rule is one, because the last part moves the Step past
the method's phase. Everything here is proved.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- Clearing an invocation keeps the shape. -/
theorem wpf3_shape_clearInvocation (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (shape : WPF3RunShape run) :
    WPF3RunShape (run.updateStep step (fun record => { record with invocation := none })) := by
  refine ⟨?_, ?_, ?_⟩
  · rw [Dex.Run.ids_updateStep run step (fun record => { record with invocation := none })
      (fun _ => rfl)]
    exact shape.idsNodup
  · intro next isMember
    obtain ⟨original, isOriginalMember, isNext⟩ := (Dex.Run.mem_updateStep run step _ next).mp isMember
    have isSameId : next.id = original.id := by rw [isNext]; split <;> rfl
    rw [isSameId]
    exact shape.numbersCounted original isOriginalMember
  · intro next isMember invocation isInvocation
    obtain ⟨original, isOriginalMember, isNext⟩ := (Dex.Run.mem_updateStep run step _ next).mp isMember
    split at isNext
    · rw [isNext] at isInvocation; cases isInvocation
    · rw [isNext] at isInvocation ⊢
      exact shape.methodPhase original isOriginalMember invocation isInvocation

/-- `eval(e')` keeps the records of a Step that does not wait. -/
theorem wpf3_evalStep_keeps_notWaiting (run : Dex.Run σ) (step other : Dex.StepExecId σ)
    (isNotWaiting : ∀ record, record ∈ run.steps → record.id = step → record.phase ≠ .waiting) :
    ∀ record, record ∈ (run.evalStep other).steps → record.id = step → record ∈ run.steps := by
  rcases Dex.Run.evalStep_cases run other with ⟨isSame, _⟩ |
    ⟨found, waitState, consumeFlags, isFound, isWaiting, _, isEval⟩
  · rw [isSame]; exact fun record isMember _ => isMember
  · intro record isMember isStep
    rw [isEval] at isMember
    obtain ⟨original, isOriginalMember, isRecord⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMember
    rw [Dex.Run.consumeMessages_steps] at isOriginalMember
    split at isRecord
    · rename_i isOther
      exfalso
      obtain ⟨isFoundMember, isFoundId⟩ := Dex.Run.mem_of_findStep? run other found isFound
      have isRecordId : record.id = original.id := by rw [isRecord]
      apply isNotWaiting found isFoundMember (by rw [isFoundId, ← isOther, ← isRecordId, isStep])
      exact isWaiting
    · rw [isRecord]; exact isOriginalMember

theorem wpf3_foldl_evalStep_keeps_notWaiting (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ)
    (step : Dex.StepExecId σ)
    (isNotWaiting : ∀ record, record ∈ run.steps → record.id = step → record.phase ≠ .waiting) :
    ∀ record, record ∈ (steps.foldl Dex.Run.evalStep run).steps → record.id = step →
      record ∈ run.steps := by
  induction steps generalizing run with
  | nil => exact fun record isMember _ => isMember
  | cons other laterSteps inductionHypothesis =>
    intro record isMember isStep
    have keeps := wpf3_evalStep_keeps_notWaiting run step other isNotWaiting
    exact keeps record (inductionHypothesis (run.evalStep other)
      (fun candidate isCandidate isCandidateStep =>
        isNotWaiting candidate (keeps candidate isCandidate isCandidateStep) isCandidateStep)
      record isMember isStep) isStep

/-- The data effects keep the records of a Step that does not wait. -/
theorem wpf3_applyEffects_keeps_notWaiting (run : Dex.Run σ) (effects : Dex.Effects σ)
    (step : Dex.StepExecId σ)
    (isNotWaiting : ∀ record, record ∈ run.steps → record.id = step → record.phase ≠ .waiting) :
    ∀ record, record ∈ (run.applyEffects effects).steps → record.id = step →
      record ∈ run.steps := by
  intro record isMember isStep
  unfold Dex.Run.applyEffects Dex.Run.wake at isMember
  have sameSteps : ((run.applyWrites effects.writes).applyPublications
      effects.publications).steps = run.steps := by
    rw [(Dex.Run.applyPublications_frame _ _).1, (Dex.Run.applyWrites_frame _ _).1]
  have kept := wpf3_foldl_evalStep_keeps_notWaiting _ _ step
    (by rw [sameSteps]; exact isNotWaiting) record isMember isStep
  rw [sameSteps] at kept
  exact kept

/-- `register` moves its Step past `WF`. -/
theorem wpf3_registerWait_beyond (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ)
    (now : Nat) (shape : WPF3RunShape run)
    (isWaitingForWait : ∀ record, record ∈ run.steps → record.id = step →
      record.phase = .waitFor ∧ record.invocation = none)
    (hasStep : ∃ record, record ∈ run.steps ∧ record.id = step) :
    ∀ record, record ∈ (run.registerWait step wait now).steps → record.id = step →
      wpf3IsBeyond record.phase .waitFor := by
  intro record isMember isStep
  unfold Dex.Run.registerWait at isMember
  split at isMember
  · obtain ⟨original, _, isRecord⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMember
    have isOriginalStep : original.id = step := by
      rw [← isStep, isRecord]; split <;> rfl
    rw [isRecord, if_pos isOriginalStep]
    simp [wpf3IsBeyond, wpf3PhaseOf, wpf3PhaseRank]
  · dsimp only at isMember
    have registered := wpf3_evolution_updateStep
      { run with timers := Dex.Run.armTimers step (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0 run.timers }
      step (fun current =>
      { current with
        phase := .waiting
        waitState := some
          { wait := wait
            fireAt := wait.timerSeconds.map (fun seconds => now + seconds * 1000000)
            timersDone := wait.timerSeconds.map (fun _ => false)
            registeredSeq := run.seq } })
      (by exact ⟨shape.idsNodup, shape.numbersCounted, shape.methodPhase⟩) (by intro; rfl)
      (by
        intro original isOriginalMember isOriginalStep
        obtain ⟨isPhase, hasNoInvocation⟩ := isWaitingForWait original isOriginalMember
          isOriginalStep
        refine ⟨rfl, ?_, ?_, ?_⟩
        · show _ ≤ wpf3PhaseRank .waiting
          rw [isPhase]; simp [wpf3PhaseRank]
        · intro invocation isInvocation
          rw [hasNoInvocation] at isInvocation; cases isInvocation
        · intro invocation isInvocation
          rw [hasNoInvocation] at isInvocation; cases isInvocation)
      (by
        intro original isOriginalMember isOriginalStep invocation isInvocation
        rw [(isWaitingForWait original isOriginalMember isOriginalStep).2] at isInvocation
        cases isInvocation)
    have evolution := wpf3_evolution_evalStep _ step registered.2
    rcases evolution.1.evolves record isMember with ⟨middle, isMiddleMember, middleEvolves⟩ | isNew
    · obtain ⟨original, _, isMiddle⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMiddleMember
      have isMiddleStep : middle.id = step := middleEvolves.sameId.symm.trans isStep
      have isOriginalStep : original.id = step := by
        rw [← isMiddleStep, isMiddle]; split <;> rfl
      rw [isMiddle, if_pos isOriginalStep] at middleEvolves
      have grows := middleEvolves.rankGrows
      simp only [wpf3PhaseRank] at grows
      unfold wpf3IsBeyond
      simp only [wpf3PhaseOf, wpf3PhaseRank]
      omega
    · exfalso
      obtain ⟨original, isOriginalMember, isOriginalStep⟩ := hasStep
      obtain ⟨middle, isMiddleMember, isMiddleId⟩ := registered.1.keepsIds original
        (by exact isOriginalMember)
      exact isNew middle isMiddleMember (isMiddleId.trans (isOriginalStep.trans isStep.symm))

/-- `decide` moves its Step to `CO`, past `EX`. -/
theorem wpf3_applyDecision_beyond (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (shape : WPF3RunShape run)
    (hasStep : ∃ record, record ∈ run.steps ∧ record.id = step) :
    ∀ record, record ∈ (run.applyDecision step decision).steps → record.id = step →
      wpf3IsBeyond record.phase .execute := by
  intro record isMember isStep
  have finished := wpf3_evolution_finishStep run step .completed rfl shape
  have rest : WPF3Evolution (run.finishStep step .completed) (run.applyDecision step decision) := by
    unfold Dex.Run.applyDecision
    split
    · rename_i output _
      have collected := wpf3_evolution_collectOutput _ step output finished.2
      exact wpf3_evolution_trans collected (wpf3_evolution_closeRun _ _ collected.2)
    · exact wpf3_evolution_closeRun _ _ finished.2
    · exact ⟨WPF3RunEvolves.refl _, finished.2⟩
    · exact wpf3_evolution_foldl_newStep decision.moves (.step step) _ finished.2
  rcases rest.1.evolves record isMember with ⟨middle, isMiddleMember, middleEvolves⟩ | isNew
  · obtain ⟨original, _, isMiddle⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMiddleMember
    have isMiddleStep : middle.id = step := middleEvolves.sameId.symm.trans isStep
    have isOriginalStep : original.id = step := by
      rw [← isMiddleStep, isMiddle]; split <;> rfl
    rw [isMiddle, if_pos isOriginalStep] at middleEvolves
    have grows := middleEvolves.rankGrows
    simp only [wpf3PhaseRank] at grows
    unfold wpf3IsBeyond
    simp only [wpf3PhaseOf, wpf3PhaseRank]
    revert grows
    cases record.phase <;> simp [wpf3PhaseRank]
  · exfalso
    obtain ⟨original, isOriginalMember, isOriginalStep⟩ := hasStep
    obtain ⟨middle, isMiddleMember, isMiddleId⟩ := finished.1.keepsIds original isOriginalMember
    exact isNew middle isMiddleMember (isMiddleId.trans (isOriginalStep.trans isStep.symm))

/-- **A Complete rule is an evolution.** `finalize` is `register` or `decide`. -/
theorem wpf3_evolution_complete (run : Dex.Run σ) (shape : WPF3RunShape run)
    (step : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isRecord : run.findStep? step = some record) (invocation : Dex.Invocation)
    (isInvocation : record.invocation = some invocation) (effects : Dex.Effects σ)
    (finalize : Dex.Run σ → Dex.Run σ)
    (finalizeEvolution : ∀ cleared, WPF3RunShape cleared →
      (∀ candidate, candidate ∈ cleared.steps → candidate.id = step →
        candidate.phase = record.phase ∧ candidate.invocation = none) →
      (∃ candidate, candidate ∈ cleared.steps ∧ candidate.id = step) →
      WPF3Evolution cleared (finalize cleared) ∧
      ∀ candidate, candidate ∈ (finalize cleared).steps → candidate.id = step →
        wpf3IsBeyond candidate.phase invocation.method) :
    WPF3Evolution run (finalize ((run.updateStep step (fun current =>
      { current with invocation := none })).applyEffects effects)) := by
  obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? run step record isRecord
  have recordPhase := shape.methodPhase record isMember invocation isInvocation
  have isOnly : ∀ candidate, candidate ∈ run.steps → candidate.id = step → candidate = record :=
    fun candidate isCandidate isCandidateStep => eq_of_nodup_map (fun record => record.id)
      run.steps shape.idsNodup isCandidate isMember (isCandidateStep.trans isId.symm)
  have clearedShape := wpf3_shape_clearInvocation run step shape
  have applied := wpf3_evolution_applyEffects _ effects clearedShape
  have clearedRecords : ∀ candidate, candidate ∈ (run.updateStep step (fun current =>
      { current with invocation := none })).steps → candidate.id = step →
      candidate = { record with invocation := none } := by
    intro candidate isCandidate isCandidateStep
    obtain ⟨original, isOriginalMember, isCandidateEq⟩ :=
      (Dex.Run.mem_updateStep _ _ _ _).mp isCandidate
    have isOriginalStep : original.id = step := by
      rw [← isCandidateStep, isCandidateEq]; split <;> rfl
    rw [isCandidateEq, if_pos isOriginalStep, isOnly original isOriginalMember isOriginalStep]
  have isNotWaiting : ∀ candidate, candidate ∈ (run.updateStep step (fun current =>
      { current with invocation := none })).steps → candidate.id = step →
      candidate.phase ≠ .waiting := by
    intro candidate isCandidate isCandidateStep
    rw [clearedRecords candidate isCandidate isCandidateStep]
    show record.phase ≠ .waiting
    rw [recordPhase]
    cases invocation.method <;> simp [wpf3PhaseOf]
  have appliedRecords : ∀ candidate, candidate ∈ ((run.updateStep step (fun current =>
      { current with invocation := none })).applyEffects effects).steps →
      candidate.id = step → candidate.phase = record.phase ∧ candidate.invocation = none := by
    intro candidate isCandidate isCandidateStep
    rw [clearedRecords candidate (wpf3_applyEffects_keeps_notWaiting _ effects step isNotWaiting
      candidate isCandidate isCandidateStep) isCandidateStep]
    exact ⟨rfl, rfl⟩
  have hasStep : ∃ candidate, candidate ∈ ((run.updateStep step (fun current =>
      { current with invocation := none })).applyEffects effects).steps ∧ candidate.id = step := by
    obtain ⟨candidate, isCandidate, isCandidateId⟩ := applied.1.keepsIds
      { record with invocation := none }
      ((Dex.Run.mem_updateStep _ _ _ _).mpr ⟨record, isMember, by rw [if_pos isId]⟩)
    exact ⟨candidate, isCandidate, isCandidateId.trans isId⟩
  obtain ⟨finalized, beyond⟩ := finalizeEvolution _ applied.2 appliedRecords hasStep
  refine ⟨⟨?_, ?_⟩, finalized.2⟩
  · intro original isOriginalMember
    obtain ⟨middle, isMiddleMember, isMiddleId⟩ := applied.1.keepsIds
      (if original.id = step then { original with invocation := none } else original)
      ((Dex.Run.mem_updateStep _ _ _ _).mpr ⟨original, isOriginalMember, rfl⟩)
    obtain ⟨next, isNextMember, isNextId⟩ := finalized.1.keepsIds middle isMiddleMember
    refine ⟨next, isNextMember, isNextId.trans (isMiddleId.trans ?_)⟩
    split <;> rfl
  · intro next isNextMember
    have isNewOfApplied : (∀ middle, middle ∈ ((run.updateStep step (fun current =>
        { current with invocation := none })).applyEffects effects).steps →
        middle.id ≠ next.id) → ∀ original, original ∈ run.steps → original.id ≠ next.id := by
      intro isNewMiddle original isOriginalMember isSameId
      obtain ⟨middle, isMiddleMember, isMiddleId⟩ := applied.1.keepsIds
        (if original.id = step then { original with invocation := none } else original)
        ((Dex.Run.mem_updateStep _ _ _ _).mpr ⟨original, isOriginalMember, rfl⟩)
      apply isNewMiddle middle isMiddleMember
      rw [isMiddleId, ← isSameId]
      split <;> rfl
    rcases finalized.1.evolves next isNextMember with ⟨middle, isMiddleMember, middleEvolves⟩ |
      isNewFinal
    · rcases applied.1.evolves middle isMiddleMember with
        ⟨cleared, isClearedMember, clearedEvolves⟩ | isNewApplied
      · obtain ⟨original, isOriginalMember, isCleared⟩ :=
          (Dex.Run.mem_updateStep _ _ _ _).mp isClearedMember
        by_cases isOriginalStep : original.id = step
        · rw [if_pos isOriginalStep] at isCleared
          have isOriginal := isOnly original isOriginalMember isOriginalStep
          subst isOriginal
          subst isCleared
          have chain := clearedEvolves.trans middleEvolves
          refine Or.inl ⟨original, isOriginalMember, chain.sameId, chain.rankGrows, ?_, ?_⟩
          · intro nextInvocation isNextInvocation
            rcases chain.invocationOrigin nextInvocation isNextInvocation with
              ⟨previous, isPrevious, _⟩ | isExecute
            · cases isPrevious
            · exact Or.inr isExecute
          · intro previous isPrevious
            rw [isInvocation] at isPrevious
            cases isPrevious
            exact Or.inr (beyond next isNextMember
              (chain.sameId.trans isOriginalStep))
        · rw [if_neg isOriginalStep] at isCleared
          rw [isCleared] at clearedEvolves
          exact Or.inl ⟨original, isOriginalMember, clearedEvolves.trans middleEvolves⟩
      · refine Or.inr ?_
        intro original isOriginalMember isSameId
        apply isNewApplied
          (if original.id = step then { original with invocation := none } else original)
          ((Dex.Run.mem_updateStep _ _ _ _).mpr ⟨original, isOriginalMember, rfl⟩)
        have isClearedId : (if original.id = step then { original with invocation := none }
            else original).id = original.id := by split <;> rfl
        exact isClearedId.trans (isSameId.trans middleEvolves.sameId)
    · exact Or.inr (isNewOfApplied isNewFinal)

end DexExpressiveness.DexF
