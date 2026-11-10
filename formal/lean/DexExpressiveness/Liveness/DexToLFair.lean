import DexExpressiveness.Liveness.DexToLRun
import DexExpressiveness.Liveness.DexToLFairLemmas

/-!
# Liveness, work package L4: the L run of a fair Dex run is fair

One lemma per fairness assumption of L (spec §5.5, last paragraph;
`L/Semantics.lean`), each from the Dex assumption it corresponds to:

| L assumption | From the Dex assumptions |
| --- | --- |
| F1 `TimeDiverges` | F1 `TimeDiverges` |
| F2 `TimersFire` | F2 `TimersFire` |
| F3 `ThreadsBegin` | F3 `StepsStart` |
| F4 `EmitsSettle` | F4 `WorkersSettle` |
| F5 `ActionsComplete` | F4 `WorkersSettle` and F5 `ResultsCommit` |

Each proof reads an L fact off the simulation state at a block boundary
(`LFollowsDex.isState`), follows the Dex run until the Dex fact fails, and
reads the L fact off again. `LRunOfDexRun.isFair` is proved here from the
five lemmas. The five lemmas are proved. Their helper lemmas are in three
files:

- `Liveness/DexToLFairSteps.lean`: what one Dex transition keeps of a pending
  invocation and of a task.
- `Liveness/DexToLFairLate.lean`: an L configuration that is not closed has
  no late emit.
- `Liveness/DexToLFairLemmas.lean`: the Dex object of each L fact, and that it
  stays the same along the Dex run while the L fact holds.

F2 needs no step argument: the timer number names the Dex timer. F3 follows
the pending invocation of the thread's Step. F4 and F5 follow the thread's
task. F5 has two stages: the task returns (Dex F4), and then it leaves `Θ`
(Dex F5); it is the same task in between, because a thread has at most one
task and a transition that drops a task adds none. After the close, the late
emits are the effects that the tasks hold, and no transition adds one.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

