import DexExpressiveness.Failure.Relation
import DexExpressiveness.Translation.Projection

/-!
# Decidable equality of configurations, for the executable checks

Test tooling, not part of the theorem. The executable checks of
`Failure/Check.lean` compare `α(next)` with the Dex configuration that the
transition map reaches, so they need decidable equality of Dex
configurations. The Dex types are generic in the signature, and `deriving`
does not add instance arguments for `σ.Val`, `σ.Fx`, and `σ.MapSelection`,
so the instances are written here: a structure through the tuple of its
fields, an inductive type through an injective map into a sum.
-/

namespace DexExpressiveness.Translation

deriving instance DecidableEq for DexVal

end DexExpressiveness.Translation

namespace DexExpressiveness.Dex

set_option synthInstance.maxSize 4096

variable {σ : Signature} [DecidableEq σ.Val] [DecidableEq σ.Fx] [DecidableEq σ.MapSelection]

/-- The constructor and fields of a creating Step execution. -/
def Origin.encode : Origin σ → Option (StepExecId σ)
  | .flowStart => none
  | .step creator => some creator

instance : DecidableEq (Origin σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [Origin.encode])

instance : DecidableEq (Context σ) := fun first second =>
  decidable_of_iff
    ((first.stepExecutionId, first.fromStepExecutionId) =
      (second.stepExecutionId, second.fromStepExecutionId))
    (by cases first; cases second; simp)

instance : DecidableEq (Snapshot σ) := fun first second =>
  decidable_of_iff (first.attributes = second.attributes) (by cases first; cases second; simp)

instance : DecidableEq (Effects σ) := fun first second =>
  decidable_of_iff ((first.writes, first.publications) = (second.writes, second.publications))
    (by cases first; cases second; simp)

instance : DecidableEq (Wait σ) := fun first second =>
  decidable_of_iff
    ((first.kind, first.timerSeconds, first.channels) =
      (second.kind, second.timerSeconds, second.channels))
    (by cases first; cases second; simp)

instance : DecidableEq (StepOptions σ) := fun first second =>
  decidable_of_iff
    ((first.skipWaitFor, first.waitForLoadMaps, first.executeLoadMaps) =
      (second.skipWaitFor, second.waitForLoadMaps, second.executeLoadMaps))
    (by cases first; cases second; simp)

instance : DecidableEq (Move σ) := fun first second =>
  decidable_of_iff
    ((first.stepType, first.input, first.options) = (second.stepType, second.input, second.options))
    (by cases first; cases second; simp)

/-- The constructor and fields of a close decision. -/
def Close.encode : Close σ → Option σ.Val ⊕ String ⊕ Unit
  | .forceComplete output => .inl output
  | .forceFail message => .inr (.inl message)
  | .deadEnd => .inr (.inr ())

instance : DecidableEq (Close σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [Close.encode])

instance : DecidableEq (Decision σ) := fun first second =>
  decidable_of_iff ((first.moves, first.close) = (second.moves, second.close))
    (by cases first; cases second; simp)

instance : DecidableEq (ConditionResults σ) := fun first second =>
  decidable_of_iff
    ((first.timersDone, first.channelMessages) = (second.timersDone, second.channelMessages))
    (by cases first; cases second; simp)

instance : DecidableEq (WaitForResponse σ) := fun first second =>
  decidable_of_iff ((first.effects, first.wait) = (second.effects, second.wait))
    (by cases first; cases second; simp)

instance : DecidableEq (ExecuteResponse σ) := fun first second =>
  decidable_of_iff ((first.effects, first.decision) = (second.effects, second.decision))
    (by cases first; cases second; simp)

instance : DecidableEq (WaitState σ) := fun first second =>
  decidable_of_iff
    ((first.wait, first.fireAt, first.timersDone, first.registeredSeq) =
      (second.wait, second.fireAt, second.timersDone, second.registeredSeq))
    (by cases first; cases second; simp)

