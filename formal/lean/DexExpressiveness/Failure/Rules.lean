import DexExpressiveness.Failure.Config

/-!
# DexF: the rules under failures (spec §11.3)

`step program config action` is a partial function, like `Dex.step`. The
action names the rule and carries its nondeterministic choice. Every rule of
the failure-free model is here, with the attempt bookkeeping it now needs,
together with the failure rules of spec §11.3:

| Spec rule | Action |
| --- | --- |
| [TICK] | `tick` |
| [FLOW-START], [PUBLISH], the rejected publication | `request` |
| [START-RETRY] | `startRetry` |
| [ENGINE-REJECT] | `engineReject` |
| [STEP-START], extended | `stepStart` |
| [WORKER-EFFECT], extended, and [ZOMBIE-EFFECT] | `workerRun`, `workerEffect`, `workerReturn` |
| [WORKER-ABANDON], and the drop of a zombie | `workerAbandon` |
| Complete rules, extended | `stepComplete` |
| [RESULT-STALE] of a returned task | `resultStale` |
| [TIMER-FIRE] and [RETRY-FIRE] | `timerFire` |
| [ATTEMPT-LOST] (Running and before-start branches), [ATTEMPT-EXHAUSTED] | `attemptFailed` |
| [ATTEMPT-RETURNED] | `attemptReturned` |
| [ATTEMPT-HEARTBEAT] | `attemptHeartbeat` |
| [CRASH], [RECOVER], [RECOVER-INTERRUPT] | `crash`, `recover`, `recoverInterrupt` |
| Bookkeeping and other internal ignored commands | `internalCommit` |

Failures are over-approximated (spec §11.3): every failure rule is
nondeterministic and has no detection premise.

**Gating on `up`** (spec §11.2): "Every commit rule, every engine rule except
[RECOVER], and [ENGINE-REJECT] require `up`. [RECOVER] requires `¬up`.
Worker rules ([WORKER-EFFECT], [ZOMBIE-EFFECT], [WORKER-ABANDON]) and [TICK]
do not."

**Progress labels.** `TransitionSystem.isProgress` reads only the label, but
spec §11.5 defines progress through the image under the transition map,
which for a Worker effect depends on the state. Lean decision: the labels
whose image depends on the state carry that classification as a Boolean
(`isSnapshotReused`, `isFirstRun`, `isFirstOccurrence`, `isFirstReturn`,
`isLastOfCanceled`, `isWaitTimerFired`), and `step` rejects a label whose
Boolean disagrees with the state. So exactly one of the two labels is
enabled, and `isProgress` (`Failure/Observation.lean`) is a function of the
label.

