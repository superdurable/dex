import DexExpressiveness.Failure.Composite.WPF5Forward
import DexExpressiveness.Dex.RunLemmas

/-!
# WP-F5: the shape of a DexF run without failures, and lookups through α

`WPF5FreeShape` is what a DexF run with no failure transition keeps: the
server is up, nothing is a zombie or interrupted, no retry timer is armed,
every commit is normal, and the real tasks are exactly the virtual tasks of α,
with the same IDs, phases, and effect counters. The lemmas here read the
ghosts after a commit and look a virtual task of α up as its real task.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- The shape of a DexF configuration that a run without failures reaches. -/
structure WPF5FreeShape (config : Config σ) : Prop where
  /-- The server is up. -/
  isUp : config.isUp = true
  /-- No task is a zombie. -/
  noZombies : config.zombies = []
  /-- No attempt is interrupted. -/
  noInterrupts : config.interrupts = []
  /-- Every commit was normal. -/
  lastCommitIsNormal : config.lastCommitAt = config.ghost.lastNormalCommitAt
  /-- No retry timer is armed. -/
  noRetryTimers : ∀ run, config.run = some run → run.retryTimers = []
  /-- Every Step start computed a snapshot. -/
  nextTaskIds : config.nextTaskId = config.ghost.nextVirtualTaskId
  /-- Each real task is the virtual task of its invocation. -/
  tasksMatch : ∀ task, task ∈ config.tasks →
    ∃ history arguments, config.ghost.findInvocation? task.key = some history ∧
      history.arguments = some arguments ∧ arguments.virtualTaskId = task.base.id ∧
      task.emittedCount = history.emittedCount ∧ task.base.phase = history.virtualPhase ∧
      history.outcome ≠ .committed
  /-- A started current invocation has its real task. -/
  liveHaveTasks : ∀ key history, config.ghost.findInvocation? key = some history →
    history.outcome = .live → history.arguments.isSome = true →
    ∃ task, task ∈ config.tasks ∧ task.key = key
  /-- At most one real task per invocation. -/
  taskKeysNodup : (config.tasks.map Task.key).Nodup

/-! ## Association lists -/

