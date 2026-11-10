/-!
# L: syntax (spec §8.1)

"L has threads with local variables, shared variables with sequentially
consistent single reads and writes, `seq`, `if`, `while`, procedure calls with
recursion, `fork`/`join`, mutexes, condition variables, `sleep(d)`,
`select { recv(c) → s | after(d) → s }`, `recv(c)` on external channels,
`emit(a)`, `throw`/`try`, `cancel(t)`, `exit(v)`, and `fail(s)`."

Spec §8.1 fixes the constructs and their behavior, not a concrete grammar. The
grammar below is this development's choice; the README lists each choice.
-/

namespace DexExpressiveness.L

/-- A thread-local variable name. -/
abbrev Var := Nat

/-- A shared variable name. -/
abbrev Global := Nat

/-- A procedure name. -/
abbrev ProcName := Nat

/-- An external input channel name (spec §8.1, "External input"). -/
abbrev ExtChan := Nat

/-- A thread ID: the path of fork indices from `main`, which is `[]`. Spec
§8.3 derives a forked thread's ID "from the parent's ID and a counter". -/
abbrev Tid := List Nat

/-- A mutex identity: one of the program's statically known mutexes, or one
created by `newMutex` (spec §8.2, §8.3 "New mutex"). A created mutex is named
by its creating thread and that thread's counter. -/
inductive MutexId
  /-- The statically known mutex with this index. -/
  | static (index : Nat)
  /-- The mutex created by thread `creator` as its `index`-th fresh object. -/
  | dynamic (creator : Tid) (index : Nat)
  deriving DecidableEq, Repr

/-- A condition variable identity. It names its mutex: spec §8.1 says
"`signal(cv)` and `broadcast(cv)` require `m` held" without saying how `m` is
found from `cv`, so the identity carries it. -/
structure CvId where
  /-- The mutex `m` every operation on this condition variable requires. -/
  mutex : MutexId
  /-- The index of the condition variable among those of its mutex. -/
  index : Nat
  deriving DecidableEq, Repr

/-- An L value. Program inputs, external messages, emitted effects, and exit
values are L values. `exit(v)` "may carry any value, including null";
`fail(s)` carries a string (spec §8.1, "Exit"). -/
inductive Val
  /-- L's null. -/
  | null
  /-- A Boolean. -/
  | bool (value : Bool)
  /-- An integer. -/
  | int (value : Int)
  /-- A string. -/
  | str (value : String)
  /-- A thread handle. -/
  | thread (thread : Tid)
  /-- A mutex handle. -/
  | mutex (mutex : MutexId)
  /-- A condition variable handle. -/
  | cv (cv : CvId)
  deriving DecidableEq, Repr

/-- A pure expression over thread-local variables. "Local computation is
invisible" (spec §8.1), so expressions never touch shared state. -/
inductive Expr
  /-- A literal value. -/
  | lit (value : Val)
  /-- A local variable; an unset variable reads null. -/
  | var (name : Var)
  /-- Integer addition. -/
  | add (left right : Expr)
  /-- Equality of two values. -/
  | eq (left right : Expr)
  /-- Integer strict order. -/
  | lt (left right : Expr)
  /-- Boolean negation. -/
  | not (operand : Expr)
  /-- The condition variable `index` of the mutex `mutex` evaluates to. -/
  | cvOf (mutex : Expr) (index : Nat)
  deriving DecidableEq, Repr

