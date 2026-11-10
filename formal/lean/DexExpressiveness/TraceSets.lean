import DexExpressiveness.Translation.Projection

/-!
# The trace sets of the theorem (spec §5.4, §5.5)

The four sets the two statements compare: `π(FinTr_{E_adm}(⟦S⟧))` and
`FinTr(S)` for safety, `π(MaxTr^fair_w(⟦S⟧))` and `MaxTr^fair_w(S)` for
liveness. Each is a predicate on traces. `Theorems.lean` states the two
equalities.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- `π(FinTr_{E_adm}(⟦S⟧))` (spec §5.4): the projections of the finite traces
of the translated Flow, over runs whose requests are drawn from `E_adm`. -/
def IsProjectedDexFiniteTrace (program : Program) (trace : List Event) : Prop :=
  (Dex.system ⟦program⟧).IsFiniteTrace environment.IsAdmissiblePrefix project trace

/-- `FinTr(S)` (spec §5.4): "L's set of finite traces over `Obs_L`". L's own
rules admit only a start followed by inputs, so no request filter is needed. -/
def IsLFiniteTrace (program : Program) (trace : List Event) : Prop :=
  (L.system program).IsFiniteTrace (fun _ => True) some trace

/-- `π(MaxTr^fair_w(⟦S⟧))` (spec §5.5): the projections of the fair maximal
traces, with markers, of the translated Flow for the Dex form of the input
word `word`. -/
def IsProjectedDexFairMaximalTrace (program : Program) (word : Sequence L.Request)
    (trace : Sequence Event) (marker : Marker) : Prop :=
  (Dex.system ⟦program⟧).IsFairMaximalTrace Dex.IsFair project
    (word.map encodeRequest) trace marker

/-- `MaxTr^fair_w(S)` (spec §5.5): L's fair maximal traces, with markers, for
the input word `word`. -/
def IsLFairMaximalTrace (program : Program) (word : Sequence L.Request)
    (trace : Sequence Event) (marker : Marker) : Prop :=
  (L.system program).IsFairMaximalTrace L.IsFair some word trace marker

end DexExpressiveness
