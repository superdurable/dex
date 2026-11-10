import DexExpressiveness.Failure.Observation
import DexExpressiveness.Dex.RunInvariant

/-!
# WP-F6: one-step frame lemmas of DexF, attempts, retry timers, and commits

Only the failure rules ([ATTEMPT-LOST], [ATTEMPT-EXHAUSTED], and an applied
[RECOVER-INTERRUPT]) put an invocation in Backoff or arm a retry timer. A
Complete rule ends the invocation it commits. `liveness_failure` uses these
to show that every invocation of a fair run eventually ends.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## The attempts frame -/

/-- From `before` to `after`, no attempt entered Backoff and no retry timer
was armed. -/
def wpf6a1_AttemptsFrame (before after : Run σ) : Prop :=
  (∀ stepId attempt, after.findAttempt? stepId = some attempt → attempt.isBackoff = true →
    ∃ original, before.findAttempt? stepId = some original ∧ original.isBackoff = true ∧
      original.number = attempt.number) ∧
  (∀ timer fireTime, after.retryTimers.get? timer = some fireTime →
    before.retryTimers.get? timer = some fireTime)

/-- The attempts frame is reflexive. -/
theorem wpf6a1_AttemptsFrame.refl (run : Run σ) : wpf6a1_AttemptsFrame run run :=
  ⟨fun _ attempt isFound isBackoff => ⟨attempt, isFound, isBackoff, rfl⟩,
    fun _ _ isArmed => isArmed⟩

/-- The attempts frame is transitive. -/
theorem wpf6a1_AttemptsFrame.trans {first second third : Run σ}
    (firstFrame : wpf6a1_AttemptsFrame first second)
    (secondFrame : wpf6a1_AttemptsFrame second third) : wpf6a1_AttemptsFrame first third := by
  refine ⟨fun stepId attempt isFound isBackoff => ?_, fun timer fireTime isArmed => ?_⟩
  · obtain ⟨middle, isMiddle, isMiddleBackoff, middleNumber⟩ :=
      secondFrame.1 stepId attempt isFound isBackoff
    obtain ⟨original, isOriginal, isOriginalBackoff, originalNumber⟩ :=
      firstFrame.1 stepId middle isMiddle isMiddleBackoff
    exact ⟨original, isOriginal, isOriginalBackoff, originalNumber.trans middleNumber⟩
  · exact firstFrame.2 timer fireTime (secondFrame.2 timer fireTime isArmed)

/-- A change that keeps the attempts and the retry timers is in the frame. -/
theorem wpf6a1_attemptsFrame_of_eq {before after : Run σ}
    (sameAttempts : after.attempts = before.attempts)
    (sameTimers : after.retryTimers = before.retryTimers) : wpf6a1_AttemptsFrame before after := by
  refine ⟨fun stepId attempt isFound isBackoff => ⟨attempt, ?_, isBackoff, rfl⟩,
    fun timer fireTime isArmed => ?_⟩
  · unfold Run.findAttempt? at isFound ⊢
    rw [← sameAttempts]
    exact isFound
  · rw [← sameTimers]
    exact isArmed

/-- Setting an attempt to one with the same Backoff flag and number is in
the frame. -/
theorem wpf6a1_attemptsFrame_of_set {before after : Run σ} (stepId : Dex.StepExecId σ)
    (original changed : Attempt σ) (isOriginal : before.findAttempt? stepId = some original)
    (setAttempts : after.attempts = before.attempts.set stepId changed)
    (sameTimers : after.retryTimers = before.retryTimers)
    (sameBackoff : changed.isBackoff = original.isBackoff)
    (sameNumber : changed.number = original.number) : wpf6a1_AttemptsFrame before after := by
  refine ⟨fun otherId attempt isFound isBackoff => ?_, fun timer fireTime isArmed => ?_⟩
  · unfold Run.findAttempt? at isFound isOriginal ⊢
    rw [setAttempts] at isFound
    by_cases isSame : otherId = stepId
    · subst isSame
      rw [AssocList.get?_set_self] at isFound
      cases isFound
      refine ⟨original, isOriginal, ?_, sameNumber.symm⟩
      rw [← sameBackoff]
      exact isBackoff
    · rw [AssocList.get?_set_other _ _ _ _ isSame] at isFound
      exact ⟨attempt, isFound, isBackoff, rfl⟩
  · rw [← sameTimers]
    exact isArmed

