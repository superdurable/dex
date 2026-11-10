import DexExpressiveness.Examples.Lockstep

/-!
# Open issue: the environment can still hand a program a forged mutex handle

Found in Stage 2a right after handles became unforgeable in the source
syntax (spec §8.1 "Handles and literals"). It needs an owner decision; nothing
in the model was changed for it.

The program input and every external message are L values (`Val`), and a
`Val` can be a mutex handle. So the environment can pass the handle of a
mutex that `newMutex` has not created yet:

```text
locker(h):  lock(h); emit(1)
main(x):    t := fork locker(x); sleep(0); m := newMutex(); y := recv(9)
```

started with the input `x = <handle of the mutex main will create>`.

This is the counterexample of spec Review log row L5 again, with the forged
handle coming from the input instead of a literal:

- In L, the locker blocks in `lock` on a mutex that does not exist, and
  `newMutex` does not serve blocked lockers. The locker stays blocked.
- In Dex, the token that `newMutex` publishes wakes the locker's Step.

The program has no literal handle, so the source syntax accepts it. It is
well formed. So `liveness_of_isWellFormed` is still false for it. The README
lists the options. The facts below are checked in the Lean kernel.
-/

namespace DexExpressiveness.Examples.ForgedHandleInput

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples

/-- The identity of the mutex `main` creates: its second fresh object, after
the forked thread. -/
def createdMutex : MutexId := .dynamic [] 1

/-- The forged program input. -/
def forgedInput : Val := .mutex createdMutex

/-- `lock(h); emit(1)`, with `h` in variable 0. -/
def lockerBody : Stmt :=
  .seq (.lock (.var 0)) (.emit (.lit (.int 1)))

/-- `t := fork locker(x); sleep(0); m := newMutex(); y := recv(9)`, with `x`
in variable 0. -/
def mainBody : Stmt :=
  .seq (.fork 1 0 [.var 0]) (.seq (.sleep (.lit (.int 0))) (.seq (.newMutex 2) (.recv 3 9)))

/-- The program. -/
def program : Program :=
  { procs := [(0, { params := [0], body := lockerBody })]
    mainParam := 0
    mainBody := mainBody
    staticMutexCount := 0 }

/-- An L schedule that prefers the locker, thread `[0]`: it blocks in `lock`
before `main` creates the mutex. The schedule runs until no thread, timer, or
late emit can act. -/
def lSchedule : List L.Label :=
  lExtend program [[0]] 200 [.request (.start forgedInput)]

/-- A Dex schedule of the translated Flow, run until no Step, task, or timer can act. -/
def dexSchedule : List (Dex.Action signature) :=
  dexExtend program true 400 [.request (.startFlow StepType.start (DexVal.value forgedInput))]

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
theorem l_trace : traceOf (L.system program) some lSchedule = some [.begin forgedInput] := by
  decide +kernel

/-- At the end of the L run the mutex exists and is unlocked, and the locker
is still blocked on it. No later L action can wake the locker: only `unlock`
of this mutex wakes a thread blocked in `lock`, and no thread owns it. -/
theorem l_lockerIsStuck : isLockerStuck lSchedule = true := by
  decide +kernel

/-- The translated Flow emits `out(1)`. -/
theorem dex_trace :
    traceOf (Dex.system ⟦program⟧) project dexSchedule =
      some [.begin forgedInput, .out (.int 1)] := by
  decide +kernel

end DexExpressiveness.Examples.ForgedHandleInput
