import DexExpressiveness.Failure.Statements

/-!
# Fair maximal DexF runs from a finite schedule

Test tooling for the Stage 3 witnesses, not part of the theorem. A finite
DexF schedule that ends in a quiet configuration, followed by `tick 1`
forever, is a fair maximal run (`prefixThenTicks_isFailureFairMaximalTrace`):
F1 to F6, FF, FR, FI, and the run hypotheses H1, H4, D6, and HP hold. The
facts about the schedule itself are Boolean checks that `decide +kernel`
evaluates: the labels (`schedulePassesLabelChecks`), each configuration
before a label (`schedulePassesConfigChecks`), and the final configuration
(`isQuietAtRest`).

The file also has the generic facts the witnesses share: an outcome-word
prefix gives an admissible outcome word, and every DexF trace has an
erasure.
-/

namespace DexExpressiveness.DexF.Examples

open DexExpressiveness

variable {σ : Dex.Signature}

/-! ## Concatenations -/

/-- `concatUpTo` with its first chunk split off. -/
theorem concatUpTo_succ_shift {α : Type} (chunks : Nat → List α) (count : Nat) :
    concatUpTo chunks (count + 1) = chunks 0 ++ concatUpTo (fun index => chunks (index + 1)) count := by
  induction count with
  | zero => simp [concatUpTo]
  | succ count inductionHypothesis =>
    rw [concatUpTo, inductionHypothesis, concatUpTo, List.append_assoc]

/-- Chunks that are empty from `stop` on concatenate to their first `stop`
chunks. -/
theorem isConcatenation_of_nil_from {α : Type} (chunks : Nat → List α) (stop : Nat)
    (isNilFrom : ∀ index, stop ≤ index → chunks index = []) :
    IsConcatenation chunks (Sequence.ofList (concatUpTo chunks stop)) := by
  have isStable : ∀ index, stop ≤ index → concatUpTo chunks index = concatUpTo chunks stop := by
    intro index
    induction index with
    | zero =>
      intro isAfter
      have isZero : stop = 0 := by omega
      rw [isZero]
    | succ index inductionHypothesis =>
      intro isAfter
      by_cases isStop : index + 1 = stop
      · rw [isStop]
      · have isBefore : stop ≤ index := by omega
        rw [concatUpTo, inductionHypothesis isBefore, isNilFrom index isBefore, List.append_nil]
  intro position
  by_cases isInside : position < (concatUpTo chunks stop).length
  · exact Or.inl ⟨stop, isInside, rfl⟩
  · refine Or.inr ⟨?_, List.getElem?_eq_none (by omega)⟩
    intro count
    by_cases isAfter : stop ≤ count
    · rw [isStable count isAfter]
      omega
    · have isShorter := concatUpTo_length_le chunks (show count ≤ stop by omega)
      omega

/-- The requests of the first `count` labels of a list. -/
theorem concatUpTo_filterMap {α β : Type} (elements : List α) (transform : α → Option β)
    (count : Nat) :
    concatUpTo (fun index => (elements[index]?.bind transform).toList) count =
      (elements.take count).filterMap transform := by
  induction count with
  | zero => simp [concatUpTo]
  | succ count inductionHypothesis =>
    rw [concatUpTo, inductionHypothesis, List.take_add_one, List.filterMap_append]
    rcases elements[count]? with _ | element
    · simp
    · cases isTransformed : transform element <;> simp [isTransformed]

/-! ## The configurations of a schedule -/

/-- The configurations a schedule passes through from `config`: the one
before each label, then the final one. It stops at the first label that is
not enabled. -/
def scanConfigs (program : Program σ) : Config σ → List (Action σ) → List (Config σ)
  | config, [] => [config]
  | config, label :: laterLabels =>
    config ::
      match step program config label with
      | some (_, next) => scanConfigs program next laterLabels
      | none => []

/-- The events of the step at a position of a schedule, from the
configuration `scanConfigs` gives there. -/
def scheduleStepEvents (program : Program σ) (configs : List (Config σ))
    (labels : List (Action σ)) (index : Nat) : List (Obs σ) :=
  match step program ((configs[index]?).getD (Config.initial σ)) ((labels[index]?).getD (.tick 1)) with
  | some (events, _) => events
  | none => []

