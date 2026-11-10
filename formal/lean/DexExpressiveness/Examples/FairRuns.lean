import DexExpressiveness.Liveness.RestingRuns
import DexExpressiveness.Theorems
import DexExpressiveness.Examples.WellFormed

/-!
# Fair maximal traces of example programs

The trace sets of liveness are defined by the existence of a fair run (spec
§5.5). If no program had a fair run, `liveness_of_isWellFormed` would compare
two empty sets. This file exhibits fair maximal traces of concrete programs
for concrete admissible words, one for each marker, on the L side and, by
`liveness_of_isWellFormed`, on the Dex side:

| Marker | Program | Word | Trace |
| --- | --- | --- | --- |
| `✓` | `Mutex.program` | the start | `begin`, both emits, `exit` |
| `δ` | `waitingProgram`: `emit(1); recv(5)` | the start | `begin`, `out(1)`: `main` waits for a message forever |
| `δ` | `selfDeadlockProgram`: `lock(m0); emit(1); lock(m0)` | the start | `begin`, `out(1)`: `main` waits for its own mutex forever |
| `∞` | `Mutex.program` | the start, then one input forever | the `✓` trace, then `refused` forever |

Each L run is a finite schedule, checked by evaluation in the Lean kernel,
followed by a tail that `Liveness/RestingRuns.lean` proves fair: clock ticks
for `✓` and `δ`, and clock ticks and refused inputs for `∞`.

The `∞` witness has an infinite word. No witness here is a program that runs
forever on a finite word: its configurations never come to rest, so its run
is not a finite schedule followed by a regular tail.
-/

namespace DexExpressiveness.Examples.FairRuns

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation
open DexExpressiveness.Examples

/-! ## Checking a schedule that ends at rest -/

/-- The schedule runs from the initial configuration with these events and
these requests, and ends in a configuration at rest that is closed or not as
`isClosed` says. -/
def reachesRest (program : Program) (labels : List L.Label) (events : List Event)
    (requests : List L.Request) (isClosed : Bool) : Bool :=
  match (L.system program).exec (L.system program).init labels with
  | some (actual, final) =>
    decide (actual = events) &&
    decide (labels.filterMap (L.system program).requestOf = requests) &&
    final.isAtRest && (decide (final.status = .closed) == isClosed)
  | none => false

/-- The Boolean check gives the configuration at rest. -/
theorem exists_rest_of_reachesRest (program : Program) (labels : List L.Label)
    (events : List Event) (requests : List L.Request) (isClosed : Bool)
    (passes : reachesRest program labels events requests isClosed = true) :
    ∃ final,
      (L.system program).exec (L.system program).init labels = some (events, final) ∧
      labels.filterMap (L.system program).requestOf = requests ∧
      final.isAtRest = true ∧ (final.status = .closed ↔ isClosed = true) := by
  unfold reachesRest at passes
  split at passes
  · rename_i actual final isExec
    simp only [Bool.and_eq_true, decide_eq_true_eq, beq_iff_eq] at passes
    obtain ⟨⟨⟨isEvents, isRequests⟩, isAtRest⟩, isStatus⟩ := passes
    refine ⟨final, by rw [isExec, isEvents], isRequests, isAtRest, ?_⟩
    rw [← isStatus]
    simp
  · cases passes

/-! ## Words -/

/-- The word of one start. -/
theorem isAdmissibleWord_start (input : Literal) :
    L.IsAdmissibleWord (Sequence.ofList [L.Request.start input]) := by
  refine ⟨⟨input, rfl⟩, fun position request isRequest => ?_, fun position _ => ?_⟩
  · simp [Sequence.ofList] at isRequest
  · simp [Sequence.ofList]

/-- The infinite word of one start and then the same input forever. -/
def startThenInputs (input : Literal) (chan : ExtChan) (value : Literal) : Sequence L.Request :=
  fun position =>
    if position < [L.Request.start input].length then [L.Request.start input][position]?
    else some (.input chan value)

