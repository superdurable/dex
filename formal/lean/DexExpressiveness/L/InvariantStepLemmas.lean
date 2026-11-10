import DexExpressiveness.L.Invariant
import DexExpressiveness.Dex.RunLemmas
import DexExpressiveness.Properties

/-!
# Helper lemmas for work package 1: values, handles, and thread-local steps

`L/InvariantStep.lean` proves that every L transition keeps `L.Invariant`.
This file holds the facts that do not mention the invariant as a whole:

1. association lists and thread ID prefixes;
2. `IsValidVal` and its relatives grow with the set of existing mutexes;
3. an expression over valid locals evaluates to a valid value: literals carry
   no handle, and `cvOf` only wraps a mutex handle that is already valid;
4. what `completeStatement`, `resumeBlocked`, and `resumeTimed` do to the
   thread ID, the fresh counter, and the handles a thread holds;
5. how a phase fits the code.
-/

namespace DexExpressiveness.L

/-! ## Association lists -/

section AssocListFacts

variable {κ ν : Type} [DecidableEq κ]

/-- `k ∈ dom(M[k ↦ v])`. -/
theorem contains_set_self (map : AssocList κ ν) (key : κ) (value : ν) :
    (map.set key value).contains key = true := by
  simp [AssocList.contains, AssocList.get?_set_self]

/-- `dom(M[k ↦ v])` is `dom(M)` and `k`. -/
theorem contains_set_iff (map : AssocList κ ν) (key other : κ) (value : ν) :
    (map.set key value).contains other = true ↔ other = key ∨ map.contains other = true := by
  by_cases isKey : other = key
  · subst isKey
    simp [contains_set_self]
  · simp [AssocList.contains, AssocList.get?_set_other _ _ _ _ isKey, isKey]

/-- Setting a key keeps every key of the map. -/
theorem contains_set_of_contains (map : AssocList κ ν) (key other : κ) (value : ν)
    (isContained : map.contains other = true) : (map.set key value).contains other = true :=
  (contains_set_iff map key other value).mpr (Or.inr isContained)

/-- Setting a key of the map does not change the domain. -/
theorem contains_set_of_existing (map : AssocList κ ν) (key other : κ) (value : ν)
    (isExisting : map.contains key = true) :
    (map.set key value).contains other = map.contains other := by
  by_cases isKey : other = key
  · subst isKey
    rw [contains_set_self, isExisting]
  · simp [AssocList.contains, AssocList.get?_set_other _ _ _ _ isKey]

/-- A binding that `get?` returns is an entry of the map. -/
theorem mem_of_get? (map : AssocList κ ν) (key : κ) (value : ν)
    (isBound : map.get? key = some value) : (key, value) ∈ map := by
  induction map with
  | nil => simp [AssocList.get?] at isBound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · simp only [AssocList.get?, isKey, ↓reduceIte, Option.some.injEq] at isBound
      simp [isKey, isBound]
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isBound
      exact List.mem_cons_of_mem _ (inductionHypothesis isBound)

/-- A key with a binding is in the domain. -/
theorem contains_of_get? (map : AssocList κ ν) (key : κ) (value : ν)
    (isBound : map.get? key = some value) : map.contains key = true := by
  simp [AssocList.contains, isBound]

end AssocListFacts

/-! ## Thread ID prefixes -/

/-- A list is not a proper extension of itself. -/
theorem not_concat_prefix_self (path : Tid) (index : Nat) : ¬ (path ++ [index]) <+: path := by
  intro isPrefix
  have lengthBound := isPrefix.length_le
  simp only [List.length_append, List.length_cons, List.length_nil] at lengthBound
  omega

/-- Two paths with equal last steps agree before the last step. -/
theorem concat_inj {first second : Tid} {firstIndex secondIndex : Nat}
    (isSame : first ++ [firstIndex] = second ++ [secondIndex]) :
    first = second ∧ firstIndex = secondIndex := by
  obtain ⟨samePath, sameLast⟩ := List.append_inj' isSame rfl
  exact ⟨samePath, by simpa using sameLast⟩

/-- A path that extends `child` extends its parent path. -/
theorem prefix_of_concat_prefix {parent path : Tid} {index : Nat}
    (isPrefix : (parent ++ [index]) <+: path) : parent <+: path :=
  (List.prefix_append parent [index]).trans isPrefix

namespace Config

/-! ## Validity grows with the set of existing mutexes -/

/-- Every mutex that exists in `config` exists in `next`. -/
def IsExtendedBy (config next : Config) : Prop :=
  ∀ mutex, config.mutexOwners.contains mutex = true → next.mutexOwners.contains mutex = true

theorem IsExtendedBy.refl (config : Config) : config.IsExtendedBy config := fun _ isContained =>
  isContained