/-- An executable schedule: `scanConfigs` has one configuration per label and
the final one, consecutive configurations are related by the step of their
label, and the step events concatenate to the events of the run. -/
theorem scanConfigs_of_exec (program : Program σ) (config : Config σ) (labels : List (Action σ))
    (events : List (Obs σ)) (final : Config σ)
    (isExec : (system program).exec config labels = some (events, final)) :
    (scanConfigs program config labels).length = labels.length + 1 ∧
    (scanConfigs program config labels)[labels.length]? = some final ∧
    (scanConfigs program config labels)[0]? = some config ∧
    (∀ index (isInside : index < labels.length), ∃ stepEvents,
      step program ((scanConfigs program config labels)[index]?.getD (Config.initial σ))
          labels[index] =
        some (stepEvents, (scanConfigs program config labels)[index + 1]?.getD (Config.initial σ))) ∧
    concatUpTo (scheduleStepEvents program (scanConfigs program config labels) labels)
      labels.length = events := by
  induction labels generalizing config events with
  | nil =>
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    simp [scanConfigs, concatUpTo]
  | cons label laterLabels inductionHypothesis =>
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, isEvents⟩ :=
      (system program).exec_cons config label laterLabels events final isExec
    change step program config label = some (stepEvents, next) at isStep
    obtain ⟨isLength, isFinal, isFirst, isSteps, isConcat⟩ :=
      inductionHypothesis next laterEvents isLater
    have isScan : scanConfigs program config (label :: laterLabels) =
        config :: scanConfigs program next laterLabels := by
      simp [scanConfigs, isStep]
    rw [isScan]
    refine ⟨by simp [isLength], by simpa using isFinal, rfl, ?_, ?_⟩
    · intro index isInside
      cases index with
      | zero =>
        refine ⟨stepEvents, ?_⟩
        have nextConfig : (scanConfigs program next laterLabels)[0]?.getD (Config.initial σ) = next := by
          rw [isFirst]; rfl
        simpa [nextConfig] using isStep
      | succ index =>
        simpa using isSteps index (by simpa using isInside)
    · rw [List.length_cons, concatUpTo_succ_shift, isEvents]
      congr 1
      simp [scheduleStepEvents, isStep]

/-- A Boolean check on every configuration before a label, with its label,
holds at every position. -/
theorem scanConfigs_check (program : Program σ) (config : Config σ) (labels : List (Action σ))
    (isLength : (scanConfigs program config labels).length = labels.length + 1)
    (check : Config σ → Action σ → Bool)
    (passes : ((scanConfigs program config labels).zip labels).all
      (fun pair => check pair.1 pair.2) = true)
    (index : Nat) (isInside : index < labels.length) :
    check ((scanConfigs program config labels)[index]?.getD (Config.initial σ)) labels[index] =
      true := by
  have isZipInside : index < ((scanConfigs program config labels).zip labels).length := by
    simp [isLength]; omega
  have holds := List.all_eq_true.mp passes _ (List.getElem_mem isZipInside)
  have isConfig : (scanConfigs program config labels)[index]?.getD (Config.initial σ) =
      (scanConfigs program config labels)[index]'(by omega) := by
    simp [List.getElem?_eq_getElem (show index < (scanConfigs program config labels).length by omega)]
  rw [isConfig]
  simpa using holds

/-! ## The run: the schedule, then ticks -/

/-- The final configuration of a schedule. -/
def scheduleFinal (program : Program σ) (labels : List (Action σ)) : Config σ :=
  ((scanConfigs program (Config.initial σ) labels)[labels.length]?).getD (Config.initial σ)

/-- The run that takes the labels of the schedule and then `tick 1` forever. -/
def prefixThenTicks (program : Program σ) (labels : List (Action σ)) :
    InfiniteRun (Config σ) (Action σ) :=
  { states := fun index =>
      if index < labels.length then
        ((scanConfigs program (Config.initial σ) labels)[index]?).getD (Config.initial σ)
      else
        { scheduleFinal program labels with
          clock := (scheduleFinal program labels).clock + (index - labels.length) }
    labels := fun index => (labels[index]?).getD (.tick 1) }

/-- A configuration with its own clock. -/
theorem config_with_own_clock (config : Config σ) :
    { config with clock := config.clock + 0 } = config := by
  cases config
  rfl

/-- The states from the end of the schedule on are the final configuration
with a later clock. -/
theorem prefixThenTicks_states_tail (program : Program σ) (labels : List (Action σ))
    (index : Nat) (isAfter : labels.length ≤ index) :
    (prefixThenTicks program labels).states index =
      { scheduleFinal program labels with
        clock := (scheduleFinal program labels).clock + (index - labels.length) } := by
  simp [prefixThenTicks, show ¬ index < labels.length by omega]

/-- The labels from the end of the schedule on are `tick 1`. -/
theorem prefixThenTicks_labels_tail (program : Program σ) (labels : List (Action σ))
    (index : Nat) (isAfter : labels.length ≤ index) :
    (prefixThenTicks program labels).labels index = .tick 1 := by
  simp [prefixThenTicks, List.getElem?_eq_none isAfter]

/-- The labels inside the schedule are its labels. -/
theorem prefixThenTicks_labels_inside (program : Program σ) (labels : List (Action σ))
    (index : Nat) (isInside : index < labels.length) :
    (prefixThenTicks program labels).labels index = labels[index] := by
  simp [prefixThenTicks, List.getElem?_eq_getElem isInside]

