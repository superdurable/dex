import DexExpressiveness.Failure.Composite.WPF6Abstraction
import DexExpressiveness.Dex.RunInvariant

/-!
# WP-F6: the DexF step with no failure that follows a Dex step

The backward inclusion of `liveness_failure` follows a fair Dex run step by
step with a DexF run that has no failure. `NoFailure` is what such a DexF
configuration looks like: the server is up, nothing is in `Z`, `I`, or the
retry timers, no invocation is in Backoff or holds a retained snapshot, and
each invocation has at most one real task, whose ID, phase, and effect count
are those of its virtual task. On such a configuration α is transparent, and
every Dex step lifts to one DexF step (`liftAction`).
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- A DexF configuration that no failure reached. -/
structure NoFailure (program : Program σ) (config : Config σ) : Prop where
  /-- The server is up. -/
  isUp : config.isUp = true
  /-- No zombie task. -/
  noZombies : config.zombies = []
  /-- No interrupted attempt. -/
  noInterrupts : config.interrupts = []
  /-- No retry timer. -/
  noRetryTimers : ∀ run, config.run = some run → run.retryTimers = []
  /-- Every log commit is a normal commit. -/
  lastCommitIsNormal : config.lastCommitAt = config.ghost.lastNormalCommitAt
  /-- Real and virtual task IDs are minted together. -/
  nextTaskIdIsVirtual : config.nextTaskId = config.ghost.nextVirtualTaskId
  /-- No invocation is in Backoff. -/
  noBackoff : ∀ run stepId attempt, config.run = some run →
    run.findAttempt? stepId = some attempt → attempt.isBackoff = false
  /-- A pending invocation holds no snapshot. -/
  pendingHasNoSnapshot : ∀ run record method attempt, config.run = some run →
    record ∈ run.base.steps → record.invocation = some { method := method, state := .pending } →
    run.findAttempt? record.id = some attempt → attempt.snapshot = none
  /-- Each real task is its invocation's virtual task: the same ID, the same
  number of emitted effects, and the same phase. -/
  tasksMatchHistories : ∀ task, task ∈ config.realTasks →
    ∃ history arguments, config.ghost.findInvocation? task.key = some history ∧
      history.arguments = some arguments ∧ arguments.virtualTaskId = task.base.id ∧
      task.emittedCount = history.emittedCount ∧ task.base.phase = history.virtualPhase
  /-- No real task belongs to a committed invocation. -/
  tasksNotCommitted : ∀ task history, task ∈ config.realTasks →
    config.ghost.findInvocation? task.key = some history → history.outcome ≠ .committed
  /-- An invocation has at most one real task. -/
  oneTaskPerKey : ∀ first second, first ∈ config.realTasks → second ∈ config.realTasks →
    first.key = second.key → first.base.id = second.base.id
  /-- A live invocation that has started has its real task in `Θ`. -/
  liveHasTask : ∀ key history arguments, config.ghost.findInvocation? key = some history →
    history.outcome = .live → history.arguments = some arguments →
    ∃ task, task ∈ config.tasks ∧ task.key = key

/-- The DexF request of a Dex request: a StartFlow gets the request ID of the
start. -/
def liftRequest (startRequestId : Nat) : Dex.Request σ → Request σ
  | .startFlow stepType input => .startFlow startRequestId stepType input
  | .publish chan value => .publish chan value

/-- The DexF label with no failure that follows a Dex label. A stale result
is dropped by [WORKER-ABANDON], which takes no stamp. -/
def liftAction (startRequestId : Nat) : Dex.Action σ → Action σ
  | .tick duration => .tick duration
  | .request request => .request (liftRequest startRequestId request)
  | .stepStart stepId => .stepStart stepId false
  | .workerRun taskId => .workerRun taskId true
  | .workerEffect taskId => .workerEffect taskId true
  | .workerReturn taskId => .workerReturn taskId true
  | .workerAbandon taskId => .workerAbandon taskId true
  | .stepComplete taskId => .stepComplete taskId
  | .stepCompleteStale taskId => .workerAbandon taskId true
  | .timerFire count => .timerFire count true

/-! ## Helpers -/

/-- A binding found by `get?` is a member of the map. -/
theorem wpf6b_mem_of_get? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (isFound : AssocList.get? map key = some value) : (key, value) ∈ map := by
  induction map with
  | nil => cases isFound
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    simp only [AssocList.get?] at isFound
    by_cases isKey : entryKey = key
    · simp only [isKey, ↓reduceIte] at isFound
      cases isFound
      subst isKey
      exact List.mem_cons_self
    · simp only [isKey, ↓reduceIte] at isFound
      exact List.mem_cons_of_mem _ (inductionHypothesis isFound)

/-- With distinct keys, `get?` finds every member's value. -/
theorem wpf6b_get?_of_mem_nodup {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (keysNodup : (map.map (fun entry => entry.1)).Nodup)
    (isMember : (key, value) ∈ map) : AssocList.get? map key = some value := by
  induction map with
  | nil => cases isMember
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    simp only [List.map_cons, List.nodup_cons] at keysNodup
    simp only [AssocList.get?]
    rcases List.mem_cons.mp isMember with isHead | isRest
    · cases isHead
      simp
    · have isOther : entryKey ≠ key := by
        intro isSame
        subst isSame
        exact keysNodup.1 (List.mem_map.mpr ⟨_, isRest, rfl⟩)
      simp only [isOther, ↓reduceIte]
      exact inductionHypothesis keysNodup.2 isRest

/-- A map with no binding is empty. -/
theorem wpf6b_nil_of_get?_none {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (isEmpty : ∀ key, AssocList.get? map key = none) : map = [] := by
  cases map with
  | nil => rfl
  | cons entry rest =>
    have isHead := isEmpty entry.1
    simp [AssocList.get?] at isHead

/-- Insertion adds one element. -/
theorem wpf6b_length_insertSorted {α : Type} (isAtMost : α → α → Bool) (newElement : α)
    (elements : List α) :
    (insertSorted isAtMost newElement elements).length = elements.length + 1 := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [insertSorted]
    split
    · rfl
    · simp [inductionHypothesis]

/-- Sorting keeps the length. -/
theorem wpf6b_length_sortStable {α : Type} (isAtMost : α → α → Bool) (elements : List α) :
    (sortStable isAtMost elements).length = elements.length := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp [sortStable, wpf6b_length_insertSorted, inductionHypothesis]

/-- With no zombie, the real tasks are `Θ`. -/
theorem wpf6b_realTasks_eq (program : Program σ) (config : Config σ)
    (noFailure : NoFailure program config) : config.realTasks = config.tasks := by
  unfold Config.realTasks
  rw [noFailure.noZombies, List.append_nil]

/-- α of a Step record: the record found under α is the abstracted record. -/
theorem wpf6b_findStep?_abstract (ghost : Ghost σ) (run : Run σ) (stepId : Dex.StepExecId σ) :
    (run.abstract ghost).findStep? stepId = (run.base.findStep? stepId).map (fun record =>
      { record with
        invocation := run.abstractInvocation ghost record
        waitState := record.waitState.map (fun waitState =>
          { waitState with registeredSeq := ghost.normalIndexOf waitState.registeredSeq }) }) := by
  unfold Dex.Run.findStep? Run.abstract
  rw [List.find?_map]
  rfl

/-- With no failure, α may commit exactly when DexF may. -/
theorem wpf6b_canCommit_abstract (program : Program σ) (config : Config σ)
    (noFailure : NoFailure program config) :
    (abstractConfig config).canCommit = config.canCommit := by
  unfold Dex.Config.canCommit Config.canCommit abstractConfig
  rw [noFailure.lastCommitIsNormal]
  rfl

/-- With no failure, DexF may serve a commit exactly when α may commit. -/
theorem wpf6b_canServeCommit_abstract (program : Program σ) (config : Config σ)
    (noFailure : NoFailure program config) :
    config.canServeCommit = (abstractConfig config).canCommit := by
  unfold Config.canServeCommit
  rw [noFailure.isUp, wpf6b_canCommit_abstract program config noFailure, Bool.true_and]

/-- The members of the virtual task list. -/
theorem wpf6b_mem_virtualTasks (config : Config σ) (dexTask : Dex.Task σ)
    (isMember : dexTask ∈ config.virtualTasks) :
    ∃ entry arguments, entry ∈ config.ghost.invocations ∧ entry.2.arguments = some arguments ∧
      config.hasVirtualTask entry.1 entry.2 = true ∧
      dexTask = arguments.toTask entry.1 entry.2.virtualPhase := by
  unfold Config.virtualTasks at isMember
  rw [mem_sortStable, List.mem_filterMap] at isMember
  obtain ⟨entry, isEntry, isMapped⟩ := isMember
  cases hasArguments : entry.2.arguments with
  | none => rw [hasArguments] at isMapped; cases isMapped
  | some arguments =>
    rw [hasArguments] at isMapped
    simp only at isMapped
    split at isMapped
    · rename_i hasVirtual
      cases isMapped
      exact ⟨entry, arguments, isEntry, hasArguments, hasVirtual, rfl⟩
    · cases isMapped

/-- A ghost history kept as a member is what lookup finds. -/
theorem wpf6b_findInvocation?_of_mem (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) :
    config.ghost.findInvocation? entry.1 = some entry.2 :=
  wpf6b_get?_of_mem_nodup config.ghost.invocations entry.1 entry.2 invariant.ghostKeysNodup
    isMember

/-- The bridge from α to DexF: a task of α is the virtual task of the
history of a real task in `Θ` with the same ID and phase. -/
theorem wpf6b_realTask_of_abstractTask (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (virtualTaskId : Nat) (dexTask : Dex.Task σ)
    (isFound : (abstractConfig config).findTask? virtualTaskId = some dexTask) :
    ∃ task history arguments, task ∈ config.tasks ∧ task.base.id = virtualTaskId ∧
      config.ghost.findInvocation? task.key = some history ∧
      history.arguments = some arguments ∧ arguments.virtualTaskId = virtualTaskId ∧
      dexTask = arguments.toTask task.key history.virtualPhase ∧
      task.emittedCount = history.emittedCount ∧ task.base.phase = history.virtualPhase ∧
      config.findRealTask? virtualTaskId = some task ∧
      config.virtualTaskIdOf virtualTaskId = some virtualTaskId := by
  have isMember : dexTask ∈ config.virtualTasks := List.mem_of_find?_eq_some isFound
  have hasId : dexTask.id = virtualTaskId := by
    simpa using List.find?_some isFound
  obtain ⟨entry, arguments, isEntry, hasArguments, hasVirtual, isTask⟩ :=
    wpf6b_mem_virtualTasks config dexTask isMember
  have isHistory := wpf6b_findInvocation?_of_mem program config invariant entry isEntry
  have hasRealTask : ∃ task, task ∈ config.tasks ∧ task.key = entry.1 := by
    unfold Config.hasVirtualTask at hasVirtual
    rw [hasArguments] at hasVirtual
    cases isOutcome : entry.2.outcome with
    | live => exact noFailure.liveHasTask entry.1 entry.2 arguments isHistory isOutcome hasArguments
    | committed => rw [isOutcome] at hasVirtual; cases hasVirtual
    | canceled =>
      rw [isOutcome] at hasVirtual
      simp only [Option.isSome_some, Bool.true_and, List.any_eq_true, decide_eq_true_eq]
        at hasVirtual
      obtain ⟨task, isReal, hasKey⟩ := hasVirtual
      rw [wpf6b_realTasks_eq program config noFailure] at isReal
      exact ⟨task, isReal, hasKey⟩
  obtain ⟨task, isLive, hasKey⟩ := hasRealTask
  have isReal : task ∈ config.realTasks := by
    rw [wpf6b_realTasks_eq program config noFailure]; exact isLive
  obtain ⟨history, taskArguments, isTaskHistory, hasTaskArguments, isTaskId, sameCount, samePhase⟩ :=
    noFailure.tasksMatchHistories task isReal
  rw [hasKey, isHistory] at isTaskHistory
  cases isTaskHistory
  rw [hasArguments] at hasTaskArguments
  cases hasTaskArguments
  have isVirtualId : arguments.virtualTaskId = virtualTaskId := by
    rw [← hasId, isTask]; rfl
  have taskId : task.base.id = virtualTaskId := by rw [← isTaskId, isVirtualId]
  have isRealFound : config.findRealTask? virtualTaskId = some task :=
    (wpf6_findRealTask?_iff program config invariant virtualTaskId task).mpr ⟨isReal, taskId⟩
  refine ⟨task, entry.2, arguments, isLive, taskId, by rw [hasKey]; exact isHistory,
    hasArguments, isVirtualId, by rw [hasKey]; exact isTask, sameCount, samePhase,
    isRealFound, ?_⟩
  simp [Config.virtualTaskIdOf, isRealFound, Ghost.argumentsOf, hasKey, isHistory, hasArguments,
    isVirtualId]

/-- Lookup after a map that keeps every key. -/
theorem wpf6b_get?_map_keepsKey {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (transform : κ × ν → κ × ν) (keepsKey : ∀ entry, (transform entry).1 = entry.1) (key : κ) :
    AssocList.get? (map.map transform) key =
      (AssocList.get? map key).map (fun value => (transform (key, value)).2) := by
  induction map with
  | nil => rfl
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    rcases isTransformed : transform (entryKey, entryValue) with ⟨newKey, newValue⟩
    have sameKey : newKey = entryKey := by
      have kept := keepsKey (entryKey, entryValue)
      rw [isTransformed] at kept
      exact kept
    subst sameKey
    simp only [List.map_cons, isTransformed, AssocList.get?]
    by_cases isKey : newKey = key
    · subst isKey
      simp [isTransformed]
    · simp only [isKey, ↓reduceIte]
      exact inductionHypothesis

/-- Adding missing keys keeps every present binding. -/
theorem wpf6b_get?_foldl_insertMissing_of {κ ν : Type} [DecidableEq κ] (keys : List κ)
    (fresh : ν) (map : AssocList κ ν) (key : κ) (value : ν)
    (isFound : AssocList.get? map key = some value) :
    AssocList.get? (keys.foldl (fun (entries : AssocList κ ν) addedKey =>
      if entries.contains addedKey then entries else entries.set addedKey fresh) map) key =
      some value := by
  induction keys generalizing map with
  | nil => exact isFound
  | cons addedKey laterKeys inductionHypothesis =>
    simp only [List.foldl_cons]
    apply inductionHypothesis
    split
    · exact isFound
    · rename_i isMissing
      have isOther : key ≠ addedKey := by
        intro isSame
        subst isSame
        simp [AssocList.contains, isFound] at isMissing
      rw [AssocList.get?_set_other map addedKey key fresh isOther]
      exact isFound

/-- A binding after adding missing keys was present or is the fresh value. -/
theorem wpf6b_get?_foldl_insertMissing {κ ν : Type} [DecidableEq κ] (keys : List κ)
    (fresh : ν) (map : AssocList κ ν) (key : κ) (value : ν)
    (isFound : AssocList.get? (keys.foldl (fun (entries : AssocList κ ν) addedKey =>
      if entries.contains addedKey then entries else entries.set addedKey fresh) map) key =
      some value) :
    AssocList.get? map key = some value ∨ value = fresh := by
  induction keys generalizing map with
  | nil => exact Or.inl isFound
  | cons addedKey laterKeys inductionHypothesis =>
    simp only [List.foldl_cons] at isFound
    rcases inductionHypothesis _ isFound with isBefore | isFresh
    · split at isBefore
      · exact Or.inl isBefore
      · by_cases isSame : key = addedKey
        · subst isSame
          rw [AssocList.get?_set_self] at isBefore
          cases isBefore
          exact Or.inr rfl
        · rw [AssocList.get?_set_other map addedKey key fresh isSame] at isBefore
          exact Or.inl isBefore
    · exact Or.inr isFresh

/-- Adding missing keys after a key-keeping map keeps the mapped binding. -/
theorem wpf6b_get?_foldl_map {κ ν : Type} [DecidableEq κ] (keys : List κ) (fresh : ν)
    (map : AssocList κ ν) (transform : κ × ν → κ × ν)
    (keepsKey : ∀ entry, (transform entry).1 = entry.1) (key : κ) (value : ν)
    (isFound : AssocList.get? map key = some value) :
    AssocList.get? (keys.foldl (fun (entries : AssocList κ ν) addedKey =>
      if entries.contains addedKey then entries else entries.set addedKey fresh)
      (map.map transform)) key = some (transform (key, value)).2 := by
  apply wpf6b_get?_foldl_insertMissing_of
  rw [wpf6b_get?_map_keepsKey map transform keepsKey, isFound]
  rfl

/-- How a commit changes one ghost history: only its outcome, and only
from live to the outcome of the commit. -/
def wpf6b_IsReconciled (completed : Option (InvocationKey σ)) (key : InvocationKey σ)
    (before after : InvocationHistory σ) : Prop :=
  after.arguments = before.arguments ∧ after.progress = before.progress ∧
    after.emittedCount = before.emittedCount ∧
    (after.outcome = before.outcome ∨
      (before.outcome = .live ∧
        after.outcome = if completed = some key then .committed else .canceled))

/-- The marked history of a commit is reconciled. -/
theorem wpf6b_marked_isReconciled (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isFound : ghost.findInvocation? key = some history) :
    ∃ marked, AssocList.get? ((ghost.reconcile completed base).invocations) key = some marked ∧
      wpf6b_IsReconciled completed key history marked := by
  unfold Ghost.reconcile
  simp only
  refine ⟨_, wpf6b_get?_foldl_map _ _ _ _ (fun entry => by split <;> rfl) key history isFound, ?_⟩
  · simp only
    split
    · rename_i isMarked
      simp only [Bool.and_eq_true, decide_eq_true_eq] at isMarked
      exact ⟨rfl, rfl, rfl, Or.inr ⟨isMarked.1, rfl⟩⟩
    · exact ⟨rfl, rfl, rfl, Or.inl rfl⟩

/-- A history after a commit: the reconciled history of one before, or a
fresh one without arguments. -/
theorem wpf6b_reconcile_findInvocation? (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) (after : InvocationHistory σ)
    (isFound : (ghost.reconcile completed base).findInvocation? key = some after) :
    (∃ before, ghost.findInvocation? key = some before ∧
      wpf6b_IsReconciled completed key before after) ∨
    (ghost.findInvocation? key = none ∧ after.arguments = none) := by
  cases isBefore : ghost.findInvocation? key with
  | some before =>
    obtain ⟨marked, isMarked, isReconciled⟩ :=
      wpf6b_marked_isReconciled ghost completed base key before isBefore
    unfold Ghost.findInvocation? at isFound
    rw [isMarked] at isFound
    cases isFound
    exact Or.inl ⟨before, rfl, isReconciled⟩
  | none =>
    refine Or.inr ⟨rfl, ?_⟩
    unfold Ghost.findInvocation? Ghost.reconcile at isFound
    simp only at isFound
    rcases wpf6b_get?_foldl_insertMissing _ _ _ key after isFound with isMarked | isFresh
    · rw [wpf6b_get?_map_keepsKey] at isMarked
      · unfold Ghost.findInvocation? at isBefore
        rw [isBefore] at isMarked
        cases isMarked
      · intro entry
        split <;> rfl
    · rw [isFresh]
      rfl

/-- The history changed by `updateInvocation`. -/
theorem wpf6b_updateInvocation_self (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).findInvocation? key =
      (ghost.findInvocation? key).map change := by
  unfold Ghost.updateInvocation
  split
  · rename_i isNone
    rw [isNone]
    rfl
  · rename_i history isSome
    rw [isSome]
    exact AssocList.get?_set_self _ _ _

/-- The histories `updateInvocation` leaves alone. -/
theorem wpf6b_updateInvocation_other (ghost : Ghost σ) (key other : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) (isOther : other ≠ key) :
    (ghost.updateInvocation key change).findInvocation? other = ghost.findInvocation? other := by
  unfold Ghost.updateInvocation
  split
  · rfl
  · exact AssocList.get?_set_other _ _ _ _ isOther

/-- `updateInvocation` keeps the counters of the ghost. -/
theorem wpf6b_updateInvocation_fields (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).lastNormalCommitAt = ghost.lastNormalCommitAt ∧
      (ghost.updateInvocation key change).nextVirtualTaskId = ghost.nextVirtualTaskId := by
  unfold Ghost.updateInvocation
  split <;> exact ⟨rfl, rfl⟩

/-- One enabled Dex step is a run of one label. -/
theorem wpf6b_exec_single (flow : Dex.FlowDef σ) (dexConfig : Dex.Config σ)
    (dexAction : Dex.Action σ) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step flow dexConfig dexAction = some (dexEvents, dexNext)) :
    (Dex.system flow).exec dexConfig [dexAction] = some (dexEvents, dexNext) := by
  simp [TransitionSystem.exec, Dex.system, isDexStep]

/-- The simulation fixes α of the result and the erased events, once the
Dex actions of the step are known to run to a given result. -/
theorem wpf6b_lift_conclude (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isNotExhausting : action.isExhausting = false) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next))
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isExec : (Dex.system program.failureFreeFlow).exec (abstractConfig config)
      (dexActionsOf program config action) = some (dexEvents, dexNext)) :
    abstractConfig next = dexNext ∧ eraseStepEvents config events = dexEvents := by
  have isSimulated := step_simulated program ignoresAttemptContext config invariant action
    isNotExhausting events next isStep
  unfold IsStepSimulated at isSimulated
  rw [isExec] at isSimulated
  cases isSimulated
  exact ⟨rfl, rfl⟩

