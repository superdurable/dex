import DexExpressiveness.Examples.Inductiveness

/-!
# The inductiveness fuzz as a program

`lake exe inductiveness-fuzz [fuel period walkLength seeds]` runs the fuzz of
`DexExpressiveness/Examples/Inductiveness.lean` compiled, on every example
program, and prints the failures and the coverage. It exits with status 1
when a failure was found. This file is test tooling, not part of the theorem.
-/

open DexExpressiveness DexExpressiveness.Examples DexExpressiveness.Examples.LockstepChecks

/-- The example programs the fuzz runs on. -/
def fuzzPrograms : List (String × L.Program) :=
  [("Mutex", Mutex.program), ("TimedSelect", TimedSelect.program),
   ("recursion", recursionProgram), ("exception", exceptionProgram),
   ("lostUpdate", lostUpdateProgram), ("broadcast", broadcastProgram),
   ("signal", signalProgram), ("selectRace", selectRaceProgram),
   ("lateEmit", lateEmitProgram), ("twoLateEmits", twoLateEmitsProgram),
   ("newMutex", newMutexProgram),
   ("cancel", cancelProgram), ("typeError", typeErrorProgram)]

/-- Add the counts of `more` to `counts`. -/
def addCounts (counts more : AssocList String Nat) : AssocList String Nat :=
  more.foldl (fun total entry => total.set entry.1 ((total.get? entry.1).getD 0 + entry.2)) counts

/-- Run the fuzz on every example program. -/
def main (arguments : List String) : IO UInt32 := do
  let numbers := arguments.filterMap String.toNat?
  let fuel := numbers.getD 0 120
  let period := numbers.getD 1 5
  let walkLength := numbers.getD 2 6
  let seeds := numbers.getD 3 2
  IO.println s!"fuel {fuel}, period {period}, walk length {walkLength}, seeds {seeds}"
  let mut failureCount := 0
  let mut kept : AssocList String Nat := []
  let mut steps : AssocList String Nat := []
  let mut pairCount := 0
  let mut triedCount := 0
  for (name, program) in fuzzPrograms do
    let coverage := fuzzCoverage program fuel period seeds
    let failures := inductivenessFailures program fuel period walkLength seeds
    let keptCount := (coverage.keptByPerturbation.map (fun entry => entry.2)).sum
    IO.println s!"{name}: {coverage.sampledPairs} pairs, {keptCount} of {coverage.triedPerturbations} changed copies kept, {failures.length} failures"
    for failure in failures do
      IO.println s!"  {failure}"
    failureCount := failureCount + failures.length
    kept := addCounts kept coverage.keptByPerturbation
    steps := addCounts steps coverage.checkedSteps
    pairCount := pairCount + coverage.sampledPairs
    triedCount := triedCount + coverage.triedPerturbations
  IO.println s!"total: {pairCount} pairs, {triedCount} changed copies, {failureCount} failures"
  IO.println "kept changed copies, by perturbation:"
  for entry in kept do
    IO.println s!"  {entry.1}: {entry.2}"
  IO.println "single steps checked from kept copies, by kind:"
  for entry in steps do
    IO.println s!"  {entry.1}: {entry.2}"
  return (if failureCount = 0 then 0 else 1)
