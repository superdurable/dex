import DexExpressiveness.Theorems
import DexExpressiveness.Examples.WellFormed

/-!
# The safety theorem at well-formed example programs

`safety_of_isWellFormed` assumes `Program.IsWellFormed` (spec §0, §8.1
"Well-formed programs"). `Examples/WellFormed.lean` proves the assumption for
example programs, so the theorem applies to them with no hypothesis left.
This file states those instances. They show that the scope of the theorem is
not empty.
-/

namespace DexExpressiveness.Examples.WellFormed

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Examples

/-- Safety for the two-thread mutex program (spec §5.4), with no hypothesis. -/
theorem mutexProgram_safety (trace : List Event) :
    IsProjectedDexFiniteTrace Mutex.program trace ↔ IsLFiniteTrace Mutex.program trace :=
  safety_of_isWellFormed Mutex.program mutexProgram_isWellFormed trace

/-- Safety for the program with timers, `select`, and external input. -/
theorem timedSelectProgram_safety (trace : List Event) :
    IsProjectedDexFiniteTrace TimedSelect.program trace ↔
      IsLFiniteTrace TimedSelect.program trace :=
  safety_of_isWellFormed TimedSelect.program timedSelectProgram_isWellFormed trace

/-- Safety for the program with a condition variable, `wait`, and `signal`. -/
theorem signalProgram_safety (trace : List Event) :
    IsProjectedDexFiniteTrace LockstepChecks.signalProgram trace ↔
      IsLFiniteTrace LockstepChecks.signalProgram trace :=
  safety_of_isWellFormed LockstepChecks.signalProgram signalProgram_isWellFormed trace

end DexExpressiveness.Examples.WellFormed
