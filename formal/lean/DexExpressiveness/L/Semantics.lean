import DexExpressiveness.Core.TransitionSystem
import DexExpressiveness.L.ThreadLocal

/-!
# L: small-step semantics (spec §8.1)

Each bullet of spec §8.1 is cited at the rule that implements it.

## The timing model (spec §8.1, "Clock and timing model")

L has a fixed timing model [owner, 2026-11-09]: each kind of statement takes a
fixed number of clock-stamped actions, equal to the number of Dex commits of
its translation, so an L run and its Dex run can carry the same timestamps.
The counts, as spec §8.1 tabulates them:

| L step | Stamped actions |
| --- | --- |
| `begin(v)` | 1, then 2 more before `main`'s first statement |
| `seq`, `skip` | 0 |
| a statement that does not block; the end of a `try` body or procedure body | 2: begin, complete |
| `lock`, `recv`, `join` | 4: begin the wait, register, begin the continuation, complete it |
| `wait(cv, m)` | 10: 2 to release and enqueue, 4 to wait for the signal, 4 to re-lock |
| `sleep`, `select` | 6: 2 for Enter, then 4, of which the register is Arm |
| `in(c, v)` while the program runs | 1 |
| a batch of `count ≥ 1` consecutive `fire` actions | 1 |
| `out(a)`, the delivery or drop of a late emit, `refused(c, v)` | 0 |

A shared read happens at begin, an `emit` is issued at begin, and every other
atomic action happens at complete or at register.

The owner's rationale: real programs cannot rely on timing races. A program
whose outcomes do not depend on the order of two deadlines means the same
under this timing model as under any other. The theorem adopts this one
model, the one aligned with Dex, which makes the equivalence exact without
narrowing what the theorem covers in practice. It is like counting one time
unit per instruction in algorithm analysis. The theorem does not say that a
program is equivalent to itself under another cost model: other counts can
reorder two deadlines and change the outcome of a `select`.
-/

namespace DexExpressiveness.L

/-- What a blocked thread waits for. -/
inductive BlockedOn
  /-- Blocked in `lock(m)`. -/
  | lock (mutex : MutexId)
  /-- Blocked in `recv(c)`. -/
  | recv (chan : ExtChan)
  /-- Blocked in `join(t)`. -/
  | join (thread : Tid)
  /-- Waiting in the queue of `cv` for a signal. -/
  | park (cv : CvId)
  /-- Blocked in a timed operation with this timer number and deadline, and,
  for `select`, on this external channel. The timer is armed. -/
  | timed (number : Nat) (deadline : Nat) (chan : Option ExtChan)
  deriving DecidableEq, Repr

/-- Where a thread is in the actions of its current statement. -/
inductive Phase
  /-- `main` before the program's start action began. -/
  | bootPending
  /-- `main` while the program's start action runs. -/
  | bootRunning
  /-- The next action of the statement has not begun. -/
  | ready
  /-- A statement began; `loaded` is what a `load` read. -/
  | running (loaded : Val)
  /-- `emit(a)` was issued; `out(a)` has not happened. -/
  | emitting (effect : Val)
  /-- `out(a)` happened; the thread has not continued. -/
  | emitted
  /-- A blocking statement began; its wait is not registered yet. -/
  | waitStarted
  /-- Blocked. `since` orders blocked threads: FIFO "order of blocking". -/
  | blocked (on : BlockedOn) (since : Nat)
  /-- The wait was decided; the continuation has not begun. -/
  | woken (outcome : WakeOutcome)
  /-- The continuation began. -/
  | resuming (outcome : WakeOutcome)
  /-- Halted by a dynamic type error or by an uncaught exception that is not
  a string (spec §8.1, "Halting"). The thread takes no further action. -/
  | halted
  deriving DecidableEq, Repr

