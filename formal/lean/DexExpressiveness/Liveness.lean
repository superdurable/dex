import DexExpressiveness.Liveness.DexToLFair
import DexExpressiveness.Liveness.LToDexFair

/-!
# Liveness: the two inclusions of fair maximal traces

Spec §5.5: for every admissible input word, the fair maximal traces with
markers of the translated Flow, after projection, are those of the L program.
The two inclusions are proved here from the work packages of Stage 2b:

| Package | File | Content |
| --- | --- | --- |
| L1 | `Core/BlockRun.lean` | Runs in blocks and their flattening. |
| L2 | `Liveness/Silent.lean` | Silent Dex steps, the kinds of labels, admissible actions. |
| L3 | `Liveness/DexToLRun.lean` | The L run of a Dex run: word, trace, marker. |
| L4 | `Liveness/DexToLFair.lean` | The L run of a fair Dex run is fair. |
| L5 | `Liveness/LToDexRun.lean` | The Dex run of an L run: word, trace, marker. |
| L6 | `Liveness/LToDexFair.lean` | The Dex run of a fair L run is fair. |

`Liveness/Statements.lean` holds the fixed definitions. This file has no
`sorry` of its own.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- Liveness, Dex to L: every fair maximal Dex trace with its marker, for an
admissible word, is one of L. The L run is built from the Dex run by dropping
the Worker steps L does not see. -/
theorem fairMaximalTrace_dex_to_l (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker)
    (isDexTrace : IsProjectedDexFairMaximalTrace program word trace marker) :
    IsLFairMaximalTrace program word trace marker := by
  obtain ⟨dexRun, isRun, isRunFor, _, isFair, isTrace, hasMarker⟩ := isDexTrace
  obtain ⟨lBlocks, lRun, boundary, built⟩ :=
    exists_lRun_of_dexRun program isWellFormed word isAdmissible dexRun isRun isRunFor isFair.1
  have isLRunFor := built.isRunFor word isRunFor
  exact ⟨lRun, built.isLRun, isLRunFor,
    (L.system program).requestsArrive_of_isRunFor lRun word isLRunFor, built.isFair isFair,
    built.isConcatenation_events trace isTrace, built.hasMarker marker hasMarker⟩

/-- Liveness, L to Dex: every fair maximal L trace with its marker, for an
admissible word, is one of the translated Flow. The Dex run is built from the
L run by inserting the Worker steps and the drops of canceled tasks. -/
theorem fairMaximalTrace_l_to_dex (program : Program) (_isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (_isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker)
    (isLTrace : IsLFairMaximalTrace program word trace marker) :
    IsProjectedDexFairMaximalTrace program word trace marker := by
  obtain ⟨lRun, isRun, isRunFor, _, isFair, isTrace, hasMarker⟩ := isLTrace
  obtain ⟨dexBlocks, dexRun, boundary, built⟩ :=
    exists_dexRun_of_lRun program lRun isRun isFair.1
  have isDexRunFor := built.isRunFor word isRunFor
  exact ⟨dexRun, built.isDexRun, isDexRunFor,
    (Dex.system ⟦program⟧).requestsArrive_of_isRunFor dexRun _ isDexRunFor, built.isFair isFair,
    built.isConcatenation_events trace isTrace, built.hasMarker marker hasMarker⟩

end DexExpressiveness
