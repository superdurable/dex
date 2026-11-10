import DexExpressiveness.L.InvariantStepLemmas

/-!
# Helper lemmas for work package 1: one thread-local step

What `completeStatement`, `resumeBlocked`, and `resumeTimed` do to the facts
that `L.Invariant` tracks for one thread:

- the thread ID stays, and the fresh counter grows only by `fork` and
  `newMutex`, which use the old counter for the new name;
- every handle in the new locals, stack, and code was already held, was
  evaluated from the locals, or is the handle of the mutex that the statement
  creates (`IsValidResult`);
- a statement that releases a mutex is one that requires it.

It also relates the phase clause `Thread.PhaseFitsCode` to the code.
-/

namespace DexExpressiveness.L

/-! ## Thread ID and fresh counter -/

/-- A return keeps the thread ID and the counter, and asks for no action on a
mutex. -/
theorem returnValue_shape (core : ThreadCore) (value : Val) :
    (returnValue core value).core.tid = core.tid ∧
    (returnValue core value).core.freshCounter = core.freshCounter := by
  simp only [returnValue]
  repeat' split
  all_goals simp

/-- Completing a statement keeps the thread ID. The counter grows by one for
`fork` and `newMutex`, whose new name is built from the old counter, and stays
otherwise. -/
theorem completeStatement_shape (program : Program) (core : ThreadCore) (code : Code)
    (loaded : Val) :
    (completeStatement program core code loaded).core.tid = core.tid ∧
    core.freshCounter ≤ (completeStatement program core code loaded).core.freshCounter ∧
    (∀ child childCode,
      (completeStatement program core code loaded).intent = .fork child childCode →
      child.tid = core.tid ++ [core.freshCounter] ∧
      (completeStatement program core code loaded).core.freshCounter = core.freshCounter + 1) ∧
    (∀ mutex, (completeStatement program core code loaded).intent = .newMutex mutex →
      mutex = .dynamic core.tid core.freshCounter ∧
      (completeStatement program core code loaded).core.freshCounter = core.freshCounter + 1) := by
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

/-- The continuation of a blocking statement keeps the thread ID and the
counter, and asks for no action on shared state except the end of the thread. -/
theorem resumeBlocked_shape (core : ThreadCore) (code : Code) (outcome : WakeOutcome) :
    (resumeBlocked core code outcome).core.tid = core.tid ∧
    (resumeBlocked core code outcome).core.freshCounter = core.freshCounter ∧
    ((resumeBlocked core code outcome).intent = .silent ∨
      (resumeBlocked core code outcome).intent = .endThread ∨
      (resumeBlocked core code outcome).intent = .halt) := by
  simp only [resumeBlocked]
  split <;> simp [ThreadCore.setLocal]

/-- The continuation of a timed operation keeps the thread ID and the counter,
and asks for no action on shared state except the end of the thread. -/
theorem resumeTimed_shape (core : ThreadCore) (code : Code) (outcome : WakeOutcome) :
    (resumeTimed core code outcome).core.tid = core.tid ∧
    (resumeTimed core code outcome).core.freshCounter = core.freshCounter ∧
    ((resumeTimed core code outcome).intent = .silent ∨
      (resumeTimed core code outcome).intent = .endThread ∨
      (resumeTimed core code outcome).intent = .halt) := by
  simp only [resumeTimed]
  split <;> simp [ThreadCore.setLocal]

/-! ## Handles after a thread-local step -/

namespace Config

/-- The result of a statement holds valid handles only: in the thread's new
state and code, in the value a `store` writes, and in the state and code of
the thread a `fork` starts. -/
def IsValidResult (config : Config) (result : StatementResult) : Prop :=
  config.IsValidState result.core result.code ∧
  (∀ target value, result.intent = .store target value → config.IsValidVal value) ∧
  (∀ child childCode, result.intent = .fork child childCode →
    config.IsValidState child childCode)

/-- A return holds valid handles only. -/
theorem isValidResult_returnValue (config : Config) (core : ThreadCore) (value : Val)
    (localsValid : ∀ entry, entry ∈ core.locals → config.IsValidVal entry.2)
    (stackValid : ∀ frame, frame ∈ core.stack → config.IsValidFrame frame)
    (valueValid : config.IsValidVal value) : config.IsValidResult (returnValue core value) := by
  simp only [returnValue]
  split
  · rename_i resultVar savedLocals returnCode outerFrames isUnwound
    obtain ⟨savedValid, returnValid, outerValid⟩ :=
      isValid_unwindToCall config core.stack stackValid _ _ _ _ isUnwound
    exact ⟨⟨isValidVal_of_mem_set config _ _ _ savedValid valueValid, outerValid, returnValid⟩,
      by simp, by simp⟩
  · split <;>
      exact ⟨⟨localsValid, stackValid, fun _ isMember => nomatch isMember⟩, by simp, by simp⟩

