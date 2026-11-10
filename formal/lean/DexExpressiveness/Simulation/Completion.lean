import DexExpressiveness.Simulation.RelationLemmas
import DexExpressiveness.Simulation.Completion.Statements
import DexExpressiveness.Simulation.Completion.IntentsBroadcast

/-!
# L to Dex: the completion of an action

Work package 3b. The three completion lemmas of
`Simulation/LToDexComplete.lean` are proved here:

| Lemma of `LToDexComplete.lean` | Proved here as |
| --- | --- |
| `l_thread_boot_simulated` | `wp3b_boot_simulated` |
| `l_thread_complete_simulated` | `wp3b_complete_simulated` |
| `l_thread_resume_simulated` | `wp3b_resume_thread_simulated` |

Each proof runs the same explicit action sequence, `completionActions`
(`Simulation/Completion/Exec.lean`): the Worker steps that are still missing
to return the handler of the thread's task, then one
[STEP-COMPLETE-EXECUTE]. It is the sequence `simulatingActions` names
(`simulatingActions_complete`).

The files under `Simulation/Completion/` hold the parts. The lemmas shared
with the other step simulations are in `Simulation/Frame.lean`.

| File | Content |
| --- | --- |
| `Exec.lean` | The action sequence and the configuration it ends in. |
| `Mid.lean` | `MidThreads`, `MidData`: the relation while the Execute commits; the frame lemma for `wake`. |
| `Start.lean` | From `IsSimulationState` to the relation after `S[e].inv := ⊥`. |
| `Wake.lean`, `Fire.lean` | `effects` on the Dex side: publications and `wake`. |
| `Data.lean` | The data part across `effects`; decoding of condition results. |
| `Effects.lean`, `EffectsCancel.lean` | `effects` of each atomic L action. |
| `Decision.lean`, `Decide.lean` | `decide`: finish the Step, create the Steps of the moves. |
| `Assemble.lean` | `Completing`; back to `Related`. |
| `Intents.lean`, `IntentsSync.lean`, `IntentsBroadcast.lean` | One lemma per row of the mapping table (spec §8.3). |
| `BroadcastL.lean`, `Broadcast.lean` | `broadcast`: the L fold in closed form, and the batch `wake`. |
| `Close.lean` | `exit` and `fail`. |
| `Statements.lean` | Facts about `completeStatement`, `resumeBlocked`, `resumeTimed`. |
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The L step with events `lEvents` that ends in `lNext` is simulated by the
completion actions of `task`. -/
def IsSimulatedByCompletion (program : Program) (dexConfig : Dex.Config signature)
    (task : Dex.Task signature) (lEvents : List Event) (lNext : L.Config) : Prop :=
  ∃ dexEvents dexNext,
    (Dex.system ⟦program⟧).exec dexConfig (completionActions task) = some (dexEvents, dexNext) ∧
    dexEvents.filterMap project = lEvents ∧
    Related program lNext dexNext

/-- A WaitFor task has no effect left to emit. -/
theorem wp3b_waitForQuiet {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig) :
    WaitForTasksHaveNoEffects program dexConfig :=
  fun task isTask isWaitFor =>
    pendingEffects_eq_nil_of_waitFor program task (state.dexInvariant.tasksComputed task isTask)
      isWaitFor

/-- The completion actions are the simulating actions of a thread that
completes an action. -/
theorem Completing.toActions {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature} {thread : L.Thread}
    {record : Dex.StepRecord signature} {task : Dex.Task signature}
    (start : Completing program lConfig dexConfig run thread record task)
    (state : IsSimulationState program lConfig dexConfig)
    (isFound : lConfig.findThread? thread.core.tid = some thread)
    (isCompleting : thread.phase = .bootRunning ∨ (∃ loaded, thread.phase = .running loaded) ∨
      thread.phase = .emitted ∨ thread.phase = .waitStarted ∨
      ∃ outcome, thread.phase = .resuming outcome)
    (lEvents : List Event) (lNext : L.Config)
    (isSimulated : IsSimulatedByCompletion program dexConfig task lEvents lNext) :
    IsSimulatedByActions program lConfig dexConfig (.thread thread.core.tid) lEvents lNext := by
  obtain ⟨dexEvents, dexNext, isExec, sameEvents, isRelated⟩ := isSimulated
  have isActions := simulatingActions_complete
    (state.isRelated.running run start.hasRun start.isRunning) start.hasRun state.tasksDistinct
    isFound record start.isRecord start.isActive
    (threadOfInput_of_mem state.lInvariant start.isThread start.carries) .execute task start.isTask start.hasInvocation isCompleting
  exact ⟨dexEvents, dexNext, by rw [isActions]; exact isExec, sameEvents, isRelated⟩

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}
  {run : Dex.Run signature} {thread : L.Thread} {record : Dex.StepRecord signature}
  {task : Dex.Task signature} {result : StatementResult}

/-! ## From a row of the mapping table to the simulation -/

/-- A row that does not close the run: the completion actions simulate the
L step. -/
theorem wp3b_simulated_of_goal
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (isResponse : executeResponse program task = encodeIntent task.snapshot result)
    (isNotExit : ∀ value, result.intent ≠ .exit value)
    (isNotFail : ∀ message, result.intent ≠ .fail message)
    (goal : IntentGoal program lConfig dexConfig run thread record task result) :
    IsSimulatedByCompletion program dexConfig task
      (lConfig.applyIntent thread.core.tid result).1
      (lConfig.applyIntent thread.core.tid result).2.stamp := by
  have isExec := start.start.exec start.state
  rw [isResponse] at isExec
  refine ⟨_, _, isExec, ?_, goal⟩
  rw [wp3b_closeEvents_running _ _ (wp3b_completedRun_status run record.id _
      start.start.isRunning (wp3b_encodeIntent_close task.snapshot result isNotExit isNotFail)),
    wp3b_applyIntent_events lConfig _ result isNotExit isNotFail]
  rfl

