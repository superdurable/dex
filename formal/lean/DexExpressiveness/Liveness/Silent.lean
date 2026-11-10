import DexExpressiveness.FiniteTraces
import DexExpressiveness.Liveness.Statements
import DexExpressiveness.Liveness.SilentLemmas

/-!
# Liveness, work package L2: silent Dex steps and the kinds of labels

A Dex step is simulated by at most one L label (`dex_step_simulated`). This
package says which L label, as far as the liveness proof needs:

- a clock tick is simulated by a clock tick, and a progress step by progress
  steps only (spec §5.5: "[TICK] steps are time passing, not progress");
- a Dex step with no L label is of a silent kind, and every action of a
  silent kind lowers `Dex.Config.silentWeight`;
- the L label attempts the request the Dex action attempts, and no other;
- in a run for an admissible word every Dex action is admissible.

Every lemma here is proved, from the lemmas of `Liveness/SilentLemmas.lean`.
`silentWeight_lt_of_isSilentKind` assumes that task IDs are unique.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- An action of a silent kind lowers the silent weight when task IDs are
unique. Without unique IDs `workerRun` can keep or raise the weight: it
changes every task with the ID, and a `Returned` one among them gets heavier. -/
theorem silentWeight_lt_of_isSilentKind (flow : Dex.FlowDef signature)
    (config : Dex.Config signature) (tasksDistinct : TasksAreDistinct config)
    (action : Dex.Action signature)
    (events : List (Dex.Obs signature)) (next : Dex.Config signature)
    (isStep : Dex.step flow config action = some (events, next))
    (isSilent : action.isSilentKind = true) : next.silentWeight < config.silentWeight := by
  cases action with
  | workerRun taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep
      · rename_i isStarted
        resolve_step isStep
        obtain ⟨isMember, hasId⟩ := mem_of_findTask? config taskId task isFound
        simp only [Dex.Config.silentWeight, Dex.Config.updateTask]
        apply sum_map_lt_of_change Dex.Task.silentWeight _ config.tasks ?_ task isMember ?_
        · intro element isElement
          by_cases isChanged : element.id = taskId
          · have isTask := eq_of_isFound config taskId element task tasksDistinct isElement
              isChanged isFound
            subst isTask
            simp [isChanged, Dex.Task.silentWeight, isStarted]
          · simp [isChanged]
        · simp [hasId, Dex.Task.silentWeight, isStarted]
      · cases isStep
  | workerReturn taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep
      · rename_i response isRunning
        resolve_step isStep
        obtain ⟨isMember, hasId⟩ := mem_of_findTask? config taskId task isFound
        simp only [Dex.Config.silentWeight, Dex.Config.updateTask]
        apply sum_map_lt_of_change Dex.Task.silentWeight _ config.tasks ?_ task isMember ?_
        · intro element _
          by_cases isChanged : element.id = taskId
          · have isPositive := element.silentWeight_pos
            simp only [isChanged, ite_true]
            simp only [Dex.Task.silentWeight] at isPositive ⊢
            omega
          · simp [isChanged]
        · simp [hasId, Dex.Task.silentWeight, isRunning]
      · cases isStep
  | workerAbandon taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep
      · cases isStep
      · resolve_step isStep
        exact silentWeight_removeTask_lt config taskId task isFound
  | stepCompleteStale taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep
      · split at isStep
        · cases isStep
        · resolve_step isStep
          exact silentWeight_removeTask_lt config taskId task isFound
      · cases isStep
  | _ => cases isSilent

