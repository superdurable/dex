import DexExpressiveness.Dex.Config

/-!
# Dex core: the meta-functions of one commit (spec §4.2)

"All functions act on the run `ρ` of the command's Flow." Each is a total
function on `Run`. A commit first takes `seq := seq + 1` (spec §4.1), then
calls these in the order its rule writes them.

Left out, with the reason:

- `pend`'s lock queue and `release`: no Attribute locks (spec §8.2).
- `cancelTypes`: no CancelSteps (spec §8.2).
- `gcheck`: GracefulComplete is never decided, so `gc` stays false and
  `gcheck()` never closes the run. Its call sites are marked.
- `closeNow`: no StopFlow (spec §5.3).
- the deferral branch of `close`: `Tx⁻[f] = 0`, because there are no RPCs.
-/

namespace DexExpressiveness.Dex

variable {σ : Signature}

namespace Run

/-- `S[e]`. -/
def findStep? (run : Run σ) (step : StepExecId σ) : Option (StepRecord σ) :=
  run.steps.find? (fun record => decide (record.id = step))

/-- `S[e] := change(S[e])`. -/
def updateStep (run : Run σ) (step : StepExecId σ)
    (change : StepRecord σ → StepRecord σ) : Run σ :=
  { run with
    steps := run.steps.map (fun record => if record.id = step then change record else record) }

/-- `Q[c]`: the pending messages of a Channel, oldest first. -/
def pendingMessages (run : Run σ) (chan : σ.Chan) : List σ.Val :=
  (run.channels.get? chan).getD []

/-- `Q[c] := messages`. -/
def setPendingMessages (run : Run σ) (chan : σ.Chan) (messages : List σ.Val) : Run σ :=
  { run with channels := run.channels.set chan messages }

