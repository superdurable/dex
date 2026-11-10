import DexExpressiveness.Liveness.Statements
import DexExpressiveness.Liveness.DexToLFairSteps
import DexExpressiveness.Liveness.DexToLFairLate

/-!
# Liveness, work package L4: an L fact and the Dex object it follows

The lemmas that `Liveness/DexToLFair.lean` puts together.

1. One simulation state. An L fact of a fairness assumption gives a Dex
   object: an armed timer (`timer_of_blocked`), a Step with a pending
   invocation (`pendingStep_of_thread`), or a task (`task_of_thread`). A task
   names its thread, and a thread has at most one task
   (`task_eq_of_thread`).
2. The L run. Every block of the L run has at most one label, so every
   position of the L run is the start of a block (`exists_boundary`), where
   the simulation state holds (`isState`). An L fact therefore fails at some
   position when it cannot hold at every block start from one on
   (`eventuallyFails_of_not_forever`).
3. Along the Dex run. While the L fact holds, the Dex object stays the same:
   the pending invocation (`pendingInvocation_forever`), the task
   (`task_forever`). After the close the effects that the tasks hold are the
   late emits, and they only shrink (`heldEffectCount_mono`,
   `heldEffectCount_lt`).

Everything here is proved.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The phases in which the action of a thread runs as a task. -/
def runsAsTask : L.Phase → Bool
  | .bootRunning => true
  | .running _ => true
  | .emitting _ => true
  | .emitted => true
  | .waitStarted => true
  | .resuming _ => true
  | _ => false

/-- The number of effects that the tasks of `Θ` have not emitted. -/
def heldEffectCount (program : Program) (config : Dex.Config signature) : Nat :=
  (config.tasks.flatMap (pendingEffects ⟦program⟧)).length

/-! ## One simulation state -/

section State

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}

/-- The timer of a thread blocked in a timed operation is armed in the Flow,
with the thread's deadline (F2). -/
theorem IsSimulationState.timer_of_blocked (state : IsSimulationState program lConfig dexConfig)
    {thread : Tid} {current : L.Thread} (isFound : lConfig.findThread? thread = some current)
    {number deadline since : Nat} {chan : Option ExtChan}
    (isBlocked : current.phase = .blocked (.timed number deadline chan) since) :
    ∃ run, dexConfig.run = some run ∧
      run.timers.get? { step := { stepType := StepType.timed, number := number }, index := 0 } =
        some deadline := by
  obtain ⟨isMember, _⟩ := mem_of_findThread? isFound
  obtain ⟨_, run, hasRun, isRunning⟩ := state.run_of_thread current isMember
  exact ⟨run, hasRun, ((state.isRelated.running run hasRun isRunning).timers _ deadline).mpr
    ⟨current, chan, since, isMember, isBlocked, rfl, rfl⟩⟩

/-- A thread whose next action has not begun is carried by a Step that does
not wait and has a pending invocation (F3). -/
theorem IsSimulationState.pendingStep_of_thread
    (state : IsSimulationState program lConfig dexConfig)
    {thread : Tid} {current : L.Thread} (isFound : lConfig.findThread? thread = some current)
    (isPending : current.phase = .bootPending ∨ current.phase = .ready ∨
      ∃ outcome, current.phase = .woken outcome) :
    ∃ run record method, dexConfig.run = some run ∧ run.status = .running ∧
      record ∈ run.steps ∧ run.findStep? record.id = some record ∧
      record.invocation = some { method := method, state := .pending } ∧
      record.phase ≠ .waiting ∧ threadOfInput record.input = thread := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  obtain ⟨_, run, hasRun, isRunning⟩ := state.run_of_thread current isMember
  have related := state.isRelated.running run hasRun isRunning
  have isLive : current.phase ≠ .halted := by
    rcases isPending with isPhase | isPhase | ⟨outcome, isPhase⟩ <;> rw [isPhase] <;> simp
  obtain ⟨record, isRecord, isActive, carries, phaseMatches⟩ :=
    related.threadsHaveSteps current isMember isLive
  have isRecordFound := Dex.Run.findStep?_of_mem_nodup run record isRecord
    (state.dexInvariant.runInvariant run hasRun).idsNodup
  have sameThread : threadOfInput record.input = thread :=
    (threadOfInput_of_mem state.lInvariant isMember carries).trans hasTid
  rcases isPending with isPhase | isPhase | ⟨outcome, isPhase⟩
  · simp only [PhaseMatchesStep, isPhase] at phaseMatches
    refine ⟨run, record, .execute, hasRun, isRunning, isRecord, isRecordFound, phaseMatches.2,
      ?_, sameThread⟩
    rw [phaseMatches.1]
    simp
  · simp only [PhaseMatchesStep, isPhase] at phaseMatches
    refine ⟨run, record, _, hasRun, isRunning, isRecord, isRecordFound, phaseMatches.2.2,
      ?_, sameThread⟩
    rw [phaseMatches.1]
    split <;> simp
  · simp only [PhaseMatchesStep, isPhase] at phaseMatches
    refine ⟨run, record, .execute, hasRun, isRunning, isRecord, isRecordFound, phaseMatches.2.1,
      ?_, sameThread⟩
    rw [phaseMatches.1]
    simp

