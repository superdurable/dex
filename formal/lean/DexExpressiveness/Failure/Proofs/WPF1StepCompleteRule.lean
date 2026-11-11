import DexExpressiveness.Failure.Proofs.WPF1StepComplete

/-!
# WP-F1: [STEP-COMPLETE-WAITFOR] and [STEP-COMPLETE-EXECUTE]

Each Complete rule clears the invocation, applies the handler's effects, and
then registers the wait or applies the decision. Each of these commutes with
the relabeling, keeps `seq` and the Step IDs unique, and ends the invocation,
so `complete_simulated` applies.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

theorem numbersCounted_base {config : Config σ} {run : Run σ}
    (runInvariant : RunInvariant config run) : NumbersCounted run.base := by
  intro step isMember
  obtain ⟨record, isRecord, isId⟩ := List.mem_map.mp isMember
  rw [← isId]
  exact runInvariant.stepNumbersCounted record isRecord

/-- [STEP-COMPLETE-WAITFOR] and [STEP-COMPLETE-EXECUTE] simulate. -/
theorem stepComplete_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config (.stepComplete taskId) = some (events, next)) :
    IsStepSimulated program config (.stepComplete taskId) events next := by
  have isStepCopy := isStep
  simp only [step] at isStepCopy
  split at isStepCopy
  · rename_i canServe
    split at isStepCopy
    · rename_i run task hasRun isTask
      split at isStepCopy
      · rename_i isFence
        obtain ⟨record, attempt, _, _, _, isMember, isId, isAttempt, hasInvocation, isMethod, _,
          _, _, _, _, _, _, _⟩ := complete_context invariant hasRun isTask isFence
        have runInvariant := invariant.runInvariant run hasRun
        have canCommit : config.canCommit = true := by
          simp only [Config.canServeCommit, Bool.and_eq_true] at canServe
          exact canServe.2
        have absCommit := canCommit_abstract invariant canCommit
        have absRun : (abstractConfig config).run = some (Run.abstract config.ghost run) := by
          simp [abstractConfig, hasRun]
        have freshUpdated : IsWaitFresh (commitView config run)
            ((bumpedRun run).base.updateStep task.base.step
              (fun record => { record with invocation := none })) := by
          apply (isWaitFresh_bumpedRun runInvariant).updateStep
          · intro _
            rfl
          · intro other isOther _ isWaiting
            exact isWaitFresh_bumpedRun runInvariant other isOther isWaiting
        have orderUpdated : IsOrderKept (commitRelabel config run)
            ((bumpedRun run).base.updateStep task.base.step
              (fun record => { record with invocation := none })) :=
          (isOrderKept_bumpedRun runInvariant).updateStep _ _ (fun _ => rfl) (fun _ => rfl)
        have relabelUpdated :
            (relabelRun (commitView config run) (commitRelabel config run)
              (bumpedRun run).base).updateStep task.base.step
                (fun record => { record with invocation := none }) =
              relabelRun (commitView config run) (commitRelabel config run)
                ((bumpedRun run).base.updateStep task.base.step
                  (fun record => { record with invocation := none })) :=
          updateStep_relabelRun _ _ _ _ _ _ (fun _ _ => rfl)
        have idsUpdated : ((bumpedRun run).base.updateStep task.base.step
            (fun record => { record with invocation := none })).steps.map
              (fun record => record.id) = run.base.steps.map (fun record => record.id) :=
          Dex.Run.ids_updateStep _ _ _ (fun _ => rfl)
        have nonWaitingUpdated : NonWaitingAt task.base.step
            ((bumpedRun run).base.updateStep task.base.step
              (fun record => { record with invocation := none })) := by
          have nonWaiting := nonWaitingAt_of_invocation runInvariant record isMember _ hasInvocation
          rw [isId] at nonWaiting
          exact NonWaitingAt.updateStep nonWaiting _ _ (fun _ => rfl) (fun _ => rfl)
        have isCounted : task.base.step.number ≤
            (run.base.counters.get? task.base.step.stepType).getD 0 := by
          have := runInvariant.stepNumbersCounted record isMember
          rw [isId] at this
          exact this
        split at isStepCopy
        · rename_i response isTaskMethod isPhase
          simp only [Option.some.injEq] at isStepCopy
          have isPending : commitView config run task.base.step .execute = .pending := by
            unfold commitView attemptView
            rw [isAttempt]
            simp [isMethod, isTaskMethod]
          apply complete_simulated program config invariant run hasRun taskId task isTask isFence
            (.waitFor response) isPhase
            (fun base => ((base.updateStep task.base.step
              (fun record => { record with invocation := none })).applyEffects
                response.effects).registerWait task.base.step response.wait config.clock)
          · rw [relabelUpdated, applyEffects_relabelRun _ _ _ _ freshUpdated orderUpdated,
              registerWait_relabelRun _ _ _ _ _ _ isPending (freshUpdated.applyEffects _)]
          · rw [Dex.Run.seq_registerWait, Dex.Run.seq_applyEffects]
            rfl
          · rw [ids_registerWait, ids_applyEffects, idsUpdated]
            exact runInvariant.stepIdsNodup
          · rw [isTaskMethod]
            apply NotCurrentAt.registerWait
            apply NotCurrentAt.applyEffects _ _ (Or.inl rfl)
            exact notCurrentAt_updateStep_none _ _ _
          · intro arguments isFoundTask isCurrentTask
            simp only [Dex.step, absRun, isFoundTask]
            rw [isCurrentTask, absCommit]
            simp only [Bool.and_self, ↓reduceIte, Arguments.toTask, Task.key, isTaskMethod]
            rfl
          · exact isStep
          · exact isStepCopy
        · rename_i response isTaskMethod isPhase
          simp only [Option.some.injEq] at isStepCopy
          apply complete_simulated program config invariant run hasRun taskId task isTask isFence
            (.execute response) isPhase
            (fun base => ((base.updateStep task.base.step
              (fun record => { record with invocation := none })).applyEffects
                response.effects).applyDecision task.base.step response.decision)
          · rw [relabelUpdated, applyEffects_relabelRun _ _ _ _ freshUpdated orderUpdated,
              applyDecision_relabelRun _ _ _ _ _
                ((isCounterFresh_bumpedRun runInvariant).of_counters (by
                  rw [counters_applyEffects]
                  rfl))]
          · rw [Dex.Run.seq_applyDecision, Dex.Run.seq_applyEffects]
            rfl
          · apply nodup_applyDecision
            · rw [ids_applyEffects, idsUpdated]
              exact runInvariant.stepIdsNodup
            · apply (numbersCounted_base runInvariant).of_same
              · rw [ids_applyEffects, idsUpdated]
              · rw [counters_applyEffects]
                rfl
          · apply NotCurrentAt.applyDecision
            · apply NotCurrentAt.applyEffects _ _ (Or.inr nonWaitingUpdated)
              exact notCurrentAt_updateStep_none _ _ _
            · rw [counters_applyEffects]
              exact isCounted
          · intro arguments isFoundTask isCurrentTask
            simp only [Dex.step, absRun, isFoundTask]
            rw [isCurrentTask, absCommit]
            simp only [Bool.and_self, ↓reduceIte, Arguments.toTask, Task.key, isTaskMethod]
            try rfl
          · exact isStep
          · exact isStepCopy
        · cases isStepCopy
      · cases isStepCopy
    · cases isStepCopy
  · cases isStepCopy

end DexExpressiveness.DexF.WPF1
