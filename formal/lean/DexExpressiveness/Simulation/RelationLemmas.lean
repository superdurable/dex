import DexExpressiveness.Simulation.Relation

/-!
# Consequences of the simulation relation

Facts that follow from the fixed definitions of `Simulation/Relation.lean` and
`Invariants.lean`, for the proofs of the step simulations. Everything here is
proved. The proofs of the work packages may add lemmas to this file.

- A task holds at most one effect, and a WaitFor task holds none
  (`pendingEffects_length_le_one`, `pendingEffects_eq_nil_of_waitFor`).
- The Step that carries a live thread is the one whose state matches the
  thread's phase (`RunningRelated.phaseMatchesStep_of_carries`). So a waiting
  Step has no invocation and carries a blocked thread
  (`RunningRelated.blocked_of_waiting`).
- The actions of `simulatingActions` hold the encoded request of the label and
  no other request, so `IsSimulatedByActions` implies `IsSimulatedByDex`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Tasks hold at most one effect -/

/-- A task of a translated Flow holds at most one effect it has not emitted. -/
theorem pendingEffects_length_le_one (program : Program) (task : Dex.Task signature)
    (isComputed : IsTaskComputed program task) :
    (pendingEffects ⟦program⟧ task).length ≤ 1 := by
  unfold pendingEffects
  split
  · exact (translate_compute_effects program task).1
  · rename_i remainingEffects response isPhase
    exact (isComputed.remainingEffects remainingEffects response isPhase).1
  · exact Nat.zero_le 1

/-- A WaitFor task of a translated Flow holds no effect. -/
theorem pendingEffects_eq_nil_of_waitFor (program : Program) (task : Dex.Task signature)
    (isComputed : IsTaskComputed program task) (isWaitFor : task.method = .waitFor) :
    pendingEffects ⟦program⟧ task = [] := by
  unfold pendingEffects
  split
  · exact (translate_compute_effects program task).2 isWaitFor
  · rename_i remainingEffects response isPhase
    exact (isComputed.remainingEffects remainingEffects response isPhase).2 isWaitFor
  · rfl

/-! ## The Step of a thread -/

/-- Two Steps that carry the same thread have the same input. -/
theorem StepCarriesThread.input_eq {program : Program} {thread : L.Thread}
    {first second : Dex.StepRecord signature}
    (carriesFirst : StepCarriesThread program thread first)
    (carriesSecond : StepCarriesThread program thread second) : first.input = second.input := by
  obtain ⟨core, code, timer, phase⟩ := thread
  rcases timer with _ | ⟨number, seconds⟩ <;> cases phase <;>
    simp only [StepCarriesThread] at carriesFirst carriesSecond
  all_goals first
    | (obtain ⟨_, firstInput, isFirst, firstLocals⟩ := carriesFirst
       obtain ⟨_, secondInput, isSecond, secondLocals⟩ := carriesSecond
       have sameLocals := firstLocals.symm.trans secondLocals
       simp only [List.cons.injEq, Prod.mk.injEq, true_and, and_true] at sameLocals
       rw [isFirst, isSecond, sameLocals])
    | rw [carriesFirst.2, carriesSecond.2]
    | rw [carriesFirst.2.1, carriesSecond.2.1]

/-- The Step that carries a live thread is the one whose state matches the
thread's phase. -/
theorem RunningRelated.phaseMatchesStep_of_carries {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads) (isLive : thread.phase ≠ .halted)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true)
    (carries : StepCarriesThread program thread record) :
    PhaseMatchesStep program lConfig dexConfig thread record := by
  obtain ⟨matching, isMatching, isMatchingActive, matchingCarries, phaseMatches⟩ :=
    related.threadsHaveSteps thread isThread isLive
  have isSame : record = matching :=
    related.activeStepsHaveDistinctThreads record isRecord matching isMatching isActive
      isMatchingActive (by rw [carries.input_eq matchingCarries])
  rw [isSame]
  exact phaseMatches

