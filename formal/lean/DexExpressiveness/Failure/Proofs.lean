import DexExpressiveness.Failure.Proofs.WPF6Progress
import DexExpressiveness.Failure.Proofs.WPF6FrameTasks
import DexExpressiveness.Failure.Proofs.WPF6FrameAttempts

/-!
# Proofs of the leaf statements

Empty until Stage 2. `Failure/Statements/Leaf.lean` imports this file. Each
work package that proves a leaf statement `name` adds a file under
`Failure/Proofs/` with `theorem name_proof` of the same type, which imports
definitions only (never `Failure/Statements/Leaf.lean`), and adds one import
line here. The expected files:

| File | Package | Proves |
| --- | --- | --- |
| `Failure/Proofs/WPF1NormalSteps.lean` | WP-F1 | `normalStep_simulated_proof` |
| `Failure/Proofs/WPF2FailureSteps.lean` | WP-F2 | `failureStep_isStutter_proof` |
| `Failure/Proofs/WPF3WorkerSteps.lean` | WP-F3 | `workerStep_simulated_proof`, `effectHistory_of_exec_proof` |
| `Failure/Proofs/WPF4Invariant.lean` | WP-F4 | `invariant_step_proof` |
| `Failure/Proofs/WPF6Progress.lean` | WP-F6 | `isProgress_eq_dexProgress_proof` |

A package may split its proof into more files under `Failure/Proofs/` whose
names start with its prefix (`WPF1`, `WPF2`, …), and import each here.
-/