/-- `new(T, v, o, from)` creates a Step execution (spec §4.2). `pend(e, m)`
reduces to setting the pending invocation, because no lock key is queued. -/
def newStep (run : Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (skipWaitFor : Bool) (origin : Origin σ) : Run σ :=
  let number := (run.counters.get? stepType).getD 0 + 1
  let method := if skipWaitFor then Method.execute else Method.waitFor
  { run with
    counters := run.counters.set stepType number
    steps := run.steps ++
      [{ id := { stepType := stepType, number := number }
         input := input
         skipWaitFor := skipWaitFor
         origin := origin
         phase := if skipWaitFor then .execute else .waitFor
         invocation := some { method := method, state := .pending }
         waitState := none
         conditionResults := none }] }

/-- The `Any` branch of `plan` over the Channel conditions, in declaration
order: the first Channel with a pending message consumes one; the others
consume nothing (spec §4.2 `plan`, `candidates(Any)`). -/
def planAnyChannel (pendingCount : σ.Chan → Nat) : List σ.Chan → Option (List Bool)
  | [] => none
  | chan :: laterChans =>
    if 0 < pendingCount chan then some (true :: laterChans.map (fun _ => false))
    else (planAnyChannel pendingCount laterChans).map (fun laterPlan => false :: laterPlan)

/-- `plan(W, progress)` picks the first satisfied candidate and its exact
consumption (spec §4.2). The result has one flag per Channel condition: whether
it consumes one message. `none` is `⊥`.

- An empty wait is satisfied at once with no consumption.
- `All`: every timer is done, and each Channel has at least as many pending
  messages as conditions on it ("per Channel name, the sum of the minimums").
- `Any`: candidates are each timer, then each Channel, in declaration order. -/
def plan (wait : Wait σ) (pendingCount : σ.Chan → Nat) (timersDone : List Bool) :
    Option (List Bool) :=
  if wait.hasNoCondition then some []
  else
    match wait.kind with
    | .all =>
      if timersDone.all id &&
          wait.channels.all (fun chan => decide (wait.channels.count chan ≤ pendingCount chan))
      then some (wait.channels.map (fun _ => true))
      else none
    | .any =>
      if timersDone.any id then some (wait.channels.map (fun _ => false))
      else planAnyChannel pendingCount wait.channels

/-- The consumption step of `eval` (spec §4.2): "for `(j, count)` in `π`:
`msgs_j :=` first `count` of `Q[c_j]`; `Q[c_j] :=` rest". Returns the run and,
per Channel condition, the consumed message or `none` for "not done". -/
def consumeMessages : List σ.Chan → List Bool → Run σ → Run σ × List (Option σ.Val)
  | [], _, run => (run, [])
  | chan :: laterChans, consumeFlags, run =>
    match consumeFlags.headD false, run.pendingMessages chan with
    | true, message :: remaining =>
      let later := consumeMessages laterChans consumeFlags.tail
        (run.setPendingMessages chan remaining)
      (later.1, some message :: later.2)
    | _, _ =>
      let later := consumeMessages laterChans consumeFlags.tail run
      (later.1, none :: later.2)

/-- The timer part of `unregister(e)` (spec §4.2 `eval`, `finish`): disarm the
unfired timers of `e`. Waiter rows are not stored; a Step waits on a Channel
exactly while its phase is `WT` and its wait names the Channel. -/
def disarmTimersOf (run : Run σ) (step : StepExecId σ) : Run σ :=
  { run with timers := run.timers.filter (fun entry => !decide (entry.1.step = step)) }

/-- `eval(e)` fires a satisfied wait (spec §4.2). -/
def evalStep (run : Run σ) (step : StepExecId σ) : Run σ :=
  match run.findStep? step with
  | none => run
  | some record =>
    match record.phase, record.waitState with
    | .waiting, some waitState =>
      match plan waitState.wait (fun chan => (run.pendingMessages chan).length)
          waitState.timersDone with
      | none => run
      | some consumeFlags =>
        let consumed := consumeMessages waitState.wait.channels consumeFlags
          (run.disarmTimersOf step)
        consumed.1.updateStep step (fun current =>
          { current with
            conditionResults :=
              some { timersDone := waitState.timersDone, channelMessages := consumed.2 }
            phase := .execute
            invocation := some { method := .execute, state := .pending } })
    | _, _ => run

/-- The Steps that wait on a Channel in `changed`, earliest waiter first (spec
§4.2 `wake`: sorted by `(sws, e)`). The tie-break on `e` is not modeled: each
commit registers at most one wait, so two waiters never share `sws` (spec
Appendix A, assumption 11). -/
def waitersOn (run : Run σ) (changed : List σ.Chan) : List (StepExecId σ) :=
  let waiters := run.steps.filterMap (fun record =>
    match record.phase, record.waitState with
    | .waiting, some waitState =>
      if waitState.wait.channels.any (fun chan => changed.contains chan)
      then some (waitState.registeredSeq, record.id) else none
    | _, _ => none)
  (sortStable (fun first second => decide (first.1 ≤ second.1)) waiters).map (fun waiter => waiter.2)

/-- `wake(C)` re-evaluates every Step that waits on a Channel in `C`, earliest
waiter first (spec §4.2). -/
def wake (run : Run σ) (changed : List σ.Chan) : Run σ :=
  (run.waitersOn changed).foldl evalStep run

/-- The writes of `effects`, in list order: "`A[k] := v` (null deletes `k`)";
"Later writes in the list win" (spec §4.2). -/
def applyWrites (run : Run σ) (writes : List (σ.Key × Option σ.Val)) : Run σ :=
  writes.foldl (fun current write =>
    match write.2 with
    | some value => { current with attributes := current.attributes.set write.1 value }
    | none => { current with attributes := current.attributes.erase write.1 }) run

/-- The publications of `effects`, in list order: "`Q[c] := Q[c] ++ [msg]`" (spec §4.2). -/
def applyPublications (run : Run σ) (publications : List (σ.Chan × σ.Val)) : Run σ :=
  publications.foldl (fun current publication =>
    current.setPendingMessages publication.1
      (current.pendingMessages publication.1 ++ [publication.2])) run

/-- `effects(e?, E, locals)` applies a handler's data effects: writes, then
publications, then `wake` of the Channels published to (spec §4.2). Deletions
and locals are not modeled. -/
def applyEffects (run : Run σ) (effects : Effects σ) : Run σ :=
  ((run.applyWrites effects.writes).applyPublications effects.publications).wake
    (effects.publications.map (fun publication => publication.1))

/-- Arm the timers of a wait: "for each timer `i`: `Tm[wait(e, i)] := now + d_i · 10⁶`"
(spec §4.2 `register`). `fireAt` holds the fire times from `index` on. -/
def armTimers (step : StepExecId σ) :
    List Nat → Nat → AssocList (TimerId σ) Nat → AssocList (TimerId σ) Nat
  | [], _, timers => timers
  | fireTime :: laterFireTimes, index, timers =>
    armTimers step laterFireTimes (index + 1)
      (timers.set { step := step, index := index } fireTime)

/-- `register(e, W)` stores a wait after WaitFor (spec §4.2). `now` is the
commit timestamp. "A zero-second timer is armed, not completed." -/
def registerWait (run : Run σ) (step : StepExecId σ) (wait : Wait σ) (now : Nat) : Run σ :=
  if wait.hasNoCondition then
    run.updateStep step (fun current =>
      { current with
        conditionResults := some { timersDone := [], channelMessages := [] }
        phase := .execute
        invocation := some { method := .execute, state := .pending } })
  else
    let fireAt := wait.timerSeconds.map (fun seconds => now + seconds * 1000000)
    let armed := { run with timers := armTimers step fireAt 0 run.timers }
    let registered := armed.updateStep step (fun current =>
      { current with
        phase := .waiting
        waitState := some
          { wait := wait
            fireAt := fireAt
            timersDone := wait.timerSeconds.map (fun _ => false)
            registeredSeq := run.seq } })
    registered.evalStep step

/-- `finish(e, φ')` ends a Step as `CO` or `CA` (spec §4.2): clear the
invocation, drop the input, unregister, and discard the claimed messages
("messages are discarded, not re-queued"). -/
def finishStep (run : Run σ) (step : StepExecId σ) (finalPhase : Phase) : Run σ :=
  (run.disarmTimersOf step).updateStep step (fun current =>
    { current with
      invocation := none
      input := none
      conditionResults := none
      phase := finalPhase })

/-- `collect(e, v)` appends `(S[e].T, e, v)` to `outs` when `v` is not null (spec §4.2). -/
def collectOutput (run : Run σ) (step : StepExecId σ) (output : Option σ.Val) : Run σ :=
  match output with
  | some value => { run with outputs := run.outputs ++ [(step, value)] }
  | none => run

/-- `close(κ)` then `record(κ)`, a natural close (spec §4.2): cancel every
active Step in creation order and set `st := Closed(κ.status)`. The close is
never deferred, because no transactional RPC exists. The `closed` event that
`record` emits is produced by the commit wrapper (`Config.commit`). -/
def closeRun (run : Run σ) (status : CloseStatus) : Run σ :=
  match run.status with
  | .closed _ => run
  | .running =>
    let activeSteps := (run.steps.filter (fun record => record.phase.isActive)).map
      (fun record => record.id)
    let canceled := activeSteps.foldl (fun current step => current.finishStep step .canceled) run
    { canceled with status := .closed status }

/-- `decide(e, D)` applies an Execute decision (spec §4.2): `finish(e, CO)`,
then the close, or else the moves. The trailing `gcheck()` is a no-op here. -/
def applyDecision (run : Run σ) (step : StepExecId σ) (decision : Decision σ) : Run σ :=
  let finished := run.finishStep step .completed
  match decision.close with
  | some (.forceComplete output) => (finished.collectOutput step output).closeRun .completed
  | some (.forceFail message) => finished.closeRun (.failed message)
  | some .deadEnd => finished
  | none =>
    decision.moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.skipWaitFor (.step step)) finished

