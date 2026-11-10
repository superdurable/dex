import DexExpressiveness.Failure.Proofs.WPF1Steps

/-!
# WP-F1: [TICK], requests, and [ENGINE-REJECT]

[TICK] and the rejected publications change nothing α reads but the clock.
[PUBLISH] is a normal commit whose change, `applyPublications` then `wake`,
commutes with the relabeling, and it keeps every live invocation current.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

theorem tick_simulated (program : Program σ) (config : Config σ) (duration : Nat)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.tick duration) = some (events, next)) :
    IsStepSimulated program config (.tick duration) events next := by
  simp only [step] at isStep
  split at isStep
  · cases isStep
    unfold IsStepSimulated
    simp [dexActionsOf, TransitionSystem.exec, Dex.system, Dex.step, *, eraseStepEvents]
    rfl
  · cases isStep

theorem engineReject_simulated (program : Program σ) (config : Config σ) (request : Request σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.engineReject request) = some (events, next)) :
    IsStepSimulated program config (.engineReject request) events next := by
  unfold IsStepSimulated
  simp only [dexActionsOf, exec_singleton]
  cases request with
  | startFlow requestId stepType input =>
    simp [step] at isStep
  | publish chan value =>
    simp only [step] at isStep
    split at isStep
    · rename_i isAllowed
      cases isStep
      have hasNoRun : config.run = none := by
        simp only [Bool.and_eq_true, Option.isNone_iff_eq_none] at isAllowed
        exact isAllowed.2
      simp [Dex.step, Request.toDex, abstractConfig, hasNoRun, eraseStepEvents, Obs.toDex]
    · cases isStep

/-- Every live invocation stays current through `applyPublications` then
`wake`. -/
theorem publish_staysCurrent {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (runInvariant : RunInvariant config run)
    (chan : σ.Chan) (value : σ.Val) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) (isLive : entry.2.outcome = .live) :
    (currentKeys ((((bumpedRun run).base.applyPublications [(chan, value)]).wake [chan]))).contains
      entry.1 = true := by
  obtain ⟨record, invocation, isRecord, isId, hasInvocation, isMethod⟩ :=
    live_isCurrent invariant runInvariant entry isMember isLive
  have nonWaiting := nonWaitingAt_of_invocation runInvariant record isRecord invocation
    hasInvocation
  have publishedSteps : ((bumpedRun run).base.applyPublications [(chan, value)]).steps =
      run.base.steps := (Dex.Run.applyPublications_frame _ _).1
  have isKept := (keeps_foldl_evalStep
    ((((bumpedRun run).base.applyPublications [(chan, value)])).waitersOn [chan])
    ((bumpedRun run).base.applyPublications [(chan, value)]) record.id
    (fun other isOther => nonWaiting other (by rw [publishedSteps] at isOther; exact isOther))).2
    record (by rw [publishedSteps]; exact isRecord) rfl
  have isCurrent : entry.1 ∈ currentKeys ((((bumpedRun run).base.applyPublications
      [(chan, value)]).wake [chan])) := by
    apply (mem_currentKeys _ _).mpr
    refine ⟨record, invocation, isKept, hasInvocation, ?_⟩
    rw [isId, isMethod]
  simpa using isCurrent

theorem publish_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (chan : σ.Chan) (value : σ.Val)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.request (.publish chan value)) = some (events, next)) :
    IsStepSimulated program config (.request (.publish chan value)) events next := by
  unfold IsStepSimulated
  simp only [dexActionsOf, Request.toDex, exec_singleton]
  simp only [step] at isStep
  split at isStep
  · split at isStep
    · rename_i hasNoRun
      cases isStep
      simp [Dex.step, abstractConfig, hasNoRun, eraseStepEvents, Obs.toDex]
    · rename_i run hasRun
      have runInvariant := invariant.runInvariant run hasRun
      have absRun : (abstractConfig config).run = some (Run.abstract config.ghost run) := by
        simp [abstractConfig, hasRun]
      split at isStep
      · rename_i status isClosed
        cases isStep
        have absClosed : (Run.abstract config.ghost run).status = .closed status := isClosed
        simp [Dex.step, absRun, absClosed, eraseStepEvents, Obs.toDex]
      · rename_i isRunning
        split at isStep
        · rename_i canCommit
          simp only [Option.some.injEq] at isStep
          have absRunning : (Run.abstract config.ghost run).status = .running := isRunning
          have absCommit := canCommit_abstract invariant canCommit
          simp only [Dex.step, absRun, absRunning, absCommit, ↓reduceIte, Dex.Config.commit]
          have published : ∀ base : Dex.Run σ,
              (base.applyPublications [(chan, value)]).steps = base.steps :=
            fun base => (Dex.Run.applyPublications_frame _ _).1
          obtain ⟨isEvents, isConfig⟩ := commit_simulated config run runInvariant none
            [Obs.pub chan value]
            (fun current =>
              { current with base := (current.base.applyPublications [(chan, value)]).wake [chan] })
            (fun base => (base.applyPublications [(chan, value)]).wake [chan]) rfl
            (fun _ => rfl)
            (by
              rw [applyPublications_relabelRun, wake_relabelRun]
              · exact (isWaitFresh_bumpedRun runInvariant).of_steps (published _)
              · exact (isOrderKept_bumpedRun runInvariant).of_steps (published _))
            (by
              unfold Dex.Run.wake
              rw [Dex.Run.seq_foldl_evalStep, (Dex.Run.applyPublications_frame _ _).2.2.2.1]
              rfl)
            (by
              rw [ids_wake, published]
              exact runInvariant.stepIdsNodup)
          obtain ⟨isEventsEq, isNextEq⟩ := Prod.ext_iff.mp isStep
          subst isEventsEq isNextEq
          rw [isEvents, isConfig]
          obtain ⟨extra, isInvocations, isFresh⟩ := reconcile_invocations
            (countedGhost config.ghost (run.base.seq + 1) config.clock) none
            (((bumpedRun run).base.applyPublications [(chan, value)]).wake [chan])
          have tasksEq : (config.commitRun run true none [Obs.pub chan value]
              (fun current =>
                { current with
                  base := (current.base.applyPublications [(chan, value)]).wake [chan] })).2.virtualTasks =
              config.virtualTasks := by
            rw [commitRun_normal]
            refine virtualTasks_of_extra config _ ?_ extra ?_ ?_
            · rfl
            · rw [← map_markHistory_of_current config.ghost.invocations none
                (currentKeys (((bumpedRun run).base.applyPublications [(chan, value)]).wake [chan]))
                (fun entry isMember isLive =>
                  publish_staysCurrent invariant runInvariant chan value entry isMember isLive)]
              exact isInvocations
            · intro entry isMember
              rw [isFresh entry isMember]
              rfl
          rw [tasksEq]
          have absSeq : (Run.abstract config.ghost run).seq = config.ghost.normalCount := rfl
          simp [eraseStepEvents, Obs.toDex, absRunning, absSeq]
          exact ⟨rfl, rfl, rfl⟩
        · cases isStep
  · cases isStep

end DexExpressiveness.DexF.WPF1
