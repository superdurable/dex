import DexExpressiveness.Dex.Syntax
import DexExpressiveness.L.ThreadLocal

/-!
# The translation `⟦·⟧ : L → Dex Flow` (spec §8.2, §8.3)

"Compile each L thread to continuation-passing Steps." A program point is a
Step type; the thread-local state is the Step input (spec §8.2).

"Each L statement is its own Step" (spec §8.2, "One Step per statement"): its
Execute runs the thread-local semantics (`L.completeStatement`) and encodes
the atomic action it asks for (`encodeIntent`). `encodeIntent` is the mapping table of spec §8.3, one case
per row.

The translation uses only GoTo, GoToMany, DeadEnd, ForceComplete, and
ForceFail, and only ForOne Channel conditions: "The translation never uses
CancelSteps, CancelSiblingSteps, GracefulComplete, RPCs, SubFlows, or
Attribute locks" (spec §8.2). The restricted Dex model has no other construct,
so this holds by construction.
-/

namespace DexExpressiveness.Translation

open DexExpressiveness.L

/-- The Step types of a translated program (spec §8.2). -/
inductive StepType
  /-- The entry Step `T₀`: "The start Step publishes one token to each
  statically known mutex Channel before it starts `main`." -/
  | start
  /-- "A program point is a Step type": the Step that runs the head statement
  of `code` and moves to the program point of what follows. -/
  | point (code : Code)
  /-- "One Step type for every timed operation": the generic `Timed` Step,
  "whose input carries the duration, the Channels to wait on, and the
  continuation". -/
  | timed
  deriving DecidableEq, Repr

/-- The Channels of a translated program (spec §8.3). -/
inductive Chan
  /-- An external Channel: the translation of an L external input channel (spec §5.3). -/
  | external (chan : ExtChan)
  /-- The token Channel of a mutex: one token while the mutex is unlocked. -/
  | mutexToken (mutex : MutexId)
  /-- `join/t`: "`t`'s last Step publishes to the Channel `join/t`". -/
  | joinDone (thread : Tid)
  /-- `cancel/t`: `cancel(t)` publishes to it. -/
  | cancelRequest (thread : Tid)
  /-- `cv/w`: "the per-waiter Channel instance `cv/w`". The waiter ID `w` is
  the waiting thread's ID. -/
  | cvWake (cv : CvId) (waiter : Tid)
  deriving DecidableEq, Repr

/-- The Attributes of a translated program (spec §8.2, §8.3). -/
inductive Key
  /-- "Shared variables are Attributes": a plain Attribute, whose key has no `/`. -/
  | shared (name : Global)
  /-- `waiters/cv`: the FIFO list of the waiter IDs of a condition variable.
  It is the instance `cv` of the AttributeMap `waiters`, so a snapshot holds
  it only when the Step selects it (spec §4.2 `snapshot`). -/
  | cvWaiters (cv : CvId)
  deriving DecidableEq, Repr

/-- The AttributeMap load selections of a translated program (spec §2.1
`loadMaps`). The translation selects exact instances only; it never uses a
`map/` prefix selection. -/
inductive MapSelection
  /-- The exact instance `waiters/cv`. -/
  | cvWaiters (cv : CvId)
  deriving DecidableEq, Repr

/-- The Dex values of a translated program. -/
inductive DexVal
  /-- `enc(v)`: an L value as a Dex value. Shared variables, external
  messages, and the program input use it. -/
  | value (value : Val)
  /-- `⟦exit: x⟧ = {"exit": enc(x)}`: the tagged exit value, "which is never
  null, also when `x` is L's null" (spec §5.3). -/
  | exitTag (value : Val)
  /-- The message of a synchronization Channel: a mutex token, a join
  notice, a cancel request, or a condition-variable wake. -/
  | token
  /-- The value of `waiters/cv`. -/
  | waiterQueue (waiters : List Tid)
  /-- The input of a program-point Step: "Thread-local variables, the thread
  ID, and the call stack" (spec §8.2). -/
  | thread (core : ThreadCore)
  /-- The input of a `Timed` Step: the thread state, the continuation (the
  code whose head is the `sleep` or `select`), and the duration. -/
  | timedThread (core : ThreadCore) (code : Code) (seconds : Nat)

