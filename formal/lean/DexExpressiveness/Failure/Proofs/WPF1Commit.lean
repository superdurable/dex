import DexExpressiveness.Failure.Proofs.WPF1Abstract

/-!
# WP-F1: α of a normal commit

A normal commit takes `seq`, applies a change of the failure-free run, and
reconciles the attempt states and the ghosts. `abstract_commitRun` says that
α of the result is the same change applied to α of the run before, with
`seq` taken, when the change commutes with the relabeling. The invariant
gives the side conditions: invocations written by the change are pending
under the view, and `ζ` keeps the order of the waiters.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-! ## Small list facts -/

theorem mem_of_pairwise {α : Type} (relation : α → α → Prop) (elements : List α)
    (isPairwise : elements.Pairwise relation) (first second : α) (isFirst : first ∈ elements)
    (isSecond : second ∈ elements) :
    first = second ∨ relation first second ∨ relation second first := by
  induction elements with
  | nil => cases isFirst
  | cons head rest inductionHypothesis =>
    rw [List.pairwise_cons] at isPairwise
    rcases List.mem_cons.mp isFirst with isHead | inRest <;>
      rcases List.mem_cons.mp isSecond with isSecondHead | inSecondRest
    · exact Or.inl (isHead.trans isSecondHead.symm)
    · subst isHead
      exact Or.inr (Or.inl (isPairwise.1 second inSecondRest))
    · subst isSecondHead
      exact Or.inr (Or.inr (isPairwise.1 first inRest))
    · exact inductionHypothesis isPairwise.2 inRest inSecondRest

theorem mem_of_get? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ) (value : ν)
    (isFound : AssocList.get? map key = some value) : (key, value) ∈ map := by
  induction map with
  | nil => cases isFound
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    simp only [AssocList.get?] at isFound
    by_cases isKey : entryKey = key
    · rw [ite_eq_left isKey] at isFound
      cases isFound
      subst isKey
      exact List.mem_cons_self ..
    · rw [ite_eq_right isKey] at isFound
      exact List.mem_cons_of_mem _ (inductionHypothesis isFound)