/-- Completing a statement never forges a handle. `createdExists` covers
`newMutex`: the handle it puts into a local names the mutex it creates. -/
theorem isValidResult_completeStatement (program : Program) (config : Config)
    (core : ThreadCore) (code : Code) (loaded : Val) (isValid : config.IsValidState core code)
    (loadedValid : config.IsValidVal loaded)
    (createdExists : ∀ mutex,
      (completeStatement program core code loaded).intent = .newMutex mutex →
      config.IsExistingMutex mutex) :
    config.IsValidResult (completeStatement program core code loaded) := by
  obtain ⟨localsValid, stackValid, codeValid⟩ := isValid
  have nilValid : ∀ instr, instr ∈ ([] : Code) → config.IsValidInstr instr :=
    fun _ isMember => nomatch isMember
  have nilFramesValid : ∀ frame, frame ∈ ([] : List Frame) → config.IsValidFrame frame :=
    fun _ isMember => nomatch isMember
  cases code with
  | nil =>
    simp only [completeStatement]
    split
    · rename_i exceptionVar handlerCode afterCode outerFrames isStack
      have frameValid := stackValid (.handler exceptionVar handlerCode afterCode)
        (by rw [isStack]; exact List.mem_cons_self ..)
      exact ⟨⟨localsValid,
        fun frame isMember => stackValid frame (by rw [isStack]; exact List.mem_cons_of_mem _ isMember),
        frameValid.2⟩, by simp, by simp⟩
    · exact isValidResult_returnValue config core .null localsValid stackValid trivial
  | cons instr rest =>
    have restValid : ∀ later, later ∈ rest → config.IsValidInstr later :=
      fun later isMember => codeValid later (List.mem_cons_of_mem _ isMember)
    cases instr with
    | park cv => exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp [completeStatement],
        by simp [completeStatement]⟩
    | relock mutex => exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp [completeStatement],
        by simp [completeStatement]⟩
    | source statement =>
      cases statement with
      | call result procName arguments =>
        simp only [completeStatement]
        split
        · rename_i proc values isProc isValues
          split
          · rename_i bound isBound
            refine ⟨⟨isValidVal_bindParams config _ _ _ isBound
              (isValidVal_evalArguments config _ localsValid _ _ isValues), ?_,
              isValidInstr_pushCode config _ _ nilValid⟩, by simp, by simp⟩
            intro frame isMember
            rcases List.mem_cons.mp isMember with isHead | isInRest
            · rw [isHead]
              exact ⟨localsValid, restValid⟩
            · exact stackValid frame isInRest
          · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
        · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
      | ret value =>
        simp only [completeStatement]
        split
        · rename_i result isValue
          exact isValidResult_returnValue config core result localsValid stackValid
            (isValidVal_evalExpr config _ localsValid _ _ isValue)
        · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
      | fork handle procName arguments =>
        simp only [completeStatement]
        split
        · rename_i proc values isProc isValues
          split
          · rename_i bound isBound
            refine ⟨⟨isValidVal_of_mem_set config _ _ _ localsValid trivial, stackValid,
              restValid⟩, by simp, ?_⟩
            intro child childCode isIntent
            simp only [Intent.fork.injEq] at isIntent
            obtain ⟨isChild, isChildCode⟩ := isIntent
            rw [← isChild, ← isChildCode]
            exact ⟨isValidVal_bindParams config _ _ _ isBound
              (isValidVal_evalArguments config _ localsValid _ _ isValues),
              nilFramesValid, isValidInstr_pushCode config _ _ nilValid⟩
          · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
        · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
      | newMutex target =>
        simp only [completeStatement] at createdExists ⊢
        exact ⟨⟨isValidVal_of_mem_set config _ _ _ localsValid (createdExists _ rfl), stackValid,
          restValid⟩, by simp, by simp⟩
      | wait cv mutex =>
        simp only [completeStatement]
        split
        · rename_i cvId mutexId isCv isMutex
          split
          · rename_i sameMutex
            refine ⟨⟨localsValid, stackValid, ?_⟩, by simp, by simp⟩
            intro instr isMember
            simp only [List.mem_cons] at isMember
            rcases isMember with isPark | isRelock | isInRest
            · rw [isPark]
              exact isValidVal_evalExpr config _ localsValid _ _ isCv
            · rw [isRelock]
              exact isValidVal_evalExpr config _ localsValid _ _ isMutex
            · exact restValid instr isInRest
          · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
        · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
      | raise exception =>
        simp only [completeStatement]
        split
        · rename_i thrown isThrown
          split
          · rename_i exceptionVar handlerCode restoredLocals outerFrames isUnwound
            obtain ⟨handlerValid, restoredValid, outerValid⟩ :=
              isValid_unwindToHandler config core.stack stackValid core.locals localsValid
                _ _ _ _ isUnwound
            exact ⟨⟨isValidVal_of_mem_set config _ _ _ restoredValid
              (isValidVal_evalExpr config _ localsValid _ _ isThrown), outerValid, handlerValid⟩,
              by simp, by simp⟩
          · split
            · exact ⟨⟨localsValid, stackValid, nilValid⟩, by simp, by simp⟩
            · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
        · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
      | tryCatch body exceptionVar handler =>
        simp only [completeStatement]
        refine ⟨⟨localsValid, ?_, isValidInstr_pushCode config _ _ nilValid⟩, by simp, by simp⟩
        intro frame isMember
        rcases List.mem_cons.mp isMember with isHead | isInRest
        · rw [isHead]
          exact ⟨isValidInstr_pushCode config _ _ restValid, restValid⟩
        · exact stackValid frame isInRest
      | store target value =>
        simp only [completeStatement]
        split
        · rename_i result isValue
          refine ⟨⟨localsValid, stackValid, restValid⟩, ?_, by simp⟩
          intro storeTarget storeValue isIntent
          simp only [Intent.store.injEq] at isIntent
          rw [← isIntent.2]
          exact isValidVal_evalExpr config _ localsValid _ _ isValue
        · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
      | assign target value =>
        simp only [completeStatement]
        split
        · rename_i result isValue
          exact ⟨⟨isValidVal_of_mem_set config _ _ _ localsValid
            (isValidVal_evalExpr config _ localsValid _ _ isValue), stackValid, restValid⟩,
            by simp, by simp⟩
        · exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
      | load target source =>
        simp only [completeStatement]
        exact ⟨⟨isValidVal_of_mem_set config _ _ _ localsValid loadedValid, stackValid, restValid⟩,
          by simp, by simp⟩
      | seq first second =>
        simp only [completeStatement]
        exact ⟨⟨localsValid, stackValid,
          isValidInstr_pushCode config _ _ (isValidInstr_pushCode config _ _ restValid)⟩,
          by simp, by simp⟩
      | _ =>
        simp only [completeStatement]
        repeat' split
        all_goals first
          | exact ⟨⟨localsValid, stackValid, codeValid⟩, by simp, by simp⟩
          | exact ⟨⟨localsValid, stackValid, restValid⟩, by simp, by simp⟩
          | exact ⟨⟨localsValid, stackValid, nilValid⟩, by simp, by simp⟩
          | exact ⟨⟨localsValid, stackValid, isValidInstr_pushCode config _ _ restValid⟩,
              by simp, by simp⟩
          | exact ⟨⟨localsValid, stackValid, isValidInstr_pushCode config _ _ codeValid⟩,
              by simp, by simp⟩

