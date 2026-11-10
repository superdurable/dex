import DexExpressiveness.Failure.Composite.WPF5Forward
import DexExpressiveness.Failure.Composite.WPF5Free

/-!
# Helper lemmas of the main statements

Empty until Stage 2. `Failure/Statements/Main.lean` imports this file. Each
work package that proves a main statement adds its helper files under
`Failure/Composite/`, with names that start with its prefix (`WPF5`, `WPF6`,
`WPF7`), and adds one import line here per file. A helper file may import
`Failure/Statements/Leaf.lean`, never `Failure/Statements/Main.lean`.
-/