/-- The states inside the schedule, and at its end, are the configurations of
the schedule. -/
theorem prefixThenTicks_states_upTo (program : Program σ) (labels : List (Action σ))
    (index : Nat) (isUpTo : index ≤ labels.length) :
    (prefixThenTicks program labels).states index =
      ((scanConfigs program (Config.initial σ) labels)[index]?).getD (Config.initial σ) := by
  by_cases isInside : index < labels.length
  · simp [prefixThenTicks, isInside]
  · have isEnd : index = labels.length := by omega
    subst isEnd
    rw [prefixThenTicks_states_tail program labels _ (Nat.le_refl _), Nat.sub_self,
      config_with_own_clock]
    rfl

/-- The run is a DexF run. -/
theorem prefixThenTicks_isRun (program : Program σ) (labels : List (Action σ))
    (events : List (Obs σ)) (final : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, final)) :
    (system program).IsRun (prefixThenTicks program labels) := by
  obtain ⟨_, _, isFirst, isSteps, _⟩ :=
    scanConfigs_of_exec program (Config.initial σ) labels events final isExec
  refine ⟨?_, ?_⟩
  · rw [prefixThenTicks_states_upTo program labels 0 (Nat.zero_le _), isFirst]
    rfl
  · intro index
    by_cases isInside : index < labels.length
    · obtain ⟨stepEvents, isStep⟩ := isSteps index isInside
      refine ⟨stepEvents, ?_⟩
      rw [prefixThenTicks_states_upTo program labels index (by omega),
        prefixThenTicks_states_upTo program labels (index + 1) (by omega),
        prefixThenTicks_labels_inside program labels index isInside]
      exact isStep
    · refine ⟨[], ?_⟩
      rw [prefixThenTicks_states_tail program labels index (by omega),
        prefixThenTicks_states_tail program labels (index + 1) (by omega),
        prefixThenTicks_labels_tail program labels index (by omega)]
      simp only [system, step, ite_true, Nat.zero_lt_one]
      congr 3
      omega

/-- The run is for the outcome word of the schedule's requests. -/
theorem prefixThenTicks_isRunFor (program : Program σ) (labels : List (Action σ)) :
    (system program).IsRunFor (prefixThenTicks program labels)
      (Sequence.ofList (labels.filterMap Action.requestOf)) := by
  have isChunks : (system program).requestsAt (prefixThenTicks program labels) =
      fun index => (labels[index]?.bind Action.requestOf).toList := by
    funext index
    simp only [TransitionSystem.requestsAt, system, prefixThenTicks]
    cases labels[index]? <;> rfl
  have isConcat := isConcatenation_of_nil_from
    (fun index => (labels[index]?.bind Action.requestOf).toList) labels.length (by
      intro index isAfter
      simp [List.getElem?_eq_none isAfter])
  rw [concatUpTo_filterMap, List.take_length] at isConcat
  unfold TransitionSystem.IsRunFor
  rw [isChunks]
  exact isConcat

/-- The trace of the run is the trace of the schedule. -/
theorem prefixThenTicks_trace (program : Program σ) (labels : List (Action σ))
    (events : List (Obs σ)) (final : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, final)) :
    IsConcatenation
      (fun index => ((system program).eventsAt (prefixThenTicks program labels) index).filterMap some)
      (Sequence.ofList events) := by
  obtain ⟨_, _, _, _, isConcat⟩ :=
    scanConfigs_of_exec program (Config.initial σ) labels events final isExec
  have isChunks : (fun index =>
      ((system program).eventsAt (prefixThenTicks program labels) index).filterMap some) =
      fun index => if index < labels.length then
        scheduleStepEvents program (scanConfigs program (Config.initial σ) labels) labels index
      else [] := by
    funext index
    rw [List.filterMap_some]
    by_cases isInside : index < labels.length
    · simp only [isInside, ite_true, TransitionSystem.eventsAt, scheduleStepEvents, system]
      rw [prefixThenTicks_states_upTo program labels index (by omega),
        prefixThenTicks_labels_inside program labels index isInside,
        List.getElem?_eq_getElem isInside, Option.getD_some]
      cases step program ((scanConfigs program (Config.initial σ) labels)[index]?.getD
        (Config.initial σ)) labels[index] <;> rfl
    · simp only [isInside, ite_false, TransitionSystem.eventsAt, system]
      rw [prefixThenTicks_states_tail program labels index (by omega),
        prefixThenTicks_labels_tail program labels index (by omega)]
      simp [step]
  rw [isChunks]
  have isPrefixConcat : concatUpTo (fun index => if index < labels.length then
      scheduleStepEvents program (scanConfigs program (Config.initial σ) labels) labels index
      else []) labels.length = events := by
    rw [← isConcat]
    have agree : ∀ count, count ≤ labels.length →
        concatUpTo (fun index => if index < labels.length then
          scheduleStepEvents program (scanConfigs program (Config.initial σ) labels) labels index
          else []) count =
        concatUpTo (scheduleStepEvents program (scanConfigs program (Config.initial σ) labels)
          labels) count := by
      intro count
      induction count with
      | zero => intro _; rfl
      | succ count inductionHypothesis =>
        intro isUpTo
        rw [concatUpTo, concatUpTo, inductionHypothesis (by omega)]
        simp [show count < labels.length by omega]
    exact agree _ (Nat.le_refl _)
  have tailConcat := isConcatenation_of_nil_from (fun index => if index < labels.length then
      scheduleStepEvents program (scanConfigs program (Config.initial σ) labels) labels index
      else []) labels.length (by
        intro index isAfter
        simp [show ¬ index < labels.length by omega])
  rw [isPrefixConcat] at tailConcat
  exact tailConcat

