import DexExpressiveness.Failure.Statements.Leaf

/-!
# WP-F6: facts about α for the liveness proof

What the Dex configuration `abstractConfig config` says about the DexF
configuration `config`: its initial value, closedness, clock, pending
invocations, virtual tasks, and wait timers; and that a closed Dex run stays
closed.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Helper lemmas -/

/-- Two members with the same image are equal when the images are distinct. -/
theorem wpf6c_eq_of_nodup_map {α β : Type} (image : α → β) (elements : List α)
    (imagesNodup : (elements.map image).Nodup) {first second : α}
    (isFirstMember : first ∈ elements) (isSecondMember : second ∈ elements)
    (sameImage : image first = image second) : first = second := by
  induction elements with
  | nil => cases isFirstMember
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, List.nodup_cons, List.mem_map, not_exists, not_and] at imagesNodup
    rcases List.mem_cons.mp isFirstMember with isHead | isInRest <;>
      rcases List.mem_cons.mp isSecondMember with isOtherHead | isOtherInRest
    · rw [isHead, isOtherHead]
    · subst isHead
      exact absurd sameImage.symm (imagesNodup.1 second isOtherInRest)
    · subst isOtherHead
      exact absurd sameImage (imagesNodup.1 first isInRest)
    · exact inductionHypothesis imagesNodup.2 isInRest isOtherInRest

/-- A member of a list with distinct images is what `find?` finds by its image. -/
theorem wpf6c_find?_of_mem_nodup {α β : Type} [DecidableEq β] (image : α → β)
    (elements : List α) (imagesNodup : (elements.map image).Nodup) (element : α)
    (isMember : element ∈ elements) :
    elements.find? (fun candidate => decide (image candidate = image element)) = some element := by
  cases isFound : elements.find? (fun candidate => decide (image candidate = image element)) with
  | none =>
    have isAbsent := List.find?_eq_none.mp isFound element isMember
    simp at isAbsent
  | some found =>
    have isFoundMember := List.mem_of_find?_eq_some isFound
    have hasImage := List.find?_some isFound
    simp only [decide_eq_true_eq] at hasImage
    rw [wpf6c_eq_of_nodup_map image elements imagesNodup isFoundMember isMember hasImage]

