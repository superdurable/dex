import DexExpressiveness.Failure.Proofs.WPF1CompleteBase

/-!
# WP-F1: the virtual tasks after a Complete rule

A Complete rule commits the invocation of its task: α loses the virtual task
of that invocation (Dex removes it with the Complete rule), and every
invocation that the commit canceled and that has no real task left loses its
virtual task too (Dex drops it with [WORKER-ABANDON]). Every other virtual
task stays.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-! ## The context of a Complete rule -/

/-- What the fence and the invariant say about the task of a Complete rule. -/
theorem complete_context {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) {run : Run σ} (hasRun : config.run = some run)
    {taskId : Nat} {task : Task σ} (isTask : config.findRealTask? taskId = some task)
    (isFence : config.isFencePassed task = true) :
    ∃ record attempt history arguments,
      run.base.findStep? task.base.step = some record ∧ record ∈ run.base.steps ∧
      record.id = task.base.step ∧ run.findAttempt? task.base.step = some attempt ∧
      record.invocation = some { method := task.base.method, state := .running task.base.id } ∧
      attempt.method = task.base.method ∧ attempt.snapshot.isSome = true ∧
      config.ghost.findInvocation? task.key = some history ∧ history.outcome = .live ∧
      history.arguments = some arguments ∧
      TaskFollowsHistory program.failureFreeFlow task history ∧
      HistoryIsConsistent program.failureFreeFlow task.key history ∧
      task ∈ config.realTasks ∧ task.base.id = taskId := by
  have runInvariant := invariant.runInvariant run hasRun
  have isReal : task ∈ config.realTasks := List.mem_of_find?_eq_some isTask
  have isTaskId : task.base.id = taskId := by simpa using List.find?_some isTask
  unfold Config.isFencePassed at isFence
  rw [hasRun] at isFence
  dsimp only at isFence
  cases isFound : run.base.findStep? task.base.step with
  | none => rw [isFound] at isFence; cases isFence
  | some record =>
    cases isAttempt : run.findAttempt? task.base.step with
    | none => rw [isFound, isAttempt] at isFence; cases isFence
    | some attempt =>
      rw [isFound, isAttempt] at isFence
      simp only [Bool.and_eq_true, decide_eq_true_eq] at isFence
      obtain ⟨hasInvocation, _⟩ := isFence
      obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? _ _ _ isFound
      have isMatch := runInvariant.attemptsMatch record isMember
      unfold AttemptMatchesRecord at isMatch
      rw [hasInvocation] at isMatch
      obtain ⟨matchedAttempt, history, isMatchedAttempt, isHistory, isLive, isMethod, _,
        isRunning, _⟩ := isMatch
      rw [isId, isAttempt] at isMatchedAttempt
      cases isMatchedAttempt
      obtain ⟨_, hasSnapshot⟩ := isRunning task.base.id rfl
      rw [isId] at isHistory
      simp only at isHistory isMethod
      replace isHistory : config.ghost.findInvocation? task.key = some history := isHistory
      obtain ⟨followed, isFollowed, follows⟩ := invariant.realTasksFollowHistory task isReal
      rw [isHistory] at isFollowed
      cases isFollowed
      obtain ⟨arguments, hasArguments, _⟩ := id follows
      have isConsistent := invariant.historiesConsistent (task.key, history)
        (mem_of_get? _ _ _ isHistory)
      exact ⟨record, attempt, history, arguments, rfl, isMember, isId, rfl, hasInvocation,
        isMethod, hasSnapshot, isHistory, isLive, hasArguments, follows, isConsistent, isReal,
        isTaskId⟩

/-! ## Distinct virtual task IDs -/

