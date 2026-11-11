import DexExpressiveness.Failure.Proofs.WPF3GoodSteps

/-!
# WP-F3: the effect history of every finite run

`wpf3_good_step`: every DexF step keeps `WPF3Good`, for a program that
satisfies H3. `effectHistory_of_exec_proof` follows from it and
`wpf3_effectHistory_of_exec`. Everything here is proved.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- Every DexF step keeps the invariant of the effect history. -/
theorem wpf3_good_step (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (good : WPF3Good program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next)) :
    WPF3Good program next := by
  cases action with
  | tick duration =>
    dsimp only [step] at isStep
    split at isStep
    · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
      subst isNext
      exact wpf3_good_of_sameTasks program config _ good rfl rfl rfl rfl
    · cases isStep
  | request clientRequest =>
    cases clientRequest with
    | startFlow requestId stepType input =>
      dsimp only [step] at isStep
      cases isRun : config.run with
      | some _ => rw [isRun] at isStep; simp at isStep
      | none =>
        rw [isRun] at isStep
        dsimp only at isStep
        split at isStep
        · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
          subst isNext
          have emptyGood := wpf3_runGood_empty config.ghost (good.notStarted isRun) requestId
          refine wpf3_good_commitRun program config good _ emptyGood true none _ _ ?_
            (by exact WPF3AttemptsEvolve.refl _)
          exact wpf3_evolution_trans (wpf3_evolution_bump _ emptyGood.shape)
            (wpf3_evolution_newStep _ _ _ _ _ (wpf3_evolution_bump _ emptyGood.shape).2)
        · cases isStep
    | publish chan value =>
      dsimp only [step] at isStep
      split at isStep
      · cases isRun : config.run with
        | none =>
          rw [isRun] at isStep
          obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
          subst isNext
          exact good
        | some run =>
          rw [isRun] at isStep
          dsimp only at isStep
          have runGood := good.runGood run isRun
          split at isStep
          · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
            subst isNext
            exact good
          · split at isStep
            · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
              subst isNext
              refine wpf3_good_commitRun program config good run runGood true none _ _ ?_
                (by exact WPF3AttemptsEvolve.refl _)
              have bumped := wpf3_evolution_bump run runGood.shape
              have published := wpf3_evolution_applyPublications _ [(chan, value)] bumped.2
              exact wpf3_evolution_trans (wpf3_evolution_trans bumped published)
                (wpf3_evolution_wake _ _ published.2)
            · cases isStep
      · cases isStep
  | startRetry requestId stepType input =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
         subst isNext
         exact wpf3_good_of_sameTasks program config _ good rfl rfl rfl rfl)
  | engineReject clientRequest =>
    cases clientRequest with
    | startFlow _ _ _ => simp [step] at isStep
    | publish chan value =>
      dsimp only [step] at isStep
      split at isStep
      · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
        subst isNext
        exact good
      · cases isStep
  | stepStart stepId isSnapshotReused =>
    exact wpf3_good_stepStart program config good stepId isSnapshotReused events next isStep
  | workerRun taskId isFirstRun =>
    exact wpf3_good_workerRun program ignoresAttemptContext config good taskId isFirstRun events
      next isStep
  | workerEffect taskId isFirstOccurrence =>
    exact wpf3_good_workerEffect program config good taskId isFirstOccurrence events next isStep
  | workerReturn taskId isFirstReturn =>
    exact wpf3_good_workerReturn program config good taskId isFirstReturn events next isStep
  | workerAbandon taskId isLastOfCanceled =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
         subst isNext
         exact wpf3_good_removeRealTask program config good taskId)
  | stepComplete taskId =>
    dsimp only [step] at isStep
    split at isStep
    · cases isRun : config.run with
      | none => rw [isRun] at isStep; simp at isStep
      | some run =>
        cases isFound : config.findRealTask? taskId with
        | none => rw [isRun, isFound] at isStep; simp at isStep
        | some task =>
          rw [isRun, isFound] at isStep
          dsimp only at isStep
          split at isStep
          · rename_i isFence
            have runGood := good.runGood run isRun
            obtain ⟨record, isRecord, isInvocation⟩ :=
              wpf3_fence_facts config run isRun task isFence
            have recordPhase := runGood.shape.methodPhase record
              (Dex.Run.mem_of_findStep? _ _ _ isRecord).1 _ isInvocation
            have removedGood := wpf3_good_removeRealTask program config good taskId
            split at isStep
            · rename_i response isMethod _
              obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
              subst isNext
              refine wpf3_good_commitRun program _ removedGood run runGood true _ [] _ ?_
                (by exact WPF3AttemptsEvolve.refl _)
              refine wpf3_evolution_trans (wpf3_evolution_bump run runGood.shape) ?_
              exact wpf3_evolution_complete _ (wpf3_evolution_bump run runGood.shape).2
                task.base.step record isRecord _ isInvocation response.effects
                (fun cleared => cleared.registerWait task.base.step response.wait config.clock)
                (fun cleared clearedShape clearedRecords hasStep => by
                  have isWaitingForWait : ∀ candidate, candidate ∈ cleared.steps →
                      candidate.id = task.base.step →
                      candidate.phase = .waitFor ∧ candidate.invocation = none := by
                    intro candidate isCandidate isCandidateStep
                    obtain ⟨isPhase, hasNoInvocation⟩ :=
                      clearedRecords candidate isCandidate isCandidateStep
                    refine ⟨?_, hasNoInvocation⟩
                    rw [isPhase, recordPhase]
                    show wpf3PhaseOf task.base.method = _
                    rw [isMethod]
                    rfl
                  refine ⟨wpf3_evolution_registerWait cleared _ _ _ clearedShape isWaitingForWait,
                    ?_⟩
                  show ∀ candidate : Dex.StepRecord σ, candidate ∈
                      (cleared.registerWait task.base.step response.wait config.clock).steps →
                      candidate.id = task.base.step →
                      wpf3IsBeyond candidate.phase task.base.method
                  rw [isMethod]
                  exact wpf3_registerWait_beyond cleared _ _ _ clearedShape isWaitingForWait
                    hasStep)
            · rename_i response isMethod _
              obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
              subst isNext
              refine wpf3_good_commitRun program _ removedGood run runGood true _ [] _ ?_
                (by exact WPF3AttemptsEvolve.refl _)
              refine wpf3_evolution_trans (wpf3_evolution_bump run runGood.shape) ?_
              exact wpf3_evolution_complete _ (wpf3_evolution_bump run runGood.shape).2
                task.base.step record isRecord _ isInvocation response.effects
                (fun cleared => cleared.applyDecision task.base.step response.decision)
                (fun cleared clearedShape _ hasStep => by
                  refine ⟨wpf3_evolution_applyDecision cleared _ _ clearedShape, ?_⟩
                  show ∀ candidate : Dex.StepRecord σ, candidate ∈
                      (cleared.applyDecision task.base.step response.decision).steps →
                      candidate.id = task.base.step →
                      wpf3IsBeyond candidate.phase task.base.method
                  rw [isMethod]
                  exact wpf3_applyDecision_beyond cleared _ _ clearedShape hasStep)
            · cases isStep
          · cases isStep
    · cases isStep
  | resultStale taskId isLastOfCanceled =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
         subst isNext
         exact wpf3_good_of_sameTasks program (config.removeRealTask taskId) _
           (wpf3_good_removeRealTask program config good taskId) rfl rfl rfl rfl)
  | timerFire count isWaitTimerFired =>
    dsimp only [step] at isStep
    split at isStep
    · cases isRun : config.run with
      | none => rw [isRun] at isStep; simp at isStep
      | some run =>
        rw [isRun] at isStep
        dsimp only at isStep
        split at isStep
        · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
          subst isNext
          have runGood := good.runGood run isRun
          have bumped := wpf3_evolution_bump run runGood.shape
          obtain ⟨fired, firedAttempts⟩ := wpf3_fireDueTimers_facts
            ((run.dueTimers config.clock).take count) (wpf3Bumped run) bumped.2
          exact wpf3_good_commitRun program config good run runGood isWaitTimerFired none [] _
            (wpf3_evolution_trans bumped fired) firedAttempts
        · cases isStep
    · cases isStep
  | attemptFailed stepId decision =>
    dsimp only [step] at isStep
    split at isStep
    · cases isRun : config.run with
      | none => rw [isRun] at isStep; simp at isStep
      | some run =>
        rw [isRun] at isStep
        dsimp only at isStep
        cases isRecord : run.base.findStep? stepId with
        | none => rw [isRecord] at isStep; simp at isStep
        | some record =>
          cases isAttempt : run.findAttempt? stepId with
          | none => rw [isRecord, isAttempt] at isStep; simp at isStep
          | some attempt =>
            rw [isRecord, isAttempt] at isStep
            dsimp only at isStep
            cases isInvocation : record.invocation with
            | none => rw [isInvocation] at isStep; simp at isStep
            | some invocation =>
              obtain ⟨method, state⟩ := invocation
              cases state with
              | running taskId =>
                rw [isInvocation] at isStep
                dsimp only at isStep
                split at isStep
                · cases isStep
                · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
                  subst isNext
                  exact wpf3_good_failAttempt program config good run isRun stepId record isRecord
                    method _ isInvocation attempt isAttempt _ _ _
              | pending =>
                rw [isInvocation] at isStep
                dsimp only at isStep
                split at isStep
                · cases isStep
                · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
                  subst isNext
                  exact wpf3_good_failAttempt program config good run isRun stepId record isRecord
                    method _ isInvocation attempt isAttempt _ _ _
    · cases isStep
  | attemptReturned stepId =>
    dsimp only [step] at isStep
    split at isStep
    · cases isRun : config.run with
      | none => rw [isRun] at isStep; simp at isStep
      | some run =>
        rw [isRun] at isStep
        dsimp only at isStep
        cases isRecord : run.base.findStep? stepId with
        | none => rw [isRecord] at isStep; simp at isStep
        | some record =>
          cases isAttempt : run.findAttempt? stepId with
          | none => rw [isRecord, isAttempt] at isStep; simp at isStep
          | some attempt =>
            rw [isRecord, isAttempt] at isStep
            dsimp only at isStep
            cases isInvocation : record.invocation with
            | none => rw [isInvocation] at isStep; simp at isStep
            | some invocation =>
              obtain ⟨method, state⟩ := invocation
              cases state with
              | pending => rw [isInvocation] at isStep; simp at isStep
              | running taskId =>
                rw [isInvocation] at isStep
                dsimp only at isStep
                split at isStep
                · cases isStep
                · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
                  subst isNext
                  have runGood := good.runGood run isRun
                  have currentMethod := wpf3_currentMethod run.base runGood.shape stepId record
                    isRecord _ isInvocation
                  refine wpf3_good_commitRun program _
                    (wpf3_good_moveToZombies program config good taskId) run runGood false none
                    [] _ ?_ (by exact WPF3AttemptsEvolve.refl _)
                  exact wpf3_evolution_trans (wpf3_evolution_bump run runGood.shape)
                    (wpf3_evolution_setInvocation _ stepId
                      (wpf3_evolution_bump run runGood.shape).2 method .pending currentMethod)
    · cases isStep
  | attemptHeartbeat stepId value =>
    dsimp only [step] at isStep
    split at isStep
    · cases isRun : config.run with
      | none => rw [isRun] at isStep; simp at isStep
      | some run =>
        rw [isRun] at isStep
        dsimp only at isStep
        cases isRecord : run.base.findStep? stepId with
        | none => rw [isRecord] at isStep; simp at isStep
        | some record =>
          cases isAttempt : run.findAttempt? stepId with
          | none => rw [isRecord, isAttempt] at isStep; simp at isStep
          | some attempt =>
            rw [isRecord, isAttempt] at isStep
            dsimp only at isStep
            cases isInvocation : record.invocation with
            | none => rw [isInvocation] at isStep; simp at isStep
            | some invocation =>
              obtain ⟨method, state⟩ := invocation
              cases state with
              | pending => rw [isInvocation] at isStep; simp at isStep
              | running taskId =>
                rw [isInvocation] at isStep
                dsimp only at isStep
                split at isStep
                · cases isStep
                · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
                  subst isNext
                  have runGood := good.runGood run isRun
                  exact wpf3_good_commitRun program config good run runGood false none [] _
                    (wpf3_evolution_bump run runGood.shape)
                    (wpf3_attemptsEvolve_set run.attempts stepId attempt _ isAttempt rfl rfl)
    · cases isStep
  | crash =>
    dsimp only [step] at isStep
    split at isStep
    · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
      subst isNext
      have isPerm : (config.zombies ++ config.tasks).Perm config.realTasks :=
        List.perm_append_comm
      refine wpf3_good_of_sameRun program config _ good rfl (fun _ => rfl)
        (fun _ => Nat.le_refl _) (fun task isMember => isPerm.mem_iff.mp ?_)
        (wpf3_ids_nodup_of_perm isPerm good.realTaskIdsNodup) (Nat.le_refl _)
      simpa [Config.realTasks] using isMember
    · cases isStep
  | recover =>
    dsimp only [step] at isStep
    split at isStep
    · cases isStep
    · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
      subst isNext
      exact wpf3_good_of_sameTasks program config _ good rfl rfl rfl rfl
  | recoverInterrupt interrupt decision =>
    dsimp only [step] at isStep
    split at isStep
    · have removedGood : WPF3Good program
          { config with interrupts := config.interrupts.erase interrupt } :=
        wpf3_good_of_sameTasks program config _ good rfl rfl rfl rfl
      split at isStep
      · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
        subst isNext
        exact wpf3_good_of_sameTasks program config _ good rfl rfl rfl rfl
      · rename_i run isRun
        split at isStep
        · rename_i record attempt isRecord isAttempt
          split at isStep
          · rename_i isApplied
            obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
            subst isNext
            simp only [Bool.and_eq_true, decide_eq_true_eq, Bool.not_eq_true'] at isApplied
            exact wpf3_good_failAttempt program _ removedGood run isRun interrupt.step record
              isRecord interrupt.method _ isApplied.1.1.1 attempt isAttempt .interrupted
              (some interrupt.taskId) decision
          · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
            subst isNext
            exact wpf3_good_of_sameTasks program config _ good rfl rfl rfl rfl
        · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
          subst isNext
          exact wpf3_good_of_sameTasks program config _ good rfl rfl rfl rfl
    · cases isStep
  | internalCommit =>
    dsimp only [step] at isStep
    split at isStep
    · obtain ⟨_, isNext⟩ := wpf3_of_some_eq isStep
      subst isNext
      exact wpf3_good_of_sameTasks program config _ good rfl rfl rfl rfl
    · cases isStep

/-- **WP-F3.** The ghost `h` and the trace agree on every finite run: the
indexes of the `fx(e, m)` events of the trace are exactly `1, …, h(e, m)`,
and each carries the label `fx⃗(e, m)[j]`. -/
theorem effectHistory_of_exec_proof (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (labels : List (Action σ))
    (events : List (Obs σ)) (config : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, config)) :
    HasEffectHistory program config events :=
  wpf3_effectHistory_of_exec program (WPF3Good program) (wpf3_good_initial program)
    (fun config action events next good isStep =>
      wpf3_good_step program ignoresAttemptContext config good action events next isStep)
    (wpf3_facts_of_good program) labels events config isExec

end DexExpressiveness.DexF