/-- Erasing events with no repeated `fx` only drops the ghost indexes. -/
theorem wpf6b_eraseStepEvents_of_fresh (config : Config σ) (events : List (Obs σ))
    (isFresh : ∀ stepId method effect index, Obs.fx stepId method effect index ∈ events →
      config.ghost.emittedCountOf (stepId, method) < index) :
    eraseStepEvents config events = events.map Obs.toDex := by
  induction events with
  | nil => rfl
  | cons event laterEvents inductionHypothesis =>
    have isLaterFresh : ∀ stepId method effect index,
        Obs.fx stepId method effect index ∈ laterEvents →
        config.ghost.emittedCountOf (stepId, method) < index :=
      fun stepId method effect index isMember =>
        isFresh stepId method effect index (List.mem_cons_of_mem _ isMember)
    unfold eraseStepEvents at inductionHypothesis ⊢
    rw [List.filterMap_cons, List.map_cons, ← inductionHypothesis isLaterFresh]
    cases event with
    | fx stepId method effect index =>
      have isAbove := isFresh stepId method effect index List.mem_cons_self
      simp only [Nat.not_le.mpr isAbove, ↓reduceIte]
      rfl
    | _ => rfl

/-- The events of a commit hold no `fx`. -/
theorem wpf6b_closeEvents_noFx (before after : Dex.Run σ) (stepId : Dex.StepExecId σ)
    (method : Dex.Method) (effect : σ.Fx) (index : Nat) :
    Obs.fx stepId method effect index ∉ Config.closeEvents before after := by
  unfold Config.closeEvents
  split <;> simp

/-- The fields of `NoFailure` about real tasks and ghost histories. -/
def wpf6b_GhostParts (config : Config σ) : Prop :=
  (∀ task, task ∈ config.realTasks →
    ∃ history arguments, config.ghost.findInvocation? task.key = some history ∧
      history.arguments = some arguments ∧ arguments.virtualTaskId = task.base.id ∧
      task.emittedCount = history.emittedCount ∧ task.base.phase = history.virtualPhase) ∧
  (∀ task history, task ∈ config.realTasks →
    config.ghost.findInvocation? task.key = some history → history.outcome ≠ .committed) ∧
  (∀ first second, first ∈ config.realTasks → second ∈ config.realTasks →
    first.key = second.key → first.base.id = second.base.id) ∧
  (∀ key history arguments, config.ghost.findInvocation? key = some history →
    history.outcome = .live → history.arguments = some arguments →
    ∃ task, task ∈ config.tasks ∧ task.key = key)

/-- The virtual phase reads only the progress and the emitted count. -/
theorem wpf6b_virtualPhase_congr (first second : InvocationHistory σ)
    (sameProgress : first.progress = second.progress)
    (sameCount : first.emittedCount = second.emittedCount) :
    first.virtualPhase = second.virtualPhase := by
  unfold InvocationHistory.virtualPhase
  rw [sameProgress, sameCount]

/-- The ghost parts after a commit that removes at most some real tasks. -/
theorem wpf6b_ghostParts_reconcile (program : Program σ) (config : Config σ)
    (noFailure : NoFailure program config) (next : Config σ) (counted : Ghost σ)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ)
    (sameInvocations : counted.invocations = config.ghost.invocations)
    (isGhost : next.ghost = counted.reconcile completed base)
    (realSubset : ∀ task, task ∈ next.realTasks → task ∈ config.realTasks)
    (notCompleted : ∀ task, task ∈ next.realTasks → completed ≠ some task.key)
    (liveKept : ∀ task history, task ∈ config.tasks →
      next.ghost.findInvocation? task.key = some history → history.outcome = .live →
      ∃ nextTask, nextTask ∈ next.tasks ∧ nextTask.key = task.key) :
    wpf6b_GhostParts next := by
  have sameFind : ∀ key, counted.findInvocation? key = config.ghost.findInvocation? key := by
    intro key
    unfold Ghost.findInvocation?
    rw [sameInvocations]
  refine ⟨?_, ?_, ?_, ?_⟩
  · intro task isReal
    obtain ⟨history, arguments, isHistory, hasArguments, isVirtualId, sameCount, samePhase⟩ :=
      noFailure.tasksMatchHistories task (realSubset task isReal)
    rw [← sameFind] at isHistory
    obtain ⟨after, isAfter, sameArguments, sameProgress, sameAfterCount, _⟩ :=
      wpf6b_marked_isReconciled counted completed base task.key history isHistory
    refine ⟨after, arguments, by rw [isGhost]; exact isAfter, by rw [sameArguments, hasArguments],
      isVirtualId, by rw [sameAfterCount, sameCount], ?_⟩
    rw [samePhase]
    exact wpf6b_virtualPhase_congr _ _ sameProgress.symm sameAfterCount.symm
  · intro task after isReal isAfter isCommitted
    rw [isGhost] at isAfter
    rcases wpf6b_reconcile_findInvocation? counted completed base task.key after isAfter with
      ⟨before, isBefore, _, _, _, sameOutcome⟩ | ⟨isNone, _⟩
    · rw [sameFind] at isBefore
      rcases sameOutcome with sameOutcome | ⟨_, isOutcome⟩
      · exact noFailure.tasksNotCommitted task before (realSubset task isReal) isBefore
          (by rw [← sameOutcome]; exact isCommitted)
      · rw [isCommitted] at isOutcome
        split at isOutcome
        · rename_i isCompleted
          exact notCompleted task isReal isCompleted
        · cases isOutcome
    · rw [sameFind] at isNone
      obtain ⟨history, _, isHistory, _⟩ := noFailure.tasksMatchHistories task (realSubset task isReal)
      rw [isNone] at isHistory
      cases isHistory
  · intro first second isFirst isSecond sameKey
    exact noFailure.oneTaskPerKey first second (realSubset first isFirst)
      (realSubset second isSecond) sameKey
  · intro key after arguments isAfter isLive hasArguments
    have isNextAfter := isAfter
    rw [isGhost] at isAfter
    rcases wpf6b_reconcile_findInvocation? counted completed base key after isAfter with
      ⟨before, isBefore, sameArguments, _, _, sameOutcome⟩ | ⟨_, noArguments⟩
    · rw [sameFind] at isBefore
      have wasLive : before.outcome = .live := by
        rcases sameOutcome with sameOutcome | ⟨wasLive, _⟩
        · rw [← sameOutcome]; exact isLive
        · exact wasLive
      obtain ⟨task, isTask, hasKey⟩ := noFailure.liveHasTask key before arguments isBefore wasLive
        (by rw [← sameArguments]; exact hasArguments)
      subst hasKey
      exact liveKept task after isTask isNextAfter isLive
    · rw [noArguments] at hasArguments
      cases hasArguments

