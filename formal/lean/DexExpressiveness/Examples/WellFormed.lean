import DexExpressiveness.L.LockDisciplineSound
import DexExpressiveness.Examples.Mutex
import DexExpressiveness.Examples.TimedSelect
import DexExpressiveness.Examples.LockstepChecks

/-!
# Example programs that are well formed

The main theorems assume `Program.IsWellFormed` (spec §0, §8.1 "Well-formed
programs"). This file shows that the assumption holds for nontrivial programs,
so the theorems cover every construct of L. Each program obeys the static lock
discipline (`Program.hasStaticLockDiscipline`), which the Lean kernel checks by
`decide`, and is then well formed by
`Program.isWellFormed_of_hasStaticLockDiscipline`.

| Program | Constructs |
| --- | --- |
| `Mutex.program` | `fork`, `join`, `lock`, `unlock`, `emit`, `exit` |
| `TimedSelect.program` | `fork`, `join`, `sleep`, timed `select`, `recv`, `cancel`, `emit` |
| `LockstepChecks.signalProgram` | `wait`, `signal`, `lock`, `unlock`, `fork`, `join`, `sleep` |
| `LockstepChecks.broadcastProgram` | `wait` in a `while`, `broadcast`, shared variables |
| `LockstepChecks.recursionProgram` | recursive `call`, `ret`, `if`, `while`, shared variables |
| `LockstepChecks.exceptionProgram` | `try`, `throw` across a call, an uncaught exception |
| `LockstepChecks.selectRaceProgram` | three threads racing in `select`, `cancel`, `join` |
| `createdMutexProgram` (below) | `newMutex`, `lock` of a created mutex, `fail` |

The discipline tracks statically known mutexes only, so it rejects `unlock`,
`wait`, `signal`, and `broadcast` on a created mutex.
`LockstepChecks.newMutexProgram` unlocks a created mutex and is not covered
here.
-/

namespace DexExpressiveness.Examples.WellFormed

open DexExpressiveness.L DexExpressiveness.Examples

/-- The two-thread mutex program is well formed. -/
theorem mutexProgram_isWellFormed : Mutex.program.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- The program with timers, `select`, external input, and cancellation is well formed. -/
theorem timedSelectProgram_isWellFormed : TimedSelect.program.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- The program with a condition variable, `wait`, and `signal` is well formed. -/
theorem signalProgram_isWellFormed : LockstepChecks.signalProgram.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- The program with `wait` inside a loop and `broadcast` is well formed. -/
theorem broadcastProgram_isWellFormed : LockstepChecks.broadcastProgram.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- The program with recursive calls, shared variables, `if`, and `while` is well formed. -/
theorem recursionProgram_isWellFormed : LockstepChecks.recursionProgram.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- The program with `try`, `throw`, and an uncaught exception is well formed. -/
theorem exceptionProgram_isWellFormed : LockstepChecks.exceptionProgram.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- The program with three threads racing in `select` is well formed. -/
theorem selectRaceProgram_isWellFormed : LockstepChecks.selectRaceProgram.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- `m := newMutex; lock(m); emit(1); fail("done")`: a created mutex, then `fail`. -/
def createdMutexProgram : Program :=
  { procs := []
    mainParam := 0
    mainBody := LockstepChecks.block
      [.newMutex 1, .lock (.var 1), .emit (LockstepChecks.int 1), .fail (.lit (.str "done"))]
    staticMutexCount := 0 }

/-- The program that creates and locks a mutex, then fails, is well formed. -/
theorem createdMutexProgram_isWellFormed : createdMutexProgram.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

end DexExpressiveness.Examples.WellFormed
