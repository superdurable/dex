import DexExpressiveness.Examples.Inductiveness
import DexExpressiveness.Liveness.Statements

/-!
# Executable checks of the liveness definitions

This file is test tooling, not part of the theorem. Liveness (spec §5.5) is
about infinite runs, which cannot be run. The proof of Stage 2b reduces it to
facts about one configuration pair or one step, and these can be tested. The
checks run on the configuration pairs of pseudo-random lockstep runs and on
the changed copies of them that the Boolean mirror of `IsSimulationState`
accepts (`Examples/Inductiveness.lean`).

1. Fair steps (`fairStepFailures`): from a settled pair, `fairActions` of
   every enabled L label are enabled, have the events of the label, and end
   in a settled pair that `checkState` accepts. This is `DexFollowsL`.
2. Fairness correspondence (`correspondenceFailures`): the facts that the
   fairness assumptions F2 to F5 speak about correspond in a pair. A thread
   whose next action has not begun has a pending invocation; a thread that
   issued an emit or began an action has a task; a due timer is a due timer;
   a late emit is an effect of a task.
3. Silent steps (`silentFailures`): every Dex action of a silent kind lowers
   `Dex.Config.silentWeight`, a clock tick keeps it, a Dex step with no L
   label is of a silent kind, and the L labels of a step are progress steps
   exactly when the Dex action is one.
4. Quiescence (`quiescenceFailures`): in a settled pair, L has a step other
   than a clock tick or a request exactly when the translated Flow has one.
   This is what the markers `✓` and `δ` compare.

A pair is settled when no canceled task without an effect and no `Returned`
task is left, as between two blocks of `DexFollowsL`.
-/

namespace DexExpressiveness.Examples

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation

/-- Mirror of `DexFollowsL.isSettled` and `DexFollowsL.noReturnedTask`. -/
def isSettled (program : Program) (config : Dex.Config signature) : Bool :=
  (cleanupActions program config).isEmpty &&
  config.tasks.all (fun task =>
    match task.phase with
    | .returned _ => false
    | _ => true)

/-! ## Check 1: fair steps -/

/-- Run one L label on the L side and its fair actions on the Dex side. -/
def stepFairFromL (program : Program) (pair : ConfigPair) (label : L.Label) : Lockstep :=
  checkAligned "fair step" ((L.system program).exec pair.1 [label])
    ((Dex.system ⟦program⟧).exec pair.2 (fairActions program pair.1 pair.2 label))

/-- The failures of the fair step of every enabled L label from a settled pair. -/
def fairStepFailures (program : Program) (pair : ConfigPair) : List String :=
  (enabledLLabels program pair.1).filterMap (fun label =>
    match checkedStep program s!"fair {describeLabel label}" (stepFairFromL program pair label) with
    | .error message => some message
    | .ok next =>
      if isSettled program next.2 then none
      else some s!"fair {describeLabel label}: the result is not settled")

/-! ## Check 2: fairness correspondence -/

/-- The phases of `L.Fairness.IsThreadPending`. -/
def isBeginPhase : Phase → Bool
  | .bootPending => true
  | .ready => true
  | .woken _ => true
  | _ => false

/-- The phases of `L.Fairness.IsThreadCompleting`. -/
def isCompletingPhase : Phase → Bool
  | .bootRunning => true
  | .emitted => true
  | .waitStarted => true
  | .running _ => true
  | .resuming _ => true
  | _ => false

/-- The phase of `L.Fairness.IsThreadEmitting`. -/
def isEmittingPhase : Phase → Bool
  | .emitting _ => true
  | _ => false

