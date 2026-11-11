import DexExpressiveness.Failure.Composite.WPF6Run
import DexExpressiveness.Failure.Composite.WPF6Sequences
import DexExpressiveness.Core.BlockRun

/-!
# WP-F6: liveness, the forward inclusion

α maps a fair DexF run to a Dex run given in blocks: block `index` is the
transition map of DexF step `index` (`dexActionsOf`), which runs from α of
the configuration before the step to α of the one after it
(`step_simulated`). Ticks are infinitely many, so the blocks flatten into an
infinite Dex run. It attempts `w↓`, its trace is `erase(t)`, its marker is
the DexF marker (`isProgress_eq_dexProgress`), and it is fair.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## The Dex run in blocks -/

/-- The Dex run in blocks that α maps a DexF run to. -/
def wpf6ForwardBlocks (program : Program σ) (run : InfiniteRun (Config σ) (Action σ)) :
    BlockRun (Dex.Config σ) (Dex.Action σ) (Dex.Obs σ) :=
  { states := fun index => abstractConfig (run.states index)
    blocks := fun index => dexActionsOf program (run.states index) (run.labels index)
    events := fun index =>
      eraseStepEvents (run.states index) ((system program).eventsAt run index) }

/-- Each step of a DexF run produces its `eventsAt`. -/
theorem wpf6_step_eventsAt (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (index : Nat) :
    step program (run.states index) (run.labels index) =
      some ((system program).eventsAt run index, run.states (index + 1)) :=
  (system program).step_eq_eventsAt run isRun.2 index

/-- α maps a DexF run to a Dex run in blocks. -/
theorem wpf6_forward_isBlockRun (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (hasNoExhaustion : RunHasNoExhaustion run) :
    (Dex.system program.failureFreeFlow).IsBlockRun (wpf6ForwardBlocks program run) := by
  refine ⟨?_, fun index => ?_⟩
  · show abstractConfig (run.states 0) = Dex.Config.initial σ
    rw [isRun.1]
    exact wpf6_abstractConfig_initial
  · exact step_simulated program ignoresAttemptContext (run.states index)
      (wpf6_invariant_at program ignoresAttemptContext run isRun index) (run.labels index)
      (hasNoExhaustion index) _ _ (wpf6_step_eventsAt program run isRun index)

/-- The clock of a DexF run never decreases. -/
theorem wpf6_clock_mono (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (first : Nat) :
    ∀ extra, (run.states first).clock ≤ (run.states (first + extra)).clock := by
  intro extra
  induction extra with
  | zero => exact Nat.le_refl _
  | succ extra inductionHypothesis =>
    obtain ⟨events, isStep⟩ := isRun.2 (first + extra)
    exact Nat.le_trans inductionHypothesis (wpf6_clock_step program _ _ events _ isStep).1

/-- A fair DexF run has infinitely many ticks, so infinitely many blocks are
not empty. -/
theorem wpf6_forward_hasInfinitelyManySteps (program : Program σ)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (timeDiverges : Fairness.TimeDiverges run) :
    (wpf6ForwardBlocks program run).HasInfinitelyManySteps := by
  intro bound
  apply Classical.byContradiction
  intro noStep
  have isNotTick : ∀ extra duration, run.labels (bound + extra) ≠ .tick duration := by
    intro extra duration isTick
    apply noStep
    refine ⟨bound + extra, Nat.le_add_right _ _, ?_⟩
    show dexActionsOf program (run.states (bound + extra)) (run.labels (bound + extra)) ≠ []
    rw [isTick]
    simp [dexActionsOf]
  have isConstant : ∀ extra, (run.states (bound + extra)).clock = (run.states bound).clock := by
    intro extra
    induction extra with
    | zero => rfl
    | succ extra inductionHypothesis =>
      obtain ⟨events, isStep⟩ := isRun.2 (bound + extra)
      rw [← Nat.add_assoc, (wpf6_clock_step program _ _ events _ isStep).2 (isNotTick extra),
        inductionHypothesis]
  obtain ⟨index, isAbove⟩ := timeDiverges (run.states bound).clock
  by_cases isBefore : index ≤ bound
  · obtain ⟨extra, isSum⟩ := Nat.exists_eq_add_of_le isBefore
    have isMono := wpf6_clock_mono program run isRun index extra
    rw [← isSum] at isMono
    omega
  · obtain ⟨extra, isSum⟩ := Nat.exists_eq_add_of_le (Nat.le_of_lt (Nat.lt_of_not_le isBefore))
    rw [isSum, isConstant] at isAbove
    exact Nat.lt_irrefl _ isAbove

/-- A property of the flattened run that fails at infinitely many block
boundaries eventually fails after every position. -/
theorem wpf6_eventuallyFails_of_boundaries {State Label Event : Type}
    {blockRun : BlockRun State Label Event} {run : InfiniteRun State Label}
    {boundary : Nat → Nat} (isFlattened : blockRun.IsFlattenedTo run boundary)
    (isInfinite : blockRun.HasInfinitelyManySteps) (holdsAt : Nat → Prop)
    (failsOften : ∀ start, ∃ index, start ≤ index ∧ ¬ holdsAt (boundary index)) :
    EventuallyFails holdsAt := by
  intro start _
  obtain ⟨first, isFirst⟩ := isFlattened.exists_boundary_ge isInfinite start
  obtain ⟨index, isLater, isFailing⟩ := failsOften first
  exact ⟨boundary index, Nat.le_trans isFirst (isFlattened.boundary_mono first index isLater),
    isFailing⟩


/-! ## The word: the Dex run attempts `w↓` -/

/-- [FLOW-START] needs no run and starts one. -/
theorem wpf6_step_startFlow_run (program : Program σ) (config : Config σ) (requestId : Nat)
    (stepType : σ.StepType) (input : σ.Val) (events : List (Obs σ)) (next : Config σ)
    (isStep :
      step program config (.request (.startFlow requestId stepType input)) = some (events, next)) :
    config.run = none ∧ next.run.isSome = true := by
  simp only [step] at isStep
  cases hasRun : config.run with
  | some run => simp [hasRun] at isStep
  | none =>
    simp only [hasRun] at isStep
    refine ⟨rfl, ?_⟩
    split at isStep
    · simp only [Config.commitRun, Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨-, isNext⟩ := isStep
      rw [← isNext]
      rfl
    · cases isStep

/-- [START-RETRY] needs a run. -/
theorem wpf6_step_startRetry_run (program : Program σ) (config : Config σ) (requestId : Nat)
    (stepType : σ.StepType) (input : σ.Val) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.startRetry requestId stepType input) = some (events, next)) :
    config.run.isSome = true := by
  simp only [step] at isStep
  cases hasRun : config.run with
  | none => simp [hasRun] at isStep
  | some run => rfl

/-- [ENGINE-REJECT] needs no run and rejects only a publication. -/
theorem wpf6_step_engineReject_run (program : Program σ) (config : Config σ)
    (request : Request σ) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.engineReject request) = some (events, next)) :
    config.run = none ∧ ∃ chan value, request = .publish chan value := by
  cases request with
  | startFlow requestId stepType input => simp [step] at isStep
  | publish chan value =>
    simp only [step] at isStep
    split at isStep
    · rename_i isEnabled
      simp only [Bool.and_eq_true, Option.isNone_iff_eq_none] at isEnabled
      exact ⟨isEnabled.2, chan, value, rfl⟩
    · cases isStep

