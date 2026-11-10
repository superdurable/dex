import DexExpressiveness.Dex.MetaFunctions

/-!
# Dex core: the transition rules (spec §4.3 to §4.6) and the event alphabet (spec §5.1)

The rules of the restricted system: [FLOW-START], [STEP-START],
[WORKER-EFFECT], [WORKER-ABANDON], [STEP-COMPLETE-WAITFOR],
[STEP-COMPLETE-EXECUTE], [STEP-COMPLETE-STALE], [TICK], [TIMER-FIRE],
[PUBLISH], and the rejected publication. The rules for the inputs that spec
§5.3 excludes ([SET-ATTRIBUTES], [DELETE-MESSAGE], [STOP], [SKIP-TIMER]), the
RPC rules (spec §4.7), the SubFlow rules (spec §4.8), and
[PENDING-CLOSE-CHECK] (spec §4.9) are not modeled.

`step` is a function of the configuration and the action. The action names the
rule and carries its nondeterministic choice.
-/

namespace DexExpressiveness.Dex

variable {σ : Signature}

/-- The Dex event alphabet `Obs` (spec §5.1), restricted to the events the
modeled rules produce. The Flow ID is elided: there is one Flow. -/
inductive Obs (σ : Signature)
  /-- `start(f, T, v, ∅)`, produced by [FLOW-START]. -/
  | start (stepType : σ.StepType) (input : σ.Val)
  /-- `pub(f, [(c, v)])`, produced by [PUBLISH]. -/
  | pub (chan : σ.Chan) (value : σ.Val)
  /-- `rejected(pub(f, [(c, v)]), NOT_FOUND)`: a publication to a Flow that is
  not running (spec §4.6, §5.3). -/
  | rejectedPub (chan : σ.Chan) (value : σ.Val)
  /-- `fx(f, (e, m), a)`, produced by [WORKER-EFFECT]. -/
  | fx (step : StepExecId σ) (method : Method) (effect : σ.Fx)
  /-- `closed(f, status, error, outs)`, produced by `record` (spec §4.2). The
  error is inside `CloseStatus.failed`. -/
  | closed (status : CloseStatus) (outputs : List (StepExecId σ × σ.Val))

/-- A client request (spec §5.3: "attempted client requests"). Only StartFlow
and PublishToChannel with one message are admissible. -/
inductive Request (σ : Signature)
  /-- `start(f, T₀, v, ∅)`: no initial Attributes, no Flow timeout. -/
  | startFlow (stepType : σ.StepType) (input : σ.Val)
  /-- `pub(f, [(c, v)])`. -/
  | publish (chan : σ.Chan) (value : σ.Val)

/-- One transition of the system, with the nondeterministic choice of its rule. -/
inductive Action (σ : Signature)
  /-- [TICK]: the clock advances by `duration > 0` microseconds. -/
  | tick (duration : Nat)
  /-- A client request commits: [FLOW-START], [PUBLISH], or a rejection. -/
  | request (request : Request σ)
  /-- [STEP-START] of the pending invocation of this Step. -/
  | stepStart (step : StepExecId σ)
  /-- [WORKER-EFFECT], first part: a `Started` task runs its handler and
  becomes `Running(fx⃗, resp)`. No event. -/
  | workerRun (taskId : Nat)
  /-- [WORKER-EFFECT]: a `Running` task emits the head of its effects. -/
  | workerEffect (taskId : Nat)
  /-- [WORKER-EFFECT], last part: a `Running` task with no effect left becomes
  `Returned(resp)`. No event. -/
  | workerReturn (taskId : Nat)
  /-- [WORKER-ABANDON]: a task of a canceled invocation is dropped. -/
  | workerAbandon (taskId : Nat)
  /-- [STEP-COMPLETE-WAITFOR] or [STEP-COMPLETE-EXECUTE] of a `Returned` task. -/
  | stepComplete (taskId : Nat)
  /-- [STEP-COMPLETE-STALE]: a `Returned` task of an invocation that is no longer running is dropped. -/
  | stepCompleteStale (taskId : Nat)
  /-- [TIMER-FIRE] of the first `count ≥ 1` due timers in index order. -/
  | timerFire (count : Nat)

namespace Config

/-- A commit may stamp `now = t_p` only when the clock is strictly above the
last commit's timestamp (spec §4.1; spec Appendix A, assumption 5). -/
def canCommit (config : Config σ) : Bool :=
  match config.lastCommitAt with
  | none => true
  | some last => decide (last < config.clock)

