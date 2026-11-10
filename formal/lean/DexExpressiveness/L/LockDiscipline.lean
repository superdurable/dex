import DexExpressiveness.L.InvariantStep

/-!
# A syntactic lock discipline that implies well-formedness

`Program.IsWellFormed` (spec §0, §8.1 "Well-formed programs") quantifies over
every reachable configuration, so it cannot be decided directly. This file
defines a decidable, syntactic sufficient condition for it,
`Program.hasStaticLockDiscipline`, and the thread-local facts its soundness
proof needs. `L/LockDisciplineSound.lean` proves the soundness theorem.

The condition is a must-hold lock-set analysis over the statically known
mutexes (`Expr.staticMutex`). A lock set is a bitmask: bit `i` is set when
the thread surely holds `static i`. The analysis of a statement from a lock
set either rejects it, says that the statement never falls through (`ret`,
`throw`, `exit`, `fail`), or gives the lock set after it:

- `lock(staticMutex i)` adds `i`; a `lock` of any other operand adds nothing;
- `unlock(m)`, `wait(cv, m)`, `signal(cv)`, and `broadcast(cv)` need a
  statically known operand whose mutex is in the set, and `unlock` removes it;
  any other operand is rejected;
- the two branches of an `if` or a `select` must agree on the lock set when
  both fall through;
- a loop body must give back the lock set it started from;
- a called or forked procedure body is checked from the empty set, and so are
  a `try` body and its handler; after a `call` or a `try` the set is empty.

The set is a must-hold under-approximation: a thread may hold more mutexes
than its set says, but never fewer.
-/

namespace DexExpressiveness.L

/-- What the lock-set analysis says about a statement or the rest of a code. -/
inductive LockSetOutcome
  /-- The statement breaks the discipline. -/
  | rejected
  /-- Control never falls through the statement: `ret`, `throw`, `exit`, or `fail`. -/
  | ended
  /-- Control falls through with this lock set. -/
  | continues (held : Nat)
  deriving DecidableEq, Repr

namespace LockSetOutcome

/-- Sequential composition: analyze what follows from the lock set this
outcome falls through with. -/
def andThen : LockSetOutcome → (Nat → LockSetOutcome) → LockSetOutcome
  | .rejected, _ => .rejected
  | .ended, _ => .ended
  | .continues held, next => next held

/-- The outcome of a choice between two branches. Branches that both fall
through must agree on the lock set. -/
def join : LockSetOutcome → LockSetOutcome → LockSetOutcome
  | .rejected, _ => .rejected
  | _, .rejected => .rejected
  | .ended, other => other
  | other, .ended => other
  | .continues first, .continues second =>
    if first = second then .continues first else .rejected

/-- Whether the analysis accepts. -/
def isAccepted : LockSetOutcome → Bool
  | .rejected => false
  | _ => true

end LockSetOutcome

/-- The bit of the statically known mutex `static index` in a lock set. -/
def lockBit (index : Nat) : Nat := 2 ^ index

/-- The index of a statically known mutex operand, written `staticMutex i`. -/
def staticMutexIndex? : Expr → Option Nat
  | .staticMutex index => some index
  | _ => none

/-- The mutex index of a condition variable operand of a statically known
mutex, written `cvOf (staticMutex i) k`. -/
def staticCvMutexIndex? : Expr → Option Nat
  | .cvOf mutex _ => staticMutexIndex? mutex
  | _ => none

/-- The lock-set analysis of one source statement from the lock set `held`
(the discipline that implies spec §8.1 "Well-formed programs"). -/
def checkStmt (held : Nat) : Stmt → LockSetOutcome
  | .skip => .continues held
  | .seq first second => (checkStmt held first).andThen (fun middle => checkStmt middle second)
  | .assign _ _ => .continues held
  | .load _ _ => .continues held
  | .store _ _ => .continues held
  | .ite _ thenBranch elseBranch => (checkStmt held thenBranch).join (checkStmt held elseBranch)
  | .whileLoop _ body => (checkStmt held body).join (.continues held)
  | .call _ _ _ => .continues 0
  | .ret _ => .ended
  | .fork _ _ _ => .continues held
  | .join _ => .continues held
  | .newMutex _ => .continues held
  | .lock mutex =>
    match staticMutexIndex? mutex with
    | some index => .continues (held ||| lockBit index)
    | none => .continues held
  | .unlock mutex =>
    match staticMutexIndex? mutex with
    | some index => if held.testBit index then .continues (held ^^^ lockBit index) else .rejected
    | none => .rejected
  | .wait cv mutex =>
    match staticCvMutexIndex? cv, staticMutexIndex? mutex with
    | some cvIndex, some index =>
      if cvIndex = index ∧ held.testBit index = true then .continues held else .rejected
    | _, _ => .rejected
  | .signal cv =>
    match staticCvMutexIndex? cv with
    | some index => if held.testBit index then .continues held else .rejected
    | none => .rejected
  | .broadcast cv =>
    match staticCvMutexIndex? cv with
    | some index => if held.testBit index then .continues held else .rejected
    | none => .rejected
  | .sleep _ => .continues held
  | .select _ _ onMessage _ onTimeout =>
    (checkStmt held onMessage).join (checkStmt held onTimeout)
  | .recv _ _ => .continues held
  | .emit _ => .continues held
  | .raise _ => .ended
  | .tryCatch body _ handler =>
    if (checkStmt 0 body).isAccepted then (checkStmt 0 handler).join (.continues 0)
    else .rejected
  | .cancel _ => .continues held
  | .exit _ => .ended
  | .fail _ => .ended