/-- A thread whose action runs has a task. The task holds the emit that the
thread issued, and no other effect (F4, F5). -/
theorem IsSimulationState.task_of_thread (state : IsSimulationState program lConfig dexConfig)
    {thread : Tid} {current : L.Thread} (isFound : lConfig.findThread? thread = some current)
    (isTaskPhase : runsAsTask current.phase = true) :
    ∃ run task, dexConfig.run = some run ∧ run.status = .running ∧ task ∈ dexConfig.tasks ∧
      threadOfInput task.input = thread ∧
      (∀ effect, current.phase = .emitting effect → pendingEffects ⟦program⟧ task = [effect]) ∧
      ((∀ effect, current.phase ≠ .emitting effect) → pendingEffects ⟦program⟧ task = []) := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  obtain ⟨_, run, hasRun, isRunning⟩ := state.run_of_thread current isMember
  have related := state.isRelated.running run hasRun isRunning
  have isLive : current.phase ≠ .halted := by
    intro isHalted
    simp [runsAsTask, isHalted] at isTaskPhase
  obtain ⟨record, isRecord, isActive, carries, phaseMatches⟩ :=
    related.threadsHaveSteps current isMember isLive
  have sameThread : threadOfInput record.input = thread :=
    (threadOfInput_of_mem state.lInvariant isMember carries).trans hasTid
  cases phaseCase : current.phase with
  | bootRunning =>
    simp only [PhaseMatchesStep, phaseCase] at phaseMatches
    obtain ⟨_, task, isTask, _, _, _, sameInput, isEmpty⟩ := phaseMatches
    exact ⟨run, task, hasRun, isRunning, isTask, by rw [sameInput]; exact sameThread,
      nofun, fun _ => isEmpty⟩
  | running loaded =>
    simp only [PhaseMatchesStep, phaseCase] at phaseMatches
    obtain ⟨_, task, isTask, _, _, _, sameInput, isEmpty, _⟩ := phaseMatches
    exact ⟨run, task, hasRun, isRunning, isTask, by rw [sameInput]; exact sameThread,
      nofun, fun _ => isEmpty⟩
  | emitting effect =>
    simp only [PhaseMatchesStep, phaseCase] at phaseMatches
    obtain ⟨_, task, isTask, _, _, _, sameInput, isHeld, _⟩ := phaseMatches
    refine ⟨run, task, hasRun, isRunning, isTask, by rw [sameInput]; exact sameThread, ?_, ?_⟩
    · intro other isEmitting
      cases isEmitting
      exact isHeld
    · intro isNotEmitting
      exact absurd rfl (isNotEmitting effect)
  | emitted =>
    simp only [PhaseMatchesStep, phaseCase] at phaseMatches
    obtain ⟨_, task, isTask, _, _, _, sameInput, isEmpty, _⟩ := phaseMatches
    exact ⟨run, task, hasRun, isRunning, isTask, by rw [sameInput]; exact sameThread,
      nofun, fun _ => isEmpty⟩
  | waitStarted =>
    simp only [PhaseMatchesStep, phaseCase] at phaseMatches
    obtain ⟨_, task, isTask, _, _, _, sameInput, isEmpty⟩ := phaseMatches
    exact ⟨run, task, hasRun, isRunning, isTask, by rw [sameInput]; exact sameThread,
      nofun, fun _ => isEmpty⟩
  | resuming outcome =>
    simp only [PhaseMatchesStep, phaseCase] at phaseMatches
    obtain ⟨_, task, isTask, _, _, _, sameInput, isEmpty, _⟩ := phaseMatches
    exact ⟨run, task, hasRun, isRunning, isTask, by rw [sameInput]; exact sameThread,
      nofun, fun _ => isEmpty⟩
  | bootPending => simp [runsAsTask, phaseCase] at isTaskPhase
  | ready => simp [runsAsTask, phaseCase] at isTaskPhase
  | blocked on since => simp [runsAsTask, phaseCase] at isTaskPhase
  | woken outcome => simp [runsAsTask, phaseCase] at isTaskPhase
  | halted => simp [runsAsTask, phaseCase] at isTaskPhase

