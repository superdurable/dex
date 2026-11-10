import DexExpressiveness.Simulation

/-!
# Safety: the two inclusions of finite traces (spec §5.4)

`finiteTrace_dex_to_l` and `finiteTrace_l_to_dex` are proved here by
induction on the run, from the two step simulations (`dex_step_simulated`,
`l_step_simulated`) and from the fact that every transition keeps the
invariant of its side (`L.Invariant.step`, `ConfigInvariant.step`). This file
has no `sorry` of its own.

The inductions carry the shape of the requests still to come. On the Dex side
a publication before the start is possible (it is rejected), so admissibility
of a run is a condition on its requests (spec §5.3). On the L side an input
before the start is not enabled at all.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Finite runs -/

/-- A run of `first ++ second` is a run of `first` followed by a run of `second`. -/
theorem TransitionSystem.exec_append {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State)
    (firstLabels secondLabels : List Label) (firstEvents secondEvents : List Event)
    (middle final : State)
    (isFirst : system.exec state firstLabels = some (firstEvents, middle))
    (isSecond : system.exec middle secondLabels = some (secondEvents, final)) :
    system.exec state (firstLabels ++ secondLabels) =
      some (firstEvents ++ secondEvents, final) := by
  induction firstLabels generalizing state firstEvents with
  | nil =>
    simp only [TransitionSystem.exec] at isFirst
    cases isFirst
    simpa using isSecond
  | cons label laterLabels inductionHypothesis =>
    simp only [TransitionSystem.exec, List.cons_append] at isFirst ⊢
    split at isFirst
    · cases isFirst
    · rename_i stepEvents next isStep
      split at isFirst
      · cases isFirst
      · rename_i laterEvents reached isLater
        cases isFirst
        rw [inductionHypothesis next laterEvents isLater]
        simp

/-- A run of one more label from a run's end. -/
theorem TransitionSystem.exec_cons {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State) (label : Label)
    (laterLabels : List Label) (events : List Event) (final : State)
    (isExec : system.exec state (label :: laterLabels) = some (events, final)) :
    ∃ stepEvents next laterEvents,
      system.step state label = some (stepEvents, next) ∧
      system.exec next laterLabels = some (laterEvents, final) ∧
      events = stepEvents ++ laterEvents := by
  simp only [TransitionSystem.exec] at isExec
  split at isExec
  · cases isExec
  · rename_i stepEvents next isStep
    split at isExec
    · cases isExec
    · rename_i laterEvents reached isLater
      cases isExec
      exact ⟨stepEvents, next, laterEvents, isStep, isLater, rfl⟩

/-- A state reached by a run from a reachable state is reachable. -/
theorem TransitionSystem.Reachable.exec {State Label Event Request : Type}
    {system : TransitionSystem State Label Event Request} {state final : State}
    (isReachable : system.Reachable state) (labels : List Label) (events : List Event)
    (isExec : system.exec state labels = some (events, final)) : system.Reachable final := by
  obtain ⟨earlierLabels, earlierEvents, isEarlier⟩ := isReachable
  exact ⟨earlierLabels ++ labels, earlierEvents ++ events,
    system.exec_append system.init earlierLabels labels earlierEvents events state final
      isEarlier isExec⟩

/-! ## The run of the root Flow exists exactly from its start on -/