/-- A waiting Step has no invocation, and the thread it carries is blocked:
the Step waits (`WT`) exactly while its thread is blocked. -/
theorem RunningRelated.blocked_of_waiting {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isWaiting : record.phase = .waiting) :
    record.invocation = none ∧
    ∃ thread on since, thread ∈ lConfig.threads ∧ thread.phase = .blocked on since ∧
      StepCarriesThread program thread record := by
  have isActive : record.phase.isActive = true := by
    rw [isWaiting]
    rfl
  obtain ⟨thread, isThread, isLive, carries⟩ := related.stepsHaveThreads record isRecord isActive
  have phaseMatches :=
    related.phaseMatchesStep_of_carries thread isThread isLive record isRecord isActive carries
  cases phaseCase : thread.phase <;> simp only [PhaseMatchesStep, phaseCase] at phaseMatches
  all_goals first
    | exact ⟨phaseMatches.2.1, thread, _, _, isThread, phaseCase, carries⟩
    | (have isPhase := phaseMatches.1
       rw [isWaiting] at isPhase
       first
         | cases isPhase
         | (split at isPhase <;> cases isPhase))

/-! ## Facts about the Dex configuration of a simulation state -/

/-- A waiting Step has no invocation, while the Flow runs and after the close. -/
theorem IsSimulationState.waitingHasNoInvocation {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig) :
    ∀ run, dexConfig.run = some run → ∀ record, record ∈ run.steps →
      record.phase = .waiting → record.invocation = none := by
  intro run hasRun record isRecord isWaiting
  cases isStatus : run.status with
  | running =>
    exact ((state.isRelated.running run hasRun isStatus).blocked_of_waiting record isRecord
      isWaiting).1
  | closed status =>
    exact (state.isRelated.closed run status hasRun isStatus).noInvocation record isRecord

/-- The armed timers have distinct IDs. -/
theorem IsSimulationState.timerKeysNodup {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig) :
    ∀ run, dexConfig.run = some run → (run.timers.map (fun entry => entry.1)).Nodup :=
  state.timersDistinct

/-- Every task holds at most one effect it has not emitted. -/
theorem IsSimulationState.holdsAtMostOneEffect {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig) :
    ∀ task, task ∈ dexConfig.tasks → (pendingEffects ⟦program⟧ task).length ≤ 1 :=
  fun task isMember =>
    pendingEffects_length_le_one program task (state.dexInvariant.tasksComputed task isMember)

/-! ## The simulating actions hold the encoded request only -/

/-- The Worker step that runs a handler is no request. -/
theorem runTaskActions_requests (flow : Dex.FlowDef signature) (task : Dex.Task signature) :
    (runTaskActions task).filterMap (Dex.system flow).requestOf = [] := by
  unfold runTaskActions
  split <;> rfl

/-- The Worker steps that return a handler are no requests. -/
theorem returnTaskActions_requests (flow : Dex.FlowDef signature) (task : Dex.Task signature) :
    (returnTaskActions task).filterMap (Dex.system flow).requestOf = [] := by
  unfold returnTaskActions
  split <;> rfl

/-- The only request among the simulating actions of a label is the encoded
request of the label. -/
theorem simulatingActions_requests (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) :
    (simulatingActions program lConfig dexConfig label).filterMap
        (Dex.system ⟦program⟧).requestOf =
      ((L.system program).requestOf label).toList.map encodeRequest := by
  cases label with
  | tick duration => rfl
  | request request => rfl
  | fire count => rfl
  | thread thread =>
    show _ = []
    simp only [simulatingActions]
    split
    · split
      all_goals first
        | rfl
        | (rw [List.filterMap_append, runTaskActions_requests]; rfl)
        | (rw [List.filterMap_append, returnTaskActions_requests]; rfl)
    · rfl
  | lateDeliver index =>
    show _ = []
    simp only [simulatingActions]
    split
    · rfl
    · split
      · rw [List.filterMap_append, runTaskActions_requests]
        rfl
      · rfl
  | lateDrop index =>
    show _ = []
    simp only [simulatingActions]
    split
    · rfl
    · split <;> rfl

/-- A step that is simulated by the simulating actions is simulated by the
translated Flow. -/
theorem IsSimulatedByActions.isSimulatedByDex {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {label : L.Label} {lEvents : List Event}
    {lNext : L.Config}
    (isSimulated : IsSimulatedByActions program lConfig dexConfig label lEvents lNext) :
    IsSimulatedByDex program dexConfig label lEvents lNext := by
  obtain ⟨dexEvents, dexNext, isExec, sameEvents, isRelated⟩ := isSimulated
  exact ⟨_, dexEvents, dexNext, isExec, sameEvents,
    simulatingActions_requests program lConfig dexConfig label, isRelated⟩

end DexExpressiveness
