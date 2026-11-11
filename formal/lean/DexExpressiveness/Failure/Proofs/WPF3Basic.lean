import DexExpressiveness.Failure.Observation
import DexExpressiveness.Dex.RunLemmas

/-!
# WP-F3: basic lemmas

List, sorting, and ghost lookup facts that the Worker step simulation
(`Failure/Proofs/WPF3WorkerSteps.lean`) and the effect history
(`Failure/Proofs/WPF3EffectHistory.lean`) use. Everything here is proved.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Runs of one label -/

/-- Running one label is one step. -/
theorem wpf3_exec_singleton {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State) (label : Label) :
    system.exec state [label] = system.step state label := by
  unfold TransitionSystem.exec
  cases system.step state label with
  | none => rfl
  | some result =>
    obtain ⟨events, next⟩ := result
    simp [TransitionSystem.exec]

/-! ## Sorting -/

/-- Inserting commutes with a map that keeps the order. -/
theorem wpf3_insertSorted_map {α : Type} (isAtMost : α → α → Bool) (transform : α → α)
    (keepsOrder : ∀ first second, isAtMost (transform first) (transform second) =
      isAtMost first second)
    (newElement : α) (elements : List α) :
    insertSorted isAtMost (transform newElement) (elements.map transform) =
      (insertSorted isAtMost newElement elements).map transform := by
  induction elements with
  | nil => simp [insertSorted]
  | cons head rest inductionHypothesis =>
    by_cases isBefore : isAtMost newElement head = true
    · simp [insertSorted, keepsOrder, isBefore]
    · simp [insertSorted, keepsOrder, isBefore, inductionHypothesis]

/-- Sorting commutes with a map that keeps the order. -/
theorem wpf3_sortStable_map {α : Type} (isAtMost : α → α → Bool) (transform : α → α)
    (keepsOrder : ∀ first second, isAtMost (transform first) (transform second) =
      isAtMost first second)
    (elements : List α) :
    sortStable isAtMost (elements.map transform) = (sortStable isAtMost elements).map transform := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, sortStable, inductionHypothesis]
    exact wpf3_insertSorted_map isAtMost transform keepsOrder head _

/-- Inserting an element that a filter drops does not change the filtered list. -/
theorem wpf3_filter_insertSorted_of_dropped {α : Type} (isAtMost : α → α → Bool)
    (isKept : α → Bool) (newElement : α) (isDropped : isKept newElement = false)
    (elements : List α) :
    (insertSorted isAtMost newElement elements).filter isKept = elements.filter isKept := by
  induction elements with
  | nil => simp [insertSorted, isDropped]
  | cons head rest inductionHypothesis =>
    by_cases isBefore : isAtMost newElement head = true
    · simp [insertSorted, isBefore, List.filter_cons, isDropped]
    · simp only [insertSorted, isBefore, Bool.false_eq_true, ↓reduceIte, List.filter_cons]
      split <;> simp [inductionHypothesis]

/-- An element at most the head of a sorted list goes first. -/
theorem wpf3_insertSorted_of_atMost_all {α : Type} (isAtMost : α → α → Bool) (newElement : α)
    (elements : List α) (isAtMostAll : ∀ element, element ∈ elements →
      isAtMost newElement element = true) :
    insertSorted isAtMost newElement elements = newElement :: elements := by
  cases elements with
  | nil => rfl
  | cons head rest => simp [insertSorted, isAtMostAll head (List.mem_cons_self ..)]

/-- Inserting into a sorted list keeps it sorted, for a total and transitive order. -/
theorem wpf3_pairwise_insertSorted {α : Type} (isAtMost : α → α → Bool)
    (isTotal : ∀ first second, isAtMost first second = false → isAtMost second first = true)
    (isTransitive : ∀ first second third, isAtMost first second = true →
      isAtMost second third = true → isAtMost first third = true)
    (newElement : α) (elements : List α)
    (isSorted : elements.Pairwise (fun first second => isAtMost first second = true)) :
    (insertSorted isAtMost newElement elements).Pairwise
      (fun first second => isAtMost first second = true) := by
  induction elements with
  | nil => simp [insertSorted]
  | cons head rest inductionHypothesis =>
    rw [List.pairwise_cons] at isSorted
    by_cases isBefore : isAtMost newElement head = true
    · simp only [insertSorted, isBefore, ↓reduceIte]
      refine List.Pairwise.cons ?_ (List.Pairwise.cons isSorted.1 isSorted.2)
      intro element isMember
      rcases List.mem_cons.mp isMember with isHead | isInRest
      · rw [isHead]; exact isBefore
      · exact isTransitive _ _ _ isBefore (isSorted.1 element isInRest)
    · simp only [insertSorted, isBefore, Bool.false_eq_true, ↓reduceIte]
      refine List.Pairwise.cons ?_ (inductionHypothesis isSorted.2)
      intro element isMember
      rcases (mem_insertSorted isAtMost newElement element rest).mp isMember with isNew | isOld
      · rw [isNew]; exact isTotal _ _ (by simpa using isBefore)
      · exact isSorted.1 element isOld