variable {program : Program}
  {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
  {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
  {boundary : Nat → Nat}

/-- F1: the L clock is the Dex clock. -/
theorem LRunOfDexRun.timeDiverges (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (timeDiverges : Dex.Fairness.TimeDiverges dexRun) : L.Fairness.TimeDiverges lRun := by
  intro bound
  obtain ⟨index, isAbove⟩ := timeDiverges bound
  refine ⟨boundary index, ?_⟩
  rw [(built.isState index).isRelated.clock]
  exact isAbove

/-- F2: an armed and due L timer is an armed and due Dex timer. -/
theorem LRunOfDexRun.timersFire (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (timersFire : Dex.Fairness.TimersFire dexRun) : L.Fairness.TimersFire lRun := by
  intro thread number
  apply built.eventuallyFails_of_imp _
    (Dex.Fairness.IsTimerArmedAndDue dexRun
      { step := { stepType := StepType.timed, number := number }, index := 0 }) ?_ (timersFire _)
  intro index ⟨current, deadline, chan, since, isFound, isBlocked, isDue⟩
  obtain ⟨run, hasRun, isArmed⟩ := (built.isState index).timer_of_blocked isFound isBlocked
  refine ⟨run, deadline, hasRun, isArmed, ?_⟩
  rw [← (built.isState index).isRelated.clock]
  exact isDue

/-- F3: a thread whose next action has not begun is carried by a Step with a
pending invocation. -/
theorem LRunOfDexRun.threadsBegin (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (stepsStart : Dex.Fairness.StepsStart dexRun) : L.Fairness.ThreadsBegin lRun := by
  intro thread
  apply built.eventuallyFails_of_not_forever
  intro index isAlways
  obtain ⟨stepId, method, isPendingAlways⟩ :=
    built.pendingInvocation_forever thread index isAlways
  obtain ⟨later, isLater, failsLater⟩ :=
    stepsStart stepId method index (isPendingAlways index (Nat.le_refl _))
  exact failsLater (isPendingAlways later isLater)

/-- F4: an issued emit is an effect a task still holds, before the close and
after it. -/
theorem LRunOfDexRun.emitsSettle (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (workersSettle : Dex.Fairness.WorkersSettle dexRun) : L.Fairness.EmitsSettle lRun := by
  constructor
  · -- Before the close: the task of the thread holds the emit until it delivers.
    intro thread
    apply built.eventuallyFails_of_not_forever
    intro index isAlways
    have runsAlways : ∀ later, index ≤ later → ∃ current,
        (lRun.states (boundary later)).findThread? thread = some current ∧
        runsAsTask current.phase = true := by
      intro later isLater
      obtain ⟨current, effect, isFound, isPhase⟩ := isAlways later isLater
      exact ⟨current, isFound, by rw [isPhase]; rfl⟩
    obtain ⟨current, effect, isFound, isPhase⟩ := isAlways index (Nat.le_refl _)
    obtain ⟨run, task, hasRun, isRunning, isTask, isThreadTask, holdsEffect, _⟩ :=
      (built.isState index).task_of_thread isFound (by rw [isPhase]; rfl)
    have isTaskFound := findTask?_of_mem (built.isState index).tasksDistinct isTask
    have isUnsettled : Dex.Fairness.IsTaskUnsettled dexRun task.id index := by
      refine ⟨task, isTaskFound, fun response isReturned => ?_⟩
      have isHolding := holdsEffect effect isPhase
      simp [pendingEffects, isReturned] at isHolding
    obtain ⟨later, isLater, isSettled⟩ := workersSettle task.id index isUnsettled
    obtain ⟨phase, isLaterFound, _⟩ :=
      built.task_forever thread index runsAlways task.id task isTaskFound isThreadTask later isLater
    obtain ⟨laterCurrent, laterEffect, isLaterThreadFound, isLaterPhase⟩ := isAlways later isLater
    obtain ⟨laterRun, laterTask, hasLaterRun, isLaterRunning, isLaterTask, isLaterThreadTask,
      laterHoldsEffect, _⟩ :=
      (built.isState later).task_of_thread isLaterThreadFound (by rw [isLaterPhase]; rfl)
    have sameTask := (built.isState later).task_eq_of_thread hasLaterRun isLaterRunning
      isLaterTask (mem_of_findTask? _ _ _ isLaterFound).1
      (by rw [isLaterThreadTask]; exact isThreadTask.symm)
    apply isSettled
    refine ⟨_, isLaterFound, fun response isReturned => ?_⟩
    have isHolding := laterHoldsEffect laterEffect isLaterPhase
    have isReturnedPhase : phase = .returned response := isReturned
    rw [sameTask, isReturnedPhase] at isHolding
    simp [pendingEffects] at isHolding
  · -- After the close: a task that holds a late emit emits it or is dropped.
    intro position hasLate
    obtain ⟨index, isPosition⟩ := built.exists_boundary position
    subst isPosition
    have state := built.isState index
    have isClosedL : (lRun.states (boundary index)).status = .closed :=
      Classical.byContradiction (fun isNotClosed =>
        hasLate (L.lateEmits_eq_nil_of_isRun program lRun built.isLRun _ isNotClosed))
    obtain ⟨run, status, hasRun, isClosed⟩ := state.isRelated.run_of_closed isClosedL
    have sameCount := built.lateEmits_length index run status hasRun isClosed
    have isPositive : 0 < heldEffectCount program (dexRun.states index) := by
      rw [← sameCount]
      exact List.length_pos_iff.mpr hasLate
    obtain ⟨effect, isHeld⟩ := List.exists_mem_of_length_pos isPositive
    obtain ⟨task, isTask, isTaskEffect⟩ := List.mem_flatMap.mp isHeld
    have isHolding : pendingEffects ⟦program⟧ task ≠ [] := List.ne_nil_of_mem isTaskEffect
    have isTaskFound := findTask?_of_mem state.tasksDistinct isTask
    have isUnsettled : Dex.Fairness.IsTaskUnsettled dexRun task.id index := by
      refine ⟨task, isTaskFound, fun response isReturned => isHolding ?_⟩
      simp [pendingEffects, isReturned]
    obtain ⟨later, isLater, isSettled⟩ := workersSettle task.id index isUnsettled
    obtain ⟨distance, isDistance⟩ := Nat.exists_eq_add_of_le isLater
    subst isDistance
    refine ⟨boundary (index + distance), built.boundary_mono isLater, ?_⟩
    rw [sameCount, built.lateEmits_length (index + distance) run status
      (built.closed_forever index run status hasRun isClosed _ isLater) isClosed]
    apply built.heldEffectCount_lt run status isClosed task.id distance index hasRun task
      isTaskFound isHolding
    intro laterTask isLaterFound
    apply Classical.byContradiction
    intro isLaterHolding
    apply isSettled
    refine ⟨laterTask, isLaterFound, fun response isReturned => isLaterHolding ?_⟩
    simp [pendingEffects, isReturned]

/-- F5: a thread that began an action has a task, which returns and then
completes. -/
theorem LRunOfDexRun.actionsComplete
    (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (workersSettle : Dex.Fairness.WorkersSettle dexRun)
    (resultsCommit : Dex.Fairness.ResultsCommit dexRun) :
    L.Fairness.ActionsComplete lRun := by
  intro thread
  apply built.eventuallyFails_of_not_forever
  intro index isAlways
  have runsAlways : ∀ later, index ≤ later → ∃ current,
      (lRun.states (boundary later)).findThread? thread = some current ∧
      runsAsTask current.phase = true := by
    intro later isLater
    obtain ⟨current, isFound, isPhase⟩ := isAlways later isLater
    refine ⟨current, isFound, ?_⟩
    rcases isPhase with isPhase | isPhase | isPhase | ⟨loaded, isPhase⟩ | ⟨outcome, isPhase⟩ <;>
      rw [isPhase] <;> rfl
  obtain ⟨current, isFound, isTaskPhase⟩ := runsAlways index (Nat.le_refl _)
  obtain ⟨run, task, hasRun, isRunning, isTask, isThreadTask, _⟩ :=
    (built.isState index).task_of_thread isFound isTaskPhase
  have isTaskFound := findTask?_of_mem (built.isState index).tasksDistinct isTask
  have isKept := built.task_forever thread index runsAlways task.id task isTaskFound isThreadTask
  -- Dex F4: the task returns. It cannot be dropped while its thread runs the action.
  have hasReturned : ∃ settled, index ≤ settled ∧
      Dex.Fairness.IsTaskReturned dexRun task.id settled := by
    by_cases isUnsettled : Dex.Fairness.IsTaskUnsettled dexRun task.id index
    · obtain ⟨settled, isLater, isSettled⟩ := workersSettle task.id index isUnsettled
      obtain ⟨phase, isSettledFound, _⟩ := isKept settled isLater
      refine ⟨settled, isLater, Classical.byContradiction (fun isNotReturned => isSettled ?_)⟩
      exact ⟨_, isSettledFound, fun response isReturned =>
        isNotReturned ⟨_, response, isSettledFound, isReturned⟩⟩
    · refine ⟨index, Nat.le_refl _, Classical.byContradiction (fun isNotReturned => isUnsettled ?_)⟩
      exact ⟨task, isTaskFound, fun response isReturned =>
        isNotReturned ⟨task, response, isTaskFound, isReturned⟩⟩
  -- Dex F5: the returned task leaves `Θ`. It is the same task, still `Returned`.
  obtain ⟨settled, isSettledLater, settledTask, response, isSettledFound, isReturned⟩ :=
    hasReturned
  obtain ⟨committed, isCommittedLater, isCommitted⟩ :=
    resultsCommit task.id settled ⟨settledTask, response, isSettledFound, isReturned⟩
  have isSettledThreadTask : threadOfInput settledTask.input = thread := by
    obtain ⟨phase, isAlsoFound, _⟩ := isKept settled isSettledLater
    rw [isSettledFound] at isAlsoFound
    cases isAlsoFound
    exact isThreadTask
  obtain ⟨phase, isCommittedFound, staysReturned⟩ :=
    built.task_forever thread settled
      (fun later isLater => runsAlways later (Nat.le_trans isSettledLater isLater))
      task.id settledTask isSettledFound isSettledThreadTask committed isCommittedLater
  exact isCommitted ⟨_, response, isCommittedFound, staysReturned response isReturned⟩

/-- The L run built from a fair Dex run is fair. -/
theorem LRunOfDexRun.isFair (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (isFair : Dex.IsFair dexRun) : L.IsFair lRun :=
  ⟨built.timeDiverges isFair.1, built.timersFire isFair.2.1, built.threadsBegin isFair.2.2.1,
    built.emitsSettle isFair.2.2.2.1,
    built.actionsComplete isFair.2.2.2.1 isFair.2.2.2.2.1, trivial⟩

end DexExpressiveness
