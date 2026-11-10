import DexExpressiveness.Examples.Lockstep
import DexExpressiveness.Examples.Mutex
import DexExpressiveness.Examples.TimedSelect

/-!
# Lockstep checks on pseudo-random schedules

Each `#guard` runs an L program and its translated Flow side by side
(`Examples/Lockstep.lean`), in both directions, on pseudo-random schedules,
and fails the build when the events or the clock stamps of the two sides ever
differ. This is testing, not proof: it exercises the step correspondence that
`Simulation.lean` states, on every construct of L.

The checks run in the Lean interpreter when the file is built. The seed counts
are kept small so that the build stays fast.
-/

namespace DexExpressiveness.Examples.LockstepChecks

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples

/-- An integer literal. -/
def int (value : Int) : Expr := .lit (.int value)

/-- A statement sequence. -/
def block : List Stmt → Stmt
  | [] => .skip
  | [statement] => statement
  | statement :: laterStatements => .seq statement (block laterStatements)

/-- The requests every check offers, in order: a start and three inputs. -/
def requests : List L.Request :=
  [.start (.int 3), .input 7 (.int 50), .input 7 (.int 51), .input 3 (.int 9)]

/-- The statically known mutex `m0`. -/
def m0 : Expr := .staticMutex 0

/-- The condition variable 0 of `m0`. -/
def cv0 : Expr := .cvOf m0 0

/-- Recursion, calls, shared variables, `while`, `if`, and the return of `main`:
`sum(n) = if n < 1 then 0 else sum(n - 1) + n`. -/
def sumBody : Stmt :=
  .ite (.lt (.var 0) (int 1)) (.ret (int 0))
    (block [.call 1 0 [.add (.var 0) (int (-1))], .ret (.add (.var 1) (.var 0))])

/-- `main` calls `sum`, stores and reloads the result, emits it, loops twice,
and returns it. -/
def recursionProgram : Program :=
  { procs := [(0, { params := [0], body := sumBody })]
    mainParam := 0
    mainBody := block
      [.call 1 0 [.var 0], .store 5 (.var 1), .skip, .load 2 5, .emit (.var 2), .assign 3 (int 0),
       .whileLoop (.lt (.var 3) (int 2)) (block [.emit (.var 3), .assign 3 (.add (.var 3) (int 1))]),
       .ret (.var 2)]
    staticMutexCount := 0 }

/-- `throw` and `try` across a call, then an uncaught exception, which fails
the program with its string. -/
def exceptionProgram : Program :=
  { procs := [(0, { params := [], body := .raise (.lit (.str "boom")) })]
    mainParam := 0
    mainBody := block
      [.tryCatch (block [.call 1 0 [], .emit (int 99)]) 2 (.emit (.var 2)),
       .emit (int 5), .call 1 0 []]
    staticMutexCount := 0 }

/-- Three threads increment a shared counter without a lock. Updates can be
lost (spec §7 item 1); both models must lose the same ones. -/
def lostUpdateProgram : Program :=
  { procs := [(0, { params := [], body := block [.load 1 0, .store 0 (.add (.var 1) (int 1))] })]
    mainParam := 0
    mainBody := block
      [.store 0 (int 0), .fork 1 0 [], .fork 2 0 [], .fork 3 0 [],
       .join (.var 1), .join (.var 2), .join (.var 3), .load 4 0, .exit (.var 4)]
    staticMutexCount := 0 }

/-- Two consumers wait on a condition variable for a shared value; `main`
writes it and broadcasts. -/
def consumerBody : Stmt :=
  block
    [.lock m0, .load 1 0,
     .whileLoop (.eq (.var 1) (.lit .null)) (block [.wait cv0 m0, .load 1 0]),
     .emit (.var 1), .unlock m0]

/-- `main` forks two consumers, writes the value under the mutex, and broadcasts. -/
def broadcastProgram : Program :=
  { procs := [(0, { params := [], body := consumerBody })]
    mainParam := 0
    mainBody := block
      [.fork 1 0 [], .fork 2 0 [], .lock m0, .store 0 (int 42), .broadcast cv0, .unlock m0,
       .join (.var 1), .join (.var 2), .exit (int 1)]
    staticMutexCount := 1 }