/-- The lock-set analysis of the code a thread has left, including the runtime
instructions of `wait(cv, m)`: `park` keeps the set and `relock (static i)`
adds `i` back. -/
def checkCode (held : Nat) : Code → LockSetOutcome
  | [] => .continues held
  | .source statement :: rest => (checkStmt held statement).andThen (fun after => checkCode after rest)
  | .park _ :: rest => checkCode held rest
  | .relock (.static index) :: rest => checkCode (held ||| lockBit index) rest
  | .relock (.dynamic _ _) :: _ => .rejected

/-- A stack frame whose saved code obeys the discipline from the empty lock
set: the code after a call, and the handler and the code after a `try`. -/
def Frame.isLockDisciplined : Frame → Bool
  | .call _ _ returnCode => (checkCode 0 returnCode).isAccepted
  | .handler _ handlerCode afterCode =>
    (checkCode 0 handlerCode).isAccepted && (checkCode 0 afterCode).isAccepted

/-- The program obeys the static lock discipline: the body of `main` and of
every procedure is accepted from the empty lock set. A sufficient condition
for spec §8.1 "Well-formed programs" (`Program.isWellFormed_of_hasStaticLockDiscipline`). -/
def Program.hasStaticLockDiscipline (program : Program) : Bool :=
  (checkStmt 0 program.mainBody).isAccepted &&
    program.procs.all (fun entry => (checkStmt 0 entry.2.body).isAccepted)

/-! ## Facts about the analysis -/

namespace LockSetOutcome

/-- Sequential composition is associative. -/
theorem andThen_assoc (outcome : LockSetOutcome) (first second : Nat → LockSetOutcome) :
    (outcome.andThen first).andThen second =
      outcome.andThen (fun held => (first held).andThen second) := by
  cases outcome <;> rfl

/-- The choice between two branches that fall through with the same lock set. -/
theorem join_continues_self (held : Nat) :
    (LockSetOutcome.continues held).join (.continues held) = .continues held := by
  simp [join]

/-- The choice between two branches that fall through with different lock sets. -/
theorem join_continues_other {first second : Nat} (isOther : first ≠ second) :
    (LockSetOutcome.continues first).join (.continues second) = .rejected := by
  simp [join, isOther]

/-- Taking the first branch of an accepted choice is accepted. -/
theorem isAccepted_andThen_of_join_left {first second : LockSetOutcome}
    {next : Nat → LockSetOutcome} (isAccepted : ((first.join second).andThen next).isAccepted = true) :
    (first.andThen next).isAccepted = true := by
  cases first with
  | rejected => simp [join, andThen, LockSetOutcome.isAccepted] at isAccepted
  | ended => rfl
  | continues firstHeld =>
    cases second with
    | rejected => simp [join, andThen, LockSetOutcome.isAccepted] at isAccepted
    | ended => exact isAccepted
    | continues secondHeld =>
      by_cases isSame : firstHeld = secondHeld
      · subst isSame
        rw [join_continues_self] at isAccepted
        exact isAccepted
      · rw [join_continues_other isSame] at isAccepted
        simp [andThen, LockSetOutcome.isAccepted] at isAccepted

/-- Taking the second branch of an accepted choice is accepted. -/
theorem isAccepted_andThen_of_join_right {first second : LockSetOutcome}
    {next : Nat → LockSetOutcome} (isAccepted : ((first.join second).andThen next).isAccepted = true) :
    (second.andThen next).isAccepted = true := by
  cases first with
  | rejected => simp [join, andThen, LockSetOutcome.isAccepted] at isAccepted
  | ended =>
    cases second with
    | rejected => simp [join, andThen, LockSetOutcome.isAccepted] at isAccepted
    | ended => rfl
    | continues secondHeld => exact isAccepted
  | continues firstHeld =>
    cases second with
    | rejected => simp [join, andThen, LockSetOutcome.isAccepted] at isAccepted
    | ended => rfl
    | continues secondHeld =>
      by_cases isSame : firstHeld = secondHeld
      · subst isSame
        rw [join_continues_self] at isAccepted
        exact isAccepted
      · rw [join_continues_other isSame] at isAccepted
        simp [andThen, LockSetOutcome.isAccepted] at isAccepted

/-- A choice with `continues held` that is accepted ends or falls through with
`held`, and what follows is accepted from `held`. -/
theorem join_continues_of_isAccepted {outcome : LockSetOutcome} {held : Nat}
    {next : Nat → LockSetOutcome}
    (isAccepted : ((outcome.join (.continues held)).andThen next).isAccepted = true) :
    (outcome = .ended ∨ outcome = .continues held) ∧ (next held).isAccepted = true := by
  cases outcome with
  | rejected => simp [join, andThen, LockSetOutcome.isAccepted] at isAccepted
  | ended => exact ⟨Or.inl rfl, isAccepted⟩
  | continues after =>
    by_cases isSame : after = held
    · subst isSame
      rw [join_continues_self] at isAccepted
      exact ⟨Or.inr rfl, isAccepted⟩
    · rw [join_continues_other isSame] at isAccepted
      simp [andThen, LockSetOutcome.isAccepted] at isAccepted

end LockSetOutcome

/-- The analysis of code with a statement in front is the analysis of the
statement followed by the analysis of the code. -/
theorem checkCode_pushCode (statement : Stmt) :
    ∀ (held : Nat) (code : Code),
      checkCode held (pushCode statement code) =
        (checkStmt held statement).andThen (fun after => checkCode after code) := by
  induction statement <;> intro held code
  case skip => rfl
  case seq first second firstHypothesis secondHypothesis =>
    simp only [pushCode, firstHypothesis, secondHypothesis, checkStmt,
      LockSetOutcome.andThen_assoc]
  all_goals rfl

/-- A statically known mutex operand is `staticMutex i`. -/
theorem eq_of_staticMutexIndex? {mutex : Expr} {index : Nat}
    (isStatic : staticMutexIndex? mutex = some index) : mutex = .staticMutex index := by
  cases mutex <;> simp_all [staticMutexIndex?]

