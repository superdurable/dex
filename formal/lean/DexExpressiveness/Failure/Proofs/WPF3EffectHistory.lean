import DexExpressiveness.Failure.Proofs.WPF3Basic

/-!
# WP-F3: the effect history of a finite run

`wpf3_effectHistory_step`: one DexF step keeps `HasEffectHistory`, given two
facts about the configuration before it (`WPF3EffectFacts`): every real task
has emitted a prefix of its invocation's effect list of length at most
`h(e, m)`, and a [STEP-START] that computes a snapshot finds no `par`.
`wpf3_effectHistory_of_exec` lifts this to every finite run along any
inductive invariant that gives the two facts. Everything here is proved.

Only three ghost writes matter: [WORKER-EFFECT] sets `h(e, m)` to the
index of a first occurrence, a [STEP-START] that computes a snapshot sets
`par(e, m)`, and `Ghost.reconcile` adds fresh histories for absent keys only.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Facts the effect history needs -/

/-- What the effect history needs of the configuration before a step. -/
structure WPF3EffectFacts (program : Program σ) (config : Config σ) : Prop where
  /-- Every real task has emitted a prefix of `fx⃗(e, m)` of length at most `h(e, m)`. -/
  tasksFollow : ∀ task, task ∈ config.realTasks → ∃ arguments,
    config.ghost.argumentsOf task.key = some arguments ∧
    task.emittedCount ≤ config.ghost.emittedCountOf task.key ∧
    ∀ remaining response, task.base.phase = .running remaining response →
      remaining = (arguments.compute program.failureFreeFlow task.key).1.drop task.emittedCount
  /-- A pending invocation without a snapshot has no `par`. -/
  freshStartsHaveNoArguments : ∀ run stepId record attempt method, config.run = some run →
    run.base.findStep? stepId = some record → run.findAttempt? stepId = some attempt →
    record.invocation = some { method := method, state := .pending } →
    attempt.snapshot = none → config.ghost.argumentsOf (stepId, method) = none

/-- The ghost keeps `par` and `h` of every invocation. -/
def WPF3GhostKeeps (ghost next : Ghost σ) : Prop :=
  ∀ key, next.argumentsOf key = ghost.argumentsOf key ∧
    next.emittedCountOf key = ghost.emittedCountOf key

/-- The events contain no external effect. -/
def WPF3NoFx (events : List (Obs σ)) : Prop :=
  ∀ step method effect index, Obs.fx step method effect index ∉ events

theorem wpf3_ghostKeeps_refl (ghost : Ghost σ) : WPF3GhostKeeps ghost ghost :=
  fun _ => ⟨rfl, rfl⟩

theorem wpf3_ghostKeeps_trans {first second third : Ghost σ}
    (firstKeeps : WPF3GhostKeeps first second) (secondKeeps : WPF3GhostKeeps second third) :
    WPF3GhostKeeps first third := fun key =>
  ⟨(secondKeeps key).1.trans (firstKeeps key).1, (secondKeeps key).2.trans (firstKeeps key).2⟩

theorem wpf3_noFx_nil : WPF3NoFx ([] : List (Obs σ)) := by
  intro _ _ _ _ isMember; cases isMember

theorem wpf3_of_some_eq {α β : Type} {result : α × β} {first : α} {second : β}
    (isSome : some result = some (first, second)) : first = result.1 ∧ second = result.2 := by
  cases isSome; exact ⟨rfl, rfl⟩

/-! ## The ghost after a commit -/