/-- A start followed by inputs forever is an admissible word (spec §5.3). -/
theorem isAdmissibleWord_startThenInputs (input : Literal) (chan : ExtChan) (value : Literal) :
    L.IsAdmissibleWord (startThenInputs input chan value) := by
  refine ⟨⟨input, rfl⟩, fun position request isRequest => ?_, fun position isNone => ?_⟩
  · simp [startThenInputs] at isRequest
    exact ⟨chan, value, isRequest.symm⟩
  · unfold startThenInputs at isNone
    by_cases isFirst : position < [L.Request.start input].length
    · rw [if_pos isFirst] at isNone
      have isZero : position = 0 := by simpa using isFirst
      subst isZero
      simp at isNone
    · rw [if_neg isFirst] at isNone
      cases isNone

/-- A finite trace followed by the same `refused` event forever. -/
def thenRefused (events : List Event) (chan : ExtChan) (value : Val) : Sequence Event :=
  fun position =>
    if position < events.length then events[position]? else some (Event.refused chan value)

/-! ## `✓`: the mutex program runs to its exit -/

/-- The mutex program has a fair maximal trace with marker `✓`: it starts,
both threads emit, and `main` exits. -/
theorem mutexProgram_terminates :
    IsLFairMaximalTrace Mutex.program (Sequence.ofList [L.Request.start (.int 7)])
      (Sequence.ofList Mutex.mainFirstTrace) .terminated := by
  obtain ⟨final, isExec, isRequests, isAtRest, isClosed⟩ :=
    exists_rest_of_reachesRest Mutex.program Mutex.lMainFirst Mutex.mainFirstTrace
      [L.Request.start (.int 7)] true (by decide +kernel)
  have isTrace := isLFairMaximalTrace_of_rest_closed Mutex.program Mutex.lMainFirst
    Mutex.mainFirstTrace final isExec isAtRest (isClosed.mpr rfl)
  rwa [isRequests] at isTrace

/-- The translated Flow of the mutex program has the same fair maximal trace
with marker `✓`, by the liveness theorem. -/
theorem mutexProgram_terminates_dex :
    IsProjectedDexFairMaximalTrace Mutex.program (Sequence.ofList [L.Request.start (.int 7)])
      (Sequence.ofList Mutex.mainFirstTrace) .terminated :=
  (liveness_of_isWellFormed Mutex.program WellFormed.mutexProgram_isWellFormed _
    (isAdmissibleWord_start (.int 7)) _ _).mpr mutexProgram_terminates

/-! ## `δ`: a program that waits for input forever -/

/-- `emit(1); x := recv(5)`: after its emit, `main` waits for a message. -/
def waitingProgram : Program :=
  { procs := []
    mainParam := 0
    mainBody := LockstepChecks.block [.emit (LockstepChecks.int 1), .recv 1 5]
    staticMutexCount := 0 }

/-- The schedule of the waiting program until no thread can move. -/
def lWaiting : List L.Label :=
  lExtend waitingProgram [] 40 [.request (.start (.int 0))]

/-- The waiting program is well formed. -/
theorem waitingProgram_isWellFormed : waitingProgram.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- The waiting program has a fair maximal trace with marker `δ`: no message
arrives, time passes, and `main` stays blocked in `recv`. -/
theorem waitingProgram_deadlocks :
    IsLFairMaximalTrace waitingProgram (Sequence.ofList [L.Request.start (.int 0)])
      (Sequence.ofList [Event.begin (.int 0), Event.out (.int 1)]) .deadlocked := by
  obtain ⟨final, isExec, isRequests, isAtRest, isClosed⟩ :=
    exists_rest_of_reachesRest waitingProgram lWaiting [Event.begin (.int 0), Event.out (.int 1)]
      [L.Request.start (.int 0)] false (by decide +kernel)
  have isTrace := isLFairMaximalTrace_of_rest_open waitingProgram lWaiting _ final isExec
    isAtRest (fun isClosedNow => by simpa using isClosed.mp isClosedNow)
  rwa [isRequests] at isTrace

/-- The translated Flow of the waiting program has the same fair maximal
trace with marker `δ`. -/
theorem waitingProgram_deadlocks_dex :
    IsProjectedDexFairMaximalTrace waitingProgram (Sequence.ofList [L.Request.start (.int 0)])
      (Sequence.ofList [Event.begin (.int 0), Event.out (.int 1)]) .deadlocked :=
  (liveness_of_isWellFormed waitingProgram waitingProgram_isWellFormed _
    (isAdmissibleWord_start (.int 0)) _ _).mpr waitingProgram_deadlocks

