import DexExpressiveness.Simulation.Relation
import DexExpressiveness.Core.BlockRun

/-!
# Liveness: the fixed definitions

Stage 2b proves the two inclusions of liveness (spec §5.5): every fair
maximal trace of the translated Flow, with its marker, is one of the L
program, and conversely. Each inclusion builds a run of one side from a run
of the other, step by step, with the step simulations of Stage 2a.

- Dex to L. Every Dex step is simulated by at most one L label
  (`dex_step_simulated`), so a Dex run gives an L run in blocks of length 0
  or 1 (`LFollowsDex`). A Dex step with an empty block is a Worker or drop
  step; `Dex.Config.silentWeight` bounds how many of them follow each other.
- L to Dex. Every L label is simulated by the Dex actions of
  `simulatingActions`. After the close they leave stale tasks in `Θ` that
  hold no effect, which F4 forbids forever (spec §5.5: "Every task
  eventually leaves `Θ`"). `fairActions` therefore adds the
  [WORKER-ABANDON] of those tasks (`cleanupActions`), and an L run gives a
  Dex run in blocks of `fairActions` (`DexFollowsL`).

The definitions of this file are fixed. The work packages L1 to L6 proved the
statements of `Core/BlockRun.lean` and `Liveness/*.lean` with these
definitions. `Examples/LivenessChecks.lean` tests the definitions on
pseudo-random runs.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Dex steps that L does not see -/

/-- The kinds of Dex action that can have no L label: running a handler,
returning it, and dropping a task (spec §4.4 [WORKER-EFFECT] first and last
part, [WORKER-ABANDON], [STEP-COMPLETE-STALE]). [WORKER-ABANDON] of a task
that holds an effect is the drop of a late emit, which L sees. -/
def Dex.Action.isSilentKind {σ : Dex.Signature} : Dex.Action σ → Bool
  | .workerRun _ => true
  | .workerReturn _ => true
  | .workerAbandon _ => true
  | .stepCompleteStale _ => true
  | _ => false

/-- The number of silent steps a task can still take: run, return, drop. -/
def Dex.Task.silentWeight {σ : Dex.Signature} (task : Dex.Task σ) : Nat :=
  match task.phase with
  | .started => 3
  | .running _ _ => 2
  | .returned _ => 1

/-- A bound on the silent steps the tasks of `Θ` can take before another
step happens. Every action of a silent kind lowers it, so a run cannot take
infinitely many silent steps in a row (spec §5.5, marker `∞`). -/
def Dex.Config.silentWeight {σ : Dex.Signature} (config : Dex.Config σ) : Nat :=
  (config.tasks.map Dex.Task.silentWeight).sum

/-! ## The Dex actions of a fair run that follows an L run -/

/-- [WORKER-ABANDON] of every task whose invocation was canceled and that
holds no effect (spec §4.4: "A task whose invocation was canceled may be
dropped at any point"). L has no step for these tasks, and F4 needs them to
leave `Θ`. While the Flow runs the list is empty. -/
def cleanupActions (program : Program) (config : Dex.Config signature) :
    List (Dex.Action signature) :=
  (config.tasks.filter (fun task =>
    !config.isTaskCurrent task && (pendingEffects ⟦program⟧ task).isEmpty)).map
    (fun task => Dex.Action.workerAbandon task.id)

/-- The Dex actions that follow one L label in a fair run: the simulating
actions, then the cleanup of the configuration they end in. -/
def fairActions (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (label : L.Label) : List (Dex.Action signature) :=
  let actions := simulatingActions program lConfig dexConfig label
  match (Dex.system ⟦program⟧).exec dexConfig actions with
  | some (_, next) => actions ++ cleanupActions program next
  | none => actions

/-! ## A run of one side that follows a run of the other -/

/-- The L run in blocks `lBlocks` follows the Dex run `dexRun`: block
`index` simulates the Dex step `index`. -/
structure LFollowsDex (program : Program)
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (lBlocks : BlockRun L.Config L.Label Event) : Prop where
  /-- The blocks are a run of the L program. -/
  isBlockRun : (L.system program).IsBlockRun lBlocks
  /-- The configurations before each step are a simulation state. -/
  isState : ∀ index, IsSimulationState program (lBlocks.states index) (dexRun.states index)
  /-- Every Dex action is one an admissible run can take. -/
  isAdmissible : ∀ index, IsAdmissibleAction (dexRun.states index) (dexRun.labels index)
  /-- A Dex step is simulated by at most one L label. -/
  atMostOne : ∀ index, (lBlocks.blocks index).length ≤ 1
  /-- The block has the projected events of the Dex step. -/
  sameEvents : ∀ index, lBlocks.events index =
    ((Dex.system ⟦program⟧).eventsAt dexRun index).filterMap project

/-- The L run `lRun` is built from the Dex run `dexRun`: it is the flattening
of blocks that follow `dexRun`. -/
structure LRunOfDexRun (program : Program)
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (lBlocks : BlockRun L.Config L.Label Event) (lRun : InfiniteRun L.Config L.Label)
    (boundary : Nat → Nat) : Prop where
  /-- The Dex run is a run. -/
  isDexRun : (Dex.system ⟦program⟧).IsRun dexRun
  /-- The blocks follow the Dex run. -/
  follows : LFollowsDex program dexRun lBlocks
  /-- The blocks hold infinitely many labels. -/
  isInfinite : lBlocks.HasInfinitelyManySteps
  /-- The L run is a run. -/
  isLRun : (L.system program).IsRun lRun
  /-- The L run is the flattening of the blocks. -/
  isFlattened : lBlocks.IsFlattenedTo lRun boundary

/-- The Dex run in blocks `dexBlocks` follows the L run `lRun`: block `index`
is `fairActions` of the L step `index`. -/
structure DexFollowsL (program : Program) (lRun : InfiniteRun L.Config L.Label)
    (dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)) :
    Prop where
  /-- The blocks are a run of the translated Flow. -/
  isBlockRun : (Dex.system ⟦program⟧).IsBlockRun dexBlocks
  /-- The configurations before each step are a simulation state. -/
  isState : ∀ index, IsSimulationState program (lRun.states index) (dexBlocks.states index)
  /-- No canceled task without an effect is left between two blocks. -/
  isSettled : ∀ index, cleanupActions program (dexBlocks.states index) = []
  /-- No task is `Returned` between two blocks: a block that returns a handler
  completes it (F5). -/
  noReturnedTask : ∀ index task, task ∈ (dexBlocks.states index).tasks →
    ∀ response, task.phase ≠ .returned response
  /-- Each block is the fair actions of the L label. -/
  isFair : ∀ index, dexBlocks.blocks index =
    fairActions program (lRun.states index) (dexBlocks.states index) (lRun.labels index)
  /-- The block has the events of the L step, after projection. -/
  sameEvents : ∀ index, (dexBlocks.events index).filterMap project =
    (L.system program).eventsAt lRun index

/-- The Dex run `dexRun` is built from the L run `lRun`: it is the flattening
of blocks that follow `lRun`. -/
structure DexRunOfLRun (program : Program) (lRun : InfiniteRun L.Config L.Label)
    (dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature))
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (boundary : Nat → Nat) : Prop where
  /-- The L run is a run. -/
  isLRun : (L.system program).IsRun lRun
  /-- The blocks follow the L run. -/
  follows : DexFollowsL program lRun dexBlocks
  /-- The blocks hold infinitely many actions. -/
  isInfinite : dexBlocks.HasInfinitelyManySteps
  /-- The Dex run is a run. -/
  isDexRun : (Dex.system ⟦program⟧).IsRun dexRun
  /-- The Dex run is the flattening of the blocks. -/
  isFlattened : dexBlocks.IsFlattenedTo dexRun boundary

end DexExpressiveness