/-- What lookup finds is an entry of the map. -/
theorem wpf6a1_mem_of_get? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (isFound : map.get? key = some value) : (key, value) ∈ map := by
  induction map with
  | nil => cases isFound
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    simp only [AssocList.get?] at isFound
    split at isFound
    · rename_i isSame
      cases isFound
      subst isSame
      exact List.mem_cons_self
    · exact List.mem_cons_of_mem _ (inductionHypothesis isFound)

/-- A Backoff attempt that `reconcileAttempts` keeps was there before. -/
theorem wpf6a1_get?_reconcile (attempts : AssocList (Dex.StepExecId σ) (Attempt σ))
    (steps : List (Dex.StepRecord σ)) (stepId : Dex.StepExecId σ) (attempt : Attempt σ)
    (isFound : AssocList.get? (steps.filterMap (fun record =>
      match record.invocation with
      | none => none
      | some invocation =>
        match attempts.get? record.id with
        | some attempt =>
          if attempt.method = invocation.method then some (record.id, attempt)
          else some (record.id, Attempt.fresh invocation.method)
        | none => some (record.id, Attempt.fresh invocation.method))) stepId = some attempt)
    (isBackoff : attempt.isBackoff = true) : attempts.get? stepId = some attempt := by
  obtain ⟨record, _, isEntry⟩ :=
    List.mem_filterMap.1 (wpf6a1_mem_of_get? _ stepId attempt isFound)
  split at isEntry
  · cases isEntry
  · split at isEntry
    · rename_i found hasAttempt
      split at isEntry
      · cases isEntry
        exact hasAttempt
      · cases isEntry
        simp [Attempt.fresh] at isBackoff
    · cases isEntry
      simp [Attempt.fresh] at isBackoff

/-- `reconcileAttempts` is in the frame. -/
theorem wpf6a1_attemptsFrame_reconcile (run : Run σ) :
    wpf6a1_AttemptsFrame run run.reconcileAttempts := by
  refine ⟨fun stepId attempt isFound isBackoff =>
    ⟨attempt, wpf6a1_get?_reconcile run.attempts run.base.steps stepId attempt isFound isBackoff,
      isBackoff, rfl⟩, fun _ _ isArmed => isArmed⟩

/-- [RETRY-FIRE] of one timer is in the frame. -/
theorem wpf6a1_attemptsFrame_fireRetryTimer (run : Run σ) (timer : RetryTimerId σ) :
    wpf6a1_AttemptsFrame run (run.fireRetryTimer timer) := by
  have disarmedFrame :
      wpf6a1_AttemptsFrame run { run with retryTimers := run.retryTimers.erase timer } := by
    refine ⟨fun stepId attempt isFound isBackoff => ⟨attempt, isFound, isBackoff, rfl⟩,
      fun other fireTime isArmed => ?_⟩
    by_cases isSame : other = timer
    · subst isSame
      simp only [AssocList.get?_erase_self] at isArmed
      cases isArmed
    · simp only [AssocList.get?_erase_other _ _ _ isSame] at isArmed
      exact isArmed
  unfold Run.fireRetryTimer
  split
  · dsimp only
    split
    · split
      · refine disarmedFrame.trans ⟨fun stepId found isFound isBackoff => ?_,
          fun _ _ isArmed => isArmed⟩
        unfold Run.findAttempt? Run.setAttempt at isFound
        by_cases isSame : stepId = timer.step
        · subst isSame
          simp only [AssocList.get?_set_self] at isFound
          cases isFound
          cases isBackoff
        · simp only [AssocList.get?_set_other _ _ _ _ isSame] at isFound
          exact ⟨found, isFound, isBackoff, rfl⟩
      · exact disarmedFrame
    · exact disarmedFrame
  · exact wpf6a1_AttemptsFrame.refl run

/-- [TIMER-FIRE] and [RETRY-FIRE] of a list of due timers are in the frame. -/
theorem wpf6a1_attemptsFrame_foldl_fireDueTimer (timers : List (DueTimer σ)) (run : Run σ) :
    wpf6a1_AttemptsFrame run (timers.foldl Run.fireDueTimer run) := by
  induction timers generalizing run with
  | nil => exact wpf6a1_AttemptsFrame.refl run
  | cons timer rest inductionHypothesis =>
    rw [List.foldl_cons]
    refine wpf6a1_AttemptsFrame.trans ?_ (inductionHypothesis _)
    cases timer with
    | wait waitTimer => exact wpf6a1_attemptsFrame_of_eq rfl rfl
    | retry retryTimer => exact wpf6a1_attemptsFrame_fireRetryTimer run retryTimer

