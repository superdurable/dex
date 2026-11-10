import DexExpressiveness.Dex.Observation
import DexExpressiveness.L.Semantics
import DexExpressiveness.Translation.Flow

/-!
# The projection `π` and the admissible environment of a translated program (spec §5.3)
-/

namespace DexExpressiveness.Translation

open DexExpressiveness.L

/-- The projection `π : Obs → Obs_L ∪ {ε}` (spec §5.3, table), with `none`
for `ε`. The Flow conditions "for the root Flow" hold for every event: the
model has one Flow.

| Dex event | L event |
| --- | --- |
| `start(f, T₀, v, ∅)` | `begin(v)` |
| `pub(f, [(c, v)])`, `c` external | `in(c, v)` |
| `rejected(pub(f, [(c, v)]), NOT_FOUND)` | `refused(c, v)` |
| `fx(f', src, a)` | `out(a)` (the source is dropped) |
| `closed(f, Completed, _, [(T, e, ⟦exit: x⟧)])` | `exit(x)` |
| `closed(f, Failed, (STEP_DECISION_FAILING_FLOW, msg), _)` | `fail(msg)` |
| every other event | `ε` | -/
def project : Dex.Obs signature → Option Event
  | .start StepType.start (DexVal.value input) => some (.begin input)
  | .pub (Chan.external chan) (DexVal.value value) => some (.input chan value)
  | .rejectedPub (Chan.external chan) (DexVal.value value) => some (.refused chan value)
  | .fx _ _ effect => some (.out effect)
  | .closed .completed [(_, DexVal.exitTag value)] => some (.exit value)
  | .closed (.failed message) _ => some (.fail message)
  | _ => none

/-- The Dex form of an L request: the program input and every external
message travel as `enc(v)` of a literal; the start names the entry Step. -/
def encodeRequest : L.Request → Dex.Request signature
  | .start input => .startFlow StepType.start (DexVal.value input.toVal)
  | .input chan value => .publish (Chan.external chan) (DexVal.value value.toVal)

/-- The admissible environment `E_adm` of a translated program (spec §5.3):
the entry Step is the start Step, a program input is an encoded literal, and
an external message is an encoded literal on an external Channel. "The
environment sends plain data only": no handle enters a Flow from outside. -/
def environment : Dex.Environment signature :=
  { entryStep := StepType.start
    IsProgramInput := fun input => ∃ literal : Literal, input = DexVal.value literal.toVal
    IsExternalMessage := fun chan message =>
      ∃ (externalChan : ExtChan) (literal : Literal),
        chan = Chan.external externalChan ∧ message = DexVal.value literal.toVal }

end DexExpressiveness.Translation