/-- The Dex signature of translated programs. An external effect is the
emitted L value. `waiters/cv` keys are AttributeMap instances; shared
variables are plain Attributes. For equal fire times, timers are ordered by
Step number: every timer is the index-0 timer of a `Timed` Step, for which
spec §8.2 derives that "Dex's order is timer-number order" (spec Appendix A,
assumption 26). Step numbers are unbounded naturals here; the derivation holds
below 2³² Step executions of one type per run (spec Appendix A, assumption 27). -/
abbrev signature : Dex.Signature :=
  { StepType := StepType
    Chan := Chan
    Key := Key
    Val := DexVal
    Fx := Val
    MapSelection := MapSelection
    isMapInstanceKey := fun key =>
      match key with
      | .shared _ => false
      | .cvWaiters _ => true
    isSelectedBy := fun selection key =>
      match selection with
      | .cvWaiters cv => decide (key = Key.cvWaiters cv)
    isTimerIdAtMost := fun _ firstNumber _ _ secondNumber _ => decide (firstNumber ≤ secondNumber) }

/-- Read a shared variable from the Execute snapshot (spec §8.3 "Shared read":
"read from the snapshot"). An absent Attribute reads null. -/
def readShared (snapshot : Dex.Snapshot signature) (name : Global) : Val :=
  match snapshot.attributes.get? (Key.shared name) with
  | some (.value value) => value
  | _ => .null

/-- Read `waiters/cv` from the Execute snapshot. An absent Attribute is the empty list. -/
def readWaiters (snapshot : Dex.Snapshot signature) (cv : CvId) : List Tid :=
  match snapshot.attributes.get? (Key.cvWaiters cv) with
  | some (.waiterQueue waiters) => waiters
  | _ => []

/-- The Attribute write of a shared variable. L's null is the Dex null, which
deletes the Attribute (spec §4.2 `effects`); a deleted Attribute reads null. -/
def encodeSharedWrite (value : Val) : Option DexVal :=
  match value with
  | .null => none
  | other => some (.value other)

/-- The AttributeMap instances the Execute of a program point reads: the
waiter list `waiters/cv` when its head statement is `wait(cv, m)`,
`signal(cv)`, or `broadcast(cv)` (spec §8.3), and none otherwise. The mover
knows `cv`, because the operand is evaluated in the thread state it passes on. -/
def executeLoadMaps (core : ThreadCore) (code : Code) : List MapSelection :=
  match cvOperand core code with
  | some cv => [MapSelection.cvWaiters cv]
  | none => []

/-- A move to the program point of `code` with the thread state as input. A
program point has a WaitFor exactly when its head statement blocks. Its
Execute options select the waiter list it reads (`executeLoadMaps`). -/
def moveToPoint (core : ThreadCore) (code : Code) : Dex.Move signature :=
  { stepType := StepType.point code
    input := some (DexVal.thread core)
    options :=
      { skipWaitFor := !isBlockingHead code
        executeLoadMaps := executeLoadMaps core code } }

/-- A decision that only moves: GoTo or GoToMany. -/
def moveDecision (moves : List (Dex.Move signature)) : Dex.ExecuteResponse signature :=
  { effects := {}, decision := { moves := moves, close := none } }

/-- A decision that only closes: DeadEnd, ForceComplete, or ForceFail. -/
def closeDecision (close : Dex.Close signature) : Dex.ExecuteResponse signature :=
  { effects := {}, decision := { moves := [], close := some close } }