/-- `exit` and `fail`: the completion actions simulate the L step that ends
the program. -/
theorem wp3b_simulated_close
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (isResponse : executeResponse program task = encodeIntent task.snapshot result)
    (waitForQuiet : WaitForTasksHaveNoEffects program dexConfig)
    (isClose : (∃ value, result.intent = .exit value) ∨ ∃ message, result.intent = .fail message) :
    IsSimulatedByCompletion program dexConfig task
      (lConfig.applyIntent thread.core.tid result).1
      (lConfig.applyIntent thread.core.tid result).2.stamp := by
  have isExec := start.start.exec start.state
  rw [isResponse] at isExec
  have related := start.state.isRelated.running run start.start.hasRun start.start.isRunning
  have noOutputs : ((releasedRun run record.id).finishStep record.id .completed).outputs = [] :=
    related.outputsEmpty
  rcases isClose with ⟨value, intentCase⟩ | ⟨message, intentCase⟩
  · have isEncoded : encodeIntent task.snapshot result =
        closeDecision (.forceComplete (some (DexVal.exitTag value))) := by
      simp [encodeIntent, intentCase]
    have isRun : completedRun run record.id
        (closeDecision (.forceComplete (some (DexVal.exitTag value)))) =
        (((releasedRun run record.id).finishStep record.id .completed).collectOutput record.id
          (some (DexVal.exitTag value))).closeRun .completed := by
      rw [wp3b_completedRun_eq]
      simp only [closeDecision]
      rw [applyEffects_empty]
      rfl
    have finishedRunning :
        (((releasedRun run record.id).finishStep record.id .completed).collectOutput record.id
          (some (DexVal.exitTag value))).status = .running := start.start.isRunning
    obtain ⟨closedStatus, closedOutputs⟩ := wp3b_closeRun_running _ .completed finishedRunning
    have isNext : lConfig.applyIntent thread.core.tid result =
        ([Event.exit value], lConfig.closeProgram) := by
      simp [L.Config.applyIntent, intentCase]
    rw [isEncoded, isRun] at isExec
    rw [isNext]
    refine ⟨_, _, isExec, ?_, ?_⟩
    · have isOutputs :
          (((releasedRun run record.id).finishStep record.id .completed).collectOutput record.id
            (some (DexVal.exitTag value))).outputs = [(record.id, DexVal.exitTag value)] := by
        show ((releasedRun run record.id).finishStep record.id .completed).outputs ++
          [(record.id, DexVal.exitTag value)] = _
        rw [noOutputs]
        rfl
      simp [Dex.Config.closeEvents, start.start.isRunning, closedStatus, closedOutputs, isOutputs,
        project]
    · exact wp3b_close_related program lConfig dexConfig start.state run thread record task
        start.start waitForQuiet _ .completed rfl rfl finishedRunning
  · have isEncoded : encodeIntent task.snapshot result = closeDecision (.forceFail message) := by
      simp [encodeIntent, intentCase]
    have isRun : completedRun run record.id (closeDecision (.forceFail message)) =
        ((releasedRun run record.id).finishStep record.id .completed).closeRun
          (.failed message) := by
      rw [wp3b_completedRun_eq]
      simp only [closeDecision]
      rw [applyEffects_empty]
      rfl
    have finishedRunning :
        ((releasedRun run record.id).finishStep record.id .completed).status = .running :=
      start.start.isRunning
    obtain ⟨closedStatus, _⟩ := wp3b_closeRun_running _ (.failed message) finishedRunning
    have isNext : lConfig.applyIntent thread.core.tid result =
        ([Event.fail message], lConfig.closeProgram) := by
      simp [L.Config.applyIntent, intentCase]
    rw [isEncoded, isRun] at isExec
    rw [isNext]
    refine ⟨_, _, isExec, ?_, ?_⟩
    · simp [Dex.Config.closeEvents, start.start.isRunning, closedStatus, project]
    · exact wp3b_close_related program lConfig dexConfig start.state run thread record task
        start.start waitForQuiet _ (.failed message) rfl rfl finishedRunning

/-! ## The Execute response of the task of a thread -/