/-- A commit whose change is in the frame is in the frame: the commit then
reconciles the attempts. -/
theorem wpf6a1_attemptsFrame_of_reconciled {before changed nextRun : Run σ}
    (hasNextRun : some changed.reconcileAttempts = some nextRun)
    (changeFrame : wpf6a1_AttemptsFrame before changed) :
    wpf6a1_AttemptsFrame before nextRun := by
  cases hasNextRun
  exact changeFrame.trans (wpf6a1_attemptsFrame_reconcile changed)

/-- The `seq` bump of a commit is in the frame. -/
theorem wpf6a1_attemptsFrame_bump (run after : Run σ)
    (frame : wpf6a1_AttemptsFrame { run with base := { run.base with seq := run.base.seq + 1 } }
      after) : wpf6a1_AttemptsFrame run after :=
  (wpf6a1_attemptsFrame_of_eq rfl rfl).trans frame

/-- The conclusion of `wpf6a1_frame_step`. -/
def wpf6a1_FramedFrom (config : Config σ) (nextRun : Run σ) : Prop :=
  (∃ run, config.run = some run ∧ wpf6a1_AttemptsFrame run nextRun) ∨
    ∃ start : Run σ, start.attempts = [] ∧ start.retryTimers = [] ∧
      wpf6a1_AttemptsFrame start nextRun

/-- A step that keeps the run is framed. -/
theorem wpf6a1_framedFrom_of_same (config next : Config σ) (sameRun : next.run = config.run)
    (nextRun : Run σ) (hasNextRun : next.run = some nextRun) :
    wpf6a1_FramedFrom config nextRun :=
  Or.inl ⟨nextRun, sameRun ▸ hasNextRun, wpf6a1_AttemptsFrame.refl nextRun⟩

/-- Outside the failure rules, every step is in the attempts frame. -/
theorem wpf6a1_frame_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next))
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (nextRun : Run σ) (hasNextRun : next.run = some nextRun) :
    wpf6a1_FramedFrom config nextRun := by
  cases action with
  | tick duration =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
      exact wpf6a1_framedFrom_of_same config _ rfl nextRun hasNextRun
    · cases isStep
  | request request =>
    cases request with
    | startFlow requestId stepType input =>
      simp only [step] at isStep
      split at isStep
      · cases isStep
      · split at isStep
        · cases isStep
          exact Or.inr ⟨⟨Dex.Run.empty σ, [], [], requestId⟩, rfl, rfl,
            wpf6a1_attemptsFrame_of_reconciled hasNextRun (wpf6a1_attemptsFrame_of_eq rfl rfl)⟩
        · cases isStep
    | publish chan value =>
      simp only [step] at isStep
      split at isStep
      · split at isStep
        · cases isStep
          exact wpf6a1_framedFrom_of_same config _ rfl nextRun hasNextRun
        · rename_i run isRun
          split at isStep
          · cases isStep
            exact wpf6a1_framedFrom_of_same config _ rfl nextRun hasNextRun
          · split at isStep
            · cases isStep
              exact Or.inl ⟨run, isRun,
                wpf6a1_attemptsFrame_of_reconciled hasNextRun (wpf6a1_attemptsFrame_of_eq rfl rfl)⟩
            · cases isStep
      · cases isStep
  | stepStart stepId isSnapshotReused =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         rename_i _ _ run isRun _ _ _ attempt _ isAttempt _ _ _ _ _
         exact Or.inl ⟨run, isRun, wpf6a1_attemptsFrame_of_reconciled hasNextRun
           (wpf6a1_attemptsFrame_of_set stepId attempt _ isAttempt rfl rfl rfl rfl)⟩)
  | stepComplete taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         rename_i _ _ _ run _ isRun _ _ _ _ _ _ _
         exact Or.inl ⟨run, isRun, wpf6a1_attemptsFrame_of_reconciled hasNextRun
           (wpf6a1_attemptsFrame_of_eq rfl rfl)⟩)
  | timerFire count isWaitTimerFired =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         rename_i _ _ run isRun _
         exact Or.inl ⟨run, isRun, wpf6a1_attemptsFrame_of_reconciled hasNextRun
           (wpf6a1_attemptsFrame_bump run _ (wpf6a1_attemptsFrame_foldl_fireDueTimer _ _))⟩)
  | attemptReturned stepId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         rename_i _ _ run isRun _ _ _ _ _ _ _ _ _ _ _
         exact Or.inl ⟨run, isRun, wpf6a1_attemptsFrame_of_reconciled hasNextRun
           (wpf6a1_attemptsFrame_of_eq rfl rfl)⟩)
  | attemptHeartbeat stepId value =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         rename_i _ _ run isRun _ _ _ attempt _ isAttempt _ _ _ _ _
         exact Or.inl ⟨run, isRun, wpf6a1_attemptsFrame_of_reconciled hasNextRun
           (wpf6a1_attemptsFrame_of_set stepId attempt _ isAttempt rfl rfl rfl rfl)⟩)
  | attemptFailed stepId decision => exact absurd rfl (isNotFailed stepId decision)
  | recoverInterrupt interrupt decision => exact absurd rfl (isNotInterrupt interrupt decision)
  | _ =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep; exact wpf6a1_framedFrom_of_same config _ rfl nextRun hasNextRun)