/-- `snapshot(e, m)`: what the attempt reads (spec §4.2). Every Attribute is
loaded; `sizes` and `loaded` are not modeled. -/
def snapshot (run : Run σ) : Snapshot σ :=
  { attributes := run.attributes }

/-- The index order of two armed timers: `(fire time, encoded TimerId bytes)`
(spec §4.5), with the byte order supplied by the signature. -/
def isTimerEntryAtMost (first second : TimerId σ × Nat) : Bool :=
  decide (first.2 < second.2) ||
  (decide (first.2 = second.2) &&
    σ.isTimerIdAtMost first.1.step.stepType first.1.step.number first.1.index
      second.1.step.stepType second.1.step.number second.1.index)

/-- `Due_f = { i ∈ dom(Tm) | Tm(i) ≤ t_{p(f)} }` in index order (spec §4.5). -/
def dueTimers (run : Run σ) (clock : Nat) : List (TimerId σ) :=
  (sortStable isTimerEntryAtMost
    (run.timers.filter (fun entry => decide (entry.2 ≤ clock)))).map (fun entry => entry.1)

/-- The effect of [TIMER-FIRE] for one timer `i` (spec §4.5): skip it when an
earlier timer of the command disarmed it; otherwise remove it and, when its
Step still waits and the condition is not done, mark it done and `eval(e)`.
The trailing `gcheck()` is a no-op here. -/
def fireTimer (run : Run σ) (timer : TimerId σ) : Run σ :=
  if run.timers.contains timer then
    let disarmed := { run with timers := run.timers.erase timer }
    match disarmed.findStep? timer.step with
    | none => disarmed
    | some record =>
      match record.phase, record.waitState with
      | .waiting, some waitState =>
        if waitState.timersDone.getD timer.index true then disarmed
        else
          (disarmed.updateStep timer.step (fun current =>
            { current with
              waitState := some
                { waitState with timersDone := waitState.timersDone.set timer.index true } })
          ).evalStep timer.step
      | _, _ => disarmed
  else run

end Run

end DexExpressiveness.Dex