theorem wpf5_get?_of_mem {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (keysNodup : (map.map (fun entry => entry.1)).Nodup)
    (isMember : (key, value) ∈ map) : AssocList.get? map key = some value := by
  induction map with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    simp only [List.map_cons, List.nodup_cons, List.mem_map] at keysNodup
    rcases List.mem_cons.mp isMember with isHead | isRest
    · cases isHead
      simp [AssocList.get?]
    · have isOther : headKey ≠ key := by
        intro isSame
        subst isSame
        exact keysNodup.1 ⟨(headKey, value), isRest, rfl⟩
      simp [AssocList.get?, isOther, inductionHypothesis keysNodup.2 isRest]

theorem wpf5_mem_of_get? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (isFound : AssocList.get? map key = some value) : (key, value) ∈ map := by
  induction map with
  | nil => cases isFound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    simp only [AssocList.get?] at isFound
    split at isFound
    · rename_i isKey
      subst isKey
      cases isFound
      exact List.mem_cons_self ..
    · exact List.mem_cons_of_mem _ (inductionHypothesis isFound)

theorem wpf5_get?_map {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (change : κ × ν → κ × ν) (keepsKey : ∀ entry, (change entry).1 = entry.1) (key : κ) :
    AssocList.get? (map.map change) key =
      (AssocList.get? map key).map (fun value => (change (key, value)).2) := by
  induction map with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    have isHeadKey := keepsKey (headKey, headValue)
    simp only [List.map_cons, AssocList.get?]
    rcases isChanged : change (headKey, headValue) with ⟨changedKey, changedValue⟩
    rw [isChanged] at isHeadKey
    simp only at isHeadKey
    subst isHeadKey
    by_cases isKey : changedKey = key
    · subst isKey
      simp [isChanged]
    · simp [isKey, inductionHypothesis]

theorem wpf5_get?_foldl_addMissing {κ ν : Type} [DecidableEq κ] (fresh : ν) (key : κ) :
    ∀ (keys : List κ) (entries : AssocList κ ν),
      AssocList.get? (keys.foldl (fun (current : AssocList κ ν) added =>
          if current.contains added then current else current.set added fresh) entries) key =
        match AssocList.get? entries key with
        | some value => some value
        | none => if key ∈ keys then some fresh else none := by
  intro keys
  induction keys with
  | nil =>
    intro entries
    cases isFound : AssocList.get? entries key <;> simp [isFound]
  | cons added laterKeys inductionHypothesis =>
    intro entries
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    by_cases isContained : entries.contains added = true
    · simp only [isContained, ite_true]
      cases isFound : AssocList.get? entries key with
      | some value => rfl
      | none =>
        have isOther : key ≠ added := by
          intro isSame
          subst isSame
          simp [AssocList.contains, isFound] at isContained
        simp [isOther]
    · simp only [isContained, Bool.false_eq_true, ite_false]
      by_cases isAdded : key = added
      · subst isAdded
        have isAbsent : AssocList.get? entries key = none := by
          simpa [AssocList.contains] using isContained
        simp [AssocList.get?_set_self, isAbsent]
      · rw [AssocList.get?_set_other _ _ _ _ isAdded]
        cases AssocList.get? entries key <;> simp [isAdded]

/-! ## The ghosts after a commit -/

/-- How a commit changes the history of an invocation: only an invocation that
is no longer current ends, and only its outcome changes. -/
def WPF5HistoryEnds (completed : Option (InvocationKey σ)) (key : InvocationKey σ)
    (before after : InvocationHistory σ) : Prop :=
  after.arguments = before.arguments ∧ after.emittedCount = before.emittedCount ∧
    after.progress = before.progress ∧
    (after.outcome = before.outcome ∨
      (before.outcome = .live ∧
        after.outcome = if completed = some key then .committed else .canceled))

theorem wpf5_reconcile_findInvocation (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) :
    (ghost.reconcile completed base).findInvocation? key =
      match ghost.findInvocation? key with
      | some before =>
        some (if before.outcome = .live &&
            !(base.steps.filterMap (fun record => record.invocation.map (fun invocation =>
              (record.id, invocation.method)))).contains key then
          { before with outcome := if completed = some key then .committed else .canceled }
        else before)
      | none =>
        if key ∈ base.steps.filterMap (fun record => record.invocation.map (fun invocation =>
            (record.id, invocation.method))) then
          some (InvocationHistory.fresh ghost.normalCount)
        else none := by
  unfold Ghost.reconcile Ghost.findInvocation?
  simp only
  rw [wpf5_get?_foldl_addMissing, wpf5_get?_map _ _ (by intro entry; split <;> rfl)]
  cases isFound : AssocList.get? ghost.invocations key with
  | none => rfl
  | some before =>
    simp only [Option.map_some]
    split <;> simp

theorem wpf5_historyEnds_of_some (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) (before : InvocationHistory σ)
    (isBefore : ghost.findInvocation? key = some before) :
    ∃ after, (ghost.reconcile completed base).findInvocation? key = some after ∧
      WPF5HistoryEnds completed key before after := by
  rw [wpf5_reconcile_findInvocation, isBefore]
  refine ⟨_, rfl, ?_⟩
  split
  · rename_i isEnded
    simp only [Bool.and_eq_true, decide_eq_true_eq] at isEnded
    exact ⟨rfl, rfl, rfl, .inr ⟨isEnded.1, rfl⟩⟩
  · exact ⟨rfl, rfl, rfl, .inl rfl⟩

theorem wpf5_historyEnds_of_after (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) (after : InvocationHistory σ)
    (isAfter : (ghost.reconcile completed base).findInvocation? key = some after) :
    (∃ before, ghost.findInvocation? key = some before ∧
      WPF5HistoryEnds completed key before after) ∨
    (ghost.findInvocation? key = none ∧ after = InvocationHistory.fresh ghost.normalCount) := by
  cases isBefore : ghost.findInvocation? key with
  | some before =>
    obtain ⟨found, isFound, ends⟩ := wpf5_historyEnds_of_some ghost completed base key before isBefore
    rw [isFound] at isAfter
    cases isAfter
    exact .inl ⟨before, rfl, ends⟩
  | none =>
    simp only [wpf5_reconcile_findInvocation, isBefore] at isAfter
    split at isAfter
    · cases isAfter
      exact .inr ⟨rfl, rfl⟩
    · cases isAfter

theorem wpf5_updateInvocation_find (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) (other : InvocationKey σ) :
    (ghost.updateInvocation key change).findInvocation? other =
      if other = key then (ghost.findInvocation? key).map change
      else ghost.findInvocation? other := by
  unfold Ghost.updateInvocation
  cases isFound : ghost.findInvocation? key with
  | none =>
    by_cases isKey : other = key
    · subst isKey
      simp [isFound]
    · simp [isKey]
  | some history =>
    by_cases isKey : other = key
    · subst isKey
      simp [Ghost.findInvocation?, AssocList.get?_set_self]
    · simp only [isKey, ite_false]
      exact AssocList.get?_set_other _ _ _ _ isKey

/-! ## Lookups -/

theorem wpf5_find?_of_mem {α β : Type} [DecidableEq β] (image : α → β) (elements : List α)
    (element : α) (isMember : element ∈ elements) (imagesNodup : (elements.map image).Nodup) :
    elements.find? (fun candidate => decide (image candidate = image element)) = some element := by
  cases isFound : elements.find? (fun candidate => decide (image candidate = image element)) with
  | none =>
    have noMatch := List.find?_eq_none.mp isFound element isMember
    simp at noMatch
  | some found =>
    have isFoundMember := List.mem_of_find?_eq_some isFound
    have isSame : image found = image element := by simpa using List.find?_some isFound
    rw [eq_of_nodup_map image elements imagesNodup isFoundMember isMember isSame]

theorem wpf5_findLiveTask (config : Config σ) (invariant : Invariant program config)
    (shape : WPF5FreeShape config) (task : Task σ) (isMember : task ∈ config.tasks) :
    config.findLiveTask? task.base.id = some task ∧ config.findRealTask? task.base.id = some task := by
  have idsNodup := invariant.realTaskIdsNodup
  simp only [Config.realTasks, shape.noZombies, List.append_nil] at idsNodup
  have isFound := wpf5_find?_of_mem (fun candidate : Task σ => candidate.base.id) config.tasks task
    isMember idsNodup
  refine ⟨isFound, ?_⟩
  simp only [Config.findRealTask?, Config.realTasks, shape.noZombies, List.append_nil]
  exact isFound

theorem wpf5_eq_of_key (config : Config σ) (shape : WPF5FreeShape config) (first second : Task σ)
    (isFirst : first ∈ config.tasks) (isSecond : second ∈ config.tasks)
    (isSameKey : first.key = second.key) : first = second :=
  eq_of_nodup_map Task.key config.tasks shape.taskKeysNodup isFirst isSecond isSameKey

theorem wpf5_eq_of_id (config : Config σ) (invariant : Invariant program config)
    (shape : WPF5FreeShape config) (first second : Task σ)
    (isFirst : first ∈ config.tasks) (isSecond : second ∈ config.tasks)
    (isSameId : first.base.id = second.base.id) : first = second := by
  have idsNodup := invariant.realTaskIdsNodup
  simp only [Config.realTasks, shape.noZombies, List.append_nil] at idsNodup
  exact eq_of_nodup_map (fun candidate : Task σ => candidate.base.id) config.tasks idsNodup
    isFirst isSecond isSameId

theorem wpf5_findInvocation_mem (ghost : Ghost σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) (isFound : ghost.findInvocation? key = some history) :
    (key, history) ∈ ghost.invocations :=
  wpf5_mem_of_get? _ _ _ isFound

/-- A virtual task of α is the virtual task of a real task with its ID. -/
theorem wpf5_virtualTask_lookup (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (virtualTaskId : Nat)
    (virtualTask : Dex.Task σ)
    (isFound : (abstractConfig config).findTask? virtualTaskId = some virtualTask) :
    ∃ task history arguments, task ∈ config.tasks ∧ task.base.id = virtualTaskId ∧
      config.ghost.findInvocation? task.key = some history ∧
      history.arguments = some arguments ∧ arguments.virtualTaskId = virtualTaskId ∧
      task.emittedCount = history.emittedCount ∧ task.base.phase = history.virtualPhase ∧
      history.outcome ≠ .committed ∧
      virtualTask = arguments.toTask task.key history.virtualPhase := by
  unfold Dex.Config.findTask? at isFound
  have isId : virtualTask.id = virtualTaskId := by simpa using List.find?_some isFound
  have isMember := List.mem_of_find?_eq_some isFound
  simp only [abstractConfig, Config.virtualTasks, mem_sortStable, List.mem_filterMap] at isMember
  obtain ⟨⟨key, history⟩, isEntry, isVirtual⟩ := isMember
  simp only at isVirtual
  split at isVirtual
  · rename_i arguments isArguments
    split at isVirtual
    · rename_i hasVirtual
      cases isVirtual
      have isHistory : config.ghost.findInvocation? key = some history :=
        wpf5_get?_of_mem _ _ _ invariant.ghostKeysNodup isEntry
      obtain ⟨task, isTask, isKey⟩ : ∃ task, task ∈ config.tasks ∧ task.key = key := by
        simp only [Config.hasVirtualTask, isArguments, Option.isSome_some, Bool.true_and]
          at hasVirtual
        split at hasVirtual
        · rename_i isLive
          exact shape.liveHaveTasks key history isHistory isLive (by simp [isArguments])
        · cases hasVirtual
        · simp only [Config.realTasks, shape.noZombies, List.append_nil, List.any_eq_true,
            decide_eq_true_eq] at hasVirtual
          exact hasVirtual
      subst isKey
      obtain ⟨found, foundArguments, isFoundHistory, isFoundArguments, isVirtualId, isEmitted,
        isPhase, isNotCommitted⟩ := shape.tasksMatch task isTask
      rw [isHistory] at isFoundHistory
      cases isFoundHistory
      rw [isArguments] at isFoundArguments
      cases isFoundArguments
      have isTaskId : task.base.id = virtualTaskId := by
        rw [← isVirtualId, ← isId]
        rfl
      exact ⟨task, history, arguments, isTask, isTaskId, isHistory, isArguments,
        by rw [← isId]; rfl, isEmitted, isPhase, isNotCommitted, rfl⟩
    · cases isVirtual
  · cases isVirtual

theorem wpf5_virtualTaskIdOf (config : Config σ) (invariant : Invariant program config)
    (shape : WPF5FreeShape config) (task : Task σ) (isMember : task ∈ config.tasks)
    (history : InvocationHistory σ) (arguments : Arguments σ)
    (isHistory : config.ghost.findInvocation? task.key = some history)
    (isArguments : history.arguments = some arguments) :
    config.virtualTaskIdOf task.base.id = some arguments.virtualTaskId := by
  unfold Config.virtualTaskIdOf
  rw [(wpf5_findLiveTask config invariant shape task isMember).2]
  simp [Ghost.argumentsOf, isHistory, isArguments]

/-- α keeps every field of a Step record except the invocation and the wait's
registration `seq`. -/
theorem wpf5_abstract_findStep (run : Run σ) (ghost : Ghost σ) (step : Dex.StepExecId σ) :
    (run.abstract ghost).findStep? step =
      (run.base.findStep? step).map (fun record =>
        { record with
          invocation := run.abstractInvocation ghost record
          waitState := record.waitState.map (fun waitState =>
            { waitState with registeredSeq := ghost.normalIndexOf waitState.registeredSeq }) }) := by
  simp only [Dex.Run.findStep?, Run.abstract, List.find?_map]
  rfl

end DexExpressiveness.DexF
