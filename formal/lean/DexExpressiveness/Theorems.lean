import DexExpressiveness.Simulation

/-!
# The expressiveness theorem (spec §5.4, §5.5)

"Every program of a small concurrent imperative language L translates into a
Dex Flow that is trace-equivalent to it, assuming no failures" (spec §0).

"The theorem states safety (5.4) and liveness (5.5) together." The trace sets
are defined in `TraceSets.lean`. Each statement is an equality of two sets,
written as membership in both directions. Each is proved here from its two
inclusions, which `Simulation.lean` states and leaves as `sorry` for Stage 2.

Both statements carry the hypothesis `program.IsSafe`: the program never
breaks a mutex requirement of spec §8.1 (`L.IsViolation`). The spec states
those requirements without a behavior for breaking them, so the theorem covers
the programs that keep them. The README lists this as a point for the owner.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- **Safety** (spec §5.4): `π(FinTr_{E_adm}(⟦S⟧)) = FinTr(S)`, "weak trace
equality of finite traces under the projection". -/
theorem safety (program : Program) (isSafe : program.IsSafe) (trace : List Event) :
    IsProjectedDexFiniteTrace program trace ↔ IsLFiniteTrace program trace :=
  ⟨finiteTrace_dex_to_l program isSafe trace, finiteTrace_l_to_dex program isSafe trace⟩

/-- **Liveness** (spec §5.5): `∀ w ∈ E_adm. π(MaxTr^fair_w(⟦S⟧)) = MaxTr^fair_w(S)`,
"equality of fair maximal traces with markers, for every admissible input
word". The word is given in L form; `encodeRequest` gives its Dex form.
`isAdmissibleWord_encode` and `isAdmissibleWord_decode` (`Properties.lean`)
show that these are exactly the words of `E_adm`. -/
theorem liveness (program : Program) (isSafe : program.IsSafe)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker) :
    IsProjectedDexFairMaximalTrace program word trace marker ↔
      IsLFairMaximalTrace program word trace marker :=
  ⟨fairMaximalTrace_dex_to_l program isSafe word isAdmissible trace marker,
   fairMaximalTrace_l_to_dex program isSafe word isAdmissible trace marker⟩

/-- **Expressiveness** (spec §0, §5.5: "The theorem states safety and liveness
together"): the translation of a safe L program is trace-equivalent to it. -/
theorem expressiveness (program : Program) (isSafe : program.IsSafe) :
    (∀ trace : List Event,
      IsProjectedDexFiniteTrace program trace ↔ IsLFiniteTrace program trace) ∧
    (∀ (word : Sequence L.Request), L.IsAdmissibleWord word →
      ∀ (trace : Sequence Event) (marker : Marker),
        IsProjectedDexFairMaximalTrace program word trace marker ↔
          IsLFairMaximalTrace program word trace marker) :=
  ⟨fun trace => safety program isSafe trace,
   fun word isAdmissible trace marker => liveness program isSafe word isAdmissible trace marker⟩

end DexExpressiveness
