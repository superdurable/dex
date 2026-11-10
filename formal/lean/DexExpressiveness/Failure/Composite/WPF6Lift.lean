import DexExpressiveness.Failure.Composite.WPF6Abstraction

/-!
# WP-F6: the DexF step with no failure that follows a Dex step

The backward inclusion of `liveness_failure` follows a fair Dex run step by
step with a DexF run that has no failure. `NoFailure` is what such a DexF
configuration looks like: the server is up, nothing is in `Z`, `I`, or the
retry timers, no invocation is in Backoff or holds a retained snapshot, and
each invocation has at most one real task, whose ID, phase, and effect count
are those of its virtual task. On such a configuration α is transparent, and
every Dex step lifts to one DexF step (`liftAction`).
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- A DexF configuration that no failure reached. -/
structure NoFailure (program : Program σ) (config : Config σ) : Prop where
  /-- The server is up. -/
  isUp : config.isUp = true
  /-- No zombie task. -/
  noZombies : config.zombies = []
  /-- No interrupted attempt. -/
  noInterrupts : config.interrupts = []
  /-- No retry timer. -/
  noRetryTimers : ∀ run, config.run = some run → run.retryTimers = []
  /-- Every log commit is a normal commit. -/
  lastCommitIsNormal : config.lastCommitAt = config.ghost.lastNormalCommitAt
  /-- Real and virtual task IDs are minted together. -/
  nextTaskIdIsVirtual : config.nextTaskId = config.ghost.nextVirtualTaskId
  /-- No invocation is in Backoff. -/
  noBackoff : ∀ run stepId attempt, config.run = some run →
    run.findAttempt? stepId = some attempt → attempt.isBackoff = false
  /-- A pending invocation holds no snapshot. -/
  pendingHasNoSnapshot : ∀ run record method attempt, config.run = some run →
    record ∈ run.base.steps → record.invocation = some { method := method, state := .pending } →
    run.findAttempt? record.id = some attempt → attempt.snapshot = none
  /-- Each real task is its invocation's virtual task: the same ID, the same
  number of emitted effects, and the same phase. -/
  tasksMatchHistories : ∀ task, task ∈ config.realTasks →
    ∃ history arguments, config.ghost.findInvocation? task.key = some history ∧
      history.arguments = some arguments ∧ arguments.virtualTaskId = task.base.id ∧
      task.emittedCount = history.emittedCount ∧ task.base.phase = history.virtualPhase
  /-- No real task belongs to a committed invocation. -/
  tasksNotCommitted : ∀ task history, task ∈ config.realTasks →
    config.ghost.findInvocation? task.key = some history → history.outcome ≠ .committed
  /-- An invocation has at most one real task. -/
  oneTaskPerKey : ∀ first second, first ∈ config.realTasks → second ∈ config.realTasks →
    first.key = second.key → first.base.id = second.base.id
  /-- A live invocation that has started has its real task in `Θ`. -/
  liveHasTask : ∀ key history arguments, config.ghost.findInvocation? key = some history →
    history.outcome = .live → history.arguments = some arguments →
    ∃ task, task ∈ config.tasks ∧ task.key = key

/-- The DexF request of a Dex request: a StartFlow gets the request ID of the
start. -/
def liftRequest (startRequestId : Nat) : Dex.Request σ → Request σ
  | .startFlow stepType input => .startFlow startRequestId stepType input
  | .publish chan value => .publish chan value

/-- The DexF label with no failure that follows a Dex label. A stale result
is dropped by [WORKER-ABANDON], which takes no stamp. -/
def liftAction (startRequestId : Nat) : Dex.Action σ → Action σ
  | .tick duration => .tick duration
  | .request request => .request (liftRequest startRequestId request)
  | .stepStart stepId => .stepStart stepId false
  | .workerRun taskId => .workerRun taskId true
  | .workerEffect taskId => .workerEffect taskId true
  | .workerReturn taskId => .workerReturn taskId true
  | .workerAbandon taskId => .workerAbandon taskId true
  | .stepComplete taskId => .stepComplete taskId
  | .stepCompleteStale taskId => .workerAbandon taskId true
  | .timerFire count => .timerFire count true

/-- The initial configuration has no failure. -/
theorem wpf6_noFailure_initial (program : Program σ) : NoFailure program (Config.initial σ) := by
  sorry

/-- **The lift.** On a configuration with no failure, every Dex step of α
lifts to a DexF step with no failure: the lifted label is enabled, its
events are the Dex events with ghost indexes, none of them is a repeated
`fx`, α of the result is the Dex result, and the result has no failure. -/
theorem wpf6_lift_step (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (config : Config σ) (invariant : Invariant program config)
    (noFailure : NoFailure program config) (dexAction : Dex.Action σ)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) dexAction =
      some (dexEvents, dexNext)) :
    ∃ events next, step program config (liftAction startRequestId dexAction) = some (events, next) ∧
      abstractConfig next = dexNext ∧ dexEvents = events.map Obs.toDex ∧
      eraseStepEvents config events = events.map Obs.toDex ∧ NoFailure program next := by
  sorry

/-- With no failure, a pending invocation of the DexF run is pending under α. -/
theorem wpf6_abstract_pending_of_noFailure (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (run : Run σ) (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ) (method : Dex.Method)
    (hasRun : config.run = some run) (isFound : run.base.findStep? stepId = some record)
    (isPending : record.invocation = some { method := method, state := .pending }) :
    ∃ dexRun dexRecord, (abstractConfig config).run = some dexRun ∧
      dexRun.findStep? stepId = some dexRecord ∧
      dexRecord.invocation = some { method := method, state := .pending } := by
  sorry

/-- With no failure, a real task is the task of α with the same ID and phase. -/
theorem wpf6_abstract_task_of_noFailure (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (taskId : Nat) (task : Task σ) (isFound : config.findRealTask? taskId = some task) :
    ∃ dexTask, (abstractConfig config).findTask? taskId = some dexTask ∧
      dexTask.phase = task.base.phase := by
  sorry

/-- D6 holds by the premise of the timer rule: every timer among the first
`count` due timers is due. -/
theorem wpf6_firedTimers_due (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (run : Run σ) (hasRun : config.run = some run)
    (count : Nat) (timer : DueTimer σ) (fireTime : Nat)
    (isFired : timer ∈ (run.dueTimers config.clock).take count)
    (isArmed : run.fireTimeOf? timer = some fireTime) : fireTime ≤ config.clock := by
  sorry

end DexExpressiveness.DexF
