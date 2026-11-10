import DexExpressiveness.L.Semantics

/-!
# The invariant of reachable L configurations

`L.Invariant` collects the facts about one L configuration that the step
simulations (`Simulation.lean`) need and that do not mention Dex. It plays
the part on the L side that `ConfigInvariant` (`Invariants.lean`) plays on the
Dex side. `Related` then holds only the clauses that link the two sides.

The invariant, by group:

1. Before the start and after the end: `notStartedIsInitial`,
   `closedHasNoThread`.
2. Fresh names (spec §8.3 `fork`: "a fresh thread ID derived from the
   parent's ID and a counter"): `threadIdsNodup`, `threadIdsFresh`,
   `joinableNodup`, `joinableFresh`, `mutexesFresh`.
3. Handles are unforgeable (spec §8.1 "Handles and literals"): `handlesExist`.
   Every handle of a created mutex that a thread can reach names a mutex that
   exists.
4. The start of `main`: `bootIsInitial`.
5. Blocking order (spec §8.1: "FIFO order of blocking"): `sinceBelowCounter`,
   `sinceInjective`.
6. Timer numbers (spec §8.1: "the next timer number `n` from one program-wide
   counter"): `timerNumbersCounted`, `timerNumbersInjective`.
7. Each thread's phase fits its code: `phasesFitCode`.
8. A statement that requires a mutex runs while its thread holds the mutex
   (spec §8.1 "Well-formed programs"): `runningHoldsRequiredMutex`.

This file only defines the invariant and proves it for the initial
configuration. Its definitions are fixed. `L/InvariantStep.lean` states that
every transition keeps the invariant.
-/

namespace DexExpressiveness.L

namespace Config

/-- The mutex exists, or it is a statically known mutex. A statically known
mutex beyond `staticMutexCount` never exists, which is harmless: nothing ever
creates it. -/
def IsExistingMutex (config : Config) : MutexId → Prop
  | .static _ => True
  | .dynamic creator index => config.mutexOwners.contains (.dynamic creator index) = true

/-- A value names no mutex that does not exist: a mutex handle and a condition
variable handle name an existing mutex. -/
def IsValidVal (config : Config) : Val → Prop
  | .mutex mutex => config.IsExistingMutex mutex
  | .cv cv => config.IsExistingMutex cv.mutex
  | _ => True

/-- A runtime instruction names no mutex that does not exist. A source
statement holds no handle (`Expr.lit` takes a `Literal`). -/
def IsValidInstr (config : Config) : Instr → Prop
  | .source _ => True
  | .park cv => config.IsExistingMutex cv.mutex
  | .relock mutex => config.IsExistingMutex mutex

/-- A stack frame names no mutex that does not exist. -/
def IsValidFrame (config : Config) : Frame → Prop
  | .call _ savedLocals returnCode =>
    (∀ entry, entry ∈ savedLocals → config.IsValidVal entry.2) ∧
    (∀ instr, instr ∈ returnCode → config.IsValidInstr instr)
  | .handler _ handlerCode afterCode =>
    (∀ instr, instr ∈ handlerCode → config.IsValidInstr instr) ∧
    (∀ instr, instr ∈ afterCode → config.IsValidInstr instr)

/-- A phase names no mutex that does not exist. -/
def IsValidPhase (config : Config) : Phase → Prop
  | .running loaded => config.IsValidVal loaded
  | .blocked (.lock mutex) _ => config.IsExistingMutex mutex
  | .blocked (.park cv) _ => config.IsExistingMutex cv.mutex
  | .woken (.received message) => config.IsValidVal message
  | .resuming (.received message) => config.IsValidVal message
  | _ => True

/-- A thread names no mutex that does not exist: in its locals, its stack, its
code, and its phase. -/
def IsValidThread (config : Config) (thread : Thread) : Prop :=
  (∀ entry, entry ∈ thread.core.locals → config.IsValidVal entry.2) ∧
  (∀ frame, frame ∈ thread.core.stack → config.IsValidFrame frame) ∧
  (∀ instr, instr ∈ thread.code → config.IsValidInstr instr) ∧
  config.IsValidPhase thread.phase

end Config

/-- The wait of a blocking statement that a blocked thread is in; `none` for
a timed operation. -/
def BlockedOn.toWaitRequest? : BlockedOn → Option WaitRequest
  | .lock mutex => some (.lock mutex)
  | .recv chan => some (.recv chan)
  | .join thread => some (.join thread)
  | .park cv => some (.park cv)
  | .timed _ _ _ => none

/-- The phase of a thread fits its code and its timer:

- an Execute-only action (`running`, `emitting`, `emitted`) is at a statement
  that does not block, outside a timed operation, and the emit phases are at
  an `emit` whose operand evaluates;
- a wait action (`waitStarted`, `woken`, `resuming`) is at a blocking
  statement or inside a timed operation;
- a blocked thread waits for what its statement asks: the wait request of its
  head statement, or, inside a timed operation, its own timer and the channel
  of its `select`. -/
def Thread.PhaseFitsCode (thread : Thread) : Prop :=
  match thread.phase with
  | .bootPending => True
  | .bootRunning => True
  | .ready => True
  | .halted => True
  | .running _ =>
    thread.timer = none ∧ isBlockingHead thread.code = false ∧
    ∀ effect, beginKind thread.core thread.code ≠ .emit effect
  | .emitting effect =>
    thread.timer = none ∧ isBlockingHead thread.code = false ∧
    beginKind thread.core thread.code = .emit effect
  | .emitted =>
    thread.timer = none ∧ isBlockingHead thread.code = false ∧
    ∃ effect, beginKind thread.core thread.code = .emit effect
  | .waitStarted => thread.timer.isSome = true ∨ isBlockingHead thread.code = true
  | .woken _ => thread.timer.isSome = true ∨ isBlockingHead thread.code = true
  | .resuming _ => thread.timer.isSome = true ∨ isBlockingHead thread.code = true
  | .blocked (.timed number _ chan) _ =>
    (∃ seconds, thread.timer = some (number, seconds)) ∧ timedChan thread.code = some chan
  | .blocked on _ =>
    thread.timer = none ∧ waitRequest thread.core thread.code = on.toWaitRequest?

/-- The invariant of reachable L configurations. -/
structure Invariant (program : Program) (config : Config) : Prop where
  /-- Before `begin(v)` the configuration is the initial one, up to the clock. -/
  notStartedIsInitial : config.status = .notStarted →
    config.threads = [] ∧ config.shared = [] ∧ config.mutexOwners = [] ∧
    config.cvQueues = [] ∧ config.cvWakes = [] ∧ config.inputQueues = [] ∧
    config.cancelFlags = [] ∧ config.joinable = [] ∧ config.timerCounter = 0
  /-- After `exit(v)` or `fail(s)` no thread is left. -/
  closedHasNoThread : config.status = .closed → config.threads = []
  /-- Thread IDs are unique. -/
  threadIdsNodup : (config.threads.map (fun thread => thread.core.tid)).Nodup
  /-- The next children of a thread have fresh IDs: no live thread's ID
  starts with `tid ++ [index]` for an index the thread has not used yet. -/
  threadIdsFresh : ∀ first, first ∈ config.threads → ∀ second, second ∈ config.threads →
    ∀ index, first.core.freshCounter ≤ index →
      ¬ (first.core.tid ++ [index]) <+: second.core.tid
  /-- An ended thread waits to be joined at most once. -/
  joinableNodup : config.joinable.Nodup
  /-- A live thread is not joinable, and the next children of a live thread
  are not joinable either. -/
  joinableFresh : ∀ thread, thread ∈ config.threads →
    thread.core.tid ∉ config.joinable ∧
    ∀ ended, ended ∈ config.joinable → ∀ index, thread.core.freshCounter ≤ index →
      ¬ (thread.core.tid ++ [index]) <+: ended
  /-- The next mutex a thread creates is fresh: every existing mutex of its
  own has a smaller index, and no existing mutex was created by one of its
  next children. -/
  mutexesFresh : ∀ thread, thread ∈ config.threads → ∀ creator index,
    config.mutexOwners.contains (.dynamic creator index) = true →
    (creator = thread.core.tid → index < thread.core.freshCounter) ∧
    ∀ later, thread.core.freshCounter ≤ later → ¬ (thread.core.tid ++ [later]) <+: creator
  /-- Every handle a thread can reach names an existing mutex: in the threads,
  in the shared variables, and in the queued external messages. -/
  handlesExist :
    (∀ thread, thread ∈ config.threads → config.IsValidThread thread) ∧
    (∀ entry, entry ∈ config.shared → config.IsValidVal entry.2) ∧
    (∀ entry, entry ∈ config.inputQueues → ∀ message, message ∈ entry.2 →
      config.IsValidVal message)
  /-- While `main` boots it is the only thread, in its initial state. -/
  bootIsInitial : ∀ thread, thread ∈ config.threads →
    thread.phase = .bootPending ∨ thread.phase = .bootRunning →
    config.threads = [thread] ∧ thread.timer = none ∧
    thread.code = pushCode program.mainBody [] ∧
    ∃ input, thread.core =
      { tid := [], freshCounter := 0, locals := [(program.mainParam, input)], stack := [] }
  /-- The `since` stamp of a blocked thread is below the next stamp. -/
  sinceBelowCounter : ∀ thread, thread ∈ config.threads → ∀ on since,
    thread.phase = .blocked on since → since < config.blockCounter
  /-- Two blocked threads never share a `since` stamp. -/
  sinceInjective : ∀ first, first ∈ config.threads → ∀ second, second ∈ config.threads →
    ∀ firstOn secondOn since, first.phase = .blocked firstOn since →
      second.phase = .blocked secondOn since → first = second
  /-- A timer number in use is positive and at or below the timer counter. -/
  timerNumbersCounted : ∀ thread, thread ∈ config.threads → ∀ number seconds,
    thread.timer = some (number, seconds) → 0 < number ∧ number ≤ config.timerCounter
  /-- Two threads never share a timer number. -/
  timerNumbersInjective : ∀ first, first ∈ config.threads → ∀ second, second ∈ config.threads →
    ∀ number firstSeconds secondSeconds, first.timer = some (number, firstSeconds) →
      second.timer = some (number, secondSeconds) → first = second
  /-- Each thread's phase fits its code and its timer. -/
  phasesFitCode : ∀ thread, thread ∈ config.threads → thread.PhaseFitsCode
  /-- A thread that began a statement that requires a mutex holds that mutex
  until the statement completes (spec §8.1: "`wait(cv, m)` requires `m`
  held"). No other thread can release it in between. -/
  runningHoldsRequiredMutex : ∀ thread, thread ∈ config.threads → ∀ loaded,
    thread.phase = .running loaded → ∀ mutex,
      requiredMutex thread.core thread.code = some mutex →
      config.mutexOwners.get? mutex = some (some thread.core.tid)

/-- The initial configuration satisfies the invariant. -/
theorem Invariant.initial (program : Program) : Invariant program (system program).init :=
  { notStartedIsInitial := fun _ => ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
    closedHasNoThread := fun _ => rfl
    threadIdsNodup := List.nodup_nil
    threadIdsFresh := fun first isMember => by cases isMember
    joinableNodup := List.nodup_nil
    joinableFresh := fun thread isMember => by cases isMember
    mutexesFresh := fun thread isMember => by cases isMember
    handlesExist := ⟨(fun thread isMember => by cases isMember),
      (fun entry isMember => by cases isMember), (fun entry isMember => by cases isMember)⟩
    bootIsInitial := fun thread isMember => by cases isMember
    sinceBelowCounter := fun thread isMember => by cases isMember
    sinceInjective := fun first isMember => by cases isMember
    timerNumbersCounted := fun thread isMember => by cases isMember
    timerNumbersInjective := fun first isMember => by cases isMember
    phasesFitCode := fun thread isMember => by cases isMember
    runningHoldsRequiredMutex := fun thread isMember => by cases isMember }

end DexExpressiveness.L
