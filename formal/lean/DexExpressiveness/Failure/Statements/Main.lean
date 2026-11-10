import DexExpressiveness.Failure.Statements.Leaf
import DexExpressiveness.Failure.Composite
import DexExpressiveness.Theorems

/-!
# Failure transparency: the main statements (spec §11.5, plan §5)

"Failures are transparent: they change no observable event except that
external effects may repeat" (spec §11). The statements of this file are
composed from the leaf statements of `Failure/Statements/Leaf.lean`:

| Package | Statements |
| --- | --- |
| WP-F5, safety and `Tr_free ⊆ Tr_fail` | `freeFiniteTrace_isFailureFiniteTrace`, `safety_failure_forward`, `safety_failure_repetition` |
| WP-F6, liveness | `liveness_failure` |
| WP-F7, the end-to-end corollary | `endToEnd_safety`, `endToEnd_liveness` |

A work package proves its statements in place, here, with helper lemmas in
new files under `Failure/Composite/`. A helper file may import
`Failure/Statements/Leaf.lean` but never this file. The package adds one
import line to `Failure/Composite.lean` per helper file. WP-F7 may use the
statements of WP-F5 and WP-F6 directly, because they come first in this
file. No package changes a definition or the text of a statement. If a
statement is false, it stops and reports a counterexample.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## WP-F5: safety -/

