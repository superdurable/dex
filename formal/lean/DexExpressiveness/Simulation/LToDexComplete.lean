import DexExpressiveness.Simulation.LToDexBasicWake

/-!
# L to Dex: the registration of a wait and the completion of an action

Work package 3 of the safety proof, the largest. Each lemma shows that one
kind of L thread step is simulated by exactly the Dex actions that
`simulatingActions` names (`IsSimulatedByActions`): the Worker steps that run
and return the handler (`workerRun`, `workerReturn`, as far as they have not
happened), then one [STEP-COMPLETE-WAITFOR] or [STEP-COMPLETE-EXECUTE].

| L thread phase | L action | Dex commit |
| --- | --- | --- |
| `waitStarted` | register the wait (`Config.registerWait`): the atomic synchronization action, or Arm | [STEP-COMPLETE-WAITFOR], `register(e, W)` |
| `bootRunning` | finish the start action | [STEP-COMPLETE-EXECUTE] of the start Step |
| `running`, `emitted` | complete the statement (`Config.applyIntent`) | [STEP-COMPLETE-EXECUTE], `effects` then `decide` |
| `resuming` | complete the continuation of a wait (`Config.applyIntent`) | [STEP-COMPLETE-EXECUTE] |

The completion lemmas go through every `Intent`, which is every row of the
mapping table of spec §8.3 (`encodeIntent`). The package can be split in two:
`l_thread_register_simulated`, and the three completion lemmas.

Every lemma here is `sorry`; the README lists them. The proofs may add lemmas
to this file and to `Simulation/RelationLemmas.lean`. They may not change
`Simulation/Relation.lean`, `L/Invariant.lean`, `Invariants.lean`, or any
model file.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The registration of a wait is simulated by [STEP-COMPLETE-WAITFOR]. The
checks run in the same order on both sides: the operation's own Channel, then
the cancel flag, then block (spec §8.3 "AnyOf at registration"). -/
theorem l_thread_register_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (isWaitStarted : thread.phase = .waitStarted)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  sorry

/-- The end of the program's start action is simulated by
[STEP-COMPLETE-EXECUTE] of the start Step, which publishes the tokens of the
statically known mutexes and moves to the first program point of `main`. -/
theorem l_thread_boot_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (isBootRunning : thread.phase = .bootRunning)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  sorry

/-- The completion of a statement is simulated by [STEP-COMPLETE-EXECUTE]:
the Dex effects and decision of `encodeIntent` do to the run what
`Config.applyIntent` does to the L configuration. -/
theorem l_thread_complete_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (isCompleting : (∃ loaded, thread.phase = .running loaded) ∨ thread.phase = .emitted)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  sorry

/-- The completion of the continuation of a wait is simulated by
[STEP-COMPLETE-EXECUTE] of the waiting Step or of the `Timed` Step. -/
theorem l_thread_resume_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (outcome : WakeOutcome) (isResuming : thread.phase = .resuming outcome)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  sorry

end DexExpressiveness