/-- Two configurations with the same mutex table extend each other. -/
theorem IsExtendedBy.of_eq {config next : Config}
    (sameOwners : next.mutexOwners = config.mutexOwners) : config.IsExtendedBy next := by
  intro mutex isContained
  rw [sameOwners]
  exact isContained

theorem IsExtendedBy.isExistingMutex {config next : Config} (isExtended : config.IsExtendedBy next)
    {mutex : MutexId} (isExisting : config.IsExistingMutex mutex) : next.IsExistingMutex mutex := by
  cases mutex with
  | «static» index => trivial
  | dynamic creator index => exact isExtended _ isExisting

theorem IsExtendedBy.isValidVal {config next : Config} (isExtended : config.IsExtendedBy next)
    {value : Val} (isValid : config.IsValidVal value) : next.IsValidVal value := by
  cases value with
  | mutex mutex => exact isExtended.isExistingMutex isValid
  | cv cv => exact isExtended.isExistingMutex isValid
  | _ => trivial

theorem IsExtendedBy.isValidInstr {config next : Config} (isExtended : config.IsExtendedBy next)
    {instr : Instr} (isValid : config.IsValidInstr instr) : next.IsValidInstr instr := by
  cases instr with
  | source statement => trivial
  | park cv => exact isExtended.isExistingMutex isValid
  | relock mutex => exact isExtended.isExistingMutex isValid

theorem IsExtendedBy.isValidFrame {config next : Config} (isExtended : config.IsExtendedBy next)
    {frame : Frame} (isValid : config.IsValidFrame frame) : next.IsValidFrame frame := by
  cases frame with
  | call resultVar savedLocals returnCode =>
    exact ⟨fun entry isMember => isExtended.isValidVal (isValid.1 entry isMember),
      fun instr isMember => isExtended.isValidInstr (isValid.2 instr isMember)⟩
  | handler exceptionVar handlerCode afterCode =>
    exact ⟨fun instr isMember => isExtended.isValidInstr (isValid.1 instr isMember),
      fun instr isMember => isExtended.isValidInstr (isValid.2 instr isMember)⟩

theorem IsExtendedBy.isValidPhase {config next : Config} (isExtended : config.IsExtendedBy next)
    {phase : Phase} (isValid : config.IsValidPhase phase) : next.IsValidPhase phase := by
  cases phase with
  | running loaded => exact isExtended.isValidVal isValid
  | blocked on since =>
    cases on with
    | lock mutex => exact isExtended.isExistingMutex isValid
    | park cv => exact isExtended.isExistingMutex isValid
    | _ => trivial
  | woken outcome =>
    cases outcome with
    | received message => exact isExtended.isValidVal isValid
    | _ => trivial
  | resuming outcome =>
    cases outcome with
    | received message => exact isExtended.isValidVal isValid
    | _ => trivial
  | _ => trivial

theorem IsExtendedBy.isValidThread {config next : Config} (isExtended : config.IsExtendedBy next)
    {thread : Thread} (isValid : config.IsValidThread thread) : next.IsValidThread thread :=
  ⟨fun entry isMember => isExtended.isValidVal (isValid.1 entry isMember),
    fun frame isMember => isExtended.isValidFrame (isValid.2.1 frame isMember),
    fun instr isMember => isExtended.isValidInstr (isValid.2.2.1 instr isMember),
    isExtended.isValidPhase isValid.2.2.2⟩

/-- The handles in a thread's locals, stack, and code name existing mutexes:
`IsValidThread` without the phase. -/
def IsValidState (config : Config) (core : ThreadCore) (code : Code) : Prop :=
  (∀ entry, entry ∈ core.locals → config.IsValidVal entry.2) ∧
  (∀ frame, frame ∈ core.stack → config.IsValidFrame frame) ∧
  (∀ instr, instr ∈ code → config.IsValidInstr instr)

theorem IsValidThread.isValidState {config : Config} {thread : Thread}
    (isValid : config.IsValidThread thread) : config.IsValidState thread.core thread.code :=
  ⟨isValid.1, isValid.2.1, isValid.2.2.1⟩

theorem IsExtendedBy.isValidState {config next : Config} (isExtended : config.IsExtendedBy next)
    {core : ThreadCore} {code : Code} (isValid : config.IsValidState core code) :
    next.IsValidState core code :=
  ⟨fun entry isMember => isExtended.isValidVal (isValid.1 entry isMember),
    fun frame isMember => isExtended.isValidFrame (isValid.2.1 frame isMember),
    fun instr isMember => isExtended.isValidInstr (isValid.2.2 instr isMember)⟩