/-- The mapping table of spec §8.3: the Dex effects and decision of each
atomic L action. The thread continues at the program point of `result.code`. -/
def encodeIntent (snapshot : Dex.Snapshot signature) (result : StatementResult) :
    Dex.ExecuteResponse signature :=
  let next := moveToPoint result.core result.code
  match result.intent with
  -- "Sequence, `if`", "`while`", "Call and recursion", "`throw` / `try`": GoTo.
  | .silent => moveDecision [next]
  -- "Shared read / write: One Execute per access: ... or write one Attribute."
  | .store target value =>
    { effects := { writes := [(Key.shared target, encodeSharedWrite value)] }
      decision := { moves := [next], close := none } }
  -- "`fork t`: GoToMany: the parent's continuation and `t`'s first Step."
  | .fork child childCode => moveDecision [next, moveToPoint child childCode]
  -- "New mutex: Execute publishes the first token."
  | .newMutex mutex =>
    { effects := { publications := [(Chan.mutexToken mutex, DexVal.token)] }
      decision := { moves := [next], close := none } }
  -- "`unlock(m)`: Execute publishes one token to `m`."
  | .unlock mutex =>
    { effects := { publications := [(Chan.mutexToken mutex, DexVal.token)] }
      decision := { moves := [next], close := none } }
  -- "`wait(cv, m)`: One Execute, under `m`: append the waiter ID `w` to the
  -- Attribute `waiters/cv` and publish `m`'s token." The snapshot holds
  -- `waiters/cv` because the move to this Step selected it (`moveToPoint`).
  | .cvWait cv =>
    { effects :=
        { writes := [(Key.cvWaiters cv,
            some (DexVal.waiterQueue (readWaiters snapshot cv ++ [result.core.tid])))]
          publications := [(Chan.mutexToken cv.mutex, DexVal.token)] }
      decision := { moves := [next], close := none } }
  -- "`signal(cv)`: One Execute, under `m`: if `waiters/cv` is not empty,
  -- remove its head `h` and publish to `cv/h`."
  | .signal cv =>
    match readWaiters snapshot cv with
    | [] => moveDecision [next]
    | waiter :: laterWaiters =>
      { effects :=
          { writes := [(Key.cvWaiters cv, some (DexVal.waiterQueue laterWaiters))]
            publications := [(Chan.cvWake cv waiter, DexVal.token)] }
        decision := { moves := [next], close := none } }
  -- "`broadcast(cv)`: As `signal`, for every waiter in order."
  | .broadcast cv =>
    match readWaiters snapshot cv with
    | [] => moveDecision [next]
    | waiters =>
      { effects :=
          { writes := [(Key.cvWaiters cv, some (DexVal.waiterQueue []))]
            publications := waiters.map (fun waiter => (Chan.cvWake cv waiter, DexVal.token)) }
        decision := { moves := [next], close := none } }
  -- "`cancel(t)`: One Execute publishes to `cancel/t`."
  | .cancel target =>
    { effects := { publications := [(Chan.cancelRequest target, DexVal.token)] }
      decision := { moves := [next], close := none } }
  -- "`sleep(d)`: GoTo a `Timed` Step (Enter)"; the same for `select`.
  | .enterTimed seconds =>
    moveDecision
      [{ stepType := StepType.timed
         input := some (DexVal.timedThread result.core result.code seconds)
         options := { skipWaitFor := false } }]
  -- "`join(t)`: `t`'s last Step publishes to the Channel `join/t` and decides DE."
  -- "Thread end: DE."
  | .endThread =>
    { effects := { publications := [(Chan.joinDone result.core.tid, DexVal.token)] }
      decision := { moves := [], close := some .deadEnd } }
  -- "`exit(v)` / return of `main`: FC(`{"exit": enc(v)}`)."
  | .exit value => closeDecision (.forceComplete (some (DexVal.exitTag value)))
  -- "`fail(s)` / uncaught exception with a string value `s`: FF(s)."
  | .fail message => closeDecision (.forceFail message)
  -- "In Dex its Step decides DeadEnd and publishes nothing" (spec §8.1, "Halting").
  | .halt => closeDecision .deadEnd

/-- The wait of a blocking statement (spec §8.3):

- `lock(m)`: "WaitFor `Until(m.ForOne())`";
- `recv(c)`: "WaitFor `AnyOf(c.ForOne(), cancel/self.ForOne())`";
- `join(t)`: "`AnyOf(join/t.ForOne(), cancel/self.ForOne())`";
- the second Step of `wait(cv, m)`: "waits `Until(cv/w.ForOne())`".

A statement whose operand has a type error waits for nothing. -/
def encodeWaitRequest (self : Tid) : Option WaitRequest → Dex.Wait signature
  | some (.lock mutex) =>
    { kind := .all, timerSeconds := [], channels := [Chan.mutexToken mutex] }
  | some (.recv chan) =>
    { kind := .any, timerSeconds := [], channels := [Chan.external chan, Chan.cancelRequest self] }
  | some (.join thread) =>
    { kind := .any, timerSeconds := [], channels := [Chan.joinDone thread, Chan.cancelRequest self] }
  | some (.park cv) =>
    { kind := .all, timerSeconds := [], channels := [Chan.cvWake cv self] }
  | none => Dex.Wait.empty signature

/-- An external message as an L value. -/
def decodeMessage : DexVal → Val
  | .value value => value
  | _ => .null

/-- Read how a blocking statement's wait was decided from the condition
results: "AnyOf consumes only from the winning condition" (spec §8.3). -/
def decodeBlockedOutcome (code : Code) (results : Option (Dex.ConditionResults signature)) :
    WakeOutcome :=
  match results with
  | none => .invalid
  | some conditionResults =>
    match code, conditionResults.channelMessages with
    | .source (.lock _) :: _, [some _] => .granted
    | .park _ :: _, [some _] => .granted
    | .source (.join _) :: _, [some _, _] => .granted
    | .source (.join _) :: _, [none, some _] => .canceled
    | .source (.recv _ _) :: _, [some message, _] => .received (decodeMessage message)
    | .source (.recv _ _) :: _, [none, some _] => .canceled
    | _, _ => .invalid

