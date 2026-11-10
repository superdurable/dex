import DexExpressiveness.Liveness.LToDexRun
import DexExpressiveness.Liveness.LToDexFairWorkers
import DexExpressiveness.Liveness.LToDexFairTime

/-!
# Liveness, work package L6: the Dex run of a fair L run is fair

One lemma per fairness assumption of Dex (spec §5.5; `Dex/Observation.lean`),
each from the L assumptions it corresponds to:

| Dex assumption | From the L assumptions |
| --- | --- |
| F1 `TimeDiverges` | F1 `TimeDiverges` |
| F2 `TimersFire` | F2 `TimersFire` |
| F3 `StepsStart` | F3 `ThreadsBegin` |
| F4 `WorkersSettle` | F4 `EmitsSettle` and F5 `ActionsComplete` |
| F5 `ResultsCommit` | none: a task is `Returned` only inside a block |

The Dex run has positions inside a block, where no L configuration is
related to the Dex configuration. Inside a block every action but one is a
Worker step, which changes only the phase of one task or removes one task
(`flattening_exec_take` gives the configuration). After the close a task
without an effect is removed by the cleanup of the block that left it
(`DexFollowsL.isSettled`), and a task with an effect is a late emit, which F4
of L settles.

`DexRunOfLRun.isFair` is proved here from the five lemmas. The five lemmas
are proved. Their helper lemmas are in `Liveness/LToDexFairTime.lean` (F1, F2,
F3) and `Liveness/LToDexFairWorkers.lean` (F4, F5).
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