/-- The Execute of a statement that does not block and emits nothing. -/
theorem wp3b_response_running (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (task : Dex.Task signature) (loaded : Val)
    (hasStep : task.step = record.id) (hasInput : task.input = record.input)
    (hasType : record.id.stepType = StepType.point thread.code)
    (recordInput : record.input = some (DexVal.thread thread.core))
    (isNotBlocking : isBlockingHead thread.code = false)
    (isNotEmit : ∀ effect, beginKind thread.core thread.code ≠ .emit effect)
    (isLoaded : ∀ source, beginKind thread.core thread.code = .load source →
      readShared task.snapshot source = loaded) :
    executeResponse program task =
      encodeIntent task.snapshot (completeStatement program thread.core thread.code loaded) := by
  unfold executeResponse
  rw [hasStep, hasType, hasInput, recordInput]
  simp only [translate, pointStepDef, isNotBlocking, Bool.false_eq_true, ↓reduceIte]
  cases beginCase : beginKind thread.core thread.code with
  | plain =>
    simp only
    rw [wp3b_completeStatement_loaded program thread.core thread.code loaded .null
      (by simp [beginCase])]
  | load source =>
    simp only
    rw [isLoaded source beginCase]
  | emit effect => exact absurd beginCase (isNotEmit effect)

/-- The Execute of an `emit` after its effect was delivered. -/
theorem wp3b_response_emitted (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (task : Dex.Task signature) (effect : Val)
    (hasStep : task.step = record.id) (hasInput : task.input = record.input)
    (hasType : record.id.stepType = StepType.point thread.code)
    (recordInput : record.input = some (DexVal.thread thread.core))
    (isNotBlocking : isBlockingHead thread.code = false)
    (isEmit : beginKind thread.core thread.code = .emit effect) :
    executeResponse program task =
      encodeIntent task.snapshot (completeStatement program thread.core thread.code .null) := by
  unfold executeResponse
  rw [hasStep, hasType, hasInput, recordInput]
  simp only [translate, pointStepDef, isNotBlocking, Bool.false_eq_true, ↓reduceIte, isEmit]

/-- The Execute of the continuation of a blocking statement. -/
theorem wp3b_response_resumeBlocked (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (task : Dex.Task signature)
    (hasStep : task.step = record.id) (hasInput : task.input = record.input)
    (hasType : record.id.stepType = StepType.point thread.code)
    (recordInput : record.input = some (DexVal.thread thread.core))
    (isBlocking : isBlockingHead thread.code = true) :
    executeResponse program task =
      encodeIntent task.snapshot (resumeBlocked thread.core thread.code
        (decodeBlockedOutcome thread.code task.conditionResults)) := by
  unfold executeResponse
  rw [hasStep, hasType, hasInput, recordInput]
  simp only [translate, pointStepDef, isBlocking, ↓reduceIte]

/-- The Execute of the `Timed` Step. -/
theorem wp3b_response_resumeTimed (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (task : Dex.Task signature) (number seconds : Nat)
    (hasStep : task.step = record.id) (hasInput : task.input = record.input)
    (hasId : record.id = { stepType := StepType.timed, number := number })
    (recordInput : record.input = some (DexVal.timedThread thread.core thread.code seconds)) :
    executeResponse program task =
      encodeIntent task.snapshot (resumeTimed thread.core thread.code
        (decodeTimedOutcome thread.code task.conditionResults)) := by
  unfold executeResponse
  rw [hasStep, hasId, hasInput, recordInput]
  simp only [translate, timedStepDef]

/-! ## The thread and its Step -/

/-- A thread that has not halted, with its run and the Step that carries it. -/
theorem wp3b_thread_context (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads) (isLive : thread.phase ≠ .halted) :
    ∃ run record, dexConfig.run = some run ∧ run.status = .running ∧ record ∈ run.steps ∧
      record.phase.isActive = true ∧ StepCarriesThread program thread record ∧
      PhaseMatchesStep program lConfig dexConfig thread record := by
  obtain ⟨run, hasRun, isRunning⟩ := (IsSimulationState.run_of_thread state thread isThread).2
  obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
    (state.isRelated.running run hasRun isRunning).threadsHaveSteps thread isThread isLive
  exact ⟨run, record, hasRun, isRunning, isRecord, isActive, carries, isMatch⟩

/-- The cancel flag of a thread that has not observed it is a pending request. -/
theorem wp3b_ownFlag (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (state : IsSimulationState program lConfig dexConfig) (run : Dex.Run signature)
    (hasRun : dexConfig.run = some run) (isRunning : run.status = .running) (thread : L.Thread)
    (isFound : lConfig.findThread? thread.core.tid = some thread)
    (isNotWoken : thread.phase ≠ .woken .canceled)
    (isNotResuming : thread.phase ≠ .resuming .canceled)
    (isFlagged : lConfig.cancelFlags.contains thread.core.tid = true) :
    run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [] := by
  rcases (state.isRelated.running run hasRun isRunning).cancelFlagsAreRequests thread.core.tid
    isFlagged with hasRequest | ⟨current, isCurrent, isCanceled⟩ | ⟨isGone, _⟩
  · exact hasRequest
  · rw [isFound] at isCurrent
    cases isCurrent
    rcases isCanceled with isWoken | isResuming
    · exact absurd isWoken isNotWoken
    · exact absurd isResuming isNotResuming
  · rw [isFound] at isGone
    cases isGone

/-! ## The completion of a statement -/

/-- The completion of a statement that began, for every row of the mapping
table. -/
theorem wp3b_running_simulated
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (isResponse : executeResponse program task = encodeIntent task.snapshot result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (waitForQuiet : WaitForTasksHaveNoEffects program dexConfig)
    (unlockHeld : ∀ mutex, result.intent = .unlock mutex →
      lConfig.mutexOwners.get? mutex = some (some thread.core.tid))
    (cvHeld : ∀ cv, result.intent = .cvWait cv →
      lConfig.mutexOwners.get? cv.mutex = some (some thread.core.tid))
    (cvWaiters : ∀ cv, (result.intent = .cvWait cv ∨ result.intent = .signal cv ∨
        result.intent = .broadcast cv) →
      readWaiters task.snapshot cv = (lConfig.cvQueues.get? cv).getD [] ∧
      NoOtherOperand lConfig thread.core.tid cv)
    (forkFresh : ∀ child childCode, result.intent = .fork child childCode →
      (∀ other, other ∈ lConfig.threads → other.core.tid ≠ child.tid) ∧
      (lConfig.cancelFlags.contains child.tid = true →
        run.pendingMessages (Chan.cancelRequest child.tid) ≠ []) ∧
      ∀ target, lConfig.cancelFlags.contains target = true →
        run.pendingMessages (Chan.cancelRequest target) = [] →
        ∀ index, child.freshCounter ≤ index → ¬ (child.tid ++ [index]) <+: target)
    (mutexFresh : ∀ mutex, result.intent = .newMutex mutex →
      lConfig.mutexOwners.get? mutex = none ∧
      ∀ other, other ∈ lConfig.threads → ∀ since, other.phase ≠ .blocked (.lock mutex) since) :
    IsSimulatedByCompletion program dexConfig task
      (lConfig.applyIntent thread.core.tid result).1
      (lConfig.applyIntent thread.core.tid result).2.stamp := by
  cases intentCase : result.intent with
  | exit value =>
    exact wp3b_simulated_close start isResponse waitForQuiet (Or.inl ⟨value, intentCase⟩)
  | fail message =>
    exact wp3b_simulated_close start isResponse waitForQuiet (Or.inr ⟨message, intentCase⟩)
  | silent =>
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_silent start ownFlag intentCase)
  | store target value =>
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_store start ownFlag target value intentCase)
  | fork child childCode =>
    obtain ⟨childFresh, childFlag, childIsNoParent⟩ := forkFresh child childCode intentCase
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_fork start ownFlag child childCode intentCase childFresh childFlag
        childIsNoParent)
  | newMutex mutex =>
    obtain ⟨isFresh, noLocker⟩ := mutexFresh mutex intentCase
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_newMutex start ownFlag mutex intentCase isFresh noLocker)
  | unlock mutex =>
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_unlock start ownFlag mutex intentCase (unlockHeld mutex intentCase))
  | cvWait cv =>
    obtain ⟨sameWaiters, noOther⟩ := cvWaiters cv (Or.inl intentCase)
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_cvWait start ownFlag cv intentCase (cvHeld cv intentCase) sameWaiters noOther)
  | signal cv =>
    obtain ⟨sameWaiters, noOther⟩ := cvWaiters cv (Or.inr (Or.inl intentCase))
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_signal start ownFlag cv intentCase sameWaiters noOther)
  | broadcast cv =>
    obtain ⟨sameWaiters, noOther⟩ := cvWaiters cv (Or.inr (Or.inr intentCase))
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_broadcast start ownFlag cv intentCase sameWaiters noOther)
  | cancel target =>
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_cancel start ownFlag target intentCase)
  | enterTimed seconds =>
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_enterTimed start ownFlag seconds intentCase)
  | endThread =>
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_endThread start intentCase)
  | halt =>
    exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_halt start ownFlag intentCase)

