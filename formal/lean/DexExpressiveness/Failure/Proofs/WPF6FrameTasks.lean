import DexExpressiveness.Failure.Observation

/-!
# WP-F6: one-step frame lemmas of DexF, clock, run, availability, and tasks

What one DexF step (`step`) can change: the clock, the run's existence,
`up`, `I`, the real tasks, `Θ`, the ghost histories, the next task ID, and
the number of `fx` events. `liveness_failure` uses them to follow a fair run
past its last failure.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- Only a tick moves the clock, and it moves it forward. -/
theorem wpf6_clock_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    config.clock ≤ next.clock ∧
      ((∀ duration, action ≠ .tick duration) → next.clock = config.clock) := by
  sorry

/-- A started run stays started; only [FLOW-START] starts it. -/
theorem wpf6_run_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    (config.run.isSome = true → next.run.isSome = true) ∧
    (config.run = none → next.run = none ∨
      ∃ requestId stepType input, action = .request (.startFlow requestId stepType input)) := by
  sorry

/-- Only [CRASH] and [RECOVER] change `up`; [RECOVER] needs the server down. -/
theorem wpf6_isUp_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    (action ≠ .crash → action ≠ .recover → next.isUp = config.isUp) ∧
    (action = .recover → config.isUp = false ∧ next.isUp = true) := by
  sorry

/-- Only [RECOVER] adds interrupts. -/
theorem wpf6_interrupts_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next))
    (isNotRecover : action ≠ .recover) :
    ∀ interrupt, interrupt ∈ next.interrupts → interrupt ∈ config.interrupts := by
  sorry

/-- The next task ID never decreases. -/
theorem wpf6_nextTaskId_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    config.nextTaskId ≤ next.nextTaskId := by
  sorry

/-- A real task after a step was a real task before, with the same ID and
invocation, and a `Returned` one keeps its phase; or it is the task that
[STEP-START] just minted, of a live invocation. -/
theorem wpf6_realTask_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (nextTask : Task σ) (isMember : nextTask ∈ next.realTasks) :
    (∃ task, task ∈ config.realTasks ∧ task.base.id = nextTask.base.id ∧
      task.key = nextTask.key ∧
      ∀ response, task.base.phase = .returned response →
        nextTask.base.phase = .returned response) ∨
    (nextTask.base.id = config.nextTaskId ∧
      ∃ history, config.ghost.findInvocation? nextTask.key = some history ∧
        history.outcome = .live) := by
  sorry

/-- A task of `Θ` stays in `Θ`, with its invocation, unless the step removes
it by its ID or is a failure. -/
theorem wpf6_liveTask_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (task : Task σ) (isMember : task ∈ config.tasks)
    (isNotComplete : action ≠ .stepComplete task.base.id)
    (isNotAbandon : ∀ isLastOfCanceled, action ≠ .workerAbandon task.base.id isLastOfCanceled)
    (isNotStale : ∀ isLastOfCanceled, action ≠ .resultStale task.base.id isLastOfCanceled)
    (isNotFailure : Fairness.Action.isCountedFailure action = false)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision) :
    ∃ nextTask, nextTask ∈ next.tasks ∧ nextTask.base.id = task.base.id ∧
      nextTask.key = task.key := by
  sorry

/-- A task of `Θ` that passes the fence cannot be abandoned. -/
theorem wpf6_workerAbandon_disabled (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (task : Task σ) (isMember : task ∈ config.tasks)
    (isPassed : config.isFencePassed task = true) (isLastOfCanceled : Bool) :
    step program config (.workerAbandon task.base.id isLastOfCanceled) = none := by
  sorry

/-- The result of a task of `Θ` that passes the fence is not stale. -/
theorem wpf6_resultStale_disabled (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (task : Task σ) (isMember : task ∈ config.tasks)
    (isPassed : config.isFencePassed task = true) (isLastOfCanceled : Bool) :
    step program config (.resultStale task.base.id isLastOfCanceled) = none := by
  sorry

/-- A ghost history stays: an ended invocation keeps its outcome, and `par`
once set never changes. -/
theorem wpf6_history_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history) :
    ∃ nextHistory, next.ghost.findInvocation? key = some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      (∀ arguments, history.arguments = some arguments →
        nextHistory.arguments = some arguments) := by
  sorry

/-- A step emits at most one `fx` event: either at most one event, or no `fx`. -/
theorem wpf6_events_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    events.length ≤ 1 ∨
      ∀ stepId method effect index, Obs.fx stepId method effect index ∉ events := by
  sorry

end DexExpressiveness.DexF
