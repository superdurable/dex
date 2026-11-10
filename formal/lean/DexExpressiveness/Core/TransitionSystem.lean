import DexExpressiveness.Core.Assoc

/-!
# Runs, traces, and markers shared by the Dex model and the L model

Spec §5.2 defines a run and its trace. Spec §5.4 defines finite traces
(safety). Spec §5.5 defines runs for an input word, progress steps, the three
markers, and fair maximal traces (liveness). Both sides of the theorem use the
definitions of this file, so the two trace sets are built the same way.

A transition system here is a partial function `step state label`. The label
carries every nondeterministic choice of a rule (which Step starts, how long a
tick is, how many due timers fire), so each rule's conclusion is a function of
the state and the label. This matches spec §4.1: "The conclusions call
deterministic meta-functions."
-/

namespace DexExpressiveness

/-- A finite or infinite sequence: `none` from the end on. Traces and input
words are sequences (spec §5.2, §5.3: "a finite or infinite sequence"). -/
abbrev Sequence (α : Type) := Nat → Option α

namespace Sequence

/-- The finite sequence with the elements of `elements`. -/
def ofList {α : Type} (elements : List α) : Sequence α :=
  fun position => elements[position]?

/-- Apply `transform` to every element. -/
def map {α β : Type} (transform : α → β) (sequence : Sequence α) : Sequence β :=
  fun position => (sequence position).map transform

end Sequence

/-- The concatenation of the first `count` chunks. -/
def concatUpTo {α : Type} (chunks : Nat → List α) : Nat → List α
  | 0 => []
  | count + 1 => concatUpTo chunks count ++ chunks count