/-- The completion of a statement that began and emits nothing. -/
theorem wp3b_complete_running (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (thread : L.Thread) (isFound : lConfig.findThread? thread.core.tid = some thread)
    (loaded : Val) (isPhase : thread.phase = .running loaded)
    (canStamp : lConfig.canStamp = true) :
    IsSimulatedByActions program lConfig dexConfig (.thread thread.core.tid)
      (lConfig.applyIntent thread.core.tid
        (completeStatement program thread.core thread.code loaded)).1
      (lConfig.applyIntent thread.core.tid
        (completeStatement program thread.core thread.code loaded)).2.stamp := by
  obtain ⟨isThread, _⟩ := mem_of_findThread? isFound
  have lInvariant := state.lInvariant
  obtain ⟨run, record, hasRun, isRunning, isRecord, isActive, carries, isMatch⟩ :=
    wp3b_thread_context program lConfig dexConfig state thread isThread (by simp [isPhase])
  unfold PhaseMatchesStep at isMatch
  simp only [isPhase] at isMatch
  obtain ⟨task, completing, holds, hasInput⟩ := Completing.of_hasRunningTask hasRun isRunning
    isThread isRecord isActive carries (by simp [isPhase]) canStamp _ isMatch.2
    (fun _ taskHolds => taskHolds.1)
  obtain ⟨_, _, isLoaded, sameWaiters⟩ := holds
  have fits := lInvariant.phasesFitCode thread isThread
  unfold Thread.PhaseFitsCode at fits
  simp only [isPhase] at fits
  obtain ⟨noTimer, isNotBlocking, isNotEmit⟩ := fits
  have shape := (stepCarriesThread_iff_of_booted program thread record (by simp [isPhase])
    (by simp [isPhase])).mp carries
  simp only [noTimer] at shape
  have isResponse := wp3b_response_running program thread record task loaded completing.hasStep
    hasInput shape.1 shape.2.1 isNotBlocking isNotEmit (fun source isLoad => by
      simp only [isLoad] at isLoaded
      exact isLoaded)
  obtain ⟨sameTid, forkChild, newMutexId, operandOfCv⟩ :=
    wp3b_completeStatement_facts program thread.core thread.code loaded
  have requiredEq := requiredMutex_eq_of_intent program thread.core thread.code loaded
  have holdsRequired := lInvariant.runningHoldsRequiredMutex thread isThread loaded isPhase
  have noBoot := noBoot_of_booted program lConfig lInvariant thread isThread
    ⟨by simp [isPhase], by simp [isPhase]⟩
  have start : IntentStart program lConfig dexConfig run thread record task
      (completeStatement program thread.core thread.code loaded) :=
    ⟨state, state.waitingHasNoInvocation, completing, sameTid,
      wp3b_completeStatement_counter program thread.core thread.code loaded, noBoot⟩
  have ownFlag := wp3b_ownFlag program lConfig dexConfig state run hasRun isRunning thread isFound
    (by simp [isPhase]) (by simp [isPhase])
  apply completing.toActions state isFound (Or.inr (Or.inl ⟨loaded, isPhase⟩))
  apply wp3b_running_simulated start isResponse ownFlag (wp3b_waitForQuiet state)
  · intro mutex intentCase
    rw [intentCase] at requiredEq
    exact holdsRequired mutex requiredEq
  · intro cv intentCase
    rw [intentCase] at requiredEq
    exact holdsRequired cv.mutex requiredEq
  · intro cv isCvIntent
    have isOperand := operandOfCv cv isCvIntent
    refine ⟨sameWaiters cv isOperand, ?_⟩
    intro other isOther isDistinct otherLoaded otherPhase otherOperand
    have ownHolds := holdsRequired cv.mutex
      (wp3b_requiredMutex_of_cvOperand thread.core thread.code cv isOperand)
    have otherHolds := lInvariant.runningHoldsRequiredMutex other isOther otherLoaded otherPhase
      cv.mutex (wp3b_requiredMutex_of_cvOperand other.core other.code cv otherOperand)
    rw [ownHolds] at otherHolds
    simp only [Option.some.injEq] at otherHolds
    exact isDistinct otherHolds.symm
  · intro child childCode intentCase
    have childTid := forkChild child childCode intentCase
    have related := state.isRelated.running run hasRun isRunning
    have childFresh : ∀ other, other ∈ lConfig.threads → ¬ child.tid <+: other.core.tid := by
      intro other isOther
      rw [childTid]
      exact lInvariant.threadIdsFresh thread isThread other isOther thread.core.freshCounter
        (Nat.le_refl _)
    have ofLonger : ∀ target index, (child.tid ++ [index]) <+: target → child.tid <+: target :=
      fun target index isPrefix => (List.prefix_append child.tid [index]).trans isPrefix
    refine ⟨fun other isOther sameId =>
      childFresh other isOther (by rw [sameId]; exact List.prefix_refl _), ?_, ?_⟩
    · intro isFlagged
      rcases related.cancelFlagsAreRequests child.tid isFlagged with hasRequest |
        ⟨current, isCurrent, _⟩ | ⟨_, isNoChild⟩
      · exact hasRequest
      · obtain ⟨isMember, hasId⟩ := mem_of_findThread? isCurrent
        exact absurd (by rw [hasId]; exact List.prefix_refl _) (childFresh current isMember)
      · exact absurd (by rw [childTid]; exact List.prefix_refl _)
          (isNoChild thread isThread thread.core.freshCounter (Nat.le_refl _))
    · intro target isFlagged isEmpty index _ isPrefix
      have isChildPrefix := ofLonger target index isPrefix
      rcases related.cancelFlagsAreRequests target isFlagged with hasRequest |
        ⟨current, isCurrent, _⟩ | ⟨_, isNoChild⟩
      · exact hasRequest isEmpty
      · obtain ⟨isMember, hasId⟩ := mem_of_findThread? isCurrent
        exact childFresh current isMember (by rw [hasId]; exact isChildPrefix)
      · rw [childTid] at isChildPrefix
        exact isNoChild thread isThread thread.core.freshCounter (Nat.le_refl _) isChildPrefix
  · intro mutex intentCase
    have isMutex := newMutexId mutex intentCase
    subst isMutex
    have isNotContained : lConfig.mutexOwners.contains
        (.dynamic thread.core.tid thread.core.freshCounter) ≠ true := by
      intro isContained
      exact Nat.lt_irrefl _ ((lInvariant.mutexesFresh thread isThread _ _ isContained).1 rfl)
    refine ⟨?_, ?_⟩
    · cases getCase : lConfig.mutexOwners.get?
          (.dynamic thread.core.tid thread.core.freshCounter) with
      | none => rfl
      | some owner => exact absurd (by simp [AssocList.contains, getCase]) isNotContained
    · intro other isOther since isBlocked
      have isValid := (lInvariant.handlesExist.1 other isOther).2.2.2
      rw [isBlocked] at isValid
      exact isNotContained isValid

/-- The completion of an `emit` after its effect was delivered. -/
theorem wp3b_complete_emitted (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (thread : L.Thread)
    (isFound : lConfig.findThread? thread.core.tid = some thread)
    (isPhase : thread.phase = .emitted) (canStamp : lConfig.canStamp = true) :
    IsSimulatedByActions program lConfig dexConfig (.thread thread.core.tid)
      (lConfig.applyIntent thread.core.tid
        (completeStatement program thread.core thread.code .null)).1
      (lConfig.applyIntent thread.core.tid
        (completeStatement program thread.core thread.code .null)).2.stamp := by
  obtain ⟨isThread, _⟩ := mem_of_findThread? isFound
  have lInvariant := state.lInvariant
  obtain ⟨run, record, hasRun, isRunning, isRecord, isActive, carries, isMatch⟩ :=
    wp3b_thread_context program lConfig dexConfig state thread isThread (by simp [isPhase])
  unfold PhaseMatchesStep at isMatch
  simp only [isPhase] at isMatch
  obtain ⟨task, completing, _, hasInput⟩ := Completing.of_hasRunningTask hasRun isRunning
    isThread isRecord isActive carries (by simp [isPhase]) canStamp _ isMatch.2
    (fun _ taskHolds => taskHolds.1)
  have fits := lInvariant.phasesFitCode thread isThread
  unfold Thread.PhaseFitsCode at fits
  simp only [isPhase] at fits
  obtain ⟨noTimer, isNotBlocking, effect, isEmit⟩ := fits
  have shape := (stepCarriesThread_iff_of_booted program thread record (by simp [isPhase])
    (by simp [isPhase])).mp carries
  simp only [noTimer] at shape
  have isResponse := wp3b_response_emitted program thread record task effect completing.hasStep
    hasInput shape.1 shape.2.1 isNotBlocking isEmit
  have intentCase := wp3b_intent_of_emit program thread.core thread.code effect isEmit
  have start : IntentStart program lConfig dexConfig run thread record task
      (completeStatement program thread.core thread.code .null) :=
    ⟨state, state.waitingHasNoInvocation, completing,
      (wp3b_completeStatement_facts program thread.core thread.code .null).1,
      wp3b_completeStatement_counter program thread.core thread.code .null,
      noBoot_of_booted program lConfig lInvariant thread isThread
        ⟨by simp [isPhase], by simp [isPhase]⟩⟩
  apply completing.toActions state isFound (Or.inr (Or.inr (Or.inl isPhase)))
  exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
    (wp3b_intent_silent start (wp3b_ownFlag program lConfig dexConfig state run hasRun isRunning
      thread isFound (by simp [isPhase]) (by simp [isPhase])) intentCase)

/-- `l_thread_complete_simulated`: the completion of a statement is simulated
by the completion actions of the thread's task. -/
theorem wp3b_complete_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (isCompleting : (∃ loaded, thread.phase = .running loaded) ∨ thread.phase = .emitted)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  obtain ⟨_, hasId⟩ := mem_of_findThread? isFound
  subst hasId
  have isStepThread : stepThread program lConfig thread = some (lEvents, lNext) := by
    simpa [L.system, L.step, isFound] using isStep
  rcases isCompleting with ⟨loaded, isPhase⟩ | isPhase
  · simp only [stepThread, isPhase] at isStepThread
    split at isStepThread
    · rename_i canStamp
      cases isStepThread
      exact wp3b_complete_running program lConfig dexConfig state thread isFound loaded isPhase
        canStamp
    · cases isStepThread
  · simp only [stepThread, isPhase] at isStepThread
    split at isStepThread
    · rename_i canStamp
      cases isStepThread
      exact wp3b_complete_emitted program lConfig dexConfig state thread isFound isPhase
        canStamp
    · cases isStepThread

/-! ## The completion of the continuation of a wait -/

/-- The continuation of a wait continues, ends, or halts the thread. -/
theorem wp3b_resume_simulated
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (isResponse : executeResponse program task = encodeIntent task.snapshot result)
    (isFound : lConfig.findThread? thread.core.tid = some thread) (outcome : WakeOutcome)
    (isPhase : thread.phase = .resuming outcome)
    (intents : result.intent = .silent ∨ result.intent = .endThread ∨ result.intent = .halt)
    (canceledEnds : outcome = .canceled → result.intent = .endThread) :
    IsSimulatedByCompletion program dexConfig task
      (lConfig.applyIntent thread.core.tid result).1
      (lConfig.applyIntent thread.core.tid result).2.stamp := by
  have ownFlag : result.intent ≠ .endThread →
      lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [] := by
    intro isNotEnd
    apply wp3b_ownFlag program lConfig dexConfig start.state run start.start.hasRun
      start.start.isRunning thread isFound (by simp [isPhase])
    intro isCanceledPhase
    rw [isPhase] at isCanceledPhase
    exact isNotEnd (canceledEnds (by simpa using isCanceledPhase))
  rcases intents with intentCase | intentCase | intentCase
  · exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_silent start (ownFlag (by simp [intentCase])) intentCase)
  · exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_endThread start intentCase)
  · exact wp3b_simulated_of_goal start isResponse (by simp [intentCase]) (by simp [intentCase])
      (wp3b_intent_halt start (ownFlag (by simp [intentCase])) intentCase)

