import DexExpressiveness.Failure.Composite.WPF6Run
import DexExpressiveness.Failure.Composite.WPF6Sequences

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
  sorry

end DexExpressiveness.DexF
