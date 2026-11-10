import DexExpressiveness.L.InvariantStepActions

/-!
# Every L transition keeps the L invariant

Work package 1 of the safety proof. `Invariant.step` is proved here.
`Invariant.of_exec` and `Invariant.of_reachable` follow from it.

The proof of `Invariant.step` is a case analysis on the label, the thread's
phase, and the `Intent`, on the L side only. The helper lemmas are in:

- `L/InvariantStepLemmas.lean`: values and handles;
- `L/InvariantStepLocal.lean`: one thread-local step;
- `L/InvariantStepFrame.lean`: the invariant under one change of a configuration;
- `L/InvariantStepActions.lean`: the atomic actions of `L/Semantics.lean`.

The clause `handlesExist` needs that no handle enters a program from outside.
The semantics guarantees it: `Expr.lit` takes a `Literal`, and `Request.start`
and `Request.input` carry a `Literal` (`Config.isValidVal_literal`).
-/

namespace DexExpressiveness.L

/-- The next action of one thread keeps the invariant. -/
theorem Invariant.stepThread {program : Program} {config : Config}
    (invariant : Invariant program config) {thread : Thread} (isMember : thread ∈ config.threads)
    {events : List Event} {next : Config}
    (isStep : L.stepThread program config thread = some (events, next)) :
    Invariant program next := by
  have phaseValid := (invariant.handlesExist.1 thread isMember).2.2.2
  have fits := invariant.phasesFitCode thread isMember
  cases isPhase : thread.phase with
  | bootPending =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · cases isStep
      exact (invariant.setPhase isMember .bootRunning trivial (fun _ => Or.inl isPhase)
        (fun on since isBlocked => by cases isBlocked) trivial
        (fun loaded isRunning => by cases isRunning)).stamp
    · cases isStep
  | bootRunning =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · cases isStep
      exact (invariant.setPhase isMember .ready trivial
        (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
        (fun on since isBlocked => by cases isBlocked) trivial
        (fun loaded isRunning => by cases isRunning)).stamp
    · cases isStep
  | ready =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · rename_i isWaitHead
      split at isStep
      · cases isStep
        refine (invariant.setPhase isMember .waitStarted trivial
          (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
          (fun on since isBlocked => by cases isBlocked) ?_
          (fun loaded isRunning => by cases isRunning)).stamp
        show thread.timer.isSome = true ∨ isBlockingHead thread.code = true
        simpa only [Bool.or_eq_true] using isWaitHead
      · cases isStep
    · rename_i notWaitHead
      split at isStep
      · rename_i isHolding
        have noTimer : thread.timer = none ∧ isBlockingHead thread.code = false := by
          simpa only [Bool.or_eq_true, not_or, Bool.not_eq_true, Option.isSome_eq_false_iff,
            Option.isNone_iff_eq_none] using notWaitHead
        have holds : ∀ mutex, requiredMutex thread.core thread.code = some mutex →
            config.mutexOwners.get? mutex = some (some thread.core.tid) := by
          intro mutex isRequired
          simpa only [Config.holdsRequiredMutex, isRequired, decide_eq_true_eq] using isHolding
        split at isStep
        · rename_i isKind
          split at isStep
          · cases isStep
            exact (invariant.setPhase isMember (.running .null) trivial
              (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
              (fun on since isBlocked => by cases isBlocked)
              ⟨noTimer.1, noTimer.2, by rw [isKind]; intro effect isEmit; cases isEmit⟩
              (fun loaded _ => holds)).stamp
          · cases isStep
        · rename_i source isKind
          split at isStep
          · cases isStep
            exact (invariant.setPhase isMember (.running (config.readShared source))
              (invariant.readShared_valid source)
              (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
              (fun on since isBlocked => by cases isBlocked)
              ⟨noTimer.1, noTimer.2, by rw [isKind]; intro effect isEmit; cases isEmit⟩
              (fun loaded _ => holds)).stamp
          · cases isStep
        · rename_i effect isKind
          split at isStep
          · cases isStep
            exact (invariant.setPhase isMember (.emitting effect) trivial
              (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
              (fun on since isBlocked => by cases isBlocked)
              ⟨noTimer.1, noTimer.2, isKind⟩
              (fun loaded isRunning => by cases isRunning)).stamp
          · cases isStep
      · cases isStep
  | running loaded =>
    simp only [L.stepThread, isPhase] at isStep
    rw [isPhase] at phaseValid
    split at isStep
    · cases isStep
      exact (invariant.applyIntent isMember _
        (invariant.isSoundResult_completeStatement isMember loaded phaseValid
          (invariant.runningHoldsRequiredMutex thread isMember loaded isPhase))).stamp
    · cases isStep
  | emitting effect =>
    simp only [L.stepThread, isPhase, Option.some.injEq, Prod.mk.injEq] at isStep
    simp only [Thread.PhaseFitsCode, isPhase] at fits
    rw [← isStep.2]
    exact invariant.setPhase isMember .emitted trivial
      (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
      (fun on since isBlocked => by cases isBlocked)
      ⟨fits.1, fits.2.1, effect, fits.2.2⟩
      (fun loaded isRunning => by cases isRunning)
  | emitted =>
    simp only [L.stepThread, isPhase] at isStep
    simp only [Thread.PhaseFitsCode, isPhase] at fits
    obtain ⟨_, _, effect, isKind⟩ := fits
    split at isStep
    · cases isStep
      exact (invariant.applyIntent isMember _
        (invariant.isSoundResult_completeStatement isMember .null trivial
          (fun mutex isRequired =>
            absurd isKind (beginKind_of_requiredMutex _ _ mutex isRequired effect)))).stamp
    · cases isStep
  | waitStarted =>
    simp only [L.stepThread, isPhase] at isStep
    split at isStep
    · cases isStep
      exact (invariant.registerWait isMember isPhase).stamp
    · cases isStep
  | blocked on since => simp [L.stepThread, isPhase] at isStep
  | woken outcome =>
    simp only [L.stepThread, isPhase] at isStep
    rw [isPhase] at phaseValid
    split at isStep
    · cases isStep
      refine (invariant.setPhase isMember (.resuming outcome) ?_
        (fun isBoot => by rcases isBoot with isBoot | isBoot <;> cases isBoot)
        (fun on since isBlocked => by cases isBlocked) ?_
        (fun loaded isRunning => by cases isRunning)).stamp
      · cases outcome <;> exact phaseValid
      · show thread.timer.isSome = true ∨ isBlockingHead thread.code = true
        simpa only [Thread.PhaseFitsCode, isPhase] using fits
    · cases isStep
  | resuming outcome =>
    simp only [L.stepThread, isPhase] at isStep
    rw [isPhase] at phaseValid
    split at isStep
    · cases isStep
      exact (invariant.applyIntent isMember _
        (invariant.isSoundResult_resume isMember outcome phaseValid)).stamp
    · cases isStep
  | halted => simp [L.stepThread, isPhase] at isStep

/-- Every transition of an L program keeps the invariant. -/
theorem Invariant.step (program : Program) (config : Config) (label : Label)
    (events : List Event) (next : Config) (invariant : Invariant program config)
    (isStep : step program config label = some (events, next)) : Invariant program next := by
  cases label with
  | tick duration =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
      exact invariant.of_sameFields rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl
    · cases isStep
  | request request =>
    cases request with
    | start input =>
      simp only [L.step] at isStep
      split at isStep
      · rename_i isNotStarted
        split at isStep
        · cases isStep
          exact (invariant.begun isNotStarted input).stamp
        · cases isStep
      · cases isStep
    | input chan value =>
      simp only [L.step] at isStep
      split at isStep
      · cases isStep
      · rename_i isRunning
        split at isStep
        · cases isStep
          exact (invariant.deliverInput (by rw [isRunning]; intro isStatus; cases isStatus) chan
            value).stamp
        · cases isStep
      · cases isStep
        exact invariant
  | thread tid =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
    · rename_i current isFound
      exact Invariant.stepThread invariant (Config.mem_of_findThread? isFound).1 isStep
  | fire count =>
    simp only [L.step] at isStep
    split at isStep
    · simp only [Option.map_eq_some_iff, Prod.mk.injEq] at isStep
      obtain ⟨fired, isFired, _, isNext⟩ := isStep
      rw [← isNext]
      exact (Invariant.fireTimers count config fired invariant isFired).stamp
    · cases isStep
  | lateDeliver index =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
      exact invariant.of_sameFields rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl
    · cases isStep
  | lateDrop index =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
      exact invariant.of_sameFields rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl rfl
    · cases isStep

/-- Every finite run keeps the invariant. -/
theorem Invariant.of_exec (program : Program) (labels : List Label) :
    ∀ (config : Config) (events : List Event) (final : Config),
      Invariant program config → (system program).exec config labels = some (events, final) →
      Invariant program final := by
  induction labels with
  | nil =>
    intro config events final invariant isExec
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    exact invariant
  | cons label laterLabels inductionHypothesis =>
    intro config events final invariant isExec
    simp only [TransitionSystem.exec] at isExec
    split at isExec
    · cases isExec
    · rename_i stepEvents next isStep
      split at isExec
      · cases isExec
      · rename_i laterEvents reached isLater
        cases isExec
        exact inductionHypothesis next laterEvents _
          (Invariant.step program config label stepEvents next invariant isStep) isLater

/-- Every reachable configuration satisfies the invariant. -/
theorem Invariant.of_reachable (program : Program) (config : Config)
    (isReachable : (system program).Reachable config) : Invariant program config := by
  obtain ⟨labels, events, isExec⟩ := isReachable
  exact Invariant.of_exec program labels _ events config (Invariant.initial program) isExec

end DexExpressiveness.L
