import DexExpressiveness.Failure.Examples.Fuzz

/-!
# The failure inductiveness fuzz as a program

`lake exe failure-fuzz [fuel period budget seeds]` runs the fuzz of
`DexExpressiveness/Failure/Examples/Fuzz.lean` compiled, on every example
program: pseudo-random DexF walks with failures, and single steps from
changed copies of their configurations. It prints the failures and the
coverage, and exits with status 1 when a failure was found. This file is test
tooling, not part of the theorem.
-/

open DexExpressiveness DexExpressiveness.DexF DexExpressiveness.DexF.Examples
open DexExpressiveness.Translation

/-- Add the counts of `more` to `counts`. -/
def addFailureCounts (counts more : AssocList String Nat) : AssocList String Nat :=
  more.foldl (fun total entry => total.set entry.1 ((total.get? entry.1).getD 0 + entry.2)) counts

/-- Run the fuzz on every example program. -/
def main (arguments : List String) : IO UInt32 := do
  let numbers := arguments.filterMap String.toNat?
  let fuel := numbers.getD 0 200
  let period := numbers.getD 1 7
  let budget := numbers.getD 2 8
  let seeds := numbers.getD 3 4
  IO.println s!"fuel {fuel}, period {period}, failure budget {budget}, seeds {seeds}"
  let mut failureCount := 0
  let mut stepCounts : AssocList String Nat := []
  let mut keptCounts : AssocList String Nat := []
  let mut totalSteps := 0
  let mut totalSamples := 0
  let mut totalTried := 0
  let mut totalChecked := 0
  let flows := if arguments.contains "planted" then [("attemptSensitive (planted fault)", attemptSensitiveProgram)] else failureFuzzFlows
  for (name, failureProgram) in flows do
    let walked := walks failureProgram fuel period budget seeds
    let perturbed := perturbationChecks failureProgram walked.samples
    let steps := (walked.steps.map (fun entry => entry.2)).sum
    let keptCount := (perturbed.kept.map (fun entry => entry.2)).sum
    IO.println s!"{name}: {steps} walk steps, {walked.samples.length} samples, {keptCount} of {perturbed.tried} changed copies kept, {perturbed.checkedSteps} steps from kept copies, {walked.failures.length + perturbed.failures.length} failures"
    for failure in (walked.failures ++ perturbed.failures).take 20 do
      IO.println s!"  {failure}"
    failureCount := failureCount + walked.failures.length + perturbed.failures.length
    stepCounts := addFailureCounts stepCounts walked.steps
    totalSteps := totalSteps + steps
    totalSamples := totalSamples + walked.samples.length
    totalTried := totalTried + perturbed.tried
    totalChecked := totalChecked + perturbed.checkedSteps
    keptCounts := addFailureCounts keptCounts perturbed.kept
  IO.println s!"total: {totalSteps} walk steps, {totalSamples} samples, {totalTried} changed copies, {totalChecked} steps from kept copies, {failureCount} failures"
  IO.println "walk steps, by kind:"
  for entry in stepCounts do
    IO.println s!"  {entry.1}: {entry.2}"
  IO.println "kept changed copies, by perturbation:"
  for entry in keptCounts do
    IO.println s!"  {entry.1}: {entry.2}"
  return (if failureCount = 0 then 0 else 1)