/-- Read how a timed operation was decided from the condition results of its
`Timed` Step: "its Execute dispatches on the result" (spec §8.2). -/
def decodeTimedOutcome (code : Code) (results : Option (Dex.ConditionResults signature)) :
    WakeOutcome :=
  match results with
  | none => .invalid
  | some conditionResults =>
    if conditionResults.timersDone = [true] then .fired
    else
      match timedChan code, conditionResults.channelMessages with
      | some none, [some _] => .canceled
      | some (some _), [some message, _] => .received (decodeMessage message)
      | some (some _), [none, some _] => .canceled
      | _, _ => .invalid

/-- The entry Step (spec §8.2): publish one token to each statically known
mutex Channel, then GoTo the first program point of `main`. -/
def startStepDef (program : Program) : Dex.StepDef signature :=
  { waitFor := none
    execute := fun _ input _ _ =>
      match input with
      | some (.value programInput) =>
        ([],
          { effects :=
              { publications := (List.range program.staticMutexCount).map (fun index =>
                  (Chan.mutexToken (MutexId.static index), DexVal.token)) }
            decision :=
              { moves :=
                  [moveToPoint
                    { tid := [], freshCounter := 0
                      locals := [(program.mainParam, programInput)], stack := [] }
                    (pushCode program.mainBody [])]
                close := none } })
      | _ => ([], closeDecision .deadEnd) }

/-- The Step of a program point (spec §8.2).

- A blocking head statement gets a WaitFor that returns its wait. Its Execute
  continues from the wait's outcome.
- Any other head statement runs in Execute alone. "Each Step method performs
  at most one shared access: a read is taken from the Execute snapshot at
  Start; a write is committed at Complete."
- "Each `emit` is the only effect of its own Execute." -/
def pointStepDef (program : Program) (code : Code) : Dex.StepDef signature :=
  { waitFor :=
      if isBlockingHead code then
        some (fun _ input _ =>
          match input with
          | some (.thread core) =>
            ([], { effects := {}, wait := encodeWaitRequest core.tid (waitRequest core code) })
          | _ => ([], { effects := {}, wait := Dex.Wait.empty signature }))
      else none
    execute := fun _ input snapshot results =>
      match input with
      | some (.thread core) =>
        if isBlockingHead code then
          ([], encodeIntent snapshot (resumeBlocked core code (decodeBlockedOutcome code results)))
        else
          match beginKind core code with
          | .plain => ([], encodeIntent snapshot (completeStatement program core code .null))
          | .load source =>
            ([], encodeIntent snapshot
              (completeStatement program core code (readShared snapshot source)))
          | .emit effect =>
            ([effect], encodeIntent snapshot (completeStatement program core code .null))
      | _ => ([], closeDecision .deadEnd) }

/-- The generic `Timed` Step (spec §8.2, §8.3). "Its WaitFor returns a wait
with exactly one timer, at index 0":

- `sleep(d)`: "`AnyOf(Timer(d), cancel/self.ForOne())`";
- `select`: "`AnyOf(Timer(d), c.ForOne(), cancel/self.ForOne())`".

"Its Execute dispatches on the result and GoTos the continuation." -/
def timedStepDef : Dex.StepDef signature :=
  { waitFor := some (fun _ input _ =>
      match input with
      | some (.timedThread core code seconds) =>
        match timedChan code with
        | some none =>
          ([], { effects := {}
                 wait := { kind := .any, timerSeconds := [seconds]
                           channels := [Chan.cancelRequest core.tid] } })
        | some (some chan) =>
          ([], { effects := {}
                 wait := { kind := .any, timerSeconds := [seconds]
                           channels := [Chan.external chan, Chan.cancelRequest core.tid] } })
        | none => ([], { effects := {}, wait := Dex.Wait.empty signature })
      | _ => ([], { effects := {}, wait := Dex.Wait.empty signature }))
    execute := fun _ input snapshot results =>
      match input with
      | some (.timedThread core code _) =>
        ([], encodeIntent snapshot (resumeTimed core code (decodeTimedOutcome code results)))
      | _ => ([], closeDecision .deadEnd) }

/-- The translation `⟦S⟧` of an L program: one Flow definition (spec §8.2). -/
def translate (program : Program) : Dex.FlowDef signature :=
  { stepDef := fun stepType =>
      match stepType with
      | .start => startStepDef program
      | .point code => pointStepDef program code
      | .timed => timedStepDef }

@[inherit_doc] scoped notation "⟦" program "⟧" => translate program

end DexExpressiveness.Translation
