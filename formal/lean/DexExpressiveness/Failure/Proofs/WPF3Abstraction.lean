import DexExpressiveness.Failure.Proofs.WPF3Basic

/-!
# WP-F3: how α sees one virtual task

The virtual tasks of α (`Config.virtualTasks`) under the changes that Worker
steps make: a lookup by virtual task ID, a change of one virtual task's phase
(a first occurrence), and the removal of one virtual task (the last real task
of a canceled invocation leaves). Everything here is proved.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- The virtual task that α keeps for one ghost history, given which histories
keep one. -/
def wpf3VirtualEntry (hasVirtual : InvocationKey σ → InvocationHistory σ → Bool)
    (entry : InvocationKey σ × InvocationHistory σ) : Option (Dex.Task σ) :=
  match entry.2.arguments with
  | some arguments =>
    if hasVirtual entry.1 entry.2 then some (arguments.toTask entry.1 entry.2.virtualPhase)
    else none
  | none => none

/-- The order of virtual tasks. -/
def wpf3VirtualOrder (first second : Dex.Task σ) : Bool := decide (first.id ≤ second.id)

/-- The virtual tasks are the sorted virtual entries. -/
theorem wpf3_virtualTasks_eq (config : Config σ) :
    config.virtualTasks = sortStable wpf3VirtualOrder
      (config.ghost.invocations.filterMap (wpf3VirtualEntry config.hasVirtualTask)) := rfl

/-- The virtual task IDs of all histories with arguments. -/
def wpf3VirtualTaskIds (invocations : AssocList (InvocationKey σ) (InvocationHistory σ)) :
    List Nat :=
  (invocations.filterMap (fun entry => entry.2.arguments)).map
    (fun arguments => arguments.virtualTaskId)

theorem wpf3_virtualOrder_total (first second : Dex.Task σ)
    (isNotAtMost : wpf3VirtualOrder first second = false) :
    wpf3VirtualOrder second first = true := by
  unfold wpf3VirtualOrder at *
  simp only [decide_eq_false_iff_not, Nat.not_le] at isNotAtMost
  simp only [decide_eq_true_eq]
  omega

theorem wpf3_virtualOrder_transitive (first second third : Dex.Task σ)
    (firstAtMost : wpf3VirtualOrder first second = true)
    (secondAtMost : wpf3VirtualOrder second third = true) :
    wpf3VirtualOrder first third = true := by
  unfold wpf3VirtualOrder at *
  simp only [decide_eq_true_eq] at *
  omega

/-- Two histories with the same virtual task ID are the same entry. -/
theorem wpf3_entry_eq_of_virtualTaskId
    (invocations : AssocList (InvocationKey σ) (InvocationHistory σ))
    (idsNodup : (wpf3VirtualTaskIds invocations).Nodup)
    (first second : InvocationKey σ × InvocationHistory σ)
    (isFirstMember : first ∈ invocations) (isSecondMember : second ∈ invocations)
    (firstArguments secondArguments : Arguments σ)
    (hasFirstArguments : first.2.arguments = some firstArguments)
    (hasSecondArguments : second.2.arguments = some secondArguments)
    (sameId : firstArguments.virtualTaskId = secondArguments.virtualTaskId) :
    first = second := by
  induction invocations with
  | nil => cases isFirstMember
  | cons head rest inductionHypothesis =>
    have idMember : ∀ entry arguments, entry ∈ rest → entry.2.arguments = some arguments →
        arguments.virtualTaskId ∈ wpf3VirtualTaskIds rest := by
      intro entry arguments isMember hasArguments
      unfold wpf3VirtualTaskIds
      exact List.mem_map.mpr ⟨arguments, List.mem_filterMap.mpr ⟨entry, isMember, hasArguments⟩, rfl⟩
    cases headArguments : head.2.arguments with
    | none =>
      have restNodup : (wpf3VirtualTaskIds rest).Nodup := by
        simpa [wpf3VirtualTaskIds, List.filterMap_cons, headArguments] using idsNodup
      rcases List.mem_cons.mp isFirstMember with isFirstHead | isFirstRest
      · subst isFirstHead; rw [headArguments] at hasFirstArguments; cases hasFirstArguments
      rcases List.mem_cons.mp isSecondMember with isSecondHead | isSecondRest
      · subst isSecondHead; rw [headArguments] at hasSecondArguments; cases hasSecondArguments
      exact inductionHypothesis restNodup isFirstRest isSecondRest
    | some arguments =>
      have consNodup : (arguments.virtualTaskId :: wpf3VirtualTaskIds rest).Nodup := by
        simpa [wpf3VirtualTaskIds, List.filterMap_cons, headArguments] using idsNodup
      rw [List.nodup_cons] at consNodup
      rcases List.mem_cons.mp isFirstMember with isFirstHead | isFirstRest <;>
        rcases List.mem_cons.mp isSecondMember with isSecondHead | isSecondRest
      · rw [isFirstHead, isSecondHead]
      · subst isFirstHead
        rw [headArguments] at hasFirstArguments
        cases hasFirstArguments
        exact absurd (sameId ▸ idMember second secondArguments isSecondRest hasSecondArguments)
          consNodup.1
      · subst isSecondHead
        rw [headArguments] at hasSecondArguments
        cases hasSecondArguments
        exact absurd (sameId.symm ▸ idMember first firstArguments isFirstRest hasFirstArguments)
          consNodup.1
      · exact inductionHypothesis consNodup.2 isFirstRest isSecondRest

