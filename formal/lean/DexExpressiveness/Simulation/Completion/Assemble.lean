import DexExpressiveness.Simulation.Completion.Exec
import DexExpressiveness.Simulation.Completion.Start
import DexExpressiveness.Simulation.Completion.Decide
import DexExpressiveness.Simulation.Completion.EffectsCancel

/-!
# From `IsSimulationState` to the parts of a completion, and back to `Related`

- `Completing`: the facts a completion starts from: the thread, the Step that
  carries it, and the task that runs its Execute with no effect left.
- `Completing.exec`: the completion actions run.
- `wp3b_begin_update`, `wp3b_begin_drop`: the relation with the committing
  thread left out, right after the commit released the invocation.
- `wp3b_related_of_running`: `Related` from `RunningRelated` after the commit.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- What a completion starts from: thread `thread` is carried by the active
Step `record`, whose Execute runs as `task` and has no effect left to emit. -/
structure Completing (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (run : Dex.Run signature) (thread : L.Thread) (record : Dex.StepRecord signature)
    (task : Dex.Task signature) : Prop where
  /-- The run. -/
  hasRun : dexConfig.run = some run
  /-- The run is running. -/
  isRunning : run.status = .running
  /-- The thread. -/
  isThread : thread ∈ lConfig.threads
  /-- The Step. -/
  isRecord : record ∈ run.steps
  /-- The Step is active. -/
  isActive : record.phase.isActive = true
  /-- The Step carries the thread. -/
  carries : StepCarriesThread program thread record
  /-- The task. -/
  isTask : task ∈ dexConfig.tasks
  /-- The Step's Execute runs as the task. -/
  hasInvocation : record.invocation = some { method := .execute, state := .running task.id }
  /-- The task is the Step's. -/
  hasStep : task.step = record.id
  /-- The task runs Execute. -/
  isExecute : task.method = .execute
  /-- The task has emitted every effect. -/
  noEffects : pendingEffects ⟦program⟧ task = []
  /-- The thread is not blocked. -/
  isNotBlocked : ∀ on since, thread.phase ≠ .blocked on since
  /-- The clock has advanced since the last stamped action. -/
  canStamp : lConfig.canStamp = true

/-- A running task of a Step, with what `Completing` needs. -/
theorem Completing.of_hasRunningTask {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature} {thread : L.Thread}
    {record : Dex.StepRecord signature} (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) (isThread : thread ∈ lConfig.threads)
    (isRecord : record ∈ run.steps) (isActive : record.phase.isActive = true)
    (carries : StepCarriesThread program thread record)
    (isNotBlocked : ∀ on since, thread.phase ≠ .blocked on since)
    (canStamp : lConfig.canStamp = true) (Holds : Dex.Task signature → Prop)
    (hasTask : HasRunningTask dexConfig record .execute Holds)
    (noEffects : ∀ task, Holds task → pendingEffects ⟦program⟧ task = []) :
    ∃ task, Completing program lConfig dexConfig run thread record task ∧ Holds task ∧
      task.input = record.input := by
  obtain ⟨task, isTask, hasInvocation, hasStep, isExecute, hasInput, holds⟩ := hasTask
  exact ⟨task, ⟨hasRun, isRunning, isThread, isRecord, isActive, carries, isTask, hasInvocation,
    hasStep, isExecute, noEffects task holds, isNotBlocked, canStamp⟩, holds, hasInput⟩

/-- The Dex configuration after the commit released the task: the task is
removed and the commit is stamped. The run is set separately (`withRun`). -/
def releasedConfig (dexConfig : Dex.Config signature) (taskId : Nat) : Dex.Config signature :=
  { dexConfig with
    tasks := dexConfig.tasks.filter (fun other => !decide (other.id = taskId))
    lastCommitAt := some dexConfig.clock }

/-- The L program is running when the run is. -/
theorem wp3b_status_running (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (related : Related program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) : lConfig.status = .running := by
  have status := related.status
  rw [hasRun] at status
  cases statusCase : lConfig.status with
  | running => rfl
  | notStarted => simp [statusCase] at status
  | closed =>
    simp only [statusCase] at status
    obtain ⟨closeStatus, isClosed⟩ := status
    rw [isRunning] at isClosed
    cases isClosed

/-- The completion actions run, and end in the configuration with the task
removed and the run after the commit. -/
theorem Completing.exec {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature} {thread : L.Thread}
    {record : Dex.StepRecord signature} {task : Dex.Task signature}
    (start : Completing program lConfig dexConfig run thread record task)
    (state : IsSimulationState program lConfig dexConfig) :
    (Dex.system ⟦program⟧).exec dexConfig (completionActions task) =
      some (Dex.Config.closeEvents run
          (completedRun run record.id (executeResponse program task)),
        withRun (releasedConfig dexConfig task.id)
          (completedRun run record.id (executeResponse program task))) := by
  have isFound : run.findStep? task.step = some record := by
    rw [start.hasStep]
    exact Dex.Run.findStep?_of_mem_nodup run record start.isRecord
      (state.dexInvariant.runInvariant run start.hasRun).idsNodup
  have isCurrent : dexConfig.isTaskCurrent task = true := by
    simp [Dex.Config.isTaskCurrent, start.hasRun, isFound, start.hasInvocation, start.isExecute]
  have canCommit : dexConfig.canCommit = true := by
    have canStamp := start.canStamp
    unfold L.Config.canStamp at canStamp
    unfold Dex.Config.canCommit
    rw [← state.isRelated.stamp, ← state.isRelated.clock]
    exact canStamp
  have isExec := wp3b_exec_completionActions program dexConfig run task start.hasRun start.isTask
    state.tasksDistinct (state.dexInvariant.tasksComputed task start.isTask) start.isExecute
    start.noEffects isCurrent canCommit
  rw [start.hasStep] at isExec
  exact isExec

/-- The threads after an update of the one thread with a given ID. -/
theorem wp3b_updateThread_spec (config : L.Config) (thread : L.Thread)
    (isThread : thread ∈ config.threads)
    (idsNodup : (config.threads.map (fun other => other.core.tid)).Nodup)
    (change : L.Thread → L.Thread) (keepsTid : (change thread).core.tid = thread.core.tid) :
    (∀ other, other.core.tid ≠ thread.core.tid →
      (other ∈ (config.updateThread thread.core.tid change).threads ↔ other ∈ config.threads)) ∧
    (∀ own, own ∈ (config.updateThread thread.core.tid change).threads →
      own.core.tid = thread.core.tid → own = change thread) ∧
    change thread ∈ (config.updateThread thread.core.tid change).threads ∧
    ((config.updateThread thread.core.tid change).threads.map
      (fun other => other.core.tid)).Nodup := by
  have sameThread : ∀ original, original ∈ config.threads →
      original.core.tid = thread.core.tid → original = thread := fun original isOriginal sameId =>
    eq_of_nodup_map (fun other => other.core.tid) config.threads idsNodup isOriginal isThread
      sameId
  refine ⟨?_, ?_, ?_, ?_⟩
  · intro other isOther
    rw [wp3b_mem_updateThread]
    constructor
    · rintro ⟨original, isOriginal, isChanged⟩
      by_cases isOwn : original.core.tid = thread.core.tid
      · simp only [isOwn, ↓reduceIte] at isChanged
        rw [sameThread original isOriginal isOwn] at isChanged
        rw [isChanged, keepsTid] at isOther
        exact absurd rfl isOther
      · simp only [isOwn, ↓reduceIte] at isChanged
        rw [isChanged]
        exact isOriginal
    · intro isMember
      exact ⟨other, isMember, by simp [isOther]⟩
  · intro own isOwn ownTid
    obtain ⟨original, isOriginal, isChanged⟩ := (wp3b_mem_updateThread _ _ _ _).mp isOwn
    by_cases isSame : original.core.tid = thread.core.tid
    · simp only [isSame, ↓reduceIte] at isChanged
      rw [isChanged, sameThread original isOriginal isSame]
    · simp only [isSame, ↓reduceIte] at isChanged
      rw [isChanged] at ownTid
      exact absurd ownTid isSame
  · exact (wp3b_mem_updateThread _ _ _ _).mpr ⟨thread, isThread, by simp⟩
  · have sameIds : (config.updateThread thread.core.tid change).threads.map
        (fun other => other.core.tid) = config.threads.map (fun other => other.core.tid) := by
      simp only [L.Config.updateThread, List.map_map]
      apply List.map_congr_left
      intro candidate isCandidate
      by_cases isOwn : candidate.core.tid = thread.core.tid
      · simp only [Function.comp, isOwn, ↓reduceIte]
        rw [sameThread candidate isCandidate isOwn]
        exact keepsTid
      · simp [isOwn]
    rw [sameIds]
    exact idsNodup

/-- The start of a completion whose thread continues, halts, or enters a
timed operation: the L configuration has the thread updated. -/
theorem wp3b_begin_update (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (waiting : WaitingStepsHaveNoInvocation dexConfig) (run : Dex.Run signature)
    (thread : L.Thread) (record : Dex.StepRecord signature) (task : Dex.Task signature)
    (start : Completing program lConfig dexConfig run thread record task)
    (change : L.Thread → L.Thread) (keepsTid : (change thread).core.tid = thread.core.tid)
    (changedNotBlocked : ∀ on since, (change thread).phase ≠ .blocked on since)
    (changedNotBoot : (change thread).phase ≠ .bootPending ∧ (change thread).phase ≠ .bootRunning)
    (counterKept : thread.core.freshCounter ≤ (change thread).core.freshCounter)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ []) :
    MidThreads program (lConfig.updateThread thread.core.tid change)
      (withRun (releasedConfig dexConfig task.id) (releasedRun run record.id))
      (releasedRun run record.id) thread.core.tid record.id ∧
    MidData (lConfig.updateThread thread.core.tid change) (releasedRun run record.id)
      thread.core.tid ∧
    change thread ∈ (lConfig.updateThread thread.core.tid change).threads := by
  obtain ⟨others, ownIsChanged, isChanged, nodupMid⟩ := wp3b_updateThread_spec lConfig thread
    start.isThread state.lInvariant.threadIdsNodup change keepsTid
  refine ⟨?_, ?_, isChanged⟩
  · exact wp3b_start_threads program lConfig _ dexConfig _ run state start.hasRun start.isRunning
      waiting thread start.isThread record start.isRecord start.isActive start.carries task
      start.isTask start.hasInvocation start.hasStep start.isNotBlocked others
      (fun own isOwn ownTid => by
        rw [ownIsChanged own isOwn ownTid]
        exact ⟨changedNotBlocked, changedNotBoot⟩)
      nodupMid rfl rfl rfl
  · exact wp3b_start_data program lConfig _ dexConfig run state start.hasRun start.isRunning
      thread start.isThread record.id ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl others
      (fun own isOwn ownTid => by
        rw [ownIsChanged own isOwn ownTid]
        exact counterKept)
      nodupMid (fun isFlagged => Or.inl (ownFlag isFlagged))

/-- The start of a completion whose thread ends: the L configuration has the
thread removed. -/
theorem wp3b_begin_drop (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (waiting : WaitingStepsHaveNoInvocation dexConfig) (run : Dex.Run signature)
    (thread : L.Thread) (record : Dex.StepRecord signature) (task : Dex.Task signature)
    (start : Completing program lConfig dexConfig run thread record task) :
    MidThreads program (dropThread lConfig thread.core.tid)
      (withRun (releasedConfig dexConfig task.id) (releasedRun run record.id))
      (releasedRun run record.id) thread.core.tid record.id ∧
    MidData (dropThread lConfig thread.core.tid) (releasedRun run record.id) thread.core.tid := by
  have others : ∀ other, other.core.tid ≠ thread.core.tid →
      (other ∈ (dropThread lConfig thread.core.tid).threads ↔ other ∈ lConfig.threads) := by
    intro other isOther
    rw [wp3b_mem_dropThread]
    exact ⟨fun isMember => isMember.1, fun isMember => ⟨isMember, isOther⟩⟩
  have nodupMid := wp3b_nodup_dropThread lConfig thread.core.tid state.lInvariant.threadIdsNodup
  have ownGone : ∀ own, own ∈ (dropThread lConfig thread.core.tid).threads →
      own.core.tid ≠ thread.core.tid := fun own isOwn =>
    ((wp3b_mem_dropThread lConfig thread.core.tid own).mp isOwn).2
  refine ⟨?_, ?_⟩
  · exact wp3b_start_threads program lConfig _ dexConfig _ run state start.hasRun start.isRunning
      waiting thread start.isThread record start.isRecord start.isActive start.carries task
      start.isTask start.hasInvocation start.hasStep start.isNotBlocked others
      (fun own isOwn ownTid => absurd ownTid (ownGone own isOwn)) nodupMid rfl rfl rfl
  · exact wp3b_start_data program lConfig _ dexConfig run state start.hasRun start.isRunning
      thread start.isThread record.id ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl others
      (fun own isOwn ownTid => absurd ownTid (ownGone own isOwn)) nodupMid
      (fun _ => Or.inr ((wp3b_findThread?_none _ _).mpr ownGone))

/-- `Related` after a commit that keeps the run running, from `RunningRelated`. -/
theorem wp3b_related_of_running (program : Program) (lConfig lNext : L.Config)
    (dexConfig dexNext : Dex.Config signature) (runNext : Dex.Run signature)
    (related : Related program lConfig dexConfig)
    (sameClock : lNext.clock = lConfig.clock)
    (isStamped : lNext.lastStampAt = some lConfig.clock)
    (isRunningL : lNext.status = .running) (dexClock : dexNext.clock = dexConfig.clock)
    (isCommitted : dexNext.lastCommitAt = some dexConfig.clock)
    (hasRunNext : dexNext.run = some runNext) (isRunningNext : runNext.status = .running)
    (running : RunningRelated program lNext dexNext runNext) :
    Related program lNext dexNext :=
  { clock := by rw [sameClock, dexClock]; exact related.clock
    stamp := by rw [isStamped, isCommitted, related.clock]
    status := by
      rw [isRunningL, hasRunNext]
      exact isRunningNext
    notStarted := fun noRun => by rw [hasRunNext] at noRun; cases noRun
    running := fun found isFound _ => by
      rw [hasRunNext] at isFound
      cases isFound
      exact running
    closed := fun found status isFound isClosed => by
      rw [hasRunNext] at isFound
      cases isFound
      rw [isRunningNext] at isClosed
      cases isClosed }

/-! ## The scalar fields of the L configuration across the atomic actions -/

/-- A release keeps the status and the clock. -/
theorem wp3b_scalars_releaseMutex (config : L.Config) (mutex : MutexId) :
    (config.releaseMutex mutex).status = config.status ∧
    (config.releaseMutex mutex).clock = config.clock := by
  simp only [L.Config.releaseMutex]
  split <;> exact ⟨rfl, rfl⟩

/-- The wake of a dequeued waiter keeps the status and the clock. -/
theorem wp3b_scalars_wakeCvWaiter (config : L.Config) (cv : CvId) (waiter : Tid) :
    (config.wakeCvWaiter cv waiter).status = config.status ∧
    (config.wakeCvWaiter cv waiter).clock = config.clock := by
  unfold L.Config.wakeCvWaiter
  repeat' split
  all_goals exact ⟨rfl, rfl⟩

/-- The delivery of a cancel keeps the status and the clock. -/
theorem wp3b_scalars_cancelBlockedThread (config : L.Config) (target : Tid) :
    (config.cancelBlockedThread target).status = config.status ∧
    (config.cancelBlockedThread target).clock = config.clock := by
  unfold L.Config.cancelBlockedThread
  repeat' split
  all_goals exact ⟨rfl, rfl⟩

end DexExpressiveness