/-! ## A Complete rule ends its invocation -/

/-- Lookup reads only the Step records. -/
theorem wpf6a1_findStep?_of_steps {first second : Dex.Run σ} (sameSteps : second.steps = first.steps)
    (stepId : Dex.StepExecId σ) : second.findStep? stepId = first.findStep? stepId := by
  unfold Dex.Run.findStep?
  rw [sameSteps]

/-- Each `evalStep` of a list keeps a record that does not wait. -/
theorem wpf6a1_findStep?_foldl_evalStep (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ)
    (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isFound : run.findStep? stepId = some record) (isNotWaiting : record.phase ≠ .waiting) :
    (steps.foldl Dex.Run.evalStep run).findStep? stepId = some record := by
  induction steps generalizing run with
  | nil => exact isFound
  | cons head rest inductionHypothesis =>
    rw [List.foldl_cons]
    apply inductionHypothesis
    by_cases isSame : head = stepId
    · subst isSame
      rw [Dex.Run.evalStep_of_not_waiting run head record isFound isNotWaiting]
      exact isFound
    · rw [Dex.Run.findStep?_evalStep_other run head stepId (Ne.symm isSame)]
      exact isFound

/-- `applyEffects` keeps a record that does not wait. -/
theorem wpf6a1_findStep?_applyEffects (run : Dex.Run σ) (effects : Dex.Effects σ)
    (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isFound : run.findStep? stepId = some record) (isNotWaiting : record.phase ≠ .waiting) :
    (run.applyEffects effects).findStep? stepId = some record := by
  unfold Dex.Run.applyEffects Dex.Run.wake
  apply wpf6a1_findStep?_foldl_evalStep _ _ stepId record _ isNotWaiting
  rw [wpf6a1_findStep?_of_steps ((Dex.Run.applyPublications_frame _ _).1.trans
    (Dex.Run.applyWrites_frame _ _).1)]
  exact isFound

/-- The record of the Step exists and has no invocation. -/
def wpf6a1_HasNoInvocation (run : Dex.Run σ) (stepId : Dex.StepExecId σ) : Prop :=
  ∃ record, run.findStep? stepId = some record ∧ record.invocation = none

/-- `finishStep` of the Step clears its invocation. -/
theorem wpf6a1_finishStep_self (run : Dex.Run σ) (stepId : Dex.StepExecId σ)
    (finalPhase : Dex.Phase) (record : Dex.StepRecord σ)
    (isFound : run.findStep? stepId = some record) :
    wpf6a1_HasNoInvocation (run.finishStep stepId finalPhase) stepId :=
  ⟨_, Dex.Run.findStep?_updateStep_self _ stepId _ record isFound rfl, rfl⟩

/-- `finishStep` of any Step keeps a record without invocation. -/
theorem wpf6a1_finishStep_keeps (run : Dex.Run σ) (stepId other : Dex.StepExecId σ)
    (finalPhase : Dex.Phase) (hasNone : wpf6a1_HasNoInvocation run stepId) :
    wpf6a1_HasNoInvocation (run.finishStep other finalPhase) stepId := by
  by_cases isSame : other = stepId
  · subst isSame
    obtain ⟨record, isFound, _⟩ := hasNone
    exact wpf6a1_finishStep_self run other finalPhase record isFound
  · obtain ⟨record, isFound, noInvocation⟩ := hasNone
    refine ⟨record, ?_, noInvocation⟩
    unfold Dex.Run.finishStep
    rw [Dex.Run.findStep?_updateStep_other _ other stepId _ ?_ (Ne.symm isSame)]
    · exact isFound
    · intro candidate
      rfl

