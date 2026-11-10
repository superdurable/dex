import DexExpressiveness.Examples.Lockstep

/-!
# Open issue: a forged mutex handle separates L from its translation

Found in Stage 2a while strengthening the simulation relation. It needs an
owner decision; nothing in the model was changed for it.

```text
locker():  lock(<handle of the mutex main will create>); emit(1)
main(x):   t := fork locker(); sleep(0); m := newMutex(); x := recv(9)
```

`Expr.lit` accepts any value, so a program can write the handle
`dynamic [] 1` of a mutex before `newMutex` creates it. The locker can then
block in `lock` on a mutex that does not exist yet.

- In L, `lock` of a mutex that does not exist blocks, and `newMutex` creates
  the mutex unlocked without looking at blocked threads. The locker stays
  blocked forever, although the mutex is unlocked.
- In Dex, the locker's Step waits on the mutex's token Channel. `newMutex`
  publishes the first token, `wake` gives it to the waiting Step, and the
  locker continues and emits 1.

The program is well formed: it never unlocks, waits, signals, or broadcasts.
So `liveness_of_isWellFormed` is false for it as the model stands: for the
input word `[start 0]`, L has the fair maximal trace `[begin(0)]` with marker
`δ`, and every fair run of the translated Flow emits `out(1)`. The step
correspondence that the safety proof uses also fails, which is how the issue
was found.

The README lists the options. The facts below are checked in the Lean kernel.
-/

namespace DexExpressiveness.Examples.ForgedMutexHandle

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples

/-- The identity of the mutex `main` creates: its second fresh object, after
the forked thread. -/
def createdMutex : MutexId := .dynamic [] 1

/-- `lock(<forged handle>); emit(1)`. -/
def lockerBody : Stmt :=
  .seq (.lock (.lit (.mutex createdMutex))) (.emit (.lit (.int 1)))

/-- `t := fork locker(); sleep(0); m := newMutex(); x := recv(9)`. -/
def mainBody : Stmt :=
  .seq (.fork 1 0 []) (.seq (.sleep (.lit (.int 0))) (.seq (.newMutex 2) (.recv 3 9)))

/-- The program. -/
def program : Program :=
  { procs := [(0, { params := [], body := lockerBody })]
    mainParam := 0
    mainBody := mainBody
    staticMutexCount := 0 }

/-- An L schedule that prefers the locker, thread `[0]`: it blocks in `lock`
before `main` creates the mutex. The schedule runs until no thread, timer, or
late emit can act. -/
def lSchedule : List L.Label :=
  lExtend program [[0]] 200 [.request (.start (.int 0))]

/-- A Dex schedule of the translated Flow, run until no Step, task, or timer can act. -/
def dexSchedule : List (Dex.Action signature) :=
  dexExtend program true 400 [.request (.startFlow StepType.start (DexVal.value (.int 0)))]

/-- Whether an L run ended with the created mutex unlocked and the locker
still blocked in `lock` on it. -/
def isLockerStuck (labels : List L.Label) : Bool :=
  match (L.system program).exec (L.system program).init labels with
  | none => false
  | some (_, final) =>
    decide (final.mutexOwners.get? createdMutex = some none) &&
    final.threads.any (fun thread =>
      match thread.phase with
      | .blocked (.lock mutex) _ => decide (mutex = createdMutex) && decide (thread.core.tid = [0])
      | _ => false)

/-- The L run emits nothing. -/
theorem l_trace : traceOf (L.system program) some lSchedule = some [.begin (.int 0)] := by
  decide +kernel

/-- At the end of the L run the mutex exists and is unlocked, and the locker
is still blocked on it. No later L action can wake the locker: only `unlock`
of this mutex wakes a thread blocked in `lock`, and no thread owns it. -/
theorem l_lockerIsStuck : isLockerStuck lSchedule = true := by
  decide +kernel

/-- The translated Flow emits `out(1)`. -/
theorem dex_trace :
    traceOf (Dex.system ⟦program⟧) project dexSchedule =
      some [.begin (.int 0), .out (.int 1)] := by
  decide +kernel

/-- The lockstep run finds the two sides out of step on this program. -/
theorem lockstep_findsMismatch :
    (lockstepMismatches program [.start (.int 0)] 400 2).isEmpty = false := by
  decide +kernel

end DexExpressiveness.Examples.ForgedMutexHandle
