import DexExpressiveness.Simulation.Completion.Wake

/-!
# The data part of the relation across `effects`

`MidState` bundles the three parts of the relation while an Execute commits.
`MidData.transfer` rebuilds the data part after publications: each family of
synchronization Channels either keeps its messages and its L counterpart, or
the caller proves its clause anew. `MidData.writes` does the same for
Attribute writes.

The file also has what the completion steps need to know about how an
expected wait is decoded.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The relation while the Execute of thread `tid`, carried by Step `stepId`,
commits. -/
structure MidState (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (run : Dex.Run signature) (tid : Tid) (stepId : Dex.StepExecId signature) : Prop where
  /-- Threads and Steps. -/
  threads : MidThreads program lConfig dexConfig run tid stepId
  /-- Attributes, Channels, and counters. -/
  data : MidData lConfig run tid
  /-- Mutex tokens. -/
  tokens : TokensRelated lConfig run

/-- The thread lookup after a wake. -/
theorem wp3b_findThread?_wake (lConfig lNext : L.Config) (woken : L.Thread → Option WakeOutcome)
    (threadsNext : lNext.threads = lConfig.threads.map (wakeThreadBy woken)) (target : Tid) :
    lNext.findThread? target = (lConfig.findThread? target).map (wakeThreadBy woken) := by
  unfold L.Config.findThread?
  rw [threadsNext, List.find?_map]
  have samePredicate : ((fun candidate : L.Thread => decide (candidate.core.tid = target)) ∘
      wakeThreadBy woken) = fun candidate => decide (candidate.core.tid = target) := by
    funext candidate
    have sameCore : (wakeThreadBy woken candidate).core = candidate.core := by
      unfold wakeThreadBy
      split <;> rfl
    simp only [Function.comp, sameCore]
  rw [samePredicate]

/-- A wake that selects no thread changes no thread. -/
theorem wp3b_map_wakeThreadBy_none (threads : List L.Thread) :
    threads.map (wakeThreadBy (fun _ => none)) = threads := by
  have noThread : wakeThreadBy (fun _ => none) = id := by
    funext thread
    rfl
  rw [noThread, List.map_id]

/-- The wake of one thread, as a wake by selection. -/
theorem wp3b_wakeThread_threads (config : L.Config) (target : Tid) (outcome : WakeOutcome) :
    (config.wakeThread target outcome).threads =
      config.threads.map (wakeThreadBy (fun candidate =>
        if candidate.core.tid = target then some outcome else none)) := by
  simp only [L.Config.wakeThread, L.Config.setPhase, L.Config.updateThread]
  apply List.map_congr_left
  intro candidate _
  by_cases isTarget : candidate.core.tid = target <;> simp [wakeThreadBy, isTarget]

/-- The data part after publications. Each family of synchronization Channels
either keeps its messages and its L counterpart (`Or.inl`), or its clause is
proved anew (`Or.inr`). External Channels, Attributes, and counters are
unchanged. -/
theorem MidData.transfer {lConfig : L.Config} {run : Dex.Run signature} {tid : Tid}
    (data : MidData lConfig run tid) (lNext : L.Config) (runNext : Dex.Run signature)
    (woken : L.Thread → Option WakeOutcome)
    (threadsNext : lNext.threads = lConfig.threads.map (wakeThreadBy woken))
    (wokenWereBlocked : ∀ thread, thread ∈ lConfig.threads → ∀ outcome,
      woken thread = some outcome → ∃ on since, thread.phase = .blocked on since)
    (sameStatus : runNext.status = run.status) (sameOutputs : runNext.outputs = run.outputs)
    (sameCounters : runNext.counters = run.counters)
    (sameAttributes : runNext.attributes = run.attributes)
    (sameCounter : lNext.timerCounter = lConfig.timerCounter)
    (sameShared : lNext.shared = lConfig.shared) (sameCvQueues : lNext.cvQueues = lConfig.cvQueues)
    (sameInput : lNext.inputQueues = lConfig.inputQueues)
    (external : ∀ chan, runNext.pendingMessages (Chan.external chan) =
      run.pendingMessages (Chan.external chan))
    (joinTokens :
      ((∀ thread, runNext.pendingMessages (Chan.joinDone thread) =
          run.pendingMessages (Chan.joinDone thread)) ∧ lNext.joinable = lConfig.joinable) ∨
      ∀ thread, (runNext.pendingMessages (Chan.joinDone thread)).length =
        if lNext.joinable.contains thread then 1 else 0)
    (cvWakes :
      ((∀ cv waiter, runNext.pendingMessages (Chan.cvWake cv waiter) =
          run.pendingMessages (Chan.cvWake cv waiter)) ∧ lNext.cvWakes = lConfig.cvWakes) ∨
      ∀ cv waiter, (runNext.pendingMessages (Chan.cvWake cv waiter)).length =
        lNext.cvWakes.count (cv, waiter))
    (flags :
      ((∀ thread, runNext.pendingMessages (Chan.cancelRequest thread) =
          run.pendingMessages (Chan.cancelRequest thread)) ∧
        lNext.cancelFlags = lConfig.cancelFlags) ∨
      ((∀ thread, runNext.pendingMessages (Chan.cancelRequest thread) ≠ [] →
          lNext.cancelFlags.contains thread = true) ∧
        (∀ thread, thread ≠ tid → lNext.cancelFlags.contains thread = true →
          runNext.pendingMessages (Chan.cancelRequest thread) ≠ [] ∨
          (∃ current, lNext.findThread? thread = some current ∧
            (current.phase = .woken .canceled ∨ current.phase = .resuming .canceled)) ∨
          (lNext.findThread? thread = none ∧
            ∀ live, live ∈ lNext.threads → ∀ index, live.core.freshCounter ≤ index →
              ¬ (live.core.tid ++ [index]) <+: thread)) ∧
        (lNext.cancelFlags.contains tid = true →
          runNext.pendingMessages (Chan.cancelRequest tid) ≠ [] ∨
          lNext.findThread? tid = none))) :
    MidData lNext runNext tid := by
  have findNext := wp3b_findThread?_wake lConfig lNext woken threadsNext
  have liveNext : ∀ live, live ∈ lNext.threads →
      ∃ original, original ∈ lConfig.threads ∧ live.core = original.core := by
    intro live isLive
    rw [threadsNext] at isLive
    obtain ⟨original, isOriginal, rfl⟩ := List.mem_map.mp isLive
    refine ⟨original, isOriginal, ?_⟩
    unfold wakeThreadBy
    split <;> rfl
  exact
    { isRunning := by rw [sameStatus]; exact data.isRunning
      shared := by
        intro name
        have sameRead : lNext.readShared name = lConfig.readShared name := by
          simp [L.Config.readShared, sameShared]
        rw [sameRead, sameAttributes]
        exact data.shared name
      cvQueues := by
        intro cv
        rw [sameCvQueues, sameAttributes]
        exact data.cvQueues cv
      inputQueues := by
        intro chan
        have sameRead : lNext.pendingInput chan = lConfig.pendingInput chan := by
          simp [L.Config.pendingInput, sameInput]
        rw [sameRead, external]
        exact data.inputQueues chan
      joinTokens := by
        rcases joinTokens with ⟨samePending, sameJoinable⟩ | isProved
        · intro thread
          rw [samePending, sameJoinable]
          exact data.joinTokens thread
        · exact isProved
      cancelRequestsAreFlags := by
        rcases flags with ⟨samePending, sameFlags⟩ | isProved
        · intro thread
          rw [samePending, sameFlags]
          exact data.cancelRequestsAreFlags thread
        · exact isProved.1
      cancelFlagsAreRequests := by
        rcases flags with ⟨samePending, sameFlags⟩ | isProved
        · intro thread isOther isFlagged
          rw [sameFlags] at isFlagged
          rw [samePending, findNext]
          rcases data.cancelFlagsAreRequests thread isOther isFlagged with hasRequest |
            ⟨current, isFound, isCanceled⟩ | ⟨isGone, isNoChild⟩
          · exact Or.inl hasRequest
          · right
            left
            have isCurrent := (wp3b_findThread?_some lConfig thread current isFound).1
            have isKept : woken current = none := by
              cases wokenCase : woken current with
              | none => rfl
              | some outcome =>
                obtain ⟨on, since, isBlocked⟩ := wokenWereBlocked current isCurrent outcome wokenCase
                rcases isCanceled with isWoken | isResuming
                · rw [isWoken] at isBlocked
                  cases isBlocked
                · rw [isResuming] at isBlocked
                  cases isBlocked
            refine ⟨current, ?_, isCanceled⟩
            rw [isFound]
            simp [wakeThreadBy, isKept]
          · refine Or.inr (Or.inr ⟨by rw [isGone]; rfl, ?_⟩)
            intro live isLive index isAbove
            obtain ⟨original, isOriginal, sameCore⟩ := liveNext live isLive
            rw [sameCore] at isAbove ⊢
            exact isNoChild original isOriginal index isAbove
        · exact isProved.2.1
      ownIsNoChild := by
        intro live isLive index isAbove
        obtain ⟨original, isOriginal, sameCore⟩ := liveNext live isLive
        rw [sameCore] at isAbove ⊢
        exact data.ownIsNoChild original isOriginal index isAbove
      ownFlag := by
        rcases flags with ⟨samePending, sameFlags⟩ | isProved
        · intro isFlagged
          rw [sameFlags] at isFlagged
          rw [samePending, findNext]
          rcases data.ownFlag isFlagged with hasRequest | isGone
          · exact Or.inl hasRequest
          · exact Or.inr (by rw [isGone]; rfl)
        · exact isProved.2.2
      cvWakes := by
        rcases cvWakes with ⟨samePending, sameWakes⟩ | isProved
        · intro cv waiter
          rw [samePending, sameWakes]
          exact data.cvWakes cv waiter
        · exact isProved
      timerCounter := by
        rw [sameCounters, sameCounter]
        exact data.timerCounter
      outputsEmpty := by rw [sameOutputs]; exact data.outputsEmpty }

/-- The data part after Attribute writes: the caller proves the two Attribute
clauses; everything else is unchanged. -/
theorem MidData.writes {lConfig : L.Config} {run : Dex.Run signature} {tid : Tid}
    (data : MidData lConfig run tid) (lNext : L.Config)
    (writes : List (Key × Option DexVal))
    (sameThreads : lNext.threads = lConfig.threads)
    (sameCounter : lNext.timerCounter = lConfig.timerCounter)
    (sameInput : lNext.inputQueues = lConfig.inputQueues)
    (sameJoinable : lNext.joinable = lConfig.joinable)
    (sameFlags : lNext.cancelFlags = lConfig.cancelFlags)
    (sameWakes : lNext.cvWakes = lConfig.cvWakes)
    (shared : ∀ name,
      readShared { attributes := (run.applyWrites writes).attributes } name = lNext.readShared name)
    (cvQueues : ∀ cv, readWaiters { attributes := (run.applyWrites writes).attributes } cv =
      (lNext.cvQueues.get? cv).getD []) :
    MidData lNext (run.applyWrites writes) tid := by
  obtain ⟨_, sameCounters, _, sameChannels, _, sameStatus, sameOutputs⟩ :=
    Dex.Run.applyWrites_frame writes run
  have samePending : ∀ chan, (run.applyWrites writes).pendingMessages chan =
      run.pendingMessages chan := by
    intro chan
    simp [Dex.Run.pendingMessages, sameChannels]
  have sameFind : ∀ target, lNext.findThread? target = lConfig.findThread? target := by
    intro target
    simp [L.Config.findThread?, sameThreads]
  exact
    { isRunning := by rw [sameStatus]; exact data.isRunning
      shared := shared
      cvQueues := cvQueues
      inputQueues := by
        intro chan
        have sameRead : lNext.pendingInput chan = lConfig.pendingInput chan := by
          simp [L.Config.pendingInput, sameInput]
        rw [sameRead, samePending]
        exact data.inputQueues chan
      joinTokens := by
        intro thread
        rw [samePending, sameJoinable]
        exact data.joinTokens thread
      cancelRequestsAreFlags := by
        intro thread
        rw [samePending, sameFlags]
        exact data.cancelRequestsAreFlags thread
      cancelFlagsAreRequests := by
        intro thread isOther isFlagged
        rw [sameFlags] at isFlagged
        rw [samePending, sameFind, sameThreads]
        exact data.cancelFlagsAreRequests thread isOther isFlagged
      ownIsNoChild := by
        rw [sameThreads]
        exact data.ownIsNoChild
      ownFlag := by
        intro isFlagged
        rw [sameFlags] at isFlagged
        rw [samePending, sameFind]
        exact data.ownFlag isFlagged
      cvWakes := by
        intro cv waiter
        rw [samePending, sameWakes]
        exact data.cvWakes cv waiter
      timerCounter := by
        rw [sameCounters, sameCounter]
        exact data.timerCounter
      outputsEmpty := by rw [sameOutputs]; exact data.outputsEmpty }

/-! ## Decoding the condition results of an expected wait -/

/-- A granted `lock`: the token of the mutex. -/
theorem wp3b_decode_lock (thread : L.Thread) (mutex : MutexId) (noTimer : thread.timer = none)
    (isLock : waitRequest thread.core thread.code = some (.lock mutex)) (timersDone : List Bool)
    (message : DexVal) :
    decodeOutcome thread (some { timersDone := timersDone, channelMessages := [some message] }) =
      .granted := by
  unfold decodeOutcome
  simp only [noTimer, Option.isSome_none, Bool.false_eq_true, ↓reduceIte]
  generalize thread.code = code at isLock
  cases code with
  | nil => simp [waitRequest] at isLock
  | cons instr rest =>
    cases instr with
    | park cv => simp [waitRequest] at isLock
    | relock other => simp [decodeBlockedOutcome]
    | source statement =>
      cases statement <;>
        first
          | (simp [waitRequest] at isLock; done)
          | (simp [decodeBlockedOutcome]; done)
          | (simp only [waitRequest] at isLock; split at isLock <;> simp at isLock; done)

/-- A granted `park`: the wake of the waiter. -/
theorem wp3b_decode_park (thread : L.Thread) (cv : CvId) (noTimer : thread.timer = none)
    (isPark : waitRequest thread.core thread.code = some (.park cv)) (timersDone : List Bool)
    (message : DexVal) :
    decodeOutcome thread (some { timersDone := timersDone, channelMessages := [some message] }) =
      .granted := by
  unfold decodeOutcome
  simp only [noTimer, Option.isSome_none, Bool.false_eq_true, ↓reduceIte]
  generalize thread.code = code at isPark
  cases code with
  | nil => simp [waitRequest] at isPark
  | cons instr rest =>
    cases instr with
    | park other => simp [decodeBlockedOutcome]
    | relock other => simp [waitRequest] at isPark
    | source statement =>
      cases statement <;>
        first
          | (simp [waitRequest] at isPark; done)
          | (simp [decodeBlockedOutcome]; done)
          | (simp only [waitRequest] at isPark; split at isPark <;> simp at isPark; done)

/-- A granted `join`: the notice of the joined thread. -/
theorem wp3b_decode_join (thread : L.Thread) (joined : Tid) (noTimer : thread.timer = none)
    (isJoin : waitRequest thread.core thread.code = some (.join joined)) (timersDone : List Bool)
    (message : DexVal) :
    decodeOutcome thread
      (some { timersDone := timersDone, channelMessages := [some message, none] }) =
      .granted := by
  unfold decodeOutcome
  simp only [noTimer, Option.isSome_none, Bool.false_eq_true, ↓reduceIte]
  generalize thread.code = code at isJoin
  cases code with
  | nil => simp [waitRequest] at isJoin
  | cons instr rest =>
    cases instr with
    | park other => simp [waitRequest] at isJoin
    | relock other => simp [waitRequest] at isJoin
    | source statement =>
      cases statement <;>
        first
          | (simp [waitRequest] at isJoin; done)
          | (simp [decodeBlockedOutcome]; done)
          | (simp only [waitRequest] at isJoin; split at isJoin <;> simp at isJoin; done)

/-- A cancel observed at a blocked cancellation point: `recv`, `join`, or a
timed operation. -/
theorem wp3b_decode_canceled (thread : L.Thread) (on : BlockedOn) (since : Nat)
    (isBlocked : thread.phase = .blocked on since) (fits : thread.PhaseFitsCode)
    (isCancellationPoint : Chan.cancelRequest thread.core.tid ∈
      (expectedWaitShape thread.core.tid on).2.2)
    (message : DexVal) :
    decodeOutcome thread (some
      { timersDone := List.replicate (expectedWaitShape thread.core.tid on).2.1 false
        channelMessages := (expectedWaitShape thread.core.tid on).2.2.map (fun other =>
          if other = Chan.cancelRequest thread.core.tid then some message else none) }) =
      .canceled := by
  unfold Thread.PhaseFitsCode at fits
  cases on with
  | lock mutex => simp [expectedWaitShape] at isCancellationPoint
  | park cv => simp [expectedWaitShape] at isCancellationPoint
  | recv chan =>
    simp only [isBlocked, BlockedOn.toWaitRequest?] at fits
    obtain ⟨noTimer, isRecv⟩ := fits
    unfold decodeOutcome
    simp only [noTimer, Option.isSome_none, Bool.false_eq_true, ↓reduceIte, expectedWaitShape,
      List.map_cons, List.map_nil, reduceCtorEq]
    generalize thread.code = code at isRecv
    cases code with
    | nil => simp [waitRequest] at isRecv
    | cons instr rest =>
      cases instr with
      | park other => simp [waitRequest] at isRecv
      | relock other => simp [waitRequest] at isRecv
      | source statement =>
        cases statement <;>
          first
            | (simp [waitRequest] at isRecv; done)
            | (simp [decodeBlockedOutcome]; done)
            | (simp only [waitRequest] at isRecv; split at isRecv <;> simp at isRecv; done)
  | join joined =>
    simp only [isBlocked, BlockedOn.toWaitRequest?] at fits
    obtain ⟨noTimer, isJoin⟩ := fits
    unfold decodeOutcome
    simp only [noTimer, Option.isSome_none, Bool.false_eq_true, ↓reduceIte, expectedWaitShape,
      List.map_cons, List.map_nil, reduceCtorEq]
    generalize thread.code = code at isJoin
    cases code with
    | nil => simp [waitRequest] at isJoin
    | cons instr rest =>
      cases instr with
      | park other => simp [waitRequest] at isJoin
      | relock other => simp [waitRequest] at isJoin
      | source statement =>
        cases statement <;>
          first
            | (simp [waitRequest] at isJoin; done)
            | (simp [decodeBlockedOutcome]; done)
            | (simp only [waitRequest] at isJoin; split at isJoin <;> simp at isJoin; done)
  | timed number deadline chan =>
    simp only [isBlocked] at fits
    obtain ⟨⟨seconds, hasTimer⟩, isTimed⟩ := fits
    unfold decodeOutcome
    cases chan with
    | none =>
      simp [hasTimer, decodeTimedOutcome, isTimed, expectedWaitShape]
    | some external =>
      simp [hasTimer, decodeTimedOutcome, isTimed, expectedWaitShape]

end DexExpressiveness
