import DexExpressiveness.Failure.Statements.Leaf

/-!
# WP-F6: facts about α for the liveness proof

What the Dex configuration `abstractConfig config` says about the DexF
configuration `config`: its initial value, closedness, clock, pending
invocations, virtual tasks, and wait timers; and that a closed Dex run stays
closed.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- α maps the initial DexF configuration to the initial Dex configuration. -/
theorem wpf6_abstractConfig_initial : abstractConfig (Config.initial σ) = Dex.Config.initial σ := by
  sorry

/-- α keeps whether the program is closed. -/
theorem wpf6_isClosed_abstract (program : Program σ) (config : Config σ) :
    (Dex.system program.failureFreeFlow).isClosed (abstractConfig config) =
      (system program).isClosed config := by
  sorry

/-- A real task found by its ID is exactly a real task with that ID, since
real task IDs are distinct. -/
theorem wpf6_findRealTask?_iff (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (task : Task σ) :
    config.findRealTask? taskId = some task ↔ task ∈ config.realTasks ∧ task.base.id = taskId := by
  sorry

/-- A pending invocation under α is a pending invocation of the DexF run, and
its history is live. -/
theorem wpf6_pending_of_abstract (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ) (method : Dex.Method)
    (dexRun : Dex.Run σ) (dexRecord : Dex.StepRecord σ)
    (hasRun : (abstractConfig config).run = some dexRun)
    (isFound : dexRun.findStep? stepId = some dexRecord)
    (isPending : dexRecord.invocation = some { method := method, state := .pending }) :
    ∃ run record history, config.run = some run ∧ run.base.findStep? stepId = some record ∧
      record.invocation = some { method := method, state := .pending } ∧
      config.ghost.findInvocation? (stepId, method) = some history ∧ history.outcome = .live := by
  sorry

/-- A task under α is the virtual task of a ghost history that α keeps. -/
theorem wpf6_virtualTask_of_abstract (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (virtualTaskId : Nat) (dexTask : Dex.Task σ)
    (isFound : (abstractConfig config).findTask? virtualTaskId = some dexTask) :
    ∃ key history arguments, config.ghost.findInvocation? key = some history ∧
      history.arguments = some arguments ∧ arguments.virtualTaskId = virtualTaskId ∧
      config.hasVirtualTask key history = true := by
  sorry

/-- α keeps the wait timers. -/
theorem wpf6_timers_abstract (config : Config σ) (dexRun : Dex.Run σ)
    (hasRun : (abstractConfig config).run = some dexRun) :
    ∃ run, config.run = some run ∧ dexRun.timers = run.base.timers := by
  sorry

/-- A Dex step keeps a closed program closed. -/
theorem wpf6_dex_isClosed_step (flow : Dex.FlowDef σ) (config : Dex.Config σ)
    (action : Dex.Action σ) (events : List (Dex.Obs σ)) (next : Dex.Config σ)
    (isStep : Dex.step flow config action = some (events, next))
    (isClosed : (Dex.system flow).isClosed config = true) :
    (Dex.system flow).isClosed next = true := by
  sorry

end DexExpressiveness.DexF