/-- A virtual entry has the virtual task ID of its arguments. -/
theorem wpf3_virtualEntry_some (hasVirtual : InvocationKey σ → InvocationHistory σ → Bool)
    (entry : InvocationKey σ × InvocationHistory σ) (task : Dex.Task σ)
    (isSome : wpf3VirtualEntry hasVirtual entry = some task) :
    ∃ arguments, entry.2.arguments = some arguments ∧ hasVirtual entry.1 entry.2 = true ∧
      task = arguments.toTask entry.1 entry.2.virtualPhase := by
  unfold wpf3VirtualEntry at isSome
  split at isSome
  · rename_i arguments hasArguments
    split at isSome
    · rename_i isVirtual
      cases isSome
      exact ⟨arguments, hasArguments, isVirtual, rfl⟩
    · cases isSome
  · cases isSome

/-- The virtual task of a history that keeps one. -/
theorem wpf3_virtualEntry_of_hasVirtual (hasVirtual : InvocationKey σ → InvocationHistory σ → Bool)
    (key : InvocationKey σ) (history : InvocationHistory σ) (arguments : Arguments σ)
    (hasArguments : history.arguments = some arguments)
    (isVirtual : hasVirtual key history = true) :
    wpf3VirtualEntry hasVirtual (key, history) =
      some (arguments.toTask key history.virtualPhase) := by
  simp [wpf3VirtualEntry, hasArguments, isVirtual]

/-- Whether α keeps a virtual task reads the real tasks only through their keys. -/
theorem wpf3_hasVirtualTask_congr (config other : Config σ)
    (sameKeys : ∀ key, other.realTasks.any (fun task => decide (task.key = key)) =
      config.realTasks.any (fun task => decide (task.key = key))) :
    other.hasVirtualTask = config.hasVirtualTask := by
  funext key history
  unfold Config.hasVirtualTask
  rw [sameKeys key]

/-- `Run.abstract` reads the ghost only through `n`, `ζ`, and `par`. -/
theorem wpf3_runAbstract_congr (ghost other : Ghost σ) (run : Run σ)
    (sameCount : other.normalCount = ghost.normalCount)
    (sameIndex : other.normalIndex = ghost.normalIndex)
    (sameArguments : ∀ key, other.argumentsOf key = ghost.argumentsOf key) :
    Run.abstract other run = Run.abstract ghost run := by
  unfold Run.abstract Run.abstractInvocation Ghost.normalIndexOf
  simp only [sameCount, sameIndex, sameArguments]