/-- `NoFailure` after a step that is not a failure, from its parts that the
frame lemmas do not give. -/
theorem wpf6b_noFailure_of_parts (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (action : Action σ) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next))
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (isNotCrash : action ≠ .crash) (isNotRecover : action ≠ .recover)
    (noZombies : next.zombies = [])
    (lastCommitIsNormal : next.lastCommitAt = next.ghost.lastNormalCommitAt)
    (nextTaskIdIsVirtual : next.nextTaskId = next.ghost.nextVirtualTaskId)
    (ghostParts : wpf6b_GhostParts next) :
    NoFailure program next := by
  obtain ⟨tasksMatchHistories, tasksNotCommitted, oneTaskPerKey, liveHasTask⟩ := ghostParts
  have nextInvariant := invariant_step program ignoresAttemptContext config invariant action
    events next isStep
  refine
    { isUp := ?_
      noZombies := noZombies
      noInterrupts := ?_
      noRetryTimers := ?_
      lastCommitIsNormal := lastCommitIsNormal
      nextTaskIdIsVirtual := nextTaskIdIsVirtual
      noBackoff := ?_
      pendingHasNoSnapshot := ?_
      tasksMatchHistories := tasksMatchHistories
      tasksNotCommitted := tasksNotCommitted
      oneTaskPerKey := oneTaskPerKey
      liveHasTask := liveHasTask }
  · rw [(wpf6_isUp_step program config action events next isStep).1 isNotCrash isNotRecover]
    exact noFailure.isUp
  · apply List.eq_nil_iff_forall_not_mem.mpr
    intro interrupt isMember
    have wasMember := wpf6_interrupts_step program config action events next isStep isNotRecover
      interrupt isMember
    rw [noFailure.noInterrupts] at wasMember
    cases wasMember
  · intro nextRun hasNextRun
    apply wpf6b_nil_of_get?_none
    intro timer
    cases isArmed : nextRun.retryTimers.get? timer with
    | none => rfl
    | some fireTime =>
      obtain ⟨run, hasRun, wasArmed⟩ := wpf6_retryTimer_step program config invariant action
        events next isStep isNotFailed isNotInterrupt nextRun hasNextRun timer fireTime isArmed
      rw [noFailure.noRetryTimers run hasRun] at wasArmed
      cases wasArmed
  · intro nextRun stepId nextAttempt hasNextRun isNextAttempt
    cases isBackoff : nextAttempt.isBackoff with
    | false => rfl
    | true =>
      obtain ⟨run, attempt, hasRun, isAttempt, wasBackoff, _⟩ := wpf6_backoff_step program config
        invariant action events next isStep isNotFailed isNotInterrupt nextRun hasNextRun stepId
        nextAttempt isNextAttempt isBackoff
      rw [noFailure.noBackoff run stepId attempt hasRun isAttempt] at wasBackoff
      cases wasBackoff
  · intro nextRun record method attempt hasNextRun isMember isPending isAttempt
    cases isSnapshot : attempt.snapshot with
    | none => rfl
    | some snapshot =>
      exfalso
      have runInvariant := nextInvariant.runInvariant nextRun hasNextRun
      have isMatched := runInvariant.attemptsMatch record isMember
      unfold AttemptMatchesRecord at isMatched
      rw [isPending] at isMatched
      obtain ⟨matchedAttempt, history, isMatchedAttempt, isHistory, isLive, _, _, _,
        snapshotMatches, _⟩ := isMatched
      rw [isAttempt] at isMatchedAttempt
      cases isMatchedAttempt
      rw [isSnapshot] at snapshotMatches
      cases hasArguments : history.arguments with
      | none => rw [hasArguments] at snapshotMatches; exact snapshotMatches
      | some arguments =>
        obtain ⟨task, isTask, hasKey⟩ :=
          liveHasTask (record.id, method) history arguments isHistory isLive hasArguments
        have isPassed := nextInvariant.liveTasksPassFence task history isTask
          (by rw [hasKey]; exact isHistory) isLive
        have isStepId : task.base.step = record.id := congrArg Prod.fst hasKey
        have isMethod : task.base.method = method := congrArg Prod.snd hasKey
        unfold Config.isFencePassed at isPassed
        rw [hasNextRun] at isPassed
        simp only at isPassed
        rw [isStepId, Dex.Run.findStep?_of_mem_nodup nextRun.base record isMember
          runInvariant.stepIdsNodup, isAttempt] at isPassed
        simp only [isPending, isMethod] at isPassed
        simp at isPassed

/-- What `wpf6_lift_step` asserts for one lifted label. -/
def wpf6b_LiftResult (program : Program σ) (config : Config σ) (action : Action σ)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ) : Prop :=
  ∃ events next, step program config action = some (events, next) ∧
    abstractConfig next = dexNext ∧ dexEvents = events.map Obs.toDex ∧
    eraseStepEvents config events = events.map Obs.toDex ∧ NoFailure program next

/-- The lift of one label, once the step, its Dex run, its fresh events, and
`NoFailure` of the result are known. -/
theorem wpf6b_lift_finish (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isNotExhausting : action.isExhausting = false) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next))
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isExec : (Dex.system program.failureFreeFlow).exec (abstractConfig config)
      (dexActionsOf program config action) = some (dexEvents, dexNext))
    (isFresh : ∀ stepId method effect index, Obs.fx stepId method effect index ∈ events →
      config.ghost.emittedCountOf (stepId, method) < index)
    (nextNoFailure : NoFailure program next) :
    wpf6b_LiftResult program config action dexEvents dexNext := by
  obtain ⟨isAbstract, isErased⟩ := wpf6b_lift_conclude program ignoresAttemptContext config
    invariant action isNotExhausting events next isStep dexEvents dexNext isExec
  have isPlain := wpf6b_eraseStepEvents_of_fresh config events isFresh
  exact ⟨events, next, isStep, isAbstract, by rw [← isErased, isPlain], isPlain, nextNoFailure⟩

/-- The lift of a normal commit that keeps the real tasks. -/
theorem wpf6b_lift_commit (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (action : Action σ) (isNotExhausting : action.isExhausting = false)
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (isNotCrash : action ≠ .crash) (isNotRecover : action ≠ .recover)
    (run : Run σ) (inputEvents : List (Obs σ)) (change : Run σ → Run σ)
    (isStep : step program config action =
      some (config.commitRun run true none inputEvents change))
    (inputsHaveNoFx : ∀ stepId method effect index,
      Obs.fx stepId method effect index ∉ inputEvents)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isExec : (Dex.system program.failureFreeFlow).exec (abstractConfig config)
      (dexActionsOf program config action) = some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config action dexEvents dexNext := by
  apply wpf6b_lift_finish program ignoresAttemptContext config invariant action isNotExhausting
    _ _ isStep dexEvents dexNext isExec
  · intro stepId method effect index isMember
    rcases List.mem_append.mp isMember with isInput | isClose
    · exact absurd isInput (inputsHaveNoFx stepId method effect index)
    · exact absurd isClose (wpf6b_closeEvents_noFx _ _ stepId method effect index)
  · apply wpf6b_noFailure_of_parts program ignoresAttemptContext config invariant noFailure action
      _ _ isStep isNotFailed isNotInterrupt isNotCrash isNotRecover noFailure.noZombies rfl
      noFailure.nextTaskIdIsVirtual
    exact wpf6b_ghostParts_reconcile program config noFailure _ _ none _ (by rfl) rfl
      (fun _ isReal => isReal) (fun _ _ isCompleted => by cases isCompleted)
      (fun task _ isTask _ _ => ⟨task, isTask, rfl⟩)

/-- [TICK] lifts. -/
theorem wpf6b_lift_tick (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (duration : Nat) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.tick duration) =
      some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.tick duration) dexEvents dexNext := by
  have isPositive : 0 < duration := by
    simp only [Dex.step] at isDexStep
    split at isDexStep
    · assumption
    · cases isDexStep
  have isStep : step program config (.tick duration) =
      some ([], { config with clock := config.clock + duration }) := by
    simp only [step, isPositive, ↓reduceIte]
  exact wpf6b_lift_finish program ignoresAttemptContext config invariant _ rfl _ _ isStep
    dexEvents dexNext (wpf6b_exec_single _ _ _ _ _ isDexStep) (by simp)
    { isUp := noFailure.isUp, noZombies := noFailure.noZombies,
      noInterrupts := noFailure.noInterrupts, noRetryTimers := noFailure.noRetryTimers,
      lastCommitIsNormal := noFailure.lastCommitIsNormal,
      nextTaskIdIsVirtual := noFailure.nextTaskIdIsVirtual, noBackoff := noFailure.noBackoff,
      pendingHasNoSnapshot := noFailure.pendingHasNoSnapshot,
      tasksMatchHistories := noFailure.tasksMatchHistories,
      tasksNotCommitted := noFailure.tasksNotCommitted, oneTaskPerKey := noFailure.oneTaskPerKey,
      liveHasTask := noFailure.liveHasTask }

/-- [FLOW-START] lifts. -/
theorem wpf6b_lift_startFlow (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (config : Config σ) (invariant : Invariant program config)
    (noFailure : NoFailure program config) (stepType : σ.StepType) (input : σ.Val)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.request (.startFlow stepType input)) = some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.request (.startFlow startRequestId stepType input))
      dexEvents dexNext := by
  cases hasRun : config.run with
  | some run =>
    simp only [Dex.step, abstractConfig, hasRun, Option.map_some] at isDexStep
    cases isDexStep
  | none =>
    have canCommit : config.canServeCommit = true := by
      rw [wpf6b_canServeCommit_abstract program config noFailure]
      simp only [Dex.step, abstractConfig, hasRun, Option.map_none] at isDexStep
      split at isDexStep
      · assumption
      · cases isDexStep
    have isStep : ∃ run change, step program config
        (.request (.startFlow startRequestId stepType input)) =
        some (config.commitRun run true none [Obs.start stepType input] change) := by
      refine ⟨?run, ?change, ?_⟩
      rotate_right
      simp only [step, hasRun, canCommit, ↓reduceIte]
      rfl
    obtain ⟨run, change, isStep⟩ := isStep
    exact wpf6b_lift_commit program ignoresAttemptContext config invariant noFailure _ rfl
      (by intro _ _ isSame; cases isSame) (by intro _ _ isSame; cases isSame)
      (by intro isSame; cases isSame) (by intro isSame; cases isSame) run _ change isStep
      (by simp) dexEvents dexNext (wpf6b_exec_single _ _ _ _ _ isDexStep)

/-- [PUBLISH] and its rejection lift. -/
theorem wpf6b_lift_publish (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (chan : σ.Chan) (value : σ.Val) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.request (.publish chan value)) = some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.request (.publish chan value)) dexEvents dexNext := by
  have isExec := wpf6b_exec_single _ _ _ _ _ isDexStep
  have isRejected : step program config (.request (.publish chan value)) =
      some ([Obs.rejectedPub chan value], config) →
      wpf6b_LiftResult program config (.request (.publish chan value)) dexEvents dexNext :=
    fun isStep => wpf6b_lift_finish program ignoresAttemptContext config invariant _ rfl _ _
      isStep dexEvents dexNext isExec (by simp) noFailure
  cases hasRun : config.run with
  | none => exact isRejected (by simp only [step, noFailure.isUp, hasRun, ↓reduceIte])
  | some run =>
    cases hasStatus : run.base.status with
    | closed status =>
      exact isRejected (by simp only [step, noFailure.isUp, hasRun, hasStatus, ↓reduceIte])
    | running =>
      have canCommit : config.canCommit = true := by
        rw [← wpf6b_canCommit_abstract program config noFailure]
        simp only [Dex.step, abstractConfig, hasRun, Option.map_some] at isDexStep
        have isRunning : (run.abstract config.ghost).status = .running := hasStatus
        rw [isRunning] at isDexStep
        simp only at isDexStep
        split at isDexStep
        · assumption
        · cases isDexStep
      have isStep : ∃ change, step program config (.request (.publish chan value)) =
          some (config.commitRun run true none [Obs.pub chan value] change) := by
        refine ⟨?change, ?_⟩
        rotate_right
        simp only [step, noFailure.isUp, hasRun, hasStatus, canCommit, ↓reduceIte]
        rfl
      obtain ⟨change, isStep⟩ := isStep
      exact wpf6b_lift_commit program ignoresAttemptContext config invariant noFailure _ rfl
        (by intro _ _ isSame; cases isSame) (by intro _ _ isSame; cases isSame)
        (by intro isSame; cases isSame) (by intro isSame; cases isSame) run _ change isStep
        (by simp) dexEvents dexNext isExec