/-- Two waiters and three signals, one of which may find the queue empty. -/
def waiterBody : Stmt :=
  block [.lock m0, .wait cv0 m0, .emit (.var 0), .unlock m0]

/-- `main` forks two waiters and signals three times. -/
def signalProgram : Program :=
  { procs := [(0, { params := [0], body := waiterBody })]
    mainParam := 0
    mainBody := block
      [.fork 1 0 [int 1], .fork 2 0 [int 2], .sleep (int 0), .lock m0, .signal cv0, .unlock m0,
       .lock m0, .signal cv0, .signal cv0, .unlock m0, .join (.var 1), .join (.var 2),
       .exit (int 0)]
    staticMutexCount := 1 }

/-- Three threads race in `select` for two inputs, their timers, and two
cancels. Two of them arm one-second timers, so equal deadlines and the
timer-number tie-break occur. -/
def selectorBody : Stmt :=
  block
    [.select 1 7 (.emit (.var 1)) (.var 0) (.emit (.add (.var 0) (int 100))),
     .select 1 7 (.emit (.var 1)) (int 0) (.emit (int 200)),
     .recv 2 3, .emit (.var 2)]

/-- `main` forks three selectors, sleeps, cancels two of them, and joins all. -/
def selectRaceProgram : Program :=
  { procs := [(0, { params := [0], body := selectorBody })]
    mainParam := 0
    mainBody := block
      [.fork 1 0 [int 1], .fork 2 0 [int 1], .fork 3 0 [int 0], .sleep (int 1),
       .cancel (.var 1), .cancel (.var 3), .join (.var 1), .emit (int 77),
       .join (.var 2), .join (.var 3)]
    staticMutexCount := 0 }

/-- `exit` while another thread's emit is issued: `out(8)` before the exit,
after it, or never; then inputs are refused. -/
def lateEmitProgram : Program :=
  { procs := [(0, { params := [], body := .emit (int 8) })]
    mainParam := 0
    mainBody := block [.fork 1 0 [], .exit (.lit .null)]
    staticMutexCount := 0 }

/-- A created mutex, locked twice in a row with an unlock between, then `fail`. -/
def newMutexProgram : Program :=
  { procs := []
    mainParam := 0
    mainBody := block
      [.newMutex 1, .lock (.var 1), .unlock (.var 1), .lock (.var 1), .fail (.lit (.str "bad"))]
    staticMutexCount := 0 }

/-- A thread is canceled before or after it blocks in `recv` and ends
without its second emit; `main` joins it and then blocks forever: the program
is stuck, the `δ` outcome. -/
def cancelProgram : Program :=
  { procs := [(0, { params := [], body := block [.emit (int 1), .recv 1 4, .emit (int 2)] })]
    mainParam := 0
    mainBody := block [.fork 1 0 [], .cancel (.var 1), .join (.var 1), .emit (int 3), .recv 2 5]
    staticMutexCount := 0 }

/-- A dynamic type error in a blocking statement: the thread halts. -/
def typeErrorProgram : Program :=
  { procs := []
    mainParam := 0
    mainBody := block [.emit (int 1), .lock (int 3), .emit (int 2)]
    staticMutexCount := 0 }

#guard lockstepMismatches Mutex.program requests 300 12 == []
#guard lockstepMismatches TimedSelect.program requests 500 12 == []
#guard lockstepMismatches recursionProgram requests 400 6 == []
#guard lockstepMismatches exceptionProgram requests 300 6 == []
#guard lockstepMismatches lostUpdateProgram requests 600 12 == []
#guard lockstepMismatches broadcastProgram requests 700 12 == []
#guard lockstepMismatches signalProgram requests 700 12 == []
#guard lockstepMismatches selectRaceProgram requests 800 24 == []
#guard lockstepMismatches lateEmitProgram requests 200 16 == []
#guard lockstepMismatches newMutexProgram requests 200 4 == []
#guard lockstepMismatches cancelProgram requests 300 8 == []
#guard lockstepMismatches typeErrorProgram requests 200 4 == []

end DexExpressiveness.Examples.LockstepChecks