/-! ## The checks -/

/-- Whether a label is [CRASH]. -/
def isCrashLabel : Action σ → Bool
  | .crash => true
  | _ => false

/-- Whether a label is [RECOVER]. -/
def isRecoverLabel : Action σ → Bool
  | .recover => true
  | _ => false

/-- Every [CRASH] of the labels is followed by a later [RECOVER]. -/
def crashesAreFollowedByRecover : List (Action σ) → Bool
  | [] => true
  | label :: laterLabels =>
    (!isCrashLabel label || laterLabels.any isRecoverLabel) && crashesAreFollowedByRecover laterLabels

/-- The check gives FR on the positions of the schedule. -/
theorem crashesAreFollowedByRecover_sound (labels : List (Action σ))
    (passes : crashesAreFollowedByRecover labels = true) (index : Nat)
    (isInside : index < labels.length) (isCrash : isCrashLabel labels[index] = true) :
    ∃ later, index < later ∧ ∃ isLaterInside : later < labels.length,
      isRecoverLabel labels[later] = true := by
  induction labels generalizing index with
  | nil => simp at isInside
  | cons label laterLabels inductionHypothesis =>
    simp only [crashesAreFollowedByRecover, Bool.and_eq_true, Bool.or_eq_true,
      Bool.not_eq_true'] at passes
    cases index with
    | zero =>
      have hasRecover : laterLabels.any isRecoverLabel = true := by
        rcases passes.1 with isNotCrash | hasRecover
        · simp at isCrash; rw [isCrash] at isNotCrash; cases isNotCrash
        · exact hasRecover
      obtain ⟨recoverLabel, isMember, isRecover⟩ := List.any_eq_true.mp hasRecover
      obtain ⟨position, isPositionInside, isAt⟩ := List.getElem_of_mem isMember
      exact ⟨position + 1, by omega, by simpa using isPositionInside, by simpa [isAt] using isRecover⟩
    | succ index =>
      obtain ⟨later, isAfter, isLaterInside, isRecover⟩ :=
        inductionHypothesis passes.2 index (by simpa using isInside) (by simpa using isCrash)
      exact ⟨later + 1, by omega, by simpa using isLaterInside, by simpa using isRecover⟩

/-- The label checks of a schedule: H1, H4, and FR. -/
def schedulePassesLabelChecks (labels : List (Action σ)) : Bool :=
  labels.all (fun label => !label.isExhausting && !label.isNonRetryableFailure) &&
    crashesAreFollowedByRecover labels

/-- D6 at one configuration: every timer that the label fires is due at the
clock. -/
def firedTimersAreDueAt (config : Config σ) : Action σ → Bool
  | .timerFire count _ =>
    match config.run with
    | some flowRun =>
      ((flowRun.dueTimers config.clock).take count).all (fun timer =>
        match flowRun.fireTimeOf? timer with
        | some fireTime => decide (fireTime ≤ config.clock)
        | none => true)
    | none => true
  | _ => true

/-- HP at one configuration: a StartFlow that is not [START-RETRY] reaches
no run. -/
def startReachesNoRunAt (config : Config σ) : Action σ → Bool
  | .request (.startFlow _ _ _) => config.run.isNone
  | .engineReject (.startFlow _ _ _) => config.run.isNone
  | _ => true

/-- The configuration checks of a schedule: D6 and HP. -/
def schedulePassesConfigChecks (program : Program σ) (labels : List (Action σ)) : Bool :=
  ((scanConfigs program (Config.initial σ) labels).zip labels).all
    (fun pair => firedTimersAreDueAt pair.1 pair.2 && startReachesNoRunAt pair.1 pair.2)

/-- A configuration at rest: no real task, no interrupt, a run with no armed
timer of either kind and no invocation. -/
def isQuietAtRest (config : Config σ) : Bool :=
  config.tasks.isEmpty && config.zombies.isEmpty && config.interrupts.isEmpty &&
    match config.run with
    | none => false
    | some flowRun =>
      flowRun.base.timers.isEmpty && flowRun.retryTimers.isEmpty &&
        flowRun.base.steps.all (fun record => record.invocation.isNone)

/-! ## Fairness and the hypotheses -/

/-- A predicate that fails from a fixed point on eventually fails. -/
theorem eventuallyFails_of_tail (holdsAt : Nat → Prop) (stop : Nat)
    (failsFrom : ∀ index, stop ≤ index → ¬ holdsAt index) : EventuallyFails holdsAt := by
  intro start _
  exact ⟨max start stop, Nat.le_max_left .., failsFrom _ (Nat.le_max_right ..)⟩

/-- The tail is quiet: the facts of `Config.isQuietAtRest` at every state
from the end of the schedule on. -/
theorem prefixThenTicks_tail_quiet (program : Program σ) (labels : List (Action σ))
    (isQuiet : isQuietAtRest (scheduleFinal program labels) = true) (index : Nat)
    (isAfter : labels.length ≤ index) :
    ((prefixThenTicks program labels).states index).realTasks = [] ∧
    ((prefixThenTicks program labels).states index).interrupts = [] ∧
    ∃ flowRun, ((prefixThenTicks program labels).states index).run = some flowRun ∧
      flowRun.base.timers = [] ∧ flowRun.retryTimers = [] ∧
      ∀ record, record ∈ flowRun.base.steps → record.invocation = none := by
  rw [prefixThenTicks_states_tail program labels index isAfter]
  unfold isQuietAtRest at isQuiet
  simp only [Bool.and_eq_true, List.isEmpty_iff] at isQuiet
  obtain ⟨⟨⟨noTasks, noZombies⟩, noInterrupts⟩, hasRun⟩ := isQuiet
  refine ⟨by simp [Config.realTasks, noTasks, noZombies], noInterrupts, ?_⟩
  split at hasRun
  · cases hasRun
  · rename_i flowRun isRun
    simp only [Bool.and_eq_true, List.isEmpty_iff, List.all_eq_true, Option.isNone_iff_eq_none]
      at hasRun
    exact ⟨flowRun, isRun, hasRun.1.1, hasRun.1.2, hasRun.2⟩

/-- The run is fair. -/
theorem prefixThenTicks_isFair (program : Program σ) (labels : List (Action σ))
    (passesLabels : schedulePassesLabelChecks labels = true)
    (isQuiet : isQuietAtRest (scheduleFinal program labels) = true) :
    IsFair (prefixThenTicks program labels) := by
  have isQuietAt := prefixThenTicks_tail_quiet program labels isQuiet
  simp only [schedulePassesLabelChecks, Bool.and_eq_true] at passesLabels
  refine ⟨?_, ?_, ?_, ?_, ?_, trivial, ?_, ?_, ?_⟩
  · intro bound
    refine ⟨bound + labels.length + 1, ?_⟩
    rw [prefixThenTicks_states_tail program labels _ (by omega)]
    show bound < (scheduleFinal program labels).clock + (bound + labels.length + 1 - labels.length)
    omega
  · intro timer
    refine eventuallyFails_of_tail _ labels.length (fun index isAfter isDue => ?_)
    obtain ⟨_, _, flowRun, isRun, noTimers, noRetryTimers, _⟩ := isQuietAt index isAfter
    obtain ⟨dueRun, fireTime, isDueRun, isFireTime, _⟩ := isDue
    rw [isRun] at isDueRun
    cases isDueRun
    cases timer <;> simp [Run.fireTimeOf?, noTimers, noRetryTimers, AssocList.get?] at isFireTime
  · intro stepId method
    refine eventuallyFails_of_tail _ labels.length (fun index isAfter isStartable => ?_)
    obtain ⟨_, _, flowRun, isRun, _, _, noInvocation⟩ := isQuietAt index isAfter
    obtain ⟨_, startRun, record, _, isStartRun, isFound, isPending, _⟩ := isStartable
    rw [isRun] at isStartRun
    cases isStartRun
    have isMember : record ∈ flowRun.base.steps := List.mem_of_find?_eq_some isFound
    rw [noInvocation record isMember] at isPending
    cases isPending
  · intro taskId
    refine eventuallyFails_of_tail _ labels.length (fun index isAfter isUnsettled => ?_)
    obtain ⟨noRealTasks, _⟩ := isQuietAt index isAfter
    obtain ⟨task, isFound, _⟩ := isUnsettled
    simp [Config.findRealTask?, noRealTasks] at isFound
  · intro taskId
    refine eventuallyFails_of_tail _ labels.length (fun index isAfter isReturned => ?_)
    obtain ⟨noRealTasks, _⟩ := isQuietAt index isAfter
    obtain ⟨task, _, isFound, _⟩ := isReturned
    simp [Config.findRealTask?, noRealTasks] at isFound
  · refine ⟨labels.length, fun index isAfter => ?_⟩
    rw [prefixThenTicks_labels_tail program labels index isAfter]
    rfl
  · intro index isCrash
    by_cases isInside : index < labels.length
    · rw [prefixThenTicks_labels_inside program labels index isInside] at isCrash
      obtain ⟨later, isAfter, isLaterInside, isRecover⟩ :=
        crashesAreFollowedByRecover_sound labels passesLabels.2 index isInside (by rw [isCrash]; rfl)
      refine ⟨later, isAfter, ?_⟩
      rw [prefixThenTicks_labels_inside program labels later isLaterInside]
      revert isRecover
      cases labels[later] <;> simp [isRecoverLabel]
    · rw [prefixThenTicks_labels_tail program labels index (by omega)] at isCrash
      cases isCrash
  · intro interrupt
    refine eventuallyFails_of_tail _ labels.length (fun index isAfter isMember => ?_)
    obtain ⟨_, noInterrupts, _⟩ := isQuietAt index isAfter
    rw [noInterrupts] at isMember
    cases isMember

/-- The run satisfies H1, H4, D6, and HP. -/
theorem prefixThenTicks_isHypothesisRun (program : Program σ) (labels : List (Action σ))
    (events : List (Obs σ)) (final : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, final))
    (passesLabels : schedulePassesLabelChecks labels = true)
    (passesConfigs : schedulePassesConfigChecks program labels = true) :
    IsHypothesisRun (prefixThenTicks program labels) := by
  obtain ⟨isLength, _, _, _, _⟩ :=
    scanConfigs_of_exec program (Config.initial σ) labels events final isExec
  simp only [schedulePassesLabelChecks, Bool.and_eq_true] at passesLabels
  have labelPasses : ∀ index (isInside : index < labels.length),
      labels[index].isExhausting = false ∧ labels[index].isNonRetryableFailure = false := by
    intro index isInside
    have holds := List.all_eq_true.mp passesLabels.1 _ (List.getElem_mem isInside)
    simpa using holds
  have configPasses := scanConfigs_check program (Config.initial σ) labels isLength
    (fun config label => firedTimersAreDueAt config label && startReachesNoRunAt config label)
    passesConfigs
  refine ⟨?_, ?_, ?_, ?_⟩
  · intro index
    by_cases isInside : index < labels.length
    · rw [prefixThenTicks_labels_inside program labels index isInside]
      exact (labelPasses index isInside).1
    · rw [prefixThenTicks_labels_tail program labels index (by omega)]
      rfl
  · intro index
    by_cases isInside : index < labels.length
    · rw [prefixThenTicks_labels_inside program labels index isInside]
      exact (labelPasses index isInside).2
    · rw [prefixThenTicks_labels_tail program labels index (by omega)]
      rfl
  · intro index count isWaitTimerFired flowRun timer fireTime isFire isRun isFired isFireTime
    by_cases isInside : index < labels.length
    · rw [prefixThenTicks_labels_inside program labels index isInside] at isFire
      have holds := configPasses index isInside
      rw [isFire, ← prefixThenTicks_states_upTo program labels index (by omega)] at holds
      simp only [Bool.and_eq_true, firedTimersAreDueAt, isRun, List.all_eq_true] at holds
      have timerHolds := holds.1 timer isFired
      rw [isFireTime] at timerHolds
      simpa using timerHolds
    · rw [prefixThenTicks_labels_tail program labels index (by omega)] at isFire
      cases isFire
  · intro index requestId stepType input isRequest hasRun
    by_cases isInside : index < labels.length
    · rw [prefixThenTicks_labels_inside program labels index isInside] at isRequest ⊢
      have holds := configPasses index isInside
      rw [← prefixThenTicks_states_upTo program labels index (by omega)] at holds
      revert isRequest holds
      cases labels[index] with
      | request clientRequest =>
        cases clientRequest with
        | startFlow _ _ _ =>
          intro _ holds
          simp only [Bool.and_eq_true, startReachesNoRunAt, Option.isNone_iff_eq_none] at holds
          rw [holds.2] at hasRun
          cases hasRun
        | publish _ _ =>
          intro isRequest
          simp [Action.requestOf] at isRequest
      | startRetry retryRequestId retryStepType retryInput =>
        intro isRequest _
        simp only [Action.requestOf, Option.some.injEq, Request.startFlow.injEq] at isRequest
        obtain ⟨rfl, rfl, rfl⟩ := isRequest
        rfl
      | engineReject clientRequest =>
        cases clientRequest with
        | startFlow _ _ _ =>
          intro _ holds
          simp only [Bool.and_eq_true, startReachesNoRunAt, Option.isNone_iff_eq_none] at holds
          rw [holds.2] at hasRun
          cases hasRun
        | publish _ _ =>
          intro isRequest
          simp [Action.requestOf] at isRequest
      | _ => simp [Action.requestOf]
    · rw [prefixThenTicks_labels_tail program labels index (by omega)] at isRequest
      cases isRequest