/-- Two real tasks with the same ID are the same task. -/
theorem wpf6b_realTask_eq_of_id (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (first second : Task σ)
    (isFirst : first ∈ config.realTasks) (isSecond : second ∈ config.realTasks)
    (sameId : first.base.id = second.base.id) : first = second :=
  eq_of_nodup_map (fun task => task.base.id) config.realTasks invariant.realTaskIdsNodup
    isFirst isSecond sameId

/-- The ghost parts after a Worker step that changes one real task of `Θ`
and the history of its invocation. -/
theorem wpf6b_ghostParts_worker (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (task : Task σ) (isTask : task ∈ config.tasks) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? task.key = some history)
    (changeTask : Task σ → Task σ) (changeHistory : InvocationHistory σ → InvocationHistory σ)
    (keepsId : ∀ other, (changeTask other).base.id = other.base.id)
    (keepsKey : ∀ other, (changeTask other).key = other.key)
    (keepsArguments : (changeHistory history).arguments = history.arguments)
    (keepsOutcome : (changeHistory history).outcome = history.outcome)
    (sameCount : (changeTask task).emittedCount = (changeHistory history).emittedCount)
    (samePhase : (changeTask task).base.phase = (changeHistory history).virtualPhase) :
    wpf6b_GhostParts { config.updateRealTask task.base.id changeTask with
      ghost := config.ghost.updateInvocation task.key changeHistory } := by
  have isRealTasks := wpf6b_realTasks_eq program config noFailure
  have isTaskReal : task ∈ config.realTasks := by rw [isRealTasks]; exact isTask
  have nextRealTasks : ({ config.updateRealTask task.base.id changeTask with
      ghost := config.ghost.updateInvocation task.key changeHistory } : Config σ).realTasks =
      config.tasks.map (fun other =>
        if other.base.id = task.base.id then changeTask other else other) := by
    simp only [Config.realTasks, Config.updateRealTask, noFailure.noZombies, List.map_nil,
      List.append_nil]
  -- The changed task is the only one with its ID or its key.
  have isOrigin : ∀ other, other ∈ config.tasks → other.base.id = task.base.id → other = task :=
    fun other isOther sameId => wpf6b_realTask_eq_of_id program config invariant other task
      (by rw [isRealTasks]; exact isOther) isTaskReal sameId
  have isOtherKey : ∀ other, other ∈ config.tasks → ¬ other.base.id = task.base.id →
      other.key ≠ task.key := fun other isOther otherId sameKey =>
    otherId (noFailure.oneTaskPerKey other task (by rw [isRealTasks]; exact isOther) isTaskReal
      sameKey)
  have findOther : ∀ key, key ≠ task.key →
      (config.ghost.updateInvocation task.key changeHistory).findInvocation? key =
        config.ghost.findInvocation? key :=
    fun key isOther => wpf6b_updateInvocation_other _ _ _ _ isOther
  have findSelf : (config.ghost.updateInvocation task.key changeHistory).findInvocation? task.key =
      some (changeHistory history) := by
    rw [wpf6b_updateInvocation_self, isHistory]
    rfl
  refine ⟨?_, ?_, ?_, ?_⟩
  · intro nextTask isNext
    rw [nextRealTasks, List.mem_map] at isNext
    obtain ⟨other, isOther, isNextTask⟩ := isNext
    subst isNextTask
    by_cases sameId : other.base.id = task.base.id
    · have isSame := isOrigin other isOther sameId
      subst isSame
      obtain ⟨_, arguments, isOldHistory, hasArguments, isVirtualId, _, _⟩ :=
        noFailure.tasksMatchHistories other isTaskReal
      rw [isHistory] at isOldHistory
      cases isOldHistory
      simp only [↓reduceIte]
      refine ⟨changeHistory history, arguments, by rw [keepsKey]; exact findSelf,
        by rw [keepsArguments]; exact hasArguments, by rw [keepsId]; exact isVirtualId,
        sameCount, samePhase⟩
    · simp only [sameId, ↓reduceIte]
      obtain ⟨otherHistory, arguments, isOtherHistory, hasArguments, isVirtualId, otherCount,
        otherPhase⟩ := noFailure.tasksMatchHistories other (by rw [isRealTasks]; exact isOther)
      exact ⟨otherHistory, arguments,
        by rw [findOther _ (isOtherKey other isOther sameId)]; exact isOtherHistory,
        hasArguments, isVirtualId, otherCount, otherPhase⟩
  · intro nextTask nextHistory isNext isNextHistory
    rw [nextRealTasks, List.mem_map] at isNext
    obtain ⟨other, isOther, isNextTask⟩ := isNext
    subst isNextTask
    by_cases sameId : other.base.id = task.base.id
    · have isSame := isOrigin other isOther sameId
      subst isSame
      simp only [↓reduceIte] at isNextHistory
      rw [keepsKey] at isNextHistory
      change (config.ghost.updateInvocation other.key changeHistory).findInvocation? other.key =
        some nextHistory at isNextHistory
      rw [findSelf] at isNextHistory
      cases isNextHistory
      rw [keepsOutcome]
      exact noFailure.tasksNotCommitted other history isTaskReal isHistory
    · simp only [sameId, ↓reduceIte] at isNextHistory
      change (config.ghost.updateInvocation task.key changeHistory).findInvocation? other.key =
        some nextHistory at isNextHistory
      rw [findOther _ (isOtherKey other isOther sameId)] at isNextHistory
      exact noFailure.tasksNotCommitted other nextHistory (by rw [isRealTasks]; exact isOther)
        isNextHistory
  · intro first second isFirst isSecond sameKey
    rw [nextRealTasks, List.mem_map] at isFirst isSecond
    obtain ⟨firstOrigin, isFirstOrigin, isFirstTask⟩ := isFirst
    obtain ⟨secondOrigin, isSecondOrigin, isSecondTask⟩ := isSecond
    subst isFirstTask
    subst isSecondTask
    have keyOf : ∀ other, (if other.base.id = task.base.id then changeTask other else other).key =
        other.key := fun other => by split <;> simp [keepsKey]
    have idOf : ∀ other,
        (if other.base.id = task.base.id then changeTask other else other).base.id =
          other.base.id := fun other => by split <;> simp [keepsId]
    rw [keyOf, keyOf] at sameKey
    rw [idOf, idOf]
    exact noFailure.oneTaskPerKey firstOrigin secondOrigin (by rw [isRealTasks]; exact isFirstOrigin)
      (by rw [isRealTasks]; exact isSecondOrigin) sameKey
  · intro key nextHistory arguments isNextHistory isLive hasArguments
    have liveBefore : ∃ other, other ∈ config.tasks ∧ other.key = key := by
      by_cases isKey : key = task.key
      · subst isKey
        change (config.ghost.updateInvocation task.key changeHistory).findInvocation? task.key =
          some nextHistory at isNextHistory
        rw [findSelf] at isNextHistory
        cases isNextHistory
        exact noFailure.liveHasTask task.key history arguments isHistory
          (by rw [← keepsOutcome]; exact isLive) (by rw [← keepsArguments]; exact hasArguments)
      · change (config.ghost.updateInvocation task.key changeHistory).findInvocation? key =
          some nextHistory at isNextHistory
        rw [findOther _ isKey] at isNextHistory
        exact noFailure.liveHasTask key nextHistory arguments isNextHistory isLive hasArguments
    obtain ⟨other, isOther, hasKey⟩ := liveBefore
    refine ⟨if other.base.id = task.base.id then changeTask other else other,
      List.mem_map.mpr ⟨other, isOther, rfl⟩, ?_⟩
    split
    · rw [keepsKey]; exact hasKey
    · exact hasKey

/-- `NoFailure` after a Worker step that changes one real task of `Θ` and
the history of its invocation. -/
theorem wpf6b_noFailure_worker (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (action : Action σ) (events : List (Obs σ))
    (task : Task σ) (isTask : task ∈ config.tasks) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? task.key = some history)
    (changeTask : Task σ → Task σ) (changeHistory : InvocationHistory σ → InvocationHistory σ)
    (isStep : step program config action = some (events,
      { config.updateRealTask task.base.id changeTask with
        ghost := config.ghost.updateInvocation task.key changeHistory }))
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (isNotCrash : action ≠ .crash) (isNotRecover : action ≠ .recover)
    (keepsId : ∀ other, (changeTask other).base.id = other.base.id)
    (keepsKey : ∀ other, (changeTask other).key = other.key)
    (keepsArguments : (changeHistory history).arguments = history.arguments)
    (keepsOutcome : (changeHistory history).outcome = history.outcome)
    (sameCount : (changeTask task).emittedCount = (changeHistory history).emittedCount)
    (samePhase : (changeTask task).base.phase = (changeHistory history).virtualPhase) :
    NoFailure program { config.updateRealTask task.base.id changeTask with
      ghost := config.ghost.updateInvocation task.key changeHistory } := by
  obtain ⟨sameStamp, sameNextId⟩ := wpf6b_updateInvocation_fields config.ghost task.key changeHistory
  apply wpf6b_noFailure_of_parts program ignoresAttemptContext config invariant noFailure action
    events _ isStep isNotFailed isNotInterrupt isNotCrash isNotRecover
  · simp only [Config.updateRealTask, noFailure.noZombies, List.map_nil]
  · rw [sameStamp]; exact noFailure.lastCommitIsNormal
  · rw [sameNextId]; exact noFailure.nextTaskIdIsVirtual
  · exact wpf6b_ghostParts_worker program config invariant noFailure task isTask history isHistory
      changeTask changeHistory keepsId keepsKey keepsArguments keepsOutcome sameCount samePhase

/-- The virtual phase of a history pins its progress. -/
theorem wpf6b_progress_of_virtualPhase (history : InvocationHistory σ) :
    (history.virtualPhase = .started → history.progress = .notRun) ∧
    (∀ remaining response, history.virtualPhase = .running remaining response →
      ∃ effects, history.progress = .ran effects response ∧
        effects.drop history.emittedCount = remaining) := by
  unfold InvocationHistory.virtualPhase
  cases history.progress with
  | notRun =>
    refine ⟨fun _ => rfl, ?_⟩
    intro remaining response isRunning
    cases isRunning
  | ran effects response =>
    refine ⟨fun isStarted => (by cases isStarted), ?_⟩
    intro remaining otherResponse isRunning
    cases isRunning
    exact ⟨effects, rfl, rfl⟩
  | returned response =>
    refine ⟨fun isStarted => (by cases isStarted), ?_⟩
    intro remaining otherResponse isRunning
    cases isRunning

/-- The phase of a virtual task is the phase it was built with. -/
theorem wpf6b_dexPhase (arguments : Arguments σ) (key : InvocationKey σ)
    (phase : Dex.TaskPhase σ) : (arguments.toTask key phase).phase = phase := rfl

/-- [WORKER-EFFECT], first part, lifts. -/
theorem wpf6b_lift_workerRun (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (taskId : Nat) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.workerRun taskId) =
      some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.workerRun taskId true) dexEvents dexNext := by
  cases isFound : (abstractConfig config).findTask? taskId with
  | none => simp only [Dex.step, isFound] at isDexStep; cases isDexStep
  | some dexTask =>
    obtain ⟨task, history, arguments, isTask, hasId, isHistory, hasArguments, _, isDexTask,
      sameCount, samePhase, isReal, isVirtualOf⟩ :=
      wpf6b_realTask_of_abstractTask program config invariant noFailure taskId dexTask isFound
    have isStarted : history.virtualPhase = .started := by
      simp only [Dex.step, isFound] at isDexStep
      rw [isDexTask, wpf6b_dexPhase] at isDexStep
      cases isPhase : history.virtualPhase with
      | started => rfl
      | _ => rw [isPhase] at isDexStep; simp at isDexStep
    have isNotRun := (wpf6b_progress_of_virtualPhase history).1 isStarted
    have isConsistent := invariant.historiesConsistent (task.key, history)
      (wpf6b_mem_of_get? _ _ _ isHistory)
    simp only [HistoryIsConsistent, hasArguments, isNotRun] at isConsistent
    have zeroCount : history.emittedCount = 0 := isConsistent.1
    have taskStarted : task.base.phase = .started := samePhase.trans isStarted
    subst hasId
    have isStep : step program config (.workerRun task.base.id true) = some ([],
        { config.updateRealTask task.base.id (fun current =>
            { current with base := { current.base with
                phase := .running (task.base.compute (program task.attemptContext)).1
                  (task.base.compute (program task.attemptContext)).2 } }) with
          ghost := config.ghost.updateInvocation task.key (fun history =>
            { history with
              progress := (.ran (task.base.compute (program task.attemptContext)).1
                (task.base.compute (program task.attemptContext)).2) }) }) := by
      simp only [step, isReal, taskStarted, isHistory, isNotRun, VirtualProgress.isNotRun,
        bne_self_eq_false, Bool.false_eq_true, ↓reduceIte]
      all_goals rfl
    apply wpf6b_lift_finish program ignoresAttemptContext config invariant _ rfl _ _ isStep
    · simp only [dexActionsOf, ↓reduceIte, Config.onVirtualTask, isVirtualOf]
      exact wpf6b_exec_single _ _ _ _ _ isDexStep
    · simp
    · apply wpf6b_noFailure_worker program ignoresAttemptContext config invariant noFailure _ _
        task isTask history isHistory _ _ isStep (by intro _ _ isSame; cases isSame)
        (by intro _ _ isSame; cases isSame) (by intro isSame; cases isSame)
        (by intro isSame; cases isSame) (fun _ => rfl) (fun _ => rfl) rfl rfl sameCount
      show Dex.TaskPhase.running _ _ = Dex.TaskPhase.running (List.drop history.emittedCount _) _
      rw [zeroCount, List.drop_zero]

