import DexExpressiveness.Failure.Check

/-!
# Review probes of Stage 1, kept as regression checks

Test tooling, not part of the theorem. The first review of the Stage 1
definitions found two false statements, each with a configuration or a run
checked in the kernel. This file keeps both.

1. **A Step execution ID reused.** A configuration that the first
   `Invariant` accepted: `Start-1` runs task 0, which has returned with a
   decision that creates another `Start`, and the counter of `Start` is 0. Its
   Complete rule creates a second `Start-1`, so `IsStepSimulated` and
   `Invariant` both fail after it. `RunInvariant.stepNumbersCounted` now
   rejects the configuration (`reusedStepConfig_isNotInvariant`).
2. **Retries move time.** A failure-free run of a Flow with two racing
   timers, and the change of the due order when 1,000,001 microseconds pass
   before the second Step registers its wait. A word with that many StartFlow
   retries between two publications forces that delay, because each retry is
   a log commit that strictly advances the stamp. So the backward inclusion of
   liveness holds only for words without retries (`liveness_failure`).
-/

namespace DexExpressiveness.DexF.Examples.ReviewProbes

open DexExpressiveness DexExpressiveness.DexF DexExpressiveness.Translation

/-! ## A Step execution ID reused -/

/-- The signature of translated programs. -/
abbrev probeSignature := Translation.signature

/-- An Execute that moves to another `Start` Step. -/
def loopResponse : Dex.ExecuteResponse probeSignature :=
  { effects := {}
    decision :=
      { moves := [{ stepType := StepType.start, input := none, options := { skipWaitFor := true } }]
        close := none } }

/-- Every Step moves to another `Start` Step. -/
def loopFlow : Dex.FlowDef probeSignature :=
  { stepDef := fun _ => { waitFor := none, execute := fun _ _ _ _ => ([], loopResponse) } }

/-- The DexF program of the Flow. -/
def loopProgram : Program probeSignature := Program.ofFlow loopFlow

/-- `Start-1`. -/
def loopStep : Dex.StepExecId probeSignature := { stepType := StepType.start, number := 1 }

/-- The arguments of its first start. -/
def loopArguments : Arguments probeSignature :=
  { context := { stepExecutionId := loopStep, fromStepExecutionId := .flowStart }
    input := none, snapshot := { attributes := [] }, conditionResults := none, virtualTaskId := 0 }

/-- Its task, returned. -/
def loopTask : Task probeSignature :=
  { base := loopArguments.toTask (loopStep, .execute) (.returned (.execute loopResponse))
    attemptContext := AttemptContext.first probeSignature
    emittedCount := 0 }

/-- Its history. -/
def loopHistory : InvocationHistory probeSignature :=
  { pendedAt := 0, emittedCount := 0, progress := .returned (.execute loopResponse)
    arguments := some loopArguments, outcome := .live }

/-- Its Step record, running task 0. -/
def loopRecord : Dex.StepRecord probeSignature :=
  { id := loopStep, input := none, options := { skipWaitFor := true }, origin := .flowStart,
    phase := .execute, invocation := some { method := .execute, state := .running 0 },
    waitState := none, conditionResults := none }

/-- The run, with no counter for `Start`. -/
def reusedStepRun : Run probeSignature :=
  { base := { (Dex.Run.empty probeSignature) with steps := [loopRecord], counters := [] }
    attempts := [(loopStep, { (Attempt.fresh .execute) with snapshot := some { attributes := [] } })]
    retryTimers := [], startRequestId := 0 }

/-- The configuration of the review. -/
def reusedStepConfig : Config probeSignature :=
  { (Config.initial probeSignature) with
    run := some reusedStepRun
    tasks := [loopTask], nextTaskId := 1
    ghost := { (Ghost.initial probeSignature) with
      invocations := [((loopStep, .execute), loopHistory)], nextVirtualTaskId := 1 } }

/-- The invariant rejects the configuration: the counter of `Start` is below
the number of `Start-1`. -/
theorem reusedStepConfig_isNotInvariant : ¬ Invariant loopProgram reusedStepConfig := by
  intro invariant
  have isCounted := (invariant.runInvariant reusedStepRun rfl).stepNumbersCounted loopRecord
    (List.mem_cons_self ..)
  exact (by decide :
    ¬ loopRecord.id.number ≤ (reusedStepRun.base.counters.get? loopRecord.id.stepType).getD 0)
    isCounted

/-- The Boolean mirror rejects it for the same clause only. -/
theorem reusedStepConfig_failingClauses :
    checkInvariant loopProgram reusedStepConfig = ["stepNumbersCounted"] := by
  decide +kernel

/-- Its Complete rule is enabled and not simulated: the first `Invariant`
was too weak for `normalStep_simulated`. -/
theorem reusedStepConfig_isNotSimulated :
    (step loopProgram reusedStepConfig (.stepComplete 0)).isSome = true ∧
      checkStepSimulated loopProgram reusedStepConfig (.stepComplete 0) = false := by
  decide +kernel

/-! ## Retries move time -/

/-- A small signature with natural numbers for every name. -/
abbrev timingSignature : Dex.Signature :=
  { StepType := Nat, Chan := Nat, Key := Nat, Val := Nat, Fx := Nat, MapSelection := Unit
    isMapInstanceKey := fun _ => false, isSelectedBy := fun _ _ => false
    isTimerIdAtMost := fun firstType _ _ secondType _ _ => decide (firstType ≤ secondType) }