instance : DecidableEq (StepRecord σ) := fun first second =>
  decidable_of_iff
    ((first.id, first.input, first.options, first.origin, first.phase, first.invocation,
        first.waitState, first.conditionResults) =
      (second.id, second.input, second.options, second.origin, second.phase, second.invocation,
        second.waitState, second.conditionResults))
    (by cases first; cases second; simp)

instance : DecidableEq (Run σ) := fun first second =>
  decidable_of_iff
    ((first.status, first.outputs, first.steps, first.counters, first.attributes, first.channels,
        first.timers, first.seq) =
      (second.status, second.outputs, second.steps, second.counters, second.attributes,
        second.channels, second.timers, second.seq))
    (by cases first; cases second; simp)

/-- The constructor and fields of a response. -/
def Response.encode : Response σ → WaitForResponse σ ⊕ ExecuteResponse σ
  | .waitFor response => .inl response
  | .execute response => .inr response

instance : DecidableEq (Response σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [Response.encode])

/-- The constructor and fields of a task phase. -/
def TaskPhase.encode : TaskPhase σ → Unit ⊕ (List σ.Fx × Response σ) ⊕ Response σ
  | .started => .inl ()
  | .running effects response => .inr (.inl (effects, response))
  | .returned response => .inr (.inr response)

instance : DecidableEq (TaskPhase σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [TaskPhase.encode])

instance : DecidableEq (Task σ) := fun first second =>
  decidable_of_iff
    ((first.id, first.step, first.method, first.context, first.input, first.snapshot,
        first.conditionResults, first.phase) =
      (second.id, second.step, second.method, second.context, second.input, second.snapshot,
        second.conditionResults, second.phase))
    (by cases first; cases second; simp)

instance : DecidableEq (Config σ) := fun first second =>
  decidable_of_iff
    ((first.clock, first.lastCommitAt, first.run, first.tasks, first.nextTaskId) =
      (second.clock, second.lastCommitAt, second.run, second.tasks, second.nextTaskId))
    (by cases first; cases second; simp)

/-- The constructor and fields of an event. -/
def Obs.encode : Obs σ →
    (σ.StepType × σ.Val) ⊕ (σ.Chan × σ.Val) ⊕ (σ.Chan × σ.Val) ⊕
      (StepExecId σ × Method × σ.Fx) ⊕ (CloseStatus × List (StepExecId σ × σ.Val))
  | .start stepType input => .inl (stepType, input)
  | .pub chan value => .inr (.inl (chan, value))
  | .rejectedPub chan value => .inr (.inr (.inl (chan, value)))
  | .fx step method effect => .inr (.inr (.inr (.inl (step, method, effect))))
  | .closed status outputs => .inr (.inr (.inr (.inr (status, outputs))))

instance : DecidableEq (Obs σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [Obs.encode])

/-- The constructor and fields of a request. -/
def Request.encode : Request σ → (σ.StepType × σ.Val) ⊕ (σ.Chan × σ.Val)
  | .startFlow stepType input => .inl (stepType, input)
  | .publish chan value => .inr (chan, value)

instance : DecidableEq (Request σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [Request.encode])

end DexExpressiveness.Dex

namespace DexExpressiveness.DexF

set_option synthInstance.maxSize 4096

variable {σ : Dex.Signature} [DecidableEq σ.Val] [DecidableEq σ.Fx] [DecidableEq σ.MapSelection]

/-- The constructor and fields of a DexF event. -/
def Obs.encode : Obs σ →
    (σ.StepType × σ.Val) ⊕ (σ.Chan × σ.Val) ⊕ (σ.Chan × σ.Val) ⊕
      (Dex.StepExecId σ × Dex.Method × σ.Fx × Nat) ⊕
      (Dex.CloseStatus × List (Dex.StepExecId σ × σ.Val))
  | .start stepType input => .inl (stepType, input)
  | .pub chan value => .inr (.inl (chan, value))
  | .rejectedPub chan value => .inr (.inr (.inl (chan, value)))
  | .fx step method effect index => .inr (.inr (.inr (.inl (step, method, effect, index))))
  | .closed status outputs => .inr (.inr (.inr (.inr (status, outputs))))