/-- The failures of the correspondence of one live thread with its Step. -/
def threadCorrespondenceFailures (program : Program) (pair : ConfigPair) (thread : L.Thread) :
    List String :=
  match stepOfThread pair.2 thread.core.tid with
  | none => ["correspondence: a live thread has no Step"]
  | some (record, task) =>
    let isPending : Bool :=
      match record.invocation with
      | some { method := _, state := .pending } => true
      | _ => false
    let holdsEffect : Bool :=
      match task with
      | some found => !(pendingEffects ⟦program⟧ found).isEmpty
      | none => false
    failsUnless "correspondence: F3, pending thread and pending invocation"
      (isBeginPhase thread.phase == isPending) ++
    failsUnless "correspondence: F4, emitting thread and task effect"
      (isEmittingPhase thread.phase == holdsEffect) ++
    failsUnless "correspondence: F4 and F5, thread in an action and task"
      ((isEmittingPhase thread.phase || isCompletingPhase thread.phase) == task.isSome)

/-- The failures of the fairness correspondence in a pair. -/
def correspondenceFailures (program : Program) (pair : ConfigPair) : List String :=
  match pair.2.run with
  | none => []
  | some run =>
    match run.status with
    | .running =>
      let threads := pair.1.threads.filter (fun thread => decide (thread.phase ≠ .halted))
      let isDueInL : Nat → Bool := fun number =>
        pair.1.threads.any (fun thread =>
          match thread.phase with
          | .blocked (.timed timerNumber deadline _) _ =>
            timerNumber == number && decide (deadline ≤ pair.1.clock)
          | _ => false)
      threads.flatMap (threadCorrespondenceFailures program pair) ++
      failsUnless "correspondence: every task belongs to a thread"
        (pair.2.tasks.all (fun task => threads.any (fun thread =>
          match stepOfThread pair.2 thread.core.tid with
          | some (_, some found) => found.id == task.id
          | _ => false))) ++
      failsUnless "correspondence: F2, due timers"
        (run.timers.all (fun entry =>
          decide (entry.2 ≤ pair.2.clock) == isDueInL entry.1.step.number) &&
        pair.1.threads.all (fun thread =>
          match thread.phase with
          | .blocked (.timed number deadline _) _ =>
            !decide (deadline ≤ pair.1.clock) ||
            run.timers.any (fun entry =>
              entry.1.step.number == number && decide (entry.2 ≤ pair.2.clock))
          | _ => true))
    | .closed _ =>
      failsUnless "correspondence: F4, late emits and task effects"
        (pair.1.lateEmits.isEmpty ==
          pair.2.tasks.all (fun task => (pendingEffects ⟦program⟧ task).isEmpty))

/-! ## Check 3: silent steps -/

/-- The failures of the silent weight and of the kinds of labels, for every
enabled Dex action. -/
def silentFailures (program : Program) (pair : ConfigPair) : List String :=
  (enabledDexActions program pair.2).flatMap (fun action =>
    match (Dex.system ⟦program⟧).step pair.2 action with
    | none => []
    | some (_, next) =>
      let before := pair.2.silentWeight
      let after := next.silentWeight
      let labels := lLabelsFor program pair.1 pair.2 action
      let isProgress := (Dex.system ⟦program⟧).isProgress action
      let name := describeAction action
      failsUnless s!"silent {name}: the weight does not fall"
        (!action.isSilentKind || decide (after < before)) ++
      failsUnless s!"silent {name}: a tick changes the weight" (isProgress || after == before) ++
      (if breaksMutexRequirement pair.1 then []
       else
        failsUnless s!"silent {name}: no label and not of a silent kind"
          (!labels.isEmpty || action.isSilentKind) ++
        failsUnless s!"silent {name}: a tick has no label" (isProgress || !labels.isEmpty) ++
        failsUnless s!"silent {name}: progress differs"
          (labels.all (fun label => (L.system program).isProgress label == isProgress)) ++
        failsUnless s!"silent {name}: requests differ"
          ((labels.filterMap (L.system program).requestOf).length ==
            ((Dex.system ⟦program⟧).requestOf action).toList.length)))

/-! ## Check 4: quiescence -/

/-- No L step other than a clock tick or a request is enabled, now or after
any wait: after a long tick no thread, no timer, and no late emit can move. -/
def isQuiescentL (program : Program) (config : L.Config) : Bool :=
  let later := { config with clock := config.clock + 1000000000000 }
  (config.threads.map (fun thread => L.Label.thread thread.core.tid) ++
    [L.Label.fire 1, L.Label.lateDeliver 0, L.Label.lateDrop 0]).all (fun label =>
      ((L.system program).step later label).isNone)