/-- [WORKER-EFFECT] lifts. -/
theorem wpf6b_lift_workerEffect (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (taskId : Nat) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.workerEffect taskId) =
      some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.workerEffect taskId true) dexEvents dexNext := by
  cases isFound : (abstractConfig config).findTask? taskId with
  | none => simp only [Dex.step, isFound] at isDexStep; cases isDexStep
  | some dexTask =>
    obtain ⟨task, history, arguments, isTask, hasId, isHistory, hasArguments, _, isDexTask,
      sameCount, samePhase, isReal, isVirtualOf⟩ :=
      wpf6b_realTask_of_abstractTask program config invariant noFailure taskId dexTask isFound
    have isRunning : ∃ effect laterEffects response,
        history.virtualPhase = .running (effect :: laterEffects) response := by
      simp only [Dex.step, isFound] at isDexStep
      rw [isDexTask, wpf6b_dexPhase] at isDexStep
      cases isPhase : history.virtualPhase with
      | running remaining response =>
        cases remaining with
        | nil => rw [isPhase] at isDexStep; simp at isDexStep
        | cons effect laterEffects => exact ⟨effect, laterEffects, response, rfl⟩
      | _ => rw [isPhase] at isDexStep; simp at isDexStep
    obtain ⟨effect, laterEffects, response, isPhase⟩ := isRunning
    obtain ⟨effects, isProgress, isRemaining⟩ :=
      (wpf6b_progress_of_virtualPhase history).2 _ _ isPhase
    have taskRunning : task.base.phase = .running (effect :: laterEffects) response :=
      samePhase.trans isPhase
    have emittedCountOf : config.ghost.emittedCountOf task.key = task.emittedCount := by
      simp [Ghost.emittedCountOf, isHistory, sameCount]
    subst hasId
    have isStep : step program config (.workerEffect task.base.id true) =
        some ([Obs.fx task.base.step task.base.method effect (task.emittedCount + 1)],
          { config.updateRealTask task.base.id (fun current =>
              { current with
                base := { current.base with phase := .running laterEffects response }
                emittedCount := task.emittedCount + 1 }) with
            ghost := config.ghost.updateInvocation task.key (fun history =>
              { history with emittedCount := task.emittedCount + 1 }) }) := by
      simp only [step, isReal, taskRunning, emittedCountOf, Nat.lt_add_one, decide_true,
        bne_self_eq_false, Bool.false_eq_true, ↓reduceIte]
      all_goals rfl
    apply wpf6b_lift_finish program ignoresAttemptContext config invariant _ rfl _ _ isStep
    · simp only [dexActionsOf, ↓reduceIte, Config.onVirtualTask, isVirtualOf]
      exact wpf6b_exec_single _ _ _ _ _ isDexStep
    · intro stepId method otherEffect index isMember
      simp only [List.mem_singleton, Obs.fx.injEq] at isMember
      obtain ⟨isStepId, isMethod, _, isIndex⟩ := isMember
      subst isStepId isMethod isIndex
      show config.ghost.emittedCountOf task.key < task.emittedCount + 1
      rw [emittedCountOf]
      exact Nat.lt_add_one _
    · apply wpf6b_noFailure_worker program ignoresAttemptContext config invariant noFailure _ _
        task isTask history isHistory _ _ isStep (by intro _ _ isSame; cases isSame)
        (by intro _ _ isSame; cases isSame) (by intro isSame; cases isSame)
        (by intro isSame; cases isSame) (fun _ => rfl) (fun _ => rfl) rfl rfl rfl
      show Dex.TaskPhase.running laterEffects response = InvocationHistory.virtualPhase _
      unfold InvocationHistory.virtualPhase
      simp only [isProgress]
      rw [sameCount, ← List.tail_drop, isRemaining]
      rfl

/-- [WORKER-EFFECT], last part, lifts. -/
theorem wpf6b_lift_workerReturn (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (taskId : Nat) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.workerReturn taskId) =
      some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.workerReturn taskId true) dexEvents dexNext := by
  cases isFound : (abstractConfig config).findTask? taskId with
  | none => simp only [Dex.step, isFound] at isDexStep; cases isDexStep
  | some dexTask =>
    obtain ⟨task, history, arguments, isTask, hasId, isHistory, hasArguments, _, isDexTask,
      sameCount, samePhase, isReal, isVirtualOf⟩ :=
      wpf6b_realTask_of_abstractTask program config invariant noFailure taskId dexTask isFound
    have isRunning : ∃ response, history.virtualPhase = .running [] response := by
      simp only [Dex.step, isFound] at isDexStep
      rw [isDexTask, wpf6b_dexPhase] at isDexStep
      cases isPhase : history.virtualPhase with
      | running remaining response =>
        cases remaining with
        | nil => exact ⟨response, rfl⟩
        | cons effect laterEffects => rw [isPhase] at isDexStep; simp at isDexStep
      | _ => rw [isPhase] at isDexStep; simp at isDexStep
    obtain ⟨response, isPhase⟩ := isRunning
    obtain ⟨effects, isProgress, _⟩ := (wpf6b_progress_of_virtualPhase history).2 _ _ isPhase
    have taskRunning : task.base.phase = .running [] response := samePhase.trans isPhase
    subst hasId
    have isStep : step program config (.workerReturn task.base.id true) = some ([],
        { config.updateRealTask task.base.id (fun current =>
            { current with base := { current.base with phase := .returned response } }) with
          ghost := config.ghost.updateInvocation task.key (fun history =>
            { history with progress := .returned response }) }) := by
      simp only [step, isReal, taskRunning, isHistory, isProgress, VirtualProgress.isReturned,
        Bool.not_false, bne_self_eq_false, Bool.false_eq_true, ↓reduceIte]
      all_goals rfl
    apply wpf6b_lift_finish program ignoresAttemptContext config invariant _ rfl _ _ isStep
    · simp only [dexActionsOf, ↓reduceIte, Config.onVirtualTask, isVirtualOf]
      exact wpf6b_exec_single _ _ _ _ _ isDexStep
    · simp
    · exact wpf6b_noFailure_worker program ignoresAttemptContext config invariant noFailure _ _
        task isTask history isHistory _ _ isStep (by intro _ _ isSame; cases isSame)
        (by intro _ _ isSame; cases isSame) (by intro isSame; cases isSame)
        (by intro isSame; cases isSame) (fun _ => rfl) (fun _ => rfl) rfl rfl sameCount rfl

/-- α of a running invocation whose history has arguments: running as its
virtual task. -/
theorem wpf6b_abstractInvocation_running (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (run : Run σ) (hasRun : config.run = some run)
    (record : Dex.StepRecord σ) (isMember : record ∈ run.base.steps) (method : Dex.Method)
    (taskId : Nat) (isRunning : record.invocation = some { method := method, state := .running taskId })
    (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? (record.id, method) = some history)
    (arguments : Arguments σ) (hasArguments : history.arguments = some arguments) :
    run.abstractInvocation config.ghost record =
      some { method := method, state := .running arguments.virtualTaskId } := by
  have isMatched := (invariant.runInvariant run hasRun).attemptsMatch record isMember
  unfold AttemptMatchesRecord at isMatched
  rw [isRunning] at isMatched
  obtain ⟨attempt, _, isAttempt, _, _, _, _, isRunningAttempt, _, _⟩ := isMatched
  have hasSnapshot := (isRunningAttempt taskId rfl).2
  unfold Run.abstractInvocation
  simp only [isRunning, isAttempt, hasSnapshot, Ghost.argumentsOf, isHistory, hasArguments,
    Option.bind_some]

/-- A Dex [STEP-COMPLETE-STALE] can also be taken as [WORKER-ABANDON]. -/
theorem wpf6b_abandon_of_stale (flow : Dex.FlowDef σ) (dexConfig : Dex.Config σ) (taskId : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step flow dexConfig (.stepCompleteStale taskId) = some (dexEvents, dexNext)) :
    Dex.step flow dexConfig (.workerAbandon taskId) = some (dexEvents, dexNext) := by
  cases isFound : dexConfig.findTask? taskId with
  | none => simp only [Dex.step, isFound] at isDexStep; cases isDexStep
  | some dexTask =>
    simp only [Dex.step, isFound] at isDexStep ⊢
    split at isDexStep
    · exact isDexStep
    · cases isDexStep

/-- `NoFailure` after a real task of `Θ` whose invocation was canceled is
dropped. -/
theorem wpf6b_noFailure_remove (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (action : Action σ) (events : List (Obs σ)) (task : Task σ) (isTask : task ∈ config.tasks)
    (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? task.key = some history)
    (isCanceled : history.outcome = .canceled)
    (isStep : step program config action = some (events, config.removeRealTask task.base.id))
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (isNotCrash : action ≠ .crash) (isNotRecover : action ≠ .recover) :
    NoFailure program (config.removeRealTask task.base.id) := by
  have isRealTasks := wpf6b_realTasks_eq program config noFailure
  have nextRealTasks : (config.removeRealTask task.base.id).realTasks =
      config.tasks.filter (fun other => !decide (other.base.id = task.base.id)) := by
    simp only [Config.realTasks, Config.removeRealTask, noFailure.noZombies, List.filter_nil,
      List.append_nil]
  have realSubset : ∀ other, other ∈ (config.removeRealTask task.base.id).realTasks →
      other ∈ config.realTasks := by
    intro other isOther
    rw [nextRealTasks] at isOther
    rw [isRealTasks]
    exact (List.mem_filter.mp isOther).1
  apply wpf6b_noFailure_of_parts program ignoresAttemptContext config invariant noFailure action
    events _ isStep isNotFailed isNotInterrupt isNotCrash isNotRecover
  · simp only [Config.removeRealTask, noFailure.noZombies, List.filter_nil]
  · exact noFailure.lastCommitIsNormal
  · exact noFailure.nextTaskIdIsVirtual
  · refine ⟨fun other isOther => noFailure.tasksMatchHistories other (realSubset other isOther),
      fun other otherHistory isOther =>
        noFailure.tasksNotCommitted other otherHistory (realSubset other isOther),
      fun first second isFirst isSecond =>
        noFailure.oneTaskPerKey first second (realSubset first isFirst) (realSubset second isSecond),
      ?_⟩
    intro key liveHistory arguments isLiveHistory isLive hasArguments
    obtain ⟨other, isOther, hasKey⟩ :=
      noFailure.liveHasTask key liveHistory arguments isLiveHistory isLive hasArguments
    refine ⟨other, List.mem_filter.mpr ⟨isOther, ?_⟩, hasKey⟩
    simp only [Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not]
    intro sameId
    have isSame := wpf6b_realTask_eq_of_id program config invariant other task
      (by rw [isRealTasks]; exact isOther) (by rw [isRealTasks]; exact isTask) sameId
    subst isSame
    change config.ghost.findInvocation? key = some liveHistory at isLiveHistory
    rw [hasKey, isLiveHistory] at isHistory
    cases isHistory
    rw [isLive] at isCanceled
    cases isCanceled

/-- [WORKER-ABANDON] lifts: the abandoned virtual task is the last real task
of a canceled invocation, which fails the fence. -/
theorem wpf6b_lift_abandon (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (taskId : Nat) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.workerAbandon taskId) =
      some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.workerAbandon taskId true) dexEvents dexNext := by
  have isRealTasks := wpf6b_realTasks_eq program config noFailure
  cases isFound : (abstractConfig config).findTask? taskId with
  | none => simp only [Dex.step, isFound] at isDexStep; cases isDexStep
  | some dexTask =>
    obtain ⟨task, history, arguments, isTask, hasId, isHistory, hasArguments, isVirtualId,
      isDexTask, _, _, isReal, isVirtualOf⟩ :=
      wpf6b_realTask_of_abstractTask program config invariant noFailure taskId dexTask isFound
    have isNotCurrent : (abstractConfig config).isTaskCurrent dexTask = false := by
      simp only [Dex.step, isFound] at isDexStep
      split at isDexStep
      · cases isDexStep
      · rename_i notCurrent
        simpa using notCurrent
    have isTaskReal : task ∈ config.realTasks := by rw [isRealTasks]; exact isTask
    have isCanceled : history.outcome = .canceled := by
      cases isOutcome : history.outcome with
      | canceled => rfl
      | committed =>
        exact absurd isOutcome (noFailure.tasksNotCommitted task history isTaskReal isHistory)
      | live =>
        exfalso
        have isPassed := invariant.liveTasksPassFence task history isTask isHistory isOutcome
        unfold Config.isFencePassed at isPassed
        cases hasRun : config.run with
        | none => rw [hasRun] at isPassed; cases isPassed
        | some run =>
          rw [hasRun] at isPassed
          simp only at isPassed
          cases isRecord : run.base.findStep? task.base.step with
          | none => rw [isRecord] at isPassed; cases isPassed
          | some record =>
            rw [isRecord] at isPassed
            cases isAttempt : run.findAttempt? task.base.step with
            | none => rw [isAttempt] at isPassed; cases isPassed
            | some attempt =>
              rw [isAttempt] at isPassed
              simp only [Bool.and_eq_true, decide_eq_true_eq] at isPassed
              obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? run.base _ record isRecord
              have abstracted := wpf6b_abstractInvocation_running program config invariant run hasRun
                record isMember task.base.method task.base.id isPassed.1 history
                (by rw [isId]; exact isHistory) arguments hasArguments
              have isCurrent : (abstractConfig config).isTaskCurrent dexTask = true := by
                rw [isDexTask]
                simp only [Dex.Config.isTaskCurrent, abstractConfig, hasRun, Option.map_some]
                rw [show (arguments.toTask task.key history.virtualPhase).step = task.base.step
                  from rfl, wpf6b_findStep?_abstract, isRecord]
                simp only [Option.map_some, abstracted]
                simp [Arguments.toTask, Task.key]
              rw [isNotCurrent] at isCurrent
              cases isCurrent
    have isFenceFailed : config.isFencePassed task = false := by
      cases isPassed : config.isFencePassed task with
      | false => rfl
      | true =>
        exfalso
        unfold Config.isFencePassed at isPassed
        cases hasRun : config.run with
        | none => rw [hasRun] at isPassed; cases isPassed
        | some run =>
          rw [hasRun] at isPassed
          simp only at isPassed
          cases isRecord : run.base.findStep? task.base.step with
          | none => rw [isRecord] at isPassed; cases isPassed
          | some record =>
            rw [isRecord] at isPassed
            cases isAttempt : run.findAttempt? task.base.step with
            | none => rw [isAttempt] at isPassed; cases isPassed
            | some attempt =>
              rw [isAttempt] at isPassed
              simp only [Bool.and_eq_true, decide_eq_true_eq] at isPassed
              obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? run.base _ record isRecord
              have isMatched := (invariant.runInvariant run hasRun).attemptsMatch record isMember
              unfold AttemptMatchesRecord at isMatched
              rw [isPassed.1] at isMatched
              obtain ⟨_, recordHistory, _, isRecordHistory, isLive, _⟩ := isMatched
              rw [isId] at isRecordHistory
              change config.ghost.findInvocation? task.key = some recordHistory at isRecordHistory
              rw [isHistory] at isRecordHistory
              cases isRecordHistory
              rw [isLive] at isCanceled
              cases isCanceled
    have isLast : config.isLastOfCanceled task = true := by
      unfold Config.isLastOfCanceled
      simp only [isHistory, isCanceled, hasArguments, decide_true, Option.isSome_some,
        Bool.true_and, Bool.not_eq_eq_eq_not, Bool.not_true, List.any_eq_false, Bool.and_eq_true,
        decide_eq_true_eq, decide_eq_false_iff_not, not_and, Classical.not_not]
      intro other isOther sameKey
      exact noFailure.oneTaskPerKey other task isOther isTaskReal sameKey
    subst hasId
    have isLive : config.findLiveTask? task.base.id = some task := by
      unfold Config.findRealTask? at isReal
      rw [isRealTasks] at isReal
      exact isReal
    have isStep : step program config (.workerAbandon task.base.id true) =
        some ([], config.removeRealTask task.base.id) := by
      simp only [step, isLive, isFenceFailed, isLast, bne_self_eq_false, Bool.or_false,
        Bool.false_eq_true, ↓reduceIte]
    apply wpf6b_lift_finish program ignoresAttemptContext config invariant _ rfl _ _ isStep
    · simp only [dexActionsOf, ↓reduceIte, Config.onVirtualTask, isVirtualOf]
      exact wpf6b_exec_single _ _ _ _ _ isDexStep
    · simp
    · exact wpf6b_noFailure_remove program ignoresAttemptContext config invariant noFailure _ _ task
        isTask history isHistory isCanceled isStep (by intro _ _ isSame; cases isSame)
        (by intro _ _ isSame; cases isSame) (by intro isSame; cases isSame)
        (by intro isSame; cases isSame)

/-- Without retry timers every due timer is a wait timer. -/
theorem wpf6b_dueTimers_isWait (run : Run σ) (clock : Nat) (noRetryTimers : run.retryTimers = [])
    (timer : DueTimer σ) (isDue : timer ∈ run.dueTimers clock) : timer.isWait = true := by
  unfold Run.dueTimers at isDue
  rw [noRetryTimers] at isDue
  simp only [List.filter_nil, List.map_nil, List.append_nil, List.mem_map] at isDue
  obtain ⟨entry, isSorted, isTimer⟩ := isDue
  rw [mem_sortStable] at isSorted
  obtain ⟨waitEntry, _, isEntry⟩ := List.mem_map.mp isSorted
  subst isEntry
  subst isTimer
  rfl

/-- Without retry timers, DexF and α see as many due timers. -/
theorem wpf6b_dueTimers_length (ghost : Ghost σ) (run : Run σ) (clock : Nat)
    (noRetryTimers : run.retryTimers = []) :
    (run.dueTimers clock).length = ((run.abstract ghost).dueTimers clock).length := by
  unfold Run.dueTimers Dex.Run.dueTimers
  rw [noRetryTimers]
  simp only [List.filter_nil, List.map_nil, List.append_nil, List.length_map,
    wpf6b_length_sortStable]
  rfl

/-- [TIMER-FIRE] lifts to TimersFired of the same wait timers. -/
theorem wpf6b_lift_timerFire (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (count : Nat) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.timerFire count) =
      some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.timerFire count true) dexEvents dexNext := by
  cases hasRun : config.run with
  | none =>
    simp only [Dex.step, abstractConfig, hasRun, Option.map_none] at isDexStep
    cases isDexStep
  | some run =>
    have noRetryTimers := noFailure.noRetryTimers run hasRun
    have isEnabled : 0 < count ∧ count ≤ (run.dueTimers config.clock).length ∧
        config.canServeCommit = true := by
      rw [wpf6b_canServeCommit_abstract program config noFailure,
        wpf6b_dueTimers_length config.ghost run config.clock noRetryTimers]
      simp only [Dex.step, abstractConfig, hasRun, Option.map_some] at isDexStep
      split at isDexStep
      · rename_i isGuard
        simp only [Bool.and_eq_true, decide_eq_true_eq] at isGuard
        exact ⟨isGuard.1.1, isGuard.1.2, isGuard.2⟩
      · cases isDexStep
    obtain ⟨isPositive, isBelow, canCommit⟩ := isEnabled
    have allWait : ∀ timer, timer ∈ (run.dueTimers config.clock).take count →
        timer.isWait = true := fun timer isMember =>
      wpf6b_dueTimers_isWait run config.clock noRetryTimers timer (List.mem_of_mem_take isMember)
    have takenLength : ((run.dueTimers config.clock).take count).length = count := by
      rw [List.length_take]
      exact Nat.min_eq_left isBelow
    have firesWait : ((run.dueTimers config.clock).take count).any DueTimer.isWait = true := by
      obtain ⟨timer, isMember⟩ :=
        List.exists_mem_of_length_pos (show 0 < ((run.dueTimers config.clock).take count).length
          by rw [takenLength]; exact isPositive)
      exact List.any_eq_true.mpr ⟨timer, isMember, allWait timer isMember⟩
    have firedCount : config.firedWaitTimerCount count = count := by
      unfold Config.firedWaitTimerCount
      rw [hasRun]
      simp only
      rw [List.filter_eq_self.mpr allWait, takenLength]
    have isStep : ∃ change, step program config (.timerFire count true) =
        some (config.commitRun run true none [] change) := by
      refine ⟨?change, ?_⟩
      rotate_right
      simp only [step, canCommit, hasRun, isPositive, isBelow, firesWait, decide_true,
        Bool.and_true, beq_self_eq_true, ↓reduceIte]
      rfl
    obtain ⟨change, isStep⟩ := isStep
    apply wpf6b_lift_commit program ignoresAttemptContext config invariant noFailure _ rfl
      (by intro _ _ isSame; cases isSame) (by intro _ _ isSame; cases isSame)
      (by intro isSame; cases isSame) (by intro isSame; cases isSame) run [] change isStep
      (by simp) dexEvents dexNext
    simp only [dexActionsOf, ↓reduceIte, firedCount]
    exact wpf6b_exec_single _ _ _ _ _ isDexStep

