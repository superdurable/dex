import DexExpressiveness.Core.Assoc

/-!
# Dex core: abstract syntax (spec §2)

This is the part of the Dex core calculus that the translation of L and the
admissible environment of spec §5.3 use. The README lists what is left out and
why. In short: no RPCs, SubFlows, Attribute locks, Step execution locals,
Channel deletions, Channel load selections, GracefulComplete,
ForceCompleteIfChannelsEmpty, CancelSteps, or Channel bounds other than ForOne.
-/

namespace DexExpressiveness.Dex

/-- The names and values a Dex program is written over (spec §0, Notation:
Step type `T`, Channel name `c`, Attribute key `k`, value `v`; spec §2.2: an
external effect `Fx`). The model is generic in them; the translation of L
picks one instance. -/
structure Signature where
  /-- Step types `T`. -/
  StepType : Type
  /-- Channel names `c`, including ChannelMap instances (spec §1.2). -/
  Chan : Type
  /-- Attribute keys `k`. -/
  Key : Type
  /-- Values `v`: Step inputs, Attribute values, Channel messages, Flow outputs. -/
  Val : Type
  /-- External effects `a` of user code (spec §2.2, "Each `Fx` is one external effect"). -/
  Fx : Type
  /-- AttributeMap load selections `Sel` (spec §2.1 `loadMaps`). On the wire a
  selection is a string: an exact instance key `map/instance`, or a prefix
  `map/` that selects every instance of the map. -/
  MapSelection : Type
  /-- Whether a key is an AttributeMap instance, `map/instance`: the keys
  that contain `/` (spec §1.1: "AttributeMap entries are Attributes with keys
  `map/instance`"). A snapshot holds such a key only when it is selected
  (spec §4.2 `snapshot`). -/
  isMapInstanceKey : Key → Bool
  /-- Whether a key "matches a selection" (spec §4.2 `snapshot`): it equals an
  exact selection, or starts with a prefix selection. -/
  isSelectedBy : MapSelection → Key → Bool
  /-- Step types can be compared. -/
  [stepTypeDecidableEq : DecidableEq StepType]
  /-- Channel names can be compared. -/
  [chanDecidableEq : DecidableEq Chan]
  /-- Attribute keys can be compared. -/
  [keyDecidableEq : DecidableEq Key]
  /-- The tie-break of the timer index for equal fire times (spec §4.5: "Index
  order is `(fire time, protobuf bytes of the TimerId)`"). The arguments are
  the Step type, Step number, and timer index of two wait timers; the result
  says the first sorts at or before the second. It stands for the byte order
  of the encoded TimerIds, which the model does not compute (spec Appendix A,
  assumption 12). It must be a total preorder. -/
  isTimerIdAtMost : StepType → Nat → Nat → StepType → Nat → Nat → Bool

attribute [instance] Signature.stepTypeDecidableEq Signature.chanDecidableEq
  Signature.keyDecidableEq

variable (σ : Signature)

/-- A Step method `m ∈ {W, E}` (spec §0). -/
inductive Method
  /-- WaitFor. -/
  | waitFor
  /-- Execute. -/
  | execute
  deriving DecidableEq, Repr

/-- A Step execution ID `e = T-n` (spec §3.1: "`e = T-n`, where `n` is a
per-type counter of the run"). -/
structure StepExecId where
  /-- The Step type `T`. -/
  stepType : σ.StepType
  /-- The per-type number `n`, starting at 1. An unbounded natural; the real
  limit is below 2³² Step executions of one type per run (spec Appendix A,
  assumption 27). -/
  number : Nat
  deriving DecidableEq


/-- The creating Step execution of a Step, `from` in spec §3.1: the Step that
moved to it, or `__start__` for the start Step. -/
inductive Origin
  /-- `__start__`: created by [FLOW-START]. -/
  | flowStart
  /-- Created by a move of this Step execution. -/
  | step (creator : StepExecId σ)

/-- The context of a handler call, `Ctx` in spec §2.2. The Flow ID is elided
because the model has one Flow. -/
structure Context where
  /-- The Step execution the handler runs for. -/
  stepExecutionId : StepExecId σ
  /-- The Step execution that created it. -/
  fromStepExecutionId : Origin σ

/-- What an attempt reads, `Snap` in spec §2.2 and `snapshot(e, m)` in spec
§4.2. Only `attrs` is modeled; `sizes` and `loaded` are left out because the
translation never reads them. -/
structure Snapshot where
  /-- `attrs : Key ⇀ Val`: the plain Attributes and the selected AttributeMap instances. -/
  attributes : AssocList σ.Key σ.Val

/-- A handler's data effects, `Effects` in spec §2.2, without `deletes`. -/
structure Effects where
  /-- `writes : [(Key, Val | null)]`; `none` is null and deletes the key (spec §4.2 `effects`). -/
  writes : List (σ.Key × Option σ.Val) := []
  /-- `pubs : [(Chan, Val)]`. -/
  publications : List (σ.Chan × σ.Val) := []

/-- `kind` of a wait (spec §2.3), without `AnyComb`. -/
inductive WaitKind
  /-- AllOf; `Until(cond)` is `AllOf(cond)` (spec §1.2). -/
  | all
  /-- AnyOf. -/
  | any
  deriving DecidableEq, Repr

/-- A wait `W` (spec §2.3) restricted to timers and ForOne Channel conditions
(`lo = hi = ⊥`: proceeds with at least one queued message and consumes exactly
one). The empty wait `ε` is the wait with no condition. -/
structure Wait where
  /-- All or Any. -/
  kind : WaitKind
  /-- `timers : [⟨id?, d⟩]`, each `d` in whole seconds. -/
  timerSeconds : List Nat
  /-- `chans : [⟨id?, c, lo?, hi?⟩]`, each a ForOne condition on `c`. -/
  channels : List σ.Chan

/-- The empty wait `ε` (spec §2.3): Execute at once. -/
def Wait.empty : Wait σ := { kind := .all, timerSeconds := [], channels := [] }

/-- Whether a wait has no condition (spec §4.2 `register`: "`W = ε` or `W` has
no condition"). -/
def Wait.hasNoCondition (wait : Wait σ) : Bool :=
  wait.timerSeconds.isEmpty && wait.channels.isEmpty

/-- The Step options `Opts` (spec §2.1), without `lockKeys` and `loadChans`.
"Step options travel with each Step movement." -/
structure StepOptions where
  /-- `skipWaitFor`. -/
  skipWaitFor : Bool
  /-- `loadMaps[W]`: the AttributeMap selections of WaitFor
  (`wait_for_load_attribute_map_instances`). -/
  waitForLoadMaps : List σ.MapSelection := []
  /-- `loadMaps[E]`: the AttributeMap selections of Execute
  (`execute_load_attribute_map_instances`). -/
  executeLoadMaps : List σ.MapSelection := []

variable {σ} in
/-- `o.loadMaps[m]`: the AttributeMap selections of one method. -/
def StepOptions.loadMaps (options : StepOptions σ) : Method → List σ.MapSelection
  | .waitFor => options.waitForLoadMaps
  | .execute => options.executeLoadMaps

/-- A move `⟨T, v?, o⟩` (spec §2.4). -/
structure Move where
  /-- The Step type to start. -/
  stepType : σ.StepType
  /-- The Step input. -/
  input : Option σ.Val
  /-- The Step options `o`. -/
  options : StepOptions σ

/-- A close decision (spec §2.4), without GracefulComplete and
ForceCompleteIfChannelsEmpty. -/
inductive Close
  /-- `FC(v?)`, ForceComplete. `none` is an absent or null output. -/
  | forceComplete (output : Option σ.Val)
  /-- `FF(s?)`, ForceFail with its message. -/
  | forceFail (message : String)
  /-- `DE`, DeadEnd. -/
  | deadEnd

/-- An Execute decision `D` (spec §2.4), without `cancel` and `cancelSib`.
`moves` with one element is GoTo; with several, GoToMany. -/
structure Decision where
  /-- `moves : [Move]`. -/
  moves : List (Move σ)
  /-- `close : Close?`. -/
  close : Option (Close σ)

/-- Well-formedness of a decision (spec §2.5): "A decision has at least one
move or a close" and "GC, FC, FF, and DE have no moves". -/
def Decision.IsWellFormed (decision : Decision σ) : Prop :=
  (decision.close = none ∧ decision.moves ≠ []) ∨
  (decision.close ≠ none ∧ decision.moves = [])

/-- The condition results an Execute receives, `CR` in spec §3.1, without
SubFlow results. -/
structure ConditionResults where
  /-- `timers : [⟨id, done⟩]`, one flag per timer condition. -/
  timersDone : List Bool
  /-- `chans : [⟨id, done, c, [Msg]⟩]`, one entry per Channel condition: the
  consumed message, or `none` for "not done". -/
  channelMessages : List (Option σ.Val)

/-- `WaitResp` (spec §2.2), without locals. -/
structure WaitForResponse where
  /-- The data effects. -/
  effects : Effects σ
  /-- The wait to register. -/
  wait : Wait σ

/-- `ExecResp` (spec §2.2), without locals. -/
structure ExecuteResponse where
  /-- The data effects. -/
  effects : Effects σ
  /-- The decision. -/
  decision : Decision σ

/-- `WaitForFn : Ctx × Val × Snap → Fx* × WaitResp` (spec §2.2). A total
function (spec Appendix A, assumption 1). -/
abbrev WaitForFn :=
  Context σ → Option σ.Val → Snapshot σ → List σ.Fx × WaitForResponse σ

/-- `ExecuteFn : Ctx × Val × Snap × Locals × CR → Fx* × ExecResp` (spec §2.2),
without locals. The condition results are `none` for a Step that skipped
WaitFor (spec §3.1: `cr : CR?`). -/
abbrev ExecuteFn :=
  Context σ → Option σ.Val → Snapshot σ → Option (ConditionResults σ) →
    List σ.Fx × ExecuteResponse σ

/-- A Step definition `σd = ⟨waitFor : WaitForFn?, execute : ExecuteFn⟩` (spec §2.1). -/
structure StepDef where
  /-- The optional WaitFor handler. -/
  waitFor : Option (WaitForFn σ)
  /-- The Execute handler. -/
  execute : ExecuteFn σ

/-- A Flow definition `F` (spec §2.1), without RPCs. `steps` is total: every
Step type has a definition, which is the well-formedness that spec §2.5 puts
on user code. A program of the theorem is one Flow definition, because the
translation names no SubFlow. -/
structure FlowDef where
  /-- `steps : StepType ↦ StepDef`. -/
  stepDef : σ.StepType → StepDef σ

end DexExpressiveness.Dex
