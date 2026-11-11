import DexExpressiveness.Failure.Composite.WPF5BackwardTimer

/-!
# WP-F5: Dex Complete rules from α of a run without failures

The real task of a current virtual task passes the fence, so the DexF Complete
rule commits the same result. It leaves no virtual task without a real task:
every other invocation that the commit cancels keeps its real task. So its
transition map is the Dex Complete rule alone.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- The Dex Complete rule removes its task. -/
theorem wpf5_dex_stepComplete_tasks (flow : Dex.FlowDef σ) (config : Dex.Config σ)
    (taskId : Nat) (events : List (Dex.Obs σ)) (next : Dex.Config σ)
    (isStep : Dex.step flow config (.stepComplete taskId) = some (events, next)) :
    ∀ task, task ∈ next.tasks → task.id ≠ taskId := by
  simp only [Dex.step] at isStep
  repeat' split at isStep
  all_goals first
    | (cases isStep; done)
    | (have isNext := congrArg (fun pair => pair.2) (Option.some.inj isStep)
       simp only at isNext
       rw [← isNext]
       intro task isTask
       simp only [Dex.Config.commit, Dex.Config.removeTask, List.mem_filter, Bool.not_eq_true',
         decide_eq_false_iff_not] at isTask
       exact isTask.2)

/-- A virtual task of α for a live started invocation. -/
theorem wpf5_mem_virtualTasks (config : Config σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) (arguments : Arguments σ)
    (isHistory : config.ghost.findInvocation? key = some history)
    (isArguments : history.arguments = some arguments) (isLive : history.outcome = .live) :
    ∃ virtualTask, virtualTask ∈ config.virtualTasks ∧
      virtualTask.id = arguments.virtualTaskId := by
  refine ⟨arguments.toTask key history.virtualPhase, ?_, rfl⟩
  simp only [Config.virtualTasks, mem_sortStable, List.mem_filterMap]
  refine ⟨(key, history), wpf5_findInvocation_mem _ _ _ isHistory, ?_⟩
  simp [isArguments, Config.hasVirtualTask, isLive]

theorem wpf5_abstractInvocation_method (run : Run σ) (ghost : Ghost σ)
    (record : Dex.StepRecord σ) (abstracted : Dex.Invocation)
    (isAbstract : run.abstractInvocation ghost record = some abstracted) :
    ∃ invocation, record.invocation = some invocation ∧ invocation.method = abstracted.method := by
  unfold Run.abstractInvocation at isAbstract
  cases isInvocation : record.invocation with
  | none => rw [isInvocation] at isAbstract; cases isAbstract
  | some invocation =>
    rw [isInvocation] at isAbstract
    refine ⟨invocation, rfl, ?_⟩
    cases isAttempt : run.findAttempt? record.id with
    | none =>
      simp only [isAttempt] at isAbstract
      cases isAbstract
      rfl
    | some attempt =>
      simp only [isAttempt] at isAbstract
      cases isHolds : attempt.snapshot.isSome <;>
        cases isArguments : ghost.argumentsOf (record.id, invocation.method) <;>
        simp only [isHolds, isArguments, Option.some.injEq] at isAbstract <;>
        (subst isAbstract; rfl)