/-- An L source statement (spec §8.1). Every constructor is a construct that
spec §8.1 lists; a `Program` is built from these only. -/
inductive Stmt
  /-- Do nothing. -/
  | skip
  /-- `seq`: `first` then `second`. -/
  | seq (first second : Stmt)
  /-- Local assignment `x := e`. -/
  | assign (target : Var) (value : Expr)
  /-- Shared read `x := g`: one atomic read of the shared variable `g`. -/
  | load (target : Var) (source : Global)
  /-- Shared write `g := e`: one atomic write of the shared variable `g`. -/
  | store (target : Global) (value : Expr)
  /-- `if`. -/
  | ite (condition : Expr) (thenBranch elseBranch : Stmt)
  /-- `while`. -/
  | whileLoop (condition : Expr) (body : Stmt)
  /-- Procedure call `x := p(args)`. -/
  | call (result : Var) (proc : ProcName) (arguments : List Expr)
  /-- Return from the current procedure. A return of `main` is `exit`. -/
  | ret (value : Expr)
  /-- `fork`: start a thread that runs `p(args)`; `handle` receives its thread handle. -/
  | fork (handle : Var) (proc : ProcName) (arguments : List Expr)
  /-- `join(t)`. A cancellation point. -/
  | join (thread : Expr)
  /-- Create a mutex, unlocked; `target` receives its handle (spec §8.3 "New mutex"). -/
  | newMutex (target : Var)
  /-- `lock(m)`. Not a cancellation point. -/
  | lock (mutex : Expr)
  /-- `unlock(m)`. -/
  | unlock (mutex : Expr)
  /-- `wait(cv, m)`. Not a cancellation point. -/
  | wait (cv : Expr) (mutex : Expr)
  /-- `signal(cv)`. -/
  | signal (cv : Expr)
  /-- `broadcast(cv)`. -/
  | broadcast (cv : Expr)
  /-- `sleep(d)`, `d` in whole seconds. A cancellation point. -/
  | sleep (seconds : Expr)
  /-- `select { recv(c) → onMessage | after(d) → onTimeout }`; `target`
  receives the message. A cancellation point. -/
  | select (target : Var) (chan : ExtChan) (onMessage : Stmt) (seconds : Expr) (onTimeout : Stmt)
  /-- `x := recv(c)` on an external channel. A cancellation point. -/
  | recv (target : Var) (chan : ExtChan)
  /-- `emit(a)`. -/
  | emit (effect : Expr)
  /-- `throw`. -/
  | raise (exception : Expr)
  /-- `try body catch (x) handler`. -/
  | tryCatch (body : Stmt) (exceptionVar : Var) (handler : Stmt)
  /-- `cancel(t)`. -/
  | cancel (thread : Expr)
  /-- `exit(v)`. -/
  | exit (value : Expr)
  /-- `fail(s)`. -/
  | fail (message : Expr)
  deriving DecidableEq, Repr

/-- A procedure. -/
structure Proc where
  /-- The parameter names. -/
  params : List Var
  /-- The body. -/
  body : Stmt
  deriving DecidableEq, Repr

/-- An L program `S`. -/
structure Program where
  /-- The procedures that `call` and `fork` name. -/
  procs : List (ProcName × Proc)
  /-- The variable of `main` that receives the program input. -/
  mainParam : Var
  /-- The body of `main`. -/
  mainBody : Stmt
  /-- The number of statically known mutexes: `static 0` to `static (count - 1)`
  exist, unlocked, when the program begins (spec §8.2). -/
  staticMutexCount : Nat

/-- L's observable alphabet `Obs_L` (spec §8.1): "`begin(v)`, `in(c, v)`,
`refused(c, v)`, `out(a)`, `exit(v)`, and `fail(s)`". -/
inductive Event
  /-- `begin(v)`: the program starts with input `v`. -/
  | begin (input : Val)
  /-- `in(c, v)`: the environment's message `v` arrives on external channel `c`. -/
  | input (chan : ExtChan) (value : Val)
  /-- `refused(c, v)`: "Input that arrives after exit is `refused`." -/
  | refused (chan : ExtChan) (value : Val)
  /-- `out(a)`: an emitted effect is delivered. -/
  | out (effect : Val)
  /-- `exit(v)`. -/
  | exit (value : Val)
  /-- `fail(s)`. -/
  | fail (message : String)
  deriving DecidableEq, Repr

/-- A request of L's environment: the L form of an attempted client request
(spec §5.3). -/
inductive Request
  /-- Start the program with this input. -/
  | start (input : Val)
  /-- Send this message on this external channel. -/
  | input (chan : ExtChan) (value : Val)
  deriving DecidableEq, Repr

end DexExpressiveness.L