/-- The continuation of a blocking statement holds valid handles only: the
received message is the only new value. -/
theorem isValidState_resumeBlocked (config : Config) (core : ThreadCore) (code : Code)
    (outcome : WakeOutcome) (isValid : config.IsValidState core code)
    (outcomeValid : config.IsValidPhase (.resuming outcome)) :
    config.IsValidState (resumeBlocked core code outcome).core
      (resumeBlocked core code outcome).code := by
  have nilValid : ∀ instr, instr ∈ ([] : Code) → config.IsValidInstr instr :=
    fun _ isMember => nomatch isMember
  simp only [resumeBlocked]
  split
  all_goals
    obtain ⟨localsValid, stackValid, codeValid⟩ := isValid
    first
      | exact ⟨localsValid, stackValid, codeValid⟩
      | exact ⟨localsValid, stackValid, nilValid⟩
      | exact ⟨localsValid, stackValid,
          fun later isMember => codeValid later (List.mem_cons_of_mem _ isMember)⟩
      | exact ⟨isValidVal_of_mem_set config _ _ _ localsValid outcomeValid, stackValid,
          fun later isMember => codeValid later (List.mem_cons_of_mem _ isMember)⟩

/-- The continuation of a timed operation holds valid handles only. -/
theorem isValidState_resumeTimed (config : Config) (core : ThreadCore) (code : Code)
    (outcome : WakeOutcome) (isValid : config.IsValidState core code)
    (outcomeValid : config.IsValidPhase (.resuming outcome)) :
    config.IsValidState (resumeTimed core code outcome).core
      (resumeTimed core code outcome).code := by
  have nilValid : ∀ instr, instr ∈ ([] : Code) → config.IsValidInstr instr :=
    fun _ isMember => nomatch isMember
  simp only [resumeTimed]
  split
  all_goals
    obtain ⟨localsValid, stackValid, codeValid⟩ := isValid
    first
      | exact ⟨localsValid, stackValid, codeValid⟩
      | exact ⟨localsValid, stackValid, nilValid⟩
      | exact ⟨localsValid, stackValid,
          fun later isMember => codeValid later (List.mem_cons_of_mem _ isMember)⟩
      | exact ⟨localsValid, stackValid, isValidInstr_pushCode config _ _
          (fun later isMember => codeValid later (List.mem_cons_of_mem _ isMember))⟩
      | exact ⟨isValidVal_of_mem_set config _ _ _ localsValid outcomeValid, stackValid,
          isValidInstr_pushCode config _ _
            (fun later isMember => codeValid later (List.mem_cons_of_mem _ isMember))⟩

