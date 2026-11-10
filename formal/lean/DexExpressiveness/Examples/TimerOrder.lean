import DexExpressiveness.Examples.Lockstep

/-!
# Example: two timers with equal deadlines fire in timer-number order

```text
selector(d, id):  select { m := recv(7) → emit(m) | after(d) → emit(id) }
main(x):          b := fork selector(1, 20); a := fork selector(0, 10)
                  join(b); join(a); exit(0)
```

Spec §8.1: `fire` "fires only the least due timer in the order `(deadline,
n)`", where `n` is the timer number given at Enter. Spec §8.2: Dex fires in
the same order, because the timer number is the `Timed` Step number.

The schedule below makes thread `a` enter first (timer number 1) and arm
last, exactly one second after thread `b` (timer number 2) armed its
one-second timer. Both deadlines are equal. One `fire` then fires `a`'s timer,
although `b` armed first; the message that arrives next goes to `b`. So the
trace has `out(10)` and `out(99)`. With the opposite tie-break it would have
`out(20)` instead of `out(10)`.
-/

namespace DexExpressiveness.Examples.TimerOrder

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples

/-- `select { m := recv(7) → emit(m) | after(d) → emit(id) }`, with `d` in
variable 0 and `id` in variable 1. -/
def selectorBody : Stmt :=
  .select 2 7 (.emit (.var 2)) (.var 0) (.emit (.var 1))

/-- The body of `main`. -/
def mainBody : Stmt :=
  .seq (.fork 1 0 [.lit (.int 1), .lit (.int 20)])
  (.seq (.fork 2 0 [.lit (.int 0), .lit (.int 10)])
  (.seq (.join (.var 1)) (.seq (.join (.var 2)) (.exit (.lit (.int 0))))))

/-- The program. -/
def program : Program :=
  { procs := [(0, { params := [0, 1], body := selectorBody })]
    mainParam := 0
    mainBody := mainBody
    staticMutexCount := 0 }

/-- `count` actions of one thread, each after a one-microsecond tick. -/
def threadActions (thread : Tid) (count : Nat) : List L.Label :=
  (List.replicate count [L.Label.tick 1, L.Label.thread thread]).flatten

/-- Thread `b`, forked first. -/
def threadB : Tid := [0]

/-- Thread `a`, forked second. -/
def threadA : Tid := [1]

/-- The schedule described in the file comment. -/
def lSchedule : List L.Label :=
  lExtend program [] 100
    ([.request (.start (.int 0))] ++
      -- `main`: boot, fork, fork, and block in `join(b)`.
      threadActions [] 8 ++
      -- `a`: Enter. It takes timer number 1.
      threadActions threadA 2 ++
      -- `b`: Enter (timer number 2), begin the wait, and Arm with `d = 1`.
      threadActions threadB 4 ++
      -- `a`: begin the wait, then Arm with `d = 0` exactly one second after `b` armed.
      threadActions threadA 1 ++
      [.tick 999999, .thread threadA] ++
      -- One `fire`: both timers are due with equal deadlines.
      [.tick 1, .fire 1] ++
      -- A message arrives for the timer that is still armed.
      [.tick 1, .request (.input 7 (.int 99))])

/-- The trace: `a`'s timer fired, and `b` received the message. -/
def trace : List Event :=
  [.begin (.int 0), .input 7 (.int 99), .out (.int 99), .out (.int 10), .exit (.int 0)]

/-- The trace is a finite trace of the L program. -/
theorem l_trace : IsLFiniteTrace program trace :=
  isLFiniteTrace_of_traceOf program lSchedule trace (by decide +kernel)

/-- The trace is a projected finite trace of the translated Flow, under the
Dex schedule that simulates the L schedule label by label. -/
theorem dex_trace : IsProjectedDexFiniteTrace program trace :=
  isProjectedDexFiniteTrace_of_traceOf program (dexScheduleOf program lSchedule) trace
    (by decide +kernel) (by decide +kernel)

end DexExpressiveness.Examples.TimerOrder