/-- `sequence` is the concatenation of all `chunks`, in order (spec §5.2: "Its
trace is the concatenation of the event sequences of its transitions"). The
chunks may be empty, so the concatenation of infinitely many chunks can still
be finite. The sequence is determined position by position: either some finite
concatenation already has the position, or none ever does. -/
def IsConcatenation {α : Type} (chunks : Nat → List α) (sequence : Sequence α) : Prop :=
  ∀ position,
    (∃ count, position < (concatUpTo chunks count).length ∧
      sequence position = (concatUpTo chunks count)[position]?) ∨
    ((∀ count, (concatUpTo chunks count).length ≤ position) ∧ sequence position = none)

/-- `holdsAt` does not hold forever from any point on: whenever it holds, it
later fails. Spec §5.5 states F2 to F5 as weak fairness of this form ("a timer
that stays armed and due is eventually fired"). -/
def EventuallyFails (holdsAt : Nat → Prop) : Prop :=
  ∀ start, holdsAt start → ∃ later, start ≤ later ∧ ¬ holdsAt later

/-- The marker of a fair maximal trace (spec §5.5). -/
inductive Marker
  /-- `✓`: finitely many progress steps, and the program is closed. -/
  | terminated
  /-- `δ`: finitely many progress steps, and the program is still running. -/
  | deadlocked
  /-- `∞`: infinitely many progress steps. -/
  | divergent
  deriving DecidableEq, Repr

/-- An infinite sequence of states and labels (spec §5.5: "[TICK] is always
enabled, so no maximal run is finite"). `states n` is the state before the
`n`-th transition. -/
structure InfiniteRun (State Label : Type) where
  /-- The state before each transition. -/
  states : Nat → State
  /-- The label of each transition. -/
  labels : Nat → Label

/-- A labeled transition system with the observation data spec §5 needs. -/
structure TransitionSystem (State Label Event Request : Type) where
  /-- The initial state (spec §5.2: "an initial `Σ₀` with `R = ∅`"). -/
  init : State
  /-- One transition `Σ --ℓ⃗--> Σ'` (spec §4.1). Returns the ordered event
  sequence and the next state, or `none` when the label's rule is not enabled. -/
  step : State → Label → Option (List Event × State)
  /-- The client request a label attempts, if any (spec §5.3: "attempted
  client requests, in commit order, accepted and rejected alike"). -/
  requestOf : Label → Option Request
  /-- Whether a label is a progress step: every label except a clock tick
  (spec §5.5: "[TICK] steps are time passing, not progress"). -/
  isProgress : Label → Bool
  /-- Whether the program is closed in a state: the root Flow is closed, or
  the L program has exited or failed (spec §5.5, markers). -/
  isClosed : State → Bool

namespace TransitionSystem

variable {State Label Event Request Visible : Type}
variable (system : TransitionSystem State Label Event Request)

/-- Run the labels in order from `state`. Returns the concatenated events and
the final state, or `none` when some label is not enabled (spec §5.2). -/
def exec (state : State) : List Label → Option (List Event × State)
  | [] => some ([], state)
  | label :: laterLabels =>
    match system.step state label with
    | none => none
    | some (events, next) =>
      match exec next laterLabels with
      | none => none
      | some (laterEvents, final) => some (events ++ laterEvents, final)

/-- `state` is reachable from the initial state (spec §5.2: a finite run). -/
def Reachable (state : State) : Prop :=
  ∃ labels events, system.exec system.init labels = some (events, state)

/-- The finite traces of the system under `view`, for runs whose attempted
requests satisfy `IsAdmissible` (spec §5.4: `π(FinTr_{E_adm}(·))`, "`π` is
applied event by event with `ε` erased"). -/
def IsFiniteTrace (IsAdmissible : List Request → Prop) (view : Event → Option Visible)
    (trace : List Visible) : Prop :=
  ∃ labels events final,
    system.exec system.init labels = some (events, final) ∧
    IsAdmissible (labels.filterMap system.requestOf) ∧
    trace = events.filterMap view

/-- An infinite run of the system from its initial state (spec §5.2, §5.5). -/
def IsRun (run : InfiniteRun State Label) : Prop :=
  run.states 0 = system.init ∧
  ∀ index, ∃ events,
    system.step (run.states index) (run.labels index) = some (events, run.states (index + 1))

/-- The events of the `index`-th transition of a run. -/
def eventsAt (run : InfiniteRun State Label) (index : Nat) : List Event :=
  match system.step (run.states index) (run.labels index) with
  | some (events, _) => events
  | none => []

/-- The request attempted by the `index`-th transition: one or none. -/
def requestsAt (run : InfiniteRun State Label) (index : Nat) : List Request :=
  (system.requestOf (run.labels index)).toList

/-- A run for the input word `word` (spec §5.5): its request commits,
"accepted or rejected, are exactly the requests of `w`, each once and in
order". -/
def IsRunFor (run : InfiniteRun State Label) (word : Sequence Request) : Prop :=
  IsConcatenation (system.requestsAt run) word

/-- F0, requests arrive (spec §5.5): "Every request of `w` is eventually
attempted, in order, and committed as accepted or rejected." `IsRunFor`
already implies it (`requestsArrive_of_isRunFor`); it is stated for
readability. -/
def RequestsArrive (run : InfiniteRun State Label) (word : Sequence Request) : Prop :=
  ∀ position, (word position).isSome →
    ∃ count, position < (concatUpTo (system.requestsAt run) count).length

/-- The run has finitely many progress steps (spec §5.5). -/
def HasFinitelyManyProgressSteps (run : InfiniteRun State Label) : Prop :=
  ∃ bound, ∀ index, bound ≤ index → system.isProgress (run.labels index) = false

/-- The marker of a run (spec §5.5): `✓` for finitely many progress steps and
closed, `δ` for finitely many progress steps and never closed, `∞` for
infinitely many progress steps. A closed program stays closed, so "closed"
means closed at some point. -/
def HasMarker (run : InfiniteRun State Label) : Marker → Prop
  | .terminated =>
    system.HasFinitelyManyProgressSteps run ∧ ∃ index, system.isClosed (run.states index) = true
  | .deadlocked =>
    system.HasFinitelyManyProgressSteps run ∧ ∀ index, system.isClosed (run.states index) = false
  | .divergent => ¬ system.HasFinitelyManyProgressSteps run

/-- The fair maximal traces with markers, for the input word `word` (spec
§5.5: `π(MaxTr^fair_w(·))`). `IsFair` holds the fairness assumptions F1 to F6
of the system; F0 is `RequestsArrive`. The trace is the whole trace of the run
under `view`. -/
def IsFairMaximalTrace (IsFair : InfiniteRun State Label → Prop)
    (view : Event → Option Visible) (word : Sequence Request)
    (trace : Sequence Visible) (marker : Marker) : Prop :=
  ∃ run : InfiniteRun State Label,
    system.IsRun run ∧
    system.IsRunFor run word ∧
    system.RequestsArrive run word ∧
    IsFair run ∧
    IsConcatenation (fun index => (system.eventsAt run index).filterMap view) trace ∧
    system.HasMarker run marker

/-- A run for `word` attempts every request of `word`: F0 follows from the
definition of a run for `word`. -/
theorem requestsArrive_of_isRunFor (run : InfiniteRun State Label) (word : Sequence Request)
    (isRunFor : system.IsRunFor run word) : system.RequestsArrive run word := by
  intro position isPresent
  cases isRunFor position with
  | inl found =>
    cases found with
    | intro count bounds => exact ⟨count, bounds.1⟩
  | inr missing =>
    rw [missing.2] at isPresent
    cases isPresent

/-- A run of `first ++ second` is a run of `first` followed by a run of `second`. -/
theorem exec_append {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State)
    (firstLabels secondLabels : List Label) (firstEvents secondEvents : List Event)
    (middle final : State)
    (isFirst : system.exec state firstLabels = some (firstEvents, middle))
    (isSecond : system.exec middle secondLabels = some (secondEvents, final)) :
    system.exec state (firstLabels ++ secondLabels) =
      some (firstEvents ++ secondEvents, final) := by
  induction firstLabels generalizing state firstEvents with
  | nil =>
    simp only [TransitionSystem.exec] at isFirst
    cases isFirst
    simpa using isSecond
  | cons label laterLabels inductionHypothesis =>
    simp only [TransitionSystem.exec, List.cons_append] at isFirst ⊢
    split at isFirst
    · cases isFirst
    · rename_i stepEvents next isStep
      split at isFirst
      · cases isFirst
      · rename_i laterEvents reached isLater
        cases isFirst
        rw [inductionHypothesis next laterEvents isLater]
        simp

/-- A run of one more label from a run's end. -/
theorem exec_cons {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State) (label : Label)
    (laterLabels : List Label) (events : List Event) (final : State)
    (isExec : system.exec state (label :: laterLabels) = some (events, final)) :
    ∃ stepEvents next laterEvents,
      system.step state label = some (stepEvents, next) ∧
      system.exec next laterLabels = some (laterEvents, final) ∧
      events = stepEvents ++ laterEvents := by
  simp only [TransitionSystem.exec] at isExec
  split at isExec
  · cases isExec
  · rename_i stepEvents next isStep
    split at isExec
    · cases isExec
    · rename_i laterEvents reached isLater
      cases isExec
      exact ⟨stepEvents, next, laterEvents, isStep, isLater, rfl⟩

/-- A run followed by one more step. -/
theorem exec_snoc {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State)
    (labels : List Label) (label : Label) (events lastEvents : List Event)
    (middle final : State)
    (isFirst : system.exec state labels = some (events, middle))
    (isLast : system.step middle label = some (lastEvents, final)) :
    system.exec state (labels ++ [label]) = some (events ++ lastEvents, final) :=
  system.exec_append state labels [label] events lastEvents middle final isFirst
    (by simp [TransitionSystem.exec, isLast])

end TransitionSystem

end DexExpressiveness