/-- Step 0 starts Steps 1, 2, and 3. Step 1 (A) writes the Attribute 0 and
waits for all of a 1-second timer and Channel 0. Step 2 emits `10` when it
reads the Attribute. Step 3 waits for Channel 1 and starts Step 4 (B), which
waits for all of a 0-second timer and Channel 0 and then emits `20`. -/
def timingFlow : Dex.FlowDef timingSignature :=
  { stepDef := fun stepType =>
      match stepType with
      | 0 =>
        { waitFor := none
          execute := fun _ _ _ _ =>
            ([], { effects := {}
                   decision :=
                     { moves :=
                         [{ stepType := 1, input := none, options := { skipWaitFor := false } },
                          { stepType := 2, input := none, options := { skipWaitFor := true } },
                          { stepType := 3, input := none, options := { skipWaitFor := false } }]
                       close := none } }) }
      | 1 =>
        { waitFor := some (fun _ _ _ =>
            ([], { effects := { writes := [(0, some 1)] }
                   wait := { kind := .all, timerSeconds := [1], channels := [0] } }))
          execute := fun _ _ _ _ =>
            ([30], { effects := {}, decision := { moves := [], close := some .deadEnd } }) }
      | 2 =>
        { waitFor := none
          execute := fun _ _ snapshot _ =>
            ((if snapshot.attributes.get? 0 = some 1 then [10] else []),
              { effects := {}, decision := { moves := [], close := some .deadEnd } }) }
      | 3 =>
        { waitFor := some (fun _ _ _ =>
            ([], { effects := {}, wait := { kind := .all, timerSeconds := [], channels := [1] } }))
          execute := fun _ _ _ _ =>
            ([], { effects := {}
                   decision :=
                     { moves := [{ stepType := 4, input := none, options := { skipWaitFor := false } }]
                       close := none } }) }
      | _ =>
        { waitFor := some (fun _ _ _ =>
            ([], { effects := {}, wait := { kind := .all, timerSeconds := [0], channels := [0] } }))
          execute := fun _ _ _ _ =>
            ([20], { effects := {}, decision := { moves := [], close := some .deadEnd } }) } }

/-- Start, run Steps 0 to 3, and publish to Channel 0. -/
def timingPrefix : List (Dex.Action timingSignature) :=
  [.request (.startFlow 0 0), .tick 1, .stepStart ⟨0, 1⟩, .workerRun 0, .workerReturn 0,
   .tick 1, .stepComplete 0,
   .tick 1, .stepStart ⟨1, 1⟩, .workerRun 1, .workerReturn 1, .tick 1, .stepComplete 1,
   .tick 1, .stepStart ⟨2, 1⟩, .workerRun 2, .workerEffect 2, .workerReturn 2,
   .tick 1, .stepComplete 2,
   .tick 1, .stepStart ⟨3, 1⟩, .workerRun 3, .workerReturn 3, .tick 1, .stepComplete 3,
   .tick 1, .request (.publish 0 7)]

/-- Publish to Channel 1, which starts B, and let B register its wait. -/
def timingUntilB : List (Dex.Action timingSignature) :=
  [.tick 1, .request (.publish 1 8),
   .tick 1, .stepStart ⟨3, 1⟩, .workerRun 4, .workerReturn 4, .tick 1, .stepComplete 4,
   .tick 1, .stepStart ⟨4, 1⟩, .workerRun 5, .workerReturn 5, .tick 1, .stepComplete 5]

/-- Fire B's timer, which takes the message of Channel 0, run B, and fire A's timer. -/
def timingFinish : List (Dex.Action timingSignature) :=
  [.tick 1, .timerFire 1, .tick 1, .stepStart ⟨4, 1⟩, .workerRun 6,
   .workerEffect 6, .workerReturn 6, .tick 1, .stepComplete 6,
   .tick 1000000, .timerFire 1]

/-- The failure-free trace: B takes the only message of Channel 0 and emits
`20`. -/
theorem retryTiming_freeTrace :
    ((Dex.system timingFlow).exec (Dex.Config.initial timingSignature)
        (timingPrefix ++ timingUntilB ++ timingFinish)).map Prod.fst =
      some [.start 0 0, .fx ⟨2, 1⟩ .execute 10, .pub 0 7, .pub 1 8, .fx ⟨4, 1⟩ .execute 20] := by
  decide +kernel

/-- The Step types of the due timers just after B registers its wait. -/
def dueStepTypesAfter (delay : List (Dex.Action timingSignature)) : Option (List Nat) :=
  ((Dex.system timingFlow).exec (Dex.Config.initial timingSignature)
      (timingPrefix ++ delay ++ timingUntilB)).bind (fun result =>
    result.2.run.map (fun flowRun =>
      (flowRun.dueTimers (result.2.clock + 1)).map (fun timer => timer.step.stepType)))

/-- Without a delay only B's timer is due; after 1,000,001 microseconds A's
timer is due first. Every later [TIMER-FIRE] then serves A before B, and A
takes the only message of Channel 0. -/
theorem retryTiming_delayChangesDueOrder :
    dueStepTypesAfter [] = some [4] ∧ dueStepTypesAfter [.tick 1000001] = some [1, 4] := by
  decide +kernel

end DexExpressiveness.DexF.Examples.ReviewProbes