/-- While the Flow runs, every task runs the invocation of an active Step
with the task's input. -/
theorem IsSimulationState.step_of_task (state : IsSimulationState program lConfig dexConfig)
    {run : Dex.Run signature} (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) {task : Dex.Task signature}
    (isTask : task ∈ dexConfig.tasks) :
    ∃ record, record ∈ run.steps ∧ record.phase.isActive = true ∧
      record.invocation = some { method := task.method, state := .running task.id } ∧
      record.input = task.input := by
  have related := state.isRelated.running run hasRun isRunning
  obtain ⟨record, recordFound, hasInvocation⟩ :=
    Dex.Config.findStep?_of_isTaskCurrent dexConfig task run hasRun
      (related.tasksAreCurrent task isTask)
  have isRecord := (Dex.Run.mem_of_findStep? run _ record recordFound).1
  have isActive := related.invocationsAreActive record isRecord (by rw [hasInvocation]; simp)
  obtain ⟨thread, isThread, isLive, carries⟩ := related.stepsHaveThreads record isRecord isActive
  have phaseMatches :=
    related.phaseMatchesStep_of_carries thread isThread isLive record isRecord isActive carries
  refine ⟨record, isRecord, isActive, hasInvocation, ?_⟩
  have ofRunning : ∀ method Holds, HasRunningTask dexConfig record method Holds →
      record.input = task.input := by
    intro method Holds ⟨other, isOther, otherInvocation, _, _, otherInput, _⟩
    rw [hasInvocation] at otherInvocation
    have sameId : other.id = task.id := by
      simp only [Option.some.injEq, Dex.Invocation.mk.injEq,
        Dex.InvocationState.running.injEq] at otherInvocation
      exact otherInvocation.2.symm
    have sameTask : other = task :=
      eq_of_nodup_map (fun candidate => candidate.id) dexConfig.tasks state.tasksDistinct.1
        isOther isTask sameId
    rw [← otherInput, sameTask]
  have ofOther : ∀ invocation : Option Dex.Invocation,
      (∀ method taskId, invocation ≠ some { method := method, state := .running taskId }) →
      record.invocation = invocation → record.input = task.input := by
    intro invocation isNotRunning isInvocation
    exact absurd (isInvocation.symm.trans hasInvocation) (isNotRunning _ _)
  cases phaseCase : thread.phase <;> simp only [PhaseMatchesStep, phaseCase] at phaseMatches
  · exact ofOther _ (by simp) phaseMatches.2
  · exact ofRunning _ _ phaseMatches.2
  · exact ofOther _ (by simp) phaseMatches.2.2
  · exact ofRunning _ _ phaseMatches.2
  · exact ofRunning _ _ phaseMatches.2
  · exact ofRunning _ _ phaseMatches.2
  · exact ofRunning _ _ phaseMatches.2
  · exact ofOther _ (by simp) phaseMatches.2.1
  · exact ofOther _ (by simp) phaseMatches.2.1
  · exact ofRunning _ _ phaseMatches.2

