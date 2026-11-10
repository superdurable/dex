import DexExpressiveness.FiniteTraces
import DexExpressiveness.Liveness.Statements

/-!
# Liveness, work package L2: lemmas about one step of each side

The lemmas that `Liveness/Silent.lean` is proved from. Everything here is
proved.

- The silent weight: sums of weights under a change or a removal of list
  elements.
- One Dex step: what a tick does, that every other action keeps the clock,
  that a commit stamps the clock, and which events each action has.
- One L step: the same facts for an L label.
- The requests of a step are read off its events (`Event.encodedRequests`),
  on both sides.
- A run for an admissible word: the Flow has a run exactly once a request
  was attempted, and the request of each action is the next one of the word.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Sums of weights -/

/-- A change that raises no weight does not raise the sum. -/
theorem sum_map_le_of_change {α : Type} (weight : α → Nat) (change : α → α)
    (elements : List α)
    (isNotHigher : ∀ element, element ∈ elements → weight (change element) ≤ weight element) :
    ((elements.map change).map weight).sum ≤ (elements.map weight).sum := by
  induction elements with
  | nil => simp
  | cons head tail inductionHypothesis =>
    simp only [List.map_cons, List.sum_cons]
    have headBound := isNotHigher head (by simp)
    have tailBound := inductionHypothesis
      (fun element isMember => isNotHigher element (List.mem_cons_of_mem _ isMember))
    omega

/-- A change that raises no weight and lowers one lowers the sum. -/
theorem sum_map_lt_of_change {α : Type} (weight : α → Nat) (change : α → α)
    (elements : List α)
    (isNotHigher : ∀ element, element ∈ elements → weight (change element) ≤ weight element)
    (lowered : α) (isMember : lowered ∈ elements)
    (isLower : weight (change lowered) < weight lowered) :
    ((elements.map change).map weight).sum < (elements.map weight).sum := by
  induction elements with
  | nil => cases isMember
  | cons head tail inductionHypothesis =>
    simp only [List.map_cons, List.sum_cons]
    have headBound := isNotHigher head (by simp)
    have tailIsNotHigher : ∀ element, element ∈ tail →
        weight (change element) ≤ weight element :=
      fun element isTailMember => isNotHigher element (List.mem_cons_of_mem _ isTailMember)
    have tailBound := sum_map_le_of_change weight change tail tailIsNotHigher
    rcases List.mem_cons.mp isMember with isHead | isTailMember
    · subst isHead
      omega
    · have tailLower := inductionHypothesis tailIsNotHigher isTailMember
      omega

/-- Removing elements does not raise the sum. -/
theorem sum_map_filter_le {α : Type} (weight : α → Nat) (isKept : α → Bool)
    (elements : List α) :
    ((elements.filter isKept).map weight).sum ≤ (elements.map weight).sum := by
  induction elements with
  | nil => simp
  | cons head tail inductionHypothesis =>
    by_cases isHeadKept : isKept head = true
    · simp only [List.filter_cons, isHeadKept, ite_true, List.map_cons, List.sum_cons]
      omega
    · simp only [List.filter_cons, isHeadKept, List.map_cons, List.sum_cons]
      simp only [Bool.false_eq_true, ite_false]
      omega

/-- Removing an element of positive weight lowers the sum. -/
theorem sum_map_filter_lt {α : Type} (weight : α → Nat) (isKept : α → Bool)
    (elements : List α) (removed : α) (isMember : removed ∈ elements)
    (isRemoved : isKept removed = false) (isPositive : 0 < weight removed) :
    ((elements.filter isKept).map weight).sum < (elements.map weight).sum := by
  induction elements with
  | nil => cases isMember
  | cons head tail inductionHypothesis =>
    have tailBound := sum_map_filter_le weight isKept tail
    rcases List.mem_cons.mp isMember with isHead | isTailMember
    · subst isHead
      simp only [List.filter_cons, isRemoved, List.map_cons, List.sum_cons]
      simp only [Bool.false_eq_true, ite_false]
      omega
    · have tailLower := inductionHypothesis isTailMember
      by_cases isHeadKept : isKept head = true
      · simp only [List.filter_cons, isHeadKept, ite_true, List.map_cons, List.sum_cons]
        omega
      · simp only [List.filter_cons, isHeadKept, List.map_cons, List.sum_cons]
        simp only [Bool.false_eq_true, ite_false]
        omega

