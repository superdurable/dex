import DexExpressiveness.Liveness.LToDexRun
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
are `sorry`; the README lists them. The proofs may add lemmas to this file.
They may not change `Liveness/Statements.lean`, `Core/BlockRun.lean`, or any
file of Stage 2a.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

variable {program : Program} {lRun : InfiniteRun L.Config L.Label}
  {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
  {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}

/-- F1: the Dex clock is the L clock at every block boundary. -/
theorem DexRunOfLRun.timeDiverges (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (timeDiverges : L.Fairness.TimeDiverges lRun) : Dex.Fairness.TimeDiverges dexRun := by
  sorry

/-- F2: an armed and due Dex timer is an armed and due L timer. -/
theorem DexRunOfLRun.timersFire (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (timersFire : L.Fairness.TimersFire lRun) : Dex.Fairness.TimersFire dexRun := by
  sorry

/-- F3: a pending invocation belongs to the Step of a thread whose next
action has not begun; the close clears it. -/
theorem DexRunOfLRun.stepsStart (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (threadsBegin : L.Fairness.ThreadsBegin lRun) : Dex.Fairness.StepsStart dexRun := by
  sorry

/-- F4: a task that has not returned belongs to a thread that issued an emit
or began an action, or holds a late emit. -/
theorem DexRunOfLRun.workersSettle (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (emitsSettle : L.Fairness.EmitsSettle lRun)
    (actionsComplete : L.Fairness.ActionsComplete lRun) :
    Dex.Fairness.WorkersSettle dexRun := by
  sorry

/-- F5: a task is `Returned` only between the Worker step that returns it and
the Complete of the same block. -/
theorem DexRunOfLRun.resultsCommit
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) :
    Dex.Fairness.ResultsCommit dexRun := by
  sorry

/-- The Dex run built from a fair L run is fair. -/
theorem DexRunOfLRun.isFair (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (isFair : L.IsFair lRun) : Dex.IsFair dexRun :=
  ⟨built.timeDiverges isFair.1, built.timersFire isFair.2.1, built.stepsStart isFair.2.2.1,
    built.workersSettle isFair.2.2.2.1 isFair.2.2.2.2.1, built.resultsCommit, trivial⟩

end DexExpressiveness