/-- A start needs that the Flow has no run and creates one; every other
transition keeps whether the Flow has a run. -/
theorem step_run_isSome (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (next : Dex.Config signature)
    (isStep : Dex.step flow config action = some (events, next)) :
    match action with
    | .request (.startFlow _ _) => config.run = none ∧ next.run.isSome = true
    | _ => next.run.isSome = config.run.isSome := by
  cases action with
  | request request =>
    cases request <;> simp only [Dex.step] at isStep <;> (repeat' split at isStep) <;>
      first
        | (cases isStep; done)
        | (resolve_step isStep; simp_all)
  | _ =>
    simp only [Dex.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep
         simp_all [Dex.Config.commit, Dex.Config.updateTask, Dex.Config.removeTask])

/-! ## The states of the induction -/

/-- The initial configurations form a simulation state. -/
theorem isSimulationState_initial (program : Program) :
    IsSimulationState program (L.system program).init (Dex.system ⟦program⟧).init :=
  { isRelated := related_initial program
    lInvariant := L.Invariant.initial program
    dexInvariant := ConfigInvariant.initial program
    tasksDistinct := ⟨List.nodup_nil, fun task isMember => by cases isMember⟩
    timersDistinct := TimersAreDistinct.initial ⟦program⟧ }

/-- A finite Dex run keeps the Dex invariant, the distinctness of task IDs,
and the distinctness of timer IDs. -/
theorem dexInvariants_of_exec (program : Program) (actions : List (Dex.Action signature))
    (config : Dex.Config signature) (events : List (Dex.Obs signature))
    (final : Dex.Config signature)
    (invariant : ConfigInvariant program config) (tasksDistinct : TasksAreDistinct config)
    (timersDistinct : TimersAreDistinct config)
    (isExec : (Dex.system ⟦program⟧).exec config actions = some (events, final)) :
    ConfigInvariant program final ∧ TasksAreDistinct final ∧ TimersAreDistinct final :=
  TransitionSystem.invariant_of_exec (Dex.system ⟦program⟧)
    (fun state => ConfigInvariant program state ∧ TasksAreDistinct state ∧
      TimersAreDistinct state)
    (fun state label stepEvents next holds isStep =>
      ⟨ConfigInvariant.step program state label stepEvents next holds.1 holds.2.1 isStep,
       TasksAreDistinct.step ⟦program⟧ state label stepEvents next holds.2.1 isStep,
       TimersAreDistinct.step ⟦program⟧ state label stepEvents next holds.2.2 isStep⟩)
    actions config events final ⟨invariant, tasksDistinct, timersDistinct⟩ isExec

/-! ## Dex to L -/

/-- The requests still to come are admissible from this configuration: only
external publications once the Flow has a run, and before that an admissible
prefix (spec §5.3). -/
def AreRequestsAdmissibleFrom (config : Dex.Config signature)
    (requests : List (Dex.Request signature)) : Prop :=
  (config.run.isSome = true →
    ∀ request, request ∈ requests →
      ∃ (chan : ExtChan) (literal : Literal),
        request = .publish (Chan.external chan) (DexVal.value literal.toVal)) ∧
  (config.run.isSome = false → environment.IsAdmissiblePrefix requests)

/-- An enabled action whose request is admissible from here is an admissible
action, and the later requests are admissible from the next configuration. -/
theorem admissible_step (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (action : Dex.Action signature) (laterActions : List (Dex.Action signature))
    (events : List (Dex.Obs signature)) (next : Dex.Config signature)
    (isStep : Dex.step flow config action = some (events, next))
    (areAdmissible : AreRequestsAdmissibleFrom config
      ((action :: laterActions).filterMap (Dex.system flow).requestOf)) :
    IsAdmissibleAction config action ∧
    AreRequestsAdmissibleFrom next (laterActions.filterMap (Dex.system flow).requestOf) := by
  have isSome := step_run_isSome flow config action events next isStep
  obtain ⟨afterStart, beforeStart⟩ := areAdmissible
  cases action with
  | request request =>
    cases request with
    | startFlow stepType input =>
      obtain ⟨hasNoRun, nextHasRun⟩ := isSome
      have isPrefix := beforeStart (by simp [hasNoRun])
      simp only [Dex.system, List.filterMap_cons] at isPrefix
      rcases isPrefix with isEmpty | ⟨startInput, laterRequests, isRequests, ⟨value, isValue⟩,
        laterArePublications⟩
      · cases isEmpty
      · injection isRequests with isHead isTail
        injection isHead with isStepType isInput
        refine ⟨⟨isStepType, value, by rw [isInput, isValue]⟩, ?_, ?_⟩
        · intro _ request isMember
          obtain ⟨chan, message, isPublication, externalChan, messageValue, isExternal, isEncoded⟩ :=
            laterArePublications request (by rw [← isTail]; exact isMember)
          exact ⟨externalChan, messageValue, by rw [isPublication, isExternal, isEncoded]⟩
        · intro nextHasNoRun
          rw [nextHasRun] at nextHasNoRun
          cases nextHasNoRun
    | publish chan value =>
      simp only at isSome
      by_cases hasRun : config.run.isSome = true
      · have arePublications := afterStart hasRun
        obtain ⟨externalChan, messageValue, isPublication⟩ :=
          arePublications (.publish chan value) (by simp [Dex.system])
        injection isPublication with isChan isValue
        refine ⟨⟨by intro hasNoRun; simp [hasNoRun] at hasRun, externalChan, messageValue,
          isChan, isValue⟩, ?_, ?_⟩
        · intro _ request isMember
          exact arePublications request (by simp only [Dex.system, List.filterMap_cons]; exact List.mem_cons_of_mem _ isMember)
        · intro nextHasNoRun
          rw [isSome, hasRun] at nextHasNoRun
          cases nextHasNoRun
      · have isPrefix := beforeStart (by simpa using hasRun)
        simp only [Dex.system, List.filterMap_cons] at isPrefix
        rcases isPrefix with isEmpty | ⟨startInput, laterRequests, isRequests, _⟩
        · cases isEmpty
        · injection isRequests with isHead _
          cases isHead
  | tick duration =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩
  | stepStart stepId =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩
  | workerRun taskId =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩
  | workerEffect taskId =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩
  | workerReturn taskId =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩
  | workerAbandon taskId =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩
  | stepComplete taskId =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩
  | stepCompleteStale taskId =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩
  | timerFire count =>
    simp only at isSome
    exact ⟨trivial, by intro hasRun; exact afterStart (by rw [← isSome]; exact hasRun),
      by intro hasNoRun; exact beforeStart (by rw [← isSome]; exact hasNoRun)⟩

/-- Dex to L, for a run: a Dex run from a simulation state whose L
configuration is reachable, with admissible requests, is simulated by an L
run with the same projected events. -/
theorem dex_exec_simulated (program : Program) (isWellFormed : program.IsWellFormed)
    (actions : List (Dex.Action signature)) :
    ∀ (lConfig : L.Config) (dexConfig : Dex.Config signature)
      (events : List (Dex.Obs signature)) (dexFinal : Dex.Config signature),
      IsSimulationState program lConfig dexConfig → (L.system program).Reachable lConfig →
      AreRequestsAdmissibleFrom dexConfig
        (actions.filterMap (Dex.system ⟦program⟧).requestOf) →
      (Dex.system ⟦program⟧).exec dexConfig actions = some (events, dexFinal) →
      ∃ labels lFinal,
        (L.system program).exec lConfig labels = some (events.filterMap project, lFinal) ∧
        Related program lFinal dexFinal := by
  induction actions with
  | nil =>
    intro lConfig dexConfig events dexFinal state _ _ isExec
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    exact ⟨[], lConfig, rfl, state.isRelated⟩
  | cons action laterActions inductionHypothesis =>
    intro lConfig dexConfig events dexFinal state isReachable areAdmissible isExec
    obtain ⟨stepEvents, dexNext, laterEvents, isStep, isLater, isEvents⟩ :=
      (Dex.system ⟦program⟧).exec_cons dexConfig action laterActions events dexFinal isExec
    obtain ⟨isAdmissible, laterAdmissible⟩ :=
      admissible_step ⟦program⟧ dexConfig action laterActions stepEvents dexNext isStep
        areAdmissible
    obtain ⟨stepLabels, lNext, _, isLStep, nextRelated⟩ :=
      dex_step_simulated program lConfig dexConfig state (isWellFormed lConfig isReachable)
        action isAdmissible stepEvents dexNext isStep
    have nextState : IsSimulationState program lNext dexNext :=
      { isRelated := nextRelated
        lInvariant := L.Invariant.of_exec program stepLabels lConfig _ lNext state.lInvariant
          isLStep
        dexInvariant := ConfigInvariant.step program dexConfig action stepEvents dexNext
          state.dexInvariant state.tasksDistinct isStep
        tasksDistinct := TasksAreDistinct.step ⟦program⟧ dexConfig action stepEvents dexNext
          state.tasksDistinct isStep
        timersDistinct := TimersAreDistinct.step ⟦program⟧ dexConfig action stepEvents dexNext
          state.timersDistinct isStep }
    obtain ⟨laterLabels, lFinal, isLLater, finalRelated⟩ :=
      inductionHypothesis lNext dexNext laterEvents dexFinal nextState
        (isReachable.exec stepLabels _ isLStep) laterAdmissible isLater
    refine ⟨stepLabels ++ laterLabels, lFinal, ?_, finalRelated⟩
    rw [isEvents, List.filterMap_append]
    exact (L.system program).exec_append lConfig stepLabels laterLabels _ _ lNext lFinal isLStep
      isLLater

/-- Safety, Dex to L: `π(FinTr_{E_adm}(⟦S⟧)) ⊆ FinTr(S)`. -/
theorem finiteTrace_dex_to_l (program : Program) (isWellFormed : program.IsWellFormed)
    (trace : List Event) (isDexTrace : IsProjectedDexFiniteTrace program trace) :
    IsLFiniteTrace program trace := by
  obtain ⟨actions, events, final, isExec, isAdmissible, isTrace⟩ := isDexTrace
  obtain ⟨labels, lFinal, isLExec, _⟩ :=
    dex_exec_simulated program isWellFormed actions (L.system program).init
      (Dex.system ⟦program⟧).init events final (isSimulationState_initial program)
      ⟨[], [], rfl⟩
      ⟨fun hasRun => by simp [Dex.system, Dex.Config.initial] at hasRun, fun _ => isAdmissible⟩
      isExec
  refine ⟨labels, events.filterMap project, lFinal, isLExec, trivial, ?_⟩
  rw [isTrace]
  simp

/-! ## L to Dex -/

/-- A start is enabled only before the program started, and starts it. -/
theorem l_step_start (program : Program) (config : L.Config) (input : Literal)
    (events : List Event) (next : L.Config)
    (isStep : L.step program config (.request (.start input)) = some (events, next)) :
    config.status = .notStarted ∧ next.status = .running := by
  simp only [L.step] at isStep
  split at isStep
  · rename_i isStatus
    split at isStep
    · injection isStep with isPair
      injection isPair with _ isNext
      subst isNext
      exact ⟨isStatus, rfl⟩
    · cases isStep
  · cases isStep

/-- An input is enabled only after the program started. -/
theorem l_step_input (program : Program) (config : L.Config) (chan : ExtChan) (value : Literal)
    (events : List Event) (next : L.Config)
    (isStep : L.step program config (.request (.input chan value)) = some (events, next)) :
    config.status ≠ .notStarted := by
  simp only [L.step] at isStep
  split at isStep
  · cases isStep
  · rename_i isStatus
    rw [isStatus]
    simp
  · rename_i isStatus
    rw [isStatus]
    simp

/-- Related configurations agree on whether the program has started. -/
theorem Related.run_isSome {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig) :
    dexConfig.run.isSome = !decide (lConfig.status = .notStarted) := by
  have isStatus := isRelated.status
  cases statusCase : lConfig.status <;> cases runCase : dexConfig.run <;>
    simp [statusCase, runCase] at isStatus ⊢

/-- A Dex run without a start keeps whether the Flow has a run. -/
theorem exec_run_isSome (flow : Dex.FlowDef signature) (actions : List (Dex.Action signature)) :
    ∀ (config : Dex.Config signature) (events : List (Dex.Obs signature))
      (final : Dex.Config signature),
      (∀ request, request ∈ actions.filterMap (Dex.system flow).requestOf →
        ∃ chan value, request = .publish chan value) →
      (Dex.system flow).exec config actions = some (events, final) →
      final.run.isSome = config.run.isSome := by
  induction actions with
  | nil =>
    intro config events final _ isExec
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    rfl
  | cons action laterActions inductionHypothesis =>
    intro config events final hasNoStart isExec
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, _⟩ :=
      (Dex.system flow).exec_cons config action laterActions events final isExec
    have isSome := step_run_isSome flow config action stepEvents next isStep
    have laterHasNoStart : ∀ request,
        request ∈ laterActions.filterMap (Dex.system flow).requestOf →
        ∃ chan value, request = .publish chan value := by
      intro request isMember
      apply hasNoStart request
      simp only [List.filterMap_cons]
      split
      · exact isMember
      · exact List.mem_cons_of_mem _ isMember
    have nextIsSome : next.run.isSome = config.run.isSome := by
      cases action with
      | request request =>
        cases request with
        | startFlow stepType input =>
          obtain ⟨chan, value, isPublication⟩ :=
            hasNoStart (.startFlow stepType input) (by simp [Dex.system])
          cases isPublication
        | publish chan value => exact isSome
      | _ => exact isSome
    rw [← nextIsSome]
    exact inductionHypothesis next laterEvents final laterHasNoStart isLater

/-- L to Dex, for a run: an L run from a simulation state is simulated by a
Dex run with the same projected events, whose requests are the encoded
requests of the L run and are admissible. -/
theorem l_exec_simulated (program : Program) (labels : List L.Label) :
    ∀ (lConfig : L.Config) (dexConfig : Dex.Config signature) (events : List Event)
      (lFinal : L.Config),
      IsSimulationState program lConfig dexConfig →
      (L.system program).exec lConfig labels = some (events, lFinal) →
      ∃ actions dexEvents dexFinal,
        (Dex.system ⟦program⟧).exec dexConfig actions = some (dexEvents, dexFinal) ∧
        dexEvents.filterMap project = events ∧
        AreRequestsAdmissibleFrom dexConfig
          (actions.filterMap (Dex.system ⟦program⟧).requestOf) ∧
        Related program lFinal dexFinal := by
  induction labels with
  | nil =>
    intro lConfig dexConfig events lFinal state isExec
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    exact ⟨[], [], dexConfig, rfl, rfl,
      ⟨(fun _ request isMember => by cases isMember), (fun _ => Or.inl rfl)⟩, state.isRelated⟩
  | cons label laterLabels inductionHypothesis =>
    intro lConfig dexConfig events lFinal state isExec
    have isRelated := state.isRelated
    obtain ⟨stepEvents, lNext, laterEvents, isStep, isLater, isEvents⟩ :=
      (L.system program).exec_cons lConfig label laterLabels events lFinal isExec
    obtain ⟨stepActions, stepDexEvents, dexNext, isDexStep, stepProjected, stepRequests,
      nextRelated⟩ := l_step_simulated program lConfig dexConfig state label stepEvents lNext
        isStep
    have nextDexInvariants := dexInvariants_of_exec program stepActions dexConfig stepDexEvents
      dexNext state.dexInvariant state.tasksDistinct state.timersDistinct isDexStep
    have nextState : IsSimulationState program lNext dexNext :=
      { isRelated := nextRelated
        lInvariant := L.Invariant.step program lConfig label stepEvents lNext state.lInvariant
          isStep
        dexInvariant := nextDexInvariants.1
        tasksDistinct := nextDexInvariants.2.1
        timersDistinct := nextDexInvariants.2.2 }
    obtain ⟨laterActions, laterDexEvents, dexFinal, isDexLater, laterProjected, laterAdmissible,
      finalRelated⟩ := inductionHypothesis lNext dexNext laterEvents lFinal nextState isLater
    refine ⟨stepActions ++ laterActions, stepDexEvents ++ laterDexEvents, dexFinal,
      (Dex.system ⟦program⟧).exec_append dexConfig stepActions laterActions _ _ dexNext dexFinal
        isDexStep isDexLater,
      by rw [List.filterMap_append, stepProjected, laterProjected, isEvents], ?_, finalRelated⟩
    rw [List.filterMap_append, stepRequests]
    obtain ⟨laterAfterStart, laterBeforeStart⟩ := laterAdmissible
    have nextIsSome := nextRelated.run_isSome
    have currentIsSome := isRelated.run_isSome
    cases label with
    | request request =>
      cases request with
      | start input =>
        obtain ⟨wasNotStarted, isRunning⟩ := l_step_start program lConfig input stepEvents lNext
          isStep
        have nextHasRun : dexNext.run.isSome = true := by simp [nextIsSome, isRunning]
        have arePublications := laterAfterStart nextHasRun
        refine ⟨fun hasRun => by simp [currentIsSome, wasNotStarted] at hasRun, fun _ => ?_⟩
        refine Or.inr ⟨DexVal.value input.toVal, _, rfl, ⟨input, rfl⟩, ?_⟩
        intro request isMember
        obtain ⟨chan, value, isPublication⟩ := arePublications request isMember
        exact ⟨_, _, isPublication, chan, value, rfl, rfl⟩
      | input chan value =>
        have wasStarted := l_step_input program lConfig chan value stepEvents lNext isStep
        have hasRun : dexConfig.run.isSome = true := by simp [currentIsSome, wasStarted]
        have nextHasRun : dexNext.run.isSome = true := by
          have isSame := exec_run_isSome ⟦program⟧ stepActions dexConfig stepDexEvents dexNext
            (by
              intro request isMember
              rw [stepRequests] at isMember
              simp only [L.system, Option.toList, List.map_cons, List.map_nil, encodeRequest,
                List.mem_singleton] at isMember
              exact ⟨_, _, isMember⟩) isDexStep
          rw [isSame]
          exact hasRun
        refine ⟨fun _ request isMember => ?_, fun hasNoRun => by
          rw [hasRun] at hasNoRun; cases hasNoRun⟩
        simp only [L.system, Option.toList, List.map_cons, List.map_nil, encodeRequest,
          List.cons_append, List.nil_append, List.mem_cons] at isMember
        rcases isMember with isHead | isLaterMember
        · exact ⟨chan, value, isHead⟩
        · exact laterAfterStart nextHasRun request isLaterMember
    | _ =>
      have noRequest : stepActions.filterMap (Dex.system ⟦program⟧).requestOf = [] := by
        simpa [L.system] using stepRequests
      have isSame := exec_run_isSome ⟦program⟧ stepActions dexConfig stepDexEvents dexNext
        (by intro request isMember; rw [noRequest] at isMember; cases isMember) isDexStep
      simp only [L.system, Option.toList, List.map_nil, List.nil_append]
      exact ⟨fun hasRun => laterAfterStart (by rw [isSame]; exact hasRun),
        fun hasNoRun => laterBeforeStart (by rw [isSame]; exact hasNoRun)⟩

/-- Safety, L to Dex: `FinTr(S) ⊆ π(FinTr_{E_adm}(⟦S⟧))`. -/
theorem finiteTrace_l_to_dex (program : Program) (_isWellFormed : program.IsWellFormed)
    (trace : List Event) (isLTrace : IsLFiniteTrace program trace) :
    IsProjectedDexFiniteTrace program trace := by
  obtain ⟨labels, events, final, isExec, _, isTrace⟩ := isLTrace
  obtain ⟨actions, dexEvents, dexFinal, isDexExec, isProjected, areAdmissible, _⟩ :=
    l_exec_simulated program labels (L.system program).init (Dex.system ⟦program⟧).init events
      final (isSimulationState_initial program) isExec
  refine ⟨actions, dexEvents, dexFinal, isDexExec,
    areAdmissible.2 (by simp [Dex.system, Dex.Config.initial]), ?_⟩
  rw [isTrace, isProjected]
  simp

end DexExpressiveness
