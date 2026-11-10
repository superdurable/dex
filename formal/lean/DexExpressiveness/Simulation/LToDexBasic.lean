import DexExpressiveness.Simulation.LToDexBasicFire

/-!
# L to Dex: ticks, requests, timer fires, late emits, and the begin of an action

Work package 2 of the safety proof. Each lemma shows that one kind of L step
is simulated by exactly the Dex actions that `simulatingActions` names
(`IsSimulatedByActions`):

| L step | Dex actions |
| --- | --- |
| `tick d` | `tick d` |
| `request (start v)` | `request (startFlow start enc(v))` |
| `request (input c v)`, running or closed | `request (publish c enc(v))` |
| `fire count` | `timerFire count` |
| `lateDeliver i` | `workerRun` if needed, then `workerEffect` of the first task that still holds that effect |
| `lateDrop i` | `workerAbandon` of the first task that still holds that effect |
| `thread t` from `bootPending`, `ready`, or `woken` | `stepStart` of the Step that carries `t` |
| `thread t` from `emitting` | `workerRun` if needed, then `workerEffect` of that Step's task |

Every lemma of this package is proved. Four of them use a fact about the Dex
configuration that `IsSimulationState` gives through
`Simulation/RelationLemmas.lean`:

| Lemma | Fact | Proved form |
| --- | --- | --- |
| `l_input_simulated` | A Step in phase `WT` has no invocation (`IsSimulationState.waitingHasNoInvocation`). | `wp2_input_simulated_of_waitingHasNoInvocation` |
| `l_fire_simulated` | The keys of `run.timers` are distinct (`IsSimulationState.timerKeysNodup`). | `wp2_fire_simulated_of_timerKeysNodup` |
| `l_lateDeliver_simulated` | A task holds at most one effect (`IsSimulationState.holdsAtMostOneEffect`). | `wp2_lateDeliver_simulated_of_singleEffects` |
| `l_lateDrop_simulated` | A task holds at most one effect. | `wp2_lateDrop_simulated_of_singleEffects` |

The lemmas named `wp2_*_exec` state the Dex action sequence of a case
explicitly: `wp2_exec_stepStart`, `wp2_thread_deliver_exec`,
`wp2_input_exec_running`, `wp2_input_exec_closed`, `wp2_fire_exec`,
`wp2_lateDeliver_exec`, and `wp2_lateDrop_exec`. The helper lemmas are in
`Simulation/LToDexBasicLemmas.lean`, `Simulation/LToDexBasicWake.lean`, and
`Simulation/LToDexBasicFire.lean`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- An idle tick is simulated by [TICK]. -/
theorem l_tick_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (duration : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.tick duration) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.tick duration) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i isPositive
    injection isStep with isPair
    injection isPair with isEvents isNext
    subst isEvents
    subst isNext
    refine ⟨[], { dexConfig with clock := dexConfig.clock + duration }, ?_, rfl, ?_⟩
    · simp [simulatingActions, TransitionSystem.exec, Dex.system, Dex.step, isPositive]
    · exact
        { clock := by simp [state.isRelated.clock]
          stamp := state.isRelated.stamp
          status := state.isRelated.status
          notStarted := state.isRelated.notStarted
          running := fun run hasRun isRunning =>
            let related := state.isRelated.running run hasRun isRunning
            { shared := related.shared
              cvQueues := related.cvQueues
              inputQueues := related.inputQueues
              mutexTokens := related.mutexTokens
              mutexesBeforeBoot := related.mutexesBeforeBoot
              joinTokens := related.joinTokens
              cancelRequestsAreFlags := related.cancelRequestsAreFlags
              cancelFlagsAreRequests := related.cancelFlagsAreRequests
              cvWakes := related.cvWakes
              timers := related.timers
              timerCounter := related.timerCounter
              outputsEmpty := related.outputsEmpty
              threadsHaveSteps := related.threadsHaveSteps
              stepsHaveThreads := related.stepsHaveThreads
              activeStepsHaveDistinctThreads := related.activeStepsHaveDistinctThreads
              invocationsAreActive := related.invocationsAreActive
              blockingOrder := related.blockingOrder
              tasksAreCurrent := related.tasksAreCurrent }
          closed := fun run status hasRun isClosed =>
            let related := state.isRelated.closed run status hasRun isClosed
            { lateEmits := related.lateEmits
              tasksAreStale := related.tasksAreStale
              noInvocation := related.noInvocation
              noTimer := related.noTimer } }
  · cases isStep