/-- Sorting produces a sorted list, for a total and transitive order. -/
theorem wpf3_pairwise_sortStable {α : Type} (isAtMost : α → α → Bool)
    (isTotal : ∀ first second, isAtMost first second = false → isAtMost second first = true)
    (isTransitive : ∀ first second third, isAtMost first second = true →
      isAtMost second third = true → isAtMost first third = true)
    (elements : List α) :
    (sortStable isAtMost elements).Pairwise (fun first second => isAtMost first second = true) := by
  induction elements with
  | nil => simp [sortStable]
  | cons head rest inductionHypothesis =>
    exact wpf3_pairwise_insertSorted isAtMost isTotal isTransitive head _ inductionHypothesis

/-- Inserting into a sorted list commutes with a filter, for a total and transitive order. -/
theorem wpf3_filter_insertSorted {α : Type} (isAtMost : α → α → Bool)
    (isTransitive : ∀ first second third, isAtMost first second = true →
      isAtMost second third = true → isAtMost first third = true)
    (isKept : α → Bool) (newElement : α) (isNewKept : isKept newElement = true)
    (elements : List α)
    (isSorted : elements.Pairwise (fun first second => isAtMost first second = true)) :
    (insertSorted isAtMost newElement elements).filter isKept =
      insertSorted isAtMost newElement (elements.filter isKept) := by
  induction elements with
  | nil => simp [insertSorted, isNewKept]
  | cons head rest inductionHypothesis =>
    rw [List.pairwise_cons] at isSorted
    by_cases isBefore : isAtMost newElement head = true
    · simp only [insertSorted, isBefore, ↓reduceIte, List.filter_cons, isNewKept]
      by_cases isHeadKept : isKept head = true
      · simp [isHeadKept, insertSorted, isBefore]
      · simp only [isHeadKept, Bool.false_eq_true, ↓reduceIte]
        rw [wpf3_insertSorted_of_atMost_all]
        intro element isMember
        exact isTransitive _ _ _ isBefore (isSorted.1 element (List.mem_filter.mp isMember).1)
    · simp only [insertSorted, isBefore, Bool.false_eq_true, ↓reduceIte, List.filter_cons]
      by_cases isHeadKept : isKept head = true
      · simp [isHeadKept, insertSorted, isBefore, inductionHypothesis isSorted.2]
      · simp [isHeadKept, inductionHypothesis isSorted.2]

/-- Sorting commutes with a filter, for a total and transitive order. -/
theorem wpf3_sortStable_filter {α : Type} (isAtMost : α → α → Bool)
    (isTotal : ∀ first second, isAtMost first second = false → isAtMost second first = true)
    (isTransitive : ∀ first second third, isAtMost first second = true →
      isAtMost second third = true → isAtMost first third = true)
    (isKept : α → Bool) (elements : List α) :
    sortStable isAtMost (elements.filter isKept) = (sortStable isAtMost elements).filter isKept := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    by_cases isHeadKept : isKept head = true
    · simp only [List.filter_cons, isHeadKept, ↓reduceIte, sortStable, inductionHypothesis]
      exact (wpf3_filter_insertSorted isAtMost isTransitive isKept head isHeadKept _
        (wpf3_pairwise_sortStable isAtMost isTotal isTransitive rest)).symm
    · simp only [List.filter_cons, isHeadKept, Bool.false_eq_true, ↓reduceIte, sortStable,
        inductionHypothesis]
      exact (wpf3_filter_insertSorted_of_dropped isAtMost isKept head
        (by simpa using isHeadKept) _).symm

