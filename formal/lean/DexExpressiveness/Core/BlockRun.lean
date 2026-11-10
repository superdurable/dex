import DexExpressiveness.Core.TransitionSystem

/-!
# Runs given in blocks

The two systems of the theorem do not step in lockstep: one L label is
simulated by one to three Dex actions, and a Dex Worker step is simulated by
no L label (spec §8.2). An infinite run of one system therefore gives the
other system a run in blocks: one finite, possibly empty, list of labels per
step of the first.

This file defines runs in blocks and their flattening into an `InfiniteRun`,
and states what the flattening keeps: the trace, the attempted requests, and
whether the progress steps are finitely many (spec §5.5). The definitions are
fixed. The lemmas are work package L1 of the liveness proof (Stage 2b); they
are `sorry` and the README lists them. Nothing here mentions Dex or L.
-/

namespace DexExpressiveness

/-- A run given in blocks: `blocks index` is run from `states index`, produces
`events index`, and ends in `states (index + 1)`. A block may be empty. -/
structure BlockRun (State Label Event : Type) where
  /-- The state before each block. -/
  states : Nat → State
  /-- The labels of each block. -/
  blocks : Nat → List Label
  /-- The events of each block. -/
  events : Nat → List Event

namespace BlockRun

variable {State Label Event : Type}

/-- Infinitely many blocks are not empty, so the blocks together are an
infinite sequence of labels. -/
def HasInfinitelyManySteps (blockRun : BlockRun State Label Event) : Prop :=
  ∀ bound, ∃ index, bound ≤ index ∧ blockRun.blocks index ≠ []

/-- `run` is the blocks of `blockRun`, one label after the other. `boundary
index` is the position in `run` of the first label of block `index`. -/
structure IsFlattenedTo (blockRun : BlockRun State Label Event)
    (run : InfiniteRun State Label) (boundary : Nat → Nat) : Prop where
  /-- The first block starts the run. -/
  startsAtZero : boundary 0 = 0
  /-- Each block follows the one before it. -/
  nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blockRun.blocks index).length
  /-- The run is in the block's state where the block starts. -/
  stateAtBoundary : ∀ index, run.states (boundary index) = blockRun.states index
  /-- The run takes the labels of the block in order. -/
  labelInBlock : ∀ index offset label, (blockRun.blocks index)[offset]? = some label →
    run.labels (boundary index + offset) = label

end BlockRun

namespace TransitionSystem

variable {State Label Event Request Visible : Type}
variable (system : TransitionSystem State Label Event Request)

/-- A run in blocks of the system from its initial state: every block is a
finite run (spec §5.2) from its state to the next. -/
def IsBlockRun (blockRun : BlockRun State Label Event) : Prop :=
  blockRun.states 0 = system.init ∧
  ∀ index, system.exec (blockRun.states index) (blockRun.blocks index) =
    some (blockRun.events index, blockRun.states (index + 1))

/-! ## Work package L1: the flattening and what it keeps -/

/-- A run in blocks with infinitely many labels flattens into an infinite run. -/
theorem exists_flattening (blockRun : BlockRun State Label Event)
    (isBlockRun : system.IsBlockRun blockRun) (isInfinite : blockRun.HasInfinitelyManySteps) :
    ∃ run boundary, system.IsRun run ∧ blockRun.IsFlattenedTo run boundary := by
  sorry

/-- Every position of the flattened run lies in one block. -/
theorem flattening_position (blockRun : BlockRun State Label Event)
    (run : InfiniteRun State Label) (boundary : Nat → Nat)
    (isFlattened : blockRun.IsFlattenedTo run boundary)
    (isInfinite : blockRun.HasInfinitelyManySteps) (position : Nat) :
    ∃ index offset, offset < (blockRun.blocks index).length ∧
      position = boundary index + offset := by
  sorry

/-- The state of the flattened run inside a block is the state after the
labels of the block taken so far. -/
theorem flattening_exec_take (blockRun : BlockRun State Label Event)
    (run : InfiniteRun State Label) (boundary : Nat → Nat)
    (isBlockRun : system.IsBlockRun blockRun) (isRun : system.IsRun run)
    (isFlattened : blockRun.IsFlattenedTo run boundary) (index offset : Nat)
    (isInside : offset ≤ (blockRun.blocks index).length) :
    ∃ events, system.exec (blockRun.states index) ((blockRun.blocks index).take offset) =
      some (events, run.states (boundary index + offset)) := by
  sorry

/-- The flattened run has the trace of the blocks (spec §5.2: "Its trace is
the concatenation of the event sequences of its transitions"). -/
theorem flattening_isConcatenation_events (blockRun : BlockRun State Label Event)
    (run : InfiniteRun State Label) (boundary : Nat → Nat)
    (isBlockRun : system.IsBlockRun blockRun) (isRun : system.IsRun run)
    (isFlattened : blockRun.IsFlattenedTo run boundary)
    (isInfinite : blockRun.HasInfinitelyManySteps) (view : Event → Option Visible)
    (sequence : Sequence Visible) :
    IsConcatenation (fun index => (blockRun.events index).filterMap view) sequence ↔
      IsConcatenation (fun position => (system.eventsAt run position).filterMap view)
        sequence := by
  sorry

/-- The flattened run attempts the requests of the blocks (spec §5.5: "A run
for `w`"). -/
theorem flattening_isRunFor (blockRun : BlockRun State Label Event)
    (run : InfiniteRun State Label) (boundary : Nat → Nat)
    (isFlattened : blockRun.IsFlattenedTo run boundary)
    (isInfinite : blockRun.HasInfinitelyManySteps) (word : Sequence Request) :
    IsConcatenation (fun index => (blockRun.blocks index).filterMap system.requestOf) word ↔
      system.IsRunFor run word := by
  sorry

/-- The flattened run has finitely many progress steps exactly when, from
some block on, no block holds a progress step (spec §5.5). -/
theorem flattening_hasFinitelyManyProgressSteps (blockRun : BlockRun State Label Event)
    (run : InfiniteRun State Label) (boundary : Nat → Nat)
    (isFlattened : blockRun.IsFlattenedTo run boundary)
    (isInfinite : blockRun.HasInfinitelyManySteps) :
    system.HasFinitelyManyProgressSteps run ↔
      ∃ bound, ∀ index, bound ≤ index → ∀ label, label ∈ blockRun.blocks index →
        system.isProgress label = false := by
  sorry

end TransitionSystem

/-- Mapping every element keeps a concatenation. -/
theorem isConcatenation_map {α β : Type} (transform : α → β) (chunks : Nat → List α)
    (sequence : Sequence α) (isConcatenation : IsConcatenation chunks sequence) :
    IsConcatenation (fun index => (chunks index).map transform) (sequence.map transform) := by
  sorry

/-- An injective map reflects a concatenation. -/
theorem isConcatenation_of_map {α β : Type} (transform : α → β)
    (isInjective : ∀ first second, transform first = transform second → first = second)
    (chunks : Nat → List α) (sequence : Sequence α)
    (isConcatenation :
      IsConcatenation (fun index => (chunks index).map transform) (sequence.map transform)) :
    IsConcatenation chunks sequence := by
  sorry

end DexExpressiveness
