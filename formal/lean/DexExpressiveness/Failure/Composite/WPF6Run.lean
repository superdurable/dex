import DexExpressiveness.Failure.Composite.WPF6Abstraction
import DexExpressiveness.Dex.RunInvariant

/-!
# WP-F6: a fair DexF run settles

Facts along an infinite DexF run with the fairness assumptions of spec
§11.5:

- every configuration satisfies the invariants;
- there is a quiet point after which the server is up, `I` is empty, and no
  failure that FF counts and no [RECOVER] occurs ("The server is eventually
  up for good");
- every invocation eventually stops being live, every real task (also a
  zombie) eventually leaves for good, and so every virtual task of α
  eventually leaves and every pending invocation of α eventually stops
  being pending.

These give F3, F4, and F5 of the Dex run that α maps the DexF run to.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Helpers -/

/-- A property that holds at `first` and passes to the next index holds from
`first` on. -/
theorem wpf6e_holds_from {holdsAt : Nat → Prop} (first : Nat) (holdsFirst : holdsAt first)
    (holdsNext : ∀ index, first ≤ index → holdsAt index → holdsAt (index + 1)) :
    ∀ index, first ≤ index → holdsAt index := by
  intro index isLater
  obtain ⟨extra, isIndex⟩ := Nat.exists_eq_add_of_le isLater
  subst isIndex
  clear isLater
  induction extra with
  | zero => simpa using holdsFirst
  | succ extra inductionHypothesis =>
    exact holdsNext (first + extra) (Nat.le_add_right _ _) inductionHypothesis

/-- Finitely many properties that each hold from some index on all hold from
one common index on. -/
theorem wpf6e_common_bound {α : Type} (holdsAt : α → Nat → Prop) (elements : List α)
    (start : Nat)
    (eventually : ∀ element, element ∈ elements →
      ∃ bound, ∀ index, bound ≤ index → holdsAt element index) :
    ∃ bound, start ≤ bound ∧
      ∀ index, bound ≤ index → ∀ element, element ∈ elements → holdsAt element index := by
  induction elements with
  | nil => exact ⟨start, Nat.le_refl _, fun _ _ _ isMember => nomatch isMember⟩
  | cons head rest inductionHypothesis =>
    obtain ⟨headBound, headHolds⟩ := eventually head (by simp)
    obtain ⟨restBound, isAfter, restHolds⟩ := inductionHypothesis
      (fun element isMember => eventually element (List.mem_cons_of_mem _ isMember))
    refine ⟨headBound + restBound, by omega, ?_⟩
    intro index isLater element isMember
    rcases List.mem_cons.mp isMember with isHead | isInRest
    · subst isHead
      exact headHolds index (by omega)
    · exact restHolds index (by omega) element isInRest

/-- A binding found by `get?` is a member of the map. -/
theorem wpf6e_mem_of_get? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (isFound : AssocList.get? map key = some value) : (key, value) ∈ map := by
  induction map with
  | nil => cases isFound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · subst isKey
      simp only [AssocList.get?, ite_true, Option.some.injEq] at isFound
      subst isFound
      exact List.mem_cons_self
    · simp only [AssocList.get?, isKey, ite_false] at isFound
      exact List.mem_cons_of_mem _ (inductionHypothesis isFound)

/-- Two ghost entries whose arguments share a virtual task ID are the same entry. -/
theorem wpf6e_eq_of_virtualTaskId (invocations : AssocList (InvocationKey σ) (InvocationHistory σ))
    (isNodup : ((invocations.filterMap (fun entry => entry.2.arguments)).map
      (fun arguments => arguments.virtualTaskId)).Nodup)
    (first second : InvocationKey σ × InvocationHistory σ) (isFirst : first ∈ invocations)
    (isSecond : second ∈ invocations) (firstArguments secondArguments : Arguments σ)
    (hasFirst : first.2.arguments = some firstArguments)
    (hasSecond : second.2.arguments = some secondArguments)
    (sameId : firstArguments.virtualTaskId = secondArguments.virtualTaskId) : first = second := by
  induction invocations with
  | nil => cases isFirst
  | cons head rest inductionHypothesis =>
    have isInImage : ∀ entry arguments, entry ∈ rest → entry.2.arguments = some arguments →
        arguments.virtualTaskId ∈
          (rest.filterMap (fun entry => entry.2.arguments)).map
            (fun arguments => arguments.virtualTaskId) :=
      fun entry arguments isMember hasArguments =>
        List.mem_map_of_mem (List.mem_filterMap.mpr ⟨entry, isMember, hasArguments⟩)
    rcases List.mem_cons.mp isFirst with isFirstHead | isFirstRest <;>
      rcases List.mem_cons.mp isSecond with isSecondHead | isSecondRest
    · rw [isFirstHead, isSecondHead]
    · subst isFirstHead
      simp only [List.filterMap_cons, hasFirst, List.map_cons, List.nodup_cons] at isNodup
      exact absurd (sameId ▸ isInImage second secondArguments isSecondRest hasSecond) isNodup.1
    · subst isSecondHead
      simp only [List.filterMap_cons, hasSecond, List.map_cons, List.nodup_cons] at isNodup
      exact absurd (sameId.symm ▸ isInImage first firstArguments isFirstRest hasFirst) isNodup.1
    · apply inductionHypothesis _ isFirstRest isSecondRest
      cases hasHead : head.2.arguments with
      | none => simpa only [List.filterMap_cons, hasHead] using isNodup
      | some headArguments =>
        simp only [List.filterMap_cons, hasHead, List.map_cons, List.nodup_cons] at isNodup
        exact isNodup.2

/-- A task of `Θ` is a real task. -/
theorem wpf6e_mem_realTasks (config : Config σ) (task : Task σ) (isMember : task ∈ config.tasks) :
    task ∈ config.realTasks :=
  List.mem_append_left _ isMember

/-- The `index`-th transition of a run is a step. -/
theorem wpf6e_step_at (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (index : Nat) :
    ∃ events, step program (run.states index) (run.labels index) =
      some (events, run.states (index + 1)) :=
  isRun.2 index

/-- [RECOVER-INTERRUPT] is disabled when `I` is empty. -/
theorem wpf6e_recoverInterrupt_disabled (program : Program σ) (config : Config σ)
    (isEmpty : config.interrupts = []) (interrupt : Interrupt σ) (decision : RetryDecision) :
    step program config (.recoverInterrupt interrupt decision) = none := by
  simp [step, isEmpty]

/-- With `I` empty and no counted failure, a label is neither a failed attempt
nor an interrupt. -/
theorem wpf6e_quiet_labels (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (index : Nat)
    (isEmpty : (run.states index).interrupts = [])
    (isNotCounted : Fairness.Action.isCountedFailure (run.labels index) = false) :
    (∀ stepId decision, run.labels index ≠ .attemptFailed stepId decision) ∧
    (∀ interrupt decision, run.labels index ≠ .recoverInterrupt interrupt decision) := by
  constructor
  · intro stepId decision isLabel
    rw [isLabel] at isNotCounted
    cases isNotCounted
  · intro interrupt decision isLabel
    obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
    rw [isLabel, wpf6e_recoverInterrupt_disabled program _ isEmpty] at isStep
    cases isStep

/-! ## Invariants -/

/-- Every configuration of a DexF run satisfies the invariants. -/
theorem wpf6_invariant_at (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (index : Nat) : Invariant program (run.states index) := by
  induction index with
  | zero =>
    rw [show run.states 0 = Config.initial σ from isRun.1]
    exact invariant_initial program
  | succ index inductionHypothesis =>
    obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
    exact invariant_step program ignoresAttemptContext _ inductionHypothesis _ events _ isStep

/-! ## The quiet point -/

/-- A server that is up and sees no [CRASH] stays up. -/
theorem wpf6e_up_persists (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (first : Nat)
    (isUpFirst : (run.states first).isUp = true)
    (noCrash : ∀ index, first ≤ index → run.labels index ≠ .crash) :
    ∀ index, first ≤ index → (run.states index).isUp = true := by
  apply wpf6e_holds_from (holdsAt := fun index => (run.states index).isUp = true) first isUpFirst
  intro index isLater isUp
  obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
  have frame := wpf6_isUp_step program _ _ events _ isStep
  by_cases isRecover : run.labels index = .recover
  · exact (frame.2 isRecover).2
  · rw [frame.1 (noCrash index isLater) isRecover]
    exact isUp

/-- A down server had a [CRASH] with no [RECOVER] since. -/
theorem wpf6e_down_has_crash (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) :
    ∀ index, (run.states index).isUp = false →
      ∃ crashIndex, crashIndex < index ∧ run.labels crashIndex = .crash ∧
        ∀ between, crashIndex < between → between < index → run.labels between ≠ .recover := by
  intro index
  induction index with
  | zero =>
    intro isDown
    rw [show run.states 0 = Config.initial σ from isRun.1] at isDown
    cases isDown
  | succ index inductionHypothesis =>
    intro isDown
    obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
    have frame := wpf6_isUp_step program _ _ events _ isStep
    by_cases isRecover : run.labels index = .recover
    · rw [(frame.2 isRecover).2] at isDown
      cases isDown
    · by_cases isCrash : run.labels index = .crash
      · exact ⟨index, Nat.lt_succ_self _, isCrash, fun between isAfter isBefore => by omega⟩
      · rw [frame.1 isCrash isRecover] at isDown
        obtain ⟨crashIndex, isBefore, isCrashLabel, noRecover⟩ := inductionHypothesis isDown
        refine ⟨crashIndex, by omega, isCrashLabel, fun between isAfter isBeforeNext => ?_⟩
        by_cases isLast : between = index
        · rw [isLast]
          exact isRecover
        · exact noRecover between isAfter (by omega)

/-- In a fair run the server is eventually up for good, with no counted failure. -/
theorem wpf6e_eventually_up (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (isFair : IsFair run) :
    ∃ upPoint, ∀ index, upPoint ≤ index →
      (run.states index).isUp = true ∧
        Fairness.Action.isCountedFailure (run.labels index) = false := by
  obtain ⟨_, _, _, _, _, _, ⟨bound, noFailure⟩, recovers, _⟩ := isFair
  have noCrash : ∀ index, bound ≤ index → run.labels index ≠ .crash := by
    intro index isLater isCrash
    have isNotCounted := noFailure index isLater
    rw [isCrash] at isNotCounted
    cases isNotCounted
  cases isUpBound : (run.states bound).isUp with
  | true =>
    exact ⟨bound, fun index isLater =>
      ⟨wpf6e_up_persists program run isRun bound isUpBound noCrash index isLater,
        noFailure index isLater⟩⟩
  | false =>
    obtain ⟨crashIndex, _, isCrash, noRecover⟩ :=
      wpf6e_down_has_crash program run isRun bound isUpBound
    obtain ⟨recoverIndex, isAfterCrash, isRecover⟩ := recovers crashIndex isCrash
    have isAfterBound : bound ≤ recoverIndex := by
      apply Classical.byContradiction
      intro isBefore
      exact noRecover recoverIndex isAfterCrash (by omega) isRecover
    obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun recoverIndex
    have isUpNext := ((wpf6_isUp_step program _ _ events _ isStep).2 isRecover).2
    exact ⟨recoverIndex + 1, fun index isLater =>
      ⟨wpf6e_up_persists program run isRun (recoverIndex + 1) isUpNext
          (fun later isLaterIndex => noCrash later (by omega)) index isLater,
        noFailure index (by omega)⟩⟩

/-- The quiet point of a fair run: from it on the server is up, `I` is empty,
and no [CRASH], [ATTEMPT-LOST], [ATTEMPT-EXHAUSTED], [ATTEMPT-RETURNED], or
[RECOVER] occurs. -/
theorem wpf6_exists_quietPoint (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) :
    ∃ quiet, ∀ index, quiet ≤ index →
      (run.states index).isUp = true ∧ (run.states index).interrupts = [] ∧
      Fairness.Action.isCountedFailure (run.labels index) = false ∧
      run.labels index ≠ .recover := by
  obtain ⟨upPoint, isUpAfter⟩ := wpf6e_eventually_up program run isRun isFair
  have notRecover : ∀ index, upPoint ≤ index → run.labels index ≠ .recover := by
    intro index isLater isRecover
    obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
    have isDown := ((wpf6_isUp_step program _ _ events _ isStep).2 isRecover).1
    rw [(isUpAfter index isLater).1] at isDown
    cases isDown
  have shrinks : ∀ index, upPoint ≤ index → ∀ interrupt,
      interrupt ∈ (run.states (index + 1)).interrupts →
        interrupt ∈ (run.states index).interrupts := by
    intro index isLater
    obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
    exact wpf6_interrupts_step program _ _ events _ isStep (notRecover index isLater)
  have staysOut : ∀ interrupt first, upPoint ≤ first →
      interrupt ∉ (run.states first).interrupts →
      ∀ index, first ≤ index → interrupt ∉ (run.states index).interrupts := by
    intro interrupt first isLater isOut
    apply wpf6e_holds_from (holdsAt := fun index => interrupt ∉ (run.states index).interrupts)
      first isOut
    intro index isAfter isOutNow isIn
    exact isOutNow (shrinks index (by omega) interrupt isIn)
  have eventuallyOut : ∀ interrupt, interrupt ∈ (run.states upPoint).interrupts →
      ∃ bound, ∀ index, bound ≤ index → interrupt ∉ (run.states index).interrupts := by
    intro interrupt isIn
    obtain ⟨later, isLater, isOut⟩ := isFair.2.2.2.2.2.2.2.2 interrupt upPoint isIn
    exact ⟨later, staysOut interrupt later isLater isOut⟩
  have isInAtUp : ∀ interrupt extra, interrupt ∈ (run.states (upPoint + extra)).interrupts →
      interrupt ∈ (run.states upPoint).interrupts := by
    intro interrupt extra
    induction extra with
    | zero => exact id
    | succ extra inductionHypothesis =>
      intro isIn
      exact inductionHypothesis (shrinks (upPoint + extra) (Nat.le_add_right _ _) interrupt isIn)
  obtain ⟨quiet, isAfterUp, allOut⟩ := wpf6e_common_bound
    (fun interrupt index => interrupt ∉ (run.states index).interrupts)
    (run.states upPoint).interrupts upPoint eventuallyOut
  refine ⟨quiet, fun index isLater =>
    ⟨(isUpAfter index (by omega)).1, ?_, (isUpAfter index (by omega)).2,
      notRecover index (by omega)⟩⟩
  cases isInterrupts : (run.states index).interrupts with
  | nil => rfl
  | cons head rest =>
    exfalso
    have isHeadIn : head ∈ (run.states index).interrupts := by
      rw [isInterrupts]
      exact List.mem_cons_self
    obtain ⟨extra, isIndex⟩ := Nat.exists_eq_add_of_le (show upPoint ≤ index by omega)
    have isInUp := isInAtUp head extra (isIndex ▸ isHeadIn)
    exact allOut index isLater head isInUp isHeadIn

/-! ## Real tasks -/

/-- A real task with an ID after a step was there before with that ID, or has
the next fresh ID. -/
theorem wpf6e_present_back (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (taskId index : Nat)
    (isPresent : ∃ task, task ∈ (run.states (index + 1)).realTasks ∧ task.base.id = taskId) :
    (∃ task, task ∈ (run.states index).realTasks ∧ task.base.id = taskId) ∨
      taskId = (run.states index).nextTaskId := by
  obtain ⟨nextTask, isMember, isId⟩ := isPresent
  obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
  rcases wpf6_realTask_step program _
      (wpf6_invariant_at program ignoresAttemptContext run isRun index) _ events _ isStep
      nextTask isMember with
    ⟨task, isOld, isSameId, _, _⟩ | ⟨isMinted, _⟩
  · exact Or.inl ⟨task, isOld, isSameId.trans isId⟩
  · exact Or.inr (isId ▸ isMinted)

/-- A `Returned` real task that is still there after a step is still `Returned`. -/
theorem wpf6e_returned_next (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (taskId index : Nat) (isReturned : Fairness.IsTaskReturned run taskId index)
    (isPresent : ∃ task, task ∈ (run.states (index + 1)).realTasks ∧ task.base.id = taskId) :
    Fairness.IsTaskReturned run taskId (index + 1) := by
  obtain ⟨returnedTask, response, isFound, isPhase⟩ := isReturned
  obtain ⟨nextTask, isMember, isId⟩ := isPresent
  have invariant := wpf6_invariant_at program ignoresAttemptContext run isRun index
  have nextInvariant := wpf6_invariant_at program ignoresAttemptContext run isRun (index + 1)
  obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
  have isFoundMember := (wpf6_findRealTask?_iff program _ invariant taskId returnedTask).mp isFound
  rcases wpf6_realTask_step program _ invariant _ events _ isStep nextTask isMember with
    ⟨task, isOld, isSameId, _, keepsReturned⟩ | ⟨isMinted, _⟩
  · have isFoundOld := (wpf6_findRealTask?_iff program _ invariant taskId task).mpr
      ⟨isOld, isSameId.trans isId⟩
    rw [isFound] at isFoundOld
    have isSame : returnedTask = task := Option.some.inj isFoundOld
    subst isSame
    exact ⟨nextTask, response,
      (wpf6_findRealTask?_iff program _ nextInvariant taskId nextTask).mpr ⟨isMember, isId⟩,
      keepsReturned response isPhase⟩
  · have isBelow := invariant.realTaskIdsBelow returnedTask isFoundMember.1
    have isSameId := isFoundMember.2
    omega

/-- A real task ID cannot stay present forever in a fair run. -/
theorem wpf6e_not_present_forever (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (taskId first : Nat)
    (isPresent : ∀ index, first ≤ index →
      ∃ task, task ∈ (run.states index).realTasks ∧ task.base.id = taskId) : False := by
  obtain ⟨_, _, _, settles, commits, _⟩ := isFair
  have returnedOfSettled : ∀ index, first ≤ index →
      ¬ Fairness.IsTaskUnsettled run taskId index → Fairness.IsTaskReturned run taskId index := by
    intro index isLater isSettled
    obtain ⟨task, isMember, isId⟩ := isPresent index isLater
    have isFound := (wpf6_findRealTask?_iff program _
      (wpf6_invariant_at program ignoresAttemptContext run isRun index) taskId task).mpr
      ⟨isMember, isId⟩
    cases isPhase : task.base.phase with
    | returned response => exact ⟨task, response, isFound, isPhase⟩
    | started =>
      exact absurd ⟨task, isFound, fun response isReturnedPhase => by
        rw [isPhase] at isReturnedPhase
        cases isReturnedPhase⟩ isSettled
    | running remaining response =>
      exact absurd ⟨task, isFound, fun returnedResponse isReturnedPhase => by
        rw [isPhase] at isReturnedPhase
        cases isReturnedPhase⟩ isSettled
  have existsReturned : ∃ returnedAt, first ≤ returnedAt ∧
      Fairness.IsTaskReturned run taskId returnedAt := by
    by_cases isUnsettled : Fairness.IsTaskUnsettled run taskId first
    · obtain ⟨later, isLater, isSettled⟩ := settles taskId first isUnsettled
      exact ⟨later, isLater, returnedOfSettled later isLater isSettled⟩
    · exact ⟨first, Nat.le_refl _, returnedOfSettled first (Nat.le_refl _) isUnsettled⟩
  obtain ⟨returnedAt, isAfter, isReturned⟩ := existsReturned
  have staysReturned := wpf6e_holds_from
    (holdsAt := fun index => Fairness.IsTaskReturned run taskId index) returnedAt isReturned
    (fun index isLater isReturnedNow =>
      wpf6e_returned_next program ignoresAttemptContext run isRun taskId index isReturnedNow
        (isPresent (index + 1) (by omega)))
  obtain ⟨later, isLater, isNotReturned⟩ := commits taskId returnedAt isReturned
  exact isNotReturned (staysReturned later isLater)

/-! ## Live invocations end -/

/-- In a fair run every invocation eventually stops being live: it starts,
returns, and commits, or a cancel or a close ends it. -/
theorem wpf6_live_eventually_ends (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (key : InvocationKey σ) (start : Nat) :
    ∃ index, start ≤ index ∧ ∀ history,
      (run.states index).ghost.findInvocation? key = some history → history.outcome ≠ .live := by
  obtain ⟨stepId, method⟩ := key
  apply Classical.byContradiction
  intro notEnds
  have isLive : ∀ index, start ≤ index → ∃ history,
      (run.states index).ghost.findInvocation? (stepId, method) = some history ∧
        history.outcome = .live := by
    intro index isLater
    apply Classical.byContradiction
    intro notLive
    exact notEnds ⟨index, isLater, fun history isHistory isLiveOutcome =>
      notLive ⟨history, isHistory, isLiveOutcome⟩⟩
  obtain ⟨quietPoint, isQuiet⟩ :=
    wpf6_exists_quietPoint program ignoresAttemptContext run isRun isFair
  have facts : ∀ index, start ≤ index → ∃ flowRun record state attempt,
      (run.states index).run = some flowRun ∧ flowRun.base.findStep? stepId = some record ∧
      record.invocation = some { method := method, state := state } ∧
      flowRun.findAttempt? stepId = some attempt ∧
      (attempt.isBackoff = true → state = .pending ∧
        flowRun.retryTimers.contains
          { step := stepId, method := method, attempt := attempt.number } = true) := by
    intro index isLater
    obtain ⟨history, isHistory, isLiveOutcome⟩ := isLive index isLater
    have invariant := wpf6_invariant_at program ignoresAttemptContext run isRun index
    cases hasRun : (run.states index).run with
    | none =>
      have isEmpty := (invariant.notStarted hasRun).2.2.2.1
      simp [Ghost.findInvocation?, isEmpty, AssocList.get?] at isHistory
    | some flowRun =>
      have runInvariant := invariant.runInvariant flowRun hasRun
      obtain ⟨record, state, isMember, isId, isInvocation⟩ :=
        runInvariant.liveIsCurrent _ history isHistory isLiveOutcome
      have isStepId : record.id = stepId := isId
      have isFound := Dex.Run.findStep?_of_mem_nodup flowRun.base record isMember
        runInvariant.stepIdsNodup
      have isMatch := runInvariant.attemptsMatch record isMember
      simp only [AttemptMatchesRecord, isInvocation] at isMatch
      obtain ⟨attempt, _, isAttempt, _, _, _, _, _, _, isBackoffArmed⟩ := isMatch
      rw [isStepId] at isFound isAttempt isBackoffArmed
      exact ⟨flowRun, record, state, attempt, rfl, isFound, isInvocation, isAttempt,
        isBackoffArmed⟩
  have quietLabels : ∀ index, start + quietPoint ≤ index →
      (∀ stepId decision, run.labels index ≠ .attemptFailed stepId decision) ∧
      (∀ interrupt decision, run.labels index ≠ .recoverInterrupt interrupt decision) :=
    fun index isLater => wpf6e_quiet_labels program run isRun index
      (isQuiet index (by omega)).2.1 (isQuiet index (by omega)).2.2.1
  -- Step 1: the invocation eventually runs as some task.
  have existsRunning : ∃ runningAt taskId, start + quietPoint ≤ runningAt ∧
      ∃ flowRun record, (run.states runningAt).run = some flowRun ∧
        flowRun.base.findStep? stepId = some record ∧
        record.invocation = some { method := method, state := .running taskId } := by
    apply Classical.byContradiction
    intro notRunning
    have isPending : ∀ index, start + quietPoint ≤ index → ∃ flowRun record attempt,
        (run.states index).run = some flowRun ∧ flowRun.base.findStep? stepId = some record ∧
        record.invocation = some { method := method, state := .pending } ∧
        flowRun.findAttempt? stepId = some attempt ∧
        (attempt.isBackoff = true → flowRun.retryTimers.contains
          { step := stepId, method := method, attempt := attempt.number } = true) := by
      intro index isLater
      obtain ⟨flowRun, record, state, attempt, hasRun, isFound, isInvocation, isAttempt,
        isBackoffArmed⟩ := facts index (by omega)
      cases state with
      | pending =>
        exact ⟨flowRun, record, attempt, hasRun, isFound, isInvocation, isAttempt,
          fun isBackoff => (isBackoffArmed isBackoff).2⟩
      | running taskId =>
        exact (notRunning
          ⟨index, taskId, isLater, flowRun, record, hasRun, isFound, isInvocation⟩).elim
    by_cases existsReady : ∃ readyAt, start + quietPoint ≤ readyAt ∧ ∃ flowRun attempt,
        (run.states readyAt).run = some flowRun ∧ flowRun.findAttempt? stepId = some attempt ∧
        attempt.isBackoff = false
    · -- 1b: out of Backoff it stays out, so it stays startable.
      obtain ⟨readyAt, isAfter, isReady⟩ := existsReady
      have staysReady := wpf6e_holds_from (holdsAt := fun index => ∃ flowRun attempt,
          (run.states index).run = some flowRun ∧ flowRun.findAttempt? stepId = some attempt ∧
          attempt.isBackoff = false) readyAt isReady (fun index isLater isReadyNow => by
        obtain ⟨nextRun, _, nextAttempt, hasNextRun, _, _, isNextAttempt, _⟩ :=
          isPending (index + 1) (by omega)
        refine ⟨nextRun, nextAttempt, hasNextRun, isNextAttempt, ?_⟩
        cases isNextBackoff : nextAttempt.isBackoff with
        | false => rfl
        | true =>
          exfalso
          obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
          obtain ⟨oldRun, oldAttempt, hasOldRun, isOldAttempt, isOldBackoff, _⟩ :=
            wpf6_backoff_step program _
              (wpf6_invariant_at program ignoresAttemptContext run isRun index) _ events _ isStep
              (quietLabels index (by omega)).1 (quietLabels index (by omega)).2 nextRun
              hasNextRun stepId nextAttempt isNextAttempt isNextBackoff
          obtain ⟨flowRun, attempt, hasRun, isAttempt, isNotBackoff⟩ := isReadyNow
          rw [hasRun] at hasOldRun
          cases hasOldRun
          rw [isAttempt] at isOldAttempt
          cases isOldAttempt
          rw [isNotBackoff] at isOldBackoff
          cases isOldBackoff)
      have isStartable : ∀ index, readyAt ≤ index →
          Fairness.IsInvocationStartable run stepId method index := by
        intro index isLater
        obtain ⟨flowRun, record, _, hasRun, isFound, isInvocation, _, _⟩ :=
          isPending index (by omega)
        obtain ⟨readyRun, attempt, hasReadyRun, isAttempt, isNotBackoff⟩ :=
          staysReady index isLater
        rw [hasRun] at hasReadyRun
        cases hasReadyRun
        exact ⟨(isQuiet index (by omega)).1, flowRun, record, attempt, hasRun, isFound,
          isInvocation, isAttempt, isNotBackoff⟩
      obtain ⟨later, isLater, notStartable⟩ :=
        isFair.2.2.1 stepId method readyAt (isStartable readyAt (Nat.le_refl _))
      exact notStartable (isStartable later isLater)
    · -- 1a: in Backoff forever, its retry timer stays armed and becomes due.
      have allBackoff : ∀ index, start + quietPoint ≤ index → ∀ flowRun attempt,
          (run.states index).run = some flowRun → flowRun.findAttempt? stepId = some attempt →
          attempt.isBackoff = true := by
        intro index isLater flowRun attempt hasRun isAttempt
        cases isBackoff : attempt.isBackoff with
        | true => rfl
        | false =>
          exact (existsReady ⟨index, isLater, flowRun, attempt, hasRun, isAttempt, isBackoff⟩).elim
      obtain ⟨firstRun, _, firstAttempt, hasFirstRun, _, _, isFirstAttempt, _⟩ :=
        isPending (start + quietPoint) (Nat.le_refl _)
      have numberIsConstant := wpf6e_holds_from (holdsAt := fun index => ∀ flowRun attempt,
          (run.states index).run = some flowRun → flowRun.findAttempt? stepId = some attempt →
          attempt.number = firstAttempt.number) (start + quietPoint)
        (fun flowRun attempt hasRun isAttempt => by
          rw [hasFirstRun] at hasRun
          cases hasRun
          rw [isFirstAttempt] at isAttempt
          cases isAttempt
          rfl)
        (fun index isLater isConstant flowRun attempt hasRun isAttempt => by
          obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
          obtain ⟨oldRun, oldAttempt, hasOldRun, isOldAttempt, _, isSameNumber⟩ :=
            wpf6_backoff_step program _
              (wpf6_invariant_at program ignoresAttemptContext run isRun index) _ events _ isStep
              (quietLabels index isLater).1 (quietLabels index isLater).2 flowRun hasRun stepId
              attempt isAttempt (allBackoff (index + 1) (by omega) flowRun attempt hasRun isAttempt)
          rw [← isSameNumber]
          exact isConstant oldRun oldAttempt hasOldRun isOldAttempt)
      have isArmed : ∀ index, start + quietPoint ≤ index → ∃ flowRun fireTime,
          (run.states index).run = some flowRun ∧
          flowRun.retryTimers.get?
            { step := stepId, method := method, attempt := firstAttempt.number } =
              some fireTime := by
        intro index isLater
        obtain ⟨flowRun, _, attempt, hasRun, _, _, isAttempt, isBackoffArmed⟩ :=
          isPending index isLater
        have isContained :=
          isBackoffArmed (allBackoff index isLater flowRun attempt hasRun isAttempt)
        rw [numberIsConstant index isLater flowRun attempt hasRun isAttempt] at isContained
        simp only [AssocList.contains, Option.isSome_iff_exists] at isContained
        obtain ⟨fireTime, isFireTime⟩ := isContained
        exact ⟨flowRun, fireTime, hasRun, isFireTime⟩
      obtain ⟨armedRun, firstFireTime, hasArmedRun, isFirstFireTime⟩ :=
        isArmed (start + quietPoint) (Nat.le_refl _)
      have fireTimeIsConstant := wpf6e_holds_from (holdsAt := fun index => ∀ flowRun fireTime,
          (run.states index).run = some flowRun →
          flowRun.retryTimers.get?
            { step := stepId, method := method, attempt := firstAttempt.number } =
              some fireTime → fireTime = firstFireTime) (start + quietPoint)
        (fun flowRun fireTime hasRun isFireTime => by
          rw [hasArmedRun] at hasRun
          cases hasRun
          rw [isFirstFireTime] at isFireTime
          cases isFireTime
          rfl)
        (fun index isLater isConstant flowRun fireTime hasRun isFireTime => by
          obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
          obtain ⟨oldRun, hasOldRun, isOldFireTime⟩ :=
            wpf6_retryTimer_step program _
              (wpf6_invariant_at program ignoresAttemptContext run isRun index) _ events _ isStep
              (quietLabels index isLater).1 (quietLabels index isLater).2 flowRun hasRun _
              fireTime isFireTime
          exact isConstant oldRun fireTime hasOldRun isOldFireTime)
      obtain ⟨clockIndex, isAboveFireTime⟩ := isFair.1 firstFireTime
      have clockAbove := wpf6e_holds_from
        (holdsAt := fun index => firstFireTime < (run.states index).clock) clockIndex
        isAboveFireTime (fun index _ isAboveNow => by
          obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
          exact Nat.lt_of_lt_of_le isAboveNow (wpf6_clock_step program _ _ events _ isStep).1)
      have isDue : ∀ index, start + quietPoint + clockIndex ≤ index →
          Fairness.IsTimerArmedAndDue run
            (.retry { step := stepId, method := method, attempt := firstAttempt.number })
            index := by
        intro index isLater
        obtain ⟨flowRun, fireTime, hasRun, isFireTime⟩ := isArmed index (by omega)
        have isSameFireTime :=
          fireTimeIsConstant index (by omega) flowRun fireTime hasRun isFireTime
        have isAboveNow := clockAbove index (by omega)
        exact ⟨flowRun, fireTime, hasRun, isFireTime, by omega⟩
      obtain ⟨later, isLater, notDue⟩ := isFair.2.1 _ (start + quietPoint + clockIndex)
        (isDue _ (Nat.le_refl _))
      exact notDue (isDue later isLater)
  -- Step 2: the running task stays in `Θ` while the invocation is live.
  obtain ⟨runningAt, taskId, isAfterQuiet, flowRun, record, hasRun, isFound, isRunning⟩ :=
    existsRunning
  have inTasksAtStart : ∃ task, task ∈ (run.states runningAt).tasks ∧ task.base.id = taskId ∧
      task.key = (stepId, method) := by
    have runInvariant := (wpf6_invariant_at program ignoresAttemptContext run isRun
      runningAt).runInvariant flowRun hasRun
    obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? flowRun.base stepId record isFound
    rcases runInvariant.runningIsTracked record method taskId isMember isRunning with
      ⟨task, isTask, isTaskId⟩ | isDown | ⟨_, _, isInterrupt⟩
    · have isKey := (runInvariant.runningTaskMatches record method taskId task isMember isRunning
        (wpf6e_mem_realTasks _ task isTask) isTaskId).1
      rw [isId] at isKey
      exact ⟨task, isTask, isTaskId, isKey⟩
    · rw [(isQuiet runningAt (by omega)).1] at isDown
      cases isDown
    · rw [(isQuiet runningAt (by omega)).2.1] at isInterrupt
      cases isInterrupt
  have staysInTasks := wpf6e_holds_from (holdsAt := fun index => ∃ task,
      task ∈ (run.states index).tasks ∧ task.base.id = taskId ∧ task.key = (stepId, method))
    runningAt inTasksAtStart (fun index isLater isInTasks => by
      obtain ⟨task, isTask, isTaskId, isKey⟩ := isInTasks
      have invariant := wpf6_invariant_at program ignoresAttemptContext run isRun index
      obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
      obtain ⟨history, isHistory, isLiveOutcome⟩ := isLive index (by omega)
      rw [← isKey] at isHistory
      have isPassed := invariant.liveTasksPassFence task history isTask isHistory isLiveOutcome
      by_cases isComplete : run.labels index = .stepComplete task.base.id
      · exfalso
        rw [isComplete] at isStep
        obtain ⟨nextHistory, isNextHistory, isNextLive⟩ := isLive (index + 1) (by omega)
        rw [← isKey] at isNextHistory
        exact wpf6_stepComplete_ends program _ invariant task.base.id events _ isStep
          (wpf6_invariant_at program ignoresAttemptContext run isRun (index + 1)) task
          ((wpf6_findRealTask?_iff program _ invariant task.base.id task).mpr
            ⟨wpf6e_mem_realTasks _ task isTask, rfl⟩)
          nextHistory isNextHistory isNextLive
      · have isNotAbandon : ∀ isLastOfCanceled,
            run.labels index ≠ .workerAbandon task.base.id isLastOfCanceled := by
          intro isLastOfCanceled isLabel
          rw [isLabel, wpf6_workerAbandon_disabled program _ invariant task isTask isPassed]
            at isStep
          cases isStep
        have isNotStale : ∀ isLastOfCanceled,
            run.labels index ≠ .resultStale task.base.id isLastOfCanceled := by
          intro isLastOfCanceled isLabel
          rw [isLabel, wpf6_resultStale_disabled program _ invariant task isTask isPassed]
            at isStep
          cases isStep
        obtain ⟨nextTask, isNextTask, isNextId, isNextKey⟩ :=
          wpf6_liveTask_step program _ invariant _ events _ isStep task isTask isComplete
            isNotAbandon isNotStale (isQuiet index (by omega)).2.2.1
            (quietLabels index (by omega)).2
        exact ⟨nextTask, isNextTask, isNextId.trans isTaskId, isNextKey.trans isKey⟩)
  -- Step 3: a task that stays forever contradicts F4 and F5.
  exact wpf6e_not_present_forever program ignoresAttemptContext run isRun isFair taskId runningAt
    (fun index isLater => by
      obtain ⟨task, isTask, isTaskId, _⟩ := staysInTasks index isLater
      exact ⟨task, wpf6e_mem_realTasks _ task isTask, isTaskId⟩)

/-- In a fair run every real task ID, of `Θ` or `Z`, is eventually gone for
good ("zombies settle"). -/
theorem wpf6_realTask_eventually_gone (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (taskId start : Nat) :
    ∃ index, start ≤ index ∧ ∀ later, index ≤ later →
      ∀ task, task ∈ (run.states later).realTasks → task.base.id ≠ taskId := by
  by_cases isEverPresent : ∃ index, start ≤ index ∧
      ∃ task, task ∈ (run.states index).realTasks ∧ task.base.id = taskId
  · obtain ⟨presentAt, isAfter, task, isMember, isId⟩ := isEverPresent
    have isBelow : taskId < (run.states presentAt).nextTaskId :=
      isId ▸ (wpf6_invariant_at program ignoresAttemptContext run isRun presentAt).realTaskIdsBelow
        task isMember
    have isGoneSomewhere : ∃ goneAt, presentAt ≤ goneAt ∧
        ¬ ∃ task, task ∈ (run.states goneAt).realTasks ∧ task.base.id = taskId := by
      apply Classical.byContradiction
      intro isNeverGone
      apply wpf6e_not_present_forever program ignoresAttemptContext run isRun isFair taskId
        presentAt
      intro index isLater
      apply Classical.byContradiction
      intro isGone
      exact isNeverGone ⟨index, isLater, isGone⟩
    obtain ⟨goneAt, isLater, isGone⟩ := isGoneSomewhere
    have nextAbove : ∀ index, presentAt ≤ index → taskId < (run.states index).nextTaskId :=
      wpf6e_holds_from (holdsAt := fun index => taskId < (run.states index).nextTaskId) presentAt
        isBelow (fun index _ isBelowNow => by
          obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
          exact Nat.lt_of_lt_of_le isBelowNow (wpf6_nextTaskId_step program _ _ events _ isStep))
    have staysGone := wpf6e_holds_from
      (holdsAt := fun index =>
        ¬ ∃ task, task ∈ (run.states index).realTasks ∧ task.base.id = taskId)
      goneAt isGone (fun index isLaterIndex isGoneNow isPresentNext => by
        rcases wpf6e_present_back program ignoresAttemptContext run isRun taskId index
            isPresentNext with isPresentNow | isMinted
        · exact isGoneNow isPresentNow
        · have isAbove := nextAbove index (by omega)
          omega)
    exact ⟨goneAt, by omega, fun later isLaterGone task isMember isId =>
      staysGone later isLaterGone ⟨task, isMember, isId⟩⟩
  · exact ⟨start, Nat.le_refl _, fun later isLater task isMember isId =>
      isEverPresent ⟨later, isLater, task, isMember, isId⟩⟩

/-! ## Virtual tasks and pending invocations under α -/

/-- A ghost history stays along a run, with a non-live outcome and its
arguments kept. -/
theorem wpf6e_history_persists (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (key : InvocationKey σ) (first : Nat) (history : InvocationHistory σ)
    (isHistory : (run.states first).ghost.findInvocation? key = some history) :
    ∀ index, first ≤ index → ∃ laterHistory,
      (run.states index).ghost.findInvocation? key = some laterHistory ∧
      (history.outcome ≠ .live → laterHistory.outcome = history.outcome) ∧
      (∀ arguments, history.arguments = some arguments →
        laterHistory.arguments = some arguments) := by
  apply wpf6e_holds_from (holdsAt := fun index => ∃ laterHistory,
      (run.states index).ghost.findInvocation? key = some laterHistory ∧
      (history.outcome ≠ .live → laterHistory.outcome = history.outcome) ∧
      (∀ arguments, history.arguments = some arguments →
        laterHistory.arguments = some arguments))
    first ⟨history, isHistory, fun _ => rfl, fun _ hasArguments => hasArguments⟩
  intro index _ isKept
  obtain ⟨laterHistory, isLaterHistory, keepsOutcome, keepsArguments⟩ := isKept
  obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
  obtain ⟨nextHistory, isNextHistory, keepsNextOutcome, keepsNextArguments⟩ :=
    wpf6_history_step program _ (wpf6_invariant_at program ignoresAttemptContext run isRun index)
      _ events _ isStep key laterHistory isLaterHistory
  refine ⟨nextHistory, isNextHistory, fun isNotLive => ?_, fun arguments hasArguments =>
    keepsNextArguments arguments (keepsArguments arguments hasArguments)⟩
  have isSame := keepsOutcome isNotLive
  rw [keepsNextOutcome (isSame ▸ isNotLive), isSame]

/-- In a fair run no virtual task of α stays forever. -/
theorem wpf6_virtualTask_eventually_gone (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (virtualTaskId start : Nat) :
    ∃ index, start ≤ index ∧ (abstractConfig (run.states index)).findTask? virtualTaskId = none := by
  cases isFoundStart : (abstractConfig (run.states start)).findTask? virtualTaskId with
  | none => exact ⟨start, Nat.le_refl _, isFoundStart⟩
  | some startTask =>
    obtain ⟨key, history, arguments, isHistory, hasArguments, isVirtualId, _⟩ :=
      wpf6_virtualTask_of_abstract program _
        (wpf6_invariant_at program ignoresAttemptContext run isRun start) virtualTaskId startTask
        isFoundStart
    have persists :=
      wpf6e_history_persists program ignoresAttemptContext run isRun key start history isHistory
    -- Where α has the virtual task, it is the virtual task of `key`.
    have keyOf : ∀ index, start ≤ index → ∀ dexTask,
        (abstractConfig (run.states index)).findTask? virtualTaskId = some dexTask →
        ∃ laterHistory, (run.states index).ghost.findInvocation? key = some laterHistory ∧
          (run.states index).hasVirtualTask key laterHistory = true := by
      intro index isLater dexTask isFound
      have invariant := wpf6_invariant_at program ignoresAttemptContext run isRun index
      obtain ⟨otherKey, otherHistory, otherArguments, isOtherHistory, hasOtherArguments,
        isOtherVirtualId, hasVirtual⟩ :=
        wpf6_virtualTask_of_abstract program _ invariant virtualTaskId dexTask isFound
      obtain ⟨laterHistory, isLaterHistory, _, keepsArguments⟩ := persists index isLater
      have isSameEntry := wpf6e_eq_of_virtualTaskId _ invariant.virtualTaskIdsNodup
        (otherKey, otherHistory) (key, laterHistory)
        (wpf6e_mem_of_get? _ _ _ isOtherHistory) (wpf6e_mem_of_get? _ _ _ isLaterHistory)
        otherArguments arguments hasOtherArguments (keepsArguments arguments hasArguments)
        (isOtherVirtualId.trans isVirtualId.symm)
      simp only [Prod.mk.injEq] at isSameEntry
      obtain ⟨isSameKey, isSameHistory⟩ := isSameEntry
      subst isSameKey
      subst isSameHistory
      exact ⟨otherHistory, isLaterHistory, hasVirtual⟩
    obtain ⟨endedAt, isAfterStart, isEnded⟩ :=
      wpf6_live_eventually_ends program ignoresAttemptContext run isRun isFair key start
    obtain ⟨endedHistory, isEndedHistory, _, _⟩ := persists endedAt isAfterStart
    have isNotLive := isEnded endedHistory isEndedHistory
    have endedPersists := wpf6e_history_persists program ignoresAttemptContext run isRun key
      endedAt endedHistory isEndedHistory
    cases isOutcome : endedHistory.outcome with
    | live => exact absurd isOutcome isNotLive
    | committed =>
      refine ⟨endedAt, isAfterStart, ?_⟩
      cases isFound : (abstractConfig (run.states endedAt)).findTask? virtualTaskId with
      | none => rfl
      | some dexTask =>
        obtain ⟨laterHistory, isLaterHistory, hasVirtual⟩ :=
          keyOf endedAt isAfterStart dexTask isFound
        rw [isEndedHistory] at isLaterHistory
        cases isLaterHistory
        simp [Config.hasVirtualTask, isOutcome] at hasVirtual
    | canceled =>
      have staysCanceled : ∀ index, endedAt ≤ index → ∃ laterHistory,
          (run.states index).ghost.findInvocation? key = some laterHistory ∧
            laterHistory.outcome = .canceled := by
        intro index isLater
        obtain ⟨laterHistory, isLaterHistory, keepsOutcome, _⟩ := endedPersists index isLater
        exact ⟨laterHistory, isLaterHistory, (keepsOutcome isNotLive).trans isOutcome⟩
      -- No new real task of `key` appears once it is canceled.
      have backToEnded := wpf6e_holds_from (holdsAt := fun index => ∀ task,
          task ∈ (run.states index).realTasks → task.key = key →
          task.base.id ∈ (run.states endedAt).realTasks.map (fun task => task.base.id))
        endedAt (fun task isMember _ => List.mem_map_of_mem isMember)
        (fun index isLater isBack nextTask isNextMember isNextKey => by
          obtain ⟨events, isStep⟩ := wpf6e_step_at program run isRun index
          rcases wpf6_realTask_step program _
              (wpf6_invariant_at program ignoresAttemptContext run isRun index) _ events _
              isStep nextTask isNextMember with
            ⟨task, isOld, isSameId, isSameKey, _⟩ | ⟨_, mintedHistory, isMintedHistory,
              isMintedLive⟩
          · rw [← isSameId]
            exact isBack task isOld (isSameKey.trans isNextKey)
          · obtain ⟨canceledHistory, isCanceledHistory, isCanceled⟩ := staysCanceled index isLater
            rw [isNextKey, isCanceledHistory] at isMintedHistory
            cases isMintedHistory
            rw [isCanceled] at isMintedLive
            cases isMintedLive)
      obtain ⟨goneAt, isAfterEnded, allGone⟩ := wpf6e_common_bound
        (fun taskId index => ∀ task, task ∈ (run.states index).realTasks → task.base.id ≠ taskId)
        ((run.states endedAt).realTasks.map (fun task => task.base.id)) endedAt
        (fun taskId _ => by
          obtain ⟨goneIndex, _, isGone⟩ := wpf6_realTask_eventually_gone program
            ignoresAttemptContext run isRun isFair taskId endedAt
          exact ⟨goneIndex, isGone⟩)
      refine ⟨goneAt, by omega, ?_⟩
      cases isFound : (abstractConfig (run.states goneAt)).findTask? virtualTaskId with
      | none => rfl
      | some dexTask =>
        exfalso
        obtain ⟨laterHistory, isLaterHistory, hasVirtual⟩ :=
          keyOf goneAt (by omega) dexTask isFound
        obtain ⟨canceledHistory, isCanceledHistory, isCanceled⟩ := staysCanceled goneAt isAfterEnded
        rw [isCanceledHistory] at isLaterHistory
        cases isLaterHistory
        simp only [Config.hasVirtualTask, isCanceled, Bool.and_eq_true, List.any_eq_true,
          decide_eq_true_eq] at hasVirtual
        obtain ⟨_, task, isMember, isKey⟩ := hasVirtual
        exact allGone goneAt (Nat.le_refl _) task.base.id
          (backToEnded goneAt isAfterEnded task isMember isKey) task isMember rfl

/-- In a fair run no invocation stays pending under α forever. -/
theorem wpf6_abstractPending_eventually_ends (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (stepId : Dex.StepExecId σ) (method : Dex.Method) (start : Nat) :
    ∃ index, start ≤ index ∧ ∀ dexRun record,
      (abstractConfig (run.states index)).run = some dexRun → dexRun.findStep? stepId = some record →
      record.invocation ≠ some { method := method, state := .pending } := by
  obtain ⟨index, isLater, isEnded⟩ :=
    wpf6_live_eventually_ends program ignoresAttemptContext run isRun isFair (stepId, method) start
  refine ⟨index, isLater, fun dexRun record hasRun isFound isPending => ?_⟩
  obtain ⟨_, _, history, _, _, _, isHistory, isLiveOutcome⟩ := wpf6_pending_of_abstract program _
    (wpf6_invariant_at program ignoresAttemptContext run isRun index) stepId method dexRun record
    hasRun isFound isPending
  exact isEnded history isHistory isLiveOutcome

end DexExpressiveness.DexF
