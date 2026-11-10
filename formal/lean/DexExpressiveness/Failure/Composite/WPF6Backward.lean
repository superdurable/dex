import DexExpressiveness.Failure.Composite.WPF6Lift
import DexExpressiveness.Failure.Composite.WPF6Sequences

/-!
# WP-F6: liveness, the backward inclusion for words without retries

A fair Dex run for `w↓` lifts step by step to a DexF run with no failure
(`wpf6_lift_step`). When `w` has no retry, `w↓` is `w` without request IDs,
so the lifted run is a run for `w`. It has the Dex trace with ghost indexes,
none of them repeated, the same marker, and it is fair: FF, FR, and FI hold
because nothing fails, and F2 to F5 hold because α is transparent on it.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- **WP-F6, backward.** For an admissible outcome word `w` without retries,
every fair maximal Dex trace for `w↓` is `erase(t)` of a fair maximal DexF
trace `t` for `w` with the same marker. -/
theorem wpf6_liveness_backward (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (word : Sequence (Request σ)) (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (erasedWord : Sequence (Dex.Request σ)) (isErasure : IsRetryErasure word erasedWord)
    (trace : Sequence (Dex.Obs σ)) (marker : Marker) (hasNoRetry : HasNoRetry word)
    (isDexTrace : (Dex.system program.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some
      erasedWord trace marker) :
    ∃ failureTrace, IsFailureFairMaximalTrace program word failureTrace marker ∧
      IsRepeatErasure failureTrace trace := by
  sorry

end DexExpressiveness.DexF
