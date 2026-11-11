import DexExpressiveness.Failure.Proofs.WPF4Simple

/-!
# WP-F4: the clauses that every commit keeps the same way

`commitRun` stamps, takes a run `seq`, counts a normal command, reconciles the
attempt states, and reconciles the ghost histories. The lemmas here prove the
clauses of `Invariant` that only this bookkeeping touches. The rule files
prove the clauses about Step records.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- The ghost after the counting part of a commit. -/
def countedGhost (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat) : Ghost σ :=
  if isNormal then
    { ghost with
      normalCount := ghost.normalCount + 1
      normalIndex := ghost.normalIndex.set seq (ghost.normalCount + 1)
      lastNormalCommitAt := some clock }
  else ghost

/-- The run a commit leaves before its attempt reconcile. -/
def bumpedRun (run : Run σ) : Run σ :=
  { run with base := { run.base with seq := run.base.seq + 1 } }

/-- `commitRun` unfolded. -/
theorem commitRun_eq (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) :
    config.commitRun run isNormal completed inputEvents change =
      (inputEvents ++ Config.closeEvents run.base (change (bumpedRun run)).reconcileAttempts.base,
        { config with
          run := some (change (bumpedRun run)).reconcileAttempts
          lastCommitAt := some config.clock
          ghost := (countedGhost config.ghost isNormal (run.base.seq + 1) config.clock).reconcile
            completed (change (bumpedRun run)).reconcileAttempts.base }) := by
  unfold Config.commitRun countedGhost bumpedRun
  rfl

/-- Counting keeps the histories. -/
theorem countedGhost_invocations (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat) :
    (countedGhost ghost isNormal seq clock).invocations = ghost.invocations := by
  unfold countedGhost
  split <;> rfl

/-- Counting keeps lookup. -/
theorem countedGhost_find (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat)
    (key : InvocationKey σ) :
    (countedGhost ghost isNormal seq clock).findInvocation? key = ghost.findInvocation? key := by
  unfold Ghost.findInvocation?
  rw [countedGhost_invocations]

/-- Counting keeps the next virtual task ID. -/
theorem countedGhost_nextVirtual (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat) :
    (countedGhost ghost isNormal seq clock).nextVirtualTaskId = ghost.nextVirtualTaskId := by
  unfold countedGhost
  split <;> rfl

