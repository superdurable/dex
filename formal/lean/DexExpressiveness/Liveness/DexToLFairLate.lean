import DexExpressiveness.L.Semantics

/-!
# Liveness, work package L4: late emits exist only after the end

`L.Config.lateEmits` is set when the program ends (`closeProgram`) and is
empty before. `L.Invariant` does not say so, and F4 of L
(`L.Fairness.EmitsSettle`) speaks about every configuration that has a late
emit. This file proves that a configuration of an L run that is not closed
has no late emit (`lateEmits_eq_nil_of_isRun`).

Everything here is proved.
-/

namespace DexExpressiveness.L

namespace Config

/-- The status and the late emits: only the end of the program changes them. -/
def endState (config : Config) : Status × List Val :=
  (config.status, config.lateEmits)

/-- Releasing a mutex keeps the status and the late emits. -/
theorem endState_releaseMutex (config : Config) (mutex : MutexId) :
    (config.releaseMutex mutex).endState = config.endState := by
  unfold releaseMutex
  dsimp only
  split <;> rfl

/-- Waking a waiter of a condition variable keeps the status and the late emits. -/
theorem endState_wakeCvWaiter (config : Config) (cv : CvId) (waiter : Tid) :
    (config.wakeCvWaiter cv waiter).endState = config.endState := by
  unfold wakeCvWaiter
  repeat' split
  all_goals rfl

/-- Waking several waiters keeps the status and the late emits. -/
theorem endState_foldl_wakeCvWaiter (cv : CvId) (waiters : List Tid) (config : Config) :
    (waiters.foldl (fun current waiter => current.wakeCvWaiter cv waiter) config).endState =
      config.endState := by
  induction waiters generalizing config with
  | nil => rfl
  | cons waiter laterWaiters inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis, endState_wakeCvWaiter]

/-- Delivering a cancel keeps the status and the late emits. -/
theorem endState_cancelBlockedThread (config : Config) (target : Tid) :
    (config.cancelBlockedThread target).endState = config.endState := by
  unfold cancelBlockedThread
  repeat' split
  all_goals rfl

/-- The action of a completed statement ends the program, or keeps the status
and the late emits. -/
theorem endState_applyIntent (config : Config) (thread : Tid) (result : StatementResult) :
    (config.applyIntent thread result).2.status = .closed ∨
      (config.applyIntent thread result).2.endState = config.endState := by
  unfold applyIntent
  dsimp only
  split
  · exact Or.inr rfl
  · exact Or.inr rfl
  · exact Or.inr rfl
  · exact Or.inr rfl
  · exact Or.inr (endState_releaseMutex _ _)
  · exact Or.inr (endState_releaseMutex _ _)
  · split
    · exact Or.inr rfl
    · exact Or.inr (endState_wakeCvWaiter _ _ _)
  · exact Or.inr (endState_foldl_wakeCvWaiter _ _ _)
  · exact Or.inr (endState_cancelBlockedThread _ _)
  · exact Or.inr rfl
  · split <;> exact Or.inr rfl
  · exact Or.inl rfl
  · exact Or.inl rfl
  · exact Or.inr rfl

/-- Registering a wait keeps the status and the late emits. -/
theorem endState_registerWait (config : Config) (thread : Thread) :
    (config.registerWait thread).endState = config.endState := by
  unfold registerWait
  dsimp only
  repeat' split
  all_goals rfl

/-- An external message keeps the status and the late emits. -/
theorem endState_deliverInput (config : Config) (chan : ExtChan) (value : Val) :
    (config.deliverInput chan value).endState = config.endState := by
  unfold deliverInput
  dsimp only
  split <;> rfl

/-- Firing timers keeps the status and the late emits. -/
theorem endState_fireTimers (count : Nat) (config fired : Config)
    (isFired : config.fireTimers count = some fired) : fired.endState = config.endState := by
  induction count generalizing config with
  | zero =>
    simp only [fireTimers] at isFired
    cases isFired
    rfl
  | succ count inductionHypothesis =>
    simp only [fireTimers] at isFired
    split at isFired
    · cases isFired
    · have isKept := inductionHypothesis _ isFired
      exact isKept

end Config