/-- **WP-F5, the Lemma (no failures)** (spec §11.5): `Tr_free ⊆ Tr_fail`.
"Every Dex run is a DexF run with no failure transition. This is not
literally trivial, because DexF carries more state." The DexF run gives the
start the request ID it likes; its trace erases to the Dex trace. -/
theorem freeFiniteTrace_isFailureFiniteTrace (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (requests : List (Dex.Request σ)) (trace : List (Dex.Obs σ))
    (isFree : IsFreeFiniteTrace program.failureFreeFlow environment requests trace) :
    ∃ failureRequests failureTrace,
      IsFailureFiniteTrace program environment failureRequests failureTrace ∧
      eraseRetries failureRequests = requests ∧ failureTrace.map Obs.toDex = trace := by
  obtain ⟨dexLabels, dexFinal, isDexExec, isRequests, isAdmissible⟩ := isFree
  obtain ⟨labels, events, final, isExec, isMapped, isLiftedRequests, hasNoExhaustion,
    hasNoNonRetryable⟩ :=
    wpf5_free_exec program ignoresAttemptContext dexLabels (Config.initial σ) trace dexFinal
      (invariant_initial program) wpf5_shape_initial
      (by rw [wpf5_abstractConfig_initial program]; exact isDexExec)
  rw [← isRequests] at isLiftedRequests
  obtain ⟨isErased, isAdmissibleOutcome⟩ := wpf5_eraseRetries_lift environment requests isAdmissible
  refine ⟨labels.filterMap Action.requestOf, events,
    ⟨labels, final, isExec, hasNoExhaustion, hasNoNonRetryable, rfl, ?_⟩, ?_, isMapped⟩
  · rw [isLiftedRequests]
    exact isAdmissibleOutcome
  · rw [isLiftedRequests]
    exact isErased

/-- **WP-F5, safety, forward** (spec §11.5): "For every program `P` of the
fragment and every finite DexF trace `t` for an outcome word `w`, `erase(t)`
is a finite Dex trace for `w↓`." -/
theorem safety_failure_forward (program : Program σ)
    (hasNoFailurePolicy : HasNoFailurePolicy program) (isSyncOnly : IsSyncOnly program)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (requests : List (Request σ)) (trace : List (Obs σ))
    (isTrace : IsFailureFiniteTrace program environment requests trace) :
    IsFreeFiniteTrace program.failureFreeFlow environment (eraseRetries requests)
      (eraseRepeats trace) := by
  obtain ⟨labels, final, isExec, hasNoExhaustion, _, isRequests, isAdmissible⟩ := isTrace
  obtain ⟨dexLabels, isDexExec, isDexRequests⟩ :=
    wpf5_exec_forward program ignoresAttemptContext labels (Config.initial σ) [] trace final
      (invariant_initial program) ⟨[], rfl⟩ hasNoExhaustion isExec
  refine ⟨dexLabels, abstractConfig final, ?_, ?_, ?_⟩
  · rw [← wpf5_abstractConfig_initial program]
    exact isDexExec
  · rw [isDexRequests, isRequests]
    exact wpf5_requests_beforeStart program environment labels (Config.initial σ) trace final rfl
      isExec (isRequests ▸ isAdmissible)
  · exact wpf5_isAdmissiblePrefix_eraseRetries environment requests isAdmissible

/-- **WP-F5, safety, the repetition form** (spec §11.5): "Hence `t` is a
repetition of a finite Dex trace." Stated over DexF runs and under H3: the
inserted events equal earlier ones in label only because every attempt
computes the same effect list. -/
theorem safety_failure_repetition (program : Program σ)
    (hasNoFailurePolicy : HasNoFailurePolicy program) (isSyncOnly : IsSyncOnly program)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (requests : List (Request σ)) (trace : List (Obs σ))
    (isTrace : IsFailureFiniteTrace program environment requests trace) :
    ∃ freeTrace,
      IsFreeFiniteTrace program.failureFreeFlow environment (eraseRetries requests) freeTrace ∧
      IsRepetition (trace.map Obs.toDex) freeTrace := by
  refine ⟨eraseRepeats trace,
    safety_failure_forward program hasNoFailurePolicy isSyncOnly ignoresAttemptContext environment
      requests trace isTrace, ?_⟩
  obtain ⟨labels, final, isExec, _⟩ := isTrace
  have history := effectHistory_of_exec program ignoresAttemptContext labels trace final isExec
  exact wpf5_isRepetition_eraseRepeats trace
    (wpf5_effectLabels_consistent program final trace history) [] trace rfl

/-! ## WP-F6: liveness -/

/-- **WP-F6, liveness** (spec §11.5), as two inclusions.

- **Forward, for every admissible outcome word `w`:** `erase(t)` of every
  fair maximal DexF trace `t` for `w`, with its marker, is a fair maximal Dex
  trace for `w↓` with the same marker. The DexF marker is decided by the
  progress rule of spec §11.5 (`Action.isProgress`).
- **Backward, for words without retries:** every fair maximal Dex trace for
  `w↓` is `erase(t)` of a fair maximal DexF trace `t` for `w` with the same
  marker; the run with no failure gives it.

Spec §11.5 states an equality for every admissible word. The backward
inclusion is false for words with retries: a deduplicated StartFlow retry
is a log commit whose stamp strictly advances the partition clock (spec
§11.3, "Log commit versus run `seq`"), so a word with enough retries between
two publications forces time past a wait timer's fire time. A failure-free
run that fires a timer armed later first, because the other is not yet due,
then has no DexF counterpart for that word. The retries cannot be modeled
without stamps, because the code stamps them. -/
theorem liveness_failure (program : Program σ)
    (hasNoFailurePolicy : HasNoFailurePolicy program) (isSyncOnly : IsSyncOnly program)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (word : Sequence (Request σ)) (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (erasedWord : Sequence (Dex.Request σ)) (isErasure : IsRetryErasure word erasedWord)
    (trace : Sequence (Dex.Obs σ)) (marker : Marker) :
    ((∃ failureTrace, IsFailureFairMaximalTrace program word failureTrace marker ∧
        IsRepeatErasure failureTrace trace) →
      (Dex.system program.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some erasedWord trace
        marker) ∧
    (HasNoRetry word →
      (Dex.system program.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some erasedWord trace
        marker →
      ∃ failureTrace, IsFailureFairMaximalTrace program word failureTrace marker ∧
        IsRepeatErasure failureTrace trace) := by
  refine ⟨?_, ?_⟩
  · intro hasFailureTrace
    obtain ⟨failureTrace, isFailureTrace, isErased⟩ := hasFailureTrace
    exact wpf6_liveness_forward program ignoresAttemptContext environment word isAdmissible
      erasedWord isErasure trace marker failureTrace isFailureTrace isErased
  · intro hasNoRetry isDexTrace
    exact wpf6_liveness_backward program ignoresAttemptContext environment word isAdmissible
      erasedWord isErasure trace marker hasNoRetry isDexTrace

/-! ## WP-F7: the end-to-end corollary -/

open DexExpressiveness.L DexExpressiveness.Translation

/-- **WP-F7, end to end, safety** (spec §11.5, "End-to-end corollary"): for
every well-formed L program `S`, "every L trace of `S` is in
`π(Tr_fail(⟦S⟧))`", and "every trace in `π(Tr_fail(⟦S⟧))` is an L trace of
`S` with inserted `out(x)` events, each equal in value to an earlier
`out(x)`". It composes the failure theorem with `expressiveness_of_isWellFormed`
(`Theorems.lean`). -/
theorem endToEnd_safety (program : L.Program) (isWellFormed : program.IsWellFormed) :
    (∀ lTrace, IsLFiniteTrace program lTrace →
      ∃ requests failureTrace,
        IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests failureTrace ∧
        failureTrace.filterMap (fun event => project event.toDex) = lTrace) ∧
    (∀ requests failureTrace,
      IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests failureTrace →
      ∃ lTrace, IsLFiniteTrace program lTrace ∧
        IsOutRepetition (failureTrace.filterMap (fun event => project event.toDex)) lTrace) :=
  wpf7_endToEnd_safety_of program isWellFormed
    (fun requests trace isFree =>
      freeFiniteTrace_isFailureFiniteTrace (Program.ofFlow ⟦program⟧)
        (wpf7_ignoresAttemptContext_ofFlow ⟦program⟧) environment requests trace isFree)
    (fun requests trace isTrace =>
      safety_failure_repetition (Program.ofFlow ⟦program⟧)
        (wpf7_hasNoFailurePolicy_ofFlow ⟦program⟧) (wpf7_isSyncOnly_ofFlow ⟦program⟧)
        (wpf7_ignoresAttemptContext_ofFlow ⟦program⟧) environment requests trace isTrace)

/-- **WP-F7, end to end, liveness**: for every well-formed L program `S`,
every admissible L word, and every admissible outcome word `w` whose `w↓` is
the encoded L word: `π(erase(t))` of every fair maximal DexF trace `t` of
`⟦S⟧` for `w`, with its marker, is a fair maximal L trace of `S`; and, when
`w` has no retry, every fair maximal L trace of `S` is such a `π(erase(t))`.
It composes `liveness_failure` with `liveness_of_isWellFormed`. The backward
part needs a word without retries for the reason `liveness_failure` gives. -/
theorem endToEnd_liveness (program : L.Program) (isWellFormed : program.IsWellFormed)
    (lWord : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord lWord)
    (word : Sequence (Request signature))
    (isAdmissibleOutcome : IsAdmissibleOutcomeWord environment word)
    (isErasure : IsRetryErasure word (lWord.map encodeRequest))
    (trace : Sequence L.Event) (marker : Marker) :
    ((∃ failureTrace,
        IsFailureFairMaximalTrace (Program.ofFlow ⟦program⟧) word failureTrace marker ∧
        IsErasedProjection project failureTrace trace) →
      IsLFairMaximalTrace program lWord trace marker) ∧
    (HasNoRetry word → IsLFairMaximalTrace program lWord trace marker →
      ∃ failureTrace,
        IsFailureFairMaximalTrace (Program.ofFlow ⟦program⟧) word failureTrace marker ∧
        IsErasedProjection project failureTrace trace) :=
  wpf7_endToEnd_liveness_of program isWellFormed lWord isAdmissible word trace marker
    (fun dexTrace =>
      liveness_failure (Program.ofFlow ⟦program⟧) (wpf7_hasNoFailurePolicy_ofFlow ⟦program⟧)
        (wpf7_isSyncOnly_ofFlow ⟦program⟧) (wpf7_ignoresAttemptContext_ofFlow ⟦program⟧)
        environment word isAdmissibleOutcome (lWord.map encodeRequest) isErasure dexTrace marker)

end DexExpressiveness.DexF