/-- A literal is plain data: it carries no handle (spec §8.1, "Handles and
literals"). So the program input and every external message are valid in
every configuration. -/
theorem isValidVal_literal (config : Config) (literal : Literal) :
    config.IsValidVal literal.toVal := by
  cases literal <;> trivial

/-! ## Expressions over valid locals evaluate to valid values -/

/-- Setting a local to a valid value keeps the locals valid. -/
theorem isValidVal_of_mem_set (config : Config) (locals : Env) (name : Var) (value : Val)
    (localsValid : ∀ entry, entry ∈ locals → config.IsValidVal entry.2)
    (valueValid : config.IsValidVal value) :
    ∀ entry, entry ∈ locals.set name value → config.IsValidVal entry.2 := by
  intro entry isMember
  rcases AssocList.mem_set locals name value entry isMember with isOld | isNew
  · exact localsValid entry isOld
  · rw [isNew]
    exact valueValid

/-- An expression never forges a handle: over valid locals it evaluates to a
valid value. A mutex handle is static or read from a local; a condition
variable handle wraps such a mutex handle. -/
theorem isValidVal_evalExpr (config : Config) (locals : Env)
    (localsValid : ∀ entry, entry ∈ locals → config.IsValidVal entry.2) (expression : Expr) :
    ∀ value, evalExpr locals expression = some value → config.IsValidVal value := by
  induction expression with
  | lit literal =>
    intro value isValue
    simp only [evalExpr, Option.some.injEq] at isValue
    rw [← isValue]
    exact isValidVal_literal config literal
  | staticMutex index =>
    intro value isValue
    simp only [evalExpr, Option.some.injEq] at isValue
    rw [← isValue]
    trivial
  | var name =>
    intro value isValue
    simp only [evalExpr, Option.some.injEq] at isValue
    cases isBound : AssocList.get? locals name with
    | none =>
      rw [isBound] at isValue
      rw [← isValue]
      trivial
    | some bound =>
      rw [isBound] at isValue
      rw [← isValue]
      exact localsValid _ (mem_of_get? locals name bound isBound)
  | add left right =>
    intro value isValue
    simp only [evalExpr] at isValue
    split at isValue
    · cases isValue
      trivial
    · cases isValue
  | eq left right =>
    intro value isValue
    simp only [evalExpr] at isValue
    split at isValue
    · cases isValue
      trivial
    · cases isValue
  | lt left right =>
    intro value isValue
    simp only [evalExpr] at isValue
    split at isValue
    · cases isValue
      trivial
    · cases isValue
  | not operand =>
    intro value isValue
    simp only [evalExpr] at isValue
    split at isValue
    · cases isValue
      trivial
    · cases isValue
  | cvOf mutex index mutexHypothesis =>
    intro value isValue
    simp only [evalExpr] at isValue
    split at isValue
    · rename_i mutexId isMutex
      cases isValue
      exact mutexHypothesis (.mutex mutexId) isMutex
    · cases isValue

/-- Call arguments over valid locals evaluate to valid values. -/
theorem isValidVal_evalArguments (config : Config) (locals : Env)
    (localsValid : ∀ entry, entry ∈ locals → config.IsValidVal entry.2) (arguments : List Expr) :
    ∀ values, evalArguments locals arguments = some values →
      ∀ value, value ∈ values → config.IsValidVal value := by
  induction arguments with
  | nil =>
    intro values isValues value isMember
    simp only [evalArguments, Option.some.injEq] at isValues
    rw [← isValues] at isMember
    cases isMember
  | cons argument laterArguments inductionHypothesis =>
    intro values isValues value isMember
    simp only [evalArguments] at isValues
    split at isValues
    · rename_i first laterValues isFirst isLater
      cases isValues
      rcases List.mem_cons.mp isMember with isHead | isInRest
      · rw [isHead]
        exact isValidVal_evalExpr config locals localsValid argument first isFirst
      · exact inductionHypothesis laterValues isLater value isInRest
    · cases isValues

/-- Parameters bound to valid values are valid locals. -/
theorem isValidVal_bindParams (config : Config) (params : List Var) :
    ∀ (values : List Val) (bound : Env), bindParams params values = some bound →
      (∀ value, value ∈ values → config.IsValidVal value) →
      ∀ entry, entry ∈ bound → config.IsValidVal entry.2 := by
  induction params with
  | nil =>
    intro values bound isBound valuesValid entry isMember
    cases values with
    | nil =>
      simp only [bindParams, Option.some.injEq] at isBound
      rw [← isBound] at isMember
      cases isMember
    | cons value laterValues => simp [bindParams] at isBound
  | cons param laterParams inductionHypothesis =>
    intro values bound isBound valuesValid entry isMember
    cases values with
    | nil => simp [bindParams] at isBound
    | cons value laterValues =>
      simp only [bindParams, Option.map_eq_some_iff] at isBound
      obtain ⟨laterBound, isLaterBound, isCons⟩ := isBound
      rw [← isCons] at isMember
      rcases List.mem_cons.mp isMember with isHead | isInRest
      · rw [isHead]
        exact valuesValid value (List.mem_cons_self ..)
      · exact inductionHypothesis laterValues laterBound isLaterBound
          (fun later isLater => valuesValid later (List.mem_cons_of_mem _ isLater)) entry isInRest

/-- `pushCode` adds source statements only, and a source statement holds no handle. -/
theorem isValidInstr_pushCode (config : Config) (statement : Stmt) :
    ∀ code : Code, (∀ instr, instr ∈ code → config.IsValidInstr instr) →
      ∀ instr, instr ∈ pushCode statement code → config.IsValidInstr instr := by
  induction statement with
  | skip =>
    intro code codeValid instr isMember
    exact codeValid instr isMember
  | seq first second firstHypothesis secondHypothesis =>
    intro code codeValid instr isMember
    exact firstHypothesis (pushCode second code) (secondHypothesis code codeValid) instr isMember
  | _ =>
    intro code codeValid instr isMember
    simp only [pushCode, List.mem_cons] at isMember
    rcases isMember with isHead | isInRest
    · rw [isHead]
      trivial
    · exact codeValid instr isInRest

/-- The frames that `unwindToCall` returns come from the stack. -/
theorem isValid_unwindToCall (config : Config) (stack : List Frame)
    (stackValid : ∀ frame, frame ∈ stack → config.IsValidFrame frame) :
    ∀ resultVar savedLocals returnCode outerFrames,
      unwindToCall stack = some (resultVar, savedLocals, returnCode, outerFrames) →
      (∀ entry, entry ∈ savedLocals → config.IsValidVal entry.2) ∧
      (∀ instr, instr ∈ returnCode → config.IsValidInstr instr) ∧
      (∀ frame, frame ∈ outerFrames → config.IsValidFrame frame) := by
  induction stack with
  | nil =>
    intro resultVar savedLocals returnCode outerFrames isUnwound
    simp [unwindToCall] at isUnwound
  | cons frame laterFrames inductionHypothesis =>
    intro resultVar savedLocals returnCode outerFrames isUnwound
    have laterValid : ∀ later, later ∈ laterFrames → config.IsValidFrame later :=
      fun later isMember => stackValid later (List.mem_cons_of_mem _ isMember)
    cases frame with
    | call frameVar frameLocals frameCode =>
      simp only [unwindToCall, Option.some.injEq, Prod.mk.injEq] at isUnwound
      obtain ⟨_, sameLocals, sameCode, sameFrames⟩ := isUnwound
      have frameValid := stackValid _ (List.mem_cons_self ..)
      rw [← sameLocals, ← sameCode, ← sameFrames]
      exact ⟨frameValid.1, frameValid.2, laterValid⟩
    | handler exceptionVar handlerCode afterCode =>
      simp only [unwindToCall] at isUnwound
      exact inductionHypothesis laterValid _ _ _ _ isUnwound

/-- The handler, the locals, and the frames that `unwindToHandler` returns
come from the stack and the current locals. -/
theorem isValid_unwindToHandler (config : Config) (stack : List Frame)
    (stackValid : ∀ frame, frame ∈ stack → config.IsValidFrame frame) :
    ∀ (locals : Env), (∀ entry, entry ∈ locals → config.IsValidVal entry.2) →
      ∀ exceptionVar handlerCode restoredLocals outerFrames,
      unwindToHandler locals stack = some (exceptionVar, handlerCode, restoredLocals, outerFrames) →
      (∀ instr, instr ∈ handlerCode → config.IsValidInstr instr) ∧
      (∀ entry, entry ∈ restoredLocals → config.IsValidVal entry.2) ∧
      (∀ frame, frame ∈ outerFrames → config.IsValidFrame frame) := by
  induction stack with
  | nil =>
    intro locals localsValid exceptionVar handlerCode restoredLocals outerFrames isUnwound
    simp [unwindToHandler] at isUnwound
  | cons frame laterFrames inductionHypothesis =>
    intro locals localsValid exceptionVar handlerCode restoredLocals outerFrames isUnwound
    have laterValid : ∀ later, later ∈ laterFrames → config.IsValidFrame later :=
      fun later isMember => stackValid later (List.mem_cons_of_mem _ isMember)
    have frameValid := stackValid _ (List.mem_cons_self ..)
    cases frame with
    | call frameVar frameLocals frameCode =>
      simp only [unwindToHandler] at isUnwound
      exact inductionHypothesis laterValid frameLocals frameValid.1 _ _ _ _ isUnwound
    | handler frameVar frameCode afterCode =>
      simp only [unwindToHandler, Option.some.injEq, Prod.mk.injEq] at isUnwound
      obtain ⟨_, sameCode, sameLocals, sameFrames⟩ := isUnwound
      rw [← sameCode, ← sameLocals, ← sameFrames]
      exact ⟨frameValid.1, localsValid, laterValid⟩

end Config

end DexExpressiveness.L
