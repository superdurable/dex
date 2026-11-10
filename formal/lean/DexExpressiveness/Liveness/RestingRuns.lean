import DexExpressiveness.Liveness

/-!
# Fair maximal runs of L that come to rest

The trace sets of liveness are defined by the existence of a fair run (spec
§5.5). This file builds fair maximal runs of an L program, so that the sets
are shown to be inhabited (`Examples/FairRuns.lean`).

A configuration is at rest when no thread can move and nothing is left to
settle: every thread is blocked without a timer or halted, and no late emit
is left. An L run that reaches a configuration at rest by a finite schedule,
and then takes only steps that keep it at rest while time diverges, is fair:
no fairness assumption of L (F1 to F5, `L/Semantics.lean`) has anything left
to ask for.

Two such tails are built here:

- clock ticks only: the marker is `✓` when the program is closed, and `δ`
  when it is not (`isLFairMaximalTrace_of_rest_closed`,
  `isLFairMaximalTrace_of_rest_open`);
- clock ticks and inputs after the close, which are refused: the word is
  infinite and the marker is `∞` (`isLFairMaximalTrace_of_rest_refused`;
  spec §5.5: "With an infinite `w`, every fair run has infinitely many
  inputs and is `∞`").

The runs are flattenings of runs in blocks (`Core/BlockRun.lean`): the finite
schedule is the first block, and every later block is one label. Everything
here is proved.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Configurations at rest -/

/-- No thread of the configuration can move and nothing is left to settle:
every thread is blocked without a timer or halted, and no late emit is left.
Then no fairness assumption F2 to F5 of L speaks about the configuration. -/
def L.Config.isAtRest (config : L.Config) : Bool :=
  config.lateEmits.isEmpty &&
  config.threads.all (fun thread =>
    match thread.phase with
    | .blocked (.timed _ _ _) _ => false
    | .blocked _ _ => true
    | .halted => true
    | _ => false)

/-- A thread of a configuration at rest is halted, or blocked without a timer. -/
theorem L.Config.phase_of_isAtRest (config : L.Config) (isAtRest : config.isAtRest = true)
    (thread : L.Thread) (isMember : thread ∈ config.threads) :
    thread.phase = .halted ∨
    ∃ on since, thread.phase = .blocked on since ∧
      ∀ number deadline chan, on ≠ .timed number deadline chan := by
  unfold L.Config.isAtRest at isAtRest
  simp only [Bool.and_eq_true] at isAtRest
  have isResting := List.all_eq_true.mp isAtRest.2 thread isMember
  cases phaseCase : thread.phase with
  | blocked on since =>
    refine Or.inr ⟨on, since, rfl, ?_⟩
    intro number deadline chan isTimed
    subst isTimed
    simp [phaseCase] at isResting
  | halted => exact Or.inl rfl
  | _ => simp [phaseCase] at isResting

/-- A configuration at rest has no late emit. -/
theorem L.Config.lateEmits_of_isAtRest (config : L.Config) (isAtRest : config.isAtRest = true) :
    config.lateEmits = [] := by
  unfold L.Config.isAtRest at isAtRest
  simp only [Bool.and_eq_true] at isAtRest
  exact List.isEmpty_iff.mp isAtRest.1

/-- A closed L program stays closed along a finite run. -/
theorem L.exec_keeps_closed (program : Program) (labels : List L.Label) :
    ∀ (config : L.Config) (events : List Event) (final : L.Config),
      (L.system program).Reachable config →
      (L.system program).exec config labels = some (events, final) →
      config.status = .closed → final.status = .closed := by
  induction labels with
  | nil =>
    intro config events final _ isExec isClosed
    simp only [TransitionSystem.exec, Option.some.injEq, Prod.mk.injEq] at isExec
    rw [← isExec.2]
    exact isClosed
  | cons label laterLabels inductionHypothesis =>
    intro config events final isReachable isExec isClosed
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, _⟩ :=
      (L.system program).exec_cons config label laterLabels events final isExec
    have noThread :=
      (L.Invariant.of_reachable program config isReachable).closedHasNoThread isClosed
    have isNextReachable : (L.system program).Reachable next :=
      isReachable.exec [label] stepEvents (by simp [TransitionSystem.exec, isStep])
    exact inductionHypothesis next laterEvents final isNextReachable isLater
      (L.step_keeps_closed program config next label stepEvents isStep isClosed noThread)

/-! ## Concatenations of a prefix and a regular tail -/

/-- A list followed by empty chunks is the concatenation of the chunks. -/
theorem isConcatenation_ofList_of_tail_nil {α : Type} (chunks : Nat → List α)
    (elements : List α) (isFirst : chunks 0 = elements)
    (isRest : ∀ index, chunks (index + 1) = []) :
    IsConcatenation chunks (Sequence.ofList elements) := by
  have upTo : ∀ count, concatUpTo chunks (count + 1) = elements := by
    intro count
    induction count with
    | zero => simp [concatUpTo, isFirst]
    | succ count inductionHypothesis =>
      rw [concatUpTo, inductionHypothesis, isRest, List.append_nil]
  intro position
  by_cases isInside : position < elements.length
  · exact Or.inl ⟨1, by rw [upTo 0]; exact isInside, by rw [upTo 0]; rfl⟩
  · refine Or.inr ⟨fun count => ?_, ?_⟩
    · cases count with
      | zero => simp [concatUpTo]
      | succ count =>
        rw [upTo count]
        omega
    · show elements[position]? = none
      exact List.getElem?_eq_none (by omega)

/-- A list followed by chunks that are in turn empty and one fixed element is
the concatenation of the chunks: the list, then the element forever. -/
theorem isConcatenation_of_tail_alternating {α : Type} (chunks : Nat → List α)
    (elements : List α) (element : α) (isFirst : chunks 0 = elements)
    (isEven : ∀ index, chunks (2 * index + 1) = [])
    (isOdd : ∀ index, chunks (2 * index + 2) = [element]) :
    IsConcatenation chunks (fun position =>
      if position < elements.length then elements[position]? else some element) := by
  have upTo : ∀ count,
      concatUpTo chunks (2 * count + 1) = elements ++ List.replicate count element := by
    intro count
    induction count with
    | zero => simp [concatUpTo, isFirst]
    | succ count inductionHypothesis =>
      have isStep : 2 * (count + 1) + 1 = (2 * count + 1 + 1) + 1 := by omega
      rw [isStep, concatUpTo, concatUpTo, inductionHypothesis, isEven count]
      have isLast : chunks (2 * count + 1 + 1) = [element] := isOdd count
      rw [isLast, List.append_nil, List.append_assoc, List.replicate_succ']
  intro position
  refine Or.inl ⟨2 * (position + 1) + 1, ?_, ?_⟩
  · rw [upTo]
    simp only [List.length_append, List.length_replicate]
    omega
  · rw [upTo]
    dsimp only
    by_cases isInside : position < elements.length
    · rw [if_pos isInside, List.getElem?_append_left isInside]
    · rw [if_neg isInside, List.getElem?_append_right (by omega), List.getElem?_replicate]
      rw [if_pos (by omega)]

/-! ## The run in blocks -/

/-- The run in blocks of a finite schedule followed by one label per block. -/
def restingBlockRun (initial : L.Config) (labels : List L.Label) (events : List Event)
    (tailStates : Nat → L.Config) (tailLabels : Nat → L.Label)
    (tailEvents : Nat → List Event) : BlockRun L.Config L.Label Event :=
  { states := fun index =>
      match index with
      | 0 => initial
      | index + 1 => tailStates index
    blocks := fun index =>
      match index with
      | 0 => labels
      | index + 1 => [tailLabels index]
    events := fun index =>
      match index with
      | 0 => events
      | index + 1 => tailEvents index }

/-- A finite schedule of the program followed by a tail of steps that stays
at rest while time diverges. -/
structure IsRestingTail (program : Program) (labels : List L.Label) (events : List Event)
    (tailStates : Nat → L.Config) (tailLabels : Nat → L.Label)
    (tailEvents : Nat → List Event) : Prop where
  /-- The schedule runs from the initial configuration to the start of the tail. -/
  isExec : (L.system program).exec (L.system program).init labels = some (events, tailStates 0)
  /-- The tail is a run. -/
  isStep : ∀ index, (L.system program).step (tailStates index) (tailLabels index) =
    some (tailEvents index, tailStates (index + 1))
  /-- Every configuration of the tail is at rest. -/
  isAtRest : ∀ index, (tailStates index).isAtRest = true
  /-- Time diverges in the tail (F1). -/
  timeDiverges : ∀ bound, ∃ index, bound < (tailStates index).clock

variable {program : Program} {labels : List L.Label} {events : List Event}
  {tailStates : Nat → L.Config} {tailLabels : Nat → L.Label} {tailEvents : Nat → List Event}

/-- The blocks are a run of the L program. -/
theorem IsRestingTail.isBlockRun
    (tail : IsRestingTail program labels events tailStates tailLabels tailEvents) :
    (L.system program).IsBlockRun
      (restingBlockRun (L.system program).init labels events tailStates tailLabels tailEvents) := by
  refine ⟨rfl, fun index => ?_⟩
  cases index with
  | zero => exact tail.isExec
  | succ index =>
    show (L.system program).exec (tailStates index) [tailLabels index] =
      some (tailEvents index, tailStates (index + 1))
    simp [TransitionSystem.exec, tail.isStep index]

/-- The blocks hold infinitely many labels. -/
theorem restingBlockRun_hasInfinitelyManySteps (initial : L.Config) (labels : List L.Label)
    (events : List Event) (tailStates : Nat → L.Config) (tailLabels : Nat → L.Label)
    (tailEvents : Nat → List Event) :
    (restingBlockRun initial labels events tailStates tailLabels tailEvents).HasInfinitelyManySteps :=
  fun bound => ⟨bound + 1, Nat.le_succ bound, List.cons_ne_nil _ _⟩

/-- The block of a tail step starts after the schedule and the earlier tail steps. -/
theorem restingBlockRun_boundary (initial : L.Config) (labels : List L.Label)
    (events : List Event) (tailStates : Nat → L.Config) (tailLabels : Nat → L.Label)
    (tailEvents : Nat → List Event) (run : InfiniteRun L.Config L.Label) (boundary : Nat → Nat)
    (isFlattened : (restingBlockRun initial labels events tailStates tailLabels
      tailEvents).IsFlattenedTo run boundary) (index : Nat) :
    boundary (index + 1) = labels.length + index := by
  induction index with
  | zero =>
    have isNext := isFlattened.nextBoundary 0
    have isStart := isFlattened.startsAtZero
    change boundary 1 = boundary 0 + labels.length at isNext
    show boundary 1 = labels.length + 0
    omega
  | succ index inductionHypothesis =>
    have isNext := isFlattened.nextBoundary (index + 1)
    change boundary (index + 1 + 1) = boundary (index + 1) + 1 at isNext
    omega

/-- After the schedule, the flattened run is in the configurations of the tail. -/
theorem restingBlockRun_state (initial : L.Config) (labels : List L.Label)
    (events : List Event) (tailStates : Nat → L.Config) (tailLabels : Nat → L.Label)
    (tailEvents : Nat → List Event) (run : InfiniteRun L.Config L.Label) (boundary : Nat → Nat)
    (isFlattened : (restingBlockRun initial labels events tailStates tailLabels
      tailEvents).IsFlattenedTo run boundary) (index : Nat) :
    run.states (labels.length + index) = tailStates index := by
  rw [← restingBlockRun_boundary initial labels events tailStates tailLabels tailEvents run
    boundary isFlattened index]
  exact isFlattened.stateAtBoundary (index + 1)

/-! ## Fairness -/

/-- The flattened run is fair: after the schedule every configuration is at
rest, so F2 to F5 have nothing to ask for, and time diverges. -/
theorem IsRestingTail.isFair
    (tail : IsRestingTail program labels events tailStates tailLabels tailEvents)
    (run : InfiniteRun L.Config L.Label) (boundary : Nat → Nat)
    (isFlattened : (restingBlockRun (L.system program).init labels events tailStates
      tailLabels tailEvents).IsFlattenedTo run boundary) : L.IsFair run := by
  have stateAt := restingBlockRun_state (L.system program).init labels events tailStates
    tailLabels tailEvents run boundary isFlattened
  have atRest : ∀ index, (run.states (labels.length + index)).isAtRest = true := by
    intro index
    rw [stateAt]
    exact tail.isAtRest index
  have phaseAt : ∀ index thread current,
      (run.states (labels.length + index)).findThread? thread = some current →
      current.phase = .halted ∨ ∃ on since, current.phase = .blocked on since ∧
        ∀ number deadline chan, on ≠ .timed number deadline chan :=
    fun index thread current isFound =>
      L.Config.phase_of_isAtRest _ (atRest index) current (List.mem_of_find?_eq_some isFound)
  refine ⟨?_, ?_, ?_, ⟨?_, ?_⟩, ?_, trivial⟩
  · intro bound
    obtain ⟨index, isLater⟩ := tail.timeDiverges bound
    exact ⟨labels.length + index, by rw [stateAt]; exact isLater⟩
  · intro thread number start _
    refine ⟨labels.length + start, by omega, ?_⟩
    rintro ⟨current, deadline, chan, since, isFound, isPhase, _⟩
    rcases phaseAt start thread current isFound with isHalted |
      ⟨on, blockedSince, isBlocked, isNotTimed⟩
    · rw [isHalted] at isPhase
      cases isPhase
    · rw [isBlocked] at isPhase
      injection isPhase with isOn _
      exact isNotTimed number deadline chan isOn
  · intro thread start _
    refine ⟨labels.length + start, by omega, ?_⟩
    rintro ⟨current, isFound, isPending⟩
    rcases phaseAt start thread current isFound with isHalted | ⟨on, blockedSince, isBlocked, _⟩ <;>
      rcases isPending with isPhase | isPhase | ⟨outcome, isPhase⟩ <;> simp_all
  · intro thread start _
    refine ⟨labels.length + start, by omega, ?_⟩
    rintro ⟨current, effect, isFound, isPhase⟩
    rcases phaseAt start thread current isFound with isHalted | ⟨on, blockedSince, isBlocked, _⟩ <;>
      simp_all
  · intro index hasLateEmit
    refine ⟨labels.length + index, by omega, ?_⟩
    rw [L.Config.lateEmits_of_isAtRest _ (atRest index)]
    cases lateCase : (run.states index).lateEmits with
    | nil => exact absurd lateCase hasLateEmit
    | cons head rest => simp
  · intro thread start _
    refine ⟨labels.length + start, by omega, ?_⟩
    rintro ⟨current, isFound, isCompleting⟩
    rcases phaseAt start thread current isFound with isHalted | ⟨on, blockedSince, isBlocked, _⟩ <;>
      rcases isCompleting with isPhase | isPhase | isPhase | ⟨loaded, isPhase⟩ |
        ⟨outcome, isPhase⟩ <;> simp_all

/-! ## The fair maximal trace -/

/-- A finite schedule followed by a tail at rest gives a fair maximal trace
of the L program (spec §5.5). The word and the trace are the concatenations
over the blocks. The marker is read off the tail: `✓` or `δ` when the tail
holds no progress step, by whether the program is closed, and `∞` when the
tail holds infinitely many progress steps. -/
theorem IsRestingTail.isLFairMaximalTrace
    (tail : IsRestingTail program labels events tailStates tailLabels tailEvents)
    (word : Sequence L.Request)
    (isWord : IsConcatenation (fun index =>
      ((restingBlockRun (L.system program).init labels events tailStates tailLabels
        tailEvents).blocks index).filterMap (L.system program).requestOf) word)
    (trace : Sequence Event)
    (isTrace : IsConcatenation (fun index =>
      ((restingBlockRun (L.system program).init labels events tailStates tailLabels
        tailEvents).events index).filterMap some) trace)
    (marker : Marker)
    (hasMarker :
      match marker with
      | .terminated =>
        (∀ index, (L.system program).isProgress (tailLabels index) = false) ∧
        (tailStates 0).status = .closed
      | .deadlocked =>
        (∀ index, (L.system program).isProgress (tailLabels index) = false) ∧
        ∀ index, (tailStates index).status ≠ .closed
      | .divergent =>
        ∀ bound, ∃ index, bound ≤ index ∧
          (L.system program).isProgress (tailLabels index) = true) :
    IsLFairMaximalTrace program word trace marker := by
  have isBlockRun := tail.isBlockRun
  have isInfinite := restingBlockRun_hasInfinitelyManySteps (L.system program).init labels
    events tailStates tailLabels tailEvents
  obtain ⟨run, boundary, isRun, isFlattened⟩ :=
    (L.system program).exists_flattening _ isBlockRun isInfinite
  have stateAt := restingBlockRun_state (L.system program).init labels events tailStates
    tailLabels tailEvents run boundary isFlattened
  have isRunFor : (L.system program).IsRunFor run word :=
    ((L.system program).flattening_isRunFor _ run boundary isFlattened isInfinite word).mp isWord
  have finiteOfIdle : (∀ index, (L.system program).isProgress (tailLabels index) = false) →
      (L.system program).HasFinitelyManyProgressSteps run := by
    intro isIdle
    refine ((L.system program).flattening_hasFinitelyManyProgressSteps _ run boundary
      isFlattened isInfinite).mpr ⟨1, fun index isLater label isMember => ?_⟩
    cases index with
    | zero => omega
    | succ index =>
      have isLabel : label = tailLabels index := List.mem_singleton.mp isMember
      rw [isLabel]
      exact isIdle index
  refine ⟨run, isRun, isRunFor, (L.system program).requestsArrive_of_isRunFor run word isRunFor,
    tail.isFair run boundary isFlattened,
    ((L.system program).flattening_isConcatenation_events _ run boundary isBlockRun isRun
      isFlattened isInfinite some trace).mp isTrace, ?_⟩
  cases marker with
  | terminated =>
    refine ⟨finiteOfIdle hasMarker.1, labels.length + 0, ?_⟩
    rw [stateAt 0]
    simp [L.system, hasMarker.2]
  | deadlocked =>
    refine ⟨finiteOfIdle hasMarker.1, fun position => ?_⟩
    obtain ⟨index, offset, isInside, isPosition⟩ :=
      TransitionSystem.flattening_position _ run boundary isFlattened isInfinite position
    subst isPosition
    cases index with
    | zero =>
      by_cases isClosed : (run.states (boundary 0 + offset)).status = .closed
      · exfalso
        obtain ⟨prefixEvents, isPrefix⟩ := (L.system program).flattening_exec_take _ run boundary
          isBlockRun isRun isFlattened 0 offset (Nat.le_of_lt isInside)
        have isWhole := tail.isExec
        rw [← List.take_append_drop offset labels] at isWhole
        obtain ⟨firstEvents, middle, secondEvents, isFirst, isSecond, _⟩ :=
          (L.system program).exec_append_inv _ _ _ _ _ isWhole
        have isSame : some (firstEvents, middle) =
            some (prefixEvents, run.states (boundary 0 + offset)) := isFirst.symm.trans isPrefix
        have isMiddle : middle = run.states (boundary 0 + offset) := by
          injection isSame with isPair
          injection isPair
        exact hasMarker.2 0
          (L.exec_keeps_closed program (labels.drop offset) middle secondEvents (tailStates 0)
            ⟨labels.take offset, firstEvents, isFirst⟩ isSecond (by rw [isMiddle]; exact isClosed))
      · simp [L.system, isClosed]
    | succ index =>
      have isZero : offset = 0 := by
        change offset < 1 at isInside
        omega
      rw [isZero, Nat.add_zero, isFlattened.stateAtBoundary (index + 1)]
      have isOpen := hasMarker.2 index
      show (L.system program).isClosed (tailStates index) = false
      simp [L.system, isOpen]
  | divergent =>
    intro isFinite
    obtain ⟨bound, isIdle⟩ := ((L.system program).flattening_hasFinitelyManyProgressSteps _ run
      boundary isFlattened isInfinite).mp isFinite
    obtain ⟨index, isLater, isProgress⟩ := hasMarker bound
    have isNotProgress := isIdle (index + 1) (by omega) (tailLabels index)
      (List.mem_singleton.mpr rfl)
    rw [isProgress] at isNotProgress
    cases isNotProgress

/-! ## A tail of clock ticks -/

/-- After a schedule that ends at rest, clock ticks alone are a tail at rest. -/
theorem isRestingTail_ticks (program : Program) (labels : List L.Label) (events : List Event)
    (final : L.Config)
    (isExec : (L.system program).exec (L.system program).init labels = some (events, final))
    (isAtRest : final.isAtRest = true) :
    IsRestingTail program labels events
      (fun index => { final with clock := final.clock + index }) (fun _ => L.Label.tick 1)
      (fun _ => []) :=
  { isExec := isExec
    isStep := fun index => by simp [L.system, L.step, Nat.add_assoc]
    isAtRest := fun _ => isAtRest
    timeDiverges := fun bound => ⟨bound + 1, by
      show bound < final.clock + (bound + 1)
      omega⟩ }

/-- A schedule that ends at rest with the program closed, followed by clock
ticks, is a fair maximal trace with marker `✓`: the finite word of the
schedule's requests and the finite trace of its events. -/
theorem isLFairMaximalTrace_of_rest_closed (program : Program) (labels : List L.Label)
    (events : List Event) (final : L.Config)
    (isExec : (L.system program).exec (L.system program).init labels = some (events, final))
    (isAtRest : final.isAtRest = true) (isClosed : final.status = .closed) :
    IsLFairMaximalTrace program
      (Sequence.ofList (labels.filterMap (L.system program).requestOf))
      (Sequence.ofList events) .terminated := by
  refine (isRestingTail_ticks program labels events final isExec isAtRest).isLFairMaximalTrace
    _ (isConcatenation_ofList_of_tail_nil _ _ rfl (fun _ => rfl)) _ ?_ .terminated
    ⟨fun _ => rfl, isClosed⟩
  have isTrace := isConcatenation_ofList_of_tail_nil
    (fun index => ((restingBlockRun (L.system program).init labels events
      (fun index => { final with clock := final.clock + index }) (fun _ => L.Label.tick 1)
      (fun _ => [])).events index).filterMap some) (events.filterMap some) rfl (fun _ => rfl)
  rwa [List.filterMap_some] at isTrace

/-- A schedule that ends at rest with the program not closed, followed by
clock ticks, is a fair maximal trace with marker `δ`. -/
theorem isLFairMaximalTrace_of_rest_open (program : Program) (labels : List L.Label)
    (events : List Event) (final : L.Config)
    (isExec : (L.system program).exec (L.system program).init labels = some (events, final))
    (isAtRest : final.isAtRest = true) (isOpen : final.status ≠ .closed) :
    IsLFairMaximalTrace program
      (Sequence.ofList (labels.filterMap (L.system program).requestOf))
      (Sequence.ofList events) .deadlocked := by
  refine (isRestingTail_ticks program labels events final isExec isAtRest).isLFairMaximalTrace
    _ (isConcatenation_ofList_of_tail_nil _ _ rfl (fun _ => rfl)) _ ?_ .deadlocked
    ⟨fun _ => rfl, fun _ => isOpen⟩
  have isTrace := isConcatenation_ofList_of_tail_nil
    (fun index => ((restingBlockRun (L.system program).init labels events
      (fun index => { final with clock := final.clock + index }) (fun _ => L.Label.tick 1)
      (fun _ => [])).events index).filterMap some) (events.filterMap some) rfl (fun _ => rfl)
  rwa [List.filterMap_some] at isTrace

/-! ## A tail of refused inputs -/

/-- After a schedule that ends at rest with the program closed, clock ticks
and inputs in turn are a tail at rest: an input after the close is refused
and changes nothing (spec §8.1: "Input that arrives after exit is
`refused`"). -/
theorem isRestingTail_refused (program : Program) (labels : List L.Label) (events : List Event)
    (final : L.Config)
    (isExec : (L.system program).exec (L.system program).init labels = some (events, final))
    (isAtRest : final.isAtRest = true) (isClosed : final.status = .closed) (chan : ExtChan)
    (value : Literal) :
    IsRestingTail program labels events
      (fun index => { final with clock := final.clock + (index + 1) / 2 })
      (fun index => if index % 2 = 0 then L.Label.tick 1 else L.Label.request (.input chan value))
      (fun index => if index % 2 = 0 then [] else [Event.refused chan value.toVal]) :=
  { isExec := isExec
    isStep := fun index => by
      by_cases isEven : index % 2 = 0
      · have sameClock : (index + 1 + 1) / 2 = (index + 1) / 2 + 1 := by omega
        simp only [if_pos isEven, sameClock]
        simp [L.system, L.step, Nat.add_assoc]
      · have sameClock : (index + 1 + 1) / 2 = (index + 1) / 2 := by omega
        simp only [if_neg isEven, sameClock]
        simp [L.system, L.step, isClosed]
    isAtRest := fun _ => isAtRest
    timeDiverges := fun bound => ⟨2 * (bound + 1), by
      show bound < final.clock + (2 * (bound + 1) + 1) / 2
      omega⟩ }

/-- A schedule that ends at rest with the program closed, followed forever by
the same input, is a fair maximal trace with marker `∞`: the word is the
schedule's requests and then the input forever, and the trace is the
schedule's events and then `refused` forever. -/
theorem isLFairMaximalTrace_of_rest_refused (program : Program) (labels : List L.Label)
    (events : List Event) (final : L.Config)
    (isExec : (L.system program).exec (L.system program).init labels = some (events, final))
    (isAtRest : final.isAtRest = true) (isClosed : final.status = .closed) (chan : ExtChan)
    (value : Literal) :
    IsLFairMaximalTrace program
      (fun position =>
        if position < (labels.filterMap (L.system program).requestOf).length then
          (labels.filterMap (L.system program).requestOf)[position]?
        else some (.input chan value))
      (fun position =>
        if position < events.length then events[position]?
        else some (Event.refused chan value.toVal))
      .divergent := by
  have isEvenIndex : ∀ index, (2 * index) % 2 = 0 := fun index => by omega
  have isOddIndex : ∀ index, ¬ (2 * index + 1) % 2 = 0 := fun index => by omega
  refine (isRestingTail_refused program labels events final isExec isAtRest isClosed chan
    value).isLFairMaximalTrace _ ?_ _ ?_ .divergent ?_
  · refine isConcatenation_of_tail_alternating _ _ (L.Request.input chan value) rfl ?_ ?_
    · intro index
      show ([if (2 * index) % 2 = 0 then L.Label.tick 1
        else L.Label.request (.input chan value)].filterMap (L.system program).requestOf) = []
      rw [if_pos (isEvenIndex index)]
      rfl
    · intro index
      show ([if (2 * index + 1) % 2 = 0 then L.Label.tick 1
        else L.Label.request (.input chan value)].filterMap (L.system program).requestOf) =
          [L.Request.input chan value]
      rw [if_neg (isOddIndex index)]
      rfl
  · have isTrace := isConcatenation_of_tail_alternating
      (fun index => ((restingBlockRun (L.system program).init labels events
        (fun index => { final with clock := final.clock + (index + 1) / 2 })
        (fun index =>
          if index % 2 = 0 then L.Label.tick 1 else L.Label.request (.input chan value))
        (fun index =>
          if index % 2 = 0 then [] else [Event.refused chan value.toVal])).events
          index).filterMap some)
      (events.filterMap some) (Event.refused chan value.toVal) rfl
      (fun index => by
        show (if (2 * index) % 2 = 0 then [] else [Event.refused chan value.toVal]).filterMap
          some = []
        rw [if_pos (isEvenIndex index)]
        rfl)
      (fun index => by
        show (if (2 * index + 1) % 2 = 0 then []
          else [Event.refused chan value.toVal]).filterMap some =
            [Event.refused chan value.toVal]
        rw [if_neg (isOddIndex index)]
        rfl)
    rwa [List.filterMap_some] at isTrace
  · intro bound
    refine ⟨2 * bound + 1, by omega, ?_⟩
    show (L.system program).isProgress
      (if (2 * bound + 1) % 2 = 0 then L.Label.tick 1
        else L.Label.request (.input chan value)) = true
    rw [if_neg (isOddIndex bound)]
    rfl

end DexExpressiveness