instance : DecidableEq (Obs σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [Obs.encode])

/-- The constructor and fields of a DexF request. -/
def Request.encode : Request σ → (Nat × σ.StepType × σ.Val) ⊕ (σ.Chan × σ.Val)
  | .startFlow requestId stepType input => .inl (requestId, stepType, input)
  | .publish chan value => .inr (chan, value)

instance : DecidableEq (Request σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [Request.encode])

/-- The constructor and fields of a virtual progress. -/
def VirtualProgress.encode : VirtualProgress σ →
    Unit ⊕ (List σ.Fx × Dex.Response σ) ⊕ Dex.Response σ
  | .notRun => .inl ()
  | .ran effects response => .inr (.inl (effects, response))
  | .returned response => .inr (.inr response)

instance : DecidableEq (VirtualProgress σ) := fun first second =>
  decidable_of_iff (first.encode = second.encode)
    (by cases first <;> cases second <;> simp [VirtualProgress.encode])

instance : DecidableEq (AttemptContext σ) := fun first second =>
  decidable_of_iff
    ((first.attempt, first.firstAttemptTimestamp, first.recoveryError, first.lastHeartbeatValue) =
      (second.attempt, second.firstAttemptTimestamp, second.recoveryError,
        second.lastHeartbeatValue))
    (by cases first; cases second; simp)

instance : DecidableEq (Attempt σ) := fun first second =>
  decidable_of_iff
    ((first.method, first.isBackoff, first.number, first.snapshot, first.firstAttemptTimestamp,
        first.lastFailure, first.lastHeartbeatValue) =
      (second.method, second.isBackoff, second.number, second.snapshot,
        second.firstAttemptTimestamp, second.lastFailure, second.lastHeartbeatValue))
    (by cases first; cases second; simp)

instance : DecidableEq (Run σ) := fun first second =>
  decidable_of_iff
    ((first.base, first.attempts, first.retryTimers, first.startRequestId) =
      (second.base, second.attempts, second.retryTimers, second.startRequestId))
    (by cases first; cases second; simp)

instance : DecidableEq (Task σ) := fun first second =>
  decidable_of_iff
    ((first.base, first.attemptContext, first.emittedCount) =
      (second.base, second.attemptContext, second.emittedCount))
    (by cases first; cases second; simp)

instance : DecidableEq (Arguments σ) := fun first second =>
  decidable_of_iff
    ((first.context, first.input, first.snapshot, first.conditionResults, first.virtualTaskId) =
      (second.context, second.input, second.snapshot, second.conditionResults,
        second.virtualTaskId))
    (by cases first; cases second; simp)

instance : DecidableEq (InvocationHistory σ) := fun first second =>
  decidable_of_iff
    ((first.pendedAt, first.emittedCount, first.progress, first.arguments, first.outcome) =
      (second.pendedAt, second.emittedCount, second.progress, second.arguments, second.outcome))
    (by cases first; cases second; simp)

instance : DecidableEq (Ghost σ) := fun first second =>
  decidable_of_iff
    ((first.invocations, first.normalCount, first.normalIndex, first.lastNormalCommitAt,
        first.nextVirtualTaskId) =
      (second.invocations, second.normalCount, second.normalIndex, second.lastNormalCommitAt,
        second.nextVirtualTaskId))
    (by cases first; cases second; simp)

instance : DecidableEq (Config σ) := fun first second =>
  decidable_of_iff
    ((first.clock, first.lastCommitAt, first.run, first.tasks, first.zombies, first.nextTaskId,
        first.interrupts, first.isUp, first.ghost) =
      (second.clock, second.lastCommitAt, second.run, second.tasks, second.zombies,
        second.nextTaskId, second.interrupts, second.isUp, second.ghost))
    (by cases first; cases second; simp)

end DexExpressiveness.DexF