/-- The ghost parts after [STEP-START] adds the first real task of an
invocation and sets its arguments. -/
theorem wpf6b_ghostParts_addTask (middle : Config σ) (parts : wpf6b_GhostParts middle)
    (noZombies : middle.zombies = []) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : middle.ghost.findInvocation? key = some history)
    (noArguments : history.arguments = none) (notCommitted : history.outcome ≠ .committed)
    (zeroCount : history.emittedCount = 0) (isNotRun : history.progress = .notRun)
    (task : Task σ) (hasKey : task.key = key) (taskCount : task.emittedCount = 0)
    (taskPhase : task.base.phase = .started) (arguments : Arguments σ)
    (isVirtualId : arguments.virtualTaskId = task.base.id) (nextTaskId nextVirtualTaskId : Nat) :
    wpf6b_GhostParts { middle with
      tasks := middle.tasks ++ [task]
      nextTaskId := nextTaskId
      ghost := { middle.ghost.updateInvocation key (fun history =>
          { history with arguments := some arguments }) with
        nextVirtualTaskId := nextVirtualTaskId } } := by
  obtain ⟨tasksMatch, tasksNotCommitted, oneTaskPerKey, liveHasTask⟩ := parts
  have isMiddleReal : middle.realTasks = middle.tasks := by
    unfold Config.realTasks; rw [noZombies, List.append_nil]
  have nextRealTasks : ({ middle with
      tasks := middle.tasks ++ [task]
      nextTaskId := nextTaskId
      ghost := { middle.ghost.updateInvocation key (fun history =>
          { history with arguments := some arguments }) with
        nextVirtualTaskId := nextVirtualTaskId } } : Config σ).realTasks =
      middle.tasks ++ [task] := by
    unfold Config.realTasks; simp only [noZombies, List.append_nil]
  have findNext : ∀ otherKey, ({ middle.ghost.updateInvocation key (fun history =>
      { history with arguments := some arguments }) with
        nextVirtualTaskId := nextVirtualTaskId } : Ghost σ).findInvocation? otherKey =
      (middle.ghost.updateInvocation key (fun history =>
        { history with arguments := some arguments })).findInvocation? otherKey :=
    fun _ => rfl
  have findSelf := wpf6b_updateInvocation_self middle.ghost key
    (fun history => { history with arguments := some arguments })
  rw [isHistory] at findSelf
  have isFreshKey : ∀ other, other ∈ middle.tasks → other.key ≠ key := by
    intro other isOther sameKey
    obtain ⟨otherHistory, otherArguments, isOtherHistory, hasOtherArguments, _⟩ :=
      tasksMatch other (by rw [isMiddleReal]; exact isOther)
    rw [sameKey, isHistory] at isOtherHistory
    cases isOtherHistory
    rw [noArguments] at hasOtherArguments
    cases hasOtherArguments
  refine ⟨?_, ?_, ?_, ?_⟩
  · intro nextTask isNext
    rw [nextRealTasks, List.mem_append, List.mem_singleton] at isNext
    rcases isNext with isOld | isNew
    · obtain ⟨otherHistory, otherArguments, isOtherHistory, hasOtherArguments, otherId,
        otherCount, otherPhase⟩ := tasksMatch nextTask (by rw [isMiddleReal]; exact isOld)
      refine ⟨otherHistory, otherArguments, ?_, hasOtherArguments, otherId, otherCount, otherPhase⟩
      rw [findNext, wpf6b_updateInvocation_other _ _ _ _ (isFreshKey nextTask isOld)]
      exact isOtherHistory
    · subst isNew
      refine ⟨_, arguments, by rw [findNext, hasKey]; exact findSelf, rfl, isVirtualId,
        by rw [taskCount]; exact zeroCount.symm, ?_⟩
      rw [taskPhase]
      show Dex.TaskPhase.started = InvocationHistory.virtualPhase _
      unfold InvocationHistory.virtualPhase
      simp only [isNotRun]
  · intro nextTask nextHistory isNext isNextHistory
    rw [nextRealTasks, List.mem_append, List.mem_singleton] at isNext
    rcases isNext with isOld | isNew
    · change ({ middle.ghost.updateInvocation key (fun history =>
          { history with arguments := some arguments }) with
            nextVirtualTaskId := nextVirtualTaskId } : Ghost σ).findInvocation? nextTask.key =
          some nextHistory at isNextHistory
      rw [findNext, wpf6b_updateInvocation_other _ _ _ _ (isFreshKey nextTask isOld)]
        at isNextHistory
      exact tasksNotCommitted nextTask nextHistory (by rw [isMiddleReal]; exact isOld)
        isNextHistory
    · subst isNew
      change ({ middle.ghost.updateInvocation key (fun history =>
          { history with arguments := some arguments }) with
            nextVirtualTaskId := nextVirtualTaskId } : Ghost σ).findInvocation? nextTask.key =
          some nextHistory at isNextHistory
      rw [findNext, hasKey, findSelf] at isNextHistory
      cases isNextHistory
      exact notCommitted
  · intro first second isFirst isSecond sameKey
    rw [nextRealTasks, List.mem_append, List.mem_singleton] at isFirst isSecond
    rcases isFirst with isFirstOld | isFirstNew <;> rcases isSecond with isSecondOld | isSecondNew
    · exact oneTaskPerKey first second (by rw [isMiddleReal]; exact isFirstOld)
        (by rw [isMiddleReal]; exact isSecondOld) sameKey
    · subst isSecondNew
      exact absurd (sameKey.trans hasKey) (isFreshKey first isFirstOld)
    · subst isFirstNew
      exact absurd (sameKey.symm.trans hasKey) (isFreshKey second isSecondOld)
    · rw [isFirstNew, isSecondNew]
  · intro otherKey nextHistory otherArguments isNextHistory isLive hasArguments
    by_cases isKey : otherKey = key
    · subst isKey
      exact ⟨task, List.mem_append_right _ (List.mem_singleton_self task), hasKey⟩
    · change ({ middle.ghost.updateInvocation key (fun history =>
          { history with arguments := some arguments }) with
            nextVirtualTaskId := nextVirtualTaskId } : Ghost σ).findInvocation? otherKey =
          some nextHistory at isNextHistory
      rw [findNext, wpf6b_updateInvocation_other _ _ _ _ isKey] at isNextHistory
      obtain ⟨other, isOther, otherKeyIs⟩ :=
        liveHasTask otherKey nextHistory otherArguments isNextHistory isLive hasArguments
      exact ⟨other, List.mem_append_left _ isOther, otherKeyIs⟩