/-- While the Flow runs, a thread has at most one task. -/
theorem IsSimulationState.task_eq_of_thread (state : IsSimulationState program lConfig dexConfig)
    {run : Dex.Run signature} (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) {first second : Dex.Task signature}
    (isFirst : first ∈ dexConfig.tasks) (isSecond : second ∈ dexConfig.tasks)
    (sameThread : threadOfInput first.input = threadOfInput second.input) : first = second := by
  obtain ⟨firstRecord, isFirstRecord, isFirstActive, firstInvocation, firstInput⟩ :=
    state.step_of_task hasRun isRunning isFirst
  obtain ⟨secondRecord, isSecondRecord, isSecondActive, secondInvocation, secondInput⟩ :=
    state.step_of_task hasRun isRunning isSecond
  have sameRecord : firstRecord = secondRecord :=
    (state.isRelated.running run hasRun isRunning).activeStepsHaveDistinctThreads firstRecord
      isFirstRecord secondRecord isSecondRecord isFirstActive isSecondActive
      (by rw [firstInput, secondInput]; exact sameThread)
  subst sameRecord
  rw [firstInvocation] at secondInvocation
  have sameId : first.id = second.id := by
    simp only [Option.some.injEq, Dex.Invocation.mk.injEq,
      Dex.InvocationState.running.injEq] at secondInvocation
    exact secondInvocation.2
  exact eq_of_nodup_map (fun candidate => candidate.id) dexConfig.tasks state.tasksDistinct.1
    isFirst isSecond sameId

end State

/-! ## The L run -/

section Run