/-- A member of an association list with distinct keys is what lookup finds. -/
theorem wpf6c_get?_of_mem_nodup {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (keysNodup : (map.map (fun entry => entry.1)).Nodup) (entry : κ × ν) (isMember : entry ∈ map) :
    map.get? entry.1 = some entry.2 := by
  induction map with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨key, value⟩ := head
    simp only [List.map_cons, List.nodup_cons, List.mem_map, not_exists, not_and] at keysNodup
    rcases List.mem_cons.mp isMember with isHead | isInRest
    · subst isHead
      simp [AssocList.get?]
    · have isOther : key ≠ entry.1 := fun isSame =>
        keysNodup.1 entry isInRest isSame.symm
      simp only [AssocList.get?, isOther, ↓reduceIte]
      exact inductionHypothesis keysNodup.2 isInRest

/-- Insertion keeps the old elements and adds only the new one. -/
theorem wpf6c_mem_of_mem_insertSorted {α : Type} (isAtMost : α → α → Bool) (newElement element : α)
    (elements : List α) (isMember : element ∈ insertSorted isAtMost newElement elements) :
    element = newElement ∨ element ∈ elements := by
  induction elements with
  | nil =>
    simp only [insertSorted, List.mem_singleton] at isMember
    exact Or.inl isMember
  | cons head rest inductionHypothesis =>
    simp only [insertSorted] at isMember
    split at isMember
    · exact List.mem_cons.mp isMember
    · rcases List.mem_cons.mp isMember with isHead | isInRest
      · exact Or.inr (isHead ▸ List.mem_cons_self)
      · rcases inductionHypothesis isInRest with isNew | isOld
        · exact Or.inl isNew
        · exact Or.inr (List.mem_cons_of_mem head isOld)

/-- Sorting adds no element. -/
theorem wpf6c_mem_of_mem_sortStable {α : Type} (isAtMost : α → α → Bool) (element : α)
    (elements : List α) (isMember : element ∈ sortStable isAtMost elements) :
    element ∈ elements := by
  induction elements with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    rcases wpf6c_mem_of_mem_insertSorted isAtMost head element _ isMember with isHead | isInRest
    · exact isHead ▸ List.mem_cons_self
    · exact List.mem_cons_of_mem head (inductionHypothesis isInRest)

/-- A Step found in the abstract run is the abstraction of a Step of the run. -/
theorem wpf6c_findStep?_abstract (ghost : Ghost σ) (run : Run σ) (stepId : Dex.StepExecId σ)
    (dexRecord : Dex.StepRecord σ) (isFound : (run.abstract ghost).findStep? stepId = some dexRecord) :
    ∃ record, run.base.findStep? stepId = some record ∧
      dexRecord.invocation = run.abstractInvocation ghost record := by
  simp only [Dex.Run.findStep?, Run.abstract, List.find?_map] at isFound
  cases isBaseFound : run.base.steps.find? (fun record => decide (record.id = stepId)) with
  | none =>
    have isNone : run.base.steps.find? ((fun record : Dex.StepRecord σ => decide (record.id = stepId)) ∘
        (fun record : Dex.StepRecord σ =>
          { record with
            invocation := run.abstractInvocation ghost record
            waitState := record.waitState.map (fun waitState =>
              { waitState with registeredSeq := ghost.normalIndexOf waitState.registeredSeq }) })) =
        none := by
      rw [← isBaseFound]
      rfl
    rw [isNone] at isFound
    cases isFound
  | some record =>
    have isSome : run.base.steps.find? ((fun record : Dex.StepRecord σ => decide (record.id = stepId)) ∘
        (fun record : Dex.StepRecord σ =>
          { record with
            invocation := run.abstractInvocation ghost record
            waitState := record.waitState.map (fun waitState =>
              { waitState with registeredSeq := ghost.normalIndexOf waitState.registeredSeq }) })) =
        some record := by
      rw [← isBaseFound]
      rfl
    rw [isSome] at isFound
    cases isFound
    exact ⟨record, isBaseFound, rfl⟩

/-- Consuming messages keeps the status. -/
theorem wpf6c_status_consumeMessages (chans : List σ.Chan) :
    ∀ (consumeFlags : List Bool) (run : Dex.Run σ),
      (Dex.Run.consumeMessages chans consumeFlags run).1.status = run.status := by
  induction chans with
  | nil => intro consumeFlags run; rfl
  | cons chan laterChans inductionHypothesis =>
    intro consumeFlags run
    simp only [Dex.Run.consumeMessages]
    split
    · exact inductionHypothesis _ _
    · exact inductionHypothesis _ _

/-- `eval` keeps the status. -/
theorem wpf6c_status_evalStep (run : Dex.Run σ) (step : Dex.StepExecId σ) :
    (run.evalStep step).status = run.status := by
  unfold Dex.Run.evalStep
  split
  · rfl
  · split
    · split
      · rfl
      · exact wpf6c_status_consumeMessages _ _ _
    · rfl

/-- Evaluating Steps keeps the status. -/
theorem wpf6c_status_foldl_evalStep (steps : List (Dex.StepExecId σ)) :
    ∀ run : Dex.Run σ, (steps.foldl Dex.Run.evalStep run).status = run.status := by
  induction steps with
  | nil => intro run; rfl
  | cons step laterSteps inductionHypothesis =>
    intro run
    simp only [List.foldl_cons]
    rw [inductionHypothesis, wpf6c_status_evalStep]

/-- Writes keep the status. -/
theorem wpf6c_status_applyWrites (writes : List (σ.Key × Option σ.Val)) :
    ∀ run : Dex.Run σ, (run.applyWrites writes).status = run.status := by
  induction writes with
  | nil => intro run; rfl
  | cons write laterWrites inductionHypothesis =>
    intro run
    simp only [Dex.Run.applyWrites, List.foldl_cons] at inductionHypothesis ⊢
    rw [inductionHypothesis]
    split <;> rfl

/-- Publications keep the status. -/
theorem wpf6c_status_applyPublications (publications : List (σ.Chan × σ.Val)) :
    ∀ run : Dex.Run σ, (run.applyPublications publications).status = run.status := by
  induction publications with
  | nil => intro run; rfl
  | cons publication laterPublications inductionHypothesis =>
    intro run
    simp only [Dex.Run.applyPublications, List.foldl_cons] at inductionHypothesis ⊢
    rw [inductionHypothesis]
    rfl

/-- `effects` keeps the status. -/
theorem wpf6c_status_applyEffects (run : Dex.Run σ) (effects : Dex.Effects σ) :
    (run.applyEffects effects).status = run.status := by
  unfold Dex.Run.applyEffects Dex.Run.wake
  rw [wpf6c_status_foldl_evalStep, wpf6c_status_applyPublications, wpf6c_status_applyWrites]

/-- `register` keeps the status. -/
theorem wpf6c_status_registerWait (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ)
    (now : Nat) : (run.registerWait step wait now).status = run.status := by
  unfold Dex.Run.registerWait
  split
  · rfl
  · exact wpf6c_status_evalStep _ step

/-- Firing timers keeps the status. -/
theorem wpf6c_status_foldl_fireTimer (timers : List (Dex.TimerId σ)) :
    ∀ run : Dex.Run σ, (timers.foldl Dex.Run.fireTimer run).status = run.status := by
  induction timers with
  | nil => intro run; rfl
  | cons timer laterTimers inductionHypothesis =>
    intro run
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    unfold Dex.Run.fireTimer
    split
    · simp only
      split
      · rfl
      · split
        · split
          · rfl
          · exact wpf6c_status_evalStep _ timer.step
        · rfl
    · rfl

/-- Taking moves keeps the status. -/
theorem wpf6c_status_foldl_newStep (moves : List (Dex.Move σ)) (origin : Dex.Origin σ) :
    ∀ run : Dex.Run σ, (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run).status = run.status := by
  induction moves with
  | nil => intro run; rfl
  | cons move laterMoves inductionHypothesis =>
    intro run
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    rfl

/-- Closing a closed run changes nothing. -/
theorem wpf6c_closeRun_of_closed (run : Dex.Run σ) (newStatus closeStatus : Dex.CloseStatus)
    (isClosed : run.status = .closed closeStatus) : run.closeRun newStatus = run := by
  unfold Dex.Run.closeRun
  split
  · rfl
  · rename_i isRunning
    rw [isClosed] at isRunning
    cases isRunning

/-- A decision keeps a closed status. -/
theorem wpf6c_status_applyDecision_of_closed (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (closeStatus : Dex.CloseStatus)
    (isClosed : run.status = .closed closeStatus) :
    (run.applyDecision step decision).status = .closed closeStatus := by
  have isFinishedClosed : (run.finishStep step .completed).status = .closed closeStatus := isClosed
  unfold Dex.Run.applyDecision
  split
  · rename_i output _
    have isCollectedClosed :
        ((run.finishStep step .completed).collectOutput step output).status = .closed closeStatus := by
      unfold Dex.Run.collectOutput
      split <;> exact isFinishedClosed
    rw [wpf6c_closeRun_of_closed _ _ closeStatus isCollectedClosed]
    exact isCollectedClosed
  · rw [wpf6c_closeRun_of_closed _ _ closeStatus isFinishedClosed]
    exact isFinishedClosed
  · exact isFinishedClosed
  · rw [wpf6c_status_foldl_newStep]
    exact isFinishedClosed

/-- A Dex step from a closed run leaves a run with the same closed status. -/
theorem wpf6c_dex_step_keeps_closed (flow : Dex.FlowDef σ) (config : Dex.Config σ)
    (action : Dex.Action σ) (events : List (Dex.Obs σ)) (next : Dex.Config σ)
    (isStep : Dex.step flow config action = some (events, next)) (run : Dex.Run σ)
    (closeStatus : Dex.CloseStatus) (isRun : config.run = some run)
    (isClosed : run.status = .closed closeStatus) :
    ∃ nextRun, next.run = some nextRun ∧ nextRun.status = .closed closeStatus := by
  cases action with
  | tick duration =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
      exact ⟨run, isRun, isClosed⟩
    · cases isStep
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [Dex.step, isRun] at isStep
      cases isStep
    | publish chan value =>
      simp only [Dex.step, isRun, isClosed] at isStep
      cases isStep
      exact ⟨run, isRun, isClosed⟩
  | stepStart stepId =>
    simp only [Dex.step, isRun] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep
        · cases isStep
          exact ⟨_, rfl, isClosed⟩
        · cases isStep
      · cases isStep
  | workerRun taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨run, isRun, isClosed⟩
      · cases isStep
  | workerEffect taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨run, isRun, isClosed⟩
      · cases isStep
  | workerReturn taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        exact ⟨run, isRun, isClosed⟩
      · cases isStep
  | workerAbandon taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
      · cases isStep
        exact ⟨run, isRun, isClosed⟩
  | stepComplete taskId =>
    cases isTask : config.findTask? taskId with
    | none =>
      simp only [Dex.step, isRun, isTask] at isStep
      cases isStep
    | some task =>
      simp only [Dex.step, isRun, isTask] at isStep
      split at isStep
      · split at isStep
        · cases isStep
          exact ⟨_, rfl, by
            rw [wpf6c_status_registerWait, wpf6c_status_applyEffects]
            exact isClosed⟩
        · cases isStep
          exact ⟨_, rfl, wpf6c_status_applyDecision_of_closed _ _ _ closeStatus (by
            rw [wpf6c_status_applyEffects]
            exact isClosed)⟩
        · cases isStep
      · cases isStep
  | stepCompleteStale taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep
        · cases isStep
        · cases isStep
          exact ⟨run, isRun, isClosed⟩
      · cases isStep
  | timerFire count =>
    simp only [Dex.step, isRun] at isStep
    split at isStep
    · cases isStep
      exact ⟨_, rfl, by
        rw [wpf6c_status_foldl_fireTimer]
        exact isClosed⟩
    · cases isStep

/-! ## Facts about α -/

/-- α maps the initial DexF configuration to the initial Dex configuration. -/
theorem wpf6_abstractConfig_initial : abstractConfig (Config.initial σ) = Dex.Config.initial σ :=
  rfl

/-- α keeps whether the program is closed. -/
theorem wpf6_isClosed_abstract (program : Program σ) (config : Config σ) :
    (Dex.system program.failureFreeFlow).isClosed (abstractConfig config) =
      (system program).isClosed config := by
  simp only [Dex.system, system, abstractConfig]
  cases config.run with
  | none => rfl
  | some run => rfl

/-- A real task found by its ID is exactly a real task with that ID, since
real task IDs are distinct. -/
theorem wpf6_findRealTask?_iff (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (task : Task σ) :
    config.findRealTask? taskId = some task ↔ task ∈ config.realTasks ∧ task.base.id = taskId := by
  constructor
  · intro isFound
    refine ⟨List.mem_of_find?_eq_some isFound, ?_⟩
    have hasId := List.find?_some isFound
    simpa using hasId
  · rintro ⟨isMember, rfl⟩
    exact wpf6c_find?_of_mem_nodup (fun candidate => candidate.base.id) config.realTasks
      invariant.realTaskIdsNodup task isMember

/-- A pending invocation under α is a pending invocation of the DexF run, and
its history is live. -/
theorem wpf6_pending_of_abstract (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ) (method : Dex.Method)
    (dexRun : Dex.Run σ) (dexRecord : Dex.StepRecord σ)
    (hasRun : (abstractConfig config).run = some dexRun)
    (isFound : dexRun.findStep? stepId = some dexRecord)
    (isPending : dexRecord.invocation = some { method := method, state := .pending }) :
    ∃ run record history, config.run = some run ∧ run.base.findStep? stepId = some record ∧
      record.invocation = some { method := method, state := .pending } ∧
      config.ghost.findInvocation? (stepId, method) = some history ∧ history.outcome = .live := by
  simp only [abstractConfig] at hasRun
  cases isRun : config.run with
  | none =>
    rw [isRun] at hasRun
    cases hasRun
  | some run =>
    rw [isRun] at hasRun
    cases hasRun
    obtain ⟨record, isBaseFound, isAbstract⟩ :=
      wpf6c_findStep?_abstract config.ghost run stepId dexRecord isFound
    have isMember := List.mem_of_find?_eq_some isBaseFound
    have hasId := List.find?_some isBaseFound
    simp only [decide_eq_true_eq] at hasId
    have isMatch := (invariant.runInvariant run isRun).attemptsMatch record isMember
    rw [isPending] at isAbstract
    unfold Run.abstractInvocation at isAbstract
    unfold AttemptMatchesRecord at isMatch
    cases hasInvocation : record.invocation with
    | none =>
      rw [hasInvocation] at isAbstract
      cases isAbstract
    | some invocation =>
      rw [hasInvocation] at isAbstract isMatch
      obtain ⟨attempt, history, hasAttempt, hasHistory, isLive, _, _, isRunningSnapshot,
        snapshotMatches, _⟩ := isMatch
      simp only [hasAttempt, Ghost.argumentsOf, hasHistory, Option.bind_some] at isAbstract
      obtain ⟨invocationMethod, invocationState⟩ := invocation
      have isMethod : invocationMethod = method := by
        split at isAbstract <;> simp only [Option.some.injEq, Dex.Invocation.mk.injEq] at isAbstract <;>
          exact isAbstract.1.symm
      subst isMethod
      cases invocationState with
      | pending =>
        rw [← hasId]
        exact ⟨run, record, history, rfl, hasId ▸ isBaseFound, hasInvocation, hasHistory, isLive⟩
      | running taskId =>
        have hasSnapshot := (isRunningSnapshot taskId rfl).2
        cases isSnapshot : attempt.snapshot with
        | none => rw [isSnapshot] at hasSnapshot; cases hasSnapshot
        | some snapshot =>
          rw [isSnapshot] at snapshotMatches isAbstract
          cases hasArguments : history.arguments with
          | none => rw [hasArguments] at snapshotMatches; cases snapshotMatches
          | some arguments =>
            rw [hasArguments] at isAbstract
            simp at isAbstract

/-- A task under α is the virtual task of a ghost history that α keeps. -/
theorem wpf6_virtualTask_of_abstract (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (virtualTaskId : Nat) (dexTask : Dex.Task σ)
    (isFound : (abstractConfig config).findTask? virtualTaskId = some dexTask) :
    ∃ key history arguments, config.ghost.findInvocation? key = some history ∧
      history.arguments = some arguments ∧ arguments.virtualTaskId = virtualTaskId ∧
      config.hasVirtualTask key history = true := by
  have isMember := List.mem_of_find?_eq_some isFound
  have hasId := List.find?_some isFound
  simp only [decide_eq_true_eq] at hasId
  simp only [abstractConfig, Config.virtualTasks] at isMember
  have isInFiltered := wpf6c_mem_of_mem_sortStable _ dexTask _ isMember
  obtain ⟨entry, isEntryMember, isMapped⟩ := List.mem_filterMap.mp isInFiltered
  have isLookup := wpf6c_get?_of_mem_nodup config.ghost.invocations invariant.ghostKeysNodup entry
    isEntryMember
  split at isMapped
  · rename_i arguments hasArguments
    split at isMapped
    · rename_i hasTask
      cases isMapped
      exact ⟨entry.1, entry.2, arguments, isLookup, hasArguments, hasId, hasTask⟩
    · cases isMapped
  · cases isMapped

/-- α keeps the wait timers. -/
theorem wpf6_timers_abstract (config : Config σ) (dexRun : Dex.Run σ)
    (hasRun : (abstractConfig config).run = some dexRun) :
    ∃ run, config.run = some run ∧ dexRun.timers = run.base.timers := by
  simp only [abstractConfig] at hasRun
  cases isRun : config.run with
  | none =>
    rw [isRun] at hasRun
    cases hasRun
  | some run =>
    rw [isRun] at hasRun
    cases hasRun
    exact ⟨run, rfl, rfl⟩

/-- A Dex step keeps a closed program closed. -/
theorem wpf6_dex_isClosed_step (flow : Dex.FlowDef σ) (config : Dex.Config σ)
    (action : Dex.Action σ) (events : List (Dex.Obs σ)) (next : Dex.Config σ)
    (isStep : Dex.step flow config action = some (events, next))
    (isClosed : (Dex.system flow).isClosed config = true) :
    (Dex.system flow).isClosed next = true := by
  simp only [Dex.system] at isClosed ⊢
  cases isRun : config.run with
  | none =>
    rw [isRun] at isClosed
    cases isClosed
  | some run =>
    rw [isRun] at isClosed
    cases isStatus : run.status with
    | running =>
      simp only [isStatus] at isClosed
      cases isClosed
    | closed closeStatus =>
      obtain ⟨nextRun, isNextRun, isNextClosed⟩ :=
        wpf6c_dex_step_keeps_closed flow config action events next isStep run closeStatus isRun
          isStatus
      rw [isNextRun]
      simp only [isNextClosed]

end DexExpressiveness.DexF