/-- α reads the real tasks only through their keys. -/
theorem wpf3_abstractConfig_congr (config other : Config σ)
    (sameClock : other.clock = config.clock) (sameRun : other.run = config.run)
    (sameGhost : other.ghost = config.ghost)
    (sameKeys : ∀ key, other.realTasks.any (fun task => decide (task.key = key)) =
      config.realTasks.any (fun task => decide (task.key = key))) :
    abstractConfig other = abstractConfig config := by
  have sameVirtual := wpf3_hasVirtualTask_congr config other sameKeys
  unfold abstractConfig
  rw [wpf3_virtualTasks_eq, wpf3_virtualTasks_eq, sameVirtual, sameClock, sameRun, sameGhost]

/-- Changing a task with a key-preserving change keeps the keys of the real tasks. -/
theorem wpf3_sameKeys_updateRealTask (config : Config σ) (taskId : Nat)
    (change : Task σ → Task σ) (keepsKey : ∀ task, (change task).key = task.key)
    (key : InvocationKey σ) :
    (config.updateRealTask taskId change).realTasks.any (fun task => decide (task.key = key)) =
      config.realTasks.any (fun task => decide (task.key = key)) := by
  rw [wpf3_realTasks_updateRealTask, List.any_map]
  congr 1
  funext task
  simp only [Function.comp_apply]
  split <;> simp [keepsKey]

/-- With distinct keys, setting a present key changes exactly its entry. -/
theorem wpf3_set_eq_map {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value newValue : ν) (keysNodup : (map.map (fun entry => entry.1)).Nodup)
    (isFound : map.get? key = some value) :
    map.set key newValue =
      map.map (fun entry => if entry.1 = key then (key, newValue) else entry) := by
  induction map with
  | nil => simp [AssocList.get?] at isFound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    simp only [List.map_cons, List.nodup_cons, List.mem_map] at keysNodup
    by_cases isKey : headKey = key
    · subst isKey
      simp only [AssocList.set, ↓reduceIte, List.map_cons, List.cons.injEq, true_and]
      have sameRest : rest.map (fun entry => if entry.1 = headKey then (headKey, newValue)
          else entry) = rest.map id := by
        apply List.map_congr_left
        intro entry isMember
        have isOther : entry.1 ≠ headKey := fun isSame => keysNodup.1 ⟨entry, isMember, isSame⟩
        simp [isOther]
      rw [sameRest, List.map_id]
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isFound
      simp only [AssocList.set, isKey, ↓reduceIte, List.map_cons, List.cons.injEq, true_and]
      exact inductionHypothesis keysNodup.2 isFound

/-- The virtual task with a given ID, from a history that keeps one. -/
theorem wpf3_findTask?_virtualTasks (config : Config σ)
    (idsNodup : (wpf3VirtualTaskIds config.ghost.invocations).Nodup)
    (key : InvocationKey σ) (history : InvocationHistory σ) (arguments : Arguments σ)
    (isMember : (key, history) ∈ config.ghost.invocations)
    (hasArguments : history.arguments = some arguments)
    (isVirtual : config.hasVirtualTask key history = true) :
    (abstractConfig config).findTask? arguments.virtualTaskId =
      some (arguments.toTask key history.virtualPhase) := by
  unfold Dex.Config.findTask? abstractConfig
  simp only
  rw [wpf3_virtualTasks_eq]
  apply wpf3_find?_eq_some_of_unique
  · rw [mem_sortStable]
    exact List.mem_filterMap.mpr ⟨(key, history), isMember,
      wpf3_virtualEntry_of_hasVirtual _ key history arguments hasArguments isVirtual⟩
  · simp [Arguments.toTask]
  · intro other isOther isWanted
    rw [mem_sortStable] at isOther
    obtain ⟨entry, isEntryMember, isEntry⟩ := List.mem_filterMap.mp isOther
    obtain ⟨entryArguments, hasEntryArguments, _, isOtherTask⟩ :=
      wpf3_virtualEntry_some _ entry other isEntry
    subst isOtherTask
    have isSameId : entryArguments.virtualTaskId = arguments.virtualTaskId :=
      of_decide_eq_true isWanted
    have isSame := wpf3_entry_eq_of_virtualTaskId _ idsNodup entry (key, history)
      isEntryMember isMember entryArguments arguments hasEntryArguments hasArguments isSameId
    subst isSame
    rw [hasArguments] at hasEntryArguments
    cases hasEntryArguments
    rfl