theorem entry_eq_of_virtualTaskId
    (invocations : AssocList (InvocationKey σ) (InvocationHistory σ))
    (idsNodup : (virtualTaskIds invocations).Nodup)
    (first second : InvocationKey σ × InvocationHistory σ) (isFirst : first ∈ invocations)
    (isSecond : second ∈ invocations) (firstArguments secondArguments : Arguments σ)
    (hasFirst : first.2.arguments = some firstArguments)
    (hasSecond : second.2.arguments = some secondArguments)
    (sameId : firstArguments.virtualTaskId = secondArguments.virtualTaskId) : first = second := by
  induction invocations with
  | nil => cases isFirst
  | cons head rest inductionHypothesis =>
    unfold virtualTaskIds at idsNodup inductionHypothesis
    have inRest : ∀ entry arguments, entry ∈ rest → entry.2.arguments = some arguments →
        arguments.virtualTaskId ∈ (rest.filterMap (fun entry => entry.2.arguments)).map
          (fun arguments => arguments.virtualTaskId) := by
      intro entry arguments isMember hasArguments
      exact List.mem_map.mpr ⟨arguments, List.mem_filterMap.mpr ⟨entry, isMember, hasArguments⟩,
        rfl⟩
    rw [List.filterMap_cons] at idsNodup
    rcases List.mem_cons.mp isFirst with isFirstHead | firstInRest <;>
      rcases List.mem_cons.mp isSecond with isSecondHead | secondInRest
    · exact isFirstHead.trans isSecondHead.symm
    · subst isFirstHead
      rw [hasFirst, List.map_cons, List.nodup_cons] at idsNodup
      exact absurd (sameId ▸ inRest second secondArguments secondInRest hasSecond) idsNodup.1
    · subst isSecondHead
      rw [hasSecond, List.map_cons, List.nodup_cons] at idsNodup
      exact absurd (sameId ▸ inRest first firstArguments firstInRest hasFirst) idsNodup.1
    · apply inductionHypothesis _ firstInRest secondInRest
      cases hasHead : head.2.arguments with
      | none =>
        rw [hasHead] at idsNodup
        exact idsNodup
      | some arguments =>
        rw [hasHead] at idsNodup
        simp only [List.map_cons, List.nodup_cons] at idsNodup
        exact idsNodup.2