/-- `begin(v)` is simulated by [FLOW-START]. -/
theorem l_start_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (input : Literal) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.request (.start input)) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.request (.start input)) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i isNotStarted
    split at isStep
    · rename_i canStamp
      injection isStep with isPair
      injection isPair with isEvents isNext
      subst isEvents
      subst isNext
      have hasNoRun : dexConfig.run = none := by
        have isStatus := state.isRelated.status
        cases hasRun : dexConfig.run with
        | none => rfl
        | some run => simp [isNotStarted, hasRun] at isStatus
      have hasNoTask : dexConfig.tasks = [] := state.isRelated.notStarted hasNoRun
      have canCommit := Related.canCommit_of_canStamp state.isRelated canStamp
      obtain ⟨noThread, noShared, noMutex, noQueue, noWake, noInput, noFlag, noJoinable,
        noTimer⟩ := state.lInvariant.notStartedIsInitial isNotStarted
      let startRecord : Dex.StepRecord signature :=
        { id := { stepType := StepType.start, number := 1 }
          input := some (DexVal.value input.toVal)
          options := { skipWaitFor := true }
          origin := .flowStart
          phase := .execute
          invocation := some { method := .execute, state := .pending }
          waitState := none
          conditionResults := none }
      let startedRun : Dex.Run signature :=
        { status := .running, outputs := [], steps := [startRecord]
          counters := [(StepType.start, 1)], attributes := [], channels := [], timers := []
          seq := 1 }
      refine ⟨[Dex.Obs.start StepType.start (DexVal.value input.toVal)],
        { dexConfig with run := some startedRun, lastCommitAt := some dexConfig.clock },
        ?_, rfl, ?_⟩
      · simp [simulatingActions, encodeRequest, TransitionSystem.exec, Dex.system, Dex.step,
          hasNoRun, canCommit,
          Dex.Config.commit, Dex.Config.closeEvents, Dex.Run.newStep, Dex.Run.empty,
          AssocList.get?, AssocList.set, translate, startStepDef, startedRun, startRecord]
      · refine
          { clock := state.isRelated.clock
            stamp := by simp [L.Config.stamp, L.Config.begun, state.isRelated.clock]
            status := by simp [L.Config.stamp, L.Config.begun, startedRun]
            notStarted := fun hasRun => by cases hasRun
            running := ?_
            closed := fun run status hasRun isClosed => by
              cases hasRun
              cases isClosed }
        intro run hasRun _
        cases hasRun
        exact
          { shared := fun name => by
              simp [readShared, L.Config.readShared, L.Config.stamp, L.Config.begun, noShared,
                AssocList.get?, startedRun]
            cvQueues := fun cv => by
              simp [readWaiters, L.Config.stamp, L.Config.begun, noQueue, AssocList.get?,
                startedRun]
            inputQueues := fun chan => by
              simp [Dex.Run.pendingMessages, L.Config.pendingInput, L.Config.stamp,
                L.Config.begun, noInput, AssocList.get?, startedRun]
            mutexTokens := fun areBooted => by
              have isBooted := (areBooted _ (List.mem_singleton.mpr rfl)).1
              simp at isBooted
            mutexesBeforeBoot := fun thread _ _ =>
              ⟨rfl, fun mutex => by
                simp [Dex.Run.pendingMessages, AssocList.get?, startedRun]⟩
            joinTokens := fun thread => by
              simp [Dex.Run.pendingMessages, L.Config.stamp, L.Config.begun, noJoinable,
                AssocList.get?, startedRun]
            cancelRequestsAreFlags := fun thread isPending => by
              simp [Dex.Run.pendingMessages, AssocList.get?, startedRun] at isPending
            cancelFlagsAreRequests := fun thread isFlagged => by
              simp [L.Config.stamp, L.Config.begun, noFlag] at isFlagged
            cvWakes := fun cv waiter => by
              simp [Dex.Run.pendingMessages, L.Config.stamp, L.Config.begun, noWake,
                AssocList.get?, startedRun]
            timers := fun timer fireTime => by
              simp [L.Config.stamp, L.Config.begun, AssocList.get?, startedRun]
            timerCounter := by
              simp [L.Config.stamp, L.Config.begun, noTimer, AssocList.get?, startedRun]
            outputsEmpty := rfl
            threadsHaveSteps := fun thread isMember _ => by
              simp only [L.Config.stamp, L.Config.begun, List.mem_singleton] at isMember
              subst isMember
              exact ⟨startRecord, List.mem_singleton.mpr rfl, rfl,
                ⟨rfl, input.toVal, rfl, rfl⟩, rfl, rfl⟩
            stepsHaveThreads := fun record isMember _ => by
              simp only [startedRun, List.mem_singleton] at isMember
              subst isMember
              exact ⟨_, List.mem_singleton.mpr rfl, by simp, ⟨rfl, input.toVal, rfl, rfl⟩⟩
            activeStepsHaveDistinctThreads := fun first isFirst second isSecond _ _ _ => by
              simp only [startedRun, List.mem_singleton] at isFirst isSecond
              rw [isFirst, isSecond]
            invocationsAreActive := fun record isMember _ => by
              simp only [startedRun, List.mem_singleton] at isMember
              subst isMember
              rfl
            blockingOrder := fun first second firstRecord secondRecord firstOn firstSince
                secondOn secondSince firstWait secondWait isFirst _ _ _ _ _ _ _ isBlocked => by
              simp only [L.Config.stamp, L.Config.begun, List.mem_singleton] at isFirst
              subst isFirst
              cases isBlocked
            tasksAreCurrent := fun task isMember => by
              simp [hasNoTask] at isMember }
    · cases isStep
  · cases isStep

