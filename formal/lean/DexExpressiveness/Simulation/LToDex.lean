import DexExpressiveness.Simulation.LToDexBasic
import DexExpressiveness.Simulation.LToDexComplete

/-!
# L to Dex: every L step is simulated by the translated Flow

`l_step_simulatedByActions` is proved here from the case lemmas of
`Simulation/LToDexBasic.lean` and `Simulation/LToDexComplete.lean`, by cases
on the label and on the phase of the stepping thread. It names the Dex
actions (`simulatingActions`). `l_step_simulated` is its corollary without
the names, which `FiniteTraces.lean` uses. This file has no `sorry` of its
own.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- L to Dex, with the Dex actions named: every L step from related
configurations that satisfy their invariants is simulated by the actions of
`simulatingActions`, with the same projected events, and the results are
related. Well-formedness is not needed here: L gives no action to a thread
that would break a mutex requirement. -/
theorem l_step_simulatedByActions (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (label : L.Label) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig label = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig label lEvents lNext := by
  cases label with
  | tick duration => exact l_tick_simulated program lConfig dexConfig state duration _ _ isStep
  | request request =>
    cases request with
    | start input => exact l_start_simulated program lConfig dexConfig state input _ _ isStep
    | input chan value =>
      exact l_input_simulated program lConfig dexConfig state chan value _ _ isStep
  | fire count => exact l_fire_simulated program lConfig dexConfig state count _ _ isStep
  | lateDeliver index =>
    exact l_lateDeliver_simulated program lConfig dexConfig state index _ _ isStep
  | lateDrop index => exact l_lateDrop_simulated program lConfig dexConfig state index _ _ isStep
  | thread tid =>
    cases foundCase : lConfig.findThread? tid with
    | none => simp [L.system, L.step, foundCase] at isStep
    | some thread =>
      cases phaseCase : thread.phase with
      | bootPending =>
        exact l_thread_begin_simulated program lConfig dexConfig state tid thread foundCase
          (Or.inl phaseCase) _ _ isStep
      | ready =>
        exact l_thread_begin_simulated program lConfig dexConfig state tid thread foundCase
          (Or.inr (Or.inl phaseCase)) _ _ isStep
      | woken outcome =>
        exact l_thread_begin_simulated program lConfig dexConfig state tid thread foundCase
          (Or.inr (Or.inr ⟨outcome, phaseCase⟩)) _ _ isStep
      | emitting effect =>
        exact l_thread_deliver_simulated program lConfig dexConfig state tid thread foundCase
          effect phaseCase _ _ isStep
      | waitStarted =>
        exact l_thread_register_simulated program lConfig dexConfig state tid thread foundCase
          phaseCase _ _ isStep
      | bootRunning =>
        exact l_thread_boot_simulated program lConfig dexConfig state tid thread foundCase
          phaseCase _ _ isStep
      | running loaded =>
        exact l_thread_complete_simulated program lConfig dexConfig state tid thread foundCase
          (Or.inl ⟨loaded, phaseCase⟩) _ _ isStep
      | emitted =>
        exact l_thread_complete_simulated program lConfig dexConfig state tid thread foundCase
          (Or.inr phaseCase) _ _ isStep
      | resuming outcome =>
        exact l_thread_resume_simulated program lConfig dexConfig state tid thread foundCase
          outcome phaseCase _ _ isStep
      | blocked on since =>
        simp [L.system, L.step, foundCase, stepThread, phaseCase] at isStep
      | halted => simp [L.system, L.step, foundCase, stepThread, phaseCase] at isStep

/-- L to Dex: every L step from related configurations that satisfy their
invariants is simulated by a finite sequence of Dex actions with the same
projected events, and the results are related. -/
theorem l_step_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (label : L.Label) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig label = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig label lEvents lNext :=
  (l_step_simulatedByActions program lConfig dexConfig state label lEvents lNext
    isStep).isSimulatedByDex

end DexExpressiveness
