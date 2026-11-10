import DexExpressiveness.Examples.Lockstep

/-!
# Example: two threads and one mutex, end to end on concrete traces

```text
worker():  lock(m0); emit(2); unlock(m0)
main(x):   t := fork worker(); lock(m0); emit(1); unlock(m0); join(t); exit(0)
```

The program has two interleavings of its critical sections, so two complete
traces. Each is shown to be a finite trace of the L program and a projected
finite trace of its translated Flow, by running an explicit schedule in the
Lean kernel. These are four instances of the two sets that the safety
statement (spec §5.4) equates.
-/

namespace DexExpressiveness.Examples.Mutex

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples

/-- The statically known mutex `m0`. -/
def m0 : Expr := .lit (.mutex (.static 0))

/-- `lock(m0); emit(2); unlock(m0)`. -/
def workerBody : Stmt :=
  .seq (.lock m0) (.seq (.emit (.lit (.int 2))) (.unlock m0))

/-- `t := fork worker(); lock(m0); emit(1); unlock(m0); join(t); exit(0)`. -/
def mainBody : Stmt :=
  .seq (.fork 1 0 [])
    (.seq (.lock m0)
      (.seq (.emit (.lit (.int 1)))
        (.seq (.unlock m0) (.seq (.join (.var 1)) (.exit (.lit (.int 0)))))))

/-- The two-thread mutex program. -/
def program : Program :=
  { procs := [(0, { params := [], body := workerBody })]
    mainParam := 0
    mainBody := mainBody
    staticMutexCount := 1 }

/-- The trace in which `main` enters its critical section first. -/
def mainFirstTrace : List Event :=
  [.begin (.int 7), .out (.int 1), .out (.int 2), .exit (.int 0)]

/-- The trace in which the worker enters its critical section first. -/
def workerFirstTrace : List Event :=
  [.begin (.int 7), .out (.int 2), .out (.int 1), .exit (.int 0)]

/-- An L schedule that always prefers `main`. -/
def lMainFirst : List L.Label :=
  lExtend program [] 100 [.request (.start (.int 7))]

/-- An L schedule that always prefers the worker, thread `[0]`. -/
def lWorkerFirst : List L.Label :=
  lExtend program [[0]] 100 [.request (.start (.int 7))]

/-- A Dex schedule that finishes each Step method before it starts another. -/
def dexMainFirst : List (Dex.Action signature) :=
  dexExtend program false 100 [.request (.startFlow StepType.start (DexVal.value (.int 7)))]

/-- The Dex schedule that simulates `lWorkerFirst` label by label
(`Examples/Lockstep.lean`): each L label becomes the Dex actions that are
meant to simulate it. -/
def dexWorkerFirst : List (Dex.Action signature) :=
  dexScheduleOf program lWorkerFirst

/-- `main` first is a finite trace of the L program. -/
theorem l_mainFirst : IsLFiniteTrace program mainFirstTrace :=
  isLFiniteTrace_of_traceOf program lMainFirst mainFirstTrace (by decide +kernel)

/-- The worker first is a finite trace of the L program. -/
theorem l_workerFirst : IsLFiniteTrace program workerFirstTrace :=
  isLFiniteTrace_of_traceOf program lWorkerFirst workerFirstTrace (by decide +kernel)

/-- `main` first is a projected finite trace of the translated Flow. -/
theorem dex_mainFirst : IsProjectedDexFiniteTrace program mainFirstTrace :=
  isProjectedDexFiniteTrace_of_traceOf program dexMainFirst mainFirstTrace
    (by decide +kernel) (by decide +kernel)

/-- The worker first is a projected finite trace of the translated Flow. -/
theorem dex_workerFirst : IsProjectedDexFiniteTrace program workerFirstTrace :=
  isProjectedDexFiniteTrace_of_traceOf program dexWorkerFirst workerFirstTrace
    (by decide +kernel) (by decide +kernel)

/-- Both sides have both traces: the instances of the safety statement
(spec §5.4) for the two complete traces of this program. -/
theorem bothTraces_onBothSides :
    (IsProjectedDexFiniteTrace program mainFirstTrace ∧ IsLFiniteTrace program mainFirstTrace) ∧
    (IsProjectedDexFiniteTrace program workerFirstTrace ∧
      IsLFiniteTrace program workerFirstTrace) :=
  ⟨⟨dex_mainFirst, l_mainFirst⟩, ⟨dex_workerFirst, l_workerFirst⟩⟩

end DexExpressiveness.Examples.Mutex