/-- [PUBLISH] to a running Flow, as one explicit action. -/
theorem wp2_exec_publish (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (run : Dex.Run signature) (hasRun : config.run = some run) (isRunning : run.status = .running)
    (canCommit : config.canCommit = true) (chan : Chan) (message : DexVal)
    (newRun : Dex.Run signature)
    (isWake : (({ run with seq := run.seq + 1 } : Dex.Run signature).applyPublications
      [(chan, message)]).wake [chan] = newRun)
    (isStillRunning : newRun.status = .running) :
    (Dex.system flow).exec config [.request (.publish chan message)] =
      some ([Dex.Obs.pub chan message],
        { config with run := some newRun, lastCommitAt := some config.clock }) := by
  obtain ⟨status, outputs, steps, counters, attributes, channels, timers, seq⟩ := run
  simp only at isRunning
  subst isRunning
  subst isWake
  simp [TransitionSystem.exec, Dex.system, Dex.step, hasRun, canCommit, Dex.Config.commit,
    Dex.Config.closeEvents, isStillRunning]

/-- The named-sequence form of `in(c, v)` while the program runs, under the
hypothesis that a waiting Step has no invocation: one [PUBLISH], and the
result is related.

The hypothesis is not part of `IsSimulationState` at the base commit of this
package, and `l_input_simulated` is false without it. -/
theorem wp2_input_exec_running (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (waitingHasNoInvocation : ∀ run, dexConfig.run = some run → ∀ record, record ∈ run.steps →
      record.phase = .waiting → record.invocation = none)
    (chan : ExtChan) (value : Literal) (isRunningL : lConfig.status = .running)
    (canStamp : lConfig.canStamp = true) :
    ∃ dexNext,
      (Dex.system ⟦program⟧).exec dexConfig
        [.request (.publish (Chan.external chan) (DexVal.value value.toVal))] =
        some ([Dex.Obs.pub (Chan.external chan) (DexVal.value value.toVal)], dexNext) ∧
      Related program (lConfig.deliverInput chan value.toVal).stamp dexNext := by
  obtain ⟨run, hasRun, isRunning⟩ := Related.run_of_running state.isRelated isRunningL
  have related := state.isRelated.running run hasRun isRunning
  have invariant := state.dexInvariant.runInvariant run hasRun
  have canCommit := Related.canCommit_of_canStamp state.isRelated canStamp
  have noInvocation := waitingHasNoInvocation run hasRun
  -- What the wait of a waiting Step looks like.
  have waitOfWaiting : ∀ record, record ∈ run.steps → ∀ waitState, record.phase = .waiting →
      record.waitState = some waitState →
      ∃ thread on since, thread ∈ lConfig.threads ∧ thread.phase = .blocked on since ∧
        StepCarriesThread program thread record ∧
        waitState.wait.kind = (expectedWaitShape thread.core.tid on).1 ∧
        waitState.wait.timerSeconds.length = (expectedWaitShape thread.core.tid on).2.1 ∧
        waitState.wait.channels = (expectedWaitShape thread.core.tid on).2.2 ∧
        waitState.timersDone = waitState.wait.timerSeconds.map (fun _ => false) := by
    intro record isRecord waitState isWaiting hasWait
    obtain ⟨thread, on, since, isMember, isBlocked, carries, isMatch⟩ :=
      RunningRelated.blockedThread_of_waiting (state.isRelated.running _ hasRun isRunning) record isRecord isWaiting
    unfold PhaseMatchesStep at isMatch
    simp only [isBlocked] at isMatch
    obtain ⟨_, _, found, hasFound, isKind, hasLength, hasChannels, hasTimersDone⟩ := isMatch
    obtain rfl : found = waitState := Option.some.inj (hasFound.symm.trans hasWait)
    exact ⟨thread, on, since, isMember, isBlocked, carries, isKind, hasLength, hasChannels,
      hasTimersDone⟩
  -- From the relation of the runs to the relation of the configurations.
  have finish : ∀ (lNext : L.Config) (newRun : Dex.Run signature),
      lNext.clock = lConfig.clock → lNext.status = .running →
      (({ run with seq := run.seq + 1 } : Dex.Run signature).applyPublications
        [(Chan.external chan, DexVal.value value.toVal)]).wake [Chan.external chan] = newRun →
      newRun.status = .running →
      RunningRelated program lNext
        { dexConfig with run := some newRun, lastCommitAt := some dexConfig.clock } newRun →
      ∃ dexNext,
        (Dex.system ⟦program⟧).exec dexConfig
          [.request (.publish (Chan.external chan) (DexVal.value value.toVal))] =
          some ([Dex.Obs.pub (Chan.external chan) (DexVal.value value.toVal)], dexNext) ∧
        Related program lNext.stamp dexNext := by
    intro lNext newRun sameClock isRunningNext isWake isStillRunning newRelated
    refine ⟨_, wp2_exec_publish ⟦program⟧ dexConfig run hasRun isRunning canCommit _ _ newRun
      isWake isStillRunning, ?_⟩
    exact
      { clock := sameClock.trans state.isRelated.clock
        stamp := congrArg some (sameClock.trans state.isRelated.clock)
        status := by
          simp only [L.Config.stamp, isRunningNext]
          exact isStillRunning
        notStarted := fun hasNoRun => by simp at hasNoRun
        running := fun otherRun hasOtherRun _ => by
          obtain rfl : newRun = otherRun := Option.some.inj hasOtherRun
          exact wp2_runningRelated_stamp newRelated
        closed := fun otherRun status hasOtherRun isClosed => by
          obtain rfl : newRun = otherRun := Option.some.inj hasOtherRun
          rw [isStillRunning] at isClosed
          cases isClosed }
  rw [wp2_deliverInput_eq]
  by_cases hasReceiver : ∃ thread on since, thread ∈ lConfig.threads ∧
      thread.phase = .blocked on since ∧ wp2_isReceiveOn chan on = true
  · -- The earliest receiver takes the message.
    obtain ⟨anyThread, anyOn, anySince, isAny, isAnyBlocked, isAnyWanted⟩ := hasReceiver
    obtain ⟨thread, on, since, isMember, isBlocked, isWanted, isEarliest⟩ :=
      exists_earliestBlocked lConfig (wp2_isReceiveOn chan) anyThread isAny anyOn anySince
        isAnyBlocked isAnyWanted
    rw [earliestBlocked_eq_some lConfig state.lInvariant (wp2_isReceiveOn chan) thread
      isMember on since isBlocked isWanted isEarliest]
    have isFound := findThread?_of_mem state.lInvariant isMember
    obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
      related.threadsHaveSteps thread isMember (by simp [isBlocked])
    unfold PhaseMatchesStep at isMatch
    simp only [isBlocked] at isMatch
    obtain ⟨isWaiting, hasNoInvocation, waitState, hasWait, isKind, hasLength, hasChannels,
      hasTimersDone⟩ := isMatch
    obtain ⟨isAnyKind, isShape⟩ := wp2_shape_of_isReceiveOn thread.core.tid chan on isWanted
    rw [isAnyKind] at isKind
    rw [isShape] at hasChannels
    have wasEmpty : run.pendingMessages (Chan.external chan) = [] :=
      invariant.waitersQuiet record waitState ⟨isRecord, isWaiting, hasWait⟩ _
        (by rw [hasChannels]; simp)
    -- The Step of the earliest receiver has the least `sws` among the waiters.
    have isEarliestStep : ∀ other, other ∈ run.steps → ∀ otherWait, other.phase = .waiting →
        other.waitState = some otherWait → Chan.external chan ∈ otherWait.wait.channels →
        other = record ∨ waitState.registeredSeq < otherWait.registeredSeq := by
      intro other isOther otherWait isOtherWaiting hasOtherWait isNamed
      obtain ⟨owner, ownerOn, ownerSince, isOwner, isOwnerBlocked, ownerCarries, _, _,
        hasOwnerChannels, _⟩ := waitOfWaiting other isOther otherWait isOtherWaiting hasOtherWait
      rw [hasOwnerChannels] at isNamed
      have isOwnerWanted := wp2_isReceiveOn_of_mem owner.core.tid chan ownerOn isNamed
      have otherActive : other.phase.isActive = true := by rw [isOtherWaiting]; rfl
      by_cases isThread : owner = thread
      · subst isThread
        exact Or.inl (related.activeStepsHaveDistinctThreads other isOther record isRecord
          otherActive isActive
          ((threadOfInput_of_mem state.lInvariant isOwner ownerCarries).trans
            (threadOfInput_of_mem state.lInvariant isOwner carries).symm))
      · have isAtMost := isEarliest owner isOwner ownerOn ownerSince isOwnerBlocked isOwnerWanted
        have isNotSame : since ≠ ownerSince := by
          intro sameSince
          subst sameSince
          exact isThread (state.lInvariant.sinceInjective owner isOwner thread isMember ownerOn
            on since isOwnerBlocked isBlocked)
        exact Or.inr ((related.blockingOrder thread owner record other on since ownerOn
          ownerSince waitState otherWait isMember isOwner isRecord isOther isActive otherActive
          carries ownerCarries isBlocked isOwnerBlocked hasWait hasOtherWait).mp (by omega))
    have isWake := wp2_publish_wake_received run invariant record isRecord isWaiting waitState
      hasWait isKind (Chan.external chan) [Chan.cancelRequest thread.core.tid] hasChannels
      (DexVal.value value.toVal) isEarliestStep
    refine finish (lConfig.setPhase thread.core.tid (.woken (.received value.toVal))) _ rfl
      isRunningL isWake isRunning ?_
    let image : Dex.StepRecord signature → Dex.StepRecord signature := fun candidate =>
      if candidate.id = record.id then
        { candidate with
          conditionResults := some
            { timersDone := waitState.timersDone
              channelMessages := some (DexVal.value value.toVal) ::
                [Chan.cancelRequest thread.core.tid].map (fun _ => none) }
          phase := .execute
          invocation := some { method := .execute, state := .pending } }
      else candidate
    refine wp2_runningRelated_changeThread related state.lInvariant invariant.idsNodup isFound
      isRecord isActive carries (.woken (.received value.toVal)) (by rw [isBlocked]; rfl)
      (by simp) (by simp) (by simp [isBlocked]) image rfl ?_ ?_ rfl
      (wp2_receivedRun_pending run record waitState _ _ _ wasEmpty) ?_ rfl rfl
      (fun task isTask _ => isTask) ?_ ?_
    · intro candidate isCandidate
      by_cases isChanged : candidate.id = record.id
      · have sameRecord : candidate = record :=
          eq_of_nodup_map (fun step => step.id) run.steps invariant.idsNodup isCandidate isRecord
            isChanged
        subst sameRecord
        refine ⟨by simp [image], by simp [image], by simp [image], ?_⟩
        simp only [image, ↓reduceIte]
        rw [isActive]
        rfl
      · simp [image, isChanged]
    · intro candidate isOther
      simp [image, isOther]
    · apply wp2_timers_of_wake related state.lInvariant isFound carries isBlocked
      intro timer fireTime
      by_cases isRecordTimer : timer.step = record.id
      · have isDisarmed := AssocList.get?_filter_of_rejected
          (fun (key : Dex.TimerId signature) => !decide (key.step = record.id)) run.timers timer
          (by simp [isRecordTimer])
        constructor
        · intro isArmed
          have isNone : (none : Option Nat) = some fireTime := isDisarmed.symm.trans isArmed
          cases isNone
        · intro isArmed
          exact absurd isRecordTimer isArmed.2
      · have isKept := AssocList.get?_filter_of_kept
          (fun (key : Dex.TimerId signature) => !decide (key.step = record.id)) run.timers timer
          (by simp [isRecordTimer])
        constructor
        · intro isArmed
          exact ⟨isKept.symm.trans isArmed, isRecordTimer⟩
        · intro isArmed
          exact isKept.trans isArmed.1
    · -- The woken thread matches its Step: the wait was decided by the message.
      have isImage : image record =
          { record with
            conditionResults := some
              { timersDone := waitState.timersDone
                channelMessages := [some (DexVal.value value.toVal), none] }
            phase := .execute
            invocation := some { method := .execute, state := .pending } } := by
        simp [image]
      rw [isImage]
      unfold PhaseMatchesStep
      refine ⟨rfl, rfl, ?_⟩
      unfold decodeOutcome
      cases on with
      | recv wanted =>
        have fitsCode := state.lInvariant.phasesFitCode thread isMember
        unfold L.Thread.PhaseFitsCode at fitsCode
        simp only [isBlocked, BlockedOn.toWaitRequest?] at fitsCode
        obtain ⟨hasNoTimer, hasRequest⟩ := fitsCode
        have isReceiveCode : ∃ target receiveChan rest,
            thread.code = .source (.recv target receiveChan) :: rest := by
          cases codeCase : thread.code with
          | nil => simp [codeCase, waitRequest] at hasRequest
          | cons head rest =>
            cases head with
            | park cv => simp [codeCase, waitRequest] at hasRequest
            | relock mutex => simp [codeCase, waitRequest] at hasRequest
            | source statement =>
              cases statement <;> simp [codeCase, waitRequest] at hasRequest
              case recv target receiveChan => exact ⟨target, receiveChan, rest, rfl⟩
              all_goals (split at hasRequest <;> simp at hasRequest)
        obtain ⟨target, receiveChan, rest, isCode⟩ := isReceiveCode
        simp [hasNoTimer, isCode, decodeBlockedOutcome, decodeMessage]
      | timed number deadline optionalChan =>
        obtain ⟨⟨seconds, hasTimer⟩, hasTimedChan⟩ :=
          wp2_timer_of_blocked_timed state.lInvariant isMember isBlocked
        cases optionalChan with
        | none => simp [wp2_isReceiveOn] at isWanted
        | some wanted =>
          have hasOneTimer : waitState.wait.timerSeconds.length = 1 := hasLength
          obtain ⟨timerSeconds, isOneTimer⟩ := List.length_eq_one_iff.mp hasOneTimer
          have isNotDone : waitState.timersDone = [false] := by
            rw [hasTimersDone, isOneTimer]
            rfl
          simp [hasTimer, decodeTimedOutcome, isNotDone, hasTimedChan, decodeMessage]
      | lock mutex => simp [wp2_isReceiveOn] at isWanted
      | join joined => simp [wp2_isReceiveOn] at isWanted
      | park cv => simp [wp2_isReceiveOn] at isWanted
    · intro task isTask
      have wasCurrent := related.tasksAreCurrent task isTask
      simp only [Dex.Config.isTaskCurrent, hasRun] at wasCurrent
      by_cases sameStep : task.step = record.id
      · rw [sameStep, Dex.Run.findStep?_of_mem_nodup run record isRecord invariant.idsNodup]
          at wasCurrent
        simp [hasNoInvocation] at wasCurrent
      · have sameFind : (wp2_receivedRun run record waitState (Chan.external chan)
            [Chan.cancelRequest thread.core.tid] (DexVal.value value.toVal)).findStep? task.step =
            run.findStep? task.step :=
          Dex.Run.findStep?_updateStep_other _ _ _ _ (fun _ => rfl) sameStep
        simp only [Dex.Config.isTaskCurrent, sameFind]
        exact wasCurrent
  · -- No thread receives on the channel: the message is queued.
    have noneBlocked : ∀ thread, thread ∈ lConfig.threads → ∀ on since,
        thread.phase = .blocked on since → wp2_isReceiveOn chan on = false := by
      intro thread isMember on since isBlocked
      cases wantedCase : wp2_isReceiveOn chan on with
      | false => rfl
      | true => exact absurd ⟨thread, on, since, isMember, isBlocked, wantedCase⟩ hasReceiver
    rw [earliestBlocked_eq_none lConfig (wp2_isReceiveOn chan) noneBlocked]
    have isWake : (({ run with seq := run.seq + 1 } : Dex.Run signature).applyPublications
        [(Chan.external chan, DexVal.value value.toVal)]).wake [Chan.external chan] =
        ({ run with seq := run.seq + 1 } : Dex.Run signature).setPendingMessages
          (Chan.external chan)
          (run.pendingMessages (Chan.external chan) ++ [DexVal.value value.toVal]) := by
      have isPublished : ({ run with seq := run.seq + 1 } : Dex.Run signature).applyPublications
          [(Chan.external chan, DexVal.value value.toVal)] =
          ({ run with seq := run.seq + 1 } : Dex.Run signature).setPendingMessages
            (Chan.external chan)
            (run.pendingMessages (Chan.external chan) ++ [DexVal.value value.toVal]) := rfl
      rw [isPublished]
      apply wp2_wake_of_no_waiter
      intro record isRecord waitState isWaiting hasWait named isNamed isChanged
      have isChan : named = Chan.external chan := by simpa using isChanged
      subst isChan
      obtain ⟨owner, ownerOn, ownerSince, isOwner, isOwnerBlocked, _, _, _, hasOwnerChannels, _⟩ :=
        waitOfWaiting record isRecord waitState isWaiting hasWait
      rw [hasOwnerChannels] at isNamed
      have isOwnerWanted := wp2_isReceiveOn_of_mem owner.core.tid chan ownerOn isNamed
      rw [noneBlocked owner isOwner ownerOn ownerSince isOwnerBlocked] at isOwnerWanted
      cases isOwnerWanted
    refine finish _ _ rfl isRunningL isWake isRunning ?_
    refine wp2_runningRelated_queueInput related chan value.toVal rfl rfl rfl rfl rfl ?_ ?_ rfl ?_
    · simp [Dex.Run.pendingMessages, Dex.Run.setPendingMessages, AssocList.get?_set_self]
    · intro other isOther
      simp [Dex.Run.pendingMessages, Dex.Run.setPendingMessages,
        AssocList.get?_set_other _ _ _ _ isOther]
    · intro task isTask
      have wasCurrent := related.tasksAreCurrent task isTask
      simp only [Dex.Config.isTaskCurrent, hasRun] at wasCurrent
      simp only [Dex.Config.isTaskCurrent]
      exact wasCurrent

/-- `refused(c, v)` after the close: a rejected publication, which changes nothing. -/
theorem wp2_input_exec_closed (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (chan : ExtChan) (value : Literal) (isClosed : lConfig.status = .closed) :
    (Dex.system ⟦program⟧).exec dexConfig
      [.request (.publish (Chan.external chan) (DexVal.value value.toVal))] =
      some ([Dex.Obs.rejectedPub (Chan.external chan) (DexVal.value value.toVal)], dexConfig) := by
  obtain ⟨run, status, hasRun, isClosedRun⟩ := Related.run_of_closed state.isRelated isClosed
  simp [TransitionSystem.exec, Dex.system, Dex.step, hasRun, isClosedRun]

/-- `l_input_simulated` under the hypothesis that a waiting Step has no invocation. -/
theorem wp2_input_simulated_of_waitingHasNoInvocation (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (waitingHasNoInvocation : ∀ run, dexConfig.run = some run → ∀ record, record ∈ run.steps →
      record.phase = .waiting → record.invocation = none)
    (chan : ExtChan) (value : Literal) (lEvents : List Event) (lNext : L.Config)
    (isStep :
      (L.system program).step lConfig (.request (.input chan value)) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.request (.input chan value)) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  cases statusCase : lConfig.status with
  | notStarted => simp [statusCase] at isStep
  | closed =>
    simp only [statusCase, Option.some.injEq, Prod.mk.injEq] at isStep
    obtain ⟨rfl, rfl⟩ := isStep
    exact ⟨_, _, wp2_input_exec_closed program lConfig dexConfig state chan value statusCase,
      rfl, state.isRelated⟩
  | running =>
    simp only [statusCase] at isStep
    by_cases canStamp : lConfig.canStamp = true
    · simp only [canStamp, ↓reduceIte, Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨rfl, rfl⟩ := isStep
      obtain ⟨dexNext, isExec, isRelated⟩ := wp2_input_exec_running program lConfig dexConfig
        state waitingHasNoInvocation chan value statusCase canStamp
      exact ⟨_, dexNext, isExec, rfl, isRelated⟩
    · simp [canStamp] at isStep

/-- `in(c, v)` and `refused(c, v)` are simulated by [PUBLISH] and its rejection. -/
theorem l_input_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (chan : ExtChan) (value : Literal) (lEvents : List Event) (lNext : L.Config)
    (isStep :
      (L.system program).step lConfig (.request (.input chan value)) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.request (.input chan value)) lEvents lNext :=
  wp2_input_simulated_of_waitingHasNoInvocation program lConfig dexConfig state
    state.waitingHasNoInvocation chan value lEvents lNext isStep

/-- `l_fire_simulated` under the hypothesis that the keys of `run.timers` are distinct. -/
theorem wp2_fire_simulated_of_timerKeysNodup (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (timerKeysNodup : ∀ run, dexConfig.run = some run →
      (run.timers.map (fun entry => entry.1)).Nodup)
    (count : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.fire count) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.fire count) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  by_cases isEnabled : (decide (0 < count) && lConfig.canStamp) = true
  · simp only [isEnabled, ↓reduceIte] at isStep
    cases firedCase : lConfig.fireTimers count with
    | none => simp [firedCase] at isStep
    | some lFinal =>
      simp only [firedCase, Option.map_some, Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨rfl, rfl⟩ := isStep
      simp only [Bool.and_eq_true, decide_eq_true_eq] at isEnabled
      obtain ⟨dexNext, isExec, isRelated⟩ := wp2_fire_exec program lConfig dexConfig state
        timerKeysNodup count lFinal isEnabled.1 isEnabled.2 firedCase
      exact ⟨_, dexNext, isExec, rfl, isRelated⟩
  · simp [isEnabled] at isStep

/-- A batch of `fire` actions is simulated by one [TIMER-FIRE] of the same
number of due timers: both sides order the due timers by `(deadline, timer
number)` (spec §8.1, §8.2). -/
theorem l_fire_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (count : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.fire count) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.fire count) lEvents lNext :=
  wp2_fire_simulated_of_timerKeysNodup program lConfig dexConfig state state.timerKeysNodup
    count lEvents lNext isStep

/-- The named-sequence form of the delivery of a late emit, under the
hypothesis that every task holds at most one effect: a task that holds the
effect runs `wp2_deliverActions`, and the result is related.

`singleEffects` is not part of `IsSimulationState` at the base commit of
this package, and `l_lateDeliver_simulated` is false without it. -/
theorem wp2_lateDeliver_exec (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (index : Nat) (effect : Val) (isClosed : lConfig.status = .closed)
    (isLate : lConfig.lateEmits[index]? = some effect)
    (task : Dex.Task signature) (isTask : task ∈ dexConfig.tasks)
    (holdsEffect : effect ∈ pendingEffects ⟦program⟧ task) :
    (Dex.system ⟦program⟧).exec dexConfig (wp2_deliverActions task) =
      some ([Dex.Obs.fx task.step task.method effect],
        dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) ∧
    Related program { lConfig with lateEmits := removeAt lConfig.lateEmits index }
      (dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) := by
  obtain ⟨run, status, hasRun, isClosedRun⟩ := Related.run_of_closed state.isRelated isClosed
  have closedRelated := state.isRelated.closed run status hasRun isClosedRun
  obtain ⟨before, after, hasEffect, isSplit, beforeOther, afterOther, isPerm⟩ :=
    wp2_late_task state singleEffects hasRun isClosedRun index effect isLate task isTask
      holdsEffect
  refine ⟨wp2_exec_deliver program dexConfig state.tasksDistinct task isTask
      (state.dexInvariant.tasksComputed task isTask) effect hasEffect, ?_⟩
  have hasTasks := wp2_updateTask_of_split dexConfig task
    (fun _ => wp2_deliveredTask program task) before after isSplit beforeOther afterOther
  exact
    { clock := state.isRelated.clock
      stamp := state.isRelated.stamp
      status := state.isRelated.status
      notStarted := fun hasNoRun => by
        have isAbsent : some run = none := hasRun.symm.trans hasNoRun
        cases isAbsent
      running := fun newRun hasNewRun isRunning => by
        obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
        rw [isClosedRun] at isRunning
        cases isRunning
      closed := fun newRun newStatus hasNewRun _ => by
        obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
        exact
          { lateEmits := by
              rw [hasTasks]
              simpa [List.flatMap_append, wp2_deliveredTask, pendingEffects] using isPerm
            tasksAreStale := fun other isOther => by
              obtain ⟨original, isOriginal, isChanged⟩ := mem_updateTask _ _ _ _ isOther
              have wasStale := closedRelated.tasksAreStale original isOriginal
              by_cases sameId : original.id = task.id
              · have isTaskOriginal : original = task :=
                  eq_of_nodup_map (fun current => current.id) dexConfig.tasks
                    state.tasksDistinct.1 isOriginal isTask sameId
                rw [isTaskOriginal] at wasStale
                simp only [sameId, ↓reduceIte] at isChanged
                rw [isChanged]
                exact wasStale
              · simp only [sameId, ↓reduceIte] at isChanged
                rw [isChanged]
                exact wasStale
            noInvocation := closedRelated.noInvocation
            noTimer := closedRelated.noTimer } }

/-- `l_lateDeliver_simulated` under the hypothesis that every task holds at
most one effect. -/
theorem wp2_lateDeliver_simulated_of_singleEffects (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDeliver index) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.lateDeliver index) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i effect isClosed isLate
    injection isStep with isPair
    injection isPair with isEvents isNext
    subst isEvents
    subst isNext
    obtain ⟨_, _, hasRun, isClosedRun⟩ := Related.run_of_closed state.isRelated isClosed
    obtain ⟨task, isPicked, isTask, holdsEffect⟩ :=
      (state.isRelated.closed _ _ hasRun isClosedRun).exists_lateTask isLate
    obtain ⟨isExec, isRelated⟩ := wp2_lateDeliver_exec program lConfig dexConfig state
      singleEffects index effect isClosed isLate task isTask holdsEffect
    refine ⟨[Dex.Obs.fx task.step task.method effect], _, ?_, rfl, isRelated⟩
    rw [simulatingActions_lateDeliver isLate isPicked, ← wp2_deliverActions_eq]
    exact isExec
  · cases isStep

/-- The named-sequence form of the drop of a late emit, under the hypothesis
that every task holds at most one effect: [WORKER-ABANDON] of a task that
holds the effect, and the result is related.

`singleEffects` is not part of `IsSimulationState` at the base commit of
this package, and `l_lateDrop_simulated` is false without it. -/
theorem wp2_lateDrop_exec (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (index : Nat) (effect : Val) (isClosed : lConfig.status = .closed)
    (isLate : lConfig.lateEmits[index]? = some effect)
    (task : Dex.Task signature) (isTask : task ∈ dexConfig.tasks)
    (holdsEffect : effect ∈ pendingEffects ⟦program⟧ task) :
    (Dex.system ⟦program⟧).exec dexConfig [.workerAbandon task.id] =
      some ([], dexConfig.removeTask task.id) ∧
    Related program { lConfig with lateEmits := removeAt lConfig.lateEmits index }
      (dexConfig.removeTask task.id) := by
  obtain ⟨run, status, hasRun, isClosedRun⟩ := Related.run_of_closed state.isRelated isClosed
  have closedRelated := state.isRelated.closed run status hasRun isClosedRun
  obtain ⟨before, after, _, isSplit, beforeOther, afterOther, isPerm⟩ :=
    wp2_late_task state singleEffects hasRun isClosedRun index effect isLate task isTask
      holdsEffect
  have isFound := findTask?_of_mem state.tasksDistinct isTask
  have isStale := closedRelated.tasksAreStale task isTask
  refine ⟨?_, ?_⟩
  · simp [TransitionSystem.exec, Dex.system, Dex.step, isFound, isStale]
  · have hasTasks := wp2_removeTask_of_split dexConfig task before after isSplit beforeOther
      afterOther
    exact
      { clock := state.isRelated.clock
        stamp := state.isRelated.stamp
        status := state.isRelated.status
        notStarted := fun hasNoRun => by
          have isAbsent : some run = none := hasRun.symm.trans hasNoRun
          cases isAbsent
        running := fun newRun hasNewRun isRunning => by
          obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
          rw [isClosedRun] at isRunning
          cases isRunning
        closed := fun newRun newStatus hasNewRun _ => by
          obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
          exact
            { lateEmits := by
                rw [hasTasks]
                exact isPerm
              tasksAreStale := fun other isOther =>
                closedRelated.tasksAreStale other (List.mem_filter.mp isOther).1
              noInvocation := closedRelated.noInvocation
              noTimer := closedRelated.noTimer } }

/-- `l_lateDrop_simulated` under the hypothesis that every task holds at most
one effect. -/
theorem wp2_lateDrop_simulated_of_singleEffects (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDrop index) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.lateDrop index) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i effect isClosed isLate
    injection isStep with isPair
    injection isPair with isEvents isNext
    subst isEvents
    subst isNext
    obtain ⟨_, _, hasRun, isClosedRun⟩ := Related.run_of_closed state.isRelated isClosed
    obtain ⟨task, isPicked, isTask, holdsEffect⟩ :=
      (state.isRelated.closed _ _ hasRun isClosedRun).exists_lateTask isLate
    obtain ⟨isExec, isRelated⟩ := wp2_lateDrop_exec program lConfig dexConfig state
      singleEffects index effect isClosed isLate task isTask holdsEffect
    refine ⟨[], _, ?_, rfl, isRelated⟩
    rw [simulatingActions_lateDrop isLate isPicked]
    exact isExec
  · cases isStep

/-- The delivery of a late emit is simulated by the [WORKER-EFFECT] of a
task of a canceled Step. -/
theorem l_lateDeliver_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDeliver index) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.lateDeliver index) lEvents lNext :=
  wp2_lateDeliver_simulated_of_singleEffects program lConfig dexConfig state
    state.holdsAtMostOneEffect index lEvents lNext isStep

/-- The drop of a late emit is simulated by [WORKER-ABANDON]. -/
theorem l_lateDrop_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDrop index) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.lateDrop index) lEvents lNext :=
  wp2_lateDrop_simulated_of_singleEffects program lConfig dexConfig state
    state.holdsAtMostOneEffect index lEvents lNext isStep

/-- The begin of an action is simulated by [STEP-START] of the Step that
carries the thread: the start action of `main`, the begin of a statement or
of a wait, and the begin of the continuation after a wait was decided. A
shared read happens here on both sides (spec §8.2: "a read linearizes at
[STEP-START]"). -/
theorem l_thread_begin_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (isPending : thread.phase = .bootPending ∨ thread.phase = .ready ∨
      ∃ outcome, thread.phase = .woken outcome)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  obtain ⟨_, run, hasRun, isRunning⟩ := IsSimulationState.run_of_thread state thread isMember
  have related := state.isRelated.running run hasRun isRunning
  have idsNodup := (state.dexInvariant.runInvariant run hasRun).idsNodup
  have fitsCode := state.lInvariant.phasesFitCode thread isMember
  have wasNotBlocked : ∀ on since, thread.phase ≠ .blocked on since := by
    intro on since
    rcases isPending with phaseCase | phaseCase | ⟨outcome, phaseCase⟩ <;> simp [phaseCase]
  obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
    related.threadsHaveSteps thread isMember (by
      rcases isPending with phaseCase | phaseCase | ⟨outcome, phaseCase⟩ <;> simp [phaseCase])
  have isFoundRecord : run.findStep? record.id = some record :=
    Dex.Run.findStep?_of_mem_nodup run record isRecord idsNodup
  have isActions : simulatingActions program lConfig dexConfig (.thread tid) =
      [.stepStart record.id] :=
    simulatingActions_begin related hasRun isFound record isRecord isActive
      ((threadOfInput_of_mem state.lInvariant isMember carries).trans hasTid) isPending
  -- [STEP-START] of the Step that carries the thread simulates the begin.
  have finish : ∀ (method : Dex.Method) (newPhase : Phase),
      record.invocation = some { method := method, state := .pending } →
      lConfig.canStamp = true →
      isBootPhase newPhase = isBootPhase thread.phase →
      (∀ on since, newPhase ≠ .blocked on since) → newPhase ≠ .halted →
      (thread.phase = .woken .canceled ∨ thread.phase = .resuming .canceled →
        newPhase = .woken .canceled ∨ newPhase = .resuming .canceled) →
      PhaseMatchesStep program lConfig (wp2_startedConfig dexConfig run record method)
        { thread with phase := newPhase } (wp2_startedRecord dexConfig record method) →
      IsSimulatedByActions program lConfig dexConfig (.thread tid) []
        (lConfig.setPhase tid newPhase).stamp := by
    intro method newPhase isPendingInvocation canStamp sameBoot isNotBlocked isNotHalted
      keepsCanceled newMatch
    exact ⟨[], _,
      isActions ▸ wp2_exec_stepStart ⟦program⟧ dexConfig run hasRun isRunning record
        isFoundRecord method isPendingInvocation
        (Related.canCommit_of_canStamp state.isRelated canStamp),
      rfl,
      wp2_stepStart_related state isFound hasRun isRunning isRecord isActive carries method
        isPendingInvocation newPhase sameBoot wasNotBlocked isNotBlocked isNotHalted
        keepsCanceled newMatch⟩
  simp only [L.system, L.step, isFound, stepThread, hasTid] at isStep
  unfold PhaseMatchesStep at isMatch
  rw [stepCarriesThread_iff] at carries
  rcases isPending with phaseCase | phaseCase | ⟨outcome, phaseCase⟩
  · -- The start action of `main` begins.
    simp only [phaseCase] at isStep isMatch
    simp only [phaseCase, isBootPhase, ↓reduceIte] at carries
    obtain ⟨isStart, _⟩ := carries
    by_cases canStamp : lConfig.canStamp = true
    · simp only [canStamp, ↓reduceIte, Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨rfl, rfl⟩ := isStep
      refine finish .execute .bootRunning isMatch.2 canStamp (by rw [phaseCase]; rfl) (by simp)
        (by simp) (by simp [phaseCase]) ?_
      unfold PhaseMatchesStep
      refine ⟨isMatch.1, wp2_hasRunningTask_started _ _ _ _ _ ?_⟩
      rw [wp2_pendingEffects_started_execute, isStart]
      exact wp2_startStep_effects program _ _ _ _
    · simp [canStamp] at isStep
  · -- A statement, or the wait of a blocking statement, begins.
    simp only [phaseCase] at isStep isMatch
    simp only [phaseCase, isBootPhase] at carries
    obtain ⟨isPhase, noResults, isPendingInvocation⟩ := isMatch
    by_cases waitsFirst : (thread.timer.isSome || isBlockingHead thread.code) = true
    · simp only [waitsFirst, ↓reduceIte] at isStep isPendingInvocation isPhase
      by_cases canStamp : lConfig.canStamp = true
      · simp only [canStamp, ↓reduceIte, Option.some.injEq, Prod.mk.injEq] at isStep
        obtain ⟨rfl, rfl⟩ := isStep
        refine finish .waitFor .waitStarted isPendingInvocation canStamp (by rw [phaseCase]; rfl)
          (by simp) (by simp) (by simp [phaseCase]) ?_
        -- The WaitFor task holds no effect; the relation may or may not ask for it.
        have hasTask : HasRunningTask (wp2_startedConfig dexConfig run record .waitFor)
            (wp2_startedRecord dexConfig record .waitFor) .waitFor
            (fun task => pendingEffects ⟦program⟧ task = []) :=
          wp2_hasRunningTask_started _ _ _ _ _
            (wp2_compute_waitFor_effects program _ rfl)
        unfold PhaseMatchesStep
        exact ⟨isPhase, hasTask⟩
      · simp [canStamp] at isStep
    · simp only [waitsFirst] at isStep isPendingInvocation isPhase
      have isNotTimed : thread.timer = none := by
        cases timerCase : thread.timer with
        | none => rfl
        | some timer => simp [timerCase] at waitsFirst
      have isNotBlocking : isBlockingHead thread.code = false := by
        cases blockingCase : isBlockingHead thread.code with
        | false => rfl
        | true => simp [blockingCase] at waitsFirst
      simp only [isNotTimed, Bool.false_eq_true, ↓reduceIte] at carries
      obtain ⟨isPoint, hasInput, hasSelection⟩ := carries
      -- The effects of the started Execute task.
      have hasEffects : pendingEffects ⟦program⟧ (wp2_startedTask dexConfig run record .execute) =
          match beginKind thread.core thread.code with
          | .emit effect => [effect]
          | _ => [] := by
        rw [wp2_pendingEffects_started_execute, isPoint, hasInput]
        exact wp2_pointStep_effects program thread.core thread.code isNotBlocking _ _ _
      have hasWaiters : ∀ cv, cvOperand thread.core thread.code = some cv →
          readWaiters (wp2_startedTask dexConfig run record .execute).snapshot cv =
            (lConfig.cvQueues.get? cv).getD [] := by
        intro cv isOperand
        exact (wp2_readWaiters_snapshot run thread.core thread.code record.options hasSelection
          cv isOperand).trans (related.cvQueues cv)
      by_cases holdsMutex : lConfig.holdsRequiredMutex thread = true
      · simp only [holdsMutex, ↓reduceIte] at isStep
        cases kindCase : beginKind thread.core thread.code with
        | plain =>
          simp only [kindCase] at isStep hasEffects
          by_cases canStamp : lConfig.canStamp = true
          · simp only [canStamp, ↓reduceIte] at isStep
            obtain ⟨rfl, rfl⟩ := isStep
            refine finish .execute (.running .null) isPendingInvocation canStamp
              (by rw [phaseCase]; rfl) (by simp) (by simp) (by simp [phaseCase]) ?_
            unfold PhaseMatchesStep
            refine ⟨isPhase,
              wp2_hasRunningTask_started _ _ _ _ _ ⟨hasEffects, noResults, ?_, hasWaiters⟩⟩
            simp only [kindCase]
          · simp [canStamp] at isStep
        | load source =>
          simp only [kindCase] at isStep hasEffects
          by_cases canStamp : lConfig.canStamp = true
          · simp only [canStamp, ↓reduceIte] at isStep
            obtain ⟨rfl, rfl⟩ := isStep
            refine finish .execute (.running (lConfig.readShared source)) isPendingInvocation
              canStamp (by rw [phaseCase]; rfl) (by simp) (by simp) (by simp [phaseCase]) ?_
            unfold PhaseMatchesStep
            refine ⟨isPhase,
              wp2_hasRunningTask_started _ _ _ _ _ ⟨hasEffects, noResults, ?_, hasWaiters⟩⟩
            simp only [kindCase]
            exact (readShared_snapshot run record.options .execute source).trans
              (related.shared source)
          · simp [canStamp] at isStep
        | emit effect =>
          simp only [kindCase] at isStep hasEffects
          by_cases canStamp : lConfig.canStamp = true
          · simp only [canStamp, ↓reduceIte] at isStep
            obtain ⟨rfl, rfl⟩ := isStep
            refine finish .execute (.emitting effect) isPendingInvocation canStamp
              (by rw [phaseCase]; rfl) (by simp) (by simp) (by simp [phaseCase]) ?_
            unfold PhaseMatchesStep
            exact ⟨isPhase, wp2_hasRunningTask_started _ _ _ _ _ ⟨hasEffects, noResults⟩⟩
          · simp [canStamp] at isStep
      · simp [holdsMutex] at isStep
  · -- The continuation after a decided wait begins.
    simp only [phaseCase] at isStep isMatch
    simp only [phaseCase, isBootPhase] at carries
    obtain ⟨isPhase, isPendingInvocation, isOutcome⟩ := isMatch
    by_cases canStamp : lConfig.canStamp = true
    · simp only [canStamp, ↓reduceIte, Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨rfl, rfl⟩ := isStep
      refine finish .execute (.resuming outcome) isPendingInvocation canStamp
        (by rw [phaseCase]; rfl) (by simp) (by simp) (by simp [phaseCase]) ?_
      have hasEffects :
          pendingEffects ⟦program⟧ (wp2_startedTask dexConfig run record .execute) = [] := by
        rw [wp2_pendingEffects_started_execute]
        cases timerCase : thread.timer with
        | some timer =>
          obtain ⟨number, seconds⟩ := timer
          simp only [timerCase, Bool.false_eq_true, ↓reduceIte] at carries
          rw [carries.1]
          exact wp2_timedStep_effects _ _ _ _
        | none =>
          simp only [timerCase, Bool.false_eq_true, ↓reduceIte] at carries
          have isBlocking : isBlockingHead thread.code = true := by
            unfold L.Thread.PhaseFitsCode at fitsCode
            simpa [phaseCase, timerCase] using fitsCode
          rw [carries.1]
          exact wp2_pointStep_effects_of_blocking program thread.code isBlocking _ _ _ _
      unfold PhaseMatchesStep
      exact ⟨isPhase, wp2_hasRunningTask_started _ _ _ _ _ ⟨hasEffects, isOutcome⟩⟩
    · simp [canStamp] at isStep

/-- The named-sequence form of `l_thread_deliver_simulated`: the task of the
emit Step runs `wp2_deliverActions` (its handler, if it has not run, then
[WORKER-EFFECT]), and the result is related. -/
theorem wp2_thread_deliver_exec (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (effect : Val) (isEmitting : thread.phase = .emitting effect) :
    ∃ task, task ∈ dexConfig.tasks ∧
      simulatingActions program lConfig dexConfig (.thread tid) = wp2_deliverActions task ∧
      (Dex.system ⟦program⟧).exec dexConfig (wp2_deliverActions task) =
        some ([Dex.Obs.fx task.step task.method effect],
          dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) ∧
      Related program (lConfig.setPhase tid .emitted)
        (dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  obtain ⟨_, run, hasRun, isRunning⟩ := IsSimulationState.run_of_thread state thread isMember
  have related := state.isRelated.running run hasRun isRunning
  have idsNodup := (state.dexInvariant.runInvariant run hasRun).idsNodup
  obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
    related.threadsHaveSteps thread isMember (by simp [isEmitting])
  unfold PhaseMatchesStep at isMatch
  simp only [isEmitting] at isMatch
  obtain ⟨isPhase, task, isTask, hasInvocation, isTaskStep, isMethod, sameInput, hasEffect,
    noResults⟩ := isMatch
  refine ⟨task, isTask,
    (simulatingActions_deliver related hasRun state.tasksDistinct isFound record isRecord
      isActive ((threadOfInput_of_mem state.lInvariant isMember carries).trans hasTid)
      .execute task isTask hasInvocation effect isEmitting).trans (wp2_deliverActions_eq task).symm,
    wp2_exec_deliver program dexConfig state.tasksDistinct task isTask
      (state.dexInvariant.tasksComputed task isTask) effect hasEffect, ?_⟩
  have sameTask : ∀ other, other ∈ dexConfig.tasks → other.id = task.id → other = task :=
    fun other isOther sameId =>
      eq_of_nodup_map (fun current => current.id) dexConfig.tasks state.tasksDistinct.1 isOther
        isTask sameId
  have frame : RunningRelated program (lConfig.setPhase tid .emitted)
      (dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) run := by
    refine wp2_runningRelated_setPhase related state.lInvariant idsNodup isFound isRecord
      isActive carries .emitted (by rw [isEmitting]; rfl) (by simp [isEmitting]) (by simp)
      (by simp) (by simp [isEmitting]) id (List.map_id _).symm
      (fun _ => ⟨rfl, rfl, rfl, rfl, rfl⟩) (fun _ _ => rfl) rfl rfl rfl rfl rfl ?_ ?_ ?_
    · intro other isOther otherStep
      refine List.mem_map.mpr ⟨other, isOther, ?_⟩
      have otherId : other.id ≠ task.id := by
        intro sameId
        rw [sameTask other isOther sameId] at otherStep
        exact otherStep isTaskStep
      simp [otherId]
    · unfold PhaseMatchesStep
      exact ⟨isPhase, wp2_deliveredTask program task, List.mem_map.mpr ⟨task, isTask, by simp⟩,
        hasInvocation, isTaskStep, isMethod, sameInput, rfl, noResults⟩
    · intro other isOther
      obtain ⟨original, isOriginal, isChanged⟩ := mem_updateTask _ _ _ _ isOther
      have wasCurrent := related.tasksAreCurrent original isOriginal
      by_cases sameId : original.id = task.id
      · rw [sameTask original isOriginal sameId] at wasCurrent
        simp only [sameId, ↓reduceIte] at isChanged
        rw [isChanged]
        exact wasCurrent
      · simp only [sameId, ↓reduceIte] at isChanged
        rw [isChanged]
        exact wasCurrent
  exact
    { clock := state.isRelated.clock
      stamp := state.isRelated.stamp
      status := state.isRelated.status
      notStarted := fun hasNoRun => by
        have isAbsent : some run = none := hasRun.symm.trans hasNoRun
        cases isAbsent
      running := fun newRun hasNewRun _ => by
        obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
        exact frame
      closed := fun newRun status hasNewRun isClosed => by
        obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
        rw [isRunning] at isClosed
        cases isClosed }

/-- `out(a)` is simulated by [WORKER-EFFECT] of the emit Step's task. -/
theorem l_thread_deliver_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (effect : Val) (isEmitting : thread.phase = .emitting effect)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  simp only [L.system, L.step, isFound, stepThread, isEmitting] at isStep
  injection isStep with isPair
  injection isPair with isEvents isNext
  subst isEvents
  subst isNext
  obtain ⟨task, _, isActions, isExec, isRelated⟩ :=
    wp2_thread_deliver_exec program lConfig dexConfig state tid thread isFound effect isEmitting
  rw [(mem_of_findThread? isFound).2]
  refine ⟨[Dex.Obs.fx task.step task.method effect], _, ?_, rfl, isRelated⟩
  rw [isActions]
  exact isExec

end DexExpressiveness
