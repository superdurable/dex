import DexExpressiveness.Failure.Composite.WPF7EndToEnd
import DexExpressiveness.Failure.Composite.WPF5Steps
import DexExpressiveness.Failure.Composite.WPF5Forward
import DexExpressiveness.Failure.Composite.WPF5Lookup
import DexExpressiveness.Failure.Composite.WPF5Backward
import DexExpressiveness.Failure.Composite.WPF5BackwardWorker
import DexExpressiveness.Failure.Composite.WPF5BackwardAbandon
import DexExpressiveness.Failure.Composite.WPF5BackwardStart
import DexExpressiveness.Failure.Composite.WPF5BackwardTimer
import DexExpressiveness.Failure.Composite.WPF5BackwardComplete
import DexExpressiveness.Failure.Composite.WPF5Free

/-!
# Helper lemmas of the main statements

Empty until Stage 2. `Failure/Statements/Main.lean` imports this file. Each
work package that proves a main statement adds its helper files under
`Failure/Composite/`, with names that start with its prefix (`WPF5`, `WPF6`,
`WPF7`), and adds one import line here per file. A helper file may import
`Failure/Statements/Leaf.lean`, never `Failure/Statements/Main.lean`.
-/