/-! ## The silent weight -/

/-- Every task can still take a silent step. -/
theorem Dex.Task.silentWeight_pos (task : Dex.Task signature) : 0 < task.silentWeight := by
  unfold Dex.Task.silentWeight
  split <;> omega

/-- Dropping a task of `Θ` lowers the silent weight. -/
theorem silentWeight_removeTask_lt (config : Dex.Config signature) (taskId : Nat)
    (task : Dex.Task signature) (isFound : config.findTask? taskId = some task) :
    (config.removeTask taskId).silentWeight < config.silentWeight := by
  obtain ⟨isMember, hasId⟩ := mem_of_findTask? config taskId task isFound
  exact sum_map_filter_lt Dex.Task.silentWeight _ config.tasks task isMember
    (by simp [hasId]) task.silentWeight_pos

/-! ## One Dex step -/

/-- A tick advances the clock by a positive duration, has no event, and keeps
the timestamp of the last commit. -/
theorem dex_step_tick (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (duration : Nat) (events : List (Dex.Obs signature)) (next : Dex.Config signature)
    (isStep : Dex.step flow config (.tick duration) = some (events, next)) :
    0 < duration ∧ events = [] ∧ next.clock = config.clock + duration ∧
      next.lastCommitAt = config.lastCommitAt := by
  simp only [Dex.step] at isStep
  split at isStep
  · rename_i isPositive
    resolve_step isStep
    exact ⟨isPositive, rfl, rfl, rfl⟩
  · cases isStep

/-- Every action except a tick keeps the clock. -/
theorem dex_step_clock (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (next : Dex.Config signature)
    (isStep : Dex.step flow config action = some (events, next))
    (isProgress : (Dex.system flow).isProgress action = true) : next.clock = config.clock := by
  cases action with
  | tick duration => cases isProgress
  | request request =>
    cases request <;> simp only [Dex.step] at isStep <;> (repeat' split at isStep) <;>
      first
        | (cases isStep; done)
        | (resolve_step isStep; simp)
  | _ =>
    simp only [Dex.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep
         simp [Dex.Config.commit, Dex.Config.updateTask, Dex.Config.removeTask])

/-- A commit that no request causes ([STEP-START], a Step completion,
[TIMER-FIRE]) needs the clock above the last commit and stamps the clock. -/
theorem dex_step_stamp (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (next : Dex.Config signature)
    (isStep : Dex.step flow config action = some (events, next)) :
    match action with
    | .stepStart _ | .stepComplete _ | .timerFire _ =>
      config.canCommit = true ∧ next.lastCommitAt = some config.clock
    | _ => True := by
  cases action with
  | stepStart stepId =>
    simp only [Dex.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep
         simp_all [Dex.Config.commit])
  | stepComplete taskId =>
    simp only [Dex.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep
         simp_all [Dex.Config.removeTask])
  | timerFire count =>
    simp only [Dex.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep
         simp_all)
  | _ => trivial

/-- A configuration whose last commit has the timestamp of the clock cannot commit. -/
theorem canCommit_eq_false_of_lastCommitAt (config : Dex.Config signature)
    (isStamped : config.lastCommitAt = some config.clock) : config.canCommit = false := by
  simp [Dex.Config.canCommit, isStamped]

/-! ## The requests that the events of a step stand for -/

/-- The Dex requests that an L event stands for: `begin(v)` is the start, and
`in(c, v)` and `refused(c, v)` are a publication (spec §5.3, table). -/
def Event.encodedRequests : Event → List (Dex.Request signature)
  | .begin input => [.startFlow StepType.start (DexVal.value input)]
  | .input chan value => [.publish (Chan.external chan) (DexVal.value value)]
  | .refused chan value => [.publish (Chan.external chan) (DexVal.value value)]
  | _ => []

/-- The projection of a `closed` event stands for no request. -/
theorem project_closed_encodedRequests (status : Dex.CloseStatus)
    (outputs : List (Dex.StepExecId signature × DexVal)) :
    ([Dex.Obs.closed status outputs].filterMap project).flatMap Event.encodedRequests = [] := by
  simp only [List.filterMap_cons, List.filterMap_nil]
  split
  · rfl
  · rename_i event isProjected
    unfold project at isProjected
    split at isProjected
    all_goals first
      | (cases isProjected; done)
      | (cases isProjected; rfl)
      | (rename_i isClosed; cases isClosed)

/-- The projected `closed` event of a commit stands for no request. -/
theorem closeEvents_encodedRequests (before after : Dex.Run signature) :
    ((Dex.Config.closeEvents before after).filterMap project).flatMap Event.encodedRequests =
      [] := by
  unfold Dex.Config.closeEvents
  split
  · exact project_closed_encodedRequests _ _
  · rfl

/-- The projected events of an admissible Dex step stand for the request of
its action, and for nothing else. -/
theorem dex_step_encodedRequests (flow : Dex.FlowDef signature)
    (config : Dex.Config signature) (action : Dex.Action signature)
    (isAdmissible : IsAdmissibleAction config action) (events : List (Dex.Obs signature))
    (next : Dex.Config signature)
    (isStep : Dex.step flow config action = some (events, next)) :
    (events.filterMap project).flatMap Event.encodedRequests =
      ((Dex.system flow).requestOf action).toList := by
  cases action with
  | request request =>
    cases request with
    | startFlow stepType input =>
      obtain ⟨isStart, literal, isInput⟩ := isAdmissible
      subst isStart isInput
      simp only [Dex.step] at isStep
      repeat' split at isStep
      all_goals first
        | (cases isStep; done)
        | (resolve_step isStep
           simp only [List.cons_append, List.nil_append, List.filterMap_cons,
             project, List.flatMap_cons, closeEvents_encodedRequests]
           rfl)
    | publish chan message =>
      obtain ⟨_, externalChan, literal, isChan, isMessage⟩ := isAdmissible
      subst isChan isMessage
      simp only [Dex.step] at isStep
      repeat' split at isStep
      all_goals first
        | (cases isStep; done)
        | (resolve_step isStep
           simp only [List.cons_append, List.nil_append, List.filterMap_cons,
             List.filterMap_nil, project, List.flatMap_cons, List.flatMap_nil,
             closeEvents_encodedRequests]
           rfl)
  | workerEffect taskId =>
    simp only [Dex.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep
         rfl)
  | _ =>
    simp only [Dex.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep
         first
           | rfl
           | (simp only [Dex.Config.commit, List.nil_append]
              exact closeEvents_encodedRequests _ _))

/-- A Worker effect is one `fx` event, which L sees as `out(a)`. -/
theorem dex_step_workerEffect_events (flow : Dex.FlowDef signature)
    (config : Dex.Config signature) (taskId : Nat) (events : List (Dex.Obs signature))
    (next : Dex.Config signature)
    (isStep : Dex.step flow config (.workerEffect taskId) = some (events, next)) :
    events.filterMap project ≠ [] := by
  simp only [Dex.step] at isStep
  repeat' split at isStep
  all_goals first
    | (cases isStep; done)
    | (resolve_step isStep
       simp [project])

/-! ## One L step -/

/-- An idle tick advances the clock by a positive duration and has no event. -/
theorem l_step_tick (program : Program) (config : L.Config) (duration : Nat)
    (events : List Event) (next : L.Config)
    (isStep : L.step program config (.tick duration) = some (events, next)) :
    0 < duration ∧ events = [] ∧ next.clock = config.clock + duration := by
  simp only [L.step] at isStep
  split at isStep
  · rename_i isPositive
    resolve_step isStep
    exact ⟨isPositive, rfl, rfl⟩
  · cases isStep

/-- A completed statement has an event only for `exit` and `fail`, and that
event stands for no request. -/
theorem applyIntent_encodedRequests (config : L.Config) (tid : Tid) (result : StatementResult) :
    (config.applyIntent tid result).1.flatMap Event.encodedRequests = [] := by
  cases intentCase : result.intent with
  | signal cv =>
    simp only [L.Config.applyIntent, intentCase]
    split <;> rfl
  | endThread =>
    simp only [L.Config.applyIntent, intentCase]
    split <;> rfl
  | _ => simp [L.Config.applyIntent, intentCase, Event.encodedRequests]

/-- An action of a thread stands for no request. It has an event, or it is a
stamped action. -/
theorem l_stepThread_shape (program : Program) (config : L.Config) (thread : L.Thread)
    (events : List Event) (next : L.Config)
    (isStep : L.stepThread program config thread = some (events, next)) :
    events.flatMap Event.encodedRequests = [] ∧
    (events ≠ [] ∨ (config.canStamp = true ∧ next.lastStampAt = some next.clock)) := by
  simp only [L.stepThread] at isStep
  repeat' split at isStep
  all_goals first
    | (cases isStep; done)
    | (resolve_step isStep
       exact ⟨rfl, Or.inr ⟨‹config.canStamp = true›, rfl⟩⟩)
    | (resolve_step isStep
       exact ⟨applyIntent_encodedRequests _ _ _, Or.inr ⟨‹config.canStamp = true›, rfl⟩⟩)
    | (resolve_step isStep
       exact ⟨rfl, Or.inl (by simp)⟩)

/-- The events of an L step stand for the request of its label, and for
nothing else. -/
theorem l_step_encodedRequests (program : Program) (config : L.Config) (label : L.Label)
    (events : List Event) (next : L.Config)
    (isStep : L.step program config label = some (events, next)) :
    (((L.system program).requestOf label).toList).map encodeRequest =
      events.flatMap Event.encodedRequests := by
  cases label with
  | request request =>
    cases request <;> simp only [L.step] at isStep <;> (repeat' split at isStep) <;>
      first
        | (cases isStep; done)
        | (resolve_step isStep; rfl)
  | thread thread =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
    · rw [(l_stepThread_shape program config _ events next isStep).1]
      rfl
  | fire count =>
    simp only [L.step] at isStep
    split at isStep
    · simp only [Option.map_eq_some_iff] at isStep
      obtain ⟨fired, _, isPair⟩ := isStep
      injection isPair with isEvents _
      subst isEvents
      rfl
    · cases isStep
  | _ =>
    simp only [L.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep; rfl)

/-- An L step other than an idle tick has an event, or is a stamped action,
or keeps the clock. -/
theorem l_step_shape (program : Program) (config : L.Config) (label : L.Label)
    (events : List Event) (next : L.Config)
    (isStep : L.step program config label = some (events, next))
    (isProgress : (L.system program).isProgress label = true) :
    events ≠ [] ∨ (config.canStamp = true ∧ next.lastStampAt = some next.clock) ∨
      next.clock = config.clock := by
  cases label with
  | tick duration => cases isProgress
  | request request =>
    left
    cases request <;> simp only [L.step] at isStep <;> (repeat' split at isStep) <;>
      first
        | (cases isStep; done)
        | (resolve_step isStep; simp)
  | thread thread =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
    · rcases (l_stepThread_shape program config _ events next isStep).2 with hasEvent | isStamped
      · exact Or.inl hasEvent
      · exact Or.inr (Or.inl isStamped)
  | fire count =>
    simp only [L.step] at isStep
    split at isStep
    · rename_i isEnabled
      simp only [Option.map_eq_some_iff] at isStep
      obtain ⟨fired, _, isPair⟩ := isStep
      injection isPair with _ isNext
      subst isNext
      simp only [Bool.and_eq_true] at isEnabled
      exact Or.inr (Or.inl ⟨isEnabled.2, rfl⟩)
    · cases isStep
  | lateDeliver index =>
    simp only [L.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep; exact Or.inl (by simp))
  | lateDrop index =>
    simp only [L.step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (resolve_step isStep; exact Or.inr (Or.inr rfl))

/-- A configuration whose last stamp is at or above the clock cannot stamp. -/
theorem canStamp_eq_false_of_lastStampAt (config : L.Config) (last : Nat)
    (isStamped : config.lastStampAt = some last) (isNotBelow : config.clock ≤ last) :
    config.canStamp = false := by
  simp only [L.Config.canStamp, isStamped, decide_eq_false_iff_not]
  omega

/-! ## Runs -/

/-- A run of one label is one step. -/
theorem TransitionSystem.step_of_exec_singleton {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State) (label : Label)
    (events : List Event) (final : State)
    (isExec : system.exec state [label] = some (events, final)) :
    system.step state label = some (events, final) := by
  obtain ⟨stepEvents, next, laterEvents, isStep, isLater, isEvents⟩ :=
    system.exec_cons state label [] events final isExec
  simp only [TransitionSystem.exec] at isLater
  injection isLater with isPair
  injection isPair with isLaterEvents isNext
  subst isLaterEvents isNext isEvents
  simpa using isStep

/-- The concatenation of fewer chunks is a prefix of the concatenation of more. -/
theorem concatUpTo_prefix {α : Type} (chunks : Nat → List α) (fewer more : Nat)
    (isFewer : fewer ≤ more) :
    ∃ rest, concatUpTo chunks more = concatUpTo chunks fewer ++ rest := by
  induction more with
  | zero =>
    have isZero : fewer = 0 := by omega
    subst isZero
    exact ⟨[], by simp⟩
  | succ more inductionHypothesis =>
    by_cases isSame : fewer = more + 1
    · subst isSame
      exact ⟨[], by simp⟩
    · obtain ⟨rest, isRest⟩ := inductionHypothesis (by omega)
      exact ⟨rest ++ chunks more, by simp only [concatUpTo]; rw [isRest, List.append_assoc]⟩

/-- A position inside a finite concatenation of the chunks holds the element
of the sequence. -/
theorem IsConcatenation.getElem?_concatUpTo {α : Type} {chunks : Nat → List α}
    {sequence : Sequence α} (isConcatenation : IsConcatenation chunks sequence)
    (count position : Nat) (isInside : position < (concatUpTo chunks count).length) :
    sequence position = (concatUpTo chunks count)[position]? := by
  rcases isConcatenation position with ⟨other, isInsideOther, isElement⟩ | ⟨isOutside, _⟩
  · rw [isElement]
    rcases Nat.le_total other count with isFewer | isFewer
    · obtain ⟨rest, isRest⟩ := concatUpTo_prefix chunks other count isFewer
      rw [isRest, List.getElem?_append_left isInsideOther]
    · obtain ⟨rest, isRest⟩ := concatUpTo_prefix chunks count other isFewer
      rw [isRest, List.getElem?_append_left isInside]
  · have isBelow := isOutside count
    omega

/-- In a run for a word, the request of an action is the request of the word
at the number of requests attempted before the action. -/
theorem TransitionSystem.requestAt_of_isRunFor {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (run : InfiniteRun State Label)
    (word : Sequence Request) (isRunFor : system.IsRunFor run word) (index : Nat)
    (request : Request) (isRequest : system.requestOf (run.labels index) = some request) :
    word (concatUpTo (system.requestsAt run) index).length = some request := by
  have isInside : (concatUpTo (system.requestsAt run) index).length <
      (concatUpTo (system.requestsAt run) (index + 1)).length := by
    simp [concatUpTo, TransitionSystem.requestsAt, isRequest]
  rw [IsConcatenation.getElem?_concatUpTo isRunFor (index + 1) _ isInside]
  simp [concatUpTo, TransitionSystem.requestsAt, isRequest]

/-! ## A Dex run for an admissible word -/

/-- The Dex form of an admissible word begins with the start of the entry
Step with an encoded literal. -/
theorem encodedWord_zero (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word) :
    ∃ input : Literal, (word.map encodeRequest) 0 =
      some (.startFlow StepType.start (DexVal.value input.toVal)) := by
  obtain ⟨⟨input, startsWith⟩, _, _⟩ := isAdmissible
  exact ⟨input, by simp [Sequence.map, startsWith, encodeRequest]⟩

/-- Every later request of the Dex form of an admissible word is the
publication of an encoded literal to an external Channel. -/
theorem encodedWord_succ (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (position : Nat) (request : Dex.Request signature)
    (isRequest : (word.map encodeRequest) (position + 1) = some request) :
    ∃ (chan : ExtChan) (literal : Literal),
      request = .publish (Chan.external chan) (DexVal.value literal.toVal) := by
  obtain ⟨_, laterAreInputs, _⟩ := isAdmissible
  simp only [Sequence.map] at isRequest
  cases atPosition : word (position + 1) with
  | none => simp [atPosition] at isRequest
  | some lRequest =>
    obtain ⟨chan, value, isInput⟩ := laterAreInputs position lRequest atPosition
    subst isInput
    simp only [atPosition, Option.map_some, encodeRequest, Option.some.injEq] at isRequest
    exact ⟨chan, value, isRequest.symm⟩

/-- In a run for the Dex form of an admissible word, the Flow has a run
exactly once a request was attempted: the first request is the start. -/
theorem run_isSome_of_isRunFor (program : Program) (word : Sequence L.Request)
    (isAdmissible : L.IsAdmissibleWord word)
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun)
    (isRunFor : (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest))
    (index : Nat) :
    (dexRun.states index).run.isSome = true ↔
      0 < (concatUpTo ((Dex.system ⟦program⟧).requestsAt dexRun) index).length := by
  induction index with
  | zero =>
    rw [isRun.1]
    simp [Dex.system, Dex.Config.initial, concatUpTo]
  | succ index inductionHypothesis =>
    obtain ⟨events, isStep⟩ := isRun.2 index
    have isNext := (Dex.system ⟦program⟧).requestAt_of_isRunFor dexRun _ isRunFor index
    generalize labelCase : dexRun.labels index = action at isStep isNext
    have isSome := step_run_isSome ⟦program⟧ (dexRun.states index) action events
      (dexRun.states (index + 1)) isStep
    cases action with
    | request request =>
      have isRequest := isNext request rfl
      have hasLength : (concatUpTo ((Dex.system ⟦program⟧).requestsAt dexRun) (index + 1)).length =
          (concatUpTo ((Dex.system ⟦program⟧).requestsAt dexRun) index).length + 1 := by
        simp [concatUpTo, TransitionSystem.requestsAt, labelCase, Dex.system]
      cases request with
      | startFlow stepType input =>
        simp only at isSome
        rw [isSome.2, hasLength]
        simp
      | publish chan message =>
        simp only at isSome
        rw [isSome, hasLength, inductionHypothesis]
        refine ⟨fun _ => by omega, fun _ => ?_⟩
        cases lengthCase : (concatUpTo ((Dex.system ⟦program⟧).requestsAt dexRun) index).length with
        | zero =>
          rw [lengthCase] at isRequest
          obtain ⟨input, isStart⟩ := encodedWord_zero word isAdmissible
          rw [isStart] at isRequest
          cases isRequest
        | succ previous => omega
    | _ =>
      simp only at isSome
      rw [isSome, inductionHypothesis]
      simp [concatUpTo, TransitionSystem.requestsAt, labelCase, Dex.system]

end DexExpressiveness