/-- One action of a thread ends the program, or keeps the status and the late emits. -/
theorem endState_stepThread (program : Program) (config : Config) (thread : Thread)
    (events : List Event) (next : Config)
    (isStep : stepThread program config thread = some (events, next)) :
    next.status = .closed ∨ next.endState = config.endState := by
  unfold stepThread at isStep
  dsimp only at isStep
  split at isStep
  · split at isStep <;> cases isStep
    exact Or.inr rfl
  · split at isStep <;> cases isStep
    exact Or.inr rfl
  · split at isStep
    · split at isStep <;> cases isStep
      exact Or.inr rfl
    · split at isStep
      · split at isStep
        all_goals
          split at isStep <;> cases isStep
          exact Or.inr rfl
      · cases isStep
  · split at isStep <;> cases isStep
    exact Config.endState_applyIntent _ _ _
  · cases isStep
    exact Or.inr rfl
  · split at isStep <;> cases isStep
    exact Config.endState_applyIntent _ _ _
  · split at isStep <;> cases isStep
    exact Or.inr (Config.endState_registerWait _ _)
  · cases isStep
  · split at isStep <;> cases isStep
    exact Or.inr rfl
  · split at isStep <;> cases isStep
    exact Config.endState_applyIntent _ _ _
  · cases isStep

/-- One transition ends in a closed configuration, or it starts from a
configuration that is not closed and keeps the late emits. -/
theorem lateEmits_step (program : Program) (config : Config) (label : Label)
    (events : List Event) (next : Config)
    (isStep : step program config label = some (events, next)) :
    next.status = .closed ∨ (next.lateEmits = config.lateEmits ∧ config.status ≠ .closed) := by
  have ofEndState : next.endState = config.endState →
      next.status = .closed ∨ (next.lateEmits = config.lateEmits ∧ config.status ≠ .closed) := by
    intro isKept
    have sameStatus : next.status = config.status := congrArg Prod.fst isKept
    have sameLate : next.lateEmits = config.lateEmits := congrArg Prod.snd isKept
    by_cases isClosed : config.status = .closed
    · exact Or.inl (sameStatus.trans isClosed)
    · exact Or.inr ⟨sameLate, isClosed⟩
  cases label with
  | tick duration =>
    simp only [step] at isStep
    split at isStep <;> cases isStep
    exact ofEndState rfl
  | request request =>
    cases request with
    | start input =>
      simp only [step] at isStep
      split at isStep
      · rename_i isNotStarted
        split at isStep <;> cases isStep
        refine Or.inr ⟨rfl, ?_⟩
        rw [isNotStarted]
        nofun
      · cases isStep
    | input chan value =>
      simp only [step] at isStep
      split at isStep
      · cases isStep
      · split at isStep <;> cases isStep
        exact ofEndState (Config.endState_deliverInput _ _ _)
      · cases isStep
        exact ofEndState rfl
  | thread thread =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · rcases endState_stepThread program config _ events next isStep with isClosed | isKept
      · exact Or.inl isClosed
      · exact ofEndState isKept
  | fire count =>
    simp only [step] at isStep
    split at isStep
    · cases firedCase : config.fireTimers count with
      | none => simp [firedCase] at isStep
      | some fired =>
        simp only [firedCase, Option.map_some, Option.some.injEq, Prod.mk.injEq] at isStep
        obtain ⟨_, isNext⟩ := isStep
        subst isNext
        exact ofEndState (Config.endState_fireTimers count config fired firedCase)
    · cases isStep
  | lateDeliver index =>
    simp only [step] at isStep
    split at isStep
    · rename_i isClosed _
      cases isStep
      exact Or.inl isClosed
    · cases isStep
  | lateDrop index =>
    simp only [step] at isStep
    split at isStep
    · rename_i isClosed _
      cases isStep
      exact Or.inl isClosed
    · cases isStep

/-- A configuration of an L run that is not closed has no late emit. -/
theorem lateEmits_eq_nil_of_isRun (program : Program) (run : InfiniteRun Config Label)
    (isRun : (system program).IsRun run) (position : Nat)
    (isNotClosed : (run.states position).status ≠ .closed) :
    (run.states position).lateEmits = [] := by
  induction position with
  | zero =>
    rw [isRun.1]
    rfl
  | succ position inductionHypothesis =>
    obtain ⟨events, isStep⟩ := isRun.2 position
    rcases lateEmits_step program _ _ events _ isStep with isClosed | ⟨sameLate, wasNotClosed⟩
    · exact absurd isClosed isNotClosed
    · rw [sameLate]
      exact inductionHypothesis wasNotClosed

end DexExpressiveness.L