/-- [STEP-START] lifts to the [STEP-START] that computes a snapshot. -/
theorem wpf6b_lift_stepStart (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (stepId : Dex.StepExecId σ) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.stepStart stepId) =
      some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.stepStart stepId false) dexEvents dexNext := by
  have isPending : ∃ dexRun dexRecord method, (abstractConfig config).run = some dexRun ∧
      dexRun.findStep? stepId = some dexRecord ∧
      dexRecord.invocation = some { method := method, state := .pending } ∧
      (abstractConfig config).canCommit = true := by
    simp only [Dex.step] at isDexStep
    split at isDexStep
    · cases isDexStep
    · rename_i dexRun hasDexRun
      split at isDexStep
      · cases isDexStep
      · rename_i dexRecord isDexRecord
        split at isDexStep
        · rename_i method isDexPending
          split at isDexStep
          · rename_i canCommit
            exact ⟨dexRun, dexRecord, method, hasDexRun, isDexRecord, isDexPending, canCommit⟩
          · cases isDexStep
        · cases isDexStep
  obtain ⟨dexRun, dexRecord, method, hasDexRun, isDexRecord, isDexPending, canCommit⟩ := isPending
  rw [← wpf6b_canServeCommit_abstract program config noFailure] at canCommit
  obtain ⟨run, record, history, hasRun, isRecord, isRecordPending, isHistory, isLive⟩ :=
    wpf6_pending_of_abstract program config invariant stepId method dexRun dexRecord hasDexRun
      isDexRecord isDexPending
  obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? run.base stepId record isRecord
  have isMatched := (invariant.runInvariant run hasRun).attemptsMatch record isMember
  unfold AttemptMatchesRecord at isMatched
  rw [isRecordPending] at isMatched
  obtain ⟨attempt, matchedHistory, isAttempt, isMatchedHistory, _, sameMethod, _, _,
    snapshotMatches, _⟩ := isMatched
  rw [isId, isHistory] at isMatchedHistory
  cases isMatchedHistory
  have notBackoff := noFailure.noBackoff run record.id attempt hasRun isAttempt
  have noSnapshot := noFailure.pendingHasNoSnapshot run record method attempt hasRun isMember
    isRecordPending isAttempt
  rw [noSnapshot] at snapshotMatches
  have noArguments : history.arguments = none := by
    cases hasArguments : history.arguments with
    | none => rfl
    | some arguments => rw [hasArguments] at snapshotMatches; exact snapshotMatches.elim
  have isConsistent := invariant.historiesConsistent ((stepId, method), history)
    (wpf6b_mem_of_get? _ _ _ isHistory)
  simp only [HistoryIsConsistent, noArguments] at isConsistent
  obtain ⟨isNotRunFlag, zeroCount, notCommitted⟩ := isConsistent
  have isNotRun : history.progress = .notRun := by
    cases isProgress : history.progress with
    | notRun => rfl
    | _ => rw [isProgress] at isNotRunFlag; cases isNotRunFlag
  rw [isId] at isAttempt
  have isStep : ∃ (change : Run σ → Run σ) (task : Task σ) (arguments : Arguments σ),
      task.base.id = config.nextTaskId ∧ task.key = (stepId, method) ∧ task.emittedCount = 0 ∧
      task.base.phase = .started ∧ arguments.virtualTaskId = config.ghost.nextVirtualTaskId ∧
      step program config (.stepStart stepId false) =
        some ((config.commitRun run true none [] change).1,
          { (config.commitRun run true none [] change).2 with
            tasks := config.tasks ++ [task]
            nextTaskId := config.nextTaskId + 1
            ghost := { (config.commitRun run true none [] change).2.ghost.updateInvocation
                (stepId, method) (fun history => { history with arguments := some arguments }) with
              nextVirtualTaskId :=
                (config.commitRun run true none [] change).2.ghost.nextVirtualTaskId + 1 } }) := by
    refine ⟨?change, ?task, ?arguments, ?_, ?_, ?_, ?_, ?_, ?main⟩
    case main =>
      simp only [step, canCommit, hasRun, isRecord, isAttempt, isRecordPending, notBackoff,
        sameMethod, noSnapshot, decide_true, Bool.not_true, Bool.or_false, Option.isSome_none,
        bne_self_eq_false, Bool.false_eq_true, ↓reduceIte, Bool.not_false]
      rfl
    all_goals rfl
  obtain ⟨change, task, arguments, taskId, taskKey, taskCount, taskPhase, isVirtualId, isStep⟩ :=
    isStep
  have middleParts := wpf6b_ghostParts_reconcile program config noFailure
    (config.commitRun run true none [] change).2 _ none _ (by rfl) rfl (fun _ isReal => isReal)
    (fun _ _ isCompleted => by cases isCompleted) (fun task _ isTask _ _ => ⟨task, isTask, rfl⟩)
  have isReconciled : ∃ middleHistory,
      (config.commitRun run true none [] change).2.ghost.findInvocation? (stepId, method) =
        some middleHistory ∧ wpf6b_IsReconciled none (stepId, method) history middleHistory :=
    wpf6b_marked_isReconciled _ none _ (stepId, method) history isHistory
  obtain ⟨middleHistory, isMiddleHistory, sameArguments, sameProgress, sameCount, sameOutcome⟩ :=
    isReconciled
  apply wpf6b_lift_finish program ignoresAttemptContext config invariant _ rfl _ _ isStep
  · simp only [dexActionsOf, Bool.false_eq_true, ↓reduceIte]
    exact wpf6b_exec_single _ _ _ _ _ isDexStep
  · intro stepId method effect index isMember
    rcases List.mem_append.mp isMember with isInput | isClose
    · cases isInput
    · exact absurd isClose (wpf6b_closeEvents_noFx _ _ stepId method effect index)
  · obtain ⟨sameStamp, sameNextId⟩ := wpf6b_updateInvocation_fields
      (config.commitRun run true none [] change).2.ghost (stepId, method)
      (fun history => { history with arguments := some arguments })
    apply wpf6b_noFailure_of_parts program ignoresAttemptContext config invariant noFailure _ _ _
      isStep (by intro _ _ isSame; cases isSame) (by intro _ _ isSame; cases isSame)
      (by intro isSame; cases isSame) (by intro isSame; cases isSame) noFailure.noZombies
    · exact sameStamp.symm
    · rw [noFailure.nextTaskIdIsVirtual]
      rfl
    · apply wpf6b_ghostParts_addTask _ middleParts noFailure.noZombies (stepId, method)
        middleHistory isMiddleHistory (by rw [sameArguments, noArguments]) ?_
        (by rw [sameCount, zeroCount]) (by rw [sameProgress, isNotRun]) task taskKey taskCount
        taskPhase arguments (by rw [isVirtualId, taskId, noFailure.nextTaskIdIsVirtual])
      rcases sameOutcome with sameOutcome | ⟨_, isCanceled⟩
      · rw [sameOutcome, isLive]; intro isSame; cases isSame
      · rw [isCanceled]; intro isSame; cases isSame

/-- A virtual task that is current under α is the attempt of a real task
that passes the fence. -/
theorem wpf6b_fence_of_current (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (task : Task σ) (isTask : task ∈ config.tasks) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? task.key = some history)
    (arguments : Arguments σ) (isVirtualId : arguments.virtualTaskId = task.base.id)
    (isCurrent : (abstractConfig config).isTaskCurrent
      (arguments.toTask task.key history.virtualPhase) = true) :
    config.isFencePassed task = true := by
  have isRealTasks := wpf6b_realTasks_eq program config noFailure
  have isTaskReal : task ∈ config.realTasks := by rw [isRealTasks]; exact isTask
  unfold Dex.Config.isTaskCurrent at isCurrent
  cases hasRun : config.run with
  | none => simp [abstractConfig, hasRun] at isCurrent
  | some run =>
    have runInvariant := invariant.runInvariant run hasRun
    simp only [abstractConfig, hasRun, Option.map_some] at isCurrent
    rw [show (arguments.toTask task.key history.virtualPhase).step = task.base.step from rfl,
      wpf6b_findStep?_abstract] at isCurrent
    cases isRecord : run.base.findStep? task.base.step with
    | none => rw [isRecord] at isCurrent; cases isCurrent
    | some record =>
      rw [isRecord] at isCurrent
      simp only [Option.map_some, decide_eq_true_eq] at isCurrent
      obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? run.base _ record isRecord
      -- The invocation under α is running, so DexF's is running too.
      cases hasInvocation : record.invocation with
      | none =>
        unfold Run.abstractInvocation at isCurrent
        rw [hasInvocation] at isCurrent
        cases isCurrent
      | some invocation =>
        obtain ⟨method, state⟩ := invocation
        have sameMethod : method = task.base.method := by
          unfold Run.abstractInvocation at isCurrent
          rw [hasInvocation] at isCurrent
          simp only at isCurrent
          split at isCurrent <;> (simp only [Option.some.injEq, Dex.Invocation.mk.injEq] at isCurrent; exact isCurrent.1)
        subst sameMethod
        cases state with
        | pending =>
          exfalso
          unfold Run.abstractInvocation at isCurrent
          rw [hasInvocation] at isCurrent
          simp only at isCurrent
          cases isAttempt : run.findAttempt? record.id with
          | none =>
            rw [isAttempt] at isCurrent
            simp [Arguments.toTask] at isCurrent
          | some attempt =>
            have noSnapshot := noFailure.pendingHasNoSnapshot run record task.base.method attempt
              hasRun isMember hasInvocation isAttempt
            rw [isAttempt] at isCurrent
            simp [noSnapshot, Arguments.toTask] at isCurrent
        | running runningTaskId =>
          rcases runInvariant.runningIsTracked record task.base.method runningTaskId isMember
            hasInvocation with ⟨tracked, isTracked, trackedId⟩ | isDown | ⟨_, _, isInterrupted⟩
          · obtain ⟨trackedKey, attempt, isAttempt, sameNumber⟩ :=
              runInvariant.runningTaskMatches record task.base.method runningTaskId tracked
                isMember hasInvocation (by rw [isRealTasks]; exact isTracked) trackedId
            have trackedReal : tracked ∈ config.realTasks := by rw [isRealTasks]; exact isTracked
            have sameKey : tracked.key = task.key := by rw [trackedKey, isId]; rfl
            have isSame := wpf6b_realTask_eq_of_id program config invariant tracked task trackedReal
              isTaskReal (noFailure.oneTaskPerKey tracked task trackedReal isTaskReal sameKey)
            subst isSame
            unfold Config.isFencePassed
            rw [hasRun]
            simp only
            rw [isRecord, ← isId, isAttempt]
            simp [hasInvocation, trackedId, sameNumber]
          · rw [noFailure.isUp] at isDown; cases isDown
          · rw [noFailure.noInterrupts] at isInterrupted; cases isInterrupted

/-- [STEP-COMPLETE-WAITFOR] and [STEP-COMPLETE-EXECUTE] lift; the commit
leaves no virtual task without a real task. -/
theorem wpf6b_lift_stepComplete (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (taskId : Nat) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.stepComplete taskId) =
      some (dexEvents, dexNext)) :
    wpf6b_LiftResult program config (.stepComplete taskId) dexEvents dexNext := by
  have isRealTasks := wpf6b_realTasks_eq program config noFailure
  have isEnabled : ∃ dexRun dexTask, (abstractConfig config).run = some dexRun ∧
      (abstractConfig config).findTask? taskId = some dexTask ∧
      (abstractConfig config).isTaskCurrent dexTask = true ∧
      (abstractConfig config).canCommit = true ∧
      ((dexTask.method = .waitFor ∧ ∃ response, dexTask.phase = .returned (.waitFor response)) ∨
        (dexTask.method = .execute ∧ ∃ response, dexTask.phase = .returned (.execute response))) := by
    simp only [Dex.step] at isDexStep
    split at isDexStep
    · rename_i dexRun dexTask hasDexRun isFound
      split at isDexStep
      · rename_i isGuard
        simp only [Bool.and_eq_true] at isGuard
        refine ⟨dexRun, dexTask, hasDexRun, isFound, isGuard.1, isGuard.2, ?_⟩
        split at isDexStep
        · rename_i response isMethod isPhase
          exact Or.inl ⟨isMethod, response, isPhase⟩
        · rename_i response isMethod isPhase
          exact Or.inr ⟨isMethod, response, isPhase⟩
        · cases isDexStep
      · cases isDexStep
    · cases isDexStep
  obtain ⟨dexRun, dexTask, hasDexRun, isFound, isCurrent, canCommit, isShape⟩ := isEnabled
  rw [← wpf6b_canServeCommit_abstract program config noFailure] at canCommit
  obtain ⟨task, history, arguments, isTask, hasId, isHistory, hasArguments, isVirtualId,
    isDexTask, _, samePhase, isReal, isVirtualOf⟩ :=
    wpf6b_realTask_of_abstractTask program config invariant noFailure taskId dexTask isFound
  have isTaskReal : task ∈ config.realTasks := by rw [isRealTasks]; exact isTask
  obtain ⟨run, hasRun⟩ : ∃ run, config.run = some run := by
    cases hasRun : config.run with
    | none => simp [abstractConfig, hasRun] at hasDexRun
    | some run => exact ⟨run, rfl⟩
  subst hasId
  rw [isDexTask] at isCurrent
  have isPassed := wpf6b_fence_of_current program config invariant noFailure task isTask history
    isHistory arguments isVirtualId isCurrent
  have isStep : ∃ change, step program config (.stepComplete task.base.id) =
      some ((config.removeRealTask task.base.id).commitRun run true (some task.key) [] change) := by
    rw [isDexTask] at isShape
    have sameMethod : task.base.method = (arguments.toTask task.key history.virtualPhase).method :=
      rfl
    have taskPhase : task.base.phase = (arguments.toTask task.key history.virtualPhase).phase :=
      samePhase
    rcases isShape with ⟨isMethod, response, isPhase⟩ | ⟨isMethod, response, isPhase⟩
    · rw [isMethod] at sameMethod
      rw [isPhase] at taskPhase
      refine ⟨?_, ?_⟩
      rotate_right
      simp only [step, canCommit, hasRun, isReal, isPassed, sameMethod, taskPhase, ↓reduceIte]
      rfl
    · rw [isMethod] at sameMethod
      rw [isPhase] at taskPhase
      refine ⟨?_, ?_⟩
      rotate_right
      simp only [step, canCommit, hasRun, isReal, isPassed, sameMethod, taskPhase, ↓reduceIte]
      rfl
  obtain ⟨change, isStep⟩ := isStep
  have nextInvariant := invariant_step program ignoresAttemptContext config invariant _ _ _ isStep
  have nextRealTasks : ((config.removeRealTask task.base.id).commitRun run true (some task.key) []
      change).2.realTasks =
      config.tasks.filter (fun other => !decide (other.base.id = task.base.id)) := by
    simp only [Config.commitRun, Config.realTasks, Config.removeRealTask, noFailure.noZombies,
      List.filter_nil, List.append_nil]
  have keptTask : ∀ other, other ∈ config.tasks → other.base.id ≠ task.base.id →
      other ∈ ((config.removeRealTask task.base.id).commitRun run true (some task.key) []
        change).2.tasks := by
    intro other isOther otherId
    simp only [Config.commitRun, Config.removeRealTask]
    exact List.mem_filter.mpr ⟨isOther, by simpa using otherId⟩
  have otherKey : ∀ other, other ∈ config.tasks → other.key = task.key →
      other.base.id = task.base.id := fun other isOther sameKey =>
    noFailure.oneTaskPerKey other task (by rw [isRealTasks]; exact isOther) isTaskReal sameKey
  have isReconciled : ∀ key after,
      ((config.removeRealTask task.base.id).commitRun run true (some task.key) [] change).2.ghost.findInvocation?
        key = some after →
      (∃ before, config.ghost.findInvocation? key = some before ∧
        wpf6b_IsReconciled (some task.key) key before after) ∨
      (config.ghost.findInvocation? key = none ∧ after.arguments = none) :=
    fun key after isFound => by
      have reconciled := wpf6b_reconcile_findInvocation? _ (some task.key) _ key after isFound
      exact reconciled
  apply wpf6b_lift_finish program ignoresAttemptContext config invariant _ rfl _ _ isStep
  · simp only [dexActionsOf, Config.onVirtualTask, isVirtualOf, isStep]
    have noAbandoned : abandonedVirtualTasks config
        ((config.removeRealTask task.base.id).commitRun run true (some task.key) [] change).2 =
        [] := by
      unfold abandonedVirtualTasks
      simp only
      rw [List.filterMap_eq_nil_iff.mpr]
      · rfl
      intro entry isEntry
      have isEntryFound := wpf6b_findInvocation?_of_mem program _ nextInvariant entry isEntry
      cases hasEntryArguments : entry.2.arguments with
      | none => rfl
      | some entryArguments =>
        cases isPrevious : config.ghost.findInvocation? entry.1 with
        | none => rfl
        | some previous =>
          simp only
          cases isPreviousLive : previous.outcome with
          | committed => simp
          | canceled => simp
          | live =>
            cases isEntryCanceled : entry.2.outcome with
            | live => simp
            | committed => simp
            | canceled =>
              simp only [decide_true, Bool.true_and, Bool.not_eq_eq_eq_not, Bool.not_true,
                ite_eq_right_iff, reduceCtorEq, imp_false, Bool.not_eq_false]
              rcases isReconciled entry.1 entry.2 isEntryFound with
                ⟨before, isBefore, sameArguments, _, _, sameOutcome⟩ | ⟨isNone, _⟩
              · change config.ghost.findInvocation? entry.1 = some before at isBefore
                rw [isPrevious] at isBefore
                cases isBefore
                have notCompleted : entry.1 ≠ task.key := by
                  intro isSame
                  rcases sameOutcome with sameOutcome | ⟨_, isOutcome⟩
                  · rw [isEntryCanceled, isPreviousLive] at sameOutcome; cases sameOutcome
                  · rw [isEntryCanceled, isSame] at isOutcome; simp at isOutcome
                obtain ⟨other, isOther, hasKey⟩ := noFailure.liveHasTask entry.1 previous
                  entryArguments isPrevious isPreviousLive
                  (by rw [← sameArguments]; exact hasEntryArguments)
                have otherId : other.base.id ≠ task.base.id := by
                  intro sameId
                  have isSame := wpf6b_realTask_eq_of_id program config invariant other task
                    (by rw [isRealTasks]; exact isOther) isTaskReal sameId
                  subst isSame
                  exact notCompleted hasKey.symm
                unfold Config.hasVirtualTask
                simp only [hasEntryArguments, isEntryCanceled, Option.isSome_some, Bool.true_and,
                  List.any_eq_true, decide_eq_true_eq]
                refine ⟨other, ?_, hasKey⟩
                rw [nextRealTasks]
                exact List.mem_filter.mpr ⟨isOther, by simpa using otherId⟩
              · change config.ghost.findInvocation? entry.1 = none at isNone
                rw [isPrevious] at isNone
                cases isNone
    rw [noAbandoned, List.append_nil]
    exact wpf6b_exec_single _ _ _ _ _ isDexStep
  · intro stepId method effect index isMember
    rcases List.mem_append.mp isMember with isInput | isClose
    · cases isInput
    · exact absurd isClose (wpf6b_closeEvents_noFx _ _ stepId method effect index)
  · apply wpf6b_noFailure_of_parts program ignoresAttemptContext config invariant noFailure _ _ _
      isStep (by intro _ _ isSame; cases isSame) (by intro _ _ isSame; cases isSame)
      (by intro isSame; cases isSame) (by intro isSame; cases isSame)
      (by simp only [Config.removeRealTask, noFailure.noZombies, List.filter_nil]) rfl
      noFailure.nextTaskIdIsVirtual
    apply wpf6b_ghostParts_reconcile program config noFailure _ _ (some task.key) _ (by rfl) rfl
    · intro other isOther
      replace isOther : other ∈ config.tasks.filter
          (fun other => !decide (other.base.id = task.base.id)) := by
        rw [← nextRealTasks]; exact isOther
      rw [isRealTasks]
      exact (List.mem_filter.mp isOther).1
    · intro other isOther isCompleted
      replace isOther : other ∈ config.tasks.filter
          (fun other => !decide (other.base.id = task.base.id)) := by
        rw [← nextRealTasks]; exact isOther
      obtain ⟨isOtherTask, otherId⟩ := List.mem_filter.mp isOther
      have sameKey : other.key = task.key := (Option.some.inj isCompleted).symm
      have sameId := otherKey other isOtherTask sameKey
      simp [sameId] at otherId
    · intro other nextHistory isOther isNextHistory isLive
      by_cases otherId : other.base.id = task.base.id
      · exfalso
        have isSame := wpf6b_realTask_eq_of_id program config invariant other task
          (by rw [isRealTasks]; exact isOther) isTaskReal otherId
        subst isSame
        exact wpf6_stepComplete_ends program config invariant other.base.id _ _ isStep
          nextInvariant other isReal nextHistory isNextHistory isLive
      · exact ⟨other, keptTask other isOther otherId, rfl⟩

