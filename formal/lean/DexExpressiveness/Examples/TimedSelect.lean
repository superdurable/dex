import DexExpressiveness.Examples.Lockstep

/-!
# Example: timers, `select`, external input, and cancellation, end to end

```text
sleeper():  sleep(100); emit(13)
main(x):    t := fork sleeper()
            sleep(0); emit(1)
            select { m := recv(7) → emit(m) | after(0) → emit(2) }
            m := recv(7); emit(m)
            select { m := recv(7) → emit(m) | after(5) → emit(2) }
            cancel(t); join(t); emit(3)
```

The run below takes the timeout of the first `select`, receives one external
message in `recv` and one in the second `select`, cancels the sleeping thread
at its cancellation point, joins it, and returns from `main`, which is
`exit(null)` (spec §8.1, "Exit"). The canceled thread never emits 13.
-/

namespace DexExpressiveness.Examples.TimedSelect

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples

/-- An integer literal. -/
def int (value : Int) : Expr := .lit (.int value)

/-- `sleep(100); emit(13)`. -/
def sleeperBody : Stmt := .seq (.sleep (int 100)) (.emit (int 13))

/-- The body of `main`. -/
def mainBody : Stmt :=
  .seq (.fork 1 0 [])
  (.seq (.sleep (int 0))
  (.seq (.emit (int 1))
  (.seq (.select 2 7 (.emit (.var 2)) (int 0) (.emit (int 2)))
  (.seq (.recv 3 7)
  (.seq (.emit (.var 3))
  (.seq (.select 2 7 (.emit (.var 2)) (int 5) (.emit (int 2)))
  (.seq (.cancel (.var 1))
  (.seq (.join (.var 1)) (.emit (int 3))))))))))

/-- The program. -/
def program : Program :=
  { procs := [(0, { params := [], body := sleeperBody })]
    mainParam := 0
    mainBody := mainBody
    staticMutexCount := 0 }

/-- The trace of the run described above. -/
def trace : List Event :=
  [.begin (.int 0), .out (.int 1), .out (.int 2),
   .input 7 (.int 50), .out (.int 50),
   .input 7 (.int 51), .out (.int 51),
   .out (.int 3), .exit .null]

/-- The L schedule: run until every thread is blocked, send 50, run until
blocked again, send 51, run to the end. -/
def lSchedule : List L.Label :=
  lExtend program [] 100
    (lExtend program [] 100
      (lExtend program [] 100 [.request (.start (.int 0))] ++
        [.tick 1, .request (.input 7 (.int 50))]) ++
      [.tick 1, .request (.input 7 (.int 51))])

/-- A Dex schedule built by the Dex scheduler, with the same two publications. -/
def dexSchedule : List (Dex.Action signature) :=
  dexExtend program false 100
    (dexExtend program false 100
      (dexExtend program false 100
          [.request (.startFlow StepType.start (DexVal.value (.int 0)))] ++
        [.tick 1, .request (.publish (Chan.external 7) (DexVal.value (.int 50)))]) ++
      [.tick 1, .request (.publish (Chan.external 7) (DexVal.value (.int 51)))])

/-- The trace is a finite trace of the L program. -/
theorem l_trace : IsLFiniteTrace program trace :=
  isLFiniteTrace_of_traceOf program lSchedule trace (by decide +kernel)

/-- The trace is a projected finite trace of the translated Flow. -/
theorem dex_trace : IsProjectedDexFiniteTrace program trace :=
  isProjectedDexFiniteTrace_of_traceOf program dexSchedule trace
    (by decide +kernel) (by decide +kernel)

/-- The Dex schedule that simulates the L schedule label by label has the
same projected trace. -/
theorem dex_trace_ofSimulatedSchedule :
    traceOf (Dex.system ⟦program⟧) project (dexScheduleOf program lSchedule) = some trace := by
  decide +kernel

end DexExpressiveness.Examples.TimedSelect