/-! ## `δ`: a program that deadlocks on its own mutex -/

/-- `lock(m0); emit(1); lock(m0)`: `main` locks a mutex it already holds. -/
def selfDeadlockProgram : Program :=
  { procs := []
    mainParam := 0
    mainBody := LockstepChecks.block
      [.lock LockstepChecks.m0, .emit (LockstepChecks.int 1), .lock LockstepChecks.m0]
    staticMutexCount := 1 }

/-- The schedule of the self-deadlock program until no thread can move. -/
def lSelfDeadlock : List L.Label :=
  lExtend selfDeadlockProgram [] 40 [.request (.start (.int 0))]

/-- The self-deadlock program is well formed. -/
theorem selfDeadlockProgram_isWellFormed : selfDeadlockProgram.IsWellFormed :=
  Program.isWellFormed_of_hasStaticLockDiscipline _ (by decide)

/-- The self-deadlock program has a fair maximal trace with marker `δ`:
`main` stays blocked in its second `lock`. -/
theorem selfDeadlockProgram_deadlocks :
    IsLFairMaximalTrace selfDeadlockProgram (Sequence.ofList [L.Request.start (.int 0)])
      (Sequence.ofList [Event.begin (.int 0), Event.out (.int 1)]) .deadlocked := by
  obtain ⟨final, isExec, isRequests, isAtRest, isClosed⟩ :=
    exists_rest_of_reachesRest selfDeadlockProgram lSelfDeadlock
      [Event.begin (.int 0), Event.out (.int 1)] [L.Request.start (.int 0)] false
      (by decide +kernel)
  have isTrace := isLFairMaximalTrace_of_rest_open selfDeadlockProgram lSelfDeadlock _ final
    isExec isAtRest (fun isClosedNow => by simpa using isClosed.mp isClosedNow)
  rwa [isRequests] at isTrace

/-- The translated Flow of the self-deadlock program has the same fair
maximal trace with marker `δ`. -/
theorem selfDeadlockProgram_deadlocks_dex :
    IsProjectedDexFairMaximalTrace selfDeadlockProgram
      (Sequence.ofList [L.Request.start (.int 0)])
      (Sequence.ofList [Event.begin (.int 0), Event.out (.int 1)]) .deadlocked :=
  (liveness_of_isWellFormed selfDeadlockProgram selfDeadlockProgram_isWellFormed _
    (isAdmissibleWord_start (.int 0)) _ _).mpr selfDeadlockProgram_deadlocks

/-! ## `∞`: input forever after the exit -/

/-- The mutex program has a fair maximal trace with marker `∞` for an
infinite word: after its exit, every input is refused. -/
theorem mutexProgram_diverges :
    IsLFairMaximalTrace Mutex.program (startThenInputs (.int 7) 7 (.int 1))
      (thenRefused Mutex.mainFirstTrace 7 (.int 1)) .divergent := by
  obtain ⟨final, isExec, isRequests, isAtRest, isClosed⟩ :=
    exists_rest_of_reachesRest Mutex.program Mutex.lMainFirst Mutex.mainFirstTrace
      [L.Request.start (.int 7)] true (by decide +kernel)
  have isTrace := isLFairMaximalTrace_of_rest_refused Mutex.program Mutex.lMainFirst
    Mutex.mainFirstTrace final isExec isAtRest (isClosed.mpr rfl) 7 (.int 1)
  rw [isRequests] at isTrace
  exact isTrace

/-- The translated Flow of the mutex program has the same fair maximal trace
with marker `∞`. -/
theorem mutexProgram_diverges_dex :
    IsProjectedDexFairMaximalTrace Mutex.program (startThenInputs (.int 7) 7 (.int 1))
      (thenRefused Mutex.mainFirstTrace 7 (.int 1)) .divergent :=
  (liveness_of_isWellFormed Mutex.program WellFormed.mutexProgram_isWellFormed _
    (isAdmissibleWord_startThenInputs (.int 7) 7 (.int 1)) _ _).mpr mutexProgram_diverges

end DexExpressiveness.Examples.FairRuns