/-! ## Statements -/

/-- The initial configuration has no failure. -/
theorem wpf6_noFailure_initial (program : Program σ) : NoFailure program (Config.initial σ) := by
  refine
    { isUp := rfl
      noZombies := rfl
      noInterrupts := rfl
      noRetryTimers := ?_
      lastCommitIsNormal := rfl
      nextTaskIdIsVirtual := rfl
      noBackoff := ?_
      pendingHasNoSnapshot := ?_
      tasksMatchHistories := ?_
      tasksNotCommitted := ?_
      oneTaskPerKey := ?_
      liveHasTask := ?_ } <;>
    simp [Config.initial, Config.realTasks, Ghost.initial, Ghost.findInvocation?, AssocList.get?]

/-- **The lift.** On a configuration with no failure, every Dex step of α
lifts to a DexF step with no failure: the lifted label is enabled, its
events are the Dex events with ghost indexes, none of them is a repeated
`fx`, α of the result is the Dex result, and the result has no failure. -/
theorem wpf6_lift_step (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (config : Config σ) (invariant : Invariant program config)
    (noFailure : NoFailure program config) (dexAction : Dex.Action σ)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) dexAction =
      some (dexEvents, dexNext)) :
    ∃ events next, step program config (liftAction startRequestId dexAction) = some (events, next) ∧
      abstractConfig next = dexNext ∧ dexEvents = events.map Obs.toDex ∧
      eraseStepEvents config events = events.map Obs.toDex ∧ NoFailure program next := by
  show wpf6b_LiftResult program config (liftAction startRequestId dexAction) dexEvents dexNext
  cases dexAction with
  | tick duration =>
    exact wpf6b_lift_tick program ignoresAttemptContext config invariant noFailure duration
      dexEvents dexNext isDexStep
  | request request =>
    cases request with
    | startFlow stepType input =>
      exact wpf6b_lift_startFlow program ignoresAttemptContext startRequestId config invariant
        noFailure stepType input dexEvents dexNext isDexStep
    | publish chan value =>
      exact wpf6b_lift_publish program ignoresAttemptContext config invariant noFailure chan value
        dexEvents dexNext isDexStep
  | stepStart stepId =>
    exact wpf6b_lift_stepStart program ignoresAttemptContext config invariant noFailure stepId
      dexEvents dexNext isDexStep
  | workerRun taskId =>
    exact wpf6b_lift_workerRun program ignoresAttemptContext config invariant noFailure taskId
      dexEvents dexNext isDexStep
  | workerEffect taskId =>
    exact wpf6b_lift_workerEffect program ignoresAttemptContext config invariant noFailure taskId
      dexEvents dexNext isDexStep
  | workerReturn taskId =>
    exact wpf6b_lift_workerReturn program ignoresAttemptContext config invariant noFailure taskId
      dexEvents dexNext isDexStep
  | workerAbandon taskId =>
    exact wpf6b_lift_abandon program ignoresAttemptContext config invariant noFailure taskId
      dexEvents dexNext isDexStep
  | stepComplete taskId =>
    exact wpf6b_lift_stepComplete program ignoresAttemptContext config invariant noFailure taskId
      dexEvents dexNext isDexStep
  | stepCompleteStale taskId =>
    exact wpf6b_lift_abandon program ignoresAttemptContext config invariant noFailure taskId
      dexEvents dexNext (wpf6b_abandon_of_stale _ _ taskId dexEvents dexNext isDexStep)
  | timerFire count =>
    exact wpf6b_lift_timerFire program ignoresAttemptContext config invariant noFailure count
      dexEvents dexNext isDexStep

/-- With no failure, a pending invocation of the DexF run is pending under α. -/
theorem wpf6_abstract_pending_of_noFailure (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (run : Run σ) (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ) (method : Dex.Method)
    (hasRun : config.run = some run) (isFound : run.base.findStep? stepId = some record)
    (isPending : record.invocation = some { method := method, state := .pending }) :
    ∃ dexRun dexRecord, (abstractConfig config).run = some dexRun ∧
      dexRun.findStep? stepId = some dexRecord ∧
      dexRecord.invocation = some { method := method, state := .pending } := by
  obtain ⟨isMember, hasId⟩ := Dex.Run.mem_of_findStep? run.base stepId record isFound
  refine ⟨run.abstract config.ghost, _, by simp [abstractConfig, hasRun],
    by rw [wpf6b_findStep?_abstract, isFound]; rfl, ?_⟩
  show run.abstractInvocation config.ghost record = _
  unfold Run.abstractInvocation
  rw [isPending]
  simp only
  cases isAttempt : run.findAttempt? record.id with
  | none => rfl
  | some attempt =>
    have noSnapshot := noFailure.pendingHasNoSnapshot run record method attempt hasRun isMember
      isPending isAttempt
    simp only [noSnapshot, Option.isSome_none]

/-- With no failure, a real task is the task of α with the same ID and phase. -/
theorem wpf6_abstract_task_of_noFailure (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (noFailure : NoFailure program config)
    (taskId : Nat) (task : Task σ) (isFound : config.findRealTask? taskId = some task) :
    ∃ dexTask, (abstractConfig config).findTask? taskId = some dexTask ∧
      dexTask.phase = task.base.phase := by
  obtain ⟨isReal, hasId⟩ := (wpf6_findRealTask?_iff program config invariant taskId task).mp isFound
  obtain ⟨history, arguments, isHistory, hasArguments, isVirtualId, _, _⟩ :=
    noFailure.tasksMatchHistories task isReal
  have hasVirtual : config.hasVirtualTask task.key history = true := by
    unfold Config.hasVirtualTask
    rw [hasArguments]
    cases isOutcome : history.outcome with
    | live => rfl
    | committed => exact absurd isOutcome (noFailure.tasksNotCommitted task history isReal isHistory)
    | canceled =>
      simp only [Option.isSome_some, Bool.true_and, List.any_eq_true, decide_eq_true_eq]
      exact ⟨task, isReal, rfl⟩
  have isVirtualMember : arguments.toTask task.key history.virtualPhase ∈ config.virtualTasks := by
    unfold Config.virtualTasks
    rw [mem_sortStable, List.mem_filterMap]
    refine ⟨(task.key, history), wpf6b_mem_of_get? _ _ _ isHistory, ?_⟩
    simp only [hasArguments, hasVirtual, ↓reduceIte]
  cases isDexFound : (abstractConfig config).findTask? taskId with
  | none =>
    have noMatch := List.find?_eq_none.mp isDexFound _ isVirtualMember
    simp [Arguments.toTask, isVirtualId, hasId] at noMatch
  | some dexTask =>
    obtain ⟨realTask, realHistory, realArguments, _, _, _, _, _, isDexTask, _, samePhase,
      isRealFound, _⟩ :=
      wpf6b_realTask_of_abstractTask program config invariant noFailure taskId dexTask isDexFound
    rw [isFound] at isRealFound
    cases isRealFound
    refine ⟨dexTask, rfl, ?_⟩
    rw [isDexTask, samePhase]
    rfl

/-- D6 holds by the premise of the timer rule: every timer among the first
`count` due timers is due. -/
theorem wpf6_firedTimers_due (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (run : Run σ) (hasRun : config.run = some run)
    (count : Nat) (timer : DueTimer σ) (fireTime : Nat)
    (isFired : timer ∈ (run.dueTimers config.clock).take count)
    (isArmed : run.fireTimeOf? timer = some fireTime) : fireTime ≤ config.clock := by
  have runInvariant := invariant.runInvariant run hasRun
  have isDue := List.mem_of_mem_take isFired
  simp only [Run.dueTimers, List.mem_map] at isDue
  obtain ⟨entry, isSorted, isTimer⟩ := isDue
  rw [mem_sortStable, List.mem_append] at isSorted
  rcases isSorted with isWait | isRetry
  · obtain ⟨waitEntry, isFiltered, isEntry⟩ := List.mem_map.mp isWait
    obtain ⟨isMember, isDueEntry⟩ := List.mem_filter.mp isFiltered
    subst isEntry
    subst isTimer
    have isFound := wpf6b_get?_of_mem_nodup run.base.timers waitEntry.1 waitEntry.2
      runInvariant.waitTimersNodup isMember
    simp only [Run.fireTimeOf?] at isArmed
    rw [isFound] at isArmed
    cases isArmed
    simpa using isDueEntry
  · obtain ⟨retryEntry, isFiltered, isEntry⟩ := List.mem_map.mp isRetry
    obtain ⟨isMember, isDueEntry⟩ := List.mem_filter.mp isFiltered
    subst isEntry
    subst isTimer
    have isFound := wpf6b_get?_of_mem_nodup run.retryTimers retryEntry.1 retryEntry.2
      runInvariant.retryTimersNodup isMember
    simp only [Run.fireTimeOf?] at isArmed
    rw [isFound] at isArmed
    cases isArmed
    simpa using isDueEntry

end DexExpressiveness.DexF