/-- A clock tick keeps the silent weight. -/
theorem silentWeight_tick (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (duration : Nat) (events : List (Dex.Obs signature)) (next : Dex.Config signature)
    (isStep : Dex.step flow config (.tick duration) = some (events, next)) :
    next.silentWeight = config.silentWeight := by
  simp only [Dex.step] at isStep
  split at isStep
  · resolve_step isStep
    rfl
  · cases isStep

/-- The L labels that simulate one Dex step: they are progress steps exactly
when the Dex action is one, a clock tick is simulated by a label, a step with
no label is of a silent kind, and the labels attempt the request of the Dex
action. `labels` and `lNext` are what `dex_step_simulated` gives. -/
theorem dex_step_labels (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (action : Dex.Action signature) (isAdmissible : IsAdmissibleAction dexConfig action)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig action = some (dexEvents, dexNext))
    (labels : List L.Label) (lNext : L.Config) (atMostOne : labels.length ≤ 1)
    (isExec : (L.system program).exec lConfig labels = some (dexEvents.filterMap project, lNext))
    (isRelated : Related program lNext dexNext) :
    (∀ label, label ∈ labels →
      (L.system program).isProgress label = (Dex.system ⟦program⟧).isProgress action) ∧
    ((Dex.system ⟦program⟧).isProgress action = false → labels ≠ []) ∧
    (labels = [] → action.isSilentKind = true) ∧
    (labels.filterMap (L.system program).requestOf).map encodeRequest =
      ((Dex.system ⟦program⟧).requestOf action).toList := by
  have isDexStep : Dex.step ⟦program⟧ dexConfig action = some (dexEvents, dexNext) := isStep
  have clockBefore := state.isRelated.clock
  have stampBefore := state.isRelated.stamp
  have clockAfter := isRelated.clock
  have stampAfter := isRelated.stamp
  have dexRequests := dex_step_encodedRequests ⟦program⟧ dexConfig action isAdmissible dexEvents
    dexNext isDexStep
  rcases labels with _ | ⟨label, _ | ⟨second, laterLabels⟩⟩
  · -- No L label: the L configuration and the projected events are unchanged.
    simp only [TransitionSystem.exec] at isExec
    injection isExec with isPair
    injection isPair with isEvents isNext
    subst isNext
    have isNotTick : ∀ duration, action ≠ .tick duration := by
      intro duration isTick
      subst isTick
      obtain ⟨isPositive, _, isClock, _⟩ :=
        dex_step_tick ⟦program⟧ dexConfig duration dexEvents dexNext isDexStep
      omega
    have isNotCommit : dexNext.lastCommitAt = some dexConfig.clock →
        dexConfig.canCommit = true → False := by
      intro isStamp canCommit
      rw [canCommit_eq_false_of_lastCommitAt dexConfig
        (by rw [← stampBefore, stampAfter, isStamp])] at canCommit
      cases canCommit
    refine ⟨fun label isMember => (by cases isMember), ?_, ?_, ?_⟩
    · intro isTick
      cases action with
      | tick duration => exact absurd rfl (isNotTick duration)
      | _ => cases isTick
    · intro _
      cases action with
      | tick duration => exact absurd rfl (isNotTick duration)
      | request request =>
        rw [← isEvents] at dexRequests
        cases dexRequests
      | stepStart stepId =>
        obtain ⟨canCommit, isStamp⟩ := dex_step_stamp ⟦program⟧ dexConfig (.stepStart stepId)
          dexEvents dexNext isDexStep
        exact (isNotCommit isStamp canCommit).elim
      | stepComplete taskId =>
        obtain ⟨canCommit, isStamp⟩ := dex_step_stamp ⟦program⟧ dexConfig (.stepComplete taskId)
          dexEvents dexNext isDexStep
        exact (isNotCommit isStamp canCommit).elim
      | timerFire count =>
        obtain ⟨canCommit, isStamp⟩ := dex_step_stamp ⟦program⟧ dexConfig (.timerFire count)
          dexEvents dexNext isDexStep
        exact (isNotCommit isStamp canCommit).elim
      | workerEffect taskId =>
        exact absurd isEvents.symm
          (dex_step_workerEffect_events ⟦program⟧ dexConfig taskId dexEvents dexNext isDexStep)
      | workerRun taskId => rfl
      | workerReturn taskId => rfl
      | workerAbandon taskId => rfl
      | stepCompleteStale taskId => rfl
    · rw [← dexRequests, ← isEvents]
      rfl
  · -- One L label.
    have isLStep : L.step program lConfig label = some (dexEvents.filterMap project, lNext) :=
      (L.system program).step_of_exec_singleton lConfig label _ lNext isExec
    have lRequests := l_step_encodedRequests program lConfig label _ lNext isLStep
    have sameProgress :
        (L.system program).isProgress label = (Dex.system ⟦program⟧).isProgress action := by
      cases lProgress : (L.system program).isProgress label <;>
        cases dexProgress : (Dex.system ⟦program⟧).isProgress action
      · rfl
      · -- An idle tick moves the clock; a Dex progress step does not.
        exfalso
        cases label with
        | tick duration =>
          obtain ⟨isPositive, _, isClock⟩ :=
            l_step_tick program lConfig duration _ lNext isLStep
          have sameClock := dex_step_clock ⟦program⟧ dexConfig action dexEvents dexNext isDexStep
            dexProgress
          omega
        | _ => cases lProgress
      · -- A Dex tick has no event, no commit, and moves the clock.
        exfalso
        cases action with
        | tick duration =>
          obtain ⟨isPositive, isNoEvent, isClock, isLastCommit⟩ :=
            dex_step_tick ⟦program⟧ dexConfig duration dexEvents dexNext isDexStep
          subst isNoEvent
          rcases l_step_shape program lConfig label _ lNext isLStep lProgress with
            hasEvent | ⟨canStamp, isStamp⟩ | sameClock
          · exact hasEvent rfl
          · rw [canStamp_eq_false_of_lastStampAt lConfig lNext.clock
              (by rw [stampBefore, ← isLastCommit, ← stampAfter, isStamp]) (by omega)] at canStamp
            cases canStamp
          · omega
        | _ => cases dexProgress
      · rfl
    have isSingle : [label].filterMap (L.system program).requestOf =
        ((L.system program).requestOf label).toList := by
      simp only [List.filterMap_cons, List.filterMap_nil]
      cases (L.system program).requestOf label <;> rfl
    refine ⟨?_, fun _ => (by simp), fun isEmpty => (by cases isEmpty), ?_⟩
    · intro member isMember
      rw [List.mem_singleton.mp isMember]
      exact sameProgress
    · rw [isSingle, lRequests, dexRequests]
  · simp only [List.length_cons] at atMostOne
    omega

/-- In a run for the Dex form of an admissible word, every action is one an
admissible run can take: the start comes first, so every publication finds
the Flow started (spec §5.3). -/
theorem isAdmissibleAction_of_isRunFor (program : Program) (word : Sequence L.Request)
    (isAdmissible : L.IsAdmissibleWord word)
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun)
    (isRunFor : (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest))
    (index : Nat) : IsAdmissibleAction (dexRun.states index) (dexRun.labels index) := by
  have isNext := (Dex.system ⟦program⟧).requestAt_of_isRunFor dexRun _ isRunFor index
  have hasRun := run_isSome_of_isRunFor program word isAdmissible dexRun isRun isRunFor index
  generalize dexRun.labels index = action at isNext
  cases action with
  | request request =>
    have isRequest := isNext request rfl
    cases lengthCase : (concatUpTo ((Dex.system ⟦program⟧).requestsAt dexRun) index).length with
    | zero =>
      rw [lengthCase] at isRequest
      obtain ⟨input, isStart⟩ := encodedWord_zero word isAdmissible
      rw [isStart] at isRequest
      injection isRequest with isRequest
      subst isRequest
      exact ⟨rfl, input, rfl⟩
    | succ previous =>
      rw [lengthCase] at isRequest
      obtain ⟨chan, literal, isPublication⟩ :=
        encodedWord_succ word isAdmissible previous request isRequest
      subst isPublication
      refine ⟨?_, chan, literal, rfl, rfl⟩
      intro hasNoRun
      have isSome := hasRun.mpr (by omega)
      rw [hasNoRun] at isSome
      cases isSome
  | _ => trivial

end DexExpressiveness