/-- The cancels of a close keep a record without invocation. -/
theorem wpf6a1_foldl_finishStep_keeps (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ)
    (stepId : Dex.StepExecId σ) (hasNone : wpf6a1_HasNoInvocation run stepId) :
    wpf6a1_HasNoInvocation
      (steps.foldl (fun current step => current.finishStep step .canceled) run) stepId := by
  induction steps generalizing run with
  | nil => exact hasNone
  | cons head rest inductionHypothesis =>
    rw [List.foldl_cons]
    exact inductionHypothesis _ (wpf6a1_finishStep_keeps run stepId head .canceled hasNone)

/-- `closeRun` keeps a record without invocation. -/
theorem wpf6a1_closeRun_keeps (run : Dex.Run σ) (status : Dex.CloseStatus)
    (stepId : Dex.StepExecId σ) (hasNone : wpf6a1_HasNoInvocation run stepId) :
    wpf6a1_HasNoInvocation (run.closeRun status) stepId := by
  unfold Dex.Run.closeRun
  split
  · exact hasNone
  · exact wpf6a1_foldl_finishStep_keeps _ run stepId hasNone

/-- `collectOutput` keeps a record without invocation. -/
theorem wpf6a1_collectOutput_keeps (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (output : Option σ.Val) (stepId : Dex.StepExecId σ)
    (hasNone : wpf6a1_HasNoInvocation run stepId) :
    wpf6a1_HasNoInvocation (run.collectOutput step output) stepId := by
  unfold Dex.Run.collectOutput
  split
  · exact hasNone
  · exact hasNone

/-- `newStep` appends a record, so it keeps a record without invocation. -/
theorem wpf6a1_newStep_keeps (run : Dex.Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : Dex.StepOptions σ) (origin : Dex.Origin σ) (stepId : Dex.StepExecId σ)
    (hasNone : wpf6a1_HasNoInvocation run stepId) :
    wpf6a1_HasNoInvocation (run.newStep stepType input options origin) stepId := by
  obtain ⟨record, isFound, noInvocation⟩ := hasNone
  refine ⟨record, ?_, noInvocation⟩
  unfold Dex.Run.findStep? at isFound ⊢
  unfold Dex.Run.newStep
  simp only [List.find?_append, isFound, Option.some_or]

/-- The moves of a decision keep a record without invocation. -/
theorem wpf6a1_foldl_newStep_keeps (moves : List (Dex.Move σ)) (origin : Dex.Origin σ)
    (run : Dex.Run σ) (stepId : Dex.StepExecId σ) (hasNone : wpf6a1_HasNoInvocation run stepId) :
    wpf6a1_HasNoInvocation (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run) stepId := by
  induction moves generalizing run with
  | nil => exact hasNone
  | cons head rest inductionHypothesis =>
    rw [List.foldl_cons]
    exact inductionHypothesis _ (wpf6a1_newStep_keeps run _ _ _ origin stepId hasNone)

/-- `applyDecision` leaves its Step without invocation. -/
theorem wpf6a1_applyDecision_hasNoInvocation (run : Dex.Run σ) (stepId : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (record : Dex.StepRecord σ)
    (isFound : run.findStep? stepId = some record) :
    wpf6a1_HasNoInvocation (run.applyDecision stepId decision) stepId := by
  have finished := wpf6a1_finishStep_self run stepId .completed record isFound
  unfold Dex.Run.applyDecision
  dsimp only
  split
  · exact wpf6a1_closeRun_keeps _ _ stepId (wpf6a1_collectOutput_keeps _ _ _ stepId finished)
  · exact wpf6a1_closeRun_keeps _ _ stepId finished
  · exact finished
  · exact wpf6a1_foldl_newStep_keeps _ _ _ stepId finished

/-- The record of the Step exists and has no invocation or a pending Execute. -/
def wpf6a1_HasNoWaitFor (run : Dex.Run σ) (stepId : Dex.StepExecId σ) : Prop :=
  ∃ record, run.findStep? stepId = some record ∧
    (record.invocation = none ∨ record.invocation = some { method := .execute, state := .pending })

/-- `evalStep` of a Step without invocation leaves none or a pending Execute. -/
theorem wpf6a1_evalStep_hasNoWaitFor (run : Dex.Run σ) (stepId : Dex.StepExecId σ)
    (record : Dex.StepRecord σ) (isFound : run.findStep? stepId = some record)
    (noInvocation : record.invocation = none) :
    wpf6a1_HasNoWaitFor (run.evalStep stepId) stepId := by
  rcases Dex.Run.evalStep_cases run stepId with
    ⟨isSame, _⟩ | ⟨found, waitState, consumeFlags, isFoundRun, _, _, isEval⟩
  · rw [isSame]
    exact ⟨record, isFound, Or.inl noInvocation⟩
  · rw [isEval]
    refine ⟨_, Dex.Run.findStep?_updateStep_self _ stepId _ found ?_ rfl, Or.inr rfl⟩
    rw [wpf6a1_findStep?_of_steps (Dex.Run.consumeMessages_steps _ _ _)]
    exact isFoundRun

/-- `registerWait` of a Step without invocation leaves none or a pending Execute. -/
theorem wpf6a1_registerWait_hasNoWaitFor (run : Dex.Run σ) (stepId : Dex.StepExecId σ)
    (wait : Dex.Wait σ) (now : Nat) (record : Dex.StepRecord σ)
    (isFound : run.findStep? stepId = some record) (noInvocation : record.invocation = none) :
    wpf6a1_HasNoWaitFor (run.registerWait stepId wait now) stepId := by
  unfold Dex.Run.registerWait
  split
  · exact ⟨_, Dex.Run.findStep?_updateStep_self run stepId _ record isFound rfl, Or.inr rfl⟩
  · dsimp only
    exact wpf6a1_evalStep_hasNoWaitFor _ stepId _
      (Dex.Run.findStep?_updateStep_self _ stepId _ record isFound rfl) noInvocation

/-- The fence gives the Step record running the task. -/
theorem wpf6a1_record_of_fence (config : Config σ) (run : Run σ) (task : Task σ)
    (isRun : config.run = some run) (isFence : config.isFencePassed task = true) :
    ∃ record, run.base.findStep? task.base.step = some record ∧
      record.invocation = some { method := task.base.method, state := .running task.base.id } := by
  unfold Config.isFencePassed at isFence
  rw [isRun] at isFence
  simp only at isFence
  split at isFence
  · rename_i record _ isRecord _
    simp only [Bool.and_eq_true, decide_eq_true_eq] at isFence
    exact ⟨record, isRecord, isFence.1⟩
  · cases isFence

/-- After a Complete rule, the record of its Step has no invocation of the
task's method. -/
theorem wpf6a1_stepComplete_record (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config (.stepComplete taskId) = some (events, next))
    (task : Task σ) (isFound : config.findRealTask? taskId = some task) :
    ∃ nextRun, next.run = some nextRun ∧ ∃ record,
      nextRun.base.findStep? task.base.step = some record ∧
      ∀ state, record.invocation ≠ some { method := task.base.method, state := state } := by
  simp only [step] at isStep
  split at isStep
  · split at isStep
    · rename_i run foundTask isRun isFoundTask
      rw [isFound] at isFoundTask
      cases isFoundTask
      split at isStep
      · rename_i isFence
        obtain ⟨record, isRecord, isRunning⟩ := wpf6a1_record_of_fence config run task isRun isFence
        have isNotWaiting : record.phase ≠ .waiting := by
          have phaseFact := (invariant.runInvariant run isRun).invocationMatchesPhase record
            (Dex.Run.mem_of_findStep? _ _ _ isRecord).1
          rw [isRunning] at phaseFact
          simp only at phaseFact
          intro isWaiting
          rw [isWaiting] at phaseFact
          split at phaseFact <;> cases phaseFact
        have isUpdated :
            (({ run.base with seq := run.base.seq + 1 } : Dex.Run σ).updateStep task.base.step
              (fun record => { record with invocation := none })).findStep? task.base.step =
              some { record with invocation := none } :=
          Dex.Run.findStep?_updateStep_self _ _ _ record isRecord rfl
        split at isStep
        · rename_i response isMethod _
          cases isStep
          obtain ⟨found, isFoundNext, invocationCases⟩ :=
            wpf6a1_registerWait_hasNoWaitFor _ task.base.step response.wait config.clock _
              (wpf6a1_findStep?_applyEffects _ response.effects _ _ isUpdated isNotWaiting) rfl
          refine ⟨_, rfl, found, isFoundNext, fun state isCurrent => ?_⟩
          rcases invocationCases with noInvocation | isExecute
          · rw [noInvocation] at isCurrent
            cases isCurrent
          · rw [isExecute, isMethod] at isCurrent
            cases isCurrent
        · rename_i response _ _
          cases isStep
          obtain ⟨found, isFoundNext, noInvocation⟩ :=
            wpf6a1_applyDecision_hasNoInvocation _ task.base.step response.decision _
              (wpf6a1_findStep?_applyEffects _ response.effects _ _ isUpdated isNotWaiting)
          refine ⟨_, rfl, found, isFoundNext, fun state isCurrent => ?_⟩
          rw [noInvocation] at isCurrent
          cases isCurrent
        · cases isStep
      · cases isStep
    · cases isStep
  · cases isStep

/-- Outside the failure rules, an attempt in Backoff after a step was in
Backoff before, with the same attempt number. -/
theorem wpf6_backoff_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (nextRun : Run σ) (hasNextRun : next.run = some nextRun) (stepId : Dex.StepExecId σ)
    (nextAttempt : Attempt σ) (isNextAttempt : nextRun.findAttempt? stepId = some nextAttempt)
    (isBackoff : nextAttempt.isBackoff = true) :
    ∃ run attempt, config.run = some run ∧ run.findAttempt? stepId = some attempt ∧
      attempt.isBackoff = true ∧ attempt.number = nextAttempt.number := by
  rcases wpf6a1_frame_step program config action events next isStep isNotFailed isNotInterrupt
      nextRun hasNextRun with ⟨run, isRun, frame⟩ | ⟨start, noAttempts, _, frame⟩
  · obtain ⟨original, isOriginal, isOriginalBackoff, sameNumber⟩ :=
      frame.1 stepId nextAttempt isNextAttempt isBackoff
    exact ⟨run, original, isRun, isOriginal, isOriginalBackoff, sameNumber⟩
  · obtain ⟨_, isOriginal, _, _⟩ := frame.1 stepId nextAttempt isNextAttempt isBackoff
    unfold Run.findAttempt? at isOriginal
    rw [noAttempts] at isOriginal
    cases isOriginal

/-- Outside the failure rules, a retry timer armed after a step was armed
before, with the same fire time. -/
theorem wpf6_retryTimer_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (nextRun : Run σ) (hasNextRun : next.run = some nextRun) (timer : RetryTimerId σ)
    (fireTime : Nat) (isArmed : nextRun.retryTimers.get? timer = some fireTime) :
    ∃ run, config.run = some run ∧ run.retryTimers.get? timer = some fireTime := by
  rcases wpf6a1_frame_step program config action events next isStep isNotFailed isNotInterrupt
      nextRun hasNextRun with ⟨run, isRun, frame⟩ | ⟨start, _, noTimers, frame⟩
  · exact ⟨run, isRun, frame.2 timer fireTime isArmed⟩
  · have isOriginal := frame.2 timer fireTime isArmed
    rw [noTimers] at isOriginal
    cases isOriginal

/-- A Complete rule ends the invocation it commits: after it, the
invocation's history is not live. -/
theorem wpf6_stepComplete_ends (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config (.stepComplete taskId) = some (events, next))
    (nextInvariant : Invariant program next) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) (history : InvocationHistory σ)
    (isHistory : next.ghost.findInvocation? task.key = some history) :
    history.outcome ≠ .live := by
  intro isLive
  obtain ⟨nextRun, hasNextRun, record, isRecord, isNotCurrent⟩ :=
    wpf6a1_stepComplete_record program config invariant taskId events next isStep task isFound
  have nextRunInvariant := nextInvariant.runInvariant nextRun hasNextRun
  obtain ⟨current, state, isMember, isId, isCurrent⟩ :=
    nextRunInvariant.liveIsCurrent task.key history isHistory isLive
  have isFoundCurrent :=
    Dex.Run.findStep?_of_mem_nodup nextRun.base current isMember nextRunInvariant.stepIdsNodup
  rw [isId] at isFoundCurrent
  have sameRecord : some record = some current := isRecord.symm.trans isFoundCurrent
  cases sameRecord
  exact isNotCurrent state isCurrent

end DexExpressiveness.DexF