/-- The `closed` event of `record(κ)` (spec §4.2): "emit `closed(f, κ.status,
κ.error, outs)`", emitted by the commit in which the run closes. -/
def closeEvents (before after : Run σ) : List (Obs σ) :=
  match before.status, after.status with
  | .running, .closed status => [Obs.closed status after.outputs]
  | _, _ => []

/-- One commit of the run (spec §4.1): atomic, stamps `now = t_p`, takes
`seq := seq + 1`, then applies `change`. The events are the input event, if
any, followed by the `closed` event when the commit closes the run. -/
def commit (config : Config σ) (run : Run σ) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) : List (Obs σ) × Config σ :=
  let after := change { run with seq := run.seq + 1 }
  (inputEvents ++ closeEvents run after,
    { config with run := some after, lastCommitAt := some config.clock })

/-- `Θ[taskId]`. -/
def findTask? (config : Config σ) (taskId : Nat) : Option (Task σ) :=
  config.tasks.find? (fun task => decide (task.id = taskId))

/-- `Θ[taskId] := change(Θ[taskId])`. -/
def updateTask (config : Config σ) (taskId : Nat) (change : Task σ → Task σ) : Config σ :=
  { config with tasks := config.tasks.map (fun task => if task.id = taskId then change task else task) }

/-- Remove a task from `Θ`. -/
def removeTask (config : Config σ) (taskId : Nat) : Config σ :=
  { config with tasks := config.tasks.filter (fun task => !decide (task.id = taskId)) }

/-- Whether the invocation that `task` runs is still running in the run: the
premise "`S[e].inv = ⟨m, R, …⟩` with the same task" of the Complete rules
(spec §4.4). It fails once the Step was canceled. -/
def isTaskCurrent (config : Config σ) (task : Task σ) : Bool :=
  match config.run with
  | none => false
  | some run =>
    match run.findStep? task.step with
    | none => false
    | some record =>
      decide (record.invocation = some { method := task.method, state := .running task.id })

end Config

/-- The handler call of a task: `(fx⃗, resp) := U_{T,m}(ctx, input, snap, locals, cr)`
(spec §4.4 [WORKER-EFFECT]). A Step definition without WaitFor answers a
WaitFor call with the empty wait (spec §1.2: "Steps without WaitFor: An empty
wait"). -/
def Task.compute (flow : FlowDef σ) (task : Task σ) : List σ.Fx × Response σ :=
  match task.method with
  | .waitFor =>
    match (flow.stepDef task.step.stepType).waitFor with
    | some waitFor =>
      let result := waitFor task.context task.input task.snapshot
      (result.1, .waitFor result.2)
    | none => ([], .waitFor { effects := {}, wait := Wait.empty σ })
  | .execute =>
    let result := (flow.stepDef task.step.stepType).execute task.context task.input
      task.snapshot task.conditionResults
    (result.1, .execute result.2)

/-- One transition `Σ --ℓ⃗--> Σ'` of the restricted Dex system (spec §4.1). -/
def step (flow : FlowDef σ) (config : Config σ) : Action σ → Option (List (Obs σ) × Config σ)
  -- [TICK] (spec §4.5): `δ > 0`, no event.
  | .tick duration =>
    if 0 < duration then some ([], { config with clock := config.clock + duration }) else none
  -- [FLOW-START] (spec §4.3). Premise `f ∉ dom(R)`. `ρ₀` is the empty run, then
  -- `new(T₀, v₀, o₀, "__start__")`. `o₀.skipWaitFor` is read from the Step
  -- definition (spec Appendix A, assumption 20).
  | .request (.startFlow stepType input) =>
    match config.run with
    | some _ => none
    | none =>
      if config.canCommit then
        some (config.commit (Run.empty σ) [Obs.start stepType input] (fun run =>
          run.newStep stepType (some input) (flow.stepDef stepType).waitFor.isNone .flowStart))
      else none
  -- [PUBLISH] (spec §4.6): requires `st = Running`, else rejected with NOT_FOUND.
  -- A rejected input "changes nothing and produces the single event".
  | .request (.publish chan value) =>
    match config.run with
    | none => some ([Obs.rejectedPub chan value], config)
    | some run =>
      match run.status with
      | .closed _ => some ([Obs.rejectedPub chan value], config)
      | .running =>
        if config.canCommit then
          some (config.commit run [Obs.pub chan value] (fun current =>
            (current.applyPublications [(chan, value)]).wake [chan]))
        else none
  -- [STEP-START] (spec §4.4). Premise `S(e).inv = ⟨m, P, …⟩`; the lock premises
  -- are vacuous. Effect: snapshot, `inv := ⟨m, R, …⟩`, a fresh task in `Θ`.
  | .stepStart stepId =>
    match config.run with
    | none => none
    | some run =>
      match run.findStep? stepId with
      | none => none
      | some record =>
        match record.invocation with
        | some { method := method, state := .pending } =>
          if config.canCommit then
            let task : Task σ :=
              { id := config.nextTaskId
                step := stepId
                method := method
                context := { stepExecutionId := stepId, fromStepExecutionId := record.origin }
                input := record.input
                snapshot := run.snapshot
                conditionResults := record.conditionResults
                phase := .started }
            let committed := config.commit run [] (fun current =>
              current.updateStep stepId (fun currentRecord =>
                { currentRecord with
                  invocation := some { method := method, state := .running config.nextTaskId } }))
            some (committed.1,
              { committed.2 with
                tasks := config.tasks ++ [task], nextTaskId := config.nextTaskId + 1 })
          else none
        | _ => none
  -- [WORKER-EFFECT] (spec §4.4): "A task in phase `Started` computes `(fx⃗, resp)`
  -- and becomes `Running(fx⃗, resp)`."
  | .workerRun taskId =>
    match config.findTask? taskId with
    | none => none
    | some task =>
      match task.phase with
      | .started =>
        let result := task.compute flow
        some ([], config.updateTask taskId (fun current =>
          { current with phase := .running result.1 result.2 }))
      | _ => none
  -- [WORKER-EFFECT] (spec §4.4): "Each step of this rule emits the head of `fx⃗`."
  | .workerEffect taskId =>
    match config.findTask? taskId with
    | none => none
    | some task =>
      match task.phase with
      | .running (effect :: laterEffects) response =>
        some ([Obs.fx task.step task.method effect], config.updateTask taskId (fun current =>
          { current with phase := .running laterEffects response }))
      | _ => none
  -- [WORKER-EFFECT] (spec §4.4): "When `fx⃗` is empty the task becomes `Returned(resp)`."
  | .workerReturn taskId =>
    match config.findTask? taskId with
    | none => none
    | some task =>
      match task.phase with
      | .running [] response =>
        some ([], config.updateTask taskId (fun current =>
          { current with phase := .returned response }))
      | _ => none
  -- [WORKER-ABANDON] (spec §4.4): "A task whose invocation was canceled may be
  -- dropped at any point, before or after any of its effects."
  | .workerAbandon taskId =>
    match config.findTask? taskId with
    | none => none
    | some task =>
      if config.isTaskCurrent task then none else some ([], config.removeTask taskId)
  -- [STEP-COMPLETE-WAITFOR] and [STEP-COMPLETE-EXECUTE] (spec §4.4). Premise:
  -- the task is `Returned` and its invocation is still running.
  | .stepComplete taskId =>
    match config.run, config.findTask? taskId with
    | some run, some task =>
      if config.isTaskCurrent task && config.canCommit then
        match task.method, task.phase with
        -- `release(e)`; `S[e].inv := ⊥`; `effects(e, E, locals)`; `register(e, W)`.
        | .waitFor, .returned (.waitFor response) =>
          some ((config.removeTask taskId).commit run [] (fun current =>
            ((current.updateStep task.step (fun record => { record with invocation := none })
              ).applyEffects response.effects).registerWait task.step response.wait config.clock))
        -- `release(e)`; `S[e].inv := ⊥`; `effects(e, E, locals)`; `decide(e, D)`.
        | .execute, .returned (.execute response) =>
          some ((config.removeTask taskId).commit run [] (fun current =>
            ((current.updateStep task.step (fun record => { record with invocation := none })
              ).applyEffects response.effects).applyDecision task.step response.decision))
        | _, _ => none
      else none
    | _, _ => none
  -- [STEP-COMPLETE-STALE] (spec §4.4): "A `Returned` task whose invocation is no
  -- longer running is dropped."
  | .stepCompleteStale taskId =>
    match config.findTask? taskId with
    | none => none
    | some task =>
      match task.phase with
      | .returned _ =>
        if config.isTaskCurrent task then none else some ([], config.removeTask taskId)
      | _ => none
  -- [TIMER-FIRE] (spec §4.5): `D` is a non-empty prefix of `Due_f` in index
  -- order; each timer of `D` fires in that order, in one commit.
  | .timerFire count =>
    match config.run with
    | none => none
    | some run =>
      let due := run.dueTimers config.clock
      if 0 < count && decide (count ≤ due.length) && config.canCommit then
        some (config.commit run [] (fun current =>
          (due.take count).foldl Run.fireTimer current))
      else none

end DexExpressiveness.Dex