variable {program : Program}
  {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
  {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
  {boundary : Nat → Nat}

/-- Where a block starts, the L run and the Dex run are in a simulation state. -/
theorem LRunOfDexRun.isState (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (index : Nat) :
    IsSimulationState program (lRun.states (boundary index)) (dexRun.states index) := by
  rw [built.isFlattened.stateAtBoundary index]
  exact built.follows.isState index

/-- The Dex transition at `index`. -/
theorem LRunOfDexRun.dexStep (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (index : Nat) :
    ∃ events, Dex.step ⟦program⟧ (dexRun.states index) (dexRun.labels index) =
      some (events, dexRun.states (index + 1)) :=
  built.isDexRun.2 index

/-- Later blocks start later in the L run. -/
theorem LRunOfDexRun.boundary_mono (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    {first second : Nat} (isBefore : first ≤ second) : boundary first ≤ boundary second :=
  built.isFlattened.boundary_mono first second isBefore

/-- Every position of the L run is the start of a block: a block has at most
one label, and infinitely many blocks have one. -/
theorem LRunOfDexRun.exists_boundary (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (position : Nat) : ∃ index, boundary index = position := by
  have ofNonEmpty : ∀ index, lBlocks.blocks index ≠ [] →
      boundary (index + 1) = boundary index + 1 := by
    intro index isNonEmpty
    have isShort := built.follows.atMostOne index
    have isPositive : 0 < (lBlocks.blocks index).length := List.length_pos_iff.mpr isNonEmpty
    have isNext := built.isFlattened.nextBoundary index
    omega
  have nextBoundary : ∀ distance index, lBlocks.blocks (index + distance) ≠ [] →
      ∃ later, boundary later = boundary index + 1 := by
    intro distance
    induction distance with
    | zero =>
      intro index isNonEmpty
      exact ⟨index + 1, ofNonEmpty index isNonEmpty⟩
    | succ distance inductionHypothesis =>
      intro index isNonEmpty
      by_cases isEmpty : lBlocks.blocks index = []
      · have isShifted : index + 1 + distance = index + (distance + 1) := by omega
        obtain ⟨later, isLater⟩ := inductionHypothesis (index + 1) (by rw [isShifted]; exact isNonEmpty)
        refine ⟨later, ?_⟩
        rw [isLater, built.isFlattened.nextBoundary index, isEmpty]
        rfl
      · exact ⟨index + 1, ofNonEmpty index isEmpty⟩
  induction position with
  | zero => exact ⟨0, built.isFlattened.startsAtZero⟩
  | succ position inductionHypothesis =>
    obtain ⟨index, isIndex⟩ := inductionHypothesis
    obtain ⟨later, isLater, isNonEmpty⟩ := built.isInfinite index
    obtain ⟨distance, isDistance⟩ := Nat.exists_eq_add_of_le isLater
    subst isDistance
    obtain ⟨found, isFound⟩ := nextBoundary distance index isNonEmpty
    exact ⟨found, by rw [isFound, isIndex]⟩

/-- An L fact that implies a Dex fact at every block start fails when the Dex
fact does. -/
theorem LRunOfDexRun.eventuallyFails_of_imp
    (built : LRunOfDexRun program dexRun lBlocks lRun boundary) (lHolds dexHolds : Nat → Prop)
    (implies : ∀ index, lHolds (boundary index) → dexHolds index)
    (dexFails : EventuallyFails dexHolds) : EventuallyFails lHolds := by
  intro start holdsStart
  obtain ⟨index, isStart⟩ := built.exists_boundary start
  subst isStart
  obtain ⟨later, isLater, failsLater⟩ := dexFails index (implies index holdsStart)
  exact ⟨boundary later, built.boundary_mono isLater,
    fun holdsLater => failsLater (implies later holdsLater)⟩

/-- An L fact fails at some position when it cannot hold at every block start
from one on. -/
theorem LRunOfDexRun.eventuallyFails_of_not_forever
    (built : LRunOfDexRun program dexRun lBlocks lRun boundary) (lHolds : Nat → Prop)
    (isNotForever : ∀ index, (∀ later, index ≤ later → lHolds (boundary later)) → False) :
    EventuallyFails lHolds := by
  intro start holdsStart
  obtain ⟨index, isStart⟩ := built.exists_boundary start
  subst isStart
  apply Classical.byContradiction
  intro hasNoLater
  apply isNotForever index
  intro later isLater
  apply Classical.byContradiction
  intro failsLater
  exact hasNoLater ⟨boundary later, built.boundary_mono isLater, failsLater⟩

/-! ## Along the Dex run -/

/-- While a thread's next action has not begun, the invocation of the Step
that carries it stays pending (F3). -/
theorem LRunOfDexRun.pendingInvocation_forever
    (built : LRunOfDexRun program dexRun lBlocks lRun boundary) (thread : Tid) (index : Nat)
    (isAlways : ∀ later, index ≤ later →
      L.Fairness.IsThreadPending lRun thread (boundary later)) :
    ∃ stepId method, ∀ later, index ≤ later →
      Dex.Fairness.IsInvocationPending dexRun stepId method later := by
  obtain ⟨current, isFound, isPending⟩ := isAlways index (Nat.le_refl _)
  obtain ⟨run, record, method, hasRun, isRunning, isRecord, isRecordFound, hasInvocation,
    isNotWaiting, sameThread⟩ := (built.isState index).pendingStep_of_thread isFound isPending
  refine ⟨record.id, method, ?_⟩
  have isKept : ∀ distance, ∃ after, (dexRun.states (index + distance)).run = some after ∧
      after.findStep? record.id = some record := by
    intro distance
    induction distance with
    | zero => exact ⟨run, hasRun, isRecordFound⟩
    | succ distance inductionHypothesis =>
      obtain ⟨before, hasBefore, isBeforeFound⟩ := inductionHypothesis
      obtain ⟨events, isStep⟩ := built.dexStep (index + distance)
      obtain ⟨laterCurrent, isLaterFound, isLaterPending⟩ :=
        isAlways (index + distance + 1) (by omega)
      obtain ⟨laterRun, laterRecord, laterMethod, hasLaterRun, isLaterRunning, isLaterRecord, _,
        laterInvocation, _, laterSameThread⟩ :=
        (built.isState (index + distance + 1)).pendingStep_of_thread isLaterFound isLaterPending
      obtain ⟨after, hasAfter, isSame | ⟨status, isClosed⟩ | ⟨taskId, isStarted⟩⟩ :=
        Dex.pendingInvocation_step ⟦program⟧ _ _ _ events isStep before hasBefore record.id
          record isBeforeFound method hasInvocation isNotWaiting
      · exact ⟨after, hasAfter, isSame⟩
      · exfalso
        rw [hasAfter] at hasLaterRun
        cases hasLaterRun
        rw [isClosed] at isLaterRunning
        cases isLaterRunning
      · exfalso
        rw [hasAfter] at hasLaterRun
        cases hasLaterRun
        have related :=
          (built.isState (index + distance + 1)).isRelated.running _ hasAfter isLaterRunning
        have isStartedRecord := (Dex.Run.mem_of_findStep? _ _ _ isStarted).1
        have isStartedActive := related.invocationsAreActive _ isStartedRecord (by simp)
        have isLaterActive := related.invocationsAreActive laterRecord isLaterRecord
          (by rw [laterInvocation]; simp)
        have sameRecord := related.activeStepsHaveDistinctThreads _ isStartedRecord laterRecord
          isLaterRecord isStartedActive isLaterActive (by rw [laterSameThread]; exact sameThread)
        rw [← sameRecord] at laterInvocation
        simp at laterInvocation
  intro later isLater
  obtain ⟨distance, isDistance⟩ := Nat.exists_eq_add_of_le isLater
  subst isDistance
  obtain ⟨after, hasAfter, isAfterFound⟩ := isKept distance
  exact ⟨after, record, hasAfter, isAfterFound, hasInvocation⟩

/-- While the action of a thread runs, its task stays in `Θ`, and once the
task has returned it stays `Returned` (F4, F5). -/
theorem LRunOfDexRun.task_forever (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (thread : Tid) (index : Nat)
    (isAlways : ∀ later, index ≤ later → ∃ current,
      (lRun.states (boundary later)).findThread? thread = some current ∧
      runsAsTask current.phase = true)
    (taskId : Nat) (task : Dex.Task signature)
    (isFound : (dexRun.states index).findTask? taskId = some task)
    (isThreadTask : threadOfInput task.input = thread) :
    ∀ later, index ≤ later → ∃ phase,
      (dexRun.states later).findTask? taskId = some { task with phase := phase } ∧
      ∀ response, task.phase = .returned response → phase = .returned response := by
  have isKept : ∀ distance, ∃ phase,
      (dexRun.states (index + distance)).findTask? taskId = some { task with phase := phase } ∧
      ∀ response, task.phase = .returned response → phase = .returned response := by
    intro distance
    induction distance with
    | zero => exact ⟨task.phase, isFound, fun _ isReturned => isReturned⟩
    | succ distance inductionHypothesis =>
      obtain ⟨phase, isBeforeFound, staysReturned⟩ := inductionHypothesis
      obtain ⟨events, isStep⟩ := built.dexStep (index + distance)
      rcases findTask?_step ⟦program⟧ _ _ _ events isStep taskId _ isBeforeFound with
        ⟨nextPhase, isNextFound, nextStaysReturned⟩ | ⟨isDropped, isSubset⟩
      · exact ⟨nextPhase, isNextFound, fun response isReturned =>
          nextStaysReturned response (staysReturned response isReturned)⟩
      · exfalso
        obtain ⟨current, isThreadFound, isTaskPhase⟩ := isAlways (index + distance) (by omega)
        obtain ⟨laterCurrent, isLaterThreadFound, isLaterTaskPhase⟩ :=
          isAlways (index + distance + 1) (by omega)
        obtain ⟨run, _, hasRun, isRunning, _⟩ :=
          (built.isState (index + distance)).task_of_thread isThreadFound isTaskPhase
        obtain ⟨_, laterTask, _, _, isLaterTask, isLaterThreadTask, _⟩ :=
          (built.isState (index + distance + 1)).task_of_thread isLaterThreadFound
            isLaterTaskPhase
        obtain ⟨isBeforeMember, hasBeforeId⟩ := mem_of_findTask? _ taskId _ isBeforeFound
        have sameTask := (built.isState (index + distance)).task_eq_of_thread hasRun isRunning
          (isSubset laterTask isLaterTask) isBeforeMember
          (by rw [isLaterThreadTask]; exact isThreadTask.symm)
        have isRejected := List.find?_eq_none.mp isDropped laterTask isLaterTask
        rw [sameTask] at isRejected
        exact isRejected (decide_eq_true hasBeforeId)
  intro later isLater
  obtain ⟨distance, isDistance⟩ := Nat.exists_eq_add_of_le isLater
  subst isDistance
  exact isKept distance

/-- After the close, one Dex transition keeps the run, and the tasks hold no
more effects than before; a task that holds an effect still holds one, or the
tasks hold fewer effects than before. -/
theorem LRunOfDexRun.closedStep (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (index : Nat) (run : Dex.Run signature) (status : Dex.CloseStatus)
    (hasRun : (dexRun.states index).run = some run) (isClosed : run.status = .closed status) :
    (dexRun.states (index + 1)).run = some run ∧
    heldEffectCount program (dexRun.states (index + 1)) ≤
      heldEffectCount program (dexRun.states index) ∧
    ∀ taskId task, (dexRun.states index).findTask? taskId = some task →
      pendingEffects ⟦program⟧ task ≠ [] →
      (∃ later, (dexRun.states (index + 1)).findTask? taskId = some later ∧
        pendingEffects ⟦program⟧ later ≠ []) ∨
      heldEffectCount program (dexRun.states (index + 1)) <
        heldEffectCount program (dexRun.states index) := by
  have state := built.isState index
  have closed := state.isRelated.closed run status hasRun isClosed
  obtain ⟨events, isStep⟩ := built.dexStep index
  obtain ⟨sameRun, isNotStart⟩ := closed_step ⟦program⟧ _ _ _ events isStep run hasRun status
    isClosed closed.noInvocation closed.tasksAreStale closed.noTimer
  exact ⟨sameRun, heldEffects_step ⟦program⟧ _ _ _ events isStep state.tasksDistinct isNotStart⟩

/-- A closed Flow stays closed, with the same run. -/
theorem LRunOfDexRun.closed_forever (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (index : Nat) (run : Dex.Run signature) (status : Dex.CloseStatus)
    (hasRun : (dexRun.states index).run = some run) (isClosed : run.status = .closed status)
    (later : Nat) (isLater : index ≤ later) : (dexRun.states later).run = some run := by
  obtain ⟨distance, isDistance⟩ := Nat.exists_eq_add_of_le isLater
  subst isDistance
  clear isLater
  induction distance with
  | zero => exact hasRun
  | succ distance inductionHypothesis =>
    exact (built.closedStep (index + distance) run status inductionHypothesis isClosed).1

/-- After the close, the late emits are as many as the effects that the tasks hold. -/
theorem LRunOfDexRun.lateEmits_length (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (index : Nat) (run : Dex.Run signature) (status : Dex.CloseStatus)
    (hasRun : (dexRun.states index).run = some run) (isClosed : run.status = .closed status) :
    (lRun.states (boundary index)).lateEmits.length =
      heldEffectCount program (dexRun.states index) :=
  ((built.isState index).isRelated.closed run status hasRun isClosed).lateEmits.length_eq.symm

/-- After the close, the tasks hold no more effects later than earlier. -/
theorem LRunOfDexRun.heldEffectCount_mono
    (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (index : Nat) (run : Dex.Run signature) (status : Dex.CloseStatus)
    (hasRun : (dexRun.states index).run = some run) (isClosed : run.status = .closed status)
    (distance : Nat) :
    heldEffectCount program (dexRun.states (index + distance)) ≤
      heldEffectCount program (dexRun.states index) := by
  induction distance with
  | zero => exact Nat.le_refl _
  | succ distance inductionHypothesis =>
    exact Nat.le_trans
      (built.closedStep (index + distance) run status
        (built.closed_forever index run status hasRun isClosed _ (by omega)) isClosed).2.1
      inductionHypothesis

/-- After the close, when a task that holds an effect later holds none or has
left `Θ`, the tasks hold fewer effects than before. -/
theorem LRunOfDexRun.heldEffectCount_lt
    (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (run : Dex.Run signature) (status : Dex.CloseStatus) (isClosed : run.status = .closed status)
    (taskId : Nat) (distance : Nat) :
    ∀ index, (dexRun.states index).run = some run →
      ∀ task, (dexRun.states index).findTask? taskId = some task →
      pendingEffects ⟦program⟧ task ≠ [] →
      (∀ later, (dexRun.states (index + distance)).findTask? taskId = some later →
        pendingEffects ⟦program⟧ later = []) →
      heldEffectCount program (dexRun.states (index + distance)) <
        heldEffectCount program (dexRun.states index) := by
  induction distance with
  | zero =>
    intro index hasRun task isFound isHolding isReleased
    exact absurd (isReleased task isFound) isHolding
  | succ distance inductionHypothesis =>
    intro index hasRun task isFound isHolding isReleased
    obtain ⟨hasNextRun, isNotMore, staysOrDrops⟩ :=
      built.closedStep index run status hasRun isClosed
    have isShifted : index + 1 + distance = index + (distance + 1) := by omega
    rcases staysOrDrops taskId task isFound isHolding with
      ⟨next, isNextFound, isNextHolding⟩ | isLess
    · have isLater := inductionHypothesis (index + 1) hasNextRun next isNextFound isNextHolding
        (by rw [isShifted]; exact isReleased)
      rw [isShifted] at isLater
      exact Nat.lt_of_lt_of_le isLater isNotMore
    · have isLater := built.heldEffectCount_mono (index + 1) run status hasNextRun isClosed distance
      rw [isShifted] at isLater
      exact Nat.lt_of_le_of_lt isLater isLess

end Run

end DexExpressiveness