/-- `l_thread_resume_simulated`: the completion of the continuation of a wait
is simulated by the completion actions of the task of the waiting Step or of
the `Timed` Step. -/
theorem wp3b_resume_thread_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread)
    (isFound : lConfig.findThread? tid = some thread) (outcome : WakeOutcome)
    (isResuming : thread.phase = .resuming outcome) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  obtain ⟨isThread, hasId⟩ := mem_of_findThread? isFound
  subst hasId
  have isStepThread : stepThread program lConfig thread = some (lEvents, lNext) := by
    simpa [L.system, L.step, isFound] using isStep
  simp only [stepThread, isResuming] at isStepThread
  split at isStepThread
  · rename_i canStamp
    cases isStepThread
    have lInvariant := state.lInvariant
    obtain ⟨run, record, hasRun, isRunning, isRecord, isActive, carries, isMatch⟩ :=
      wp3b_thread_context program lConfig dexConfig state thread isThread (by simp [isResuming])
    unfold PhaseMatchesStep at isMatch
    simp only [isResuming] at isMatch
    obtain ⟨task, completing, holds, hasInput⟩ := Completing.of_hasRunningTask hasRun isRunning
      isThread isRecord isActive carries (by simp [isResuming]) canStamp _ isMatch.2
      (fun _ taskHolds => taskHolds.1)
    apply completing.toActions state isFound
      (Or.inr (Or.inr (Or.inr (Or.inr ⟨outcome, isResuming⟩))))
    have fits := lInvariant.phasesFitCode thread isThread
    unfold Thread.PhaseFitsCode at fits
    simp only [isResuming] at fits
    have noBoot := noBoot_of_booted program lConfig lInvariant thread isThread
      ⟨by simp [isResuming], by simp [isResuming]⟩
    have shape := (stepCarriesThread_iff_of_booted program thread record (by simp [isResuming])
      (by simp [isResuming])).mp carries
    have isDecodedOutcome := holds.2
    unfold decodeOutcome at isDecodedOutcome
    cases timerCase : thread.timer with
    | none =>
      have isBlocking : isBlockingHead thread.code = true := by
        rcases fits with isTimed | isBlocking
        · rw [timerCase] at isTimed
          cases isTimed
        · exact isBlocking
      simp only [timerCase] at shape
      simp only [timerCase, Option.isSome_none, Bool.false_eq_true, ↓reduceIte]
        at isDecodedOutcome ⊢
      have isResponse := wp3b_response_resumeBlocked program thread record task
        completing.hasStep hasInput shape.1 shape.2.1 isBlocking
      rw [isDecodedOutcome] at isResponse
      obtain ⟨sameTid, counterKept, intents⟩ :=
        wp3b_resumeBlocked_facts thread.core thread.code outcome
      exact wp3b_resume_simulated
        ⟨state, state.waitingHasNoInvocation, completing, sameTid, counterKept, noBoot⟩
        isResponse isFound outcome isResuming intents (fun isCanceled => by
          subst isCanceled
          exact wp3b_resumeBlocked_canceled thread.core thread.code task.conditionResults
            isDecodedOutcome)
    | some timer =>
      obtain ⟨number, seconds⟩ := timer
      simp only [timerCase] at shape
      simp only [timerCase, Option.isSome_some, ↓reduceIte] at isDecodedOutcome ⊢
      have isResponse := wp3b_response_resumeTimed program thread record task number seconds
        completing.hasStep hasInput shape.1 shape.2
      rw [isDecodedOutcome] at isResponse
      obtain ⟨sameTid, counterKept, intents⟩ :=
        wp3b_resumeTimed_facts thread.core thread.code outcome
      exact wp3b_resume_simulated
        ⟨state, state.waitingHasNoInvocation, completing, sameTid, counterKept, noBoot⟩
        isResponse isFound outcome isResuming intents (fun isCanceled => by
          subst isCanceled
          exact wp3b_resumeTimed_canceled thread.core thread.code task.conditionResults
            isDecodedOutcome)
  · cases isStepThread