/-- A condition variable operand of a statically known mutex is
`cvOf (staticMutex i) k`. -/
theorem eq_of_staticCvMutexIndex? {cv : Expr} {index : Nat}
    (isStatic : staticCvMutexIndex? cv = some index) :
    ∃ cvIndex, cv = .cvOf (.staticMutex index) cvIndex := by
  cases cv <;> simp only [staticCvMutexIndex?, reduceCtorEq] at isStatic
  rename_i mutex cvIndex
  rw [eq_of_staticMutexIndex? isStatic]
  exact ⟨cvIndex, rfl⟩

/-- The bits of `lockBit index`. -/
theorem testBit_lockBit (index other : Nat) :
    (lockBit index).testBit other = decide (index = other) :=
  Nat.testBit_two_pow

/-- Removing a held mutex and adding it back restores the lock set. -/
theorem xor_or_lockBit {held index : Nat} (isHeld : held.testBit index = true) :
    (held ^^^ lockBit index) ||| lockBit index = held := by
  apply Nat.eq_of_testBit_eq
  intro other
  rw [Nat.testBit_or, Nat.testBit_xor, testBit_lockBit]
  by_cases isSame : index = other
  · subst isSame
    simp [isHeld]
  · simp [isSame]

/-- Every frame of a stack obeys the discipline from the empty lock set. -/
def AreFramesLockDisciplined (stack : List Frame) : Prop :=
  ∀ frame, frame ∈ stack → frame.isLockDisciplined = true

/-- The code that `unwindToCall` returns to obeys the discipline from the
empty lock set, and so do the frames it leaves. -/
theorem lockDisciplined_unwindToCall :
    ∀ (stack : List Frame), AreFramesLockDisciplined stack →
      ∀ resultVar savedLocals returnCode outerFrames,
        unwindToCall stack = some (resultVar, savedLocals, returnCode, outerFrames) →
        (checkCode 0 returnCode).isAccepted = true ∧ AreFramesLockDisciplined outerFrames := by
  intro stack
  induction stack with
  | nil =>
    intro _ resultVar savedLocals returnCode outerFrames isUnwound
    simp [unwindToCall] at isUnwound
  | cons frame laterFrames inductionHypothesis =>
    intro framesDisciplined resultVar savedLocals returnCode outerFrames isUnwound
    have laterDisciplined : AreFramesLockDisciplined laterFrames :=
      fun later isMember => framesDisciplined later (List.mem_cons_of_mem _ isMember)
    have frameDisciplined := framesDisciplined frame (List.mem_cons_self ..)
    cases frame with
    | call frameVar frameLocals frameCode =>
      simp only [unwindToCall, Option.some.injEq, Prod.mk.injEq] at isUnwound
      obtain ⟨_, _, sameCode, sameFrames⟩ := isUnwound
      rw [← sameCode, ← sameFrames]
      exact ⟨frameDisciplined, laterDisciplined⟩
    | handler exceptionVar handlerCode afterCode =>
      simp only [unwindToCall] at isUnwound
      exact inductionHypothesis laterDisciplined _ _ _ _ isUnwound

/-- The handler that `unwindToHandler` jumps to obeys the discipline from the
empty lock set, and so do the frames it leaves. -/
theorem lockDisciplined_unwindToHandler :
    ∀ (stack : List Frame), AreFramesLockDisciplined stack →
      ∀ (locals : Env) exceptionVar handlerCode restoredLocals outerFrames,
        unwindToHandler locals stack =
          some (exceptionVar, handlerCode, restoredLocals, outerFrames) →
        (checkCode 0 handlerCode).isAccepted = true ∧ AreFramesLockDisciplined outerFrames := by
  intro stack
  induction stack with
  | nil =>
    intro _ locals exceptionVar handlerCode restoredLocals outerFrames isUnwound
    simp [unwindToHandler] at isUnwound
  | cons frame laterFrames inductionHypothesis =>
    intro framesDisciplined locals exceptionVar handlerCode restoredLocals outerFrames isUnwound
    have laterDisciplined : AreFramesLockDisciplined laterFrames :=
      fun later isMember => framesDisciplined later (List.mem_cons_of_mem _ isMember)
    have frameDisciplined := framesDisciplined frame (List.mem_cons_self ..)
    cases frame with
    | call frameVar frameLocals frameCode =>
      simp only [unwindToHandler] at isUnwound
      exact inductionHypothesis laterDisciplined _ _ _ _ _ isUnwound
    | handler frameVar frameCode afterCode =>
      simp only [unwindToHandler, Option.some.injEq, Prod.mk.injEq] at isUnwound
      obtain ⟨_, sameCode, _, sameFrames⟩ := isUnwound
      simp only [Frame.isLockDisciplined, Bool.and_eq_true] at frameDisciplined
      rw [← sameCode, ← sameFrames]
      exact ⟨frameDisciplined.1, laterDisciplined⟩

/-- The body of a procedure of a disciplined program is accepted from the
empty lock set. -/
theorem isAccepted_procBody {program : Program}
    (isDisciplined : program.hasStaticLockDiscipline = true) {procName : ProcName} {proc : Proc}
    (isProc : AssocList.get? program.procs procName = some proc) :
    (checkCode 0 (pushCode proc.body [])).isAccepted = true := by
  simp only [Program.hasStaticLockDiscipline, Bool.and_eq_true, List.all_eq_true] at isDisciplined
  have isAccepted := isDisciplined.2 _ (mem_of_get? _ _ _ isProc)
  rw [checkCode_pushCode]
  revert isAccepted
  cases checkStmt 0 proc.body <;> simp [LockSetOutcome.andThen, checkCode]