/-- The marker `✓`: the final configuration is closed. -/
theorem prefixThenTicks_hasMarker_terminated (program : Program σ) (labels : List (Action σ))
    (isClosed : (system program).isClosed (scheduleFinal program labels) = true) :
    (system program).HasMarker (prefixThenTicks program labels) .terminated := by
  refine ⟨⟨labels.length, fun index isAfter => ?_⟩, labels.length, ?_⟩
  · rw [prefixThenTicks_labels_tail program labels index isAfter]
    rfl
  · rw [prefixThenTicks_states_tail program labels _ (Nat.le_refl _), Nat.sub_self,
      config_with_own_clock]
    exact isClosed

/-- The marker `δ`: no configuration of the schedule is closed. -/
theorem prefixThenTicks_hasMarker_deadlocked (program : Program σ) (labels : List (Action σ))
    (events : List (Obs σ)) (final : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, final))
    (isNeverClosed : (scanConfigs program (Config.initial σ) labels).all
      (fun config => !(system program).isClosed config) = true) :
    (system program).HasMarker (prefixThenTicks program labels) .deadlocked := by
  obtain ⟨isLength, _, _, _, _⟩ :=
    scanConfigs_of_exec program (Config.initial σ) labels events final isExec
  have isOpenAt : ∀ index, index ≤ labels.length →
      (system program).isClosed
        ((scanConfigs program (Config.initial σ) labels)[index]?.getD (Config.initial σ)) = false := by
    intro index isUpTo
    have isScanInside : index < (scanConfigs program (Config.initial σ) labels).length := by omega
    have holds := List.all_eq_true.mp isNeverClosed _ (List.getElem_mem isScanInside)
    rw [List.getElem?_eq_getElem isScanInside]
    simpa using holds
  refine ⟨⟨labels.length, fun index isAfter => ?_⟩, fun index => ?_⟩
  · rw [prefixThenTicks_labels_tail program labels index isAfter]
    rfl
  · by_cases isUpTo : index ≤ labels.length
    · rw [prefixThenTicks_states_upTo program labels index isUpTo]
      exact isOpenAt index isUpTo
    · rw [prefixThenTicks_states_tail program labels index (by omega)]
      have isFinalOpen := isOpenAt labels.length (Nat.le_refl _)
      exact isFinalOpen

