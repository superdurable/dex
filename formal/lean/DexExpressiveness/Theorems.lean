import DexExpressiveness.FiniteTraces

/-!
# The expressiveness theorem (spec §5.4, §5.5)

"Every well-formed program of a small concurrent imperative language L
translates into a Dex Flow that is trace-equivalent to it, assuming no
failures" (spec §0).

"The theorem states safety (5.4) and liveness (5.5) together." The trace sets
are defined in `TraceSets.lean`. Each statement is an equality of two sets,
written as membership in both directions. Each is proved here from its two
inclusions. The inclusions of safety are proved in `FiniteTraces.lean` from
the two step simulations of `Simulation.lean`, which are still `sorry`. The
inclusions of liveness are `sorry` in `Simulation.lean` until Stage 2b.

## Scope: well-formed programs

The theorems cover well-formed programs only [owner, 2026-11-09; spec §0,
§5.4, §5.5, §8.1]. `Program.IsWellFormed` (`L/Semantics.lean`) says that no
run of the program, under any input and schedule, has a thread about to

- call `wait(cv, m)`, `signal(cv)`, or `broadcast(cv)` without holding `cv`'s
  mutex, or
- call `unlock(m)` on a mutex it does not hold.

A program that can do either is outside the domain, as a program with a data
race is outside a theorem about data-race-free programs. The hypothesis is a
property of the L program alone. Each theorem name ends in `_of_isWellFormed`
so that the scope is visible wherever the theorem is used.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- **Safety for well-formed programs** (spec §5.4):
`π(FinTr_{E_adm}(⟦S⟧)) = FinTr(S)` for every well-formed L program `S`, "weak
trace equality of finite traces under the projection". -/
theorem safety_of_isWellFormed (program : Program) (isWellFormed : program.IsWellFormed)
    (trace : List Event) :
    IsProjectedDexFiniteTrace program trace ↔ IsLFiniteTrace program trace :=
  ⟨finiteTrace_dex_to_l program isWellFormed trace,
   finiteTrace_l_to_dex program isWellFormed trace⟩

/-- **Liveness for well-formed programs** (spec §5.5):
`∀ w ∈ E_adm. π(MaxTr^fair_w(⟦S⟧)) = MaxTr^fair_w(S)` for every well-formed L
program `S`, "equality of fair maximal traces with markers, for every
admissible input word". The word is given in L form; `encodeRequest` gives its
Dex form. `isAdmissibleWord_encode` and `isAdmissibleWord_decode`
(`Properties.lean`) show that these are exactly the words of `E_adm`. -/
theorem liveness_of_isWellFormed (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker) :
    IsProjectedDexFairMaximalTrace program word trace marker ↔
      IsLFairMaximalTrace program word trace marker :=
  ⟨fairMaximalTrace_dex_to_l program isWellFormed word isAdmissible trace marker,
   fairMaximalTrace_l_to_dex program isWellFormed word isAdmissible trace marker⟩

/-- **Expressiveness for well-formed programs** (spec §0, §5.5: "The theorem
states safety and liveness together"): the translation of a well-formed L
program is trace-equivalent to it. -/
theorem expressiveness_of_isWellFormed (program : Program)
    (isWellFormed : program.IsWellFormed) :
    (∀ trace : List Event,
      IsProjectedDexFiniteTrace program trace ↔ IsLFiniteTrace program trace) ∧
    (∀ (word : Sequence L.Request), L.IsAdmissibleWord word →
      ∀ (trace : Sequence Event) (marker : Marker),
        IsProjectedDexFairMaximalTrace program word trace marker ↔
          IsLFairMaximalTrace program word trace marker) :=
  ⟨fun trace => safety_of_isWellFormed program isWellFormed trace,
   fun word isAdmissible trace marker =>
     liveness_of_isWellFormed program isWellFormed word isAdmissible trace marker⟩

end DexExpressiveness