**Retention and RunPurge are left out.** Lean decision, sound for the stated
theorem: in the fragment, nothing reads a closed run after its close except
two inputs (spec §11.2, "Post-close housekeeping"). A publication is
rejected with the same event `rejected(pub(f, [(c, v)]), NOT_FOUND)` before
and after the purge; after the purge it is [ENGINE-REJECT] instead of the
apply rejection, with the same event and no state change. A StartFlow retry
is deduplicated only before RunPurge, and HP orders every retry before
RunPurge. The housekeeping commands themselves are stutters. So without them
HP holds by construction, a retry always finds the run, and [ENGINE-REJECT]
applies only to a publication before the start, which `E_adm` excludes.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- A client request under failures (spec §11.3, "StartFlow retries, by
request identity"). StartFlow carries its request ID; PublishToChannel has
none (`protos/dex.proto:650-654`). Every StartFlow sets
`ignore_already_started_error` and uses the default reuse policy (spec §11.3,
"`E_adm` with retries"); a StartFlow without it is outside `E_adm` and not
modeled. -/
inductive Request (σ : Dex.Signature)
  /-- `start(f, T₀, v, ∅)` with its request ID. -/
  | startFlow (requestId : Nat) (stepType : σ.StepType) (input : σ.Val)
  /-- `pub(f, [(c, v)])`. -/
  | publish (chan : σ.Chan) (value : σ.Val)

/-- The failure-free form of a request: the request ID erased. -/
def Request.toDex : Request σ → Dex.Request σ
  | .startFlow _ stepType input => .startFlow stepType input
  | .publish chan value => .publish chan value

/-- The DexF event alphabet (spec §11.4): `Obs` of spec §5.1, where "`fx`
carries the ghost index: `fx(f, (e, m), x)ʲ` is the `j`-th effect of some
attempt of `(e, m)`." -/
inductive Obs (σ : Dex.Signature)
  /-- `start(f, T, v, ∅)`. -/
  | start (stepType : σ.StepType) (input : σ.Val)
  /-- `pub(f, [(c, v)])`. -/
  | pub (chan : σ.Chan) (value : σ.Val)
  /-- `rejected(pub(f, [(c, v)]), NOT_FOUND)`. -/
  | rejectedPub (chan : σ.Chan) (value : σ.Val)
  /-- `fx(f, (e, m), x)ʲ`, with the 1-based ghost index `j`. -/
  | fx (step : Dex.StepExecId σ) (method : Dex.Method) (effect : σ.Fx) (index : Nat)
  /-- `closed(f, status, error, outs)`. -/
  | closed (status : Dex.CloseStatus) (outputs : List (Dex.StepExecId σ × σ.Val))

/-- "The projection to real labels erases `j`" (spec §11.4). -/
def Obs.toDex : Obs σ → Dex.Obs σ
  | .start stepType input => .start stepType input
  | .pub chan value => .pub chan value
  | .rejectedPub chan value => .rejectedPub chan value
  | .fx step method effect _ => .fx step method effect
  | .closed status outputs => .closed status outputs

/-- Why the retry decision exhausted the method (spec §11.3
[ATTEMPT-EXHAUSTED]: "the failure is non-retryable, the attempt number
reached the policy's maximum, or the next retry would pass the total
duration"). -/
inductive ExhaustionReason
  /-- A non-retryable failure (H4 excludes it). -/
  | nonRetryable
  /-- The attempt number reached the policy's maximum. -/
  | maximumAttempts
  /-- The next retry would pass the total retry duration. -/
  | totalDuration
  deriving DecidableEq, Repr

/-- The retry decision of a failure, RetryAt or Exhausted
(`retry_backoff.rs:70-121`). -/
inductive RetryDecision
  /-- RetryAt: arm the retry timer `backoffMicros` after the commit stamp. -/
  | retryAfter (backoffMicros : Nat)
  /-- Exhausted: [ATTEMPT-EXHAUSTED]. -/
  | exhausted (reason : ExhaustionReason)
  deriving DecidableEq, Repr

/-- Whether the decision is Exhausted. -/
def RetryDecision.isExhausted : RetryDecision → Bool
  | .exhausted _ => true
  | .retryAfter _ => false

/-- One transition of DexF, with the nondeterministic choice of its rule. -/
inductive Action (σ : Dex.Signature)
  /-- [TICK]: the clock advances by `duration > 0` microseconds. -/
  | tick (duration : Nat)
  /-- A client request reaches apply: [FLOW-START] (no run yet), [PUBLISH],
  or the apply rejection of a publication. -/
  | request (request : Request σ)
  /-- [START-RETRY]: a StartFlow with the run's start request ID is
  deduplicated. -/
  | startRetry (requestId : Nat) (stepType : σ.StepType) (input : σ.Val)
  /-- [ENGINE-REJECT]: a targeted request whose Flow ID has no run is rejected
  before any proposal. -/
  | engineReject (request : Request σ)
  /-- [STEP-START], extended. `isSnapshotReused` says whether the invocation
  holds a retained snapshot. -/
  | stepStart (step : Dex.StepExecId σ) (isSnapshotReused : Bool)
  /-- [WORKER-EFFECT] first part, for a task in `Θ` or `Z`: a `Started` task
  runs the handler. `isFirstRun` says whether `v(e, m) = NotRun`. -/
  | workerRun (taskId : Nat) (isFirstRun : Bool)
  /-- [WORKER-EFFECT] or [ZOMBIE-EFFECT]: a `Running` task emits its next
  effect, with index `j`. `isFirstOccurrence` says whether `j > h(e, m)`. -/
  | workerEffect (taskId : Nat) (isFirstOccurrence : Bool)
  /-- [WORKER-EFFECT] last part, for a task in `Θ` or `Z`: a task with no
  effect left returns. `isFirstReturn` says whether `v(e, m)` is not
  `Returned`. -/
  | workerReturn (taskId : Nat) (isFirstReturn : Bool)
  /-- [WORKER-ABANDON] of a task of `Θ` whose invocation is no longer
  current, or the drop of a task of `Z` ("A zombie may also be dropped at any
  point"). `isLastOfCanceled` says whether it is the last real task of a
  canceled invocation. -/
  | workerAbandon (taskId : Nat) (isLastOfCanceled : Bool)
  /-- [STEP-COMPLETE-WAITFOR] or [STEP-COMPLETE-EXECUTE], extended: a
  `Returned` task of `Θ` or `Z` that passes the fence. -/
  | stepComplete (taskId : Nat)
  /-- [RESULT-STALE] of the result of a `Returned` task of `Θ` or `Z` that
  fails the fence. `isLastOfCanceled` as for `workerAbandon`. -/
  | resultStale (taskId : Nat) (isLastOfCanceled : Bool)
  /-- [TIMER-FIRE] and [RETRY-FIRE]: the first `count ≥ 1` due timers, wait
  and retry, in index order. `isWaitTimerFired` says whether a wait timer is
  among them. -/
  | timerFire (count : Nat) (isWaitTimerFired : Bool)
  /-- A `StepAttemptFailed` that passes its fence: [ATTEMPT-LOST] or
  [ATTEMPT-EXHAUSTED], by the decision. The Running branch fails the running
  attempt; the before-start branch fails a Pending one. -/
  | attemptFailed (step : Dex.StepExecId σ) (decision : RetryDecision)
  /-- [ATTEMPT-RETURNED] of the running attempt of the Step. -/
  | attemptReturned (step : Dex.StepExecId σ)
  /-- [ATTEMPT-HEARTBEAT] of the running attempt of the Step, with its checkpoint. -/
  | attemptHeartbeat (step : Dex.StepExecId σ) (value : σ.Val)
  /-- [CRASH]. -/
  | crash
  /-- [RECOVER]. -/
  | recover
  /-- [RECOVER-INTERRUPT] of an element of `I`, with the retry decision used
  when its fence holds. -/
  | recoverInterrupt (interrupt : Interrupt σ) (decision : RetryDecision)
  /-- A log commit that changes no modeled state: a bookkeeping command such
  as StepAttemptQueued, a fenced command that apply ignores and that carries
  no returned task, a duplicate proposal, or a TimersFired whose timers are
  all gone (spec §11.2, the classes "Bookkeeping" and "Internal ignored or
  rejected"). -/
  | internalCommit

/-- The message of the close of [ATTEMPT-EXHAUSTED]. Lean decision:
`Dex.CloseStatus.failed` has no error type, so the close carries the error
type `WORKER_API_FAIL` as its message (spec §11.3: "the Flow closes as Failed
with WORKER_API_FAIL or INTERNAL"). H1 excludes the rule from the theorem. -/
def exhaustionMessage : String := "WORKER_API_FAIL"

namespace Run

/-- The attempt state of a Step's current invocation. -/
def findAttempt? (run : Run σ) (step : Dex.StepExecId σ) : Option (Attempt σ) :=
  run.attempts.get? step

/-- Set the attempt state of a Step. -/
def setAttempt (run : Run σ) (step : Dex.StepExecId σ) (attempt : Attempt σ) : Run σ :=
  { run with attempts := run.attempts.set step attempt }

/-- Keep the attempt states in step with the Step records after a commit: a
Step without an invocation has none (the snapshot is released,
`invocations.rs:344-373`), and a newly pended invocation gets a fresh one
(`pend`, `invocations.rs:57-138`). -/
def reconcileAttempts (run : Run σ) : Run σ :=
  { run with
    attempts := run.base.steps.filterMap (fun record =>
      match record.invocation with
      | none => none
      | some invocation =>
        match run.attempts.get? record.id with
        | some attempt =>
          if attempt.method = invocation.method then some (record.id, attempt)
          else some (record.id, Attempt.fresh invocation.method)
        | none => some (record.id, Attempt.fresh invocation.method)) }

/-- The interrupts that [RECOVER] records: "`ip = R` with task ID `t`: `I := I
∪ {(e, m, a, t)}`" (spec §11.3). -/
def runningInterrupts (run : Run σ) : List (Interrupt σ) :=
  run.base.steps.filterMap (fun record =>
    match record.invocation, run.attempts.get? record.id with
    | some { method := method, state := .running taskId }, some attempt =>
      some { step := record.id, method := method, attempt := attempt.number, taskId := taskId }
    | _, _ => none)

/-- The retry part of [RETRY-FIRE] for one timer `retry(e, m, a)` (spec §11.3):
"disarmed if armed and then handled": "if `S[e].inv = ⟨m, B, a, …⟩`, then
`pend` again: `ip := P` ... The retained snapshot stays. Otherwise nothing."
A timer that is no longer armed is skipped. -/
def fireRetryTimer (run : Run σ) (timer : RetryTimerId σ) : Run σ :=
  if run.retryTimers.contains timer then
    let disarmed := { run with retryTimers := run.retryTimers.erase timer }
    match disarmed.base.findStep? timer.step, disarmed.attempts.get? timer.step with
    | some record, some attempt =>
      if decide (record.invocation = some { method := timer.method, state := .pending }) &&
          decide (attempt.method = timer.method) && attempt.isBackoff &&
          decide (attempt.number = timer.attempt) then
        disarmed.setAttempt timer.step { attempt with isBackoff := false }
      else disarmed
    | _, _ => disarmed
  else run

end Run

/-- A due timer of a run: a wait timer or a retry timer (spec §11.3: "Wait
timers and retry timers are interleaved in that order"). -/
inductive DueTimer (σ : Dex.Signature)
  /-- A wait timer `wait(e, j)`. -/
  | wait (timer : Dex.TimerId σ)
  /-- A retry timer `retry(e, m, a)`. -/
  | retry (timer : RetryTimerId σ)

/-- Whether a due timer is a wait timer. -/
def DueTimer.isWait : DueTimer σ → Bool
  | .wait _ => true
  | .retry _ => false

/-- The index order of two armed timers, wait or retry, with their fire
times: "`(fire time, encoded TimerId)`" (spec §11.3). Between two wait
timers it is the failure-free order `Dex.Run.isTimerEntryAtMost`.

Lean decision: the model does not compute TimerId bytes. At equal fire times
a wait timer sorts before a retry timer, and retry timers keep their list
order. Any fixed choice serves: "A retry handler touches neither wait state
nor Channels, so its position among the wait timers does not change their
effects" (spec §11.3, "Mapping"). -/
def isDueEntryAtMost : DueTimer σ × Nat → DueTimer σ × Nat → Bool
  | (.wait first, firstTime), (.wait second, secondTime) =>
    Dex.Run.isTimerEntryAtMost (first, firstTime) (second, secondTime)
  | (.wait _, firstTime), (.retry _, secondTime) => decide (firstTime ≤ secondTime)
  | (.retry _, firstTime), (.wait _, secondTime) => decide (firstTime < secondTime)
  | (.retry _, firstTime), (.retry _, secondTime) => decide (firstTime ≤ secondTime)

namespace Run

/-- `Due_f` of DexF: the armed wait and retry timers with fire time at most
`clock`, in index order (spec §4.5, §11.3). -/
def dueTimers (run : Run σ) (clock : Nat) : List (DueTimer σ) :=
  let waits := (run.base.timers.filter (fun entry => decide (entry.2 ≤ clock))).map
    (fun entry => (DueTimer.wait entry.1, entry.2))
  let retries := (run.retryTimers.filter (fun entry => decide (entry.2 ≤ clock))).map
    (fun entry => (DueTimer.retry entry.1, entry.2))
  (sortStable isDueEntryAtMost (waits ++ retries)).map (fun entry => entry.1)

/-- The effect of [TIMER-FIRE] or [RETRY-FIRE] for one timer of `D`, in order. -/
def fireDueTimer (run : Run σ) : DueTimer σ → Run σ
  | .wait timer => { run with base := run.base.fireTimer timer }
  | .retry timer => run.fireRetryTimer timer

end Run

/-- After a seq-taking commit, keep the ghost invocations in step with the
Step records (spec §11.2): an invocation that is no longer current ended,
committed when this commit is its Complete rule and canceled otherwise; an
invocation pended for the first time gets a fresh history with `ps₀ :=
ζ(s) = n`. -/
def Ghost.reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ)) (base : Dex.Run σ) :
    Ghost σ :=
  let current : List (InvocationKey σ) :=
    base.steps.filterMap (fun record => record.invocation.map (fun invocation =>
      (record.id, invocation.method)))
  let marked : AssocList (InvocationKey σ) (InvocationHistory σ) :=
    ghost.invocations.map (fun entry =>
      if entry.2.outcome = .live && !current.contains entry.1 then
        (entry.1, { entry.2 with
          outcome := if completed = some entry.1 then .committed else .canceled })
      else entry)
  let added := current.foldl (fun (entries : AssocList (InvocationKey σ) (InvocationHistory σ)) key =>
    if entries.contains key then entries
    else entries.set key (InvocationHistory.fresh ghost.normalCount)) marked
  { ghost with invocations := added }

namespace Config

/-- A log commit may stamp `now = t_p` only when the clock is strictly above
the last log commit's stamp (spec §4.1, §11.3). -/
def canCommit (config : Config σ) : Bool :=
  match config.lastCommitAt with
  | none => true
  | some last => decide (last < config.clock)

/-- Whether the server serves and a log commit may stamp now. -/
def canServeCommit (config : Config σ) : Bool :=
  config.isUp && config.canCommit

/-- A log commit that changes no modeled state: the classes "Internal ignored
or rejected" and "Bookkeeping" of spec §11.2. It takes no run `seq`; its
stamp still advances the partition clock. -/
def stampOnly (config : Config σ) : Config σ :=
  { config with lastCommitAt := some config.clock }

/-- The `closed` event of `record(κ)`, emitted by the commit in which the run
closes (spec §4.2). -/
def closeEvents (before after : Dex.Run σ) : List (Obs σ) :=
  match before.status, after.status with
  | .running, .closed status => [Obs.closed status after.outputs]
  | _, _ => []

/-- One applied command that takes a run `seq` (spec §11.2): it stamps `now`,
takes `seq := seq + 1`, applies `change`, and keeps the attempt states and
the ghosts in step. A normal command (`isNormal`) also sets `n := n + 1`,
`ζ[s] := n`, and records its stamp; a command of the failure class leaves
them unchanged. `completed` names the invocation whose result this command
commits. -/
def commitRun (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) : List (Obs σ) × Config σ :=
  let seq := run.base.seq + 1
  let changed := change { run with base := { run.base with seq := seq } }
  let after := changed.reconcileAttempts
  let counted : Ghost σ :=
    if isNormal then
      { config.ghost with
        normalCount := config.ghost.normalCount + 1
        normalIndex := config.ghost.normalIndex.set seq (config.ghost.normalCount + 1)
        lastNormalCommitAt := some config.clock }
    else config.ghost
  (inputEvents ++ closeEvents run.base after.base,
    { config with
      run := some after
      lastCommitAt := some config.clock
      ghost := counted.reconcile completed after.base })

/-- The task of `Θ` with this ID. -/
def findLiveTask? (config : Config σ) (taskId : Nat) : Option (Task σ) :=
  config.tasks.find? (fun task => decide (task.base.id = taskId))

/-- The task of `Z` with this ID. -/
def findZombie? (config : Config σ) (taskId : Nat) : Option (Task σ) :=
  config.zombies.find? (fun task => decide (task.base.id = taskId))

/-- The real task, in `Θ` or `Z`, with this ID. -/
def findRealTask? (config : Config σ) (taskId : Nat) : Option (Task σ) :=
  config.realTasks.find? (fun task => decide (task.base.id = taskId))

/-- Change the real task with this ID, wherever it is. -/
def updateRealTask (config : Config σ) (taskId : Nat) (change : Task σ → Task σ) : Config σ :=
  { config with
    tasks := config.tasks.map (fun task => if task.base.id = taskId then change task else task)
    zombies := config.zombies.map (fun task => if task.base.id = taskId then change task else task) }

/-- Remove the real task with this ID from `Θ` and `Z`. -/
def removeRealTask (config : Config σ) (taskId : Nat) : Config σ :=
  { config with
    tasks := config.tasks.filter (fun task => !decide (task.base.id = taskId))
    zombies := config.zombies.filter (fun task => !decide (task.base.id = taskId)) }

/-- "If `t ∈ dom(Θ)`: `Z[t] := Θ[t]`; delete `Θ[t]`" (spec §11.3 [ATTEMPT-LOST]). -/
def moveToZombies (config : Config σ) (taskId : Nat) : Config σ :=
  { config with
    tasks := config.tasks.filter (fun task => !decide (task.base.id = taskId))
    zombies := config.zombies ++ config.tasks.filter (fun task => decide (task.base.id = taskId)) }

/-- The fence of a started-attempt command (spec §11.3 [RESULT-STALE]): "the
Step execution exists, its current invocation has the command's method and
attempt number, its phase is Running, and its task ID equals the command's
task ID". -/
def isFencePassed (config : Config σ) (task : Task σ) : Bool :=
  match config.run with
  | none => false
  | some run =>
    match run.base.findStep? task.base.step, run.findAttempt? task.base.step with
    | some record, some attempt =>
      decide (record.invocation =
          some { method := task.base.method, state := .running task.base.id }) &&
        decide (attempt.number = task.attemptContext.attempt)
    | _, _ => false

/-- Whether the task is the last real task of a canceled invocation that has
a virtual task (spec §11.6: "The virtual task leaves `Θ` when the last real
task of `(e, m)` leaves `Θ` and `Z`"). -/
def isLastOfCanceled (config : Config σ) (task : Task σ) : Bool :=
  match config.ghost.findInvocation? task.key with
  | some history =>
    decide (history.outcome = .canceled) && history.arguments.isSome &&
      !config.realTasks.any (fun other =>
        decide (other.key = task.key) && !decide (other.base.id = task.base.id))
  | none => false

/-- The common effect of [ATTEMPT-LOST], [ATTEMPT-EXHAUSTED], and an applied
[RECOVER-INTERRUPT] on the invocation of `step` (spec §11.3):

```text
snap kept, pinned for attempt a+1
ip := B;  a := a + 1;  tid := ⊥
Tm[retry(e, m, a)] := now + backoff
if t ∈ dom(Θ): Z[t] := Θ[t];  delete Θ[t]
```

For Exhausted, the Flow closes as Failed instead. Both take a run `seq` and
are not normal commands. Lock keys are not modeled. -/
def failAttempt (config : Config σ) (run : Run σ) (step : Dex.StepExecId σ)
    (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) : List (Obs σ) × Config σ :=
  let moved :=
    match lostTaskId with
    | some taskId => config.moveToZombies taskId
    | none => config
  match decision with
  | .retryAfter backoffMicros =>
    let nextAttempt := attempt.number + 1
    moved.commitRun run false none [] (fun current =>
      { current with
        base := current.base.updateStep step (fun record =>
          { record with invocation := some { method := method, state := .pending } })
        attempts := current.attempts.set step
          { attempt with isBackoff := true, number := nextAttempt, lastFailure := some cause }
        retryTimers := current.retryTimers.set
          { step := step, method := method, attempt := nextAttempt }
          (config.clock + backoffMicros) })
  | .exhausted _ =>
    moved.commitRun run false none [] (fun current =>
      { current with base := current.base.closeRun (.failed exhaustionMessage) })

end Config

/-- The context a Step method attempt receives, its `ctxA` (spec §11.3
[STEP-START]: "`Θ[tid] := ⟨f, e, m, a, tid, S[e].in, snap, S[e].loc,
S[e].cr, ctxA, Started, 0⟩`"). -/
def Attempt.context (attempt : Attempt σ) (now : Nat) : AttemptContext σ :=
  { attempt := attempt.number
    firstAttemptTimestamp := attempt.firstAttemptTimestamp.getD now
    recoveryError := attempt.lastFailure
    lastHeartbeatValue := attempt.lastHeartbeatValue }

/-- One transition `Σ_F --ℓ⃗--> Σ_F'` of DexF (spec §11.3). -/
def step (program : Program σ) (config : Config σ) : Action σ → Option (List (Obs σ) × Config σ)
  -- [TICK] (spec §4.5): unchanged, and it does not need `up`.
  | .tick duration =>
    if 0 < duration then some ([], { config with clock := config.clock + duration }) else none
  -- [FLOW-START] (spec §4.3), unchanged, with the request ID recorded in the
  -- Flow directory. `o₀.skipWaitFor` is read from the Step definition of the
  -- failure-free flow (spec Appendix A, assumption 20).
  | .request (.startFlow requestId stepType input) =>
    match config.run with
    | some _ => none
    | none =>
      if config.canServeCommit then
        let run : Run σ :=
          { base := Dex.Run.empty σ, attempts := [], retryTimers := [], startRequestId := requestId }
        some (config.commitRun run true none [Obs.start stepType input] (fun current =>
          { current with
            base := current.base.newStep stepType (some input)
              { skipWaitFor := (program.failureFreeFlow.stepDef stepType).waitFor.isNone }
              .flowStart }))
      else none
  -- [PUBLISH] (spec §4.6), unchanged: requires `st = Running`, else the apply
  -- rejection with NOT_FOUND, which changes nothing. Both need `up`. The
  -- rejection takes no stamp, as in the failure-free model.
  | .request (.publish chan value) =>
    if config.isUp then
      match config.run with
      | none => some ([Obs.rejectedPub chan value], config)
      | some run =>
        match run.base.status with
        | .closed _ => some ([Obs.rejectedPub chan value], config)
        | .running =>
          if config.canCommit then
            some (config.commitRun run true none [Obs.pub chan value] (fun current =>
              { current with
                base := (current.base.applyPublications [(chan, value)]).wake [chan] }))
          else none
    else none
  -- [START-RETRY] (spec §11.3): the request ID that started the run, while the
  -- Flow directory holds the run. A log entry with a stamp; no run `seq`, no
  -- event.
  | .startRetry requestId _ _ =>
    match config.run with
    | none => none
    | some run =>
      if config.canServeCommit && decide (requestId = run.startRequestId) then
        some ([], config.stampOnly)
      else none
  -- [ENGINE-REJECT] (spec §11.3): a targeted request (PublishToChannel) whose
  -- Flow ID has no run in the Flow directory. No log entry, no stamp.
  | .engineReject request =>
    match request with
    | .publish chan value =>
      if config.isUp && config.run.isNone then some ([Obs.rejectedPub chan value], config)
      else none
    | .startFlow _ _ _ => none
  -- [STEP-START], extended (spec §11.3): `ip = P`, any attempt number;
  -- "`snap := S[e].inv.snap` when it is present, else `snapshot(e, m)`".
  | .stepStart stepId isSnapshotReused =>
    if config.canServeCommit then
      match config.run with
      | none => none
      | some run =>
        match run.base.findStep? stepId, run.findAttempt? stepId with
        | some record, some attempt =>
          match record.invocation with
          | some { method := method, state := .pending } =>
            if attempt.isBackoff || !decide (attempt.method = method) ||
                isSnapshotReused != attempt.snapshot.isSome then none
            else
              let snapshot := attempt.snapshot.getD (run.base.snapshot record.options method)
              let attemptContext := attempt.context config.clock
              let taskId := config.nextTaskId
              let context : Dex.Context σ :=
                { stepExecutionId := stepId, fromStepExecutionId := record.origin }
              let task : Task σ :=
                { base :=
                    { id := taskId, step := stepId, method := method, context := context,
                      input := record.input, snapshot := snapshot,
                      conditionResults := record.conditionResults, phase := .started }
                  attemptContext := attemptContext
                  emittedCount := 0 }
              let committed := config.commitRun run (!isSnapshotReused) none [] (fun current =>
                { (current.setAttempt stepId
                    { attempt with
                      snapshot := some snapshot
                      firstAttemptTimestamp := some attemptContext.firstAttemptTimestamp }) with
                  base := current.base.updateStep stepId (fun currentRecord =>
                    { currentRecord with
                      invocation := some { method := method, state := .running taskId } }) })
              -- "The first [STEP-START] that computes a snapshot sets
              -- `par(e, m) := ⟨S[e].in, snap, S[e].loc, S[e].cr⟩` once."
              let ghost :=
                if isSnapshotReused then committed.2.ghost
                else
                  { (committed.2.ghost.updateInvocation (stepId, method) (fun history =>
                      { history with
                        arguments := some
                          { context := context, input := record.input, snapshot := snapshot,
                            conditionResults := record.conditionResults,
                            virtualTaskId := committed.2.ghost.nextVirtualTaskId } })) with
                    nextVirtualTaskId := committed.2.ghost.nextVirtualTaskId + 1 }
              some (committed.1,
                { committed.2 with
                  tasks := config.tasks ++ [task], nextTaskId := taskId + 1, ghost := ghost })
          | _ => none
        | _, _ => none
    else none
  -- [WORKER-EFFECT] or [ZOMBIE-EFFECT], first part: a `Started` task computes
  -- `(fx⃗, resp)` with its own `ctxA`. "When a task computes `(fx⃗, resp)`,
  -- `v(e, m)` becomes `Ran(fx⃗, resp)` unless it is already further."
  | .workerRun taskId isFirstRun =>
    match config.findRealTask? taskId with
    | none => none
    | some task =>
      match task.base.phase with
      | .started =>
        let isNotRun := match config.ghost.findInvocation? task.key with
          | some history => history.progress.isNotRun
          | none => false
        if isFirstRun != isNotRun then none
        else
          let result := task.base.compute (program task.attemptContext)
          let changed := config.updateRealTask taskId (fun current =>
            { current with base := { current.base with phase := .running result.1 result.2 } })
          some ([],
            { changed with
              ghost :=
                if isFirstRun then
                  changed.ghost.updateInvocation task.key (fun history =>
                    { history with progress := .ran result.1 result.2 })
                else changed.ghost })
      | _ => none
  -- [WORKER-EFFECT] or [ZOMBIE-EFFECT]: "The `j`-th effect of a task carries
  -- the ghost index `j`. The task's counter becomes `j`, and `h(e, m) :=
  -- max(h(e, m), j)`."
  | .workerEffect taskId isFirstOccurrence =>
    match config.findRealTask? taskId with
    | none => none
    | some task =>
      match task.base.phase with
      | .running (effect :: laterEffects) response =>
        let index := task.emittedCount + 1
        if isFirstOccurrence != decide (config.ghost.emittedCountOf task.key < index) then none
        else
          let changed := config.updateRealTask taskId (fun current =>
            { current with
              base := { current.base with phase := .running laterEffects response }
              emittedCount := index })
          some ([Obs.fx task.base.step task.base.method effect index],
            { changed with
              ghost :=
                if isFirstOccurrence then
                  changed.ghost.updateInvocation task.key (fun history =>
                    { history with emittedCount := index })
                else changed.ghost })
      | _ => none
  -- [WORKER-EFFECT] or [ZOMBIE-EFFECT], last part. "When a task becomes
  -- `Returned(resp)`, `v(e, m)` becomes `Returned(resp)`."
  | .workerReturn taskId isFirstReturn =>
    match config.findRealTask? taskId with
    | none => none
    | some task =>
      match task.base.phase with
      | .running [] response =>
        let isReturned := match config.ghost.findInvocation? task.key with
          | some history => history.progress.isReturned
          | none => true
        if isFirstReturn != !isReturned then none
        else
          let changed := config.updateRealTask taskId (fun current =>
            { current with base := { current.base with phase := .returned response } })
          some ([],
            { changed with
              ghost :=
                if isFirstReturn then
                  changed.ghost.updateInvocation task.key (fun history =>
                    { history with progress := .returned response })
                else changed.ghost })
      | _ => none
  -- [WORKER-ABANDON] (spec §4.4) for a task of `Θ` whose invocation is no
  -- longer current, and the drop of any task of `Z` (spec §11.3
  -- [ZOMBIE-EFFECT]). A Worker rule: no `up`, no stamp.
  | .workerAbandon taskId isLastOfCanceled =>
    match config.findLiveTask? taskId, config.findZombie? taskId with
    | some task, _ =>
      if config.isFencePassed task || isLastOfCanceled != config.isLastOfCanceled task then none
      else some ([], config.removeRealTask taskId)
    | none, some task =>
      if isLastOfCanceled != config.isLastOfCanceled task then none
      else some ([], config.removeRealTask taskId)
    | none, none => none
  -- The Complete rules, extended (spec §11.3): "The task may be in `Θ` or in
  -- `Z`. The premise is the fence of [RESULT-STALE]. The effect is that of
  -- Section 4.4. The task leaves `Θ` or `Z`."
  | .stepComplete taskId =>
    if config.canServeCommit then
      match config.run, config.findRealTask? taskId with
      | some run, some task =>
        if config.isFencePassed task then
          match task.base.method, task.base.phase with
          -- `release(e)`; `S[e].inv := ⊥`; `effects(e, E, locals)`; `register(e, W)`.
          | .waitFor, .returned (.waitFor response) =>
            some ((config.removeRealTask taskId).commitRun run true (some task.key) []
              (fun current =>
                { current with
                  base := ((current.base.updateStep task.base.step (fun record =>
                      { record with invocation := none })).applyEffects response.effects
                    ).registerWait task.base.step response.wait config.clock }))
          -- `release(e)`; `S[e].inv := ⊥`; `effects(e, E, locals)`; `decide(e, D)`.
          | .execute, .returned (.execute response) =>
            some ((config.removeRealTask taskId).commitRun run true (some task.key) []
              (fun current =>
                { current with
                  base := ((current.base.updateStep task.base.step (fun record =>
                      { record with invocation := none })).applyEffects response.effects
                    ).applyDecision task.base.step response.decision }))
          | _, _ => none
        else none
      | _, _ => none
    else none
  -- [RESULT-STALE] (spec §11.3) of the StepMethodCompleted of a returned task
  -- that fails the fence: "The outcome is `Ignored`: the command is a log
  -- entry, but it writes no run row and takes no run `seq` ... A returned
  -- Worker task whose command is ignored leaves `Θ` or `Z`."
  | .resultStale taskId isLastOfCanceled =>
    if config.canServeCommit then
      match config.findRealTask? taskId with
      | none => none
      | some task =>
        match task.base.phase with
        | .returned _ =>
          if config.isFencePassed task || isLastOfCanceled != config.isLastOfCanceled task then none
          else some ([], (config.removeRealTask taskId).stampOnly)
        | _ => none
    else none
  -- [TIMER-FIRE] with [RETRY-FIRE] (spec §11.3, OD1 option A): `D` is a
  -- non-empty prefix of the due wait and retry timers in index order; each
  -- timer of `D` is handled in order, in one commit. The command is normal
  -- when it fires a wait timer, and in the failure class otherwise.
  | .timerFire count isWaitTimerFired =>
    if config.canServeCommit then
      match config.run with
      | none => none
      | some run =>
        let due := run.dueTimers config.clock
        let fired := due.take count
        if 0 < count && decide (count ≤ due.length) &&
            isWaitTimerFired == fired.any DueTimer.isWait then
          some (config.commitRun run isWaitTimerFired none [] (fun current =>
            fired.foldl Run.fireDueTimer current))
        else none
    else none
  -- [ATTEMPT-LOST] and [ATTEMPT-EXHAUSTED] (spec §11.3). Premise: "`ip = R`
  -- with task ID `t`, or `ip = P` (a before-start failure, with the fence of
  -- [RESULT-STALE] for that kind)".
  | .attemptFailed stepId decision =>
    if config.canServeCommit then
      match config.run with
      | none => none
      | some run =>
        match run.base.findStep? stepId, run.findAttempt? stepId with
        | some record, some attempt =>
          match record.invocation with
          | some { method := method, state := .running taskId } =>
            if attempt.isBackoff || !decide (attempt.method = method) then none
            else some (config.failAttempt run stepId method attempt .attemptLost (some taskId) decision)
          | some { method := method, state := .pending } =>
            if attempt.isBackoff || !decide (attempt.method = method) then none
            else some (config.failAttempt run stepId method attempt .beforeStart none decision)
          | none => none
        | _, _ => none
    else none
  -- [ATTEMPT-RETURNED] (spec §11.3): "`ip := P`, `tid := ⊥`. The attempt
  -- number, snapshot, locks, and queue position stay ... If `t ∈ dom(Θ)`, it
  -- moves to `Z`."
  | .attemptReturned stepId =>
    if config.canServeCommit then
      match config.run with
      | none => none
      | some run =>
        match run.base.findStep? stepId, run.findAttempt? stepId with
        | some record, some attempt =>
          match record.invocation with
          | some { method := method, state := .running taskId } =>
            if attempt.isBackoff || !decide (attempt.method = method) then none
            else
              some ((config.moveToZombies taskId).commitRun run false none [] (fun current =>
                { current with
                  base := current.base.updateStep stepId (fun currentRecord =>
                    { currentRecord with
                      invocation := some { method := method, state := .pending } }) }))
          | _ => none
        | _, _ => none
    else none
  -- [ATTEMPT-HEARTBEAT] (spec §11.3): "store the checkpoint value on the
  -- invocation".
  | .attemptHeartbeat stepId value =>
    if config.canServeCommit then
      match config.run with
      | none => none
      | some run =>
        match run.base.findStep? stepId, run.findAttempt? stepId with
        | some record, some attempt =>
          match record.invocation with
          | some { method := method, state := .running _ } =>
            if attempt.isBackoff || !decide (attempt.method = method) then none
            else
              some (config.commitRun run false none [] (fun current =>
                current.setAttempt stepId { attempt with lastHeartbeatValue := some value }))
          | _ => none
        | _, _ => none
    else none
  -- [CRASH] (spec §11.3): "`up := false`; every task of `Θ` moves to `Z`, and
  -- the rest of `η` is lost." `I` is engine memory and is lost too (spec
  -- §11.2, "What survives a crash").
  | .crash =>
    if config.isUp then
      some ([], { config with
        isUp := false, tasks := [], zombies := config.zombies ++ config.tasks, interrupts := [] })
    else none
  -- [RECOVER] (spec §11.3): "`up := true`, and the partition rebuild runs":
  -- every Running invocation enters `I`.
  | .recover =>
    if config.isUp then none
    else
      some ([], { config with
        isUp := true
        interrupts := match config.run with
          | none => []
          | some run => run.runningInterrupts })
  -- [RECOVER-INTERRUPT] (spec §11.3): "remove it from `I`. If the fence of
  -- [RESULT-STALE] holds for `t`, the effect is that of [ATTEMPT-LOST] ...
  -- Otherwise the command is ignored."
  | .recoverInterrupt interrupt decision =>
    if config.canServeCommit && config.interrupts.contains interrupt then
      let removed := { config with interrupts := config.interrupts.erase interrupt }
      match config.run with
      | none => some ([], removed.stampOnly)
      | some run =>
        match run.base.findStep? interrupt.step, run.findAttempt? interrupt.step with
        | some record, some attempt =>
          if decide (record.invocation =
                some { method := interrupt.method, state := .running interrupt.taskId }) &&
              decide (attempt.method = interrupt.method) && !attempt.isBackoff &&
              decide (attempt.number = interrupt.attempt) then
            some (removed.failAttempt run interrupt.step interrupt.method attempt .interrupted
              (some interrupt.taskId) decision)
          else some ([], removed.stampOnly)
        | _, _ => some ([], removed.stampOnly)
    else none
  -- A log commit that changes no modeled state (spec §11.2).
  | .internalCommit =>
    if config.canServeCommit then some ([], config.stampOnly) else none

end DexExpressiveness.DexF