/-- The Dex actions of a label that attempts no request attempt none. -/
theorem wpf6_dexRequests_eq_nil (program : Program σ) (config : Config σ) (action : Action σ)
    (isNone : action.requestOf = none) :
    (dexActionsOf program config action).filterMap
      (Dex.system program.failureFreeFlow).requestOf = [] := by
  have isAbandonList : ∀ taskIds : List Nat,
      (taskIds.map Dex.Action.workerAbandon).filterMap
        (Dex.system program.failureFreeFlow).requestOf = [] := by
    intro taskIds
    induction taskIds with
    | nil => rfl
    | cons taskId laterIds inductionHypothesis =>
      simp only [List.map_cons, List.filterMap_cons]
      exact inductionHypothesis
  have isOnVirtual : ∀ (taskId : Nat) (make : Nat → Dex.Action σ),
      (∀ virtualTaskId request, make virtualTaskId ≠ .request request) →
      (config.onVirtualTask taskId make).filterMap
        (Dex.system program.failureFreeFlow).requestOf = [] := by
    intro taskId make isNotRequest
    unfold Config.onVirtualTask
    split
    · rename_i virtualTaskId _
      simp only [List.filterMap_cons, List.filterMap_nil]
      cases isMade : make virtualTaskId with
      | request request => exact absurd isMade (isNotRequest virtualTaskId request)
      | _ => rfl
    · rfl
  cases action with
  | tick duration => rfl
  | request request => simp [Action.requestOf] at isNone
  | startRetry requestId stepType input => simp [Action.requestOf] at isNone
  | engineReject request => simp [Action.requestOf] at isNone
  | stepStart stepId isSnapshotReused => cases isSnapshotReused <;> rfl
  | workerRun taskId isFirst =>
    cases isFirst
    · rfl
    · exact isOnVirtual taskId _ (by intro _ _ isRequest; cases isRequest)
  | workerEffect taskId isFirst =>
    cases isFirst
    · rfl
    · exact isOnVirtual taskId _ (by intro _ _ isRequest; cases isRequest)
  | workerReturn taskId isFirst =>
    cases isFirst
    · rfl
    · exact isOnVirtual taskId _ (by intro _ _ isRequest; cases isRequest)
  | workerAbandon taskId isLast =>
    cases isLast
    · rfl
    · exact isOnVirtual taskId _ (by intro _ _ isRequest; cases isRequest)
  | resultStale taskId isLast =>
    cases isLast
    · rfl
    · exact isOnVirtual taskId _ (by intro _ _ isRequest; cases isRequest)
  | stepComplete taskId =>
    show (config.onVirtualTask taskId Dex.Action.stepComplete ++ _).filterMap _ = []
    rw [List.filterMap_append, isOnVirtual taskId _ (by intro _ _ isRequest; cases isRequest)]
    split
    · exact isAbandonList _
    · rfl
  | timerFire count isWait => cases isWait <;> rfl
  | attemptFailed stepId decision => rfl
  | attemptReturned stepId => rfl
  | attemptHeartbeat stepId value => rfl
  | crash => rfl
  | recover => rfl
  | recoverInterrupt interrupt decision => rfl
  | internalCommit => rfl