/-- **The fair-run tool.** A schedule that passes its checks and ends at rest,
followed by `tick 1` forever, gives a fair maximal DexF trace for the
outcome word of its requests, with the marker of the run. -/
theorem prefixThenTicks_isFailureFairMaximalTrace (program : Program σ)
    (labels : List (Action σ)) (events : List (Obs σ)) (final : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, final))
    (passesLabels : schedulePassesLabelChecks labels = true)
    (passesConfigs : schedulePassesConfigChecks program labels = true)
    (isQuiet : isQuietAtRest (scheduleFinal program labels) = true) (marker : Marker)
    (hasMarker : (system program).HasMarker (prefixThenTicks program labels) marker) :
    IsFailureFairMaximalTrace program (Sequence.ofList (labels.filterMap Action.requestOf))
      (Sequence.ofList events) marker :=
  ⟨prefixThenTicks program labels, prefixThenTicks_isRun program labels events final isExec,
    prefixThenTicks_isRunFor program labels,
    (system program).requestsArrive_of_isRunFor _ _ (prefixThenTicks_isRunFor program labels),
    ⟨prefixThenTicks_isFair program labels passesLabels isQuiet,
      prefixThenTicks_isHypothesisRun program labels events final isExec passesLabels
        passesConfigs⟩,
    prefixThenTicks_trace program labels events final isExec, hasMarker⟩