/-- The mutex or condition variable that a blocking statement waits for
exists: its handle is evaluated from the locals or stands in the code. -/
theorem isExistingMutex_of_waitRequest (config : Config) (core : ThreadCore) (code : Code)
    (isValid : config.IsValidState core code) :
    (∀ mutex, waitRequest core code = some (.lock mutex) → config.IsExistingMutex mutex) ∧
    (∀ cv, waitRequest core code = some (.park cv) → config.IsExistingMutex cv.mutex) := by
  obtain ⟨localsValid, stackValid, codeValid⟩ := isValid
  cases code with
  | nil => simp [waitRequest]
  | cons instr rest =>
    have headValid := codeValid instr (List.mem_cons_self ..)
    cases instr with
    | park cv =>
      refine ⟨by simp [waitRequest], ?_⟩
      intro parked isRequest
      simp only [waitRequest, Option.some.injEq, WaitRequest.park.injEq] at isRequest
      rw [← isRequest]
      exact headValid
    | relock mutex =>
      refine ⟨?_, by simp [waitRequest]⟩
      intro locked isRequest
      simp only [waitRequest, Option.some.injEq, WaitRequest.lock.injEq] at isRequest
      rw [← isRequest]
      exact headValid
    | source statement =>
      cases statement with
      | lock mutex =>
        refine ⟨?_, ?_⟩
        · intro locked isRequest
          simp only [waitRequest] at isRequest
          split at isRequest
          · rename_i mutexId isMutex
            simp only [Option.some.injEq, WaitRequest.lock.injEq] at isRequest
            rw [← isRequest]
            exact isValidVal_evalExpr config _ localsValid _ _ isMutex
          · cases isRequest
        · intro cv isRequest
          simp only [waitRequest] at isRequest
          split at isRequest <;> cases isRequest
      | join thread =>
        refine ⟨?_, ?_⟩ <;>
        · intro _ isRequest
          simp only [waitRequest] at isRequest
          split at isRequest <;> cases isRequest
      | _ => simp [waitRequest]

end Config

/-! ## The phase and the code -/

/-- A statement with a wait request is a blocking statement. -/
theorem isBlockingHead_of_waitRequest (core : ThreadCore) (code : Code) (request : WaitRequest)
    (isRequest : waitRequest core code = some request) : isBlockingHead code = true := by
  cases code with
  | nil => simp [waitRequest] at isRequest
  | cons instr rest =>
    cases instr with
    | park cv => rfl
    | relock mutex => rfl
    | source statement => cases statement <;> first | rfl | simp [waitRequest] at isRequest

/-- A statement that requires a mutex begins with nothing observable: it is
not a `load` and not an `emit`. -/
theorem beginKind_of_requiredMutex (core : ThreadCore) (code : Code) (mutex : MutexId)
    (isRequired : requiredMutex core code = some mutex) :
    ∀ effect, beginKind core code ≠ .emit effect := by
  cases code with
  | nil => simp [beginKind]
  | cons instr rest =>
    cases instr with
    | park cv => simp [beginKind]
    | relock mutex => simp [beginKind]
    | source statement =>
      cases statement <;> first
        | (simp [beginKind]; done)
        | simp [requiredMutex, cvOperand] at isRequired

/-- A blocked thread, woken, is still at a blocking statement or inside a
timed operation. -/
theorem Thread.timer_or_blockingHead_of_blocked (thread : Thread) (on : BlockedOn) (since : Nat)
    (fits : thread.PhaseFitsCode) (isBlocked : thread.phase = .blocked on since) :
    thread.timer.isSome = true ∨ isBlockingHead thread.code = true := by
  simp only [Thread.PhaseFitsCode, isBlocked] at fits
  cases on with
  | timed number deadline chan =>
    obtain ⟨⟨seconds, isTimer⟩, _⟩ := fits
    left
    simp [isTimer]
  | lock mutex => exact Or.inr (isBlockingHead_of_waitRequest _ _ _ fits.2)
  | recv chan => exact Or.inr (isBlockingHead_of_waitRequest _ _ _ fits.2)
  | join target => exact Or.inr (isBlockingHead_of_waitRequest _ _ _ fits.2)
  | park cv => exact Or.inr (isBlockingHead_of_waitRequest _ _ _ fits.2)

end DexExpressiveness.L