variable {program : Program} {lRun : InfiniteRun L.Config L.Label}
  {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
  {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}

/-- F1: the Dex clock is the L clock at every block boundary. -/
theorem DexRunOfLRun.timeDiverges (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (timeDiverges : L.Fairness.TimeDiverges lRun) : Dex.Fairness.TimeDiverges dexRun := by
  intro bound
  obtain ⟨index, isAbove⟩ := timeDiverges bound
  refine ⟨boundary index, ?_⟩
  rw [built.isFlattened.stateAtBoundary index, ← (built.follows.isState index).isRelated.clock]
  exact isAbove

/-- F2: an armed and due Dex timer is an armed and due L timer. -/
theorem DexRunOfLRun.timersFire (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (timersFire : L.Fairness.TimersFire lRun) : Dex.Fairness.TimersFire dexRun := by
  intro timer start isDue
  apply Classical.byContradiction
  intro staysDue
  have isAlwaysDue : ∀ position, start ≤ position →
      Dex.Fairness.IsTimerArmedAndDue dexRun timer position := fun position isLater =>
    Classical.byContradiction fun isNotDue => staysDue ⟨position, isLater, isNotDue⟩
  obtain ⟨first, isFirst⟩ := built.exists_boundary_ge start
  obtain ⟨firstRun, fireTime, hasFirstRun, isArmed, isFirstDue⟩ :=
    isAlwaysDue (boundary first) isFirst
  rw [built.isFlattened.stateAtBoundary first] at hasFirstRun isFirstDue
  obtain ⟨firstRecord, current, chan, since, isFirstFound, _, isCurrent, isBlocked⟩ :=
    (built.follows.isState first).timedThread hasFirstRun isArmed
  obtain ⟨later, isLater, isNotDue⟩ := timersFire (threadOfInput firstRecord.input)
    timer.step.number first ⟨current, fireTime, chan, since, isCurrent, isBlocked, by
      rw [(built.follows.isState first).isRelated.clock]
      exact isFirstDue⟩
  apply isNotDue
  obtain ⟨laterRun, laterFireTime, hasLaterRun, isLaterArmed, isLaterDue⟩ :=
    isAlwaysDue (boundary later)
      (Nat.le_trans isFirst (built.isFlattened.boundary_mono first later isLater))
  rw [built.isFlattened.stateAtBoundary later] at hasLaterRun isLaterDue
  obtain ⟨laterRecord, laterCurrent, laterChan, laterSince, isLaterFound, hasLaterInput,
    isLaterCurrent, isLaterBlocked⟩ :=
    (built.follows.isState later).timedThread hasLaterRun isLaterArmed
  rw [built.sameInput_at_boundaries first later isLater hasFirstRun isFirstFound hasLaterRun
    isLaterFound hasLaterInput] at isLaterCurrent
  exact ⟨laterCurrent, laterFireTime, laterChan, laterSince, isLaterCurrent, isLaterBlocked, by
    rw [(built.follows.isState later).isRelated.clock]
    exact isLaterDue⟩

/-- F3: a pending invocation belongs to the Step of a thread whose next
action has not begun; the close clears it. -/
theorem DexRunOfLRun.stepsStart (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (threadsBegin : L.Fairness.ThreadsBegin lRun) : Dex.Fairness.StepsStart dexRun := by
  intro stepId method start isPending
  apply Classical.byContradiction
  intro staysPending
  have isAlwaysPending : ∀ position, start ≤ position →
      Dex.Fairness.IsInvocationPending dexRun stepId method position := fun position isLater =>
    Classical.byContradiction fun isNotPending => staysPending ⟨position, isLater, isNotPending⟩
  obtain ⟨first, isFirst⟩ := built.exists_boundary_ge start
  obtain ⟨firstRun, firstRecord, hasFirstRun, isFirstFound, firstInvocation⟩ :=
    isAlwaysPending (boundary first) isFirst
  rw [built.isFlattened.stateAtBoundary first] at hasFirstRun
  obtain ⟨_, current, isCurrent, isPhase⟩ :=
    (built.follows.isState first).pendingThread hasFirstRun isFirstFound firstInvocation
  obtain ⟨later, isLater, isNotPending⟩ :=
    threadsBegin (threadOfInput firstRecord.input) first ⟨current, isCurrent, isPhase⟩
  apply isNotPending
  obtain ⟨laterRun, laterRecord, hasLaterRun, isLaterFound, laterInvocation⟩ :=
    isAlwaysPending (boundary later)
      (Nat.le_trans isFirst (built.isFlattened.boundary_mono first later isLater))
  rw [built.isFlattened.stateAtBoundary later] at hasLaterRun
  obtain ⟨hasLaterInput, laterCurrent, isLaterCurrent, isLaterPhase⟩ :=
    (built.follows.isState later).pendingThread hasLaterRun isLaterFound laterInvocation
  rw [built.sameInput_at_boundaries first later isLater hasFirstRun isFirstFound hasLaterRun
    isLaterFound hasLaterInput] at isLaterCurrent
  exact ⟨laterCurrent, isLaterCurrent, isLaterPhase⟩

/-- F4: a task that has not returned belongs to a thread that issued an emit
or began an action, or holds a late emit. -/
theorem DexRunOfLRun.workersSettle (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (emitsSettle : L.Fairness.EmitsSettle lRun)
    (actionsComplete : L.Fairness.ActionsComplete lRun) :
    Dex.Fairness.WorkersSettle dexRun :=
  DexRunOfLRun.workersSettle_of_fair built emitsSettle actionsComplete

/-- F5: a task is `Returned` only between the Worker step that returns it and
the Complete of the same block. -/
theorem DexRunOfLRun.resultsCommit
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) :
    Dex.Fairness.ResultsCommit dexRun :=
  DexRunOfLRun.resultsCommit_of_noReturnedTask built

/-- The Dex run built from a fair L run is fair. -/
theorem DexRunOfLRun.isFair (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (isFair : L.IsFair lRun) : Dex.IsFair dexRun :=
  ⟨built.timeDiverges isFair.1, built.timersFire isFair.2.1, built.stepsStart isFair.2.2.1,
    built.workersSettle isFair.2.2.2.1 isFair.2.2.2.2.1, built.resultsCommit, trivial⟩

end DexExpressiveness