/-- No Dex step other than a clock tick or a request is enabled, now or after
any wait. -/
def isQuiescentDex (program : Program) (config : Dex.Config signature) : Bool :=
  let later := { config with clock := config.clock + 1000000000000 }
  (dexCandidates false later).all (fun action =>
    ((Dex.system ⟦program⟧).step later action).isNone)

/-- The failure of the quiescence correspondence in a settled pair. -/
def quiescenceFailures (program : Program) (pair : ConfigPair) : List String :=
  failsUnless
    s!"quiescence: L {isQuiescentL program pair.1}, Dex {isQuiescentDex program pair.2}"
    (isQuiescentL program pair.1 == isQuiescentDex program pair.2)

/-! ## The checks on pairs -/

/-- The failures of the four checks in one pair that `checkState` accepts. -/
def livenessPairFailures (program : Program) (pair : ConfigPair) : List String :=
  silentFailures program pair ++ correspondenceFailures program pair ++
  (if isSettled program pair.2 then
    fairStepFailures program pair ++
    (if breaksMutexRequirement pair.1 then [] else quiescenceFailures program pair)
   else [])

/-- The pairs of a pseudo-random run in which the L side chooses the steps
and the Dex side takes `fairActions`. Every pair is settled. -/
def reachableFairPairs (program : Program) : Nat → Nat → ConfigPair → List ConfigPair
  | 0, _, pair => [pair]
  | fuel + 1, seed, pair =>
    match pick seed (enabledLLabels program pair.1) with
    | none => [pair]
    | some label =>
      match stepFairFromL program pair label with
      | .error _ => [pair]
      | .ok next => pair :: reachableFairPairs program fuel (nextRandom seed) next

/-- The pairs the liveness checks start from: those of the inductiveness fuzz
and every `period`-th pair of a fair run. -/
def livenessPairs (program : Program) (fuel period seed : Nat) : List ConfigPair :=
  let initial : ConfigPair := ((L.system program).init, (Dex.system ⟦program⟧).init)
  sampledPairs program fuel period seed ++
  everyNth period seed (reachableFairPairs program (2 * fuel) (nextRandom (seed + 7)) initial)

/-- Run the four checks on the sampled pairs and on their changed copies that
`checkState` accepts. Returns the failures, without repetitions. -/
def livenessFailures (program : Program) (fuel period seeds : Nat) : List String :=
  ((List.range seeds).flatMap (fun seed =>
    (livenessPairs program fuel period seed).flatMap (fun pair =>
      (if (checkState program pair).isEmpty then livenessPairFailures program pair
       else ["a reachable pair fails checkState"]) ++
      (keptPerturbations program pair).flatMap (fun variant =>
        ((livenessPairFailures program variant.2).take 1).map
          (fun failure => s!"{variant.1}: {failure}"))))).eraseDups

/-- What one run of the liveness checks covers: the pairs checked, the
settled ones among them, and the settled ones after the close. -/
def livenessCoverage (program : Program) (fuel period seeds : Nat) : Nat × Nat × Nat :=
  let pairs := (List.range seeds).flatMap (fun seed =>
    (livenessPairs program fuel period seed).flatMap (fun pair =>
      pair :: (keptPerturbations program pair).map (fun variant => variant.2)))
  let settled := pairs.filter (fun pair => isSettled program pair.2)
  (pairs.length, settled.length,
    (settled.filter (fun pair => decide (pair.1.status = .closed))).length)

open DexExpressiveness.Examples.LockstepChecks in
#guard livenessFailures Mutex.program 60 12 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard livenessFailures TimedSelect.program 60 12 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard livenessFailures signalProgram 80 16 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard livenessFailures twoLateEmitsProgram 40 8 1 == []
open DexExpressiveness.Examples.LockstepChecks in
#guard livenessFailures cancelProgram 60 12 1 == []

end DexExpressiveness.Examples