/-- A first-occurrence Worker step changes the phase of exactly one virtual task. -/
theorem wpf3_abstractConfig_updateInvocation (config other : Config σ)
    (idsNodup : (wpf3VirtualTaskIds config.ghost.invocations).Nodup)
    (keysNodup : (config.ghost.invocations.map (fun entry => entry.1)).Nodup)
    (key : InvocationKey σ) (history : InvocationHistory σ) (arguments : Arguments σ)
    (isFound : config.ghost.findInvocation? key = some history)
    (hasArguments : history.arguments = some arguments)
    (isVirtual : config.hasVirtualTask key history = true)
    (change : InvocationHistory σ → InvocationHistory σ)
    (keepsArguments : ∀ history, (change history).arguments = history.arguments)
    (keepsOutcome : ∀ history, (change history).outcome = history.outcome)
    (sameClock : other.clock = config.clock) (sameRun : other.run = config.run)
    (isGhost : other.ghost = config.ghost.updateInvocation key change)
    (sameKeys : ∀ key, other.realTasks.any (fun task => decide (task.key = key)) =
      config.realTasks.any (fun task => decide (task.key = key))) :
    abstractConfig other =
      (abstractConfig config).updateTask arguments.virtualTaskId
        (fun task => { task with phase := (change history).virtualPhase }) := by
  have sameVirtual := wpf3_hasVirtualTask_congr config other sameKeys
  obtain ⟨sameCount, sameIndex, sameLast, sameNext⟩ :=
    wpf3_updateInvocation_fields config.ghost key change
  have sameArguments := fun wanted =>
    wpf3_argumentsOf_updateInvocation config.ghost key wanted change keepsArguments
  have isMember := wpf3_mem_of_get? _ _ _ isFound
  have hasVirtualChanged : config.hasVirtualTask key (change history) = true := by
    unfold Config.hasVirtualTask at isVirtual ⊢
    rw [keepsArguments, keepsOutcome]
    exact isVirtual
  unfold abstractConfig Dex.Config.updateTask
  simp only [Dex.Config.mk.injEq]
  refine ⟨sameClock, ?_, ?_, ?_, ?_⟩
  · rw [isGhost, sameLast]
  · rw [sameRun, isGhost]
    cases config.run with
    | none => rfl
    | some run =>
      simp only [Option.map_some, Option.some.injEq]
      exact wpf3_runAbstract_congr _ _ run sameCount sameIndex sameArguments
  · rw [wpf3_virtualTasks_eq, wpf3_virtualTasks_eq, sameVirtual, isGhost]
    unfold Ghost.updateInvocation
    rw [isFound]
    simp only
    rw [wpf3_set_eq_map _ key history (change history) keysNodup isFound, List.filterMap_map]
    refine Eq.trans ?_ (wpf3_sortStable_map wpf3VirtualOrder
      (fun task => if task.id = arguments.virtualTaskId then
        { task with phase := (change history).virtualPhase } else task)
      (by intro first second; unfold wpf3VirtualOrder; split <;> split <;> rfl) _)
    congr 1
    rw [List.map_filterMap]
    apply wpf3_filterMap_congr
    intro entry isEntryMember
    simp only [Function.comp_apply]
    by_cases isKey : entry.1 = key
    · have isEntry : entry = (key, history) := by
        obtain ⟨entryKey, entryHistory⟩ := entry
        simp only at isKey
        subst isKey
        have isFoundEntry := wpf3_get?_of_mem_nodup _ _ _ keysNodup isEntryMember
        unfold Ghost.findInvocation? at isFound
        rw [isFound] at isFoundEntry
        cases isFoundEntry
        rfl
      subst isEntry
      simp only [↓reduceIte]
      rw [wpf3_virtualEntry_of_hasVirtual _ key history arguments hasArguments isVirtual,
        wpf3_virtualEntry_of_hasVirtual _ key (change history) arguments
          (by rw [keepsArguments]; exact hasArguments) hasVirtualChanged]
      simp [Arguments.toTask]
    · simp only [isKey, ↓reduceIte]
      cases isEntry : wpf3VirtualEntry config.hasVirtualTask entry with
      | none => rfl
      | some task =>
        obtain ⟨entryArguments, hasEntryArguments, _, isTask⟩ :=
          wpf3_virtualEntry_some _ entry task isEntry
        have isOtherId : task.id ≠ arguments.virtualTaskId := by
          intro isSameId
          rw [isTask] at isSameId
          have isSame := wpf3_entry_eq_of_virtualTaskId _ idsNodup entry (key, history)
            isEntryMember isMember entryArguments arguments hasEntryArguments hasArguments isSameId
          rw [isSame] at isKey
          exact isKey rfl
        simp [isOtherId]
  · rw [isGhost, sameNext]