/-! ## One thread-local step -/

/-- The statically known mutex that an atomic action releases: the mutex of
`unlock(m)` and of `wait(cv, m)` when it is statically known. -/
def Intent.releasedStaticIndex? : Intent → Option Nat
  | .unlock (.static index) => some index
  | .cvWait { mutex := .static index, index := _ } => some index
  | _ => none

/-- What the soundness proof needs from the result of completing a statement
that the analysis accepted from `held`: its frames obey the discipline, its
code is accepted from a lock set inside `held` that avoids the released
mutex, and a forked thread starts with an empty stack and code accepted from
the empty lock set. -/
structure IsLockDisciplinedResult (held : Nat) (result : StatementResult) : Prop where
  /-- The frames obey the discipline. -/
  framesDisciplined : AreFramesLockDisciplined result.core.stack
  /-- The code is accepted from a lock set inside `held` without the released mutex. -/
  continues : ∃ after, (∀ index, after.testBit index = true →
      held.testBit index = true ∧ result.intent.releasedStaticIndex? ≠ some index) ∧
    (checkCode after result.code).isAccepted = true
  /-- A forked thread obeys the discipline. -/
  forkDisciplined : ∀ child childCode, result.intent = .fork child childCode →
    child.stack = [] ∧ (checkCode 0 childCode).isAccepted = true

/-- A result that keeps the frames, asks for no release and no fork, and has
code accepted from a lock set inside `held`. -/
theorem IsLockDisciplinedResult.of_quiet {held : Nat} {result : StatementResult}
    (framesDisciplined : AreFramesLockDisciplined result.core.stack) (after : Nat)
    (isInside : ∀ index, after.testBit index = true → held.testBit index = true)
    (isAccepted : (checkCode after result.code).isAccepted = true)
    (notReleased : result.intent.releasedStaticIndex? = none)
    (notFork : ∀ child childCode, result.intent ≠ .fork child childCode) :
    IsLockDisciplinedResult held result :=
  { framesDisciplined := framesDisciplined
    continues := ⟨after, fun index isSet => ⟨isInside index isSet, by
      rw [notReleased]; intro isSome; cases isSome⟩, isAccepted⟩
    forkDisciplined := fun child childCode isFork => absurd isFork (notFork child childCode) }

/-- The empty lock set is inside every lock set. -/
theorem testBit_zero_inside (held index : Nat) (isSet : (0 : Nat).testBit index = true) :
    held.testBit index = true := by
  simp at isSet

/-- Returning from a procedure keeps the discipline. -/
theorem lockDisciplined_returnValue (held : Nat) (core : ThreadCore) (value : Val)
    (framesDisciplined : AreFramesLockDisciplined core.stack) :
    IsLockDisciplinedResult held (returnValue core value) := by
  simp only [returnValue]
  split
  · rename_i resultVar savedLocals returnCode outerFrames isUnwound
    obtain ⟨isAccepted, outerDisciplined⟩ :=
      lockDisciplined_unwindToCall core.stack framesDisciplined _ _ _ _ isUnwound
    exact IsLockDisciplinedResult.of_quiet outerDisciplined 0 (testBit_zero_inside held)
      isAccepted rfl (fun _ _ isFork => by cases isFork)
  · split
    · exact IsLockDisciplinedResult.of_quiet framesDisciplined 0 (testBit_zero_inside held) rfl
        rfl (fun _ _ isFork => by cases isFork)
    · exact IsLockDisciplinedResult.of_quiet framesDisciplined 0 (testBit_zero_inside held) rfl
        rfl (fun _ _ isFork => by cases isFork)

/-- A result that keeps the frames and the lock set, and asks for no release
and no fork. -/
theorem IsLockDisciplinedResult.of_rest {held : Nat} {core : ThreadCore}
    (framesDisciplined : AreFramesLockDisciplined core.stack) (intent : Intent)
    (newCore : ThreadCore) (newCode : Code) (sameStack : newCore.stack = core.stack)
    (isAccepted : (checkCode held newCode).isAccepted = true)
    (notReleased : intent.releasedStaticIndex? = none)
    (notFork : ∀ child childCode, intent ≠ .fork child childCode) :
    IsLockDisciplinedResult held { intent := intent, core := newCore, code := newCode } :=
  IsLockDisciplinedResult.of_quiet
    (by show AreFramesLockDisciplined newCore.stack; rw [sameStack]; exact framesDisciplined)
    held (fun _ isSet => isSet) isAccepted notReleased notFork

/-- A lock set without a held mutex is inside the lock set and avoids that mutex. -/
theorem testBit_xor_lockBit {held index : Nat} (isHeld : held.testBit index = true) (other : Nat)
    (isSet : (held ^^^ lockBit index).testBit other = true) :
    held.testBit other = true ∧ some index ≠ some other := by
  rw [Nat.testBit_xor, testBit_lockBit] at isSet
  by_cases isSame : index = other
  · subst isSame
    simp [isHeld] at isSet
  · simp only [isSame, decide_false, Bool.bne_false] at isSet
    exact ⟨isSet, by simpa using isSame⟩

/-- The code after an accepted statement that falls through with `held` is
accepted from `held`. -/
theorem isAccepted_rest {held : Nat} {statement : Stmt} {rest : Code}
    (isContinuing : checkStmt held statement = .continues held)
    (isAccepted : (checkCode held (.source statement :: rest)).isAccepted = true) :
    (checkCode held rest).isAccepted = true := by
  simp only [checkCode, isContinuing, LockSetOutcome.andThen] at isAccepted
  exact isAccepted

