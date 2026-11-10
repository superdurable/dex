import DexExpressiveness.TraceSets

/-!
# Schedules for the example runs

The examples check concrete finite runs of both models by kernel evaluation.
A schedule is a list of labels. This file builds schedules with a small
deterministic scheduler, so the example files do not spell out hundreds of
labels. The scheduler is not part of the model: an example theorem is about
the run of the schedule, whichever way the schedule was produced.
-/

namespace DexExpressiveness.Examples

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation

/-- Build a schedule: before each action, tick one microsecond; then take the
first enabled label that `candidates` proposes for the state. Stop when no
candidate is enabled or `fuel` runs out. -/
def schedule {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (tick : Label)
    (candidates : State → List Label) : Nat → State → List Label
  | 0, _ => []
  | fuel + 1, state =>
    match system.step state tick with
    | none => []
    | some (_, ticked) =>
      match (candidates ticked).find? (fun label => (system.step ticked label).isSome) with
      | none => []
      | some label =>
        match system.step ticked label with
        | none => []
        | some (_, next) => tick :: label :: schedule system tick candidates fuel next

/-- The trace of a schedule from the initial state under `view`, or `none`
when some label is not enabled. -/
def traceOf {State Label Event Request Visible : Type}
    (system : TransitionSystem State Label Event Request) (view : Event → Option Visible)
    (labels : List Label) : Option (List Visible) :=
  (system.exec system.init labels).map (fun result => result.1.filterMap view)

/-- A schedule whose trace under `view` is `trace`, and whose requests are
admissible, witnesses a finite trace (spec §5.4). -/
theorem isFiniteTrace_of_traceOf {State Label Event Request Visible : Type}
    (system : TransitionSystem State Label Event Request)
    (IsAdmissible : List Request → Prop) (view : Event → Option Visible)
    (labels : List Label) (trace : List Visible)
    (traceMatches : traceOf system view labels = some trace)
    (isAdmissible : IsAdmissible (labels.filterMap system.requestOf)) :
    system.IsFiniteTrace IsAdmissible view trace := by
  unfold traceOf at traceMatches
  cases executed : system.exec system.init labels with
  | none => rw [executed] at traceMatches; cases traceMatches
  | some result =>
    rw [executed] at traceMatches
    exact ⟨labels, result.1, result.2, executed, isAdmissible, (Option.some.inj traceMatches).symm⟩

/-- A Boolean check that a request list is a finite prefix of an admissible
word of a translated program: empty, or the start of the entry Step with an
encoded input followed by publications of encoded messages to external
Channels (spec §5.3). -/
def isEncodedRequestPrefix : List (Dex.Request signature) → Bool
  | [] => true
  | .startFlow StepType.start (DexVal.value _) :: laterRequests =>
    laterRequests.all (fun request =>
      match request with
      | .publish (Chan.external _) (DexVal.value _) => true
      | _ => false)
  | _ => false

/-- The Boolean check implies admissibility. -/
theorem isAdmissiblePrefix_of_check (requests : List (Dex.Request signature))
    (passes : isEncodedRequestPrefix requests = true) :
    environment.IsAdmissiblePrefix requests := by
  unfold isEncodedRequestPrefix at passes
  split at passes
  · exact Or.inl rfl
  · rename_i input laterRequests
    refine Or.inr ⟨DexVal.value input, laterRequests, rfl, ⟨input, rfl⟩, ?_⟩
    intro request isMember
    have requestPasses := List.all_eq_true.mp passes request isMember
    split at requestPasses
    · rename_i chan value
      exact ⟨_, _, rfl, chan, value, rfl, rfl⟩
    · cases requestPasses
  · cases passes

/-- A Dex schedule with projected trace `trace` and encoded requests
witnesses a member of `π(FinTr_{E_adm}(⟦S⟧))`. -/
theorem isProjectedDexFiniteTrace_of_traceOf (program : Program)
    (actions : List (Dex.Action signature)) (trace : List Event)
    (traceMatches : traceOf (Dex.system ⟦program⟧) project actions = some trace)
    (requestsPass :
      isEncodedRequestPrefix (actions.filterMap (Dex.system ⟦program⟧).requestOf) = true) :
    IsProjectedDexFiniteTrace program trace :=
  isFiniteTrace_of_traceOf _ _ _ actions trace traceMatches
    (isAdmissiblePrefix_of_check _ requestsPass)

/-- An L schedule with trace `trace` witnesses a member of `FinTr(S)`. -/
theorem isLFiniteTrace_of_traceOf (program : Program) (labels : List L.Label)
    (trace : List Event) (traceMatches : traceOf (L.system program) some labels = some trace) :
    IsLFiniteTrace program trace :=
  isFiniteTrace_of_traceOf _ _ _ labels trace traceMatches trivial

/-- Extend the schedule `soFar` by running `schedule` from the state it reaches. -/
def extend {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (tick : Label)
    (candidates : State → List Label) (fuel : Nat) (soFar : List Label) : List Label :=
  match system.exec system.init soFar with
  | none => soFar
  | some (_, state) => soFar ++ schedule system tick candidates fuel state

/-- L labels to try, in order: each thread in `priority` order, then every
other live thread, then one timer fire, then the delivery of a late emit. -/
def lCandidates (priority : List Tid) (config : L.Config) : List L.Label :=
  priority.map L.Label.thread ++
  config.threads.map (fun thread => L.Label.thread thread.core.tid) ++
  [L.Label.fire 1, L.Label.lateDeliver 0]

/-- Extend an L schedule for `program` by up to `fuel` scheduled actions. -/
def lExtend (program : Program) (priority : List Tid) (fuel : Nat) (soFar : List L.Label) :
    List L.Label :=
  extend (L.system program) (L.Label.tick 1) (lCandidates priority) fuel soFar

/-- Dex actions to try, in order: when `shouldStartFirst` is set, every pending
Step start before any Worker step; then every Worker and completion step of
every task; then every Step start; then one timer fire. -/
def dexCandidates (shouldStartFirst : Bool) (config : Dex.Config signature) :
    List (Dex.Action signature) :=
  let starts : List (Dex.Action signature) :=
    match config.run with
    | none => []
    | some run => run.steps.map (fun record => Dex.Action.stepStart record.id)
  let workerSteps : List (Dex.Action signature) :=
    config.tasks.flatMap (fun task =>
      [Dex.Action.workerRun task.id, Dex.Action.workerEffect task.id,
       Dex.Action.workerReturn task.id, Dex.Action.stepComplete task.id,
       Dex.Action.stepCompleteStale task.id, Dex.Action.workerAbandon task.id])
  (if shouldStartFirst then starts else []) ++ workerSteps ++ starts ++ [Dex.Action.timerFire 1]

/-- Extend a Dex schedule for the translation of `program` by up to `fuel`
scheduled actions. -/
def dexExtend (program : Program) (shouldStartFirst : Bool) (fuel : Nat)
    (soFar : List (Dex.Action signature)) : List (Dex.Action signature) :=
  extend (Dex.system ⟦program⟧) (Dex.Action.tick 1) (dexCandidates shouldStartFirst) fuel soFar

end DexExpressiveness.Examples