/-! ## The end of the start action -/

/-- Publications in two parts. -/
theorem wp3b_applyPublications_append (run : Dex.Run signature)
    (first second : List (Chan × DexVal)) :
    run.applyPublications (first ++ second) =
      (run.applyPublications first).applyPublications second := by
  simp [Dex.Run.applyPublications, List.foldl_append]

/-- The tokens that the start Step publishes: one for each statically known
mutex. -/
theorem wp3b_pending_staticTokens (run : Dex.Run signature) (count : Nat)
    (noToken : ∀ mutex, run.pendingMessages (Chan.mutexToken mutex) = []) (mutex : MutexId) :
    (run.applyPublications ((List.range count).map (fun index =>
      (Chan.mutexToken (MutexId.static index), DexVal.token)))).pendingMessages
        (Chan.mutexToken mutex) =
      match mutex with
      | .static index => if index < count then [DexVal.token] else []
      | .dynamic _ _ => [] := by
  induction count generalizing mutex with
  | zero =>
    have isSame : run.applyPublications ((List.range 0).map (fun index =>
        (Chan.mutexToken (MutexId.static index), DexVal.token))) = run := rfl
    rw [isSame, noToken]
    cases mutex <;> simp
  | succ count inductionHypothesis =>
    rw [List.range_succ, List.map_append, wp3b_applyPublications_append]
    simp only [List.map_cons, List.map_nil]
    rw [wp3b_pendingMessages_publish, inductionHypothesis mutex,
      inductionHypothesis (.static count)]
    cases mutex with
    | dynamic creator index => simp
    | static index =>
      by_cases isLast : index = count
      · subst isLast
        simp
      · have isOtherChan : ¬ (Chan.mutexToken (MutexId.static index) =
            Chan.mutexToken (MutexId.static count)) := by simpa using isLast
        simp only [isOtherChan, ↓reduceIte]
        by_cases isBelow : index < count
        · have isBelowNext : index < count + 1 := by omega
          simp [isBelow, isBelowNext]
        · have isNotBelowNext : ¬ index < count + 1 := by omega
          simp [isBelow, isNotBelowNext]

