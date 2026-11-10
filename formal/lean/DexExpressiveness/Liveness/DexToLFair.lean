import DexExpressiveness.Liveness.DexToLRun

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
five lemmas. The five lemmas are `sorry`; the README lists them. The proofs
may add lemmas to this file. They may not change `Liveness/Statements.lean`,
`Core/BlockRun.lean`, or any file of Stage 2a.
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
  sorry

/-- F2: an armed and due L timer is an armed and due Dex timer. -/
theorem LRunOfDexRun.timersFire (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (timersFire : Dex.Fairness.TimersFire dexRun) : L.Fairness.TimersFire lRun := by
  sorry

/-- F3: a thread whose next action has not begun is carried by a Step with a
pending invocation. -/
theorem LRunOfDexRun.threadsBegin (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (stepsStart : Dex.Fairness.StepsStart dexRun) : L.Fairness.ThreadsBegin lRun := by
  sorry

/-- F4: an issued emit is an effect a task still holds, before the close and
after it. -/
theorem LRunOfDexRun.emitsSettle (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (workersSettle : Dex.Fairness.WorkersSettle dexRun) : L.Fairness.EmitsSettle lRun := by
  sorry

/-- F5: a thread that began an action has a task, which returns and then
completes. -/
theorem LRunOfDexRun.actionsComplete
    (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (workersSettle : Dex.Fairness.WorkersSettle dexRun)
    (resultsCommit : Dex.Fairness.ResultsCommit dexRun) :
    L.Fairness.ActionsComplete lRun := by
  sorry

/-- The L run built from a fair Dex run is fair. -/
theorem LRunOfDexRun.isFair (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (isFair : Dex.IsFair dexRun) : L.IsFair lRun :=
  ⟨built.timeDiverges isFair.1, built.timersFire isFair.2.1, built.threadsBegin isFair.2.2.1,
    built.emitsSettle isFair.2.2.2.1,
    built.actionsComplete isFair.2.2.2.1 isFair.2.2.2.2.1, trivial⟩

end DexExpressiveness
