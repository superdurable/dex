import DexExpressiveness.Properties
import DexExpressiveness.Simulation.Completion.IntentsSync
import DexExpressiveness.Simulation.Completion.Close

/-!
# What a completed statement asks for

Facts about `completeStatement`, `resumeBlocked`, and `resumeTimed` (the
thread-local semantics), about `encodeIntent`, and about the events of
`Config.applyIntent`, as the completion lemmas need them.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## `completeStatement` -/

/-- A completed statement keeps the thread ID; a forked child and a created
mutex are named by the thread's counter; and a condition-variable action is
on the operand of the head statement. -/
theorem wp3b_completeStatement_facts (program : Program) (core : ThreadCore) (code : Code)
    (loaded : Val) :
    (completeStatement program core code loaded).core.tid = core.tid ∧
    (∀ child childCode, (completeStatement program core code loaded).intent =
        .fork child childCode → child.tid = core.tid ++ [core.freshCounter]) ∧
    (∀ mutex, (completeStatement program core code loaded).intent = .newMutex mutex →
      mutex = .dynamic core.tid core.freshCounter) ∧
    (∀ cv, ((completeStatement program core code loaded).intent = .cvWait cv ∨
        (completeStatement program core code loaded).intent = .signal cv ∨
        (completeStatement program core code loaded).intent = .broadcast cv) →
      cvOperand core code = some cv) := by
  generalize isResult : completeStatement program core code loaded = result
  cases code with
  | nil =>
    simp only [completeStatement, returnValue] at isResult
    repeat' split at isResult
    all_goals (subst isResult; simp)
  | cons instr rest =>
    cases instr with
    | park cv =>
      simp only [completeStatement] at isResult
      subst isResult
      simp
    | relock mutex =>
      simp only [completeStatement] at isResult
      subst isResult
      simp
    | source statement =>
      cases statement <;> simp only [completeStatement, returnValue] at isResult <;>
        (repeat' split at isResult) <;>
        (subst isResult; simp_all [cvOperand, ThreadCore.setLocal])

/-- A completed statement does not lower the counter of fresh names. -/
theorem wp3b_completeStatement_counter (program : Program) (core : ThreadCore) (code : Code)
    (loaded : Val) :
    core.freshCounter ≤ (completeStatement program core code loaded).core.freshCounter := by
  generalize isResult : completeStatement program core code loaded = result
  cases code with
  | nil =>
    simp only [completeStatement, returnValue] at isResult
    repeat' split at isResult
    all_goals (subst isResult; simp)
  | cons instr rest =>
    cases instr with
    | park cv =>
      simp only [completeStatement] at isResult
      subst isResult
      simp
    | relock mutex =>
      simp only [completeStatement] at isResult
      subst isResult
      simp
    | source statement =>
      cases statement <;> simp only [completeStatement, returnValue] at isResult <;>
        (repeat' split at isResult) <;>
        (subst isResult; simp [ThreadCore.setLocal])

/-- Only a `load` uses the value read at Start. -/
theorem wp3b_completeStatement_loaded (program : Program) (core : ThreadCore) (code : Code)
    (first second : Val) (isNotLoad : ∀ source, beginKind core code ≠ .load source) :
    completeStatement program core code first = completeStatement program core code second := by
  cases code with
  | nil => rfl
  | cons instr rest =>
    cases instr with
    | park cv => rfl
    | relock mutex => rfl
    | source statement =>
      cases statement <;> first | rfl | exact absurd rfl (isNotLoad _)

/-- An `emit` whose operand evaluates completes with a local step. -/
theorem wp3b_intent_of_emit (program : Program) (core : ThreadCore) (code : Code) (effect : Val)
    (isEmit : beginKind core code = .emit effect) :
    (completeStatement program core code .null).intent = .silent := by
  cases code with
  | nil => simp [beginKind] at isEmit
  | cons instr rest =>
    cases instr with
    | park cv => simp [beginKind] at isEmit
    | relock mutex => simp [beginKind] at isEmit
    | source statement =>
      cases statement <;> try (simp [beginKind] at isEmit; done)
      rename_i operand
      simp only [beginKind] at isEmit
      split at isEmit
      · rename_i value isValue
        simp [completeStatement, isValue]
      · cases isEmit

/-- The operand of a condition-variable action requires its mutex. -/
theorem wp3b_requiredMutex_of_cvOperand (core : ThreadCore) (code : Code) (cv : CvId)
    (isOperand : cvOperand core code = some cv) : requiredMutex core code = some cv.mutex := by
  cases code with
  | nil => simp [cvOperand] at isOperand
  | cons instr rest =>
    cases instr with
    | park other => simp [cvOperand] at isOperand
    | relock mutex => simp [cvOperand] at isOperand
    | source statement =>
      cases statement <;>
        first
          | (simp [cvOperand] at isOperand; done)
          | (show (cvOperand core _).map _ = _
             rw [isOperand]
             rfl)

/-! ## `resumeBlocked` and `resumeTimed` -/

/-- The continuation of a wait keeps the thread ID, and continues, ends, or
halts the thread. -/
theorem wp3b_resumeBlocked_facts (core : ThreadCore) (code : Code) (outcome : WakeOutcome) :
    (resumeBlocked core code outcome).core.tid = core.tid ∧
    core.freshCounter ≤ (resumeBlocked core code outcome).core.freshCounter ∧
    ((resumeBlocked core code outcome).intent = .silent ∨
      (resumeBlocked core code outcome).intent = .endThread ∨
      (resumeBlocked core code outcome).intent = .halt) := by
  unfold resumeBlocked
  split <;> simp [ThreadCore.setLocal]

/-- The continuation of a timed operation keeps the thread ID, and continues,
ends, or halts the thread. -/
theorem wp3b_resumeTimed_facts (core : ThreadCore) (code : Code) (outcome : WakeOutcome) :
    (resumeTimed core code outcome).core.tid = core.tid ∧
    core.freshCounter ≤ (resumeTimed core code outcome).core.freshCounter ∧
    ((resumeTimed core code outcome).intent = .silent ∨
      (resumeTimed core code outcome).intent = .endThread ∨
      (resumeTimed core code outcome).intent = .halt) := by
  unfold resumeTimed
  split <;> simp [ThreadCore.setLocal]

/-- A canceled `recv` or `join` ends the thread. -/
theorem wp3b_resumeBlocked_canceled (core : ThreadCore) (code : Code)
    (results : Option (Dex.ConditionResults signature))
    (isCanceled : decodeBlockedOutcome code results = .canceled) :
    (resumeBlocked core code .canceled).intent = .endThread := by
  unfold decodeBlockedOutcome at isCanceled
  split at isCanceled
  · cases isCanceled
  · split at isCanceled <;> first | (cases isCanceled; done) | simp [resumeBlocked]

/-- A canceled timed operation ends the thread. -/
theorem wp3b_resumeTimed_canceled (core : ThreadCore) (code : Code)
    (results : Option (Dex.ConditionResults signature))
    (isCanceled : decodeTimedOutcome code results = .canceled) :
    (resumeTimed core code .canceled).intent = .endThread := by
  have isTimed : timedChan code ≠ none := by
    intro noChan
    unfold decodeTimedOutcome at isCanceled
    split at isCanceled
    · cases isCanceled
    · split at isCanceled
      · cases isCanceled
      · rw [noChan] at isCanceled
        simp at isCanceled
  cases code with
  | nil => simp [timedChan] at isTimed
  | cons instr rest =>
    cases instr with
    | park cv => simp [timedChan] at isTimed
    | relock mutex => simp [timedChan] at isTimed
    | source statement =>
      cases statement <;>
        first
          | (simp [timedChan] at isTimed; done)
          | simp [resumeTimed]

/-! ## `encodeIntent` and the events of `applyIntent` -/

/-- Outside `exit` and `fail`, the decision does not close the run. -/
theorem wp3b_encodeIntent_close (snapshot : Dex.Snapshot signature) (result : StatementResult)
    (isNotExit : ∀ value, result.intent ≠ .exit value)
    (isNotFail : ∀ message, result.intent ≠ .fail message) :
    (encodeIntent snapshot result).decision.close = none ∨
    (encodeIntent snapshot result).decision.close = some .deadEnd := by
  cases intentCase : result.intent with
  | exit value => exact absurd intentCase (isNotExit value)
  | fail message => exact absurd intentCase (isNotFail message)
  | signal cv =>
    simp only [encodeIntent, intentCase]
    split <;> simp [moveDecision]
  | broadcast cv =>
    simp only [encodeIntent, intentCase]
    split <;> simp [moveDecision]
  | endThread => simp [encodeIntent, intentCase]
  | halt => simp [encodeIntent, intentCase, closeDecision]
  | _ => simp [encodeIntent, intentCase, moveDecision]

/-- Outside `exit` and `fail`, a completed statement has no event. -/
theorem wp3b_applyIntent_events (config : L.Config) (tid : Tid) (result : StatementResult)
    (isNotExit : ∀ value, result.intent ≠ .exit value)
    (isNotFail : ∀ message, result.intent ≠ .fail message) :
    (config.applyIntent tid result).1 = [] := by
  cases intentCase : result.intent with
  | exit value => exact absurd intentCase (isNotExit value)
  | fail message => exact absurd intentCase (isNotFail message)
  | signal cv =>
    simp only [L.Config.applyIntent, intentCase]
    split <;> rfl
  | endThread =>
    simp only [L.Config.applyIntent, intentCase]
    split <;> rfl
  | _ => simp [L.Config.applyIntent, intentCase]

/-! ## The status and the outputs of the run after the commit -/

/-- New Steps keep the status. -/
theorem wp3b_status_foldl_newStep (moves : List (Dex.Move signature))
    (origin : Dex.Origin signature) (run : Dex.Run signature) :
    (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run).status = run.status := by
  induction moves generalizing run with
  | nil => rfl
  | cons move laterMoves inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    rfl

/-- A commit whose decision does not close the run keeps it running. -/
theorem wp3b_completedRun_status (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (response : Dex.ExecuteResponse signature) (isRunning : run.status = .running)
    (isNotClosing : response.decision.close = none ∨ response.decision.close = some .deadEnd) :
    (completedRun run step response).status = .running := by
  have effectStatus : ((releasedRun run step).applyEffects response.effects).status = .running := by
    rw [(wp3b_applyEffects_frame (releasedRun run step) response.effects).2.2.1]
    exact isRunning
  rw [wp3b_completedRun_eq]
  unfold Dex.Run.applyDecision
  rcases isNotClosing with isNone | isDeadEnd
  · simp only [isNone]
    rw [wp3b_status_foldl_newStep]
    exact effectStatus
  · simp only [isDeadEnd]
    exact effectStatus

/-- A commit that keeps the run running has no `closed` event. -/
theorem wp3b_closeEvents_running (before after : Dex.Run signature)
    (isRunning : after.status = .running) : Dex.Config.closeEvents before after = [] := by
  unfold Dex.Config.closeEvents
  rw [isRunning]
  cases before.status <;> rfl

/-- `effects` with nothing to write or publish changes nothing, for any run. -/
theorem wp3b_applyEffects_empty (run : Dex.Run signature) : run.applyEffects {} = run := by
  have noWaiters : run.waitersOn [] = [] := by
    apply List.eq_nil_iff_forall_not_mem.mpr
    intro step isWaiter
    simp only [Dex.Run.waitersOn, List.mem_map] at isWaiter
    obtain ⟨pair, isSorted, _⟩ := isWaiter
    obtain ⟨record, _, isPair⟩ := List.mem_filterMap.mp ((mem_sortStable _ _ _).mp isSorted)
    cases phaseCase : record.phase <;> simp only [phaseCase] at isPair <;> try (cases isPair)
    cases waitCase : record.waitState with
    | none => simp [waitCase] at isPair
    | some waitState => simp [waitCase] at isPair
  show (run.wake []) = run
  unfold Dex.Run.wake
  rw [noWaiters]
  rfl

/-- Canceling Steps keeps the outputs. -/
theorem wp3b_outputs_foldl_finishStep (steps : List (Dex.StepExecId signature))
    (run : Dex.Run signature) :
    (steps.foldl (fun current step => current.finishStep step .canceled) run).outputs =
      run.outputs := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    rfl

/-- The close of a running run sets its status and keeps its outputs. -/
theorem wp3b_closeRun_running (run : Dex.Run signature) (status : Dex.CloseStatus)
    (isRunning : run.status = .running) :
    (run.closeRun status).status = .closed status ∧
    (run.closeRun status).outputs = run.outputs := by
  refine ⟨by simp [Dex.Run.closeRun, isRunning], ?_⟩
  simp only [Dex.Run.closeRun, isRunning]
  exact wp3b_outputs_foldl_finishStep _ run

/-! ## The run of a configuration with a thread -/

/-- A configuration with a thread is related to a running run. -/
theorem wp3b_running_of_thread (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads) :
    ∃ run, dexConfig.run = some run ∧ run.status = .running := by
  have status := state.isRelated.status
  cases statusCase : lConfig.status with
  | notStarted =>
    rw [(state.lInvariant.notStartedIsInitial statusCase).1] at isThread
    cases isThread
  | closed =>
    rw [state.lInvariant.closedHasNoThread statusCase] at isThread
    cases isThread
  | running =>
    cases runCase : dexConfig.run with
    | none => simp [statusCase, runCase] at status
    | some run =>
      simp only [statusCase, runCase] at status
      exact ⟨run, rfl, status⟩

end DexExpressiveness
