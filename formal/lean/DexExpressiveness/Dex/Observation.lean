import DexExpressiveness.Core.TransitionSystem
import DexExpressiveness.Dex.Rules

/-!
# Dex core: the system, the admissible environment, and fairness (spec §5.2 to §5.5)
-/

namespace DexExpressiveness.Dex

variable {σ : Signature}

/-- The restricted Dex system of one Flow definition as a transition system
(spec §5.2). A request label is a client request commit, accepted or rejected
(spec §5.3). Every label except [TICK] is a progress step (spec §5.5). The
program is closed when the root Flow's run is closed. -/
def system (flow : FlowDef σ) : TransitionSystem (Config σ) (Action σ) (Obs σ) (Request σ) :=
  { init := Config.initial σ
    step := step flow
    requestOf := fun action =>
      match action with
      | .request request => some request
      | _ => none
    isProgress := fun action =>
      match action with
      | .tick _ => false
      | _ => true
    isClosed := fun config =>
      match config.run with
      | none => false
      | some run =>
        match run.status with
        | .running => false
        | .closed _ => true }

/-- The data that fixes the admissible environment `E_adm` of a program (spec
§5.3): its entry Step, which start inputs are program inputs, and which
publications are external input. -/
structure Environment (σ : Signature) where
  /-- "the program's entry Step `T₀`". -/
  entryStep : σ.StepType
  /-- Whether a value is "the L program's input `v`" in Dex form. -/
  IsProgramInput : σ.Val → Prop
  /-- Whether `(c, v)` is one message for one of "the program's external Channels". -/
  IsExternalMessage : σ.Chan → σ.Val → Prop

namespace Environment

/-- An admissible input word (spec §5.3): it "begins with exactly one
StartFlow request `start(f, T₀, v, ∅)`", and "every later request is a
PublishToChannel request `pub(f, [(c, v)])` where `c` is one of the program's
external Channels". The other bullets of spec §5.3 hold by construction: the
request type has no `set`, `del`, `skip`, `stop`, or `call`, and it has no
request IDs. -/
def IsAdmissibleWord (environment : Environment σ) (word : Sequence (Request σ)) : Prop :=
  (∃ input, environment.IsProgramInput input ∧
    word 0 = some (.startFlow environment.entryStep input)) ∧
  (∀ position request, word (position + 1) = some request →
    ∃ chan value, request = .publish chan value ∧ environment.IsExternalMessage chan value) ∧
  (∀ position, word position = none → word (position + 1) = none)

/-- A finite prefix of an admissible input word: the requests a finite run
"whose inputs are drawn from `E_adm`" has attempted (spec §5.4). -/
def IsAdmissiblePrefix (environment : Environment σ) (requests : List (Request σ)) : Prop :=
  requests = [] ∨
  ∃ input laterRequests,
    requests = .startFlow environment.entryStep input :: laterRequests ∧
    environment.IsProgramInput input ∧
    ∀ request, request ∈ laterRequests →
      ∃ chan value, request = .publish chan value ∧ environment.IsExternalMessage chan value

end Environment

/-! ## Fairness (spec §5.5)

"A run for `w` is fair when every condition below holds. [assumption] These
are the fairness assumptions the theorem needs; the rules themselves are
unfair." F0 is `TransitionSystem.RequestsArrive`. -/

namespace Fairness

variable (run : InfiniteRun (Config σ) (Action σ))

/-- F1, time diverges: "every partition clock exceeds every bound". -/
def TimeDiverges : Prop :=
  ∀ bound, ∃ index, bound < (run.states index).clock

/-- The timer is armed and due before transition `index`. -/
def IsTimerArmedAndDue (timer : TimerId σ) (index : Nat) : Prop :=
  ∃ flowRun fireTime,
    (run.states index).run = some flowRun ∧
    flowRun.timers.get? timer = some fireTime ∧
    fireTime ≤ (run.states index).clock

/-- F2, timers fire: "A timer that stays armed and due is eventually fired by
[TIMER-FIRE]." A timer leaves `Tm` only by firing or by `unregister`. -/
def TimersFire : Prop :=
  ∀ timer : TimerId σ, EventuallyFails (IsTimerArmedAndDue run timer)

/-- The invocation of method `method` of the Step is pending before transition `index`. -/
def IsInvocationPending (stepId : StepExecId σ) (method : Method) (index : Nat) : Prop :=
  ∃ flowRun record,
    (run.states index).run = some flowRun ∧
    flowRun.findStep? stepId = some record ∧
    record.invocation = some { method := method, state := .pending }

/-- F3, Steps start: "A pending invocation that stays startable eventually
starts." Without locks every pending invocation is startable, and it stays
pending "until it starts or is canceled". -/
def StepsStart : Prop :=
  ∀ (stepId : StepExecId σ) (method : Method),
    EventuallyFails (IsInvocationPending run stepId method)

/-- The task is in `Θ` and has not returned before transition `index`. -/
def IsTaskUnsettled (taskId : Nat) (index : Nat) : Prop :=
  ∃ task,
    (run.states index).findTask? taskId = some task ∧
    ∀ response, task.phase ≠ .returned response

/-- F4, Workers settle: every task "emits all its effects and returns", or, for
a canceled invocation, "is dropped by [WORKER-ABANDON] after some prefix of
its effects". -/
def WorkersSettle : Prop :=
  ∀ taskId, EventuallyFails (IsTaskUnsettled run taskId)

/-- The task is in `Θ` in phase `Returned` before transition `index`. -/
def IsTaskReturned (taskId : Nat) (index : Nat) : Prop :=
  ∃ task response,
    (run.states index).findTask? taskId = some task ∧ task.phase = .returned response

/-- F5, results commit: "Every `Returned` task is eventually completed by a
Complete rule or dropped by [STEP-COMPLETE-STALE]." -/
def ResultsCommit : Prop :=
  ∀ taskId, EventuallyFails (IsTaskReturned run taskId)

/-- F6, messages move: outbox delivery, [PENDING-CLOSE-CHECK], and
[RPC-FINISH]. The restricted system has none of them, so F6 holds trivially. -/
def MessagesMove : Prop := True

end Fairness

/-- A fair run: F1 to F6 (spec §5.5). F0 is part of
`TransitionSystem.IsFairMaximalTrace`. -/
def IsFair (run : InfiniteRun (Config σ) (Action σ)) : Prop :=
  Fairness.TimeDiverges run ∧ Fairness.TimersFire run ∧ Fairness.StepsStart run ∧
  Fairness.WorkersSettle run ∧ Fairness.ResultsCommit run ∧ Fairness.MessagesMove

end DexExpressiveness.Dex