/-- The number of requests a DexF run attempts before transition `index`. -/
def wpf6RequestCount (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (index : Nat) : Nat :=
  (concatUpTo ((system program).requestsAt run) index).length

/-- One transition attempts its label's request, if any. -/
theorem wpf6_requestCount_succ (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (index : Nat) :
    wpf6RequestCount program run (index + 1) =
      wpf6RequestCount program run index + ((run.labels index).requestOf).toList.length := by
  simp only [wpf6RequestCount, concatUpTo, List.length_append]
  rfl

/-- Before its first request a DexF run has no run. -/
theorem wpf6_run_none_of_noRequest (program : Program σ)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run) :
    ∀ index, wpf6RequestCount program run index = 0 → (run.states index).run = none := by
  intro index
  induction index with
  | zero =>
    intro _
    rw [isRun.1]
    rfl
  | succ index inductionHypothesis =>
    intro isZero
    rw [wpf6_requestCount_succ] at isZero
    have isNoRequest : (run.labels index).requestOf = none := by
      cases isRequest : (run.labels index).requestOf with
      | none => rfl
      | some request => rw [isRequest] at isZero; simp at isZero
    obtain ⟨events, isStep⟩ := isRun.2 index
    rcases (wpf6_run_step program _ _ events _ isStep).2
        (inductionHypothesis (by omega)) with isNone | ⟨requestId, stepType, input, isStart⟩
    · exact isNone
    · rw [isStart] at isNoRequest
      cases isNoRequest

/-- The request at position `wpf6RequestCount index` of the word is the
request of transition `index`. -/
theorem wpf6_word_at_requestCount (program : Program σ)
    (run : InfiniteRun (Config σ) (Action σ)) (word : Sequence (Request σ))
    (isRunFor : (system program).IsRunFor run word) (index : Nat) (request : Request σ)
    (isRequest : (run.labels index).requestOf = some request) :
    word (wpf6RequestCount program run index) = some request := by
  rw [wpf6_isConcatenation_getElem _ word isRunFor (index + 1)]
  · show (concatUpTo ((system program).requestsAt run) index ++
        ((system program).requestOf (run.labels index)).toList)[_]? = _
    show (_ ++ ((run.labels index).requestOf).toList)[_]? = _
    rw [isRequest, wpf6RequestCount, List.getElem?_append_right (Nat.le_refl _), Nat.sub_self]
    rfl
  · rw [← wpf6RequestCount, wpf6_requestCount_succ, isRequest]
    simp

/-- After its first request a DexF run for an admissible word has a run: the
first request is the start. -/
theorem wpf6_run_some_of_request (program : Program σ)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (environment : Dex.Environment σ) (word : Sequence (Request σ))
    (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (isRunFor : (system program).IsRunFor run word) :
    ∀ index, 0 < wpf6RequestCount program run index → (run.states index).run.isSome = true := by
  intro index
  induction index with
  | zero => intro isPositive; simp [wpf6RequestCount, concatUpTo] at isPositive
  | succ index inductionHypothesis =>
    intro isPositive
    obtain ⟨events, isStep⟩ := isRun.2 index
    by_cases wasPositive : 0 < wpf6RequestCount program run index
    · exact (wpf6_run_step program _ _ events _ isStep).1 (inductionHypothesis wasPositive)
    · have isZero : wpf6RequestCount program run index = 0 := by omega
      have hadNoRun := wpf6_run_none_of_noRequest program run isRun index isZero
      rw [wpf6_requestCount_succ, isZero] at isPositive
      cases isRequest : (run.labels index).requestOf with
      | none => rw [isRequest] at isPositive; simp at isPositive
      | some request =>
        have isFirst := wpf6_word_at_requestCount program run word isRunFor index request isRequest
        rw [isZero] at isFirst
        obtain ⟨requestId, input, _, isStart⟩ := isAdmissible.1
        rw [isStart] at isFirst
        cases isFirst
        cases isLabel : run.labels index with
        | request label =>
          rw [isLabel] at isRequest isStep
          simp only [Action.requestOf, Option.some.injEq] at isRequest
          rw [isRequest] at isStep
          exact (wpf6_step_startFlow_run program _ _ _ _ events _ isStep).2
        | startRetry requestId stepType input =>
          rw [isLabel] at isStep
          have hasRun := wpf6_step_startRetry_run program _ requestId stepType input events _ isStep
          rw [hadNoRun] at hasRun
          cases hasRun
        | engineReject label =>
          rw [isLabel] at isRequest isStep
          simp only [Action.requestOf, Option.some.injEq] at isRequest
          obtain ⟨_, chan, value, isPublish⟩ :=
            wpf6_step_engineReject_run program _ _ events _ isStep
          rw [isRequest] at isPublish
          cases isPublish
        | _ => rw [isLabel] at isRequest; simp [Action.requestOf] at isRequest

/-- An admissible word stays absent from its first absent position on. -/
theorem wpf6_word_none_of_le (environment : Dex.Environment σ) (word : Sequence (Request σ))
    (isAdmissible : IsAdmissibleOutcomeWord environment word) (first : Nat)
    (isNone : word first = none) : ∀ extra, word (first + extra) = none := by
  intro extra
  induction extra with
  | zero => exact isNone
  | succ extra inductionHypothesis =>
    rw [← Nat.add_assoc]
    exact isAdmissible.2.2 _ inductionHypothesis

/-- The Dex requests of the blocks are `w↓`, block by block: the first
`index` blocks attempt the `w↓` chunks of the first requests of the word. -/
theorem wpf6_forward_word_agree (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (environment : Dex.Environment σ)
    (word : Sequence (Request σ)) (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (isRunFor : (system program).IsRunFor run word) :
    ∀ index, concatUpTo (fun index => ((wpf6ForwardBlocks program run).blocks index).filterMap
        (Dex.system program.failureFreeFlow).requestOf) index =
      concatUpTo (retryErasureChunk word) (wpf6RequestCount program run index) := by
  intro index
  induction index with
  | zero => rfl
  | succ index inductionHypothesis =>
    rw [concatUpTo, inductionHypothesis, wpf6_requestCount_succ]
    show _ ++ (dexActionsOf program (run.states index) (run.labels index)).filterMap _ = _
    cases isRequest : (run.labels index).requestOf with
    | none =>
      rw [wpf6_dexRequests_eq_nil program _ _ isRequest]
      simp
    | some request =>
      simp only [Option.toList_some, List.length_singleton]
      rw [concatUpTo]
      congr 1
      have isWord := wpf6_word_at_requestCount program run word isRunFor index request isRequest
      obtain ⟨events, isStep⟩ := isRun.2 index
      unfold retryErasureChunk
      rw [isWord]
      cases isCount : wpf6RequestCount program run index with
      | zero =>
        have hadNoRun := wpf6_run_none_of_noRequest program run isRun index isCount
        cases isLabel : run.labels index with
        | request label =>
          rw [isLabel] at isRequest
          simp only [Action.requestOf, Option.some.injEq] at isRequest
          rw [isRequest]
          rfl
        | startRetry requestId stepType input =>
          rw [isLabel] at isStep
          have hasRun := wpf6_step_startRetry_run program _ requestId stepType input events _ isStep
          rw [hadNoRun] at hasRun
          cases hasRun
        | engineReject label =>
          rw [isLabel] at isRequest
          simp only [Action.requestOf, Option.some.injEq] at isRequest
          rw [isRequest]
          rfl
        | _ => rw [isLabel] at isRequest; simp [Action.requestOf] at isRequest
      | succ position =>
        have hasRun := wpf6_run_some_of_request program run isRun environment word isAdmissible
          isRunFor index (by omega)
        obtain ⟨requestId, input, _, isStart⟩ := isAdmissible.1
        rw [isStart]
        cases isLabel : run.labels index with
        | request label =>
          rw [isLabel] at isRequest isStep
          simp only [Action.requestOf, Option.some.injEq] at isRequest
          subst isRequest
          cases label with
          | startFlow laterId stepType laterInput =>
            have hadNoRun := (wpf6_step_startFlow_run program _ _ _ _ events _ isStep).1
            rw [hadNoRun] at hasRun
            cases hasRun
          | publish chan value => rfl
        | startRetry laterId stepType laterInput =>
          rw [isLabel] at isRequest
          simp only [Action.requestOf, Option.some.injEq] at isRequest
          subst isRequest
          have isLater := isAdmissible.2.1 position _ (by rw [← isCount]; exact isWord)
          rcases isLater with ⟨first, isFirst, isRetry⟩ | ⟨chan, value, isPublish, _⟩
          · rw [isStart] at isFirst
            cases isFirst
            show [] = if _ then [] else _
            rw [isRetry]
            rfl
          · cases isPublish
        | engineReject label =>
          rw [isLabel] at isStep
          have hadNoRun := (wpf6_step_engineReject_run program _ _ events _ isStep).1
          rw [hadNoRun] at hasRun
          cases hasRun
        | _ => rw [isLabel] at isRequest; simp [Action.requestOf] at isRequest

/-- Empty chunks from `start` on add nothing. -/
theorem wpf6_concatUpTo_of_empty {α : Type} (chunks : Nat → List α) (start : Nat)
    (isEmpty : ∀ position, start ≤ position → chunks position = []) :
    ∀ extra, concatUpTo chunks (start + extra) = concatUpTo chunks start := by
  intro extra
  induction extra with
  | zero => rfl
  | succ extra inductionHypothesis =>
    rw [← Nat.add_assoc, concatUpTo, inductionHypothesis, isEmpty _ (Nat.le_add_right _ _)]
    simp

/-- The finite concatenations of the blocks reach every finite concatenation
of the `w↓` chunks in length. -/
theorem wpf6_forward_word_covers (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (environment : Dex.Environment σ) (word : Sequence (Request σ))
    (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (isRunFor : (system program).IsRunFor run word) (count : Nat) :
    ∃ index, (concatUpTo (retryErasureChunk word) count).length ≤
      (concatUpTo (retryErasureChunk word) (wpf6RequestCount program run index)).length := by
  have arrives := (system program).requestsArrive_of_isRunFor run word isRunFor
  have isAttempted : ∀ position, (word position).isSome = true →
      ∃ index, position < wpf6RequestCount program run index := arrives
  have endsAt : ∀ position, word position = none →
      ∃ index, ∀ later, wpf6RequestCount program run index ≤ later → word later = none := by
    intro position
    induction position with
    | zero =>
      intro isNone
      refine ⟨0, fun later _ => ?_⟩
      have isLater := wpf6_word_none_of_le environment word isAdmissible 0 isNone later
      rwa [Nat.zero_add] at isLater
    | succ position inductionHypothesis =>
      intro isNone
      cases isPrevious : word position with
      | none => exact inductionHypothesis isPrevious
      | some request =>
        obtain ⟨index, isAfter⟩ := isAttempted position (by rw [isPrevious]; rfl)
        refine ⟨index, fun later isLater => ?_⟩
        obtain ⟨extra, isSum⟩ := Nat.exists_eq_add_of_le (show position + 1 ≤ later by omega)
        rw [isSum]
        exact wpf6_word_none_of_le environment word isAdmissible _ isNone extra
  by_cases isReached : ∃ index, count ≤ wpf6RequestCount program run index
  · obtain ⟨index, isAfter⟩ := isReached
    exact ⟨index, concatUpTo_length_le _ isAfter⟩
  · cases count with
    | zero => exact ⟨0, by simp [concatUpTo]⟩
    | succ position =>
      have isNone : word position = none := by
        cases isPresent : word position with
        | none => rfl
        | some request =>
          obtain ⟨index, isAfter⟩ := isAttempted position (by rw [isPresent]; rfl)
          exact absurd ⟨index, isAfter⟩ isReached
      obtain ⟨index, isEnded⟩ := endsAt position isNone
      refine ⟨index, ?_⟩
      have isBelow : wpf6RequestCount program run index < position + 1 :=
        Nat.lt_of_not_le (fun isAbove => isReached ⟨index, isAbove⟩)
      obtain ⟨extra, isSum⟩ := Nat.exists_eq_add_of_le (Nat.le_of_lt isBelow)
      rw [isSum, wpf6_concatUpTo_of_empty _ _ (fun later isLater => by
        unfold retryErasureChunk
        rw [isEnded later isLater]) extra]
      exact Nat.le_refl _

/-- **The word.** The Dex run in blocks attempts `w↓`. -/
theorem wpf6_forward_isRunFor (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (environment : Dex.Environment σ)
    (word : Sequence (Request σ)) (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (erasedWord : Sequence (Dex.Request σ)) (isErasure : IsRetryErasure word erasedWord)
    (isRunFor : (system program).IsRunFor run word) :
    IsConcatenation (fun index => ((wpf6ForwardBlocks program run).blocks index).filterMap
      (Dex.system program.failureFreeFlow).requestOf) erasedWord :=
  wpf6_isConcatenation_of_regroup (retryErasureChunk word) _ (wpf6RequestCount program run)
    (wpf6_forward_word_agree program run isRun environment word isAdmissible isRunFor)
    (wpf6_forward_word_covers program run environment word isAdmissible isRunFor) erasedWord
    isErasure


/-! ## The trace: the Dex run has the trace `erase(t)` -/

/-- The first `index` transitions of a DexF run are a finite run from the
initial configuration. -/
theorem wpf6_exec_prefix (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (isRun : (system program).IsRun run) (index : Nat) :
    (system program).exec (system program).init ((List.range index).map run.labels) =
      some (concatUpTo ((system program).eventsAt run) index, run.states index) := by
  have isRange := (system program).exec_range run isRun.2 0 index
  simp only [Nat.zero_add] at isRange
  rw [← isRun.1]
  exact isRange

/-- The ghost `h` and the trace agree at every configuration of a DexF run. -/
theorem wpf6_effectHistory_at (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (index : Nat) :
    HasEffectHistory program (run.states index)
      (concatUpTo ((system program).eventsAt run) index) :=
  effectHistory_of_exec program ignoresAttemptContext _ _ _
    (wpf6_exec_prefix program run isRun index)

/-- The position of the first event of transition `index` in the trace. -/
def wpf6EventCount (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (index : Nat) : Nat :=
  (concatUpTo ((system program).eventsAt run) index).length

/-- The event at offset `offset` of transition `index` is the trace's event at
its position. -/
theorem wpf6_trace_at (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation ((system program).eventsAt run) failureTrace)
    (index offset : Nat) (event : Obs σ)
    (isEvent : ((system program).eventsAt run index)[offset]? = some event) :
    failureTrace (wpf6EventCount program run index + offset) = some event := by
  have isInside : offset < ((system program).eventsAt run index).length := by
    rcases List.getElem?_eq_some_iff.mp isEvent with ⟨isInside, _⟩
    exact isInside
  rw [wpf6_isConcatenation_getElem _ failureTrace isTrace (index + 1)]
  · show (concatUpTo _ index ++ _)[_]? = _
    rw [wpf6EventCount, List.getElem?_append_right (Nat.le_add_right _ _), Nat.add_sub_cancel_left]
    exact isEvent
  · show _ < (concatUpTo _ index ++ _).length
    rw [List.length_append]
    unfold wpf6EventCount
    omega

/-- An event of the trace before transition `index` is an event of the first
`index` transitions. -/
theorem wpf6_trace_before (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation ((system program).eventsAt run) failureTrace)
    (index position : Nat) (isBefore : position < wpf6EventCount program run index)
    (event : Obs σ) (isEvent : failureTrace position = some event) :
    event ∈ concatUpTo ((system program).eventsAt run) index := by
  rw [wpf6_isConcatenation_getElem _ failureTrace isTrace index position isBefore] at isEvent
  exact List.mem_iff_getElem?.mpr ⟨position, isEvent⟩

/-- The event at offset `offset` of transition `index` is a repeated `fx` of the
trace exactly when its ghost index is at most `h` before the transition. -/
theorem wpf6_isRepeatedAt_iff (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation ((system program).eventsAt run) failureTrace)
    (index offset : Nat) (event : Obs σ)
    (isEvent : ((system program).eventsAt run index)[offset]? = some event) :
    IsRepeatedAt failureTrace (wpf6EventCount program run index + offset) ↔
      ∃ stepId method effect effectIndex, event = .fx stepId method effect effectIndex ∧
        effectIndex ≤ (run.states index).ghost.emittedCountOf (stepId, method) := by
  have isAtPosition := wpf6_trace_at program run failureTrace isTrace index offset event isEvent
  have isMember : event ∈ (system program).eventsAt run index :=
    List.mem_iff_getElem?.mpr ⟨offset, isEvent⟩
  have isInside : offset < ((system program).eventsAt run index).length := by
    rcases List.getElem?_eq_some_iff.mp isEvent with ⟨isInside, _⟩
    exact isInside
  have history := wpf6_effectHistory_at program ignoresAttemptContext run isRun index
  constructor
  · intro isRepeated
    obtain ⟨earlier, stepId, method, firstEffect, effect, effectIndex, isEarlier, isFirst,
      isAt⟩ := isRepeated
    rw [isAtPosition] at isAt
    cases isAt
    refine ⟨stepId, method, effect, effectIndex, rfl, ?_⟩
    by_cases isBefore : earlier < wpf6EventCount program run index
    · exact (history.1 stepId method firstEffect effectIndex
        (wpf6_trace_before program run failureTrace isTrace index earlier isBefore _ isFirst)).2.1
    · obtain ⟨earlierOffset, isSum⟩ :=
        Nat.exists_eq_add_of_le (Nat.le_of_not_lt isBefore)
      have isEarlierOffset : earlierOffset < offset := by omega
      have isEarlierEvent :
          ((system program).eventsAt run index)[earlierOffset]? =
            some (.fx stepId method firstEffect effectIndex) := by
        cases isElement : ((system program).eventsAt run index)[earlierOffset]? with
        | none =>
          rw [List.getElem?_eq_none_iff] at isElement
          omega
        | some other =>
          have isOther := wpf6_trace_at program run failureTrace isTrace index earlierOffset other
            isElement
          rw [← isSum, isFirst] at isOther
          rw [isOther]
      rcases wpf6_events_step program _ _ _ _ (wpf6_step_eventsAt program run isRun index) with
        isShort | noEffect
      · omega
      · exact absurd isMember (noEffect stepId method effect effectIndex)
  · intro isRepeatedIndex
    obtain ⟨stepId, method, effect, effectIndex, isFx, isAtMost⟩ := isRepeatedIndex
    subst isFx
    have nextHistory := wpf6_effectHistory_at program ignoresAttemptContext run isRun (index + 1)
    have isPositive := (nextHistory.1 stepId method effect effectIndex (by
      show _ ∈ concatUpTo _ index ++ _
      exact List.mem_append_right _ isMember)).1
    obtain ⟨firstEffect, isFirstMember⟩ := history.2 stepId method effectIndex isPositive isAtMost
    obtain ⟨earlier, isEarlier⟩ := List.mem_iff_getElem?.mp isFirstMember
    have isEarlierInside : earlier < wpf6EventCount program run index := by
      rcases List.getElem?_eq_some_iff.mp isEarlier with ⟨isInside, _⟩
      exact isInside
    refine ⟨earlier, stepId, method, firstEffect, effect, effectIndex, by omega, ?_, isAtPosition⟩
    rw [wpf6_isConcatenation_getElem _ failureTrace isTrace index earlier isEarlierInside]
    exact isEarlier

/-- The erasure of one event before a transition, as `eraseStepEvents` does it. -/
def wpf6EraseEvent (config : Config σ) (event : Obs σ) : Option (Dex.Obs σ) :=
  match event with
  | .fx step method effect index =>
    if index ≤ config.ghost.emittedCountOf (step, method) then none
    else some (.fx step method effect)
  | other => some other.toDex

/-- `eraseStepEvents` erases event by event. -/
theorem wpf6_eraseStepEvents_eq (config : Config σ) (events : List (Obs σ)) :
    eraseStepEvents config events = events.filterMap (wpf6EraseEvent config) := by
  unfold eraseStepEvents
  congr 1

/-- The concatenation of per-offset chunks that agree with a `filterMap` is the
`filterMap`. -/
theorem wpf6_concatUpTo_eq_filterMap {α β : Type} (elements : List α) (transform : α → Option β)
    (chunks : Nat → List β)
    (isChunk : ∀ offset element, elements[offset]? = some element →
      chunks offset = (transform element).toList) :
    ∀ count, count ≤ elements.length →
      concatUpTo chunks count = (elements.take count).filterMap transform := by
  intro count
  induction count with
  | zero => intro _; rfl
  | succ count inductionHypothesis =>
    intro isInside
    have isElement : elements[count]? = some elements[count] := List.getElem?_eq_getElem isInside
    rw [concatUpTo, inductionHypothesis (by omega), List.take_add_one, isElement,
      List.filterMap_append, isChunk count _ isElement]
    simp only [Option.toList_some, List.filterMap_cons, List.filterMap_nil]
    cases transform elements[count] <;> rfl

/-- The `erase` chunks of the trace positions of transition `index` are the
erased events of the transition. -/
theorem wpf6_forward_trace_agree (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation ((system program).eventsAt run) failureTrace)
    (chunks : Nat → List (Dex.Obs σ))
    (isChunks : ∀ position,
      (IsRepeatedAt failureTrace position → chunks position = []) ∧
      (¬ IsRepeatedAt failureTrace position →
        chunks position = ((failureTrace position).map Obs.toDex).toList)) :
    ∀ index, concatUpTo (fun index => (wpf6ForwardBlocks program run).events index) index =
      concatUpTo chunks (wpf6EventCount program run index) := by
  intro index
  induction index with
  | zero => rfl
  | succ index inductionHypothesis =>
    have isCount : wpf6EventCount program run (index + 1) =
        wpf6EventCount program run index + ((system program).eventsAt run index).length := by
      simp only [wpf6EventCount, concatUpTo, List.length_append]
    rw [concatUpTo, inductionHypothesis, isCount, concatUpTo_add]
    congr 1
    show eraseStepEvents (run.states index) ((system program).eventsAt run index) = _
    rw [wpf6_eraseStepEvents_eq, wpf6_concatUpTo_eq_filterMap _ _ _ (fun offset event isEvent => ?_)
      _ (Nat.le_refl _), List.take_length]
    have isAt := wpf6_trace_at program run failureTrace isTrace index offset event isEvent
    have isIff := wpf6_isRepeatedAt_iff program ignoresAttemptContext run isRun failureTrace
      isTrace index offset event isEvent
    by_cases isRepeated : IsRepeatedAt failureTrace (wpf6EventCount program run index + offset)
    · rw [(isChunks _).1 isRepeated]
      obtain ⟨stepId, method, effect, effectIndex, isFx, isAtMost⟩ := isIff.mp isRepeated
      subst isFx
      simp [wpf6EraseEvent, isAtMost]
    · rw [(isChunks _).2 isRepeated, isAt]
      cases event with
      | fx stepId method effect effectIndex =>
        have isAbove : ¬ effectIndex ≤ (run.states index).ghost.emittedCountOf (stepId, method) :=
          fun isAtMost => isRepeated (isIff.mpr ⟨stepId, method, effect, effectIndex, rfl,
            isAtMost⟩)
        simp [wpf6EraseEvent, isAbove, Obs.toDex]
      | _ => rfl

/-- The finite concatenations at transition boundaries reach every finite
concatenation of the `erase` chunks in length. -/
theorem wpf6_forward_trace_covers (program : Program σ)
    (run : InfiniteRun (Config σ) (Action σ)) (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation ((system program).eventsAt run) failureTrace)
    (chunks : Nat → List (Dex.Obs σ))
    (isChunks : ∀ position,
      (IsRepeatedAt failureTrace position → chunks position = []) ∧
      (¬ IsRepeatedAt failureTrace position →
        chunks position = ((failureTrace position).map Obs.toDex).toList))
    (count : Nat) :
    ∃ index, (concatUpTo chunks count).length ≤
      (concatUpTo chunks (wpf6EventCount program run index)).length := by
  rcases wpf6_exists_max_or_unbounded (wpf6EventCount program run) (fun index => by
      simp only [wpf6EventCount, concatUpTo, List.length_append]
      omega) with isUnbounded | ⟨maximum, isMaximum⟩
  · obtain ⟨index, isAfter⟩ := isUnbounded count
    exact ⟨index, concatUpTo_length_le _ isAfter⟩
  · refine ⟨maximum, ?_⟩
    have isEmpty : ∀ position, wpf6EventCount program run maximum ≤ position →
        chunks position = [] := by
      intro position isAfter
      have isNone : failureTrace position = none := by
        cases isTrace position with
        | inl found =>
          obtain ⟨index, isInside, _⟩ := found
          have isBelow := isMaximum index
          unfold wpf6EventCount at isBelow isAfter
          omega
        | inr missing => exact missing.2
      have isNotRepeated : ¬ IsRepeatedAt failureTrace position := by
        intro isRepeated
        obtain ⟨_, _, _, _, _, _, _, _, isAt⟩ := isRepeated
        rw [isNone] at isAt
        cases isAt
      rw [(isChunks position).2 isNotRepeated, isNone]
      rfl
    by_cases isBefore : count ≤ wpf6EventCount program run maximum
    · exact concatUpTo_length_le _ isBefore
    · obtain ⟨extra, isSum⟩ := Nat.exists_eq_add_of_le (Nat.le_of_lt (Nat.lt_of_not_le isBefore))
      rw [isSum, wpf6_concatUpTo_of_empty chunks _ isEmpty extra]
      exact Nat.le_refl _

/-- **The trace.** The Dex run in blocks has the trace `erase(t)`. -/
theorem wpf6_forward_trace (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation (fun index => ((system program).eventsAt run index).filterMap some)
      failureTrace)
    (trace : Sequence (Dex.Obs σ)) (isErased : IsRepeatErasure failureTrace trace) :
    IsConcatenation (fun index => ((wpf6ForwardBlocks program run).events index).filterMap some)
      trace := by
  simp only [List.filterMap_some] at isTrace ⊢
  obtain ⟨chunks, isChunkConcatenation, isChunks⟩ := isErased
  exact wpf6_isConcatenation_of_regroup chunks _ (wpf6EventCount program run)
    (wpf6_forward_trace_agree program ignoresAttemptContext run isRun failureTrace isTrace chunks
      isChunks)
    (wpf6_forward_trace_covers program run failureTrace isTrace chunks isChunks) trace
    isChunkConcatenation


/-! ## The marker -/

/-- The DexF progress steps are finitely many exactly when the Dex progress
steps of the flattened run are. -/
theorem wpf6_forward_finitelyMany_iff (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (hasNoExhaustion : RunHasNoExhaustion run)
    (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ)) (boundary : Nat → Nat)
    (isFlattened : (wpf6ForwardBlocks program run).IsFlattenedTo dexRun boundary)
    (isInfinite : (wpf6ForwardBlocks program run).HasInfinitelyManySteps) :
    (Dex.system program.failureFreeFlow).HasFinitelyManyProgressSteps dexRun ↔
      (system program).HasFinitelyManyProgressSteps run := by
  rw [(Dex.system program.failureFreeFlow).flattening_hasFinitelyManyProgressSteps _ dexRun
    boundary isFlattened isInfinite]
  have isSame : ∀ index, (system program).isProgress (run.labels index) = false ↔
      ∀ label, label ∈ (wpf6ForwardBlocks program run).blocks index →
        (Dex.system program.failureFreeFlow).isProgress label = false := by
    intro index
    show (run.labels index).isProgress = false ↔ _
    rw [isProgress_eq_dexProgress program (run.states index)
      (wpf6_invariant_at program ignoresAttemptContext run isRun index) (run.labels index)
      (hasNoExhaustion index) _ _ (wpf6_step_eventsAt program run isRun index), List.any_eq_false]
    simp only [Bool.not_eq_true]
    rfl
  constructor
  · intro isFinite
    obtain ⟨bound, isQuiet⟩ := isFinite
    exact ⟨bound, fun index isLater => (isSame index).mpr (isQuiet index isLater)⟩
  · intro isFinite
    obtain ⟨bound, isQuiet⟩ := isFinite
    exact ⟨bound, fun index isLater => (isSame index).mp (isQuiet index isLater)⟩

/-- A closed Dex run stays closed along a Dex run. -/
theorem wpf6_dex_closed_forever (flow : Dex.FlowDef σ)
    (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ)) (isRun : (Dex.system flow).IsRun dexRun)
    (first : Nat) (isClosed : (Dex.system flow).isClosed (dexRun.states first) = true) :
    ∀ extra, (Dex.system flow).isClosed (dexRun.states (first + extra)) = true := by
  intro extra
  induction extra with
  | zero => exact isClosed
  | succ extra inductionHypothesis =>
    obtain ⟨events, isStep⟩ := isRun.2 (first + extra)
    rw [← Nat.add_assoc]
    exact wpf6_dex_isClosed_step flow _ _ events _ isStep inductionHypothesis

/-- **The marker.** The flattened Dex run has the marker of the DexF run. -/
theorem wpf6_forward_marker (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (hasNoExhaustion : RunHasNoExhaustion run)
    (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ)) (boundary : Nat → Nat)
    (isFlattened : (wpf6ForwardBlocks program run).IsFlattenedTo dexRun boundary)
    (isInfinite : (wpf6ForwardBlocks program run).HasInfinitelyManySteps)
    (dexIsRun : (Dex.system program.failureFreeFlow).IsRun dexRun) (marker : Marker)
    (hasMarker : (system program).HasMarker run marker) :
    (Dex.system program.failureFreeFlow).HasMarker dexRun marker := by
  have isIff := wpf6_forward_finitelyMany_iff program ignoresAttemptContext run isRun
    hasNoExhaustion dexRun boundary isFlattened isInfinite
  have isClosedAt : ∀ index,
      (Dex.system program.failureFreeFlow).isClosed (dexRun.states (boundary index)) =
        (system program).isClosed (run.states index) := by
    intro index
    rw [isFlattened.stateAtBoundary index]
    exact wpf6_isClosed_abstract program _
  cases marker with
  | terminated =>
    obtain ⟨isFinite, index, isClosed⟩ := hasMarker
    exact ⟨isIff.mpr isFinite, boundary index, by rw [isClosedAt]; exact isClosed⟩
  | deadlocked =>
    obtain ⟨isFinite, isOpen⟩ := hasMarker
    refine ⟨isIff.mpr isFinite, fun position => ?_⟩
    cases isClosed : (Dex.system program.failureFreeFlow).isClosed (dexRun.states position) with
    | false => rfl
    | true =>
      obtain ⟨index, isAfter⟩ := isFlattened.exists_boundary_ge isInfinite position
      obtain ⟨extra, isSum⟩ := Nat.exists_eq_add_of_le isAfter
      have isStillClosed := wpf6_dex_closed_forever _ dexRun dexIsRun position isClosed extra
      rw [← isSum, isClosedAt, isOpen index] at isStillClosed
      cases isStillClosed
  | divergent => exact fun isFinite => hasMarker (isIff.mp isFinite)

/-! ## Fairness -/

/-- **Fairness.** The flattened Dex run of a fair DexF run is fair: F1 and F2
directly, F3 to F5 because a fair DexF run settles (`Failure/Composite/WPF6Run.lean`). -/
theorem wpf6_forward_isFair (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ))
    (boundary : Nat → Nat)
    (isFlattened : (wpf6ForwardBlocks program run).IsFlattenedTo dexRun boundary)
    (isInfinite : (wpf6ForwardBlocks program run).HasInfinitelyManySteps) :
    Dex.IsFair dexRun := by
  have isStateAt : ∀ index, dexRun.states (boundary index) = abstractConfig (run.states index) :=
    isFlattened.stateAtBoundary
  refine ⟨?_, ?_, ?_, ?_, ?_, trivial⟩
  · intro bound
    obtain ⟨index, isAbove⟩ := isFair.1 bound
    refine ⟨boundary index, ?_⟩
    rw [isStateAt index]
    exact isAbove
  · intro timer
    have isTransferred : ∀ index,
        Dex.Fairness.IsTimerArmedAndDue dexRun timer (boundary index) →
        Fairness.IsTimerArmedAndDue run (.wait timer) index := by
      intro index isDexDue
      obtain ⟨flowRun, fireTime, hasRun, isArmed, isDue⟩ := isDexDue
      rw [isStateAt index] at hasRun isDue
      obtain ⟨baseRun, hasBase, isTimers⟩ := wpf6_timers_abstract _ flowRun hasRun
      refine ⟨baseRun, fireTime, hasBase, ?_, isDue⟩
      show baseRun.base.timers.get? timer = some fireTime
      rw [← isTimers]
      exact isArmed
    apply wpf6_eventuallyFails_of_boundaries isFlattened isInfinite
    intro start
    by_cases isDue : Fairness.IsTimerArmedAndDue run (.wait timer) start
    · obtain ⟨later, isLater, isNotDue⟩ := isFair.2.1 (.wait timer) start isDue
      exact ⟨later, isLater, fun isDexDue => isNotDue (isTransferred later isDexDue)⟩
    · exact ⟨start, Nat.le_refl _, fun isDexDue => isDue (isTransferred start isDexDue)⟩
  · intro stepId method
    apply wpf6_eventuallyFails_of_boundaries isFlattened isInfinite
    intro start
    obtain ⟨index, isLater, isNotPending⟩ := wpf6_abstractPending_eventually_ends program
      ignoresAttemptContext run isRun isFair stepId method start
    refine ⟨index, isLater, fun isPending => ?_⟩
    obtain ⟨flowRun, record, hasRun, isFound, isInvocation⟩ := isPending
    rw [isStateAt index] at hasRun
    exact isNotPending flowRun record hasRun isFound isInvocation
  · intro taskId
    apply wpf6_eventuallyFails_of_boundaries isFlattened isInfinite
    intro start
    obtain ⟨index, isLater, isGone⟩ := wpf6_virtualTask_eventually_gone program
      ignoresAttemptContext run isRun isFair taskId start
    refine ⟨index, isLater, fun isUnsettled => ?_⟩
    obtain ⟨task, isFound, _⟩ := isUnsettled
    rw [isStateAt index, isGone] at isFound
    cases isFound
  · intro taskId
    apply wpf6_eventuallyFails_of_boundaries isFlattened isInfinite
    intro start
    obtain ⟨index, isLater, isGone⟩ := wpf6_virtualTask_eventually_gone program
      ignoresAttemptContext run isRun isFair taskId start
    refine ⟨index, isLater, fun isReturned => ?_⟩
    obtain ⟨task, response, isFound, _⟩ := isReturned
    rw [isStateAt index, isGone] at isFound
    cases isFound

/-! ## The forward inclusion -/

/-- **WP-F6, forward.** `erase(t)` of a fair maximal DexF trace `t` for an
admissible outcome word `w` is a fair maximal Dex trace for `w↓` with the
same marker. -/
theorem wpf6_liveness_forward (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (word : Sequence (Request σ)) (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (erasedWord : Sequence (Dex.Request σ)) (isErasure : IsRetryErasure word erasedWord)
    (trace : Sequence (Dex.Obs σ)) (marker : Marker) (failureTrace : Sequence (Obs σ))
    (isFailureTrace : IsFailureFairMaximalTrace program word failureTrace marker)
    (isErased : IsRepeatErasure failureTrace trace) :
    (Dex.system program.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some erasedWord trace
      marker := by
  obtain ⟨run, isRun, isRunFor, _, ⟨isFair, hypotheses⟩, isTrace, hasMarker⟩ := isFailureTrace
  have hasNoExhaustion : RunHasNoExhaustion run := hypotheses.1
  have isBlockRun := wpf6_forward_isBlockRun program ignoresAttemptContext run isRun
    hasNoExhaustion
  have isInfinite := wpf6_forward_hasInfinitelyManySteps program run isRun isFair.1
  obtain ⟨dexRun, boundary, dexIsRun, isFlattened⟩ :=
    (Dex.system program.failureFreeFlow).exists_flattening _ isBlockRun isInfinite
  have dexIsRunFor : (Dex.system program.failureFreeFlow).IsRunFor dexRun erasedWord :=
    ((Dex.system program.failureFreeFlow).flattening_isRunFor _ dexRun boundary isFlattened
      isInfinite erasedWord).mp
      (wpf6_forward_isRunFor program run isRun environment word isAdmissible erasedWord isErasure
        isRunFor)
  refine ⟨dexRun, dexIsRun, dexIsRunFor,
    (Dex.system program.failureFreeFlow).requestsArrive_of_isRunFor dexRun erasedWord dexIsRunFor,
    wpf6_forward_isFair program ignoresAttemptContext run isRun isFair dexRun boundary isFlattened
      isInfinite, ?_,
    wpf6_forward_marker program ignoresAttemptContext run isRun hasNoExhaustion dexRun boundary
      isFlattened isInfinite dexIsRun marker hasMarker⟩
  exact ((Dex.system program.failureFreeFlow).flattening_isConcatenation_events _ dexRun boundary
    isBlockRun dexIsRun isFlattened isInfinite some trace).mp
    (wpf6_forward_trace program ignoresAttemptContext run isRun failureTrace isTrace trace isErased)

end DexExpressiveness.DexF
