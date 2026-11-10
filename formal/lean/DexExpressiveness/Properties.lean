import DexExpressiveness.Translation.Projection

/-!
# Proved facts about the models and the translation

Everything in this file is proved; it has no `sorry`.

- Well-formedness of the translation (spec §2.5): every Execute of `⟦S⟧`
  returns a well-formed decision, and every move's `skipWaitFor` agrees with
  the Step definition it targets.
- Determinism: each rule's conclusion is a function of the configuration and
  the label (spec §4.1).
- Sanity lemmas: a tick is always enabled (spec §5.5: "[TICK] is always
  enabled"); the projection of request events; the admissible words of spec
  §5.3 are exactly the encoded L words.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Well-formedness of the translation (spec §2.5) -/

/-- The decision of every row of the mapping table is well formed (spec
§2.5): it has moves and no close, or a close and no moves. -/
theorem encodeIntent_isWellFormed (snapshot : Dex.Snapshot signature) (result : StatementResult) :
    (encodeIntent snapshot result).decision.IsWellFormed := by
  unfold encodeIntent
  cases result.intent <;>
    simp only [moveDecision, closeDecision, Dex.Decision.IsWellFormed] <;>
    first
      | (left; simp; done)
      | (right; simp; done)
      | (split <;> left <;> simp)

/-- Every Execute handler of a translated program returns a well-formed
decision (spec §2.5), for every context, input, snapshot, and condition
results. So no Worker response of `⟦S⟧` fails validation. -/
theorem translate_execute_isWellFormed (program : Program) (stepType : StepType)
    (context : Dex.Context signature) (input : Option DexVal)
    (snapshot : Dex.Snapshot signature) (results : Option (Dex.ConditionResults signature)) :
    ((⟦program⟧.stepDef stepType).execute context input snapshot results).2.decision.IsWellFormed := by
  cases stepType with
  | start =>
    simp only [translate, startStepDef]
    split
    · left; simp
    · right; simp [closeDecision]
  | point code =>
    simp only [translate, pointStepDef]
    split
    · split
      · exact encodeIntent_isWellFormed _ _
      · split <;> exact encodeIntent_isWellFormed _ _
    · right; simp [closeDecision]
  | timed =>
    simp only [translate, timedStepDef]
    split
    · exact encodeIntent_isWellFormed _ _
    · right; simp [closeDecision]

/-- A move agrees with its target Step definition: it skips WaitFor exactly
when the definition has none (spec Appendix A, assumption 20). -/
def MoveAgreesWithStepDef (program : Program) (move : Dex.Move signature) : Prop :=
  move.skipWaitFor = (⟦program⟧.stepDef move.stepType).waitFor.isNone

/-- A move to a program point agrees with the program point's Step definition. -/
theorem moveToPoint_agrees (program : Program) (core : ThreadCore) (code : Code) :
    MoveAgreesWithStepDef program (moveToPoint core code) := by
  simp only [MoveAgreesWithStepDef, moveToPoint, translate, pointStepDef]
  cases isBlockingHead code <;> simp

/-- Every move of every row of the mapping table agrees with its target Step
definition. -/
theorem encodeIntent_moves_agree (program : Program) (snapshot : Dex.Snapshot signature)
    (result : StatementResult) (move : Dex.Move signature)
    (isMove : move ∈ (encodeIntent snapshot result).decision.moves) :
    MoveAgreesWithStepDef program move := by
  unfold encodeIntent at isMove
  cases intentCase : result.intent <;> rw [intentCase] at isMove <;>
    simp only [moveDecision, closeDecision] at isMove
  case signal cv =>
    split at isMove <;> simp at isMove <;> subst isMove <;> exact moveToPoint_agrees _ _ _
  case broadcast cv =>
    split at isMove <;> simp at isMove <;> subst isMove <;> exact moveToPoint_agrees _ _ _
  case fork child childCode =>
    simp at isMove
    cases isMove with
    | inl isParent => subst isParent; exact moveToPoint_agrees _ _ _
    | inr isChild => subst isChild; exact moveToPoint_agrees _ _ _
  case enterTimed seconds =>
    simp at isMove
    subst isMove
    simp [MoveAgreesWithStepDef, translate, timedStepDef]
  all_goals first
    | (simp at isMove; subst isMove; exact moveToPoint_agrees _ _ _)
    | (simp at isMove)

/-- The entry Step has no WaitFor, so [FLOW-START] creates it in phase `EX`. -/
theorem entryStep_hasNoWaitFor (program : Program) :
    (⟦program⟧.stepDef environment.entryStep).waitFor.isNone = true := by
  simp [translate, startStepDef, environment]

/-! ## Determinism (spec §4.1) -/

/-- A transition system's `step` is a function, so a label has at most one
outcome. Both models are transition systems, so this covers "The conclusions
call deterministic meta-functions" (spec §4.1) for Dex and the same for L. -/
theorem step_deterministic {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State) (label : Label)
    (first second : List Event × State)
    (isFirst : system.step state label = some first)
    (isSecond : system.step state label = some second) : first = second := by
  rw [isFirst] at isSecond
  exact Option.some.inj isSecond

/-- A schedule has at most one run: the events and final state of a finite
run are determined by its labels. -/
theorem exec_deterministic {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State) (labels : List Label)
    (first second : List Event × State)
    (isFirst : system.exec state labels = some first)
    (isSecond : system.exec state labels = some second) : first = second := by
  rw [isFirst] at isSecond
  exact Option.some.inj isSecond

/-! ## Sanity lemmas -/

/-- [TICK] is always enabled in Dex and produces no event (spec §4.5, §5.5). -/
theorem dex_tick_enabled (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (duration : Nat) :
    (Dex.system flow).step config (.tick (duration + 1)) =
      some ([], { config with clock := config.clock + (duration + 1) }) := by
  simp [Dex.system, Dex.step]

/-- An idle tick is always enabled in L and produces no event (spec §8.1, "Clock"). -/
theorem l_tick_enabled (program : Program) (config : L.Config) (duration : Nat) :
    (L.system program).step config (.tick (duration + 1)) =
      some ([], { config with clock := config.clock + (duration + 1) }) := by
  simp [L.system, L.step]

/-- A tick is not a progress step in either system (spec §5.5). -/
theorem tick_isNotProgress (program : Program) (duration : Nat) :
    (Dex.system ⟦program⟧).isProgress (.tick duration) = false ∧
    (L.system program).isProgress (.tick duration) = false := by
  simp [Dex.system, L.system]

/-- `π` maps the accepted start to `begin(v)` (spec §5.3, table). -/
theorem project_start (input : Val) :
    project (.start StepType.start (DexVal.value input)) = some (.begin input) := rfl

/-- `π` maps an accepted external publication to `in(c, v)` (spec §5.3, table). -/
theorem project_pub (chan : ExtChan) (value : Val) :
    project (.pub (Chan.external chan) (DexVal.value value)) = some (.input chan value) := rfl

/-- `π` maps a rejected external publication to `refused(c, v)` (spec §5.3, table). -/
theorem project_rejectedPub (chan : ExtChan) (value : Val) :
    project (.rejectedPub (Chan.external chan) (DexVal.value value)) =
      some (.refused chan value) := rfl

/-- `π` maps a Worker effect to `out(a)` and drops its source (spec §5.3, table). -/
theorem project_fx (step : Dex.StepExecId signature) (method : Dex.Method) (effect : Val) :
    project (.fx step method effect) = some (.out effect) := rfl

/-- `π` decodes the exit value from the single tagged ForceComplete output
(spec §5.3: "the root Flow's `outs` is exactly that one FC output"). -/
theorem project_exit (step : Dex.StepExecId signature) (value : Val) :
    project (.closed .completed [(step, DexVal.exitTag value)]) = some (.exit value) := rfl

/-- `π` reads the failure message of a failed close (spec §5.3, table). -/
theorem project_fail (message : String) (outputs : List (Dex.StepExecId signature × DexVal)) :
    project (.closed (.failed message) outputs) = some (.fail message) := rfl

/-- The Dex form of an admissible L word is an admissible word of `E_adm`
(spec §5.3). -/
theorem isAdmissibleWord_encode (word : Sequence L.Request)
    (isAdmissible : L.IsAdmissibleWord word) :
    environment.IsAdmissibleWord (word.map encodeRequest) := by
  obtain ⟨⟨input, startsWith⟩, laterAreInputs, isClosed⟩ := isAdmissible
  refine ⟨⟨DexVal.value input, ⟨input, rfl⟩, ?_⟩, ?_, ?_⟩
  · simp [Sequence.map, startsWith, encodeRequest, environment]
  · intro position request isRequest
    simp only [Sequence.map] at isRequest
    cases atPosition : word (position + 1) with
    | none => simp [atPosition] at isRequest
    | some lRequest =>
      obtain ⟨chan, value, isInput⟩ := laterAreInputs position lRequest atPosition
      subst isInput
      simp [atPosition, encodeRequest] at isRequest
      exact ⟨_, _, isRequest.symm, chan, value, rfl, rfl⟩
  · intro position isNone
    simp only [Sequence.map] at *
    cases atPosition : word position with
    | none => simp [isClosed position atPosition]
    | some lRequest => simp [atPosition] at isNone

/-- The L form of a Dex request of `E_adm`, when it has one. -/
def decodeRequest : Dex.Request signature → Option L.Request
  | .startFlow StepType.start (DexVal.value input) => some (.start input)
  | .publish (Chan.external chan) (DexVal.value value) => some (.input chan value)
  | _ => none

/-- Every admissible word of `E_adm` is the Dex form of an admissible L word
(spec §5.3). With `isAdmissibleWord_encode`, quantifying the liveness theorem
over admissible L words is the same as quantifying it over `E_adm`. -/
theorem isAdmissibleWord_decode (word : Sequence (Dex.Request signature))
    (isAdmissible : environment.IsAdmissibleWord word) :
    ∃ lWord : Sequence L.Request,
      L.IsAdmissibleWord lWord ∧ word = lWord.map encodeRequest := by
  obtain ⟨⟨input, ⟨value, isValue⟩, startsWith⟩, laterArePublications, isClosed⟩ := isAdmissible
  subst isValue
  refine ⟨fun position => (word position).bind decodeRequest, ⟨⟨value, ?_⟩, ?_, ?_⟩, ?_⟩
  · simp [startsWith, decodeRequest, environment]
  · intro position request isRequest
    cases atPosition : word (position + 1) with
    | none => simp [atPosition] at isRequest
    | some dexRequest =>
      obtain ⟨chan, message, isPublication, externalChan, messageValue, isExternal, isEncoded⟩ :=
        laterArePublications position dexRequest atPosition
      subst isPublication isExternal isEncoded
      simp [atPosition, decodeRequest] at isRequest
      exact ⟨externalChan, messageValue, isRequest.symm⟩
  · intro position isNone
    cases atPosition : word position with
    | none => simp [isClosed position atPosition]
    | some dexRequest =>
      cases position with
      | zero =>
        rw [startsWith] at atPosition
        cases atPosition
        simp [startsWith, decodeRequest, environment] at isNone
      | succ previous =>
        obtain ⟨chan, message, isPublication, externalChan, messageValue, isExternal, isEncoded⟩ :=
          laterArePublications previous dexRequest atPosition
        subst isPublication isExternal isEncoded
        simp [atPosition, decodeRequest] at isNone
  · funext position
    cases position with
    | zero => simp [Sequence.map, startsWith, decodeRequest, encodeRequest, environment]
    | succ previous =>
      cases atPosition : word (previous + 1) with
      | none => simp [Sequence.map, atPosition]
      | some dexRequest =>
        obtain ⟨chan, message, isPublication, externalChan, messageValue, isExternal, isEncoded⟩ :=
          laterArePublications previous dexRequest atPosition
        subst isPublication isExternal isEncoded
        simp [Sequence.map, atPosition, decodeRequest, encodeRequest]

end DexExpressiveness
