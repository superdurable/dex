import DexExpressiveness.Examples.Lockstep

/-!
# The environment cannot hand a program a forged handle

This file used to hold a counterexample (spec Review log, row L6): the
program below, started with the handle of a mutex that `newMutex` had not
created yet, blocked forever in L and was woken in Dex.

```text
locker(h):  lock(h); emit(1)
main(x):    t := fork locker(x); sleep(0); m := newMutex(); y := recv(9)
```

Owner decision, completing that of 2026-11-10: the environment sends plain
data only (spec §5.3, §8.1 "Handles and literals"). `L.Request` carries a `Literal`, so the
start request of the counterexample, `start <mutex handle>`, is no longer a
term: `Request.start` does not accept a `Val`. The counterexample cannot be
stated.

What remains is the check that replaces it:

- `literal_isNotHandle`: no literal's value is a thread, mutex, or condition
  variable handle, so no request carries one;
- the same program, started with plain data, runs in step on both sides. Its
  `lock(x)` then has an operand that is not a mutex, which halts the locker
  on both sides (spec §8.1 "Halting").
-/

namespace DexExpressiveness.Examples.ForgedHandleInput

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples

/-- No literal's value is a handle. So the program input and every external
message, which are literals, never carry a thread, mutex, or condition
variable handle. -/
theorem literal_isNotHandle (literal : Literal) :
    (∀ thread, literal.toVal ≠ .thread thread) ∧
    (∀ mutex, literal.toVal ≠ .mutex mutex) ∧
    (∀ cv, literal.toVal ≠ .cv cv) := by
  cases literal <;> simp [Literal.toVal]

/-- `lock(h); emit(1)`, with `h` in variable 0. -/
def lockerBody : Stmt :=
  .seq (.lock (.var 0)) (.emit (.lit (.int 1)))

/-- `t := fork locker(x); sleep(0); m := newMutex(); y := recv(9)`, with `x`
in variable 0. -/
def mainBody : Stmt :=
  .seq (.fork 1 0 [.var 0]) (.seq (.sleep (.lit (.int 0))) (.seq (.newMutex 2) (.recv 3 9)))

/-- The program of the former counterexample. -/
def program : Program :=
  { procs := [(0, { params := [0], body := lockerBody })]
    mainParam := 0
    mainBody := mainBody
    staticMutexCount := 0 }

/-- Started with plain data, the program stays in step on both sides, for
pseudo-random schedules in both directions. -/
theorem staysInStep :
    (lockstepMismatches program [.start (.int 0), .input 9 (.str "message")] 300 4).isEmpty =
      true := by
  decide +kernel

end DexExpressiveness.Examples.ForgedHandleInput
