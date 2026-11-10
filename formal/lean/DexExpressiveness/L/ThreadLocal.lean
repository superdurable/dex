import DexExpressiveness.Core.Assoc
import DexExpressiveness.L.Syntax

/-!
# L: the thread-local part of the semantics (spec §8.1)

"Local computation is invisible." A thread's own state is its local
variables, its call and handler stack, and the code left in its current
procedure. This file defines what one statement does to that state, and which
atomic action on the shared state it asks for (`Intent`). The global semantics
(`L/Semantics.lean`) performs the action. The translation
(`Translation/Flow.lean`) encodes the same action as a Dex decision, which is
the mapping table of spec §8.3.
-/

namespace DexExpressiveness.L

/-- The local variables of a thread. An unset variable reads null. -/
abbrev Env := AssocList Var Val

/-- One instruction of the code a thread has left to run: a source statement,
or one of the runtime instructions `park` and `relock`. They are not source
syntax (`Stmt` has no such constructor), so no program can contain them; only
`wait(cv, m)` produces them, when it rewrites itself to `park cv; relock m`. -/
inductive Instr
  /-- A source statement. -/
  | source (statement : Stmt)
  /-- The second part of `wait(cv, m)`: the thread stays in `cv`'s queue
  until a signal dequeues it (spec §8.3 `wait`: "Then a Step that waits
  `Until(cv/w.ForOne())`"). -/
  | park (cv : CvId)
  /-- The third part of `wait(cv, m)`: "then `lock(m)` again" (spec §8.3). It
  names the mutex directly, because a mutex handle is not a literal. -/
  | relock (mutex : MutexId)
  deriving DecidableEq, Repr

/-- The code left in the current procedure body, as an instruction list. Its
head is the current instruction. A program point of spec §8.2 is a `Code`. -/
abbrev Code := List Instr

/-- Put `statement` in front of `code`, flattening `seq` and dropping `skip`.
So `seq` and `skip` take no action of their own (spec §8.3: "Sequence: Code
inside one Execute, or GoTo between blocks"). -/
def pushCode : Stmt → Code → Code
  | .skip, code => code
  | .seq first second, code => pushCode first (pushCode second code)
  | statement, code => .source statement :: code

/-- Evaluate an expression. `none` is a dynamic type error, which halts the
thread (spec §8.1, "Halting"). -/
def evalExpr (locals : Env) : Expr → Option Val
  | .lit value => some value.toVal
  | .staticMutex index => some (.mutex (.static index))
  | .var name => some ((locals.get? name).getD .null)
  | .add left right =>
    match evalExpr locals left, evalExpr locals right with
    | some (.int leftValue), some (.int rightValue) => some (.int (leftValue + rightValue))
    | _, _ => none
  | .eq left right =>
    match evalExpr locals left, evalExpr locals right with
    | some leftValue, some rightValue => some (.bool (decide (leftValue = rightValue)))
    | _, _ => none
  | .lt left right =>
    match evalExpr locals left, evalExpr locals right with
    | some (.int leftValue), some (.int rightValue) => some (.bool (decide (leftValue < rightValue)))
    | _, _ => none
  | .not operand =>
    match evalExpr locals operand with
    | some (.bool value) => some (.bool (!value))
    | _ => none
  | .cvOf mutex index =>
    match evalExpr locals mutex with
    | some (.mutex mutexId) => some (.cv { mutex := mutexId, index := index })
    | _ => none

/-- Evaluate call arguments, left to right. -/
def evalArguments (locals : Env) : List Expr → Option (List Val)
  | [] => some []
  | argument :: laterArguments =>
    match evalExpr locals argument, evalArguments locals laterArguments with
    | some value, some laterValues => some (value :: laterValues)
    | _, _ => none

/-- Bind parameters to argument values. `none` on an arity mismatch. -/
def bindParams : List Var → List Val → Option Env
  | [], [] => some []
  | param :: laterParams, value :: laterValues =>
    (bindParams laterParams laterValues).map (fun bound => (param, value) :: bound)
  | _, _ => none

/-- A frame of the explicit stack that spec §8.3 keeps in the Step input
("Push the return point on an explicit stack"; "The handler stack lives in the
Step input"). -/
inductive Frame
  /-- A procedure call: where the result goes, the caller's locals, and the
  caller's code after the call. -/
  | call (resultVar : Var) (savedLocals : Env) (returnCode : Code)
  /-- A `try` in the current procedure: the exception variable, the handler's
  code, and the code after the `try`. -/
  | handler (exceptionVar : Var) (handlerCode : Code) (afterCode : Code)
  deriving DecidableEq, Repr

/-- The thread-local state that a Step input carries (spec §8.2:
"Thread-local variables, the thread ID, and the call stack are the Step
input"). -/
structure ThreadCore where
  /-- The thread ID. -/
  tid : Tid
  /-- The counter for child thread IDs and created mutex IDs (spec §8.3 `fork`:
  "a counter in the input"). -/
  freshCounter : Nat
  /-- The local variables. -/
  locals : Env
  /-- The call and handler stack, innermost first. -/
  stack : List Frame
  deriving DecidableEq, Repr

/-- `locals[x ↦ v]`. -/
def ThreadCore.setLocal (core : ThreadCore) (name : Var) (value : Val) : ThreadCore :=
  { core with locals := core.locals.set name value }

/-- Pop frames up to and including the innermost call frame. `none` when the
thread is in its top-level procedure. -/
def unwindToCall : List Frame → Option (Var × Env × Code × List Frame)
  | [] => none
  | .call resultVar savedLocals returnCode :: outerFrames =>
    some (resultVar, savedLocals, returnCode, outerFrames)
  | .handler _ _ _ :: outerFrames => unwindToCall outerFrames

/-- Pop frames up to and including the innermost handler frame, restoring the
locals of each procedure left on the way. `none` when no handler is installed. -/
def unwindToHandler : Env → List Frame → Option (Var × Code × Env × List Frame)
  | _, [] => none
  | locals, .handler exceptionVar handlerCode _ :: outerFrames =>
    some (exceptionVar, handlerCode, locals, outerFrames)
  | _, .call _ savedLocals _ :: outerFrames => unwindToHandler savedLocals outerFrames

/-- The atomic action on shared state that a thread asks for when a statement
completes. Spec §8.1: "Each single read or write of a shared variable, each
`emit` issue, and each synchronization operation is one atomic action." -/
inductive Intent
  /-- Local computation only. -/
  | silent
  /-- Write a shared variable. -/
  | store (target : Global) (value : Val)
  /-- Start a thread with this state and code. -/
  | fork (child : ThreadCore) (childCode : Code)
  /-- Create a mutex, unlocked. -/
  | newMutex (mutex : MutexId)
  /-- Release a mutex. -/
  | unlock (mutex : MutexId)
  /-- `wait(cv, m)`: "atomically releases `m` and appends the thread to `cv`'s FIFO queue". -/
  | cvWait (cv : CvId)
  /-- `signal(cv)`: "wakes the head of the queue". -/
  | signal (cv : CvId)
  /-- `broadcast(cv)`: "wakes all". -/
  | broadcast (cv : CvId)
  /-- `cancel(t)`: "sets `t`'s flag". -/
  | cancel (thread : Tid)
  /-- Enter of a timed operation: take "the next timer number" (spec §8.1). -/
  | enterTimed (seconds : Nat)
  /-- The thread ends: its top-level procedure returned, or it observed its
  cancel flag. -/
  | endThread
  /-- `exit(v)`, or the return of `main`. -/
  | exit (value : Val)
  /-- `fail(s)`, or an uncaught exception whose value is the string `s`. -/
  | fail (message : String)
  /-- The thread halts (spec §8.1, "Halting"): a dynamic type error, or an
  uncaught exception whose value is not a string. It never ends, so it is
  never joinable. -/
  | halt

/-- What completing a statement yields: the atomic action, and the thread's
state and code afterwards. -/
structure StatementResult where
  /-- The atomic action. -/
  intent : Intent
  /-- The thread-local state afterwards. -/
  core : ThreadCore
  /-- The code afterwards. -/
  code : Code

/-- Return `value` from the current procedure. From a call: continue the
caller. From the top level: "`exit(v)`, or the return of `main`, ends the
program at once"; "A thread that ends otherwise just ends" (spec §8.1). -/
def returnValue (core : ThreadCore) (value : Val) : StatementResult :=
  match unwindToCall core.stack with
  | some (resultVar, savedLocals, returnCode, outerFrames) =>
    { intent := .silent
      core := { core with locals := savedLocals.set resultVar value, stack := outerFrames }
      code := returnCode }
  | none =>
    if core.tid = [] then { intent := .exit value, core := core, code := [] }
    else { intent := .endThread, core := core, code := [] }

/-- Complete the head statement of a thread that is not at a blocking
instruction. `loaded` is the value that a `load` read when the statement began;
other statements ignore it. An empty code is the end of a block: it leaves a
`try`, or returns null from the procedure. -/
def completeStatement (program : Program) (core : ThreadCore) (code : Code) (loaded : Val) :
    StatementResult :=
  let halted : StatementResult := { intent := .halt, core := core, code := code }
  match code with
  | [] =>
    match core.stack with
    | .handler _ _ afterCode :: outerFrames =>
      { intent := .silent, core := { core with stack := outerFrames }, code := afterCode }
    | _ => returnValue core .null
  -- Blocking instructions complete through `resumeBlocked`, never through here.
  | .park _ :: _ => halted
  | .relock _ :: _ => halted
  | .source statement :: rest =>
    match statement with
    | .skip => { intent := .silent, core := core, code := rest }
    | .seq first second =>
      { intent := .silent, core := core, code := pushCode first (pushCode second rest) }
    | .assign target value =>
      match evalExpr core.locals value with
      | some result => { intent := .silent, core := core.setLocal target result, code := rest }
      | none => halted
    | .load target _ => { intent := .silent, core := core.setLocal target loaded, code := rest }
    | .store target value =>
      match evalExpr core.locals value with
      | some result => { intent := .store target result, core := core, code := rest }
      | none => halted
    | .ite condition thenBranch elseBranch =>
      match evalExpr core.locals condition with
      | some (.bool true) => { intent := .silent, core := core, code := pushCode thenBranch rest }
      | some (.bool false) => { intent := .silent, core := core, code := pushCode elseBranch rest }
      | _ => halted
    | .whileLoop condition body =>
      match evalExpr core.locals condition with
      | some (.bool true) =>
        { intent := .silent, core := core, code := pushCode body (.source statement :: rest) }
      | some (.bool false) => { intent := .silent, core := core, code := rest }
      | _ => halted
    | .call result procName arguments =>
      match AssocList.get? program.procs procName, evalArguments core.locals arguments with
      | some proc, some values =>
        match bindParams proc.params values with
        | some bound =>
          { intent := .silent
            core := { core with locals := bound, stack := .call result core.locals rest :: core.stack }
            code := pushCode proc.body [] }
        | none => halted
      | _, _ => halted
    | .ret value =>
      match evalExpr core.locals value with
      | some result => returnValue core result
      | none => halted
    | .fork handle procName arguments =>
      match AssocList.get? program.procs procName, evalArguments core.locals arguments with
      | some proc, some values =>
        match bindParams proc.params values with
        | some bound =>
          let childTid := core.tid ++ [core.freshCounter]
          { intent := .fork { tid := childTid, freshCounter := 0, locals := bound, stack := [] }
              (pushCode proc.body [])
            core := { core with
              freshCounter := core.freshCounter + 1
              locals := core.locals.set handle (.thread childTid) }
            code := rest }
        | none => halted
      | _, _ => halted
    | .newMutex target =>
      let mutex := MutexId.dynamic core.tid core.freshCounter
      { intent := .newMutex mutex
        core := { core with
          freshCounter := core.freshCounter + 1
          locals := core.locals.set target (.mutex mutex) }
        code := rest }
    | .unlock mutex =>
      match evalExpr core.locals mutex with
      | some (.mutex mutexId) => { intent := .unlock mutexId, core := core, code := rest }
      | _ => halted
    | .wait cv mutex =>
      match evalExpr core.locals cv, evalExpr core.locals mutex with
      | some (.cv cvId), some (.mutex mutexId) =>
        if cvId.mutex = mutexId then
          { intent := .cvWait cvId
            core := core
            code := .park cvId :: .relock mutexId :: rest }
        else halted
      | _, _ => halted
    | .signal cv =>
      match evalExpr core.locals cv with
      | some (.cv cvId) => { intent := .signal cvId, core := core, code := rest }
      | _ => halted
    | .broadcast cv =>
      match evalExpr core.locals cv with
      | some (.cv cvId) => { intent := .broadcast cvId, core := core, code := rest }
      | _ => halted
    | .sleep seconds =>
      match evalExpr core.locals seconds with
      | some (.int (.ofNat wholeSeconds)) =>
        { intent := .enterTimed wholeSeconds, core := core, code := code }
      | _ => halted
    | .select _ _ _ seconds _ =>
      match evalExpr core.locals seconds with
      | some (.int (.ofNat wholeSeconds)) =>
        { intent := .enterTimed wholeSeconds, core := core, code := code }
      | _ => halted
    | .emit effect =>
      match evalExpr core.locals effect with
      | some _ => { intent := .silent, core := core, code := rest }
      | none => halted
    | .raise exception =>
      match evalExpr core.locals exception with
      | some thrown =>
        match unwindToHandler core.locals core.stack with
        | some (exceptionVar, handlerCode, restoredLocals, outerFrames) =>
          { intent := .silent
            core := { core with locals := restoredLocals.set exceptionVar thrown, stack := outerFrames }
            code := handlerCode }
        | none =>
          match thrown with
          | .str message => { intent := .fail message, core := core, code := [] }
          | _ => halted
      | none => halted
    | .tryCatch body exceptionVar handler =>
      { intent := .silent
        core := { core with stack := .handler exceptionVar (pushCode handler rest) rest :: core.stack }
        code := pushCode body [] }
    | .cancel thread =>
      match evalExpr core.locals thread with
      | some (.thread target) => { intent := .cancel target, core := core, code := rest }
      | _ => halted
    | .exit value =>
      match evalExpr core.locals value with
      | some result => { intent := .exit result, core := core, code := [] }
      | none => halted
    | .fail message =>
      match evalExpr core.locals message with
      | some (.str text) => { intent := .fail text, core := core, code := [] }
      | _ => halted
    -- Blocking statements complete through `resumeBlocked`, never through here.
    | .join _ => halted
    | .lock _ => halted
    | .recv _ _ => halted

/-- What an Execute-only statement does when it begins, before it completes. -/
inductive BeginKind
  /-- Nothing observable. -/
  | plain
  /-- A shared read: the value is read now (spec §8.2: "a read linearizes at
  [STEP-START]"). -/
  | load (source : Global)
  /-- An emit is issued now (spec §8.1: "`emit(a)` is issued as one atomic
  action; the `out(a)` event happens later"). -/
  | emit (effect : Val)

/-- Classify the beginning of the head statement. -/
def beginKind (core : ThreadCore) : Code → BeginKind
  | .source (.load _ source) :: _ => .load source
  | .source (.emit effect) :: _ =>
    match evalExpr core.locals effect with
    | some value => .emit value
    | none => .plain
  | _ => .plain

/-- Whether the head instruction blocks: `lock`, `recv`, `join`, or the
runtime `park` and `relock`. Timed operations block too, but only after their
Enter; `Thread.timer` tracks that. -/
def isBlockingHead : Code → Bool
  | .source (.lock _) :: _ => true
  | .source (.recv _ _) :: _ => true
  | .source (.join _) :: _ => true
  | .park _ :: _ => true
  | .relock _ :: _ => true
  | _ => false

/-- What a blocking statement waits for. -/
inductive WaitRequest
  /-- `lock(m)`. -/
  | lock (mutex : MutexId)
  /-- `recv(c)`. -/
  | recv (chan : ExtChan)
  /-- `join(t)`. -/
  | join (thread : Tid)
  /-- The runtime `park cv`. -/
  | park (cv : CvId)
  deriving DecidableEq, Repr

/-- The wait of the head blocking statement; `none` on a dynamic type error. -/
def waitRequest (core : ThreadCore) : Code → Option WaitRequest
  | .source (.lock mutex) :: _ =>
    match evalExpr core.locals mutex with
    | some (.mutex mutexId) => some (.lock mutexId)
    | _ => none
  | .source (.recv _ chan) :: _ => some (.recv chan)
  | .source (.join thread) :: _ =>
    match evalExpr core.locals thread with
    | some (.thread target) => some (.join target)
    | _ => none
  | .park cv :: _ => some (.park cv)
  | .relock mutex :: _ => some (.lock mutex)
  | _ => none

/-- How a blocking or timed operation was decided (spec §8.1: "A blocked timed
operation is decided by whichever happens first: its `fire`, the arrival of
its message, or a cancel"). -/
inductive WakeOutcome
  /-- The mutex was acquired, the thread was joined, or the signal arrived. -/
  | granted
  /-- A message arrived. -/
  | received (message : Val)
  /-- The timer fired. -/
  | fired
  /-- The cancel flag was observed. -/
  | canceled
  /-- The operand had a dynamic type error; nothing was waited for. -/
  | invalid
  deriving DecidableEq, Repr

/-- Continue after a blocking statement was decided. A canceled thread "ends
without releasing anything it holds" (spec §8.1). -/
def resumeBlocked (core : ThreadCore) (code : Code) (outcome : WakeOutcome) : StatementResult :=
  let halted : StatementResult := { intent := .halt, core := core, code := code }
  let ended : StatementResult := { intent := .endThread, core := core, code := [] }
  match code, outcome with
  | .source (.lock _) :: rest, .granted => { intent := .silent, core := core, code := rest }
  | .park _ :: rest, .granted => { intent := .silent, core := core, code := rest }
  | .relock _ :: rest, .granted => { intent := .silent, core := core, code := rest }
  | .source (.join _) :: rest, .granted => { intent := .silent, core := core, code := rest }
  | .source (.join _) :: _, .canceled => ended
  | .source (.recv target _) :: rest, .received message =>
    { intent := .silent, core := core.setLocal target message, code := rest }
  | .source (.recv _ _) :: _, .canceled => ended
  | _, _ => halted

/-- The external channel a timed operation also waits on: `some none` for
`sleep`, `some (some c)` for `select`, `none` when the head is not a timed
operation. -/
def timedChan : Code → Option (Option ExtChan)
  | .source (.sleep _) :: _ => some none
  | .source (.select _ chan _ _ _) :: _ => some (some chan)
  | _ => none

/-- Continue after a timed operation was decided. -/
def resumeTimed (core : ThreadCore) (code : Code) (outcome : WakeOutcome) : StatementResult :=
  let halted : StatementResult := { intent := .halt, core := core, code := code }
  let ended : StatementResult := { intent := .endThread, core := core, code := [] }
  match code, outcome with
  | .source (.sleep _) :: rest, .fired => { intent := .silent, core := core, code := rest }
  | .source (.sleep _) :: _, .canceled => ended
  | .source (.select _ _ _ _ onTimeout) :: rest, .fired =>
    { intent := .silent, core := core, code := pushCode onTimeout rest }
  | .source (.select target _ onMessage _ _) :: rest, .received message =>
    { intent := .silent, core := core.setLocal target message, code := pushCode onMessage rest }
  | .source (.select _ _ _ _ _) :: _, .canceled => ended
  | _, _ => halted

/-- The condition variable that the head statement operates on: the `cv` of
`wait(cv, m)`, `signal(cv)`, or `broadcast(cv)`. `none` for any other head, and
when the operand has a dynamic type error. -/
def cvOperand (core : ThreadCore) : Code → Option CvId
  | .source (.wait cv mutex) :: _ =>
    match evalExpr core.locals cv, evalExpr core.locals mutex with
    | some (.cv cvId), some (.mutex mutexId) => if cvId.mutex = mutexId then some cvId else none
    | _, _ => none
  | .source (.signal cv) :: _ =>
    match evalExpr core.locals cv with
    | some (.cv cvId) => some cvId
    | _ => none
  | .source (.broadcast cv) :: _ =>
    match evalExpr core.locals cv with
    | some (.cv cvId) => some cvId
    | _ => none
  | _ => none

/-- The mutex that the head statement requires the thread to hold (spec §8.1):

- `unlock(m)` requires `m`: a thread releases only a mutex it holds;
- "`wait(cv, m)` requires `m` held";
- "`signal(cv)` and `broadcast(cv)` require `m` held", `m` being `cv`'s mutex.

`none` for any other head, and when the operand has a dynamic type error (the
statement then halts the thread instead). -/
def requiredMutex (core : ThreadCore) : Code → Option MutexId
  | .source (.unlock mutex) :: _ =>
    match evalExpr core.locals mutex with
    | some (.mutex mutexId) => some mutexId
    | _ => none
  | code => (cvOperand core code).map (fun cvId => cvId.mutex)

end DexExpressiveness.L