/-- Lookup in a map whose entries keep their keys. -/
theorem wpf3_get?_map_keys {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (change : κ × ν → κ × ν) (keepsKey : ∀ entry, (change entry).1 = entry.1) (key : κ) :
    AssocList.get? (map.map change) key =
      (map.get? key).map (fun value => (change (key, value)).2) := by
  induction map with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    have isHeadKey := keepsKey (headKey, headValue)
    by_cases isKey : headKey = key
    · subst isKey
      simp only [List.map_cons, AssocList.get?]
      rw [show change (headKey, headValue) = (headKey, (change (headKey, headValue)).2) from
        Prod.ext isHeadKey rfl]
      simp
    · simp only [List.map_cons, AssocList.get?, isKey, ↓reduceIte]
      rw [show change (headKey, headValue) = (headKey, (change (headKey, headValue)).2) from
        Prod.ext isHeadKey rfl]
      simp [isKey, inductionHypothesis]

/-- `par` and `h` of an association list of histories. -/
def wpf3ArgumentsIn (map : AssocList (InvocationKey σ) (InvocationHistory σ))
    (key : InvocationKey σ) : Option (Arguments σ) :=
  (map.get? key).bind (fun history => history.arguments)

def wpf3EmittedIn (map : AssocList (InvocationKey σ) (InvocationHistory σ))
    (key : InvocationKey σ) : Nat :=
  ((map.get? key).map (fun history => history.emittedCount)).getD 0

/-- Fresh histories added for absent keys, as `Ghost.reconcile` does. -/
def wpf3AddFresh (fresh : InvocationHistory σ) (map : AssocList (InvocationKey σ) (InvocationHistory σ))
    (keys : List (InvocationKey σ)) : AssocList (InvocationKey σ) (InvocationHistory σ) :=
  keys.foldl (fun entries key => if entries.contains key then entries else entries.set key fresh) map

/-- Adding fresh histories for absent keys keeps `par` and `h`, and binds every key. -/
theorem wpf3_foldl_addFresh (keys : List (InvocationKey σ)) (fresh : InvocationHistory σ)
    (freshArguments : fresh.arguments = none) (freshCount : fresh.emittedCount = 0)
    (map : AssocList (InvocationKey σ) (InvocationHistory σ)) :
    (∀ key, wpf3ArgumentsIn (wpf3AddFresh fresh map keys) key = wpf3ArgumentsIn map key ∧
      wpf3EmittedIn (wpf3AddFresh fresh map keys) key = wpf3EmittedIn map key) ∧
    (∀ key, map.contains key = true → (wpf3AddFresh fresh map keys).contains key = true) ∧
    (∀ key, key ∈ keys → (wpf3AddFresh fresh map keys).contains key = true) := by
  unfold wpf3AddFresh
  induction keys generalizing map with
  | nil => exact ⟨fun _ => ⟨rfl, rfl⟩, fun _ isContained => isContained, fun _ isMember => by
      cases isMember⟩
  | cons first rest inductionHypothesis =>
    simp only [List.foldl_cons]
    by_cases isContained : map.contains first = true
    · simp only [isContained, ↓reduceIte]
      obtain ⟨keeps, stays, binds⟩ := inductionHypothesis map
      refine ⟨keeps, stays, ?_⟩
      intro key isMember
      rcases List.mem_cons.mp isMember with isFirst | isRest
      · subst isFirst; exact stays _ isContained
      · exact binds key isRest
    · simp only [isContained, Bool.false_eq_true, ↓reduceIte]
      obtain ⟨keeps, stays, binds⟩ := inductionHypothesis (map.set first fresh)
      have isAbsent : map.get? first = none := by
        unfold AssocList.contains at isContained
        cases isFound : map.get? first with
        | none => rfl
        | some _ => rw [isFound] at isContained; simp at isContained
      refine ⟨?_, ?_, ?_⟩
      · intro key
        rw [(keeps key).1, (keeps key).2]
        unfold wpf3ArgumentsIn wpf3EmittedIn
        by_cases isKey : key = first
        · subst isKey
          rw [AssocList.get?_set_self, isAbsent]
          simp [freshArguments, freshCount]
        · rw [AssocList.get?_set_other _ _ _ _ isKey]
          exact ⟨rfl, rfl⟩
      · intro key isKeyContained
        apply stays
        unfold AssocList.contains at isKeyContained ⊢
        by_cases isKey : key = first
        · subst isKey; rw [AssocList.get?_set_self]; rfl
        · rw [AssocList.get?_set_other _ _ _ _ isKey]; exact isKeyContained
      · intro key isMember
        rcases List.mem_cons.mp isMember with isFirst | isRest
        · subst isFirst
          apply stays
          unfold AssocList.contains
          rw [AssocList.get?_set_self]; rfl
        · exact binds key isRest

/-- Marking outcomes and adding fresh histories keeps `par` and `h`. -/
theorem wpf3_keeps_of_marked_added (ghost : Ghost σ) (fresh : InvocationHistory σ)
    (freshArguments : fresh.arguments = none) (freshCount : fresh.emittedCount = 0)
    (change : InvocationKey σ × InvocationHistory σ → InvocationKey σ × InvocationHistory σ)
    (keepsKey : ∀ entry, (change entry).1 = entry.1)
    (keepsArguments : ∀ entry, (change entry).2.arguments = entry.2.arguments)
    (keepsCount : ∀ entry, (change entry).2.emittedCount = entry.2.emittedCount)
    (keys : List (InvocationKey σ)) :
    WPF3GhostKeeps ghost
      { ghost with invocations := wpf3AddFresh fresh (ghost.invocations.map change) keys } := by
  intro key
  obtain ⟨sameArguments, sameCount⟩ :=
    (wpf3_foldl_addFresh keys fresh freshArguments freshCount (ghost.invocations.map change)).1 key
  have markedKeeps := wpf3_get?_map_keys ghost.invocations change keepsKey key
  unfold wpf3ArgumentsIn at sameArguments
  unfold wpf3EmittedIn at sameCount
  unfold Ghost.argumentsOf Ghost.emittedCountOf Ghost.findInvocation?
  dsimp only
  rw [sameArguments, sameCount, markedKeeps]
  cases ghost.invocations.get? key with
  | none => exact ⟨rfl, rfl⟩
  | some history =>
    simp only [Option.map_some, Option.bind_some, Option.getD_some]
    exact ⟨keepsArguments (key, history), keepsCount (key, history)⟩

/-- After adding fresh histories, every added key is bound. -/
theorem wpf3_contains_added (ghost : Ghost σ) (fresh : InvocationHistory σ)
    (freshArguments : fresh.arguments = none) (freshCount : fresh.emittedCount = 0)
    (change : InvocationKey σ × InvocationHistory σ → InvocationKey σ × InvocationHistory σ)
    (keys : List (InvocationKey σ)) (key : InvocationKey σ) (isMember : key ∈ keys) :
    (({ ghost with invocations := wpf3AddFresh fresh (ghost.invocations.map change) keys } :
      Ghost σ).findInvocation? key).isSome = true :=
  (wpf3_foldl_addFresh keys fresh freshArguments freshCount _).2.2 key isMember

/-- `Ghost.reconcile` keeps `par` and `h`, and gives every current invocation a history. -/
theorem wpf3_reconcile_keeps (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) :
    WPF3GhostKeeps ghost (ghost.reconcile completed base) ∧
    ∀ record invocation, record ∈ base.steps → record.invocation = some invocation →
      ((ghost.reconcile completed base).findInvocation? (record.id, invocation.method)).isSome =
        true := by
  unfold Ghost.reconcile
  refine ⟨wpf3_keeps_of_marked_added ghost _ rfl rfl _ (by intro entry; dsimp only; split <;> rfl)
    (by intro entry; dsimp only; split <;> rfl) (by intro entry; dsimp only; split <;> rfl) _, ?_⟩
  intro record invocation isMember hasInvocation
  exact wpf3_contains_added ghost _ rfl rfl _ _ _
    (List.mem_filterMap.mpr ⟨record, isMember, by simp [hasInvocation]⟩)

/-- The closing event of a commit is no external effect. -/
theorem wpf3_noFx_closeEvents (before after : Dex.Run σ) :
    WPF3NoFx (Config.closeEvents before after : List (Obs σ)) := by
  intro step method effect index isMember
  unfold Config.closeEvents at isMember
  split at isMember <;> simp at isMember

/-- A commit keeps `par` and `h`, emits no external effect, and gives every
current invocation a history. -/
theorem wpf3_commitRun_keeps (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (noInputFx : WPF3NoFx inputEvents) :
    WPF3GhostKeeps config.ghost (config.commitRun run isNormal completed inputEvents change).2.ghost ∧
    WPF3NoFx (config.commitRun run isNormal completed inputEvents change).1 := by
  unfold Config.commitRun
  dsimp only
  refine ⟨wpf3_ghostKeeps_trans ?_ (wpf3_reconcile_keeps _ _ _).1, ?_⟩
  · cases isNormal <;> exact fun _ => ⟨rfl, rfl⟩
  · intro step method effect index isMember
    rcases List.mem_append.mp isMember with isInput | isClose
    · exact noInputFx _ _ _ _ isInput
    · exact wpf3_noFx_closeEvents _ _ _ _ _ _ isClose

/-- [ATTEMPT-LOST], [ATTEMPT-EXHAUSTED], and an applied interrupt keep `par`
and `h` and emit no external effect. -/
theorem wpf3_failAttempt_keeps (config : Config σ) (run : Run σ) (stepId : Dex.StepExecId σ)
    (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) :
    WPF3GhostKeeps config.ghost
      (config.failAttempt run stepId method attempt cause lostTaskId decision).2.ghost ∧
    WPF3NoFx (config.failAttempt run stepId method attempt cause lostTaskId decision).1 := by
  unfold Config.failAttempt
  have sameGhost : (match lostTaskId with
      | some taskId => config.moveToZombies taskId
      | none => config).ghost = config.ghost := by
    cases lostTaskId <;> rfl
  cases decision with
  | retryAfter backoffMicros =>
    dsimp only
    rw [← sameGhost]
    exact wpf3_commitRun_keeps _ run false none [] _ wpf3_noFx_nil
  | exhausted _ =>
    dsimp only
    rw [← sameGhost]
    exact wpf3_commitRun_keeps _ run false none [] _ wpf3_noFx_nil

/-- The keeping case of a step. -/
def WPF3KeepsStep (config : Config σ) (events : List (Obs σ)) (next : Config σ) : Prop :=
  WPF3GhostKeeps config.ghost next.ghost ∧ WPF3NoFx events

theorem wpf3_keepsStep_of_commitRun (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (noInputFx : WPF3NoFx inputEvents) :
    WPF3KeepsStep config (config.commitRun run isNormal completed inputEvents change).1
      (config.commitRun run isNormal completed inputEvents change).2 :=
  wpf3_commitRun_keeps config run isNormal completed inputEvents change noInputFx

theorem wpf3_noFx_single_nonFx (event : Obs σ)
    (isNotFx : ∀ step method effect index, event ≠ Obs.fx step method effect index) :
    WPF3NoFx [event] := by
  intro step method effect index isMember
  rw [List.mem_singleton] at isMember
  exact isNotFx step method effect index isMember.symm

theorem wpf3_keepsStep_same (config : Config σ) (next : Config σ) (events : List (Obs σ))
    (sameGhost : next.ghost = config.ghost) (noFx : WPF3NoFx events) :
    WPF3KeepsStep config events next := by
  rw [WPF3KeepsStep, sameGhost]
  exact ⟨wpf3_ghostKeeps_refl _, noFx⟩

/-! ## One step keeps the effect history -/

/-- A step that keeps `par` and `h` and emits no effect keeps the history. -/
theorem wpf3_effectHistory_of_keeps (program : Program σ) (config next : Config σ)
    (events stepEvents : List (Obs σ)) (keeps : WPF3KeepsStep config stepEvents next)
    (history : HasEffectHistory program config events) :
    HasEffectHistory program next (events ++ stepEvents) := by
  obtain ⟨ghostKeeps, noFx⟩ := keeps
  obtain ⟨labels, complete⟩ := history
  refine ⟨?_, ?_⟩
  · intro step method effect index isMember
    rcases List.mem_append.mp isMember with isOld | isNew
    · rw [(ghostKeeps _).1, (ghostKeeps _).2]
      exact labels step method effect index isOld
    · exact absurd isNew (noFx step method effect index)
  · intro step method index isPositive isAtMost
    rw [(ghostKeeps _).2] at isAtMost
    obtain ⟨effect, isMember⟩ := complete step method index isPositive isAtMost
    exact ⟨effect, List.mem_append_left _ isMember⟩

/-- A [STEP-START] that sets `par` of an invocation without one keeps the history. -/
theorem wpf3_effectHistory_of_freshStart (program : Program σ) (config next : Config σ)
    (events stepEvents : List (Obs σ)) (key : InvocationKey σ)
    (hasNoArguments : config.ghost.argumentsOf key = none)
    (keepsCounts : ∀ other, next.ghost.emittedCountOf other = config.ghost.emittedCountOf other)
    (keepsOthers : ∀ other, other ≠ key →
      next.ghost.argumentsOf other = config.ghost.argumentsOf other)
    (noFx : WPF3NoFx stepEvents) (history : HasEffectHistory program config events) :
    HasEffectHistory program next (events ++ stepEvents) := by
  obtain ⟨labels, complete⟩ := history
  refine ⟨?_, ?_⟩
  · intro step method effect index isMember
    rcases List.mem_append.mp isMember with isOld | isNew
    · obtain ⟨isPositive, isAtMost, arguments, hasArguments, isLabel⟩ :=
        labels step method effect index isOld
      have isOther : (step, method) ≠ key := by
        intro isKey
        rw [isKey, hasNoArguments] at hasArguments
        cases hasArguments
      rw [keepsCounts, keepsOthers _ isOther]
      exact ⟨isPositive, isAtMost, arguments, hasArguments, isLabel⟩
    · exact absurd isNew (noFx step method effect index)
  · intro step method index isPositive isAtMost
    rw [keepsCounts] at isAtMost
    obtain ⟨effect, isMember⟩ := complete step method index isPositive isAtMost
    exact ⟨effect, List.mem_append_left _ isMember⟩

/-- `h` after changing a history. -/
theorem wpf3_emittedCountOf_updateInvocation (ghost : Ghost σ) (key wanted : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).emittedCountOf wanted =
      if wanted = key then
        ((ghost.findInvocation? wanted).map (fun history => (change history).emittedCount)).getD 0
      else ghost.emittedCountOf wanted := by
  unfold Ghost.emittedCountOf
  rw [wpf3_findInvocation?_updateInvocation]
  split
  · cases ghost.findInvocation? wanted <;> rfl
  · rfl

/-- An effect of a real task keeps the history. -/
theorem wpf3_effectHistory_workerEffect (program : Program σ) (config : Config σ)
    (facts : WPF3EffectFacts program config) (events : List (Obs σ))
    (history : HasEffectHistory program config events) (taskId : Nat)
    (isFirstOccurrence : Bool) (stepEvents : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerEffect taskId isFirstOccurrence) =
      some (stepEvents, next)) :
    HasEffectHistory program next (events ++ stepEvents) := by
  dsimp only [step] at isStep
  cases isFound : config.findRealTask? taskId with
  | none => rw [isFound] at isStep; cases isStep
  | some task =>
    rw [isFound] at isStep
    dsimp only at isStep
    obtain ⟨isMember, _⟩ := wpf3_mem_of_findRealTask? config taskId task isFound
    obtain ⟨arguments, hasArguments, emittedAtMost, remainingFacts⟩ :=
      facts.tasksFollow task isMember
    cases isPhase : task.base.phase with
    | started => rw [isPhase] at isStep; cases isStep
    | returned _ => rw [isPhase] at isStep; cases isStep
    | running remaining response =>
      cases remaining with
      | nil => rw [isPhase] at isStep; cases isStep
      | cons effect laterEffects =>
        have isRemaining := remainingFacts _ _ isPhase
        have isLabel : (arguments.compute program.failureFreeFlow task.key).1[task.emittedCount]? =
            some effect := by
          have isHead := congrArg (fun effects : List σ.Fx => effects[0]?) isRemaining
          simp only [List.getElem?_cons_zero, List.getElem?_drop, Nat.add_zero] at isHead
          exact isHead.symm
        rw [isPhase] at isStep
        dsimp only at isStep
        split at isStep
        · cases isStep
        rename_i isAgree
        have isFirstEq : isFirstOccurrence =
            decide (config.ghost.emittedCountOf task.key < task.emittedCount + 1) := by
          simpa using isAgree
        obtain ⟨isEvents, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
        subst isEvents
        have isKeyEq : (task.base.step, task.base.method) = task.key := rfl
        obtain ⟨labels, complete⟩ := history
        have hasHistory : ∃ found, config.ghost.findInvocation? task.key = some found := by
          unfold Ghost.argumentsOf at hasArguments
          cases isFoundHistory : config.ghost.findInvocation? task.key with
          | none => rw [isFoundHistory] at hasArguments; cases hasArguments
          | some found => exact ⟨found, rfl⟩
        obtain ⟨found, isFoundHistory⟩ := hasHistory
        cases isFirstOccurrence with
        | false =>
          have sameGhost : next.ghost = config.ghost := by rw [← isNext]; rfl
          have isRepeated : task.emittedCount + 1 ≤ config.ghost.emittedCountOf task.key := by
            simp only [Bool.false_eq, decide_eq_false_iff_not, Nat.not_lt] at isFirstEq
            exact isFirstEq
          unfold HasEffectHistory
          rw [sameGhost]
          refine ⟨?_, ?_⟩
          · intro step method effect' index isEventMember
            rcases List.mem_append.mp isEventMember with isOld | isNew
            · exact labels step method effect' index isOld
            · simp only [List.mem_singleton, Obs.fx.injEq] at isNew
              obtain ⟨isStepEq, isMethodEq, isEffectEq, isIndexEq⟩ := isNew
              subst isStepEq; subst isMethodEq; subst isEffectEq; subst isIndexEq
              refine ⟨by omega, isRepeated, arguments, hasArguments, ?_⟩
              rw [Nat.add_sub_cancel]; exact isLabel
          · intro step method index isPositive isAtMost
            obtain ⟨effect', isOld⟩ := complete step method index isPositive isAtMost
            exact ⟨effect', List.mem_append_left _ isOld⟩
        | true =>
          have isEmittedEq : config.ghost.emittedCountOf task.key = task.emittedCount := by
            simp only [Bool.true_eq, decide_eq_true_eq] at isFirstEq
            omega
          have isGhost : next.ghost = config.ghost.updateInvocation task.key (fun history =>
              { history with emittedCount := task.emittedCount + 1 }) := by
            rw [← isNext]; rfl
          have nextArguments : ∀ other, next.ghost.argumentsOf other =
              config.ghost.argumentsOf other := by
            intro other
            rw [isGhost]
            exact wpf3_argumentsOf_updateInvocation _ _ _ _ (fun _ => rfl)
          have nextCount : ∀ other, next.ghost.emittedCountOf other =
              if other = task.key then task.emittedCount + 1
              else config.ghost.emittedCountOf other := by
            intro other
            rw [isGhost, wpf3_emittedCountOf_updateInvocation]
            split
            · rename_i isKey
              subst isKey
              rw [isFoundHistory]
              rfl
            · rfl
          have countGrows : ∀ other, config.ghost.emittedCountOf other ≤
              next.ghost.emittedCountOf other := by
            intro other
            rw [nextCount]
            split
            · rename_i isKey; subst isKey; omega
            · exact Nat.le_refl _
          refine ⟨?_, ?_⟩
          · intro step method effect' index isEventMember
            rcases List.mem_append.mp isEventMember with isOld | isNew
            · obtain ⟨isPositive, isAtMost, oldArguments, hasOldArguments, isOldLabel⟩ :=
                labels step method effect' index isOld
              rw [nextArguments]
              exact ⟨isPositive, Nat.le_trans isAtMost (countGrows _), oldArguments,
                hasOldArguments, isOldLabel⟩
            · simp only [List.mem_singleton, Obs.fx.injEq] at isNew
              obtain ⟨isStepEq, isMethodEq, isEffectEq, isIndexEq⟩ := isNew
              subst isStepEq; subst isMethodEq; subst isEffectEq; subst isIndexEq
              rw [nextArguments, isKeyEq, nextCount]
              refine ⟨by omega, by simp, arguments, hasArguments, ?_⟩
              rw [Nat.add_sub_cancel]; exact isLabel
          · intro step method index isPositive isAtMost
            rw [nextCount] at isAtMost
            by_cases isKey : (step, method) = task.key
            · simp only [isKey, ↓reduceIte] at isAtMost
              by_cases isNewIndex : index = task.emittedCount + 1
              · subst isNewIndex
                refine ⟨effect, List.mem_append_right _ ?_⟩
                have isStepEq : step = task.base.step := congrArg Prod.fst isKey
                have isMethodEq : method = task.base.method := congrArg Prod.snd isKey
                subst isStepEq; subst isMethodEq
                exact List.mem_singleton_self _
              · have isOldIndex : index ≤ config.ghost.emittedCountOf (step, method) := by
                  rw [isKey, isEmittedEq]; omega
                obtain ⟨effect', isOld⟩ := complete step method index isPositive isOldIndex
                exact ⟨effect', List.mem_append_left _ isOld⟩
            · simp only [isKey, ↓reduceIte] at isAtMost
              obtain ⟨effect', isOld⟩ := complete step method index isPositive isAtMost
              exact ⟨effect', List.mem_append_left _ isOld⟩

/-- A change of a history that keeps `par` and `h` keeps the ghost. -/
theorem wpf3_ghostKeeps_updateInvocation (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ)
    (keepsArguments : ∀ history, (change history).arguments = history.arguments)
    (keepsCount : ∀ history, (change history).emittedCount = history.emittedCount) :
    WPF3GhostKeeps ghost (ghost.updateInvocation key change) := by
  intro wanted
  refine ⟨wpf3_argumentsOf_updateInvocation _ _ _ _ keepsArguments, ?_⟩
  rw [wpf3_emittedCountOf_updateInvocation]
  split
  · unfold Ghost.emittedCountOf
    cases ghost.findInvocation? wanted <;> simp [keepsCount]
  · rfl

/-- Finishes a branch of a step that keeps `par` and `h` and emits no effect. -/
macro "wpf3_keeps_leaf" isStep:ident : tactic => `(tactic| first
  | (cases $isStep:ident; done)
  | (obtain ⟨isEvents, isNext⟩ := wpf3_of_some_eq $isStep:ident;
     subst isEvents; subst isNext;
     first
      | exact wpf3_keepsStep_same _ _ _ rfl wpf3_noFx_nil
      | exact wpf3_keepsStep_same _ _ _ rfl
          (wpf3_noFx_single_nonFx _ (by intro _ _ _ _ isFx; cases isFx))
      | exact wpf3_keepsStep_of_commitRun _ _ _ _ _ _ wpf3_noFx_nil
      | exact wpf3_keepsStep_of_commitRun _ _ _ _ _ _
          (wpf3_noFx_single_nonFx _ (by intro _ _ _ _ isFx; cases isFx))
      | exact wpf3_failAttempt_keeps _ _ _ _ _ _ _ _))

/-- Every step other than an effect and a [STEP-START] keeps `par` and `h`
and emits no external effect. -/
theorem wpf3_keepsStep (program : Program σ) (config : Config σ) (action : Action σ)
    (isNotEffect : ∀ taskId isFirstOccurrence, action ≠ .workerEffect taskId isFirstOccurrence)
    (isNotStart : ∀ stepId isSnapshotReused, action ≠ .stepStart stepId isSnapshotReused)
    (stepEvents : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (stepEvents, next)) :
    WPF3KeepsStep config stepEvents next := by
  cases action with
  | workerEffect taskId isFirstOccurrence => exact absurd rfl (isNotEffect taskId isFirstOccurrence)
  | stepStart stepId isSnapshotReused => exact absurd rfl (isNotStart stepId isSnapshotReused)
  | workerRun taskId isFirstRun =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (obtain ⟨rfl, rfl⟩ := wpf3_of_some_eq isStep
         refine ⟨?_, wpf3_noFx_nil⟩
         first
          | exact wpf3_ghostKeeps_refl _
          | exact wpf3_ghostKeeps_updateInvocation _ _ _ (fun _ => rfl) (fun _ => rfl))
  | workerReturn taskId isFirstReturn =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (obtain ⟨rfl, rfl⟩ := wpf3_of_some_eq isStep
         refine ⟨?_, wpf3_noFx_nil⟩
         first
          | exact wpf3_ghostKeeps_refl _
          | exact wpf3_ghostKeeps_updateInvocation _ _ _ (fun _ => rfl) (fun _ => rfl))
  | tick _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | startRetry _ _ _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | workerAbandon _ _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | stepComplete _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | resultStale _ _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | timerFire _ _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | attemptFailed _ _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | attemptReturned _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | attemptHeartbeat _ _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | crash =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | recover =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | recoverInterrupt _ _ =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | internalCommit =>
    dsimp only [step] at isStep
    repeat' split at isStep
    all_goals wpf3_keeps_leaf isStep
  | request clientRequest =>
    cases clientRequest with
    | startFlow _ _ _ =>
      dsimp only [step] at isStep
      repeat' split at isStep
      all_goals wpf3_keeps_leaf isStep
    | publish _ _ =>
      dsimp only [step] at isStep
      repeat' split at isStep
      all_goals wpf3_keeps_leaf isStep
  | engineReject clientRequest =>
    cases clientRequest with
    | startFlow _ _ _ =>
      dsimp only [step] at isStep
      repeat' split at isStep
      all_goals wpf3_keeps_leaf isStep
    | publish _ _ =>
      dsimp only [step] at isStep
      repeat' split at isStep
      all_goals wpf3_keeps_leaf isStep

/-- The ghost after a [STEP-START] that computes a snapshot. -/
theorem wpf3_freshStartGhost (ghost : Ghost σ) (key : InvocationKey σ)
    (newArguments : Arguments σ) (nextVirtualTaskId : Nat) :
    (∀ other, ({ (ghost.updateInvocation key (fun history =>
        { history with arguments := some newArguments })) with
        nextVirtualTaskId := nextVirtualTaskId } : Ghost σ).emittedCountOf other =
      ghost.emittedCountOf other) ∧
    (∀ other, other ≠ key → ({ (ghost.updateInvocation key (fun history =>
        { history with arguments := some newArguments })) with
        nextVirtualTaskId := nextVirtualTaskId } : Ghost σ).argumentsOf other =
      ghost.argumentsOf other) := by
  refine ⟨?_, ?_⟩
  · intro other
    show (ghost.updateInvocation key _).emittedCountOf other = _
    rw [wpf3_emittedCountOf_updateInvocation]
    split
    · unfold Ghost.emittedCountOf
      cases ghost.findInvocation? other <;> rfl
    · rfl
  · intro other isOther
    show (ghost.updateInvocation key _).argumentsOf other = _
    unfold Ghost.argumentsOf
    rw [wpf3_findInvocation?_updateInvocation]
    simp only [isOther, ↓reduceIte]

/-- A [STEP-START] keeps the history. -/
theorem wpf3_effectHistory_stepStart (program : Program σ) (config : Config σ)
    (facts : WPF3EffectFacts program config) (events : List (Obs σ))
    (history : HasEffectHistory program config events) (stepId : Dex.StepExecId σ)
    (isSnapshotReused : Bool) (stepEvents : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.stepStart stepId isSnapshotReused) =
      some (stepEvents, next)) :
    HasEffectHistory program next (events ++ stepEvents) := by
  dsimp only [step] at isStep
  split at isStep
  · cases isRun : config.run with
    | none => rw [isRun] at isStep; simp at isStep
    | some run =>
      rw [isRun] at isStep
      dsimp only at isStep
      cases isRecord : run.base.findStep? stepId with
      | none => rw [isRecord] at isStep; simp at isStep
      | some record =>
        cases isAttempt : run.findAttempt? stepId with
        | none => rw [isRecord, isAttempt] at isStep; simp at isStep
        | some attempt =>
          rw [isRecord, isAttempt] at isStep
          dsimp only at isStep
          cases isInvocation : record.invocation with
          | none => rw [isInvocation] at isStep; simp at isStep
          | some invocation =>
            obtain ⟨method, state⟩ := invocation
            cases state with
            | running _ => rw [isInvocation] at isStep; simp at isStep
            | pending =>
              rw [isInvocation] at isStep
              dsimp only at isStep
              split at isStep
              · cases isStep
              rename_i isAllowed
              obtain ⟨isEvents, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
              subst isEvents
              subst isNext
              have commitKeeps := wpf3_commitRun_keeps config run (!isSnapshotReused) none [] (fun current =>
                { (current.setAttempt stepId
                    { attempt with
                      snapshot := some (attempt.snapshot.getD
                        (run.base.snapshot record.options method))
                      firstAttemptTimestamp :=
                        some (attempt.context config.clock).firstAttemptTimestamp }) with
                  base := current.base.updateStep stepId (fun currentRecord =>
                    { currentRecord with
                      invocation :=
                        some { method := method, state := .running config.nextTaskId } }) })
                wpf3_noFx_nil
              cases isSnapshotReused with
              | true =>
                exact wpf3_effectHistory_of_keeps program config _ events _
                  ⟨commitKeeps.1, commitKeeps.2⟩ history
              | false =>
                have isNoSnapshot : attempt.snapshot = none := by
                  revert isAllowed
                  cases attempt.snapshot <;> simp
                have hasNoArguments := facts.freshStartsHaveNoArguments run stepId record attempt
                  method isRun isRecord isAttempt isInvocation isNoSnapshot
                obtain ⟨freshCounts, freshOthers⟩ := wpf3_freshStartGhost
                  (config.commitRun run (!false) none [] _).2.ghost (stepId, method)
                  { context := { stepExecutionId := stepId, fromStepExecutionId := record.origin },
                    input := record.input,
                    snapshot := attempt.snapshot.getD (run.base.snapshot record.options method),
                    conditionResults := record.conditionResults,
                    virtualTaskId := (config.commitRun run (!false) none [] _).2.ghost.nextVirtualTaskId }
                  ((config.commitRun run (!false) none [] _).2.ghost.nextVirtualTaskId + 1)
                refine wpf3_effectHistory_of_freshStart program config _ events _ (stepId, method)
                  hasNoArguments ?_ ?_ commitKeeps.2 history
                · intro other
                  exact (freshCounts other).trans (commitKeeps.1 other).2
                · intro other isOther
                  exact (freshOthers other isOther).trans (commitKeeps.1 other).1
  · cases isStep

/-- **The effect history, one step.** -/
theorem wpf3_effectHistory_step (program : Program σ) (config : Config σ)
    (facts : WPF3EffectFacts program config) (events : List (Obs σ))
    (history : HasEffectHistory program config events) (action : Action σ)
    (stepEvents : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (stepEvents, next)) :
    HasEffectHistory program next (events ++ stepEvents) := by
  by_cases isEffect : ∃ taskId isFirstOccurrence, action = .workerEffect taskId isFirstOccurrence
  · obtain ⟨taskId, isFirstOccurrence, isAction⟩ := isEffect
    subst isAction
    exact wpf3_effectHistory_workerEffect program config facts events history taskId
      isFirstOccurrence stepEvents next isStep
  by_cases isStart : ∃ stepId isSnapshotReused, action = .stepStart stepId isSnapshotReused
  · obtain ⟨stepId, isSnapshotReused, isAction⟩ := isStart
    subst isAction
    exact wpf3_effectHistory_stepStart program config facts events history stepId
      isSnapshotReused stepEvents next isStep
  exact wpf3_effectHistory_of_keeps program config next events stepEvents
    (wpf3_keepsStep program config action
      (fun taskId isFirstOccurrence isAction => isEffect ⟨taskId, isFirstOccurrence, isAction⟩)
      (fun stepId isSnapshotReused isAction => isStart ⟨stepId, isSnapshotReused, isAction⟩)
      stepEvents next isStep) history

/-- The initial configuration has the empty effect history. -/
theorem wpf3_effectHistory_initial (program : Program σ) :
    HasEffectHistory program (Config.initial σ) [] := by
  refine ⟨fun _ _ _ _ isMember => (by cases isMember), ?_⟩
  intro step method index isPositive isAtMost
  simp [Config.initial, Ghost.initial, Ghost.emittedCountOf, Ghost.findInvocation?,
    AssocList.get?] at isAtMost
  omega

/-- **The effect history of every finite run**, along any inductive invariant
that gives the facts the effect history needs. -/
theorem wpf3_effectHistory_of_exec (program : Program σ) (IsGood : Config σ → Prop)
    (isInitialGood : IsGood (Config.initial σ))
    (isGoodPreserved : ∀ config action events next, IsGood config →
      step program config action = some (events, next) → IsGood next)
    (factsOfGood : ∀ config, IsGood config → WPF3EffectFacts program config)
    (labels : List (Action σ)) (events : List (Obs σ)) (config : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, config)) :
    HasEffectHistory program config events := by
  have general : ∀ (labels : List (Action σ)) (start : Config σ) (earlier : List (Obs σ))
      (later : List (Obs σ)) (final : Config σ), IsGood start →
      HasEffectHistory program start earlier →
      (system program).exec start labels = some (later, final) →
      IsGood final ∧ HasEffectHistory program final (earlier ++ later) := by
    intro labels
    induction labels with
    | nil =>
      intro start earlier later final isGood history isRun
      simp only [TransitionSystem.exec, Option.some.injEq, Prod.mk.injEq] at isRun
      obtain ⟨isLater, isFinal⟩ := isRun
      subst isLater; subst isFinal
      simpa using And.intro isGood history
    | cons label laterLabels inductionHypothesis =>
      intro start earlier later final isGood history isRun
      unfold TransitionSystem.exec at isRun
      cases isStep : (system program).step start label with
      | none => rw [isStep] at isRun; cases isRun
      | some result =>
        obtain ⟨stepEvents, next⟩ := result
        rw [isStep] at isRun
        dsimp only at isRun
        cases isRest : (system program).exec next laterLabels with
        | none => rw [isRest] at isRun; cases isRun
        | some restResult =>
          obtain ⟨restEvents, restFinal⟩ := restResult
          rw [isRest] at isRun
          simp only [Option.some.injEq, Prod.mk.injEq] at isRun
          obtain ⟨isLater, isFinal⟩ := isRun
          subst isLater; subst isFinal
          have isRealStep : step program start label = some (stepEvents, next) := isStep
          obtain ⟨isRestGood, restHistory⟩ := inductionHypothesis next (earlier ++ stepEvents)
            restEvents restFinal (isGoodPreserved start label stepEvents next isGood isRealStep)
            (wpf3_effectHistory_step program start (factsOfGood start isGood) earlier history
              label stepEvents next isRealStep) isRest
          exact ⟨isRestGood, by simpa [List.append_assoc] using restHistory⟩
  have result := general labels (Config.initial σ) [] events config isInitialGood
    (wpf3_effectHistory_initial program) isExec
  simpa using result.2

end DexExpressiveness.DexF