/-- `filterMap` reads its function only on the members. -/
theorem wpf3_filterMap_congr {α β : Type} (first second : α → Option β) (elements : List α)
    (sameOnMembers : ∀ element, element ∈ elements → first element = second element) :
    elements.filterMap first = elements.filterMap second := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [List.filterMap_cons, sameOnMembers head (List.mem_cons_self ..),
      inductionHypothesis (fun element isMember => sameOnMembers element
        (List.mem_cons_of_mem _ isMember))]

/-! ## Finding -/

/-- `find?` returns the only member that satisfies the predicate. -/
theorem wpf3_find?_eq_some_of_unique {α : Type} (isWanted : α → Bool) (elements : List α)
    (element : α) (isMember : element ∈ elements) (isElementWanted : isWanted element = true)
    (isUnique : ∀ other, other ∈ elements → isWanted other = true → other = element) :
    elements.find? isWanted = some element := by
  induction elements with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    by_cases isHeadWanted : isWanted head = true
    · rw [List.find?_cons, isHeadWanted]
      simp only [Option.some.injEq]
      exact isUnique head (List.mem_cons_self ..) isHeadWanted
    · rw [List.find?_cons]
      simp only [Bool.not_eq_true] at isHeadWanted
      rw [isHeadWanted]
      simp only
      rcases List.mem_cons.mp isMember with isHead | isInRest
      · rw [isHead, isHeadWanted] at isElementWanted
        cases isElementWanted
      · exact inductionHypothesis isInRest
          (fun other isOther => isUnique other (List.mem_cons_of_mem _ isOther))

/-! ## Ghost lookups -/

/-- The history of a key after changing the history of a key. -/
theorem wpf3_findInvocation?_updateInvocation (ghost : Ghost σ) (key wanted : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).findInvocation? wanted =
      if wanted = key then (ghost.findInvocation? wanted).map change
      else ghost.findInvocation? wanted := by
  unfold Ghost.updateInvocation
  cases isFound : ghost.findInvocation? key with
  | none =>
    by_cases isKey : wanted = key
    · subst isKey; simp [isFound]
    · simp [isKey]
  | some history =>
    by_cases isKey : wanted = key
    · subst isKey
      simp only [Ghost.findInvocation?] at isFound ⊢
      simp [AssocList.get?_set_self, isFound]
    · simp only [Ghost.findInvocation?, isKey, ↓reduceIte]
      exact AssocList.get?_set_other _ _ _ _ isKey

/-- Changing a history without touching its arguments keeps `par`. -/
theorem wpf3_argumentsOf_updateInvocation (ghost : Ghost σ) (key wanted : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ)
    (keepsArguments : ∀ history, (change history).arguments = history.arguments) :
    (ghost.updateInvocation key change).argumentsOf wanted = ghost.argumentsOf wanted := by
  unfold Ghost.argumentsOf
  rw [wpf3_findInvocation?_updateInvocation]
  split
  · cases ghost.findInvocation? wanted <;> simp [keepsArguments]
  · rfl

/-- The fields of a ghost other than the histories survive changing a history. -/
theorem wpf3_updateInvocation_fields (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).normalCount = ghost.normalCount ∧
    (ghost.updateInvocation key change).normalIndex = ghost.normalIndex ∧
    (ghost.updateInvocation key change).lastNormalCommitAt = ghost.lastNormalCommitAt ∧
    (ghost.updateInvocation key change).nextVirtualTaskId = ghost.nextVirtualTaskId := by
  unfold Ghost.updateInvocation
  split <;> simp

/-- The entry of an association list found by `get?` is a member. -/
theorem wpf3_mem_of_get? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ) (value : ν)
    (isFound : map.get? key = some value) : (key, value) ∈ map := by
  induction map with
  | nil => simp [AssocList.get?] at isFound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · subst isKey
      simp only [AssocList.get?, ↓reduceIte, Option.some.injEq] at isFound
      subst isFound
      exact List.mem_cons_self ..
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isFound
      exact List.mem_cons_of_mem _ (inductionHypothesis isFound)