/-- The unlocked mutexes of a list of statically known ones. -/
theorem wp3b_get?_staticOwners (indices : List Nat) (mutex : MutexId) :
    AssocList.get? (indices.map (fun index => (MutexId.static index, (none : Option Tid))))
        mutex = some none ↔
      ∃ index, index ∈ indices ∧ mutex = .static index := by
  induction indices with
  | nil => simp [AssocList.get?]
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, AssocList.get?]
    by_cases isHead : MutexId.static head = mutex
    · simp only [isHead, ↓reduceIte, true_iff]
      exact ⟨head, List.mem_cons_self .., isHead.symm⟩
    · simp only [isHead, ↓reduceIte]
      rw [inductionHypothesis]
      constructor
      · rintro ⟨index, isMember, isStatic⟩
        exact ⟨index, List.mem_cons_of_mem _ isMember, isStatic⟩
      · rintro ⟨index, isMember, isStatic⟩
        rcases List.mem_cons.mp isMember with isSame | isRest
        · exact absurd (by rw [isStatic, isSame]) isHead
        · exact ⟨index, isRest, isStatic⟩

/-- `l_thread_boot_simulated`: the end of the start action is simulated by
the completion actions of the task of the start Step, which publishes the
tokens of the statically known mutexes and moves to the first program point
of `main`. -/
theorem wp3b_boot_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread)
    (isFound : lConfig.findThread? tid = some thread)
    (isBootRunning : thread.phase = .bootRunning) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  obtain ⟨isThread, hasId⟩ := mem_of_findThread? isFound
  subst hasId
  have isStepThread : stepThread program lConfig thread = some (lEvents, lNext) := by
    simpa [L.system, L.step, isFound] using isStep
  simp only [stepThread, isBootRunning] at isStepThread
  split at isStepThread
  · rename_i canStamp
    cases isStepThread
    have lInvariant := state.lInvariant
    obtain ⟨isOnly, noTimer, isCode, input, isCore⟩ :=
      lInvariant.bootIsInitial thread isThread (Or.inr isBootRunning)
    obtain ⟨run, record, hasRun, isRunning, isRecord, isActive, carries, isMatch⟩ :=
      wp3b_thread_context program lConfig dexConfig state thread isThread
        (by simp [isBootRunning])
    unfold PhaseMatchesStep at isMatch
    simp only [isBootRunning] at isMatch
    obtain ⟨task, completing, _, hasInput⟩ := Completing.of_hasRunningTask hasRun isRunning
      isThread isRecord isActive carries (by simp [isBootRunning]) canStamp _ isMatch.2
      (fun _ taskHolds => taskHolds)
    have carriesBoot := carries
    unfold StepCarriesThread at carriesBoot
    simp only [isBootRunning] at carriesBoot
    obtain ⟨hasType, programInput, recordInput, hasLocals⟩ := carriesBoot
    have sameCore : thread.core =
        { tid := [], freshCounter := 0, locals := [(program.mainParam, programInput)],
          stack := [] } := by
      rw [isCore] at hasLocals ⊢
      simp only [List.cons.injEq, Prod.mk.injEq, true_and, and_true] at hasLocals
      rw [hasLocals]
    have isResponse : executeResponse program task =
        { effects := { publications := (List.range program.staticMutexCount).map (fun index =>
            (Chan.mutexToken (MutexId.static index), DexVal.token)) }
          decision := { moves := [moveToPoint thread.core thread.code], close := none } } := by
      unfold executeResponse
      rw [completing.hasStep, hasType, hasInput, recordInput]
      simp only [translate, startStepDef]
      rw [isCode, sameCore]
    -- The relation after the commit released the invocation.
    obtain ⟨midThreads, midData, isOwn⟩ := wp3b_begin_update program lConfig dexConfig state
      state.waitingHasNoInvocation run thread record task completing
      (fun current => { current with phase := .ready }) rfl (by simp) (by simp) (Nat.le_refl _)
      (wp3b_ownFlag program lConfig dexConfig state run hasRun isRunning thread isFound
        (by simp [isBootRunning]) (by simp [isBootRunning]))
    have noBlocked : ∀ other, other ∈ (lConfig.updateThread thread.core.tid
        (fun current => { current with phase := .ready })).threads → ∀ on since,
        other.phase ≠ .blocked on since := by
      intro other isOther on since
      by_cases isOwnTid : other.core.tid = thread.core.tid
      · exact midThreads.ownThread other isOther isOwnTid on since
      · have isOld := ((wp3b_updateThread_spec lConfig thread isThread
          lInvariant.threadIdsNodup _ rfl).1 other isOwnTid).mp isOther
        rw [isOnly] at isOld
        have isSame : other = thread := by simpa using isOld
        exact absurd (by rw [isSame]) isOwnTid
    -- The tokens: no Step waits.
    have isPublished := wp3b_publish_noWaiter midThreads
      ((List.range program.staticMutexCount).map (fun index =>
        (Chan.mutexToken (MutexId.static index), DexVal.token)))
      (fun other isOther on since isBlocked => absurd isBlocked (noBlocked other isOther on since))
    obtain ⟨sameSteps, sameCounters, sameTimers, sameSeq, sameStatus, sameAttributes, sameOutputs,
      samePending⟩ := Dex.Run.applyPublications_frame
        ((List.range program.staticMutexCount).map (fun index =>
          (Chan.mutexToken (MutexId.static index), DexVal.token))) (releasedRun run record.id)
    have invariantNext := midThreads.runInvariant.applyEffects
      { publications := (List.range program.staticMutexCount).map (fun index =>
          (Chan.mutexToken (MutexId.static index), DexVal.token)) }
    rw [isPublished] at invariantNext
    have published : MidState program
        (lConfig.updateThread thread.core.tid (fun current => { current with phase := .ready }))
        (withRun (releasedConfig dexConfig task.id)
          ((releasedRun run record.id).applyPublications
            ((List.range program.staticMutexCount).map (fun index =>
              (Chan.mutexToken (MutexId.static index), DexVal.token)))))
        ((releasedRun run record.id).applyPublications
          ((List.range program.staticMutexCount).map (fun index =>
            (Chan.mutexToken (MutexId.static index), DexVal.token))))
        thread.core.tid record.id := by
      refine ⟨?_, ?_, ?_⟩
      · exact midThreads.frame _ _ _ rfl rfl (fun _ _ _ _ _ _ _ => rfl) rfl sameSteps sameTimers
          (by rw [sameSeq]; exact invariantNext)
      · exact midData.transfer _ _ (fun _ => none)
          (by exact (wp3b_map_wakeThreadBy_none _).symm) (fun _ _ _ isWoken => by cases isWoken)
          sameStatus sameOutputs sameCounters sameAttributes rfl rfl rfl rfl
          (fun chan => samePending _ (by simp))
          (Or.inl ⟨fun joined => samePending _ (by simp), rfl⟩)
          (Or.inl ⟨fun cv waiter => samePending _ (by simp), rfl⟩)
          (Or.inl ⟨fun target => samePending _ (by simp), rfl⟩)
      · intro mutex
        obtain ⟨ownersEq, noToken⟩ :=
          (state.isRelated.running run hasRun isRunning).mutexesBeforeBoot thread isThread
            (Or.inr isBootRunning)
        have staticOwners : ∀ other, lConfig.mutexOwners.get? other = some none ↔
            ∃ index, index < program.staticMutexCount ∧ other = .static index := by
          intro other
          rw [ownersEq, wp3b_get?_staticOwners]
          simp [List.mem_range]
        rw [wp3b_pending_staticTokens (releasedRun run record.id) program.staticMutexCount noToken
          mutex]
        show _ = if lConfig.mutexOwners.get? mutex = some none then 1 else 0
        cases mutex with
        | dynamic creator index =>
          have isNotUnlocked : lConfig.mutexOwners.get? (.dynamic creator index) ≠ some none := by
            intro isUnlocked
            obtain ⟨_, _, isStatic⟩ := (staticOwners _).mp isUnlocked
            cases isStatic
          simp [isNotUnlocked]
        | static index =>
          by_cases isBelow : index < program.staticMutexCount
          · have isUnlocked := (staticOwners (.static index)).mpr ⟨index, isBelow, rfl⟩
            simp [isBelow, isUnlocked]
          · have isNotUnlocked : lConfig.mutexOwners.get? (.static index) ≠ some none := by
              intro isUnlocked
              obtain ⟨other, otherBelow, isStatic⟩ := (staticOwners _).mp isUnlocked
              cases isStatic
              exact isBelow otherBelow
            simp [isBelow, isNotUnlocked]
    -- The commit and the relation after it.
    have isExec := completing.exec state
    have isRun : completedRun run record.id (executeResponse program task) =
        ((releasedRun run record.id).applyPublications
          ((List.range program.staticMutexCount).map (fun index =>
            (Chan.mutexToken (MutexId.static index), DexVal.token)))).applyDecision record.id
          { moves := [moveToPoint thread.core thread.code], close := none } := by
      rw [wp3b_completedRun_eq, isResponse]
      simp only
      rw [isPublished]
    rw [isRun] at isExec
    apply completing.toActions state isFound (Or.inl isBootRunning)
    refine ⟨_, _, isExec, ?_, ?_⟩
    · rw [wp3b_closeEvents_running]
      · rfl
      · exact published.data.isRunning
    · refine wp3b_related_of_running program lConfig _ dexConfig _ _ state.isRelated rfl rfl
        (wp3b_status_running program lConfig dexConfig state.isRelated run hasRun isRunning) rfl
        rfl rfl published.data.isRunning ?_
      exact wp3b_decide_goto program _ _ _ _ _ thread.core.tid record.id published.threads
        published.data published.tokens { thread with phase := .ready } isOwn rfl rfl noTimer
        ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl rfl rfl rfl
  · cases isStepThread

end DexExpressiveness