/-- The events of a schedule, or `[]` when a label is not enabled. -/
def scheduleEvents (program : Program σ) (labels : List (Action σ)) : List (Obs σ) :=
  match (system program).exec (system program).init labels with
  | some (events, _) => events
  | none => []

/-- An executable schedule runs to `scheduleEvents` and `scheduleFinal`. -/
theorem exec_of_isSome (program : Program σ) (labels : List (Action σ))
    (isSome : ((system program).exec (system program).init labels).isSome = true) :
    (system program).exec (system program).init labels =
      some (scheduleEvents program labels, scheduleFinal program labels) := by
  obtain ⟨⟨events, final⟩, isExec⟩ := Option.isSome_iff_exists.mp isSome
  obtain ⟨_, isFinal, _⟩ := scanConfigs_of_exec program (Config.initial σ) labels events final isExec
  have isScheduleFinal : scheduleFinal program labels = final := by
    simp [scheduleFinal, isFinal]
  rw [isExec, isScheduleFinal]
  simp [scheduleEvents, isExec]

/-- The fair-run tool with every premise a Boolean check. -/
theorem prefixThenTicks_isFailureFairMaximalTrace_of_checks (program : Program σ)
    (labels : List (Action σ))
    (isSome : ((system program).exec (system program).init labels).isSome = true)
    (passesLabels : schedulePassesLabelChecks labels = true)
    (passesConfigs : schedulePassesConfigChecks program labels = true)
    (isQuiet : isQuietAtRest (scheduleFinal program labels) = true) (marker : Marker)
    (hasMarker : (system program).HasMarker (prefixThenTicks program labels) marker) :
    IsFailureFairMaximalTrace program (Sequence.ofList (labels.filterMap Action.requestOf))
      (Sequence.ofList (scheduleEvents program labels)) marker :=
  prefixThenTicks_isFailureFairMaximalTrace program labels _ _ (exec_of_isSome program labels isSome)
    passesLabels passesConfigs isQuiet marker hasMarker