/-- With distinct keys, a member is what `get?` finds. -/
theorem wpf3_get?_of_mem_nodup {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (keysNodup : (map.map (fun entry => entry.1)).Nodup)
    (isMember : (key, value) ∈ map) : map.get? key = some value := by
  induction map with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    simp only [List.map_cons, List.nodup_cons, List.mem_map] at keysNodup
    rcases List.mem_cons.mp isMember with isHead | isInRest
    · simp only [Prod.mk.injEq] at isHead
      obtain ⟨isKey, isValue⟩ := isHead
      subst isKey; subst isValue
      simp [AssocList.get?]
    · have isOther : headKey ≠ key := by
        intro isSame
        subst isSame
        exact keysNodup.1 ⟨(headKey, value), isInRest, rfl⟩
      simp only [AssocList.get?, isOther, ↓reduceIte]
      exact inductionHypothesis keysNodup.2 isInRest

/-! ## The handler call -/

/-- The handler call reads only the method, the Step type, the context, the
input, the snapshot, and the condition results. -/
theorem wpf3_compute_congr (flow : Dex.FlowDef σ) (first second : Dex.Task σ)
    (sameMethod : first.method = second.method) (sameStep : first.step = second.step)
    (sameContext : first.context = second.context) (sameInput : first.input = second.input)
    (sameSnapshot : first.snapshot = second.snapshot)
    (sameConditionResults : first.conditionResults = second.conditionResults) :
    first.compute flow = second.compute flow := by
  unfold Dex.Task.compute
  rw [sameMethod, sameStep, sameContext, sameInput, sameSnapshot, sameConditionResults]

/-- Under H3 every attempt context gives the failure-free flow. -/
theorem wpf3_program_eq (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (attemptContext : AttemptContext σ) :
    program attemptContext = program.failureFreeFlow :=
  ignoresAttemptContext _ _

/-- A real task that follows its history computes what the virtual task computes. -/
theorem wpf3_task_compute (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (task : Task σ)
    (arguments : Arguments σ) (sameContext : task.base.context = arguments.context)
    (sameInput : task.base.input = arguments.input)
    (sameSnapshot : task.base.snapshot = arguments.snapshot)
    (sameConditionResults : task.base.conditionResults = arguments.conditionResults) :
    task.base.compute (program task.attemptContext) =
      arguments.compute program.failureFreeFlow task.key := by
  rw [wpf3_program_eq program ignoresAttemptContext]
  exact wpf3_compute_congr _ _ _ rfl rfl sameContext sameInput sameSnapshot sameConditionResults

/-! ## Real tasks -/

/-- A task found by ID is a real task with that ID. -/
theorem wpf3_mem_of_findRealTask? (config : Config σ) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) :
    task ∈ config.realTasks ∧ task.base.id = taskId := by
  unfold Config.findRealTask? at isFound
  exact ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- A task of `Θ` found by ID is the real task with that ID. -/
theorem wpf3_findRealTask?_of_findLiveTask? (config : Config σ) (taskId : Nat) (task : Task σ)
    (isFound : config.findLiveTask? taskId = some task) :
    config.findRealTask? taskId = some task := by
  unfold Config.findLiveTask? at isFound
  unfold Config.findRealTask? Config.realTasks
  rw [List.find?_append, isFound]
  rfl

/-- A task of `Z` that is not in `Θ` is the real task with that ID. -/
theorem wpf3_findRealTask?_of_findZombie? (config : Config σ) (taskId : Nat) (task : Task σ)
    (isNotLive : config.findLiveTask? taskId = none)
    (isFound : config.findZombie? taskId = some task) :
    config.findRealTask? taskId = some task := by
  unfold Config.findLiveTask? at isNotLive
  unfold Config.findZombie? at isFound
  unfold Config.findRealTask? Config.realTasks
  rw [List.find?_append, isNotLive, isFound]
  rfl

/-- The real tasks after changing one keep their keys, when the change keeps keys. -/
theorem wpf3_realTasks_updateRealTask (config : Config σ) (taskId : Nat)
    (change : Task σ → Task σ) :
    (config.updateRealTask taskId change).realTasks =
      config.realTasks.map (fun task => if task.base.id = taskId then change task else task) := by
  simp [Config.updateRealTask, Config.realTasks]

/-- The real tasks after removing one. -/
theorem wpf3_realTasks_removeRealTask (config : Config σ) (taskId : Nat) :
    (config.removeRealTask taskId).realTasks =
      config.realTasks.filter (fun task => !decide (task.base.id = taskId)) := by
  simp [Config.removeRealTask, Config.realTasks, List.filter_append]

end DexExpressiveness.DexF