/-- Completing a statement that the analysis accepts keeps the discipline. -/
theorem lockDisciplined_completeStatement {program : Program}
    (isDisciplined : program.hasStaticLockDiscipline = true) (core : ThreadCore) (code : Code)
    (loaded : Val) (held : Nat) (framesDisciplined : AreFramesLockDisciplined core.stack)
    (isAccepted : (checkCode held code).isAccepted = true) :
    IsLockDisciplinedResult held (completeStatement program core code loaded) := by
  have unchanged : IsLockDisciplinedResult held { intent := .halt, core := core, code := code } :=
    IsLockDisciplinedResult.of_rest framesDisciplined .halt core code rfl isAccepted rfl
      (fun _ _ isFork => by cases isFork)
  cases code with
  | nil =>
    simp only [completeStatement]
    split
    · rename_i exceptionVar handlerCode afterCode outerFrames isStack
      have frameDisciplined := framesDisciplined _ (by rw [isStack]; exact List.mem_cons_self ..)
      simp only [Frame.isLockDisciplined, Bool.and_eq_true] at frameDisciplined
      exact IsLockDisciplinedResult.of_quiet
        (fun frame isMember => framesDisciplined frame
          (by rw [isStack]; exact List.mem_cons_of_mem _ isMember))
        0 (testBit_zero_inside held) frameDisciplined.2 rfl (fun _ _ isFork => by cases isFork)
    · exact lockDisciplined_returnValue held core .null framesDisciplined
  | cons instr rest =>
    cases instr with
    | park cv => exact unchanged
    | relock mutex => exact unchanged
    | source statement =>
      cases statement with
      | skip =>
        exact IsLockDisciplinedResult.of_rest framesDisciplined .silent core rest rfl
          (isAccepted_rest rfl isAccepted) rfl (fun _ _ isFork => by cases isFork)
      | seq first second =>
        refine IsLockDisciplinedResult.of_rest framesDisciplined .silent core _ rfl ?_ rfl
          (fun _ _ isFork => by cases isFork)
        simp only [checkCode, checkStmt, LockSetOutcome.andThen_assoc] at isAccepted
        simp only [checkCode_pushCode]
        exact isAccepted
      | assign target value =>
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined .silent _ rest rfl
            (isAccepted_rest rfl isAccepted) rfl (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | load target source =>
        exact IsLockDisciplinedResult.of_rest framesDisciplined .silent _ rest rfl
          (isAccepted_rest rfl isAccepted) rfl (fun _ _ isFork => by cases isFork)
      | store target value =>
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ rest rfl
            (isAccepted_rest rfl isAccepted) rfl (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | ite condition thenBranch elseBranch =>
        simp only [checkCode, checkStmt] at isAccepted
        simp only [completeStatement]
        split
        · refine IsLockDisciplinedResult.of_rest framesDisciplined .silent core _ rfl ?_ rfl
            (fun _ _ isFork => by cases isFork)
          rw [checkCode_pushCode]
          exact LockSetOutcome.isAccepted_andThen_of_join_left isAccepted
        · refine IsLockDisciplinedResult.of_rest framesDisciplined .silent core _ rfl ?_ rfl
            (fun _ _ isFork => by cases isFork)
          rw [checkCode_pushCode]
          exact LockSetOutcome.isAccepted_andThen_of_join_right isAccepted
        · exact unchanged
      | whileLoop condition body =>
        have original := isAccepted
        simp only [checkCode, checkStmt] at isAccepted
        obtain ⟨isBody, isRest⟩ := LockSetOutcome.join_continues_of_isAccepted isAccepted
        simp only [completeStatement]
        split
        · refine IsLockDisciplinedResult.of_rest framesDisciplined .silent core _ rfl ?_ rfl
            (fun _ _ isFork => by cases isFork)
          rw [checkCode_pushCode]
          rcases isBody with isEnded | isSame
          · rw [isEnded]
            rfl
          · rw [isSame]
            exact original
        · exact IsLockDisciplinedResult.of_rest framesDisciplined .silent core rest rfl isRest rfl
            (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | call result procName arguments =>
        simp only [checkCode, checkStmt, LockSetOutcome.andThen] at isAccepted
        simp only [completeStatement]
        split
        · rename_i proc values isProc _
          split
          · exact IsLockDisciplinedResult.of_quiet
              (fun frame isMember => by
                rcases List.mem_cons.mp isMember with isNew | isOld
                · rw [isNew]
                  exact isAccepted
                · exact framesDisciplined frame isOld)
              0 (testBit_zero_inside held) (isAccepted_procBody isDisciplined isProc) rfl
              (fun _ _ isFork => by cases isFork)
          · exact unchanged
        · exact unchanged
      | ret value =>
        simp only [completeStatement]
        split
        · exact lockDisciplined_returnValue held core _ framesDisciplined
        · exact unchanged
      | fork handle procName arguments =>
        have isRest := isAccepted_rest rfl isAccepted
        simp only [completeStatement]
        split
        · rename_i proc values isProc _
          split
          · exact
              { framesDisciplined := framesDisciplined
                continues := ⟨held, fun index isSet => ⟨isSet, by
                  simp [Intent.releasedStaticIndex?]⟩, isRest⟩
                forkDisciplined := fun child childCode isFork => by
                  cases isFork
                  exact ⟨rfl, isAccepted_procBody isDisciplined isProc⟩ }
          · exact unchanged
        · exact unchanged
      | join thread => exact unchanged
      | newMutex target =>
        exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ rest rfl
          (isAccepted_rest rfl isAccepted) rfl (fun _ _ isFork => by cases isFork)
      | lock mutex => exact unchanged
      | unlock mutex =>
        cases isIndex : staticMutexIndex? mutex with
        | none =>
          simp [checkCode, checkStmt, isIndex, LockSetOutcome.andThen,
            LockSetOutcome.isAccepted] at isAccepted
        | some index =>
          rw [eq_of_staticMutexIndex? isIndex] at isAccepted ⊢
          by_cases isHeld : held.testBit index = true
          · simp only [checkCode, checkStmt, staticMutexIndex?, isHeld, ↓reduceIte,
              LockSetOutcome.andThen] at isAccepted
            simp only [completeStatement, evalExpr]
            exact
              { framesDisciplined := framesDisciplined
                continues := ⟨held ^^^ lockBit index, fun other isSet => by
                  simpa [Intent.releasedStaticIndex?] using
                    testBit_xor_lockBit isHeld other isSet, isAccepted⟩
                forkDisciplined := fun _ _ isFork => by cases isFork }
          · simp [checkCode, checkStmt, staticMutexIndex?, isHeld, LockSetOutcome.andThen,
              LockSetOutcome.isAccepted] at isAccepted
      | wait cv mutex =>
        cases isCvIndex : staticCvMutexIndex? cv with
        | none =>
          simp [checkCode, checkStmt, isCvIndex, LockSetOutcome.andThen,
            LockSetOutcome.isAccepted] at isAccepted
        | some cvMutexIndex =>
          cases isIndex : staticMutexIndex? mutex with
          | none =>
            simp [checkCode, checkStmt, isCvIndex, isIndex, LockSetOutcome.andThen,
              LockSetOutcome.isAccepted] at isAccepted
          | some index =>
            obtain ⟨cvIndex, isCv⟩ := eq_of_staticCvMutexIndex? isCvIndex
            rw [isCv, eq_of_staticMutexIndex? isIndex] at isAccepted ⊢
            by_cases isGood : cvMutexIndex = index ∧ held.testBit index = true
            · obtain ⟨isSame, isHeld⟩ := isGood
              subst isSame
              simp only [checkCode, checkStmt, staticCvMutexIndex?, staticMutexIndex?, isHeld,
                and_self, ↓reduceIte, LockSetOutcome.andThen] at isAccepted
              simp only [completeStatement, evalExpr, ↓reduceIte]
              refine
                { framesDisciplined := framesDisciplined
                  continues := ⟨held ^^^ lockBit cvMutexIndex, fun other isSet => by
                    simpa [Intent.releasedStaticIndex?] using
                      testBit_xor_lockBit isHeld other isSet, ?_⟩
                  forkDisciplined := fun _ _ isFork => by cases isFork }
              simp only [checkCode, xor_or_lockBit isHeld]
              exact isAccepted
            · simp only [checkCode, checkStmt, staticCvMutexIndex?, staticMutexIndex?] at isAccepted
              simp only [isGood, ↓reduceIte, LockSetOutcome.andThen,
                LockSetOutcome.isAccepted] at isAccepted
              exact absurd isAccepted Bool.false_ne_true
      | signal cv =>
        have isRest : (checkCode held rest).isAccepted = true := by
          simp only [checkCode, checkStmt] at isAccepted
          split at isAccepted
          · split at isAccepted
            · exact isAccepted
            · simp [LockSetOutcome.andThen, LockSetOutcome.isAccepted] at isAccepted
          · simp [LockSetOutcome.andThen, LockSetOutcome.isAccepted] at isAccepted
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ rest rfl isRest rfl
            (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | broadcast cv =>
        have isRest : (checkCode held rest).isAccepted = true := by
          simp only [checkCode, checkStmt] at isAccepted
          split at isAccepted
          · split at isAccepted
            · exact isAccepted
            · simp [LockSetOutcome.andThen, LockSetOutcome.isAccepted] at isAccepted
          · simp [LockSetOutcome.andThen, LockSetOutcome.isAccepted] at isAccepted
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ rest rfl isRest rfl
            (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | sleep seconds =>
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ _ rfl isAccepted rfl
            (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | select target chan onMessage seconds onTimeout =>
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ _ rfl isAccepted rfl
            (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | recv target chan => exact unchanged
      | emit effect =>
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined .silent _ rest rfl
            (isAccepted_rest rfl isAccepted) rfl (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | raise exception =>
        simp only [completeStatement]
        split
        · split
          · rename_i exceptionVar handlerCode restoredLocals outerFrames isUnwound
            obtain ⟨handlerAccepted, outerDisciplined⟩ :=
              lockDisciplined_unwindToHandler core.stack framesDisciplined _ _ _ _ _ isUnwound
            exact IsLockDisciplinedResult.of_quiet outerDisciplined 0 (testBit_zero_inside held)
              handlerAccepted rfl (fun _ _ isFork => by cases isFork)
          · split
            · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ [] rfl rfl rfl
                (fun _ _ isFork => by cases isFork)
            · exact unchanged
        · exact unchanged
      | tryCatch body exceptionVar handler =>
        simp only [checkCode, checkStmt] at isAccepted
        split at isAccepted
        · rename_i isBody
          obtain ⟨isHandler, isRest⟩ := LockSetOutcome.join_continues_of_isAccepted isAccepted
          refine IsLockDisciplinedResult.of_quiet ?_ 0 (testBit_zero_inside held) ?_ rfl
            (fun _ _ isFork => by cases isFork)
          · intro frame isMember
            rcases List.mem_cons.mp isMember with isNew | isOld
            · rw [isNew]
              simp only [Frame.isLockDisciplined, Bool.and_eq_true]
              refine ⟨?_, isRest⟩
              rw [checkCode_pushCode]
              rcases isHandler with isEnded | isSame
              · rw [isEnded]
                rfl
              · rw [isSame]
                exact isRest
            · exact framesDisciplined frame isOld
          · show (checkCode 0 (pushCode body [])).isAccepted = true
            rw [checkCode_pushCode]
            revert isBody
            cases checkStmt 0 body <;> simp [LockSetOutcome.andThen, checkCode]
        · simp [LockSetOutcome.andThen, LockSetOutcome.isAccepted] at isAccepted
      | cancel thread =>
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ rest rfl
            (isAccepted_rest rfl isAccepted) rfl (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | exit value =>
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ [] rfl rfl rfl
            (fun _ _ isFork => by cases isFork)
        · exact unchanged
      | fail message =>
        simp only [completeStatement]
        split
        · exact IsLockDisciplinedResult.of_rest framesDisciplined _ _ [] rfl rfl rfl
            (fun _ _ isFork => by cases isFork)
        · exact unchanged

/-- What the soundness proof needs from continuing after a wait that the
analysis accepted from `held`: the frames still obey the discipline, and the
code is accepted from a lock set inside `held` plus the statically known mutex
that a granted `lock` acquired. -/
def IsLockDisciplinedResume (core : ThreadCore) (code : Code) (outcome : WakeOutcome)
    (held : Nat) (result : StatementResult) : Prop :=
  AreFramesLockDisciplined result.core.stack ∧
    ∃ after, (∀ index, after.testBit index = true → held.testBit index = true ∨
        (outcome = .granted ∧ waitRequest core code = some (.lock (.static index)))) ∧
      (checkCode after result.code).isAccepted = true

/-- A continuation that keeps the frames and has code accepted from `held`. -/
theorem IsLockDisciplinedResume.of_held {core : ThreadCore} {code : Code} {outcome : WakeOutcome}
    {held : Nat} (framesDisciplined : AreFramesLockDisciplined core.stack) (intent : Intent)
    (newCore : ThreadCore) (newCode : Code) (sameStack : newCore.stack = core.stack)
    (isAccepted : (checkCode held newCode).isAccepted = true) :
    IsLockDisciplinedResume core code outcome held
      { intent := intent, core := newCore, code := newCode } :=
  ⟨by show AreFramesLockDisciplined newCore.stack; rw [sameStack]; exact framesDisciplined,
    held, fun _ isSet => Or.inl isSet, isAccepted⟩

/-- A granted `lock` of a statically known mutex continues with that mutex added. -/
theorem IsLockDisciplinedResume.of_granted {core : ThreadCore} {code : Code} {held index : Nat}
    (framesDisciplined : AreFramesLockDisciplined core.stack)
    (isRequest : waitRequest core code = some (.lock (.static index))) (rest : Code)
    (isAccepted : (checkCode (held ||| lockBit index) rest).isAccepted = true) :
    IsLockDisciplinedResume core code .granted held
      { intent := .silent, core := core, code := rest } := by
  refine ⟨framesDisciplined, held ||| lockBit index, ?_, isAccepted⟩
  intro other isSet
  rw [Nat.testBit_or, testBit_lockBit, Bool.or_eq_true, decide_eq_true_eq] at isSet
  rcases isSet with isHeld | isSame
  · exact Or.inl isHeld
  · subst isSame
    exact Or.inr ⟨rfl, isRequest⟩

/-- Continuing after a blocking statement that the analysis accepts keeps the
discipline. -/
theorem lockDisciplined_resumeBlocked (core : ThreadCore) (code : Code) (outcome : WakeOutcome)
    (held : Nat) (framesDisciplined : AreFramesLockDisciplined core.stack)
    (isAccepted : (checkCode held code).isAccepted = true) :
    IsLockDisciplinedResume core code outcome held (resumeBlocked core code outcome) := by
  simp only [resumeBlocked]
  split
  · rename_i mutex rest
    cases isIndex : staticMutexIndex? mutex with
    | none =>
      simp only [checkCode, checkStmt, isIndex, LockSetOutcome.andThen] at isAccepted
      exact IsLockDisciplinedResume.of_held framesDisciplined _ _ rest rfl isAccepted
    | some index =>
      have isMutex := eq_of_staticMutexIndex? isIndex
      subst isMutex
      simp only [checkCode, checkStmt, staticMutexIndex?, LockSetOutcome.andThen] at isAccepted
      exact IsLockDisciplinedResume.of_granted framesDisciplined
        (by simp [waitRequest, evalExpr]) rest isAccepted
  · rename_i cv rest
    exact IsLockDisciplinedResume.of_held framesDisciplined _ _ rest rfl isAccepted
  · rename_i mutex rest
    cases mutex with
    | static index =>
      exact IsLockDisciplinedResume.of_granted framesDisciplined rfl rest isAccepted
    | dynamic creator index =>
      simp [checkCode, LockSetOutcome.isAccepted] at isAccepted
  · rename_i thread rest
    exact IsLockDisciplinedResume.of_held framesDisciplined _ _ rest rfl
      (isAccepted_rest rfl isAccepted)
  · exact IsLockDisciplinedResume.of_held framesDisciplined _ _ [] rfl rfl
  · rename_i target chan rest message
    exact IsLockDisciplinedResume.of_held framesDisciplined _ _ rest rfl
      (isAccepted_rest rfl isAccepted)
  · exact IsLockDisciplinedResume.of_held framesDisciplined _ _ [] rfl rfl
  · exact IsLockDisciplinedResume.of_held framesDisciplined _ _ _ rfl isAccepted

/-- Continuing after a timed operation that the analysis accepts keeps the
discipline. -/
theorem lockDisciplined_resumeTimed (core : ThreadCore) (code : Code) (outcome : WakeOutcome)
    (held : Nat) (framesDisciplined : AreFramesLockDisciplined core.stack)
    (isAccepted : (checkCode held code).isAccepted = true) :
    IsLockDisciplinedResume core code outcome held (resumeTimed core code outcome) := by
  simp only [resumeTimed]
  split
  · rename_i seconds rest
    exact IsLockDisciplinedResume.of_held framesDisciplined _ _ rest rfl
      (isAccepted_rest rfl isAccepted)
  · exact IsLockDisciplinedResume.of_held framesDisciplined _ _ [] rfl rfl
  · rename_i target chan onMessage seconds onTimeout rest
    simp only [checkCode, checkStmt] at isAccepted
    refine IsLockDisciplinedResume.of_held framesDisciplined _ _ _ rfl ?_
    rw [checkCode_pushCode]
    exact LockSetOutcome.isAccepted_andThen_of_join_right isAccepted
  · rename_i target chan onMessage seconds onTimeout rest message
    simp only [checkCode, checkStmt] at isAccepted
    refine IsLockDisciplinedResume.of_held framesDisciplined _ _ _ rfl ?_
    rw [checkCode_pushCode]
    exact LockSetOutcome.isAccepted_andThen_of_join_left isAccepted
  · exact IsLockDisciplinedResume.of_held framesDisciplined _ _ [] rfl rfl
  · exact IsLockDisciplinedResume.of_held framesDisciplined _ _ _ rfl isAccepted

/-- The mutex that an accepted head statement requires is a statically known
mutex of the lock set. -/
theorem requiredMutex_mem_of_isAccepted {core : ThreadCore} {code : Code} {held : Nat}
    {mutex : MutexId} (isAccepted : (checkCode held code).isAccepted = true)
    (isRequired : requiredMutex core code = some mutex) :
    ∃ index, mutex = .static index ∧ held.testBit index = true := by
  cases code with
  | nil => simp [requiredMutex, cvOperand] at isRequired
  | cons instr rest =>
    cases instr with
    | park cv => simp [requiredMutex, cvOperand] at isRequired
    | relock lockedMutex => simp [requiredMutex, cvOperand] at isRequired
    | source statement =>
      cases statement
      all_goals try (simp [requiredMutex, cvOperand] at isRequired; done)
      case unlock operand =>
        cases isIndex : staticMutexIndex? operand with
        | none =>
          simp [checkCode, checkStmt, isIndex, LockSetOutcome.andThen,
            LockSetOutcome.isAccepted] at isAccepted
        | some index =>
          have isOperand := eq_of_staticMutexIndex? isIndex
          subst isOperand
          simp only [requiredMutex, evalExpr, Option.some.injEq] at isRequired
          refine ⟨index, isRequired.symm, ?_⟩
          by_cases isHeld : held.testBit index = true
          · exact isHeld
          · simp [checkCode, checkStmt, staticMutexIndex?, isHeld, LockSetOutcome.andThen,
              LockSetOutcome.isAccepted] at isAccepted
      case wait cv operand =>
        cases isCvIndex : staticCvMutexIndex? cv with
        | none =>
          simp [checkCode, checkStmt, isCvIndex, LockSetOutcome.andThen,
            LockSetOutcome.isAccepted] at isAccepted
        | some cvMutexIndex =>
          cases isIndex : staticMutexIndex? operand with
          | none =>
            simp [checkCode, checkStmt, isCvIndex, isIndex, LockSetOutcome.andThen,
              LockSetOutcome.isAccepted] at isAccepted
          | some index =>
            obtain ⟨cvIndex, isCv⟩ := eq_of_staticCvMutexIndex? isCvIndex
            rw [isCv, eq_of_staticMutexIndex? isIndex] at isAccepted isRequired
            by_cases isGood : cvMutexIndex = index ∧ held.testBit index = true
            · obtain ⟨isSame, isHeld⟩ := isGood
              subst isSame
              refine ⟨cvMutexIndex, ?_, isHeld⟩
              simp [requiredMutex, cvOperand, evalExpr] at isRequired
              first
                | exact isRequired.symm
                | exact isRequired
            · simp only [checkCode, checkStmt, staticCvMutexIndex?, staticMutexIndex?, isGood,
                ↓reduceIte, LockSetOutcome.andThen, LockSetOutcome.isAccepted] at isAccepted
              exact absurd isAccepted Bool.false_ne_true
      case signal cv =>
        cases isCvIndex : staticCvMutexIndex? cv with
        | none =>
          simp [checkCode, checkStmt, isCvIndex, LockSetOutcome.andThen,
            LockSetOutcome.isAccepted] at isAccepted
        | some index =>
          obtain ⟨cvIndex, isCv⟩ := eq_of_staticCvMutexIndex? isCvIndex
          subst isCv
          simp only [requiredMutex, cvOperand, evalExpr, Option.map_some,
            Option.some.injEq] at isRequired
          refine ⟨index, isRequired.symm, ?_⟩
          by_cases isHeld : held.testBit index = true
          · exact isHeld
          · simp [checkCode, checkStmt, staticCvMutexIndex?, staticMutexIndex?, isHeld,
              LockSetOutcome.andThen, LockSetOutcome.isAccepted] at isAccepted
      case broadcast cv =>
        cases isCvIndex : staticCvMutexIndex? cv with
        | none =>
          simp [checkCode, checkStmt, isCvIndex, LockSetOutcome.andThen,
            LockSetOutcome.isAccepted] at isAccepted
        | some index =>
          obtain ⟨cvIndex, isCv⟩ := eq_of_staticCvMutexIndex? isCvIndex
          subst isCv
          simp only [requiredMutex, cvOperand, evalExpr, Option.map_some,
            Option.some.injEq] at isRequired
          refine ⟨index, isRequired.symm, ?_⟩
          by_cases isHeld : held.testBit index = true
          · exact isHeld
          · simp [checkCode, checkStmt, staticCvMutexIndex?, staticMutexIndex?, isHeld,
              LockSetOutcome.andThen, LockSetOutcome.isAccepted] at isAccepted

end DexExpressiveness.L