/-- The `ζ` part of a commit: I5 for the counted ghost. -/
theorem countedGhost_normalIndex (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat)
    (values : ghost.normalIndex.map (fun entry => entry.2) = List.range' 1 ghost.normalCount)
    (increase : (ghost.normalIndex.map (fun entry => entry.1)).Pairwise (· < ·))
    (taken : ∀ entry, entry ∈ ghost.normalIndex → entry.1 ≤ seq) :
    (countedGhost ghost isNormal (seq + 1) clock).normalIndex.map (fun entry => entry.2) =
      List.range' 1 (countedGhost ghost isNormal (seq + 1) clock).normalCount ∧
    ((countedGhost ghost isNormal (seq + 1) clock).normalIndex.map
      (fun entry => entry.1)).Pairwise (· < ·) ∧
    (∀ entry, entry ∈ (countedGhost ghost isNormal (seq + 1) clock).normalIndex →
      entry.1 ≤ seq + 1) ∧
    (∀ key, (ghost.normalIndex.get? key).isSome = true →
      ((countedGhost ghost isNormal (seq + 1) clock).normalIndex.get? key).isSome = true) ∧
    (isNormal = true →
      ((countedGhost ghost isNormal (seq + 1) clock).normalIndex.get? (seq + 1)).isSome = true) := by
  cases isNormal with
  | false =>
    refine ⟨values, increase, fun entry isMember => Nat.le_succ_of_le (taken entry isMember),
      fun _ isSome => isSome, fun isTrue => by cases isTrue⟩
  | true =>
    have isAbsent : AssocList.contains ghost.normalIndex (seq + 1) = false := by
      cases isContained : AssocList.contains ghost.normalIndex (seq + 1) with
      | false => rfl
      | true =>
        obtain ⟨value, isMember⟩ := AssocList.mem_of_contains _ _ isContained
        have := taken _ isMember
        omega
    have isAppend := set_of_contains_false ghost.normalIndex (seq + 1) (ghost.normalCount + 1)
      isAbsent
    simp only [countedGhost, ↓reduceIte]
    refine ⟨?_, ?_, ?_, ?_, ?_⟩
    · rw [isAppend, List.map_append, values, List.range'_concat]
      simp [Nat.add_comm]
    · rw [isAppend, List.map_append, List.pairwise_append]
      refine ⟨increase, List.pairwise_singleton _ _, ?_⟩
      intro first isFirst second isSecond
      obtain ⟨entry, isMember, isKey⟩ := List.mem_map.mp isFirst
      simp only [List.map_cons, List.map_nil, List.mem_singleton] at isSecond
      have := taken entry isMember
      omega
    · intro entry isMember
      rw [isAppend] at isMember
      rcases List.mem_append.mp isMember with isOld | isNew
      · exact Nat.le_succ_of_le (taken entry isOld)
      · simp only [List.mem_singleton] at isNew
        rw [isNew]; exact Nat.le_refl _
    · intro key isSome
      by_cases isKey : key = seq + 1
      · subst isKey
        rw [AssocList.get?_set_self]
        rfl
      · rw [AssocList.get?_set_other _ _ _ _ isKey]
        exact isSome
    · intro _
      rw [AssocList.get?_set_self]
      rfl

/-- The last normal stamp after a commit is at most its stamp. -/
theorem countedGhost_lastNormal (ghost : Ghost σ) (isNormal : Bool) (seq : Nat)
    (last : Option Nat) (clock : Nat) (lastNormal : IsStampAtMost ghost.lastNormalCommitAt last)
    (canCommit : ∀ lastStamp, last = some lastStamp → lastStamp < clock) :
    IsStampAtMost (countedGhost ghost isNormal seq clock).lastNormalCommitAt (some clock) := by
  unfold countedGhost
  cases isNormal with
  | true => simp [IsStampAtMost]
  | false =>
    simp only [Bool.false_eq_true, ↓reduceIte]
    cases hLast : last with
    | none =>
      rw [hLast] at lastNormal
      cases hStamp : ghost.lastNormalCommitAt with
      | none => trivial
      | some stamp => rw [hStamp] at lastNormal; exact False.elim lastNormal
    | some lastStamp =>
      rw [hLast] at lastNormal
      have isBelow := canCommit lastStamp hLast
      cases hStamp : ghost.lastNormalCommitAt with
      | none => trivial
      | some stamp =>
        rw [hStamp] at lastNormal
        simp only [IsStampAtMost] at lastNormal ⊢
        omega

/-- A commit's stamp is above the last stamp. -/
theorem lt_of_canCommit {config : Config σ} (canCommit : config.canCommit = true) :
    ∀ lastStamp, config.lastCommitAt = some lastStamp → lastStamp < config.clock := by
  intro lastStamp isLast
  unfold Config.canCommit at canCommit
  rw [isLast] at canCommit
  simpa using canCommit

/-- A marked history is consistent when the original was, and a history the
commit completed has returned. -/
theorem historyIsConsistent_mark {flow : Dex.FlowDef σ} {key : InvocationKey σ}
    {history : InvocationHistory σ} (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (consistent : HistoryIsConsistent flow key history)
    (completedReturned : completed = some key → history.progress.isReturned = true) :
    HistoryIsConsistent flow key (markHistory completed current key history) := by
  obtain ⟨_, sameEmitted, sameProgress, sameArguments⟩ :=
    markHistory_fields completed current key history
  have committedOf : (markHistory completed current key history).outcome = .committed →
      history.progress.isReturned = true := by
    intro isCommitted
    rcases markHistory_committed _ _ _ _ isCommitted with wasCommitted | isCompleted
    · unfold HistoryIsConsistent at consistent
      revert consistent
      cases history.arguments with
      | none => rintro ⟨_, _, notCommitted⟩; exact absurd wasCommitted notCommitted
      | some arguments => rintro ⟨_, isReturned⟩; exact isReturned wasCommitted
    · exact completedReturned isCompleted
  unfold HistoryIsConsistent at consistent ⊢
  rw [sameArguments, sameEmitted, sameProgress]
  revert consistent
  cases history.arguments with
  | none =>
    rintro ⟨isNotRun, isZero, _⟩
    refine ⟨isNotRun, isZero, fun isCommitted => ?_⟩
    have isReturned := committedOf isCommitted
    cases hProgress : history.progress with
    | notRun => rw [hProgress] at isReturned; cases isReturned
    | ran => rw [hProgress] at isNotRun; cases isNotRun
    | returned => rw [hProgress] at isNotRun; cases isNotRun
  | some arguments =>
    rintro ⟨progressMatches, _⟩
    exact ⟨progressMatches, fun isCommitted => committedOf isCommitted⟩

/-- A fresh history is consistent. -/
theorem historyIsConsistent_fresh (flow : Dex.FlowDef σ) (key : InvocationKey σ) (count : Nat) :
    HistoryIsConsistent flow key (InvocationHistory.fresh count) := by
  unfold HistoryIsConsistent InvocationHistory.fresh
  exact ⟨rfl, rfl, by intro isCommitted; cases isCommitted⟩

/-- The ghost clauses of `Invariant` after a commit's reconcile. -/
theorem reconciled_ghost_clauses {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (isNormal : Bool) (seq clock : Nat)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ)
    (completedReturned : ∀ key history, completed = some key →
      config.ghost.findInvocation? key = some history → history.progress.isReturned = true) :
    let ghost := (countedGhost config.ghost isNormal seq clock).reconcile completed base
    (ghost.invocations.map (fun entry => entry.1)).Nodup ∧
    ((ghost.invocations.filterMap (fun entry => entry.2.arguments)).map
      (fun arguments => arguments.virtualTaskId)).Nodup ∧
    (∀ entry arguments, entry ∈ ghost.invocations → entry.2.arguments = some arguments →
      arguments.virtualTaskId < ghost.nextVirtualTaskId) ∧
    (∀ entry, entry ∈ ghost.invocations →
      HistoryIsConsistent program.failureFreeFlow entry.1 entry.2) := by
  intro ghost
  have keysNodup := invariant.ghostKeysNodup
  refine ⟨?_, ?_, ?_, ?_⟩
  · apply nodup_reconcile
    rw [countedGhost_invocations]
    exact keysNodup
  · show ((((countedGhost config.ghost isNormal seq clock).reconcile completed base).invocations.filterMap
      (fun entry => entry.2.arguments)).map (fun arguments => arguments.virtualTaskId)).Nodup
    rw [filterMap_reconcile, countedGhost_invocations]
    exact invariant.virtualTaskIdsNodup
  · intro entry arguments isMember hasArguments
    show arguments.virtualTaskId <
      (countedGhost config.ghost isNormal seq clock).nextVirtualTaskId
    rw [countedGhost_nextVirtual]
    rcases mem_reconcile _ _ _ entry isMember with ⟨original, isOriginal, _, isEntry⟩ |
      ⟨_, isFresh, _⟩
    · rw [countedGhost_invocations] at isOriginal
      rw [isEntry, (markHistory_fields _ _ _ _).2.2.2] at hasArguments
      exact invariant.virtualTaskIdsBelow original arguments isOriginal hasArguments
    · rw [isFresh] at hasArguments
      cases hasArguments
  · intro entry isMember
    rcases mem_reconcile _ _ _ entry isMember with ⟨original, isOriginal, isKey, isEntry⟩ |
      ⟨_, isFresh, _⟩
    · rw [countedGhost_invocations] at isOriginal
      rw [isEntry, isKey]
      refine historyIsConsistent_mark _ _ (invariant.historiesConsistent original isOriginal) ?_
      intro isCompleted
      exact completedReturned original.1 original.2 isCompleted
        (get?_of_mem_nodup _ original isOriginal keysNodup)
    · rw [isFresh]
      exact historyIsConsistent_fresh _ _ _

/-- Lookup in the reconciled ghost of a key that had a history. -/
theorem reconciled_find_of_some (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) (isFound : ghost.findInvocation? key = some history) :
    ((countedGhost ghost isNormal seq clock).reconcile completed base).findInvocation? key =
      some (markHistory completed (currentKeys base) key history) := by
  apply findInvocation?_reconcile_of_some
  rw [countedGhost_find]
  exact isFound

/-- Lookup in the reconciled ghost of a key without history. -/
theorem reconciled_find_of_none (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ) (key : InvocationKey σ)
    (isAbsent : ghost.findInvocation? key = none) :
    ((countedGhost ghost isNormal seq clock).reconcile completed base).findInvocation? key =
      if key ∈ currentKeys base then
        some (InvocationHistory.fresh (countedGhost ghost isNormal seq clock).normalCount)
      else none :=
  findInvocation?_reconcile_of_none _ _ _ _ (by rw [countedGhost_find]; exact isAbsent)

/-- A real task keeps following its history through the reconcile. -/
theorem follows_reconciled {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (isNormal : Bool) (seq clock : Nat)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ) {task : Task σ}
    (isTask : task ∈ config.realTasks) :
    ∃ history, ((countedGhost config.ghost isNormal seq clock).reconcile completed
      base).findInvocation? task.key = some history ∧
      TaskFollowsHistory program.failureFreeFlow task history := by
  obtain ⟨history, isHistory, follows⟩ := invariant.realTasksFollowHistory task isTask
  refine ⟨_, reconciled_find_of_some _ _ _ _ _ _ _ _ isHistory, ?_⟩
  obtain ⟨_, sameEmitted, sameProgress, sameArguments⟩ :=
    markHistory_fields completed (currentKeys base) task.key history
  exact taskFollowsHistory_of_progress follows sameArguments (Nat.le_of_eq sameEmitted.symm)
    (fun isRun => by rw [sameProgress]; exact isRun)
    (fun isReturned => by rw [sameProgress]; exact isReturned)

/-- After the reconcile every live history is the current invocation of a
Step. -/
theorem reconciled_liveIsCurrent (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ) (key : InvocationKey σ)
    (history : InvocationHistory σ)
    (isFound : ((countedGhost ghost isNormal seq clock).reconcile completed
      base).findInvocation? key = some history)
    (isLive : history.outcome = .live) :
    ∃ record state, record ∈ base.steps ∧ record.id = key.1 ∧
      record.invocation = some { method := key.2, state := state } := by
  have isCurrent := live_current_reconcile _ _ _ _ _ isFound isLive
  obtain ⟨record, invocation, isMember, hasInvocation, isId, isMethod⟩ :=
    (mem_currentKeys base key).mp isCurrent
  refine ⟨record, invocation.state, isMember, isId, ?_⟩
  rw [hasInvocation, ← isMethod]

/-- `historiesHaveSteps` after the reconcile. -/
theorem reconciled_historiesHaveSteps (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ)
    (oldHistories : ∀ entry, entry ∈ ghost.invocations →
      ∃ record, record ∈ base.steps ∧ record.id = entry.1.1 ∧
        (entry.1.2 = .execute →
          record.phase = .execute ∨ record.phase = .completed ∨ record.phase = .canceled))
    (invocationMatchesPhase : ∀ record, record ∈ base.steps →
      match record.invocation with
      | some invocation =>
        record.phase = (match invocation.method with
          | .waitFor => Dex.Phase.waitFor
          | .execute => Dex.Phase.execute)
      | none => record.phase ≠ .waitFor ∧ record.phase ≠ .execute) :
    ∀ entry, entry ∈ ((countedGhost ghost isNormal seq clock).reconcile completed base).invocations →
      ∃ record, record ∈ base.steps ∧ record.id = entry.1.1 ∧
        (entry.1.2 = .execute →
          record.phase = .execute ∨ record.phase = .completed ∨ record.phase = .canceled) := by
  intro entry isMember
  rcases mem_reconcile _ _ _ entry isMember with ⟨original, isOriginal, isKey, _⟩ |
    ⟨isCurrent, _, _⟩
  · rw [countedGhost_invocations] at isOriginal
    rw [isKey]
    exact oldHistories original isOriginal
  · obtain ⟨record, invocation, isRecord, hasInvocation, isId, isMethod⟩ :=
      (mem_currentKeys base entry.1).mp isCurrent
    refine ⟨record, isRecord, isId, fun isExecute => Or.inl ?_⟩
    have phaseMatches := invocationMatchesPhase record isRecord
    rw [hasInvocation] at phaseMatches
    simp only at phaseMatches
    rw [phaseMatches, isMethod, isExecute]

/-- `retryTimersHaveHistories` after the reconcile. -/
theorem reconciled_retryTimersHaveHistories (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ)
    (retryTimers : AssocList (RetryTimerId σ) Nat)
    (oldTimers : ∀ entry, entry ∈ retryTimers →
      (∃ history, ghost.findInvocation? (entry.1.step, entry.1.method) = some history) ∨
        (entry.1.step, entry.1.method) ∈ currentKeys base) :
    ∀ entry, entry ∈ retryTimers → ∃ history,
      ((countedGhost ghost isNormal seq clock).reconcile completed base).findInvocation?
        (entry.1.step, entry.1.method) = some history := by
  intro entry isMember
  cases isFound : ghost.findInvocation? (entry.1.step, entry.1.method) with
  | some history => exact ⟨_, reconciled_find_of_some _ _ _ _ _ _ _ _ isFound⟩
  | none =>
    rcases oldTimers entry isMember with ⟨history, isHistory⟩ | isCurrent
    · rw [isFound] at isHistory; cases isHistory
    · rw [reconciled_find_of_none _ _ _ _ _ _ _ isFound, if_pos isCurrent]
      exact ⟨_, rfl⟩

end DexExpressiveness.DexF.WPF4