/-- The last real task of a canceled invocation leaving removes exactly its virtual task. -/
theorem wpf3_abstractConfig_removeLast (config other : Config σ)
    (idsNodup : (wpf3VirtualTaskIds config.ghost.invocations).Nodup)
    (keysNodup : (config.ghost.invocations.map (fun entry => entry.1)).Nodup)
    (key : InvocationKey σ) (history : InvocationHistory σ) (arguments : Arguments σ)
    (isFound : config.ghost.findInvocation? key = some history)
    (hasArguments : history.arguments = some arguments)
    (isVirtual : config.hasVirtualTask key history = true)
    (isNotVirtual : other.hasVirtualTask key history = false)
    (sameOthers : ∀ otherKey, otherKey ≠ key →
      other.hasVirtualTask otherKey = config.hasVirtualTask otherKey)
    (sameClock : other.clock = config.clock) (sameRun : other.run = config.run)
    (sameGhost : other.ghost = config.ghost) :
    abstractConfig other = (abstractConfig config).removeTask arguments.virtualTaskId := by
  have isMember := wpf3_mem_of_get? _ _ _ isFound
  unfold abstractConfig Dex.Config.removeTask
  simp only [Dex.Config.mk.injEq]
  refine ⟨sameClock, by rw [sameGhost], by rw [sameRun, sameGhost], ?_, by rw [sameGhost]⟩
  rw [wpf3_virtualTasks_eq, wpf3_virtualTasks_eq, sameGhost]
  refine Eq.trans ?_ (wpf3_sortStable_filter wpf3VirtualOrder wpf3_virtualOrder_total
    wpf3_virtualOrder_transitive (fun task => !decide (task.id = arguments.virtualTaskId)) _)
  congr 1
  rw [List.filter_filterMap]
  apply wpf3_filterMap_congr
  intro entry isEntryMember
  by_cases isKey : entry.1 = key
  · have isEntry : entry = (key, history) := by
      obtain ⟨entryKey, entryHistory⟩ := entry
      simp only at isKey
      subst isKey
      have isFoundEntry := wpf3_get?_of_mem_nodup _ _ _ keysNodup isEntryMember
      unfold Ghost.findInvocation? at isFound
      rw [isFound] at isFoundEntry
      cases isFoundEntry
      rfl
    subst isEntry
    rw [wpf3_virtualEntry_of_hasVirtual _ key history arguments hasArguments isVirtual]
    simp [wpf3VirtualEntry, hasArguments, isNotVirtual, Option.filter, Arguments.toTask]
  · have sameEntry : wpf3VirtualEntry other.hasVirtualTask entry =
        wpf3VirtualEntry config.hasVirtualTask entry := by
      unfold wpf3VirtualEntry
      rw [sameOthers entry.1 isKey]
    rw [sameEntry]
    cases isEntry : wpf3VirtualEntry config.hasVirtualTask entry with
    | none => rfl
    | some task =>
      obtain ⟨entryArguments, hasEntryArguments, _, isTask⟩ :=
        wpf3_virtualEntry_some _ entry task isEntry
      have isOtherId : task.id ≠ arguments.virtualTaskId := by
        intro isSameId
        rw [isTask] at isSameId
        have isSame := wpf3_entry_eq_of_virtualTaskId _ idsNodup entry (key, history)
          isEntryMember isMember entryArguments arguments hasEntryArguments hasArguments isSameId
        rw [isSame] at isKey
        exact isKey rfl
      simp [Option.filter, isOtherId]

end DexExpressiveness.DexF