theorem exists_mem_of_get?_isSome {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (isSome : (AssocList.get? map key).isSome = true) : ∃ value, (key, value) ∈ map := by
  cases isFound : AssocList.get? map key with
  | none => rw [isFound] at isSome; cases isSome
  | some value => exact ⟨value, mem_of_get? map key value isFound⟩

/-- `ζ` keeps the order on its domain: its values are `1, …, n` in the order
of its strictly increasing keys. -/
theorem normalIndex_order (index : AssocList Nat Nat) (count : Nat)
    (values : index.map (fun entry => entry.2) = List.range' 1 count)
    (keysIncrease : (index.map (fun entry => entry.1)).Pairwise (· < ·))
    (first second : Nat) (hasFirst : (AssocList.get? index first).isSome = true)
    (hasSecond : (AssocList.get? index second).isSome = true) :
    decide ((AssocList.get? index first).getD 0 ≤ (AssocList.get? index second).getD 0) =
      decide (first ≤ second) := by
  have valuesIncrease : (index.map (fun entry => entry.2)).Pairwise (· < ·) := by
    rw [values]
    exact List.pairwise_lt_range'
  have isPairwise : index.Pairwise (fun a b => a.1 < b.1 ∧ a.2 < b.2) :=
    (List.pairwise_map.mp keysIncrease).and (List.pairwise_map.mp valuesIncrease)
  cases isFirst : AssocList.get? index first with
  | none => rw [isFirst] at hasFirst; cases hasFirst
  | some firstValue =>
    cases isSecond : AssocList.get? index second with
    | none => rw [isSecond] at hasSecond; cases hasSecond
    | some secondValue =>
      simp only [Option.getD_some]
      rcases mem_of_pairwise _ index isPairwise _ _ (mem_of_get? index first firstValue isFirst)
          (mem_of_get? index second secondValue isSecond) with isSame | isBefore | isAfter
      · simp only [Prod.mk.injEq] at isSame
        rw [isSame.1, isSame.2]
        simp
      · simp only at isBefore
        have : firstValue ≤ secondValue := Nat.le_of_lt isBefore.2
        have : first ≤ second := Nat.le_of_lt isBefore.1
        simp [*]
      · simp only at isAfter
        have : ¬ firstValue ≤ secondValue := Nat.not_le.mpr isAfter.2
        have : ¬ first ≤ second := Nat.not_le.mpr isAfter.1
        simp [*]

/-! ## The commit -/

/-- The run with `seq` taken, before the change of a commit. -/
def bumpedRun (run : Run σ) : Run σ :=
  { run with base := { run.base with seq := run.base.seq + 1 } }

/-- The ghost after a normal commit is counted. -/
def countedGhost (ghost : Ghost σ) (seq clock : Nat) : Ghost σ :=
  { ghost with
    normalCount := ghost.normalCount + 1
    normalIndex := ghost.normalIndex.set seq (ghost.normalCount + 1)
    lastNormalCommitAt := some clock }

theorem commitRun_normal (config : Config σ) (run : Run σ) (completed : Option (InvocationKey σ))
    (inputEvents : List (Obs σ)) (change : Run σ → Run σ) :
    config.commitRun run true completed inputEvents change =
      (inputEvents ++ Config.closeEvents run.base (change (bumpedRun run)).base,
        { config with
          run := some (change (bumpedRun run)).reconcileAttempts
          lastCommitAt := some config.clock
          ghost := (countedGhost config.ghost (run.base.seq + 1) config.clock).reconcile completed
            (change (bumpedRun run)).base }) := rfl

/-- The view and the relabeling of a normal commit from `config` on `run`. -/
def commitView (config : Config σ) (run : Run σ) : InvocationView σ :=
  attemptView run config.ghost

/-- `ζ` after a normal commit on `run`. -/
def commitRelabel (config : Config σ) (run : Run σ) : Nat → Nat :=
  (countedGhost config.ghost (run.base.seq + 1) config.clock).normalIndexOf

section FromInvariant

variable {program : Program σ} {config : Config σ} {run : Run σ}

theorem attempt_of_record (runInvariant : RunInvariant config run) (record : Dex.StepRecord σ)
    (isMember : record ∈ run.base.steps) (invocation : Dex.Invocation)
    (hasInvocation : record.invocation = some invocation) :
    ∃ attempt history, run.findAttempt? record.id = some attempt ∧
      config.ghost.findInvocation? (record.id, invocation.method) = some history ∧
      history.outcome = .live ∧ attempt.method = invocation.method := by
  have isMatch := runInvariant.attemptsMatch record isMember
  unfold AttemptMatchesRecord at isMatch
  rw [hasInvocation] at isMatch
  obtain ⟨attempt, history, isAttempt, isHistory, isLive, isMethod, _⟩ := isMatch
  exact ⟨attempt, history, isAttempt, isHistory, isLive, isMethod⟩

theorem attempt_none_of_record (runInvariant : RunInvariant config run)
    (record : Dex.StepRecord σ) (isMember : record ∈ run.base.steps)
    (hasNone : record.invocation = none) : run.findAttempt? record.id = none := by
  have isMatch := runInvariant.attemptsMatch record isMember
  unfold AttemptMatchesRecord at isMatch
  rw [hasNone] at isMatch
  exact isMatch

theorem methodsMatch (runInvariant : RunInvariant config run) (record : Dex.StepRecord σ)
    (isMember : record ∈ run.base.steps) (invocation : Dex.Invocation)
    (hasInvocation : record.invocation = some invocation) (attempt : Attempt σ)
    (isAttempt : run.findAttempt? record.id = some attempt) : attempt.method = invocation.method := by
  obtain ⟨found, _, isFound, _, _, isMethod⟩ :=
    attempt_of_record runInvariant record isMember invocation hasInvocation
  rw [isAttempt] at isFound
  cases isFound
  exact isMethod

theorem abstract_eq_commitView (runInvariant : RunInvariant config run) :
    Run.abstract config.ghost run =
      { relabelRun (commitView config run) config.ghost.normalIndexOf run.base with
        seq := config.ghost.normalCount } := by
  apply abstract_eq_relabelRun
  intro record isMember invocation hasInvocation
  exact abstractInvocation_eq_attemptView config.ghost run record invocation hasInvocation
    (methodsMatch runInvariant record isMember invocation hasInvocation)

theorem waitSeq_le (runInvariant : RunInvariant config run) (record : Dex.StepRecord σ)
    (isMember : record ∈ run.base.steps) (waitState : Dex.WaitState σ)
    (hasWait : record.waitState = some waitState) :
    (AssocList.get? config.ghost.normalIndex waitState.registeredSeq).isSome = true ∧
      waitState.registeredSeq ≤ run.base.seq := by
  have isIndexed := runInvariant.waitSeqsIndexed record waitState isMember hasWait
  refine ⟨isIndexed, ?_⟩
  obtain ⟨value, isEntry⟩ := exists_mem_of_get?_isSome _ _ isIndexed
  exact runInvariant.normalIndexKeysTaken _ isEntry

theorem commitRelabel_of_waitSeq (runInvariant : RunInvariant config run)
    (record : Dex.StepRecord σ) (isMember : record ∈ run.base.steps)
    (waitState : Dex.WaitState σ) (hasWait : record.waitState = some waitState) :
    commitRelabel config run waitState.registeredSeq =
      config.ghost.normalIndexOf waitState.registeredSeq := by
  have isBelow := (waitSeq_le runInvariant record isMember waitState hasWait).2
  unfold commitRelabel Ghost.normalIndexOf countedGhost
  dsimp only
  rw [AssocList.get?_set_other _ _ _ _ (by omega)]

theorem commitRelabel_seq : commitRelabel config run (run.base.seq + 1) =
    config.ghost.normalCount + 1 := by
  unfold commitRelabel Ghost.normalIndexOf countedGhost
  dsimp only
  rw [AssocList.get?_set_self]
  rfl

theorem relabel_bumpedRun (runInvariant : RunInvariant config run) :
    relabelRun (commitView config run) (commitRelabel config run) (bumpedRun run).base =
      { Run.abstract config.ghost run with seq := config.ghost.normalCount + 1 } := by
  rw [abstract_eq_commitView runInvariant]
  simp only [relabelRun, bumpedRun]
  rw [commitRelabel_seq]
  congr 1
  apply List.map_congr_left
  intro record isMember
  simp only [relabelRecord]
  congr 1
  cases hasWait : record.waitState with
  | none => rfl
  | some waitState =>
    simp only [Option.map_some, commitRelabel_of_waitSeq runInvariant record isMember waitState
      hasWait]

theorem phase_of_invocation (runInvariant : RunInvariant config run) (record : Dex.StepRecord σ)
    (isMember : record ∈ run.base.steps) (invocation : Dex.Invocation)
    (hasInvocation : record.invocation = some invocation) :
    record.phase = (match invocation.method with
      | .waitFor => Dex.Phase.waitFor
      | .execute => Dex.Phase.execute) := by
  have isMatch := runInvariant.invocationMatchesPhase record isMember
  rw [hasInvocation] at isMatch
  exact isMatch

theorem invocation_none_of_waiting (runInvariant : RunInvariant config run)
    (record : Dex.StepRecord σ) (isMember : record ∈ run.base.steps)
    (isWaiting : record.phase = .waiting) : record.invocation = none := by
  cases hasInvocation : record.invocation with
  | none => rfl
  | some invocation =>
    have isPhase := phase_of_invocation runInvariant record isMember invocation hasInvocation
    rw [isWaiting] at isPhase
    cases isMethod : invocation.method <;> simp [isMethod] at isPhase

theorem isWaitFresh_bumpedRun (runInvariant : RunInvariant config run) :
    IsWaitFresh (commitView config run) (bumpedRun run).base := by
  intro record isMember isWaiting
  have hasNone := invocation_none_of_waiting runInvariant record isMember isWaiting
  unfold commitView attemptView
  rw [attempt_none_of_record runInvariant record isMember hasNone]

theorem findAttempt?_none_of_counter (runInvariant : RunInvariant config run)
    (stepType : σ.StepType) (number : Nat)
    (isAbove : (run.base.counters.get? stepType).getD 0 < number) :
    run.findAttempt? { stepType := stepType, number := number } = none := by
  cases isFound : run.findAttempt? { stepType := stepType, number := number } with
  | none => rfl
  | some attempt =>
    have isEntry := mem_of_get? run.attempts _ attempt isFound
    obtain ⟨record, isMember, isId, _⟩ := runInvariant.attemptsHaveInvocations _ isEntry
    have isCounted := runInvariant.stepNumbersCounted record isMember
    simp only at isId
    rw [isId] at isCounted
    simp only at isCounted
    omega

theorem isCounterFresh_bumpedRun (runInvariant : RunInvariant config run) :
    IsCounterFresh (commitView config run) (bumpedRun run).base := by
  intro stepType number method isAbove
  unfold commitView attemptView
  rw [findAttempt?_none_of_counter runInvariant stepType number isAbove]

theorem isOrderKept_bumpedRun (runInvariant : RunInvariant config run) :
    IsOrderKept (commitRelabel config run) (bumpedRun run).base := by
  intro first isFirst second isSecond firstWait secondWait _ _ firstHas secondHas
  rw [commitRelabel_of_waitSeq runInvariant first isFirst firstWait firstHas,
    commitRelabel_of_waitSeq runInvariant second isSecond secondWait secondHas]
  exact normalIndex_order config.ghost.normalIndex config.ghost.normalCount
    runInvariant.normalIndexValues runInvariant.normalIndexKeysIncrease _ _
    (waitSeq_le runInvariant first isFirst firstWait firstHas).1
    (waitSeq_le runInvariant second isSecond secondWait secondHas).1

end FromInvariant

/-- The attempt projection that the view reads. -/
def attemptShape (attempt : Attempt σ) : Dex.Method × Bool :=
  (attempt.method, attempt.snapshot.isSome)

/-- α of a normal commit is the change applied to α with `seq` taken, when
the change commutes with the relabeling. -/
theorem abstract_commitRun (config : Config σ) (run : Run σ)
    (runInvariant : RunInvariant config run) (completed : Option (InvocationKey σ))
    (change : Run σ → Run σ) (baseChange : Dex.Run σ → Dex.Run σ)
    (isBase : (change (bumpedRun run)).base = baseChange (bumpedRun run).base)
    (keepsAttempts : ∀ step, ((change (bumpedRun run)).findAttempt? step).map attemptShape =
      (run.findAttempt? step).map attemptShape)
    (commutes : baseChange (relabelRun (commitView config run) (commitRelabel config run)
        (bumpedRun run).base) =
      relabelRun (commitView config run) (commitRelabel config run)
        (baseChange (bumpedRun run).base))
    (keepsSeq : (baseChange (bumpedRun run).base).seq = run.base.seq + 1)
    (idsNodup : ((baseChange (bumpedRun run).base).steps.map (fun record => record.id)).Nodup) :
    Run.abstract ((countedGhost config.ghost (run.base.seq + 1) config.clock).reconcile completed
        (change (bumpedRun run)).base) (change (bumpedRun run)).reconcileAttempts =
      baseChange { Run.abstract config.ghost run with seq := config.ghost.normalCount + 1 } := by
  rw [abstract_eq_relabelRun _ _ (attemptView (change (bumpedRun run))
    ((countedGhost config.ghost (run.base.seq + 1) config.clock).reconcile completed
      (change (bumpedRun run)).base))]
  · have sameView : attemptView (change (bumpedRun run))
        ((countedGhost config.ghost (run.base.seq + 1) config.clock).reconcile completed
          (change (bumpedRun run)).base) = commitView config run := by
      apply attemptView_congr
      · intro step
        have := keepsAttempts step
        unfold attemptShape at this
        exact this
      · intro key
        rw [argumentsOf_reconcile]
        rfl
    rw [sameView, ← relabel_bumpedRun runInvariant, commutes]
    simp only [Run.reconcileAttempts, isBase]
    show { relabelRun (commitView config run) (commitRelabel config run)
        (baseChange (bumpedRun run).base) with seq := config.ghost.normalCount + 1 } = _
    simp only [relabelRun, keepsSeq, commitRelabel_seq]
  · intro record isMember invocation hasInvocation
    exact abstractInvocation_reconcileAttempts (change (bumpedRun run)) _ record isMember
      (by rw [isBase]; exact idsNodup) invocation hasInvocation

/-! ## Events and tasks of a commit -/

theorem eraseStepEvents_closeEvents (config : Config σ) (inputEvents : List (Obs σ))
    (before after : Dex.Run σ) (before' after' : Dex.Run σ)
    (sameBefore : before'.status = before.status) (sameAfter : after'.status = after.status)
    (sameOutputs : after'.outputs = after.outputs) :
    eraseStepEvents config (inputEvents ++ Config.closeEvents before after) =
      eraseStepEvents config inputEvents ++ Dex.Config.closeEvents before' after' := by
  unfold eraseStepEvents
  rw [List.filterMap_append]
  congr 1
  unfold Config.closeEvents Dex.Config.closeEvents
  rw [sameBefore, sameAfter, sameOutputs]
  cases before.status <;> cases after.status <;> rfl

/-- The virtual tasks stay when the commit only appends fresh histories and
keeps the real tasks. -/
theorem virtualTasks_of_extra (config next : Config σ)
    (sameReal : next.realTasks = config.realTasks)
    (extra : AssocList (InvocationKey σ) (InvocationHistory σ))
    (isInvocations : next.ghost.invocations = config.ghost.invocations ++ extra)
    (extraNoArguments : ∀ entry, entry ∈ extra → entry.2.arguments = none) :
    next.virtualTasks = config.virtualTasks := by
  rw [virtualTasks_eq, virtualTasks_eq, isInvocations, List.filterMap_append]
  have extraNone : extra.filterMap (virtualTaskOf next) = [] := by
    apply List.filterMap_eq_nil_iff.mpr
    intro entry isMember
    simp [virtualTaskOf, extraNoArguments entry isMember]
  have sameTask : ∀ entry, virtualTaskOf next entry = virtualTaskOf config entry := by
    intro entry
    unfold virtualTaskOf Config.hasVirtualTask
    rw [sameReal]
  rw [extraNone, List.append_nil, show virtualTaskOf next = virtualTaskOf config from funext sameTask]

/-! ## Records that stay current -/

/-- No record of the Step waits. -/
def NonWaitingAt (step : Dex.StepExecId σ) (run : Dex.Run σ) : Prop :=
  ∀ record, record ∈ run.steps → record.id = step → record.phase ≠ .waiting

/-- Every record of the Step stays, unchanged. -/
def KeepsRecordsAt (step : Dex.StepExecId σ) (run after : Dex.Run σ) : Prop :=
  ∀ record, record ∈ run.steps → record.id = step → record ∈ after.steps

theorem keepsRecordsAt_trans {step : Dex.StepExecId σ} {first second third : Dex.Run σ}
    (firstKeeps : KeepsRecordsAt step first second) (secondKeeps : KeepsRecordsAt step second third) :
    KeepsRecordsAt step first third :=
  fun record isMember isStep => secondKeeps record (firstKeeps record isMember isStep) isStep

theorem keeps_updateStep_other (run : Dex.Run σ) (step updated : Dex.StepExecId σ)
    (change : Dex.StepRecord σ → Dex.StepRecord σ)
    (keepsId : ∀ record, (change record).id = record.id) (isOther : updated ≠ step)
    (nonWaiting : NonWaitingAt step run) :
    NonWaitingAt step (run.updateStep updated change) ∧
      KeepsRecordsAt step run (run.updateStep updated change) := by
  constructor
  · intro record isMember isStep
    obtain ⟨original, isOriginal, isRecord⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMember
    by_cases isUpdated : original.id = updated
    · rw [ite_eq_left isUpdated] at isRecord
      rw [isRecord, keepsId] at isStep
      exact absurd (isUpdated.symm.trans isStep) isOther
    · rw [ite_eq_right isUpdated] at isRecord
      rw [isRecord] at isStep ⊢
      exact nonWaiting original isOriginal isStep
  · intro record isMember isStep
    apply (Dex.Run.mem_updateStep _ _ _ _).mpr
    refine ⟨record, isMember, ?_⟩
    rw [ite_eq_right (by rw [isStep]; exact Ne.symm isOther)]

theorem keeps_evalStep (run : Dex.Run σ) (evaluated step : Dex.StepExecId σ)
    (nonWaiting : NonWaitingAt step run) :
    NonWaitingAt step (run.evalStep evaluated) ∧ KeepsRecordsAt step run (run.evalStep evaluated) := by
  rcases Dex.Run.evalStep_cases run evaluated with ⟨isSame, _⟩ |
    ⟨record, waitState, consumeFlags, isFound, isWaiting, _, isEval⟩
  · rw [isSame]
    exact ⟨nonWaiting, fun _ isMember _ => isMember⟩
  · have isOther : evaluated ≠ step := by
      intro isStep
      obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? run evaluated record isFound
      exact nonWaiting record isMember (isId.trans isStep) isWaiting
    rw [isEval]
    have sameSteps : (Dex.Run.consumeMessages waitState.wait.channels consumeFlags
        (run.disarmTimersOf evaluated)).1.steps = run.steps := by
      rw [(Dex.Run.consumeMessages_frame _ _ _).1]
      rfl
    have nonWaitingConsumed : NonWaitingAt step (Dex.Run.consumeMessages waitState.wait.channels
        consumeFlags (run.disarmTimersOf evaluated)).1 := by
      intro other isMember isStep
      rw [sameSteps] at isMember
      exact nonWaiting other isMember isStep
    obtain ⟨isNonWaiting, isKept⟩ := keeps_updateStep_other _ step evaluated
      (fun current =>
        { current with
          conditionResults := some
            { timersDone := waitState.timersDone
              channelMessages := (Dex.Run.consumeMessages waitState.wait.channels consumeFlags
                (run.disarmTimersOf evaluated)).2 }
          phase := .execute
          invocation := some { method := .execute, state := .pending } })
      (fun _ => rfl) isOther nonWaitingConsumed
    refine ⟨isNonWaiting, ?_⟩
    intro other isMember isStep
    apply isKept other _ isStep
    rw [sameSteps]
    exact isMember

theorem keeps_foldl_evalStep (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ)
    (step : Dex.StepExecId σ) (nonWaiting : NonWaitingAt step run) :
    NonWaitingAt step (steps.foldl Dex.Run.evalStep run) ∧
      KeepsRecordsAt step run (steps.foldl Dex.Run.evalStep run) := by
  induction steps generalizing run with
  | nil => exact ⟨nonWaiting, fun _ isMember _ => isMember⟩
  | cons evaluated laterSteps inductionHypothesis =>
    obtain ⟨firstNonWaiting, firstKeeps⟩ := keeps_evalStep run evaluated step nonWaiting
    obtain ⟨laterNonWaiting, laterKeeps⟩ := inductionHypothesis _ firstNonWaiting
    exact ⟨laterNonWaiting, keepsRecordsAt_trans firstKeeps laterKeeps⟩

theorem keeps_fireTimer (run : Dex.Run σ) (timer : Dex.TimerId σ) (step : Dex.StepExecId σ)
    (nonWaiting : NonWaitingAt step run) :
    NonWaitingAt step (run.fireTimer timer) ∧ KeepsRecordsAt step run (run.fireTimer timer) := by
  unfold Dex.Run.fireTimer
  split
  · dsimp only
    have disarmedNonWaiting : NonWaitingAt step
        ({ run with timers := run.timers.erase timer } : Dex.Run σ) := nonWaiting
    split
    · exact ⟨disarmedNonWaiting, fun _ isMember _ => isMember⟩
    · rename_i record isFound
      split
      · rename_i waitState isWaiting hasWait
        split
        · exact ⟨disarmedNonWaiting, fun _ isMember _ => isMember⟩
        · have isOther : timer.step ≠ step := by
            intro isStep
            obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? _ timer.step record isFound
            exact disarmedNonWaiting record isMember (isId.trans isStep) isWaiting
          obtain ⟨updatedNonWaiting, updatedKeeps⟩ := keeps_updateStep_other _ step timer.step
            (fun current =>
              { current with
                waitState := some
                  { waitState with timersDone := waitState.timersDone.set timer.index true } })
            (fun _ => rfl) isOther disarmedNonWaiting
          obtain ⟨evaluatedNonWaiting, evaluatedKeeps⟩ :=
            keeps_evalStep _ timer.step step updatedNonWaiting
          exact ⟨evaluatedNonWaiting, keepsRecordsAt_trans updatedKeeps evaluatedKeeps⟩
      · exact ⟨disarmedNonWaiting, fun _ isMember _ => isMember⟩
  · exact ⟨nonWaiting, fun _ isMember _ => isMember⟩

theorem keeps_foldl_fireTimer (timers : List (Dex.TimerId σ)) (run : Dex.Run σ)
    (step : Dex.StepExecId σ) (nonWaiting : NonWaitingAt step run) :
    NonWaitingAt step (timers.foldl Dex.Run.fireTimer run) ∧
      KeepsRecordsAt step run (timers.foldl Dex.Run.fireTimer run) := by
  induction timers generalizing run with
  | nil => exact ⟨nonWaiting, fun _ isMember _ => isMember⟩
  | cons timer laterTimers inductionHypothesis =>
    obtain ⟨firstNonWaiting, firstKeeps⟩ := keeps_fireTimer run timer step nonWaiting
    obtain ⟨laterNonWaiting, laterKeeps⟩ := inductionHypothesis _ firstNonWaiting
    exact ⟨laterNonWaiting, keepsRecordsAt_trans firstKeeps laterKeeps⟩

/-- Every record of a current invocation is not waiting, so it stays. -/
theorem nonWaitingAt_of_invocation {config : Config σ} {run : Run σ}
    (runInvariant : RunInvariant config run) (record : Dex.StepRecord σ)
    (isMember : record ∈ run.base.steps) (invocation : Dex.Invocation)
    (hasInvocation : record.invocation = some invocation) :
    NonWaitingAt record.id run.base := by
  intro other isOther isId isWaiting
  have isSame : other = record :=
    eq_of_nodup_map (fun candidate => candidate.id) run.base.steps runInvariant.stepIdsNodup
      isOther isMember isId
  subst isSame
  have isPhase := phase_of_invocation runInvariant other isMember invocation hasInvocation
  rw [isWaiting] at isPhase
  cases isMethod : invocation.method <;> simp [isMethod] at isPhase

end DexExpressiveness.DexF.WPF1