/-! ## Outcome words and erasures -/

/-- `w↓` of a finite outcome word is `eraseRetries` of its list. -/
theorem isRetryErasure_ofList (requests : List (Request σ)) :
    IsRetryErasure (Sequence.ofList requests) (Sequence.ofList (eraseRetries requests)) := by
  have isNilFrom : ∀ index, requests.length ≤ index →
      retryErasureChunk (Sequence.ofList requests) index = [] := by
    intro index isAfter
    simp [retryErasureChunk, Sequence.ofList, List.getElem?_eq_none isAfter]
  have isConcat := isConcatenation_of_nil_from _ requests.length isNilFrom
  cases requests with
  | nil => exact isConcat
  | cons first laterRequests =>
    have upTo : ∀ count, concatUpTo (retryErasureChunk (Sequence.ofList (first :: laterRequests)))
        (count + 1) = first.toDex ::
          ((laterRequests.take count).filter (fun request => !first.isRetryOf request)).map
            Request.toDex := by
      intro count
      induction count with
      | zero => simp [concatUpTo, retryErasureChunk, Sequence.ofList]
      | succ count inductionHypothesis =>
        rw [concatUpTo, inductionHypothesis, List.take_add_one]
        rcases isLater : laterRequests[count]? with _ | request
        · simp [retryErasureChunk, Sequence.ofList, isLater]
        · by_cases isRetry : first.isRetryOf request = true
          · simp [retryErasureChunk, Sequence.ofList, isLater, isRetry]
          · simp [retryErasureChunk, Sequence.ofList, isLater, isRetry]
    rw [List.length_cons, upTo, List.take_length] at isConcat
    exact isConcat

/-- A non-empty admissible outcome-word prefix is an admissible outcome word. -/
theorem isAdmissibleOutcomeWord_ofList (environment : Dex.Environment σ)
    (requests : List (Request σ)) (isAdmissible : IsAdmissibleOutcomePrefix environment requests)
    (isNonEmpty : requests ≠ []) :
    IsAdmissibleOutcomeWord environment (Sequence.ofList requests) := by
  rcases isAdmissible with isEmpty | ⟨requestId, input, laterRequests, isRequests, isInput, isLater⟩
  · exact absurd isEmpty isNonEmpty
  subst isRequests
  refine ⟨⟨requestId, input, isInput, rfl⟩, ?_, ?_⟩
  · intro position request isRequest
    simp only [Sequence.ofList, List.getElem?_cons_succ] at isRequest
    rcases isLater request (List.mem_of_getElem? isRequest) with isRetry | isPublication
    · exact Or.inl ⟨_, rfl, isRetry⟩
    · exact Or.inr isPublication
  · intro position isNone
    simp only [Sequence.ofList] at isNone ⊢
    exact List.getElem?_eq_none (by
      have := List.getElem?_eq_none_iff.mp isNone
      omega)

/-- Every DexF trace has an erasure `erase(t)`. -/
theorem exists_isRepeatErasure (trace : Sequence (Obs σ)) :
    ∃ erased, IsRepeatErasure trace erased := by
  classical
  obtain ⟨erased, isConcat⟩ := wpf6_exists_isConcatenation (fun position =>
    if IsRepeatedAt trace position then [] else ((trace position).map Obs.toDex).toList)
  exact ⟨erased, _, isConcat, fun position =>
    ⟨fun isRepeated => by simp [isRepeated], fun isNotRepeated => by simp [isNotRepeated]⟩⟩

end DexExpressiveness.DexF.Examples