theorem markHistory_arguments (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (entry : InvocationKey σ × InvocationHistory σ) :
    (markHistory completed current entry).2.arguments = entry.2.arguments := by
  unfold markHistory
  split <;> rfl

theorem markHistory_key (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (entry : InvocationKey σ × InvocationHistory σ) :
    (markHistory completed current entry).1 = entry.1 := by
  unfold markHistory
  split <;> rfl

theorem markHistory_virtualPhase (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (entry : InvocationKey σ × InvocationHistory σ) :
    (markHistory completed current entry).2.virtualPhase = entry.2.virtualPhase := by
  unfold markHistory
  split <;> rfl

theorem virtualTaskIds_map_markHistory (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ))
    (invocations : AssocList (InvocationKey σ) (InvocationHistory σ)) :
    virtualTaskIds (invocations.map (markHistory completed current)) =
      virtualTaskIds invocations := by
  unfold virtualTaskIds
  rw [List.filterMap_map]
  congr 2
  funext entry
  exact markHistory_arguments completed current entry

theorem virtualTaskIds_append_fresh
    (invocations extra : AssocList (InvocationKey σ) (InvocationHistory σ))
    (extraNoArguments : ∀ entry, entry ∈ extra → entry.2.arguments = none) :
    virtualTaskIds (invocations ++ extra) = virtualTaskIds invocations := by
  unfold virtualTaskIds
  rw [List.filterMap_append]
  have extraNone : extra.filterMap (fun entry => entry.2.arguments) = [] :=
    List.filterMap_eq_nil_iff.mpr (fun entry isMember => extraNoArguments entry isMember)
  rw [extraNone, List.append_nil]

theorem markHistory_of_not_marked (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (entry : InvocationKey σ × InvocationHistory σ)
    (isKept : entry.1 ∈ current ∨ entry.2.outcome ≠ .live) :
    markHistory completed current entry = entry := by
  unfold markHistory
  rcases isKept with isCurrent | isNotLive
  · simp [isCurrent]
  · simp [isNotLive]

theorem markHistory_of_marked (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (entry : InvocationKey σ × InvocationHistory σ)
    (isLive : entry.2.outcome = .live) (isNotCurrent : entry.1 ∉ current) :
    markHistory completed current entry =
      (entry.1, { entry.2 with
        outcome := if completed = some entry.1 then .committed else .canceled }) := by
  unfold markHistory
  simp [isLive, isNotCurrent]

theorem hasVirtualTask_live (config : Config σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) (hasSome : history.arguments.isSome = true)
    (isLive : history.outcome = .live) : config.hasVirtualTask key history = true := by
  simp [Config.hasVirtualTask, hasSome, isLive]

theorem hasVirtualTask_committed (config : Config σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) (isCommitted : history.outcome = .committed) :
    config.hasVirtualTask key history = false := by
  simp [Config.hasVirtualTask, isCommitted]

theorem hasVirtualTask_canceled (config : Config σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) (hasSome : history.arguments.isSome = true)
    (isCanceled : history.outcome = .canceled) :
    config.hasVirtualTask key history =
      config.realTasks.any (fun other => decide (other.key = key)) := by
  simp [Config.hasVirtualTask, hasSome, isCanceled]

/-! ## The abandoned virtual tasks -/

/-- The virtual task ID that `abandonedVirtualTasks` drops for one history. -/
def abandonedIdOf (before after : Config σ) (entry : InvocationKey σ × InvocationHistory σ) :
    Option Nat :=
  match entry.2.arguments, before.ghost.findInvocation? entry.1 with
  | some arguments, some previous =>
    if decide (previous.outcome = .live) && decide (entry.2.outcome = .canceled) &&
        !after.hasVirtualTask entry.1 entry.2 then
      some arguments.virtualTaskId
    else none
  | _, _ => none

/-- The IDs that `abandonedVirtualTasks` drops, in order. -/
def abandonedIds (before after : Config σ) : List Nat :=
  sortStable (fun first second => decide (first ≤ second))
    (after.ghost.invocations.filterMap (abandonedIdOf before after))

theorem abandonedVirtualTasks_eq (before after : Config σ) :
    abandonedVirtualTasks before after =
      (abandonedIds before after).map Dex.Action.workerAbandon := rfl

theorem virtualTaskOf_eq_some_iff (config : Config σ)
    (entry : InvocationKey σ × InvocationHistory σ) (task : Dex.Task σ) :
    virtualTaskOf config entry = some task ↔
      ∃ arguments, entry.2.arguments = some arguments ∧
        config.hasVirtualTask entry.1 entry.2 = true ∧
        task = arguments.toTask entry.1 entry.2.virtualPhase := by
  constructor
  · exact virtualTaskOf_eq_some config entry task
  · rintro ⟨arguments, hasArguments, hasTask, isTask⟩
    simp [virtualTaskOf, hasArguments, hasTask, isTask]

/-- The setting of a Complete rule: the task, its live history, the base after
the commit, and the configuration after it. -/
structure CompleteSetting (program : Program σ) (config next : Config σ) (taskId : Nat)
    (task : Task σ) (history : InvocationHistory σ) (arguments : Arguments σ)
    (completedBase : Dex.Run σ) : Prop where
  invariant : Invariant program config
  isTask : config.findRealTask? taskId = some task
  isHistory : config.ghost.findInvocation? task.key = some history
  isLive : history.outcome = .live
  hasArguments : history.arguments = some arguments
  notCurrent : task.key ∉ currentKeys completedBase
  nextReal : next.realTasks = (config.removeRealTask taskId).realTasks
  nextInvocations : ∃ extra, next.ghost.invocations =
      config.ghost.invocations.map (markHistory (some task.key) (currentKeys completedBase)) ++
        extra ∧ ∀ entry, entry ∈ extra → entry.2.arguments = none

namespace CompleteSetting

variable {program : Program σ} {config next : Config σ} {taskId : Nat} {task : Task σ}
  {history : InvocationHistory σ} {arguments : Arguments σ} {completedBase : Dex.Run σ}

theorem task_entry (setting : CompleteSetting program config next taskId task history arguments
    completedBase) : (task.key, history) ∈ config.ghost.invocations :=
  mem_of_get? _ _ _ setting.isHistory

theorem history_of_key (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) (isKey : entry.1 = task.key) :
    entry.2 = history := by
  have isFound := findInvocation?_of_mem setting.invariant entry isMember
  rw [isKey, setting.isHistory] at isFound
  cases isFound
  rfl

theorem virtualTaskId_iff (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) (entryArguments : Arguments σ)
    (hasEntryArguments : entry.2.arguments = some entryArguments) :
    entryArguments.virtualTaskId = arguments.virtualTaskId ↔ entry.1 = task.key := by
  constructor
  · intro sameId
    have isSame := entry_eq_of_virtualTaskId config.ghost.invocations
      setting.invariant.virtualTaskIdsNodup entry (task.key, history) isMember setting.task_entry
      entryArguments arguments hasEntryArguments setting.hasArguments sameId
    rw [isSame]
  · intro isKey
    have isHistoryEntry := setting.history_of_key entry isMember isKey
    rw [isHistoryEntry, setting.hasArguments] at hasEntryArguments
    cases hasEntryArguments
    rfl

theorem realTasks_any (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (key : InvocationKey σ) (isOther : key ≠ task.key) :
    next.realTasks.any (fun other => decide (other.key = key)) =
      config.realTasks.any (fun other => decide (other.key = key)) := by
  have isReal : task ∈ config.realTasks := List.mem_of_find?_eq_some setting.isTask
  have isTaskId : task.base.id = taskId := by simpa using List.find?_some setting.isTask
  rw [setting.nextReal]
  have removedEq : (config.removeRealTask taskId).realTasks =
      config.realTasks.filter (fun other => !decide (other.base.id = taskId)) := by
    simp [Config.removeRealTask, Config.realTasks, List.filter_append]
  rw [removedEq]
  apply Bool.eq_iff_iff.mpr
  simp only [List.any_eq_true, List.mem_filter, decide_eq_true_eq]
  constructor
  · rintro ⟨other, ⟨isMember, _⟩, isKey⟩
    exact ⟨other, isMember, isKey⟩
  · rintro ⟨other, isMember, isKey⟩
    refine ⟨other, ⟨isMember, ?_⟩, isKey⟩
    simp only [Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not]
    intro isId
    have isSame : other = task :=
      eq_of_nodup_map (fun candidate => candidate.base.id) config.realTasks
        setting.invariant.realTaskIdsNodup isMember isReal (isId.trans isTaskId.symm)
    rw [isSame] at isKey
    exact isOther isKey.symm

/-- The marked history of an entry, in the configuration after the commit. -/
theorem mem_next (setting : CompleteSetting program config next taskId task history arguments
    completedBase) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) :
    markHistory (some task.key) (currentKeys completedBase) entry ∈ next.ghost.invocations := by
  obtain ⟨extra, isInvocations, _⟩ := setting.nextInvocations
  rw [isInvocations]
  exact List.mem_append_left _ (List.mem_map_of_mem isMember)

/-- Every entry after the commit that has `par` is a marked entry of before. -/
theorem of_mem_next (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ next.ghost.invocations) (entryArguments : Arguments σ)
    (hasEntryArguments : entry.2.arguments = some entryArguments) :
    ∃ original, original ∈ config.ghost.invocations ∧
      entry = markHistory (some task.key) (currentKeys completedBase) original := by
  obtain ⟨extra, isInvocations, extraNoArguments⟩ := setting.nextInvocations
  rw [isInvocations] at isMember
  rcases List.mem_append.mp isMember with isMarked | isExtra
  · obtain ⟨original, isOriginal, isEntry⟩ := List.mem_map.mp isMarked
    exact ⟨original, isOriginal, isEntry.symm⟩
  · rw [extraNoArguments entry isExtra] at hasEntryArguments
    cases hasEntryArguments

theorem nextIdsNodup (setting : CompleteSetting program config next taskId task history
    arguments completedBase) : (virtualTaskIds next.ghost.invocations).Nodup := by
  obtain ⟨extra, isInvocations, extraNoArguments⟩ := setting.nextInvocations
  rw [isInvocations, virtualTaskIds_append_fresh _ _ extraNoArguments,
    virtualTaskIds_map_markHistory]
  exact setting.invariant.virtualTaskIdsNodup

/-- Which IDs `abandonedVirtualTasks` drops, entry by entry. -/
theorem mem_abandonedIds (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (id : Nat) :
    id ∈ abandonedIds config next ↔
      ∃ entry entryArguments, entry ∈ config.ghost.invocations ∧
        entry.2.arguments = some entryArguments ∧ entryArguments.virtualTaskId = id ∧
        entry.2.outcome = .live ∧
        (markHistory (some task.key) (currentKeys completedBase) entry).2.outcome = .canceled ∧
        next.hasVirtualTask entry.1
          (markHistory (some task.key) (currentKeys completedBase) entry).2 = false := by
  unfold abandonedIds
  rw [mem_sortStable', List.mem_filterMap]
  constructor
  · rintro ⟨marked, isMarkedMember, isAbandoned⟩
    unfold abandonedIdOf at isAbandoned
    split at isAbandoned
    · rename_i markedArguments previous hasMarkedArguments isPrevious
      obtain ⟨original, isOriginal, isMarked⟩ :=
        setting.of_mem_next marked isMarkedMember markedArguments hasMarkedArguments
      subst isMarked
      have isFoundOriginal := findInvocation?_of_mem setting.invariant original isOriginal
      rw [markHistory_key, isFoundOriginal] at isPrevious
      cases isPrevious
      split at isAbandoned
      · rename_i isCondition
        cases isAbandoned
        simp only [Bool.and_eq_true, decide_eq_true_eq, Bool.not_eq_true'] at isCondition
        rw [markHistory_key] at isCondition
        rw [markHistory_arguments] at hasMarkedArguments
        exact ⟨original, markedArguments, isOriginal, hasMarkedArguments, rfl, isCondition.1.1,
          isCondition.1.2, isCondition.2⟩
      · cases isAbandoned
    · cases isAbandoned
  · rintro ⟨original, entryArguments, isOriginal, hasEntryArguments, isId, isLive, isCanceled,
      hasNoTask⟩
    refine ⟨markHistory (some task.key) (currentKeys completedBase) original,
      setting.mem_next original isOriginal, ?_⟩
    unfold abandonedIdOf
    rw [markHistory_arguments, hasEntryArguments, markHistory_key,
      findInvocation?_of_mem setting.invariant original isOriginal]
    dsimp only
    simp [isLive, isCanceled, hasNoTask, isId]

/-- The abandoned IDs of one entry. -/
theorem abandoned_iff (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) (entryArguments : Arguments σ)
    (hasEntryArguments : entry.2.arguments = some entryArguments) :
    entryArguments.virtualTaskId ∈ abandonedIds config next ↔
      entry.2.outcome = .live ∧
        (markHistory (some task.key) (currentKeys completedBase) entry).2.outcome = .canceled ∧
        next.hasVirtualTask entry.1
          (markHistory (some task.key) (currentKeys completedBase) entry).2 = false := by
  rw [setting.mem_abandonedIds]
  constructor
  · rintro ⟨other, otherArguments, isOther, hasOtherArguments, sameId, condition⟩
    have isSame := entry_eq_of_virtualTaskId config.ghost.invocations
      setting.invariant.virtualTaskIdsNodup other entry isOther isMember otherArguments
      entryArguments hasOtherArguments hasEntryArguments sameId
    subst isSame
    exact condition
  · intro condition
    exact ⟨entry, entryArguments, isMember, hasEntryArguments, rfl, condition⟩

/-- α keeps a virtual task after the commit exactly when it kept one before,
it is not the committed invocation's, and the commit does not abandon it. -/
theorem hasVirtualTask_next (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) (entryArguments : Arguments σ)
    (hasEntryArguments : entry.2.arguments = some entryArguments) :
    next.hasVirtualTask entry.1
        (markHistory (some task.key) (currentKeys completedBase) entry).2 = true ↔
      config.hasVirtualTask entry.1 entry.2 = true ∧
        entryArguments.virtualTaskId ≠ arguments.virtualTaskId ∧
        entryArguments.virtualTaskId ∉ abandonedIds config next := by
  rw [setting.abandoned_iff entry isMember entryArguments hasEntryArguments, ne_eq,
    setting.virtualTaskId_iff entry isMember entryArguments hasEntryArguments]
  have historyOfKey := setting.history_of_key entry isMember
  have notCurrent := setting.notCurrent
  have isLiveTask := setting.isLive
  have hasSome : entry.2.arguments.isSome = true := by rw [hasEntryArguments]; rfl
  obtain ⟨key, entryHistory⟩ := entry
  simp only at hasEntryArguments historyOfKey hasSome ⊢
  cases isOutcome : entryHistory.outcome with
  | live =>
    by_cases isCurrent : key ∈ currentKeys completedBase
    · have isOther : key ≠ task.key := fun isKey => notCurrent (isKey ▸ isCurrent)
      rw [markHistory_of_not_marked _ _ _ (Or.inl isCurrent)]
      simp only
      rw [hasVirtualTask_live _ _ _ hasSome isOutcome, hasVirtualTask_live _ _ _ hasSome isOutcome]
      simp [isOther, isOutcome]
    · rw [markHistory_of_marked _ _ _ isOutcome isCurrent]
      by_cases isKey : key = task.key
      · subst isKey
        simp only [↓reduceIte]
        rw [hasVirtualTask_committed _ _ _ rfl]
        simp
      · have isNotSome : (some task.key = some key) = False := by simp [Ne.symm isKey]
        simp only [isNotSome, ↓reduceIte]
        rw [hasVirtualTask_canceled next key { entryHistory with outcome := .canceled } hasSome rfl,
          hasVirtualTask_live _ _ _ hasSome isOutcome]
        cases next.realTasks.any (fun other => decide (other.key = key)) <;> simp [isKey]
  | committed =>
    rw [markHistory_of_not_marked _ _ _ (Or.inr (by rw [isOutcome]; simp))]
    simp only
    rw [hasVirtualTask_committed _ _ _ isOutcome, hasVirtualTask_committed _ _ _ isOutcome]
    simp
  | canceled =>
    have isOther : key ≠ task.key := by
      intro isKey
      rw [historyOfKey isKey, isLiveTask] at isOutcome
      cases isOutcome
    rw [markHistory_of_not_marked _ _ _ (Or.inr (by rw [isOutcome]; simp))]
    simp only
    rw [hasVirtualTask_canceled _ _ _ hasSome isOutcome, hasVirtualTask_canceled _ _ _ hasSome isOutcome,
      setting.realTasks_any key isOther]
    simp [isOther, isOutcome]

/-- The virtual tasks after the commit: those before, less the committed
invocation's and the abandoned ones. -/
theorem tasks_eq (setting : CompleteSetting program config next taskId task history arguments
    completedBase) :
    (config.virtualTasks.filter (fun other => !decide (other.id = arguments.virtualTaskId))).filter
        (fun other => !decide (other.id ∈ abandonedIds config next)) = next.virtualTasks := by
  apply eq_of_same_tasks
  · exact ((virtualTasks_pairwise config setting.invariant.virtualTaskIdsNodup).filter _).filter _
  · exact virtualTasks_pairwise next setting.nextIdsNodup
  · intro candidate
    simp only [List.mem_filter, mem_virtualTasks, Bool.not_eq_eq_eq_not, Bool.not_true,
      decide_eq_false_iff_not]
    constructor
    · rintro ⟨⟨⟨entry, isMember, isTask⟩, notCommitted⟩, notAbandoned⟩
      obtain ⟨entryArguments, hasEntryArguments, hasTask, isCandidate⟩ :=
        (virtualTaskOf_eq_some_iff config entry candidate).mp isTask
      refine ⟨markHistory (some task.key) (currentKeys completedBase) entry,
        setting.mem_next entry isMember, ?_⟩
      apply (virtualTaskOf_eq_some_iff next _ candidate).mpr
      refine ⟨entryArguments, by rw [markHistory_arguments]; exact hasEntryArguments, ?_, ?_⟩
      · rw [markHistory_key]
        rw [isCandidate] at notCommitted notAbandoned
        exact (setting.hasVirtualTask_next entry isMember entryArguments hasEntryArguments).mpr
          ⟨hasTask, notCommitted, notAbandoned⟩
      · rw [markHistory_key, markHistory_virtualPhase]
        exact isCandidate
    · rintro ⟨marked, isMarkedMember, isTask⟩
      obtain ⟨entryArguments, hasMarkedArguments, hasTask, isCandidate⟩ :=
        (virtualTaskOf_eq_some_iff next marked candidate).mp isTask
      obtain ⟨original, isOriginal, isMarked⟩ :=
        setting.of_mem_next marked isMarkedMember entryArguments hasMarkedArguments
      subst isMarked
      rw [markHistory_arguments] at hasMarkedArguments
      rw [markHistory_key] at hasTask isCandidate
      rw [markHistory_virtualPhase] at isCandidate
      obtain ⟨hasOld, notCommitted, notAbandoned⟩ :=
        (setting.hasVirtualTask_next original isOriginal entryArguments hasMarkedArguments).mp hasTask
      refine ⟨⟨⟨original, isOriginal, (virtualTaskOf_eq_some_iff config original candidate).mpr
        ⟨entryArguments, hasMarkedArguments, hasOld, isCandidate⟩⟩, ?_⟩, ?_⟩
      · rw [isCandidate]
        exact notCommitted
      · rw [isCandidate]
        exact notAbandoned

theorem abandonedIds_sublist (before after : Config σ) :
    (after.ghost.invocations.filterMap (abandonedIdOf before after)).Sublist
      (virtualTaskIds after.ghost.invocations) := by
  unfold virtualTaskIds
  generalize after.ghost.invocations = invocations
  induction invocations with
  | nil => exact List.Sublist.slnil
  | cons entry rest inductionHypothesis =>
    rw [List.filterMap_cons, List.filterMap_cons]
    cases isAbandoned : abandonedIdOf before after entry with
    | none =>
      cases entry.2.arguments with
      | none => exact inductionHypothesis
      | some entryArguments => exact List.Sublist.cons _ inductionHypothesis
    | some id =>
      unfold abandonedIdOf at isAbandoned
      split at isAbandoned
      · rename_i entryArguments _ hasEntryArguments _
        split at isAbandoned
        · cases isAbandoned
          rw [hasEntryArguments]
          exact List.Sublist.cons_cons _ inductionHypothesis
        · cases isAbandoned
      · cases isAbandoned

theorem abandonedIds_nodup (setting : CompleteSetting program config next taskId task history
    arguments completedBase) : (abandonedIds config next).Nodup := by
  unfold abandonedIds
  exact ((sortStable_perm _ _).nodup_iff).mpr
    (setting.nextIdsNodup.sublist (abandonedIds_sublist config next))

theorem abandoned_present (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (id : Nat) (isAbandoned : id ∈ abandonedIds config next) :
    ∃ candidate, candidate ∈
        config.virtualTasks.filter (fun other => !decide (other.id = arguments.virtualTaskId)) ∧
      candidate.id = id := by
  obtain ⟨entry, entryArguments, isMember, hasEntryArguments, isId, isLive, isCanceled, _⟩ :=
    (setting.mem_abandonedIds id).mp isAbandoned
  refine ⟨entryArguments.toTask entry.1 entry.2.virtualPhase, ?_, isId⟩
  simp only [List.mem_filter, Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not]
  refine ⟨(mem_virtualTasks config _).mpr ⟨entry, isMember, ?_⟩, ?_⟩
  · apply (virtualTaskOf_eq_some_iff config entry _).mpr
    refine ⟨entryArguments, hasEntryArguments, ?_, rfl⟩
    simp [Config.hasVirtualTask, hasEntryArguments, isLive]
  · intro sameId
    have isKey := (setting.virtualTaskId_iff entry isMember entryArguments hasEntryArguments).mp
      sameId
    unfold markHistory at isCanceled
    split at isCanceled
    · simp only [isKey, ↓reduceIte] at isCanceled
      cases isCanceled
    · rw [isLive] at isCanceled
      cases isCanceled

theorem abandoned_notCurrent (setting : CompleteSetting program config next taskId task history
    arguments completedBase) (candidate : Dex.Task σ) (isMember : candidate ∈ config.virtualTasks)
    (isAbandoned : candidate.id ∈ abandonedIds config next) :
    (candidate.step, candidate.method) ∉ currentKeys completedBase := by
  obtain ⟨entry, isEntry, isTask⟩ := (mem_virtualTasks config candidate).mp isMember
  obtain ⟨entryArguments, hasEntryArguments, _, isCandidate⟩ :=
    (virtualTaskOf_eq_some_iff config entry candidate).mp isTask
  subst isCandidate
  obtain ⟨isLive, isCanceled, _⟩ :=
    (setting.abandoned_iff entry isEntry entryArguments hasEntryArguments).mp isAbandoned
  intro isCurrent
  have isKey : (entryArguments.toTask entry.1 entry.2.virtualPhase).step = entry.1.1 := rfl
  have isMethod : (entryArguments.toTask entry.1 entry.2.virtualPhase).method = entry.1.2 := rfl
  rw [isKey, isMethod] at isCurrent
  unfold markHistory at isCanceled
  split at isCanceled
  · rename_i isMarked
    simp only [Bool.and_eq_true, decide_eq_true_eq, Bool.not_eq_eq_eq_not, Bool.not_true] at isMarked
    have : (currentKeys completedBase).contains entry.1 = true := by simpa using isCurrent
    rw [this] at isMarked
    cases isMarked.2
  · rw [isLive] at isCanceled
    cases isCanceled

end CompleteSetting

end DexExpressiveness.DexF.WPF1