/-- A thread. -/
structure Thread where
  /-- Thread ID, counter, locals, and stack. -/
  core : ThreadCore
  /-- The code left in the current procedure. -/
  code : Code
  /-- The timer number and whole seconds of the timed operation the thread
  has entered and not finished; `none` otherwise (spec §8.1: "*Enter* gives it
  the next timer number `n`"). -/
  timer : Option (Nat × Nat)
  /-- The phase. -/
  phase : Phase

/-- Whether the program has begun and whether it has ended. -/
inductive Status
  /-- Before `begin(v)`. -/
  | notStarted
  /-- Running. -/
  | running
  /-- After `exit(v)` or `fail(s)`. -/
  | closed
  deriving DecidableEq, Repr

/-- A configuration of an L program. -/
structure Config where
  /-- Not started, running, or closed. -/
  status : Status
  /-- "L has an internal clock in microseconds" (spec §8.1, "Clock and timing model"). -/
  clock : Nat
  /-- The clock value of the last stamped action, or `none` before the first.
  A stamped action needs `clock` strictly above it. -/
  lastStampAt : Option Nat
  /-- The live threads, in creation order. -/
  threads : List Thread
  /-- The shared variables; an unset variable reads null. -/
  shared : AssocList Global Val
  /-- The owner of each existing mutex; `none` is unlocked. -/
  mutexOwners : AssocList MutexId (Option Tid)
  /-- "`cv`'s FIFO queue" of waiting threads (spec §8.1, "Condition variable"). -/
  cvQueues : AssocList CvId (List Tid)
  /-- Waiters that a signal dequeued before they parked. -/
  cvWakes : List (CvId × Tid)
  /-- "Each external channel is a FIFO queue" (spec §8.1, "External input"). -/
  inputQueues : AssocList ExtChan (List Val)
  /-- The threads whose cancel flag is set (spec §8.1: "`cancel(t)` sets `t`'s flag"). -/
  cancelFlags : List Tid
  /-- The threads that ended and were not joined yet. -/
  joinable : List Tid
  /-- The next `since` stamp of a blocked thread. -/
  blockCounter : Nat
  /-- The "one program-wide counter" of timer numbers (spec §8.1). -/
  timerCounter : Nat
  /-- After the close: the emits that were issued and not yet delivered or
  dropped (spec §8.1: "`out(a)` may still happen after `exit` or never happen"). -/
  lateEmits : List Val

/-- The configuration before `begin(v)`. -/
def Config.initial : Config :=
  { status := .notStarted, clock := 0, lastStampAt := none, threads := [], shared := [],
    mutexOwners := [], cvQueues := [], cvWakes := [], inputQueues := [], cancelFlags := [],
    joinable := [], blockCounter := 0, timerCounter := 0, lateEmits := [] }

/-- A transition label, with the nondeterministic choice of its rule. -/
inductive Label
  /-- An idle tick of `duration > 0` microseconds (spec §8.1: the clock
  "advances by idle ticks"). -/
  | tick (duration : Nat)
  /-- The environment attempts a request. -/
  | request (request : Request)
  /-- This thread takes its next action. -/
  | thread (thread : Tid)
  /-- The internal action `fire`, `count ≥ 1` times in a row under one clock
  stamp (spec §8.1: `fire` "shares its clock stamp with the `fire` actions
  that follow it with nothing in between"). -/
  | fire (count : Nat)
  /-- After the close: the late emit at this index delivers its `out(a)`. -/
  | lateDeliver (index : Nat)
  /-- After the close: the late emit at this index is dropped. -/
  | lateDrop (index : Nat)

namespace Config

/-- Whether a stamped action may happen now: "the clock strictly increases
between any two stamped actions" (spec §8.1, "Clock and timing model"). -/
def canStamp (config : Config) : Bool :=
  match config.lastStampAt with
  | none => true
  | some last => decide (last < config.clock)

/-- Record that a stamped action happened at the current clock value. -/
def stamp (config : Config) : Config :=
  { config with lastStampAt := some config.clock }

/-- The thread with this ID. -/
def findThread? (config : Config) (thread : Tid) : Option Thread :=
  config.threads.find? (fun candidate => decide (candidate.core.tid = thread))

/-- Change the thread with this ID. -/
def updateThread (config : Config) (thread : Tid) (change : Thread → Thread) : Config :=
  { config with
    threads := config.threads.map (fun candidate =>
      if candidate.core.tid = thread then change candidate else candidate) }

/-- Set the phase of a thread. -/
def setPhase (config : Config) (thread : Tid) (phase : Phase) : Config :=
  config.updateThread thread (fun current => { current with phase := phase })

/-- Decide the wait of a thread: it leaves every queue and its timer is
disarmed, because both are read off its `blocked` phase. -/
def wakeThread (config : Config) (thread : Tid) (outcome : WakeOutcome) : Config :=
  config.setPhase thread (.woken outcome)

/-- Block a thread, behind every thread that blocked before it. -/
def blockThread (config : Config) (thread : Tid) (on : BlockedOn) : Config :=
  { config.setPhase thread (.blocked on config.blockCounter) with
    blockCounter := config.blockCounter + 1 }

/-- The thread that blocked first among those whose wait satisfies `isWanted`. -/
def earliestBlocked (config : Config) (isWanted : BlockedOn → Bool) : Option Tid :=
  let candidates := config.threads.filterMap (fun candidate =>
    match candidate.phase with
    | .blocked on since => if isWanted on then some (since, candidate.core.tid) else none
    | _ => none)
  (sortStable (fun first second => decide (first.1 ≤ second.1)) candidates).head?.map
    (fun candidate => candidate.2)

/-- The value of a shared variable. -/
def readShared (config : Config) (source : Global) : Val :=
  (config.shared.get? source).getD .null

/-- The queued messages of an external channel. -/
def pendingInput (config : Config) (chan : ExtChan) : List Val :=
  (config.inputQueues.get? chan).getD []

/-- Whether the thread holds the mutex its head statement requires, if any. -/
def holdsRequiredMutex (config : Config) (thread : Thread) : Bool :=
  match requiredMutex thread.core thread.code with
  | none => true
  | some mutex => decide (config.mutexOwners.get? mutex = some (some thread.core.tid))

/-- Release a mutex: "Threads blocked in `lock(m)` acquire `m` in FIFO order
of blocking" (spec §8.1, "Mutex"). The first blocked locker becomes the owner. -/
def releaseMutex (config : Config) (mutex : MutexId) : Config :=
  let isLockOfMutex : BlockedOn → Bool := fun on =>
    match on with
    | .lock wanted => decide (wanted = mutex)
    | _ => false
  match config.earliestBlocked isLockOfMutex with
  | some next =>
    { config with mutexOwners := config.mutexOwners.set mutex (some next) }.wakeThread next .granted
  | none => { config with mutexOwners := config.mutexOwners.set mutex none }

/-- Wake one dequeued waiter of a condition variable. A waiter that has not
parked yet finds the wake when it parks; "No spurious wakeups" (spec §8.1). -/
def wakeCvWaiter (config : Config) (cv : CvId) (waiter : Tid) : Config :=
  match config.findThread? waiter with
  | some thread =>
    match thread.phase with
    | .blocked (.park parkedOn) _ =>
      if parkedOn = cv then config.wakeThread waiter .granted
      else { config with cvWakes := config.cvWakes ++ [(cv, waiter)] }
    | _ => { config with cvWakes := config.cvWakes ++ [(cv, waiter)] }
  | none => { config with cvWakes := config.cvWakes ++ [(cv, waiter)] }

/-- Deliver a cancel to a thread that is blocked at a cancellation point
(`sleep`, `recv`, `select`, `join`): "A blocked timed operation is decided by
whichever happens first: its `fire`, the arrival of its message, or a cancel"
(spec §8.1). `lock` and `wait` are not cancellation points. -/
def cancelBlockedThread (config : Config) (target : Tid) : Config :=
  match config.findThread? target with
  | some thread =>
    match thread.phase with
    | .blocked (.recv _) _ => config.wakeThread target .canceled
    | .blocked (.join _) _ => config.wakeThread target .canceled
    | .blocked (.timed _ _ _) _ => config.wakeThread target .canceled
    | _ => config
  | none => config

/-- The program ends: "every other thread stops, except that issued emits may
still deliver" (spec §8.1, "Exit"). -/
def closeProgram (config : Config) : Config :=
  { config with
    status := .closed
    threads := []
    lateEmits := config.threads.filterMap (fun thread =>
      match thread.phase with
      | .emitting effect => some effect
      | _ => none) }

/-- Perform the atomic action of a completed statement of thread `thread`,
and move the thread to its next statement. Returns the observable events. -/
def applyIntent (config : Config) (thread : Tid) (result : StatementResult) :
    List Event × Config :=
  let continued := config.updateThread thread (fun current =>
    { current with core := result.core, code := result.code, timer := none, phase := .ready })
  match result.intent with
  | .silent => ([], continued)
  -- "Each single read or write of a shared variable ... is one atomic action."
  | .store target value =>
    ([], { continued with shared := continued.shared.set target value })
  | .fork child childCode =>
    ([], { continued with
      threads := continued.threads ++
        [{ core := child, code := childCode, timer := none, phase := .ready }] })
  | .newMutex mutex =>
    ([], { continued with mutexOwners := continued.mutexOwners.set mutex none })
  | .unlock mutex => ([], continued.releaseMutex mutex)
  -- "`wait(cv, m)` ... atomically releases `m` and appends the thread to `cv`'s FIFO queue."
  | .cvWait cv =>
    let queued := { continued with
      cvQueues := continued.cvQueues.set cv ((continued.cvQueues.get? cv).getD [] ++ [thread]) }
    ([], queued.releaseMutex cv.mutex)
  -- "`signal` wakes the head of the queue."
  | .signal cv =>
    match (continued.cvQueues.get? cv).getD [] with
    | [] => ([], continued)
    | waiter :: laterWaiters =>
      ([], { continued with cvQueues := continued.cvQueues.set cv laterWaiters }.wakeCvWaiter
        cv waiter)
  -- "`broadcast` wakes all."
  | .broadcast cv =>
    let waiters := (continued.cvQueues.get? cv).getD []
    ([], waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter)
      { continued with cvQueues := continued.cvQueues.set cv [] })
  -- "`cancel(t)` sets `t`'s flag."
  | .cancel target =>
    let flagged := { continued with
      cancelFlags :=
        if continued.cancelFlags.contains target then continued.cancelFlags
        else continued.cancelFlags ++ [target] }
    ([], flagged.cancelBlockedThread target)
  -- "*Enter* gives it the next timer number `n` from one program-wide counter."
  | .enterTimed seconds =>
    ([], { config.updateThread thread (fun current =>
        { current with
          core := result.core, code := result.code
          timer := some (config.timerCounter + 1, seconds), phase := .ready }) with
      timerCounter := config.timerCounter + 1 })
  -- The thread ends. The first thread blocked in `join` on it continues;
  -- otherwise it stays joinable. "Each thread is joined at most once."
  | .endThread =>
    let removed := { config with
      threads := config.threads.filter (fun candidate => !decide (candidate.core.tid = thread)) }
    let isJoinOfThread : BlockedOn → Bool := fun on =>
      match on with
      | .join wanted => decide (wanted = thread)
      | _ => false
    match removed.earliestBlocked isJoinOfThread with
    | some joiner => ([], removed.wakeThread joiner .granted)
    | none => ([], { removed with joinable := removed.joinable ++ [thread] })
  -- "`exit(v)`, or the return of `main`, ends the program at once."
  | .exit value => ([Event.exit value], config.closeProgram)
  | .fail message => ([Event.fail message], config.closeProgram)
  | .halt => ([], config.setPhase thread .halted)

/-- Register the wait of a blocking statement or timed operation: the atomic
synchronization action. The order of the checks is spec §8.1's: the operation
proceeds if it can; otherwise a cancellation point observes the cancel flag
("only if that point would block"); otherwise the thread blocks. -/
def registerWait (config : Config) (thread : Thread) : Config :=
  let tid := thread.core.tid
  let isCanceled := config.cancelFlags.contains tid
  match thread.timer with
  -- *Arm*: "sets its deadline to `now + d` and blocks the thread". "A timer is
  -- never complete at the moment it is armed, even for `d = 0`."
  | some (number, seconds) =>
    let deadline := config.clock + seconds * 1000000
    match timedChan thread.code with
    | some none =>
      if isCanceled then config.wakeThread tid .canceled
      else config.blockThread tid (.timed number deadline none)
    | some (some chan) =>
      match config.pendingInput chan with
      | message :: laterMessages =>
        { config with inputQueues := config.inputQueues.set chan laterMessages }.wakeThread
          tid (.received message)
      | [] =>
        if isCanceled then config.wakeThread tid .canceled
        else config.blockThread tid (.timed number deadline (some chan))
    | none => config.wakeThread tid .invalid
  | none =>
    match waitRequest thread.core thread.code with
    -- "`lock` is not a cancellation point." A mutex that does not exist is never free.
    | some (.lock mutex) =>
      match config.mutexOwners.get? mutex with
      | some none =>
        { config with mutexOwners := config.mutexOwners.set mutex (some tid) }.wakeThread
          tid .granted
      | _ => config.blockThread tid (.lock mutex)
    -- "Threads blocked in `recv(c)` on it are served in FIFO order of blocking."
    | some (.recv chan) =>
      match config.pendingInput chan with
      | message :: laterMessages =>
        { config with inputQueues := config.inputQueues.set chan laterMessages }.wakeThread
          tid (.received message)
      | [] =>
        if isCanceled then config.wakeThread tid .canceled
        else config.blockThread tid (.recv chan)
    | some (.join target) =>
      if config.joinable.contains target then
        { config with joinable := config.joinable.erase target }.wakeThread tid .granted
      else if isCanceled then config.wakeThread tid .canceled
      else config.blockThread tid (.join target)
    -- "`wait` is not a cancellation point."
    | some (.park cv) =>
      if config.cvWakes.contains (cv, tid) then
        { config with cvWakes := config.cvWakes.erase (cv, tid) }.wakeThread tid .granted
      else config.blockThread tid (.park cv)
    | none => config.wakeThread tid .invalid

/-- An external message arrives while the program runs: the first thread
blocked on the channel receives it; otherwise it is queued (spec §8.1,
"External input"). -/
def deliverInput (config : Config) (chan : ExtChan) (value : Val) : Config :=
  let isReceiveOnChan : BlockedOn → Bool := fun on =>
    match on with
    | .recv wanted => decide (wanted = chan)
    | .timed _ _ (some wanted) => decide (wanted = chan)
    | _ => false
  match config.earliestBlocked isReceiveOnChan with
  | some receiver => config.wakeThread receiver (.received value)
  | none => { config with inputQueues := config.inputQueues.set chan (config.pendingInput chan ++ [value]) }

/-- The thread whose armed timer is the least due one "in the order
`(deadline, n)`" (spec §8.1). A timer is due when its deadline is at or
before the clock. -/
def leastDueTimerThread (config : Config) : Option Tid :=
  let due := config.threads.filterMap (fun thread =>
    match thread.phase with
    | .blocked (.timed number deadline _) _ =>
      if deadline ≤ config.clock then some (deadline, number, thread.core.tid) else none
    | _ => none)
  (sortStable (fun first second =>
      decide (first.1 < second.1) ||
      (decide (first.1 = second.1) && decide (first.2.1 ≤ second.2.1))) due).head?.map
    (fun entry => entry.2.2)

/-- `fire`, `count` times in a row: each "fires only the least due timer". -/
def fireTimers : Nat → Config → Option Config
  | 0, config => some config
  | count + 1, config =>
    match config.leastDueTimerThread with
    | none => none
    | some thread => fireTimers count (config.wakeThread thread .fired)

/-- The configuration right after `begin(v)`: `main` with the program input,
and the statically known mutexes, unlocked. -/
def begun (config : Config) (program : Program) (input : Val) : Config :=
  { config with
    status := .running
    threads :=
      [{ core := { tid := [], freshCounter := 0, locals := [(program.mainParam, input)], stack := [] }
         code := pushCode program.mainBody []
         timer := none
         phase := .bootPending }]
    mutexOwners := (List.range program.staticMutexCount).map (fun index => (MutexId.static index, none)) }

end Config

/-- The next action of one thread. `none` when the thread is blocked or
halted, when the clock has not advanced since the last stamped action, or when
the thread would break a mutex requirement (`BreaksMutexRequirement`). -/
def stepThread (program : Program) (config : Config) (thread : Thread) :
    Option (List Event × Config) :=
  let tid := thread.core.tid
  let stamped : List Event × Config → Option (List Event × Config) := fun next =>
    if config.canStamp then some (next.1, next.2.stamp) else none
  match thread.phase with
  | .bootPending => stamped ([], config.setPhase tid .bootRunning)
  | .bootRunning => stamped ([], config.setPhase tid .ready)
  | .ready =>
    if thread.timer.isSome || isBlockingHead thread.code then
      stamped ([], config.setPhase tid .waitStarted)
    else if config.holdsRequiredMutex thread then
      match beginKind thread.core thread.code with
      | .plain => stamped ([], config.setPhase tid (.running .null))
      | .load source => stamped ([], config.setPhase tid (.running (config.readShared source)))
      | .emit effect => stamped ([], config.setPhase tid (.emitting effect))
    else none
  | .running loaded =>
    stamped (config.applyIntent tid (completeStatement program thread.core thread.code loaded))
  -- "the `out(a)` event happens later, after any interleaving of other threads"
  | .emitting effect => some ([Event.out effect], config.setPhase tid .emitted)
  -- "the thread continues only after `out(a)`"
  | .emitted =>
    stamped (config.applyIntent tid (completeStatement program thread.core thread.code .null))
  | .waitStarted => stamped ([], config.registerWait thread)
  | .blocked _ _ => none
  | .woken outcome => stamped ([], config.setPhase tid (.resuming outcome))
  | .resuming outcome =>
    stamped (config.applyIntent tid
      (if thread.timer.isSome then resumeTimed thread.core thread.code outcome
       else resumeBlocked thread.core thread.code outcome))
  | .halted => none

/-- One transition of an L program. -/
def step (program : Program) (config : Config) : Label → Option (List Event × Config)
  -- The clock "advances by idle ticks".
  | .tick duration =>
    if 0 < duration then some ([], { config with clock := config.clock + duration }) else none
  | .request (.start input) =>
    match config.status with
    | .notStarted =>
      if config.canStamp then
        some ([Event.begin input.toVal], (config.begun program input.toVal).stamp)
      else none
    | _ => none
  | .request (.input chan value) =>
    match config.status with
    | .notStarted => none
    | .running =>
      if config.canStamp then
        some ([Event.input chan value.toVal], (config.deliverInput chan value.toVal).stamp)
      else none
    -- "Input that arrives after exit is `refused`."
    | .closed => some ([Event.refused chan value.toVal], config)
  | .thread thread =>
    match config.findThread? thread with
    | none => none
    | some current => stepThread program config current
  -- "enabled at any time no earlier than the deadline, and possibly much later"
  | .fire count =>
    if 0 < count && config.canStamp then
      (config.fireTimers count).map (fun fired => ([], fired.stamp))
    else none
  | .lateDeliver index =>
    match config.status, config.lateEmits[index]? with
    | .closed, some effect =>
      some ([Event.out effect], { config with lateEmits := removeAt config.lateEmits index })
    | _, _ => none
  | .lateDrop index =>
    match config.status, config.lateEmits[index]? with
    | .closed, some _ => some ([], { config with lateEmits := removeAt config.lateEmits index })
    | _, _ => none

/-- An L program as a transition system. Every label except an idle tick is
a progress step (spec §5.5: L counts "its own actions (including timer `fire`,
Section 8.1) as progress and idle time as not"). -/
def system (program : Program) : TransitionSystem Config Label Event Request :=
  { init := Config.initial
    step := step program
    requestOf := fun label =>
      match label with
      | .request request => some request
      | _ => none
    isProgress := fun label =>
      match label with
      | .tick _ => false
      | _ => true
    isClosed := fun config => decide (config.status = .closed) }

/-- An admissible input word in L form (spec §5.3): one start, then inputs on
external channels. -/
def IsAdmissibleWord (word : Sequence Request) : Prop :=
  (∃ input, word 0 = some (.start input)) ∧
  (∀ position request, word (position + 1) = some request →
    ∃ chan value, request = .input chan value) ∧
  (∀ position, word position = none → word (position + 1) = none)

/-! ## Well-formed programs (spec §0, §8.1 "Well-formed programs")

The theorem covers well-formed programs only [owner, 2026-11-09]. A program is
well formed when no run of it ever has a thread about to

- call `wait(cv, m)`, `signal(cv)`, or `broadcast(cv)` without holding `cv`'s
  mutex, or
- call `unlock(m)` on a mutex it does not hold.

This is a property of the source program under L's semantics, like data-race
freedom: it quantifies over every input and every schedule. It is checked on
the L side alone; the translation plays no part in it. -/

/-- The thread is about to break a mutex requirement of spec §8.1: its next
statement is `unlock(m)`, `wait(cv, m)`, `signal(cv)`, or `broadcast(cv)`
(`requiredMutex`), it has not begun the statement, and it does not own the
required mutex. L gives such a thread no action. -/
def Thread.BreaksMutexRequirement (config : Config) (thread : Thread) : Prop :=
  thread.phase = .ready ∧ thread.timer = none ∧
  ∃ mutex, requiredMutex thread.core thread.code = some mutex ∧
    config.mutexOwners.get? mutex ≠ some (some thread.core.tid)

/-- Some thread of the configuration is about to break a mutex requirement. -/
def BreaksMutexRequirement (config : Config) : Prop :=
  ∃ thread, thread ∈ config.threads ∧ thread.BreaksMutexRequirement config

/-- A well-formed program (spec §0, §8.1): no configuration that any finite
run reaches, under any input and any schedule, has a thread about to break a
mutex requirement. Both theorems assume it. -/
def Program.IsWellFormed (program : Program) : Prop :=
  ∀ config, (system program).Reachable config → ¬ BreaksMutexRequirement config

/-! ## Fairness (spec §5.5, last paragraph)

"L must define the same three outcomes ... the same way: relative to the input
word, counting its own actions (including timer `fire`) as progress and idle
time as not, with `✓` only after every issued emit has delivered or been
dropped." These are the L forms of F1 to F6. -/

namespace Fairness

variable (run : InfiniteRun Config Label)

/-- F1, time diverges. -/
def TimeDiverges : Prop :=
  ∀ bound, ∃ index, bound < (run.states index).clock

/-- The thread's timer with this number is armed and due before transition `index`. -/
def IsTimerArmedAndDue (thread : Tid) (number : Nat) (index : Nat) : Prop :=
  ∃ current deadline chan since,
    (run.states index).findThread? thread = some current ∧
    current.phase = .blocked (.timed number deadline chan) since ∧
    deadline ≤ (run.states index).clock

/-- F2, timers fire: "a timer that stays due is eventually fired" (spec §8.1). -/
def TimersFire : Prop :=
  ∀ thread number, EventuallyFails (IsTimerArmedAndDue run thread number)

/-- The thread's next action has not begun: the L form of a pending invocation. -/
def IsThreadPending (thread : Tid) (index : Nat) : Prop :=
  ∃ current,
    (run.states index).findThread? thread = some current ∧
    (current.phase = .bootPending ∨ current.phase = .ready ∨
      ∃ outcome, current.phase = .woken outcome)

/-- F3, threads begin: a thread whose next action can begin eventually begins it. -/
def ThreadsBegin : Prop :=
  ∀ thread, EventuallyFails (IsThreadPending run thread)

/-- The thread issued an emit whose `out(a)` has not happened. -/
def IsThreadEmitting (thread : Tid) (index : Nat) : Prop :=
  ∃ current effect,
    (run.states index).findThread? thread = some current ∧ current.phase = .emitting effect

/-- F4, emits settle: an issued emit eventually delivers, or the program ends;
after the end, every late emit is eventually delivered or dropped. -/
def EmitsSettle : Prop :=
  (∀ thread, EventuallyFails (IsThreadEmitting run thread)) ∧
  (∀ index, (run.states index).lateEmits ≠ [] →
    ∃ later, index ≤ later ∧
      (run.states later).lateEmits.length < (run.states index).lateEmits.length)

/-- The thread began an action and has not completed it. -/
def IsThreadCompleting (thread : Tid) (index : Nat) : Prop :=
  ∃ current,
    (run.states index).findThread? thread = some current ∧
    (current.phase = .bootRunning ∨ current.phase = .emitted ∨
      current.phase = .waitStarted ∨ (∃ loaded, current.phase = .running loaded) ∨
      ∃ outcome, current.phase = .resuming outcome)

/-- F5, actions complete: a thread that began an action eventually completes it. -/
def ActionsComplete : Prop :=
  ∀ thread, EventuallyFails (IsThreadCompleting run thread)

/-- F6, messages move: L has no cross-program messages, so F6 holds trivially. -/
def MessagesMove : Prop := True

end Fairness

/-- A fair L run: F1 to F6. F0 is part of `TransitionSystem.IsFairMaximalTrace`. -/
def IsFair (run : InfiniteRun Config Label) : Prop :=
  Fairness.TimeDiverges run ∧ Fairness.TimersFire run ∧ Fairness.ThreadsBegin run ∧
  Fairness.EmitsSettle run ∧ Fairness.ActionsComplete run ∧ Fairness.MessagesMove

end DexExpressiveness.L