theorem wpf5_free_stepComplete (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (virtualTaskId : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.stepComplete virtualTaskId) = some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.stepComplete virtualTaskId) dexEvents dexNext := by
  have dexTasks := wpf5_dex_stepComplete_tasks _ _ _ _ _ isDexStep
  have isDexStepOriginal := isDexStep
  cases hasRun : config.run with
  | none =>
    have absRun : (abstractConfig config).run = none := by simp [abstractConfig, hasRun]
    simp [Dex.step, absRun] at isDexStep
  | some run =>
    have absRun : (abstractConfig config).run = some (run.abstract config.ghost) := by
      simp [abstractConfig, hasRun]
    cases isFound : (abstractConfig config).findTask? virtualTaskId with
    | none => simp [Dex.step, absRun, isFound] at isDexStep
    | some virtualTask =>
      simp only [Dex.step, absRun, isFound] at isDexStep
      split at isDexStep
      case isFalse => cases isDexStep
      rename_i isEnabled
      simp only [Bool.and_eq_true] at isEnabled
      obtain ⟨isCurrent, canCommitAbstract⟩ := isEnabled
      have canCommit : config.canCommit = true := by
        rw [← wpf5_canCommit config shape]
        exact canCommitAbstract
      obtain ⟨task, history, arguments, isTask, isTaskId, isHistory, isArguments, isVirtualId,
        _, isPhase, isNotCommitted, isVirtual⟩ :=
        wpf5_virtualTask_lookup program config invariant shape virtualTaskId virtualTask isFound
      subst isVirtual isTaskId
      -- The invocation of the task is current, so its history is live.
      have isLive : history.outcome = .live := by
        simp only [Dex.Config.isTaskCurrent, absRun, Arguments.toTask, Task.key,
          wpf5_abstract_findStep] at isCurrent
        cases isRecord : run.base.findStep? task.base.step with
        | none => rw [isRecord] at isCurrent; cases isCurrent
        | some record =>
          rw [isRecord] at isCurrent
          simp only [Option.map_some, decide_eq_true_eq, Option.some.injEq] at isCurrent
          have isMember := List.mem_of_find?_eq_some isRecord
          have isRecordId : record.id = task.base.step := by
            simpa using List.find?_some isRecord
          obtain ⟨invocation, isInvocation, isMethod⟩ :=
            wpf5_abstractInvocation_method run config.ghost record _ (of_decide_eq_true isCurrent)
          have recordMatches := (invariant.runInvariant run hasRun).attemptsMatch record isMember
          unfold AttemptMatchesRecord at recordMatches
          rw [isInvocation] at recordMatches
          obtain ⟨_, found, _, isFoundHistory, isFoundLive, _⟩ := recordMatches
          try simp only at isMethod
          rw [isRecordId, isMethod] at isFoundHistory
          simp only [Task.key] at isHistory
          rw [isHistory] at isFoundHistory
          cases isFoundHistory
          exact isFoundLive
      have passesFence := invariant.liveTasksPassFence task history isTask isHistory isLive
      have isReal := (wpf5_findLiveTask config invariant shape task isTask).2
      have isServe := wpf5_canServeCommit config shape
      have hasNoRetryTimers := shape.noRetryTimers run hasRun
      obtain ⟨change, keepsRetryTimers, isStep⟩ : ∃ change : Run σ → Run σ,
          (∀ current : Run σ, current.retryTimers = [] → (change current).retryTimers = []) ∧
          step program config (.stepComplete task.base.id) =
            some ((config.removeRealTask task.base.id).commitRun run true (some task.key) []
              change) := by
        cases isTaskPhase : task.base.phase with
        | started =>
          cases isMethod : task.base.method <;>
            simp only [Arguments.toTask, Task.key, isMethod, ← isPhase, isTaskPhase] at isDexStep <;>
            cases isDexStep
        | running remaining runningResponse =>
          cases isMethod : task.base.method <;>
            simp only [Arguments.toTask, Task.key, isMethod, ← isPhase, isTaskPhase] at isDexStep <;>
            cases isDexStep
        | returned response =>
          cases isMethod : task.base.method with
          | waitFor =>
            cases response with
            | waitFor waitResponse =>
              refine ⟨fun current =>
                { current with
                  base := ((current.base.updateStep task.base.step (fun record =>
                      { record with invocation := none })).applyEffects waitResponse.effects
                    ).registerWait task.base.step waitResponse.wait config.clock },
                fun _ isEmpty => isEmpty, ?_⟩
              simp [step, isServe, canCommit, hasRun, isReal, passesFence, isMethod, isTaskPhase]
            | execute executeResponse =>
              simp only [Arguments.toTask, Task.key, isMethod, ← isPhase, isTaskPhase] at isDexStep
              cases isDexStep
          | execute =>
            cases response with
            | waitFor waitResponse =>
              simp only [Arguments.toTask, Task.key, isMethod, ← isPhase, isTaskPhase] at isDexStep
              cases isDexStep
            | execute executeResponse =>
              refine ⟨fun current =>
                { current with
                  base := ((current.base.updateStep task.base.step (fun record =>
                      { record with invocation := none })).applyEffects executeResponse.effects
                    ).applyDecision task.base.step executeResponse.decision },
                fun _ isEmpty => isEmpty, ?_⟩
              simp [step, isServe, canCommit, hasRun, isReal, passesFence, isMethod, isTaskPhase]
      have nextInvariant := invariant_step program ignoresAttemptContext config invariant _ _ _
        isStep
      have isNextTasks : ((config.removeRealTask task.base.id).commitRun run true (some task.key) []
          change).2.tasks = config.tasks.filter (fun other => !decide (other.base.id = task.base.id)) :=
        rfl
      have otherTask : ∀ other, other ∈ config.tasks → other.key ≠ task.key →
          other ∈ ((config.removeRealTask task.base.id).commitRun run true (some task.key) []
            change).2.tasks := by
        intro other isOther isOtherKey
        rw [isNextTasks]
        simp only [List.mem_filter, Bool.not_eq_true', decide_eq_false_iff_not]
        refine ⟨isOther, ?_⟩
        intro isSameId
        exact isOtherKey (by rw [wpf5_eq_of_id config invariant shape other task isOther isTask isSameId])
      have isAbandonedNil : abandonedVirtualTasks config
          ((config.removeRealTask task.base.id).commitRun run true (some task.key) [] change).2 =
          [] := by
        unfold abandonedVirtualTasks
        simp only
        rw [List.filterMap_eq_nil_iff.mpr]
        · rfl
        intro entry isEntry
        obtain ⟨key, after⟩ := entry
        simp only
        cases isAfterArguments : after.arguments with
        | none => rfl
        | some afterArguments =>
          cases isPrevious : config.ghost.findInvocation? key with
          | none => rfl
          | some previous =>
            simp only
            have isAfter := wpf5_get?_of_mem _ _ _ nextInvariant.ghostKeysNodup isEntry
            rcases wpf5_commitRun_historyEnds_of_after (config.removeRealTask task.base.id) run true
                (some task.key) [] change key after isAfter with
              ⟨before, isBefore, isArgumentsAfter, _, _, isOutcomeAfter⟩ | ⟨isNone, _⟩
            · have isBeforeBase : config.ghost.findInvocation? key = some before := isBefore
              rw [isPrevious] at isBeforeBase
              cases isBeforeBase
              by_cases isEnded : previous.outcome = .live ∧ after.outcome = .canceled
              · obtain ⟨isPreviousLive, isCanceled⟩ := isEnded
                have isOtherKey : key ≠ task.key := by
                  intro isSameKey
                  subst isSameKey
                  rcases isOutcomeAfter with isSame | ⟨_, isMarked⟩
                  · rw [isCanceled, isPreviousLive] at isSame
                    cases isSame
                  · rw [isCanceled] at isMarked
                    simp at isMarked
                obtain ⟨other, isOther, isOtherKeyEq⟩ := shape.liveHaveTasks key previous isPrevious
                  isPreviousLive (by rw [← isArgumentsAfter, isAfterArguments]; rfl)
                have isKept := otherTask other isOther (by rw [isOtherKeyEq]; exact isOtherKey)
                have hasVirtual : Config.hasVirtualTask
                    ((config.removeRealTask task.base.id).commitRun run true (some task.key) []
                      change).2 key after = true := by
                  simp only [Config.hasVirtualTask, isAfterArguments, isCanceled,
                    Option.isSome_some, Bool.true_and, Config.realTasks, List.any_eq_true,
                    decide_eq_true_eq]
                  exact ⟨other, List.mem_append_left _ isKept, isOtherKeyEq⟩
                simp [hasVirtual]
              · have isFalse : (decide (previous.outcome = .live) &&
                    decide (after.outcome = .canceled)) = false := by
                  cases isLiveCase : decide (previous.outcome = .live) <;>
                    cases isCanceledCase : decide (after.outcome = .canceled) <;>
                    simp_all
                simp [isFalse]
            · have isNoneBase : config.ghost.findInvocation? key = none := isNone
              rw [isPrevious] at isNoneBase
              cases isNoneBase
      have isMap : dexActionsOf program config (.stepComplete task.base.id) =
          [.stepComplete task.base.id] := by
        simp only [dexActionsOf, isStep, isAbandonedNil, Config.onVirtualTask,
          wpf5_virtualTaskIdOf config invariant shape task isTask history arguments isHistory
            isArguments, isVirtualId, List.append_nil]
      have isSimulated := step_simulated program ignoresAttemptContext config invariant _ rfl _ _
        isStep
      unfold IsStepSimulated at isSimulated
      rw [isMap] at isSimulated
      simp only [TransitionSystem.exec, Dex.system, isDexStepOriginal, List.append_nil,
        Option.some.injEq, Prod.mk.injEq] at isSimulated
      obtain ⟨isEvents, isNext⟩ := isSimulated
      have isErase := wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual)
        _ _ isStep
      refine ⟨_, _, _, isStep, by rw [← isErase, ← isEvents], isNext.symm, ?_, rfl, rfl, rfl⟩
      refine
        { isUp := shape.isUp
          noZombies := by simp [Config.commitRun, Config.removeRealTask, shape.noZombies]
          noInterrupts := shape.noInterrupts
          lastCommitIsNormal := rfl
          noRetryTimers := ?_
          nextTaskIds := shape.nextTaskIds
          tasksMatch := ?_
          liveHaveTasks := ?_
          taskKeysNodup := ?_ }
      · intro afterRun isAfterRun
        simp only [Config.commitRun, Option.some.injEq] at isAfterRun
        subst isAfterRun
        exact keepsRetryTimers _ hasNoRetryTimers
      · intro other isOtherNext
        have isOther : other ∈ config.tasks.filter
            (fun candidate => !decide (candidate.base.id = task.base.id)) := isOtherNext
        simp only [List.mem_filter, Bool.not_eq_true', decide_eq_false_iff_not] at isOther
        obtain ⟨isOtherMember, isOtherId⟩ := isOther
        have isOtherKey : other.key ≠ task.key := by
          intro isSameKey
          exact isOtherId (by
            rw [wpf5_eq_of_key config shape other task isOtherMember isTask isSameKey])
        obtain ⟨found, foundArguments, isFound, isFoundArguments, isFoundId, isFoundEmitted,
          isFoundPhase, isFoundNotCommitted⟩ := shape.tasksMatch other isOtherMember
        obtain ⟨after, isAfter, isArgumentsAfter, isEmittedAfter, isProgressAfter,
          isOutcomeAfter⟩ :=
          wpf5_commitRun_historyEnds_of_some (config.removeRealTask task.base.id) run true
            (some task.key) [] change other.key found isFound
        refine ⟨after, foundArguments, isAfter, by rw [isArgumentsAfter, isFoundArguments],
          isFoundId, by rw [isEmittedAfter, isFoundEmitted], ?_, ?_⟩
        · rw [isFoundPhase]
          simp only [InvocationHistory.virtualPhase, isEmittedAfter, isProgressAfter]
        · rcases isOutcomeAfter with isSame | ⟨_, isMarked⟩
          · rw [isSame]
            exact isFoundNotCommitted
          · rw [isMarked]
            have isNotSome : ¬ (some task.key = some other.key) := by
              intro isSome
              exact isOtherKey (Option.some.inj isSome).symm
            simp [isNotSome]
      · intro key after isAfter isAfterLive hasArguments
        rcases wpf5_commitRun_historyEnds_of_after (config.removeRealTask task.base.id) run true
            (some task.key) [] change key after isAfter with
          ⟨before, isBefore, isArgumentsAfter, _, _, isOutcomeAfter⟩ | ⟨_, _, isFresh⟩
        · have isBeforeLive : before.outcome = .live := by
            rcases isOutcomeAfter with isSame | ⟨isLiveBefore, _⟩
            · rw [← isSame]
              exact isAfterLive
            · exact isLiveBefore
          by_cases isKey : key = task.key
          · exfalso
            subst isKey
            have isBeforeBase : config.ghost.findInvocation? task.key = some before := isBefore
            rw [isHistory] at isBeforeBase
            cases isBeforeBase
            have isAfterArguments : after.arguments = some arguments := by
              rw [isArgumentsAfter, isArguments]
            obtain ⟨virtualTask, isVirtualMember, isVirtualTaskId⟩ :=
              wpf5_mem_virtualTasks _ task.key after arguments isAfter isAfterArguments isAfterLive
            have isDexMember : virtualTask ∈ dexNext.tasks := by
              rw [isNext]
              exact isVirtualMember
            exact dexTasks virtualTask isDexMember (by rw [isVirtualTaskId, isVirtualId])
          · obtain ⟨other, isOther, isOtherKeyEq⟩ := shape.liveHaveTasks key before isBefore
              isBeforeLive (by rw [← isArgumentsAfter]; exact hasArguments)
            exact ⟨other, otherTask other isOther (by rw [isOtherKeyEq]; exact isKey), isOtherKeyEq⟩
        · rw [isFresh] at hasArguments
          cases hasArguments
      · show (List.map Task.key (config.tasks.filter
            (fun candidate => !decide (candidate.base.id = task.base.id)))).Nodup
        exact shape.taskKeysNodup.sublist (List.Sublist.map _ List.filter_sublist)

end DexExpressiveness.DexF
