package evaluate

import (
	"sort"

	p "github.com/superdurable/dex/server/internal/persistence"
)

type stepReservation struct {
	executeMethodExeID int64
	conditionResults   []p.ConditionResult
}

// hasMetSingleChannelCondition checks if a single channel condition can be
// satisfied given existing reservations from INVOKING_EXECUTE steps.
//
// extraReserved is the count already taken from this channel by earlier
// satisfied branches of the SAME greedy AnyOf evaluation, so two conditions on
// one channel cannot reserve the same messages twice.
func hasMetSingleChannelCondition(
	cond *p.ChannelCondition,
	activeSteps map[string]p.ActiveStepExecution,
	unconsumed map[string][]p.ChannelMessage,
	extraReserved int,
) (bool, int) {
	if cond == nil {
		return false, 0
	}
	reservations := getCurrentReservations(activeSteps)
	reserved := reservedCountOnChannel(reservations, cond.ChannelName)
	available := len(unconsumed[cond.ChannelName]) - reserved - extraReserved
	if available < int(cond.Min) {
		return false, 0
	}
	return true, reserveCountForCondition(cond, available)
}

// hasMetAllChannelConditions checks if all channel conditions can be satisfied
// given existing reservations. Returns per-condition take counts on success.
func hasMetAllChannelConditions(
	conds []*p.ChannelCondition,
	activeSteps map[string]p.ActiveStepExecution,
	unconsumed map[string][]p.ChannelMessage,
) (bool, []int) {
	if len(conds) == 0 {
		return true, []int{}
	}
	reservations := getCurrentReservations(activeSteps)

	type channelPlan struct {
		totalMin int32
		maxTake  int32
	}
	plans := map[string]*channelPlan{}
	for _, cond := range conds {
		if cond == nil {
			return false, nil
		}
		plan, ok := plans[cond.ChannelName]
		if !ok {
			plan = &channelPlan{}
			plans[cond.ChannelName] = plan
		}
		plan.totalMin += cond.Min
		if cond.Max > 0 {
			plan.maxTake += cond.Max
		} else {
			plan.maxTake += cond.Min
		}
	}

	counts := make([]int, len(conds))
	for channelName, plan := range plans {
		available := len(unconsumed[channelName]) - reservedCountOnChannel(reservations, channelName)
		if available < int(plan.totalMin) {
			return false, nil
		}
		takeCount := int(plan.maxTake)
		if takeCount > available {
			takeCount = available
		}
		if takeCount < int(plan.totalMin) {
			return false, nil
		}
		distributeChannelTakeCounts(conds, channelName, takeCount, counts)
	}
	return true, counts
}

func distributeChannelTakeCounts(conds []*p.ChannelCondition, channelName string, takeCount int, counts []int) {
	surplus := takeCount
	for index, cond := range conds {
		if cond == nil || cond.ChannelName != channelName {
			continue
		}
		counts[index] = int(cond.Min)
		surplus -= counts[index]
	}
	if surplus <= 0 {
		return
	}
	for index, cond := range conds {
		if cond == nil || cond.ChannelName != channelName {
			continue
		}
		if cond.Max <= 0 {
			continue
		}
		headroom := int(cond.Max) - counts[index]
		if headroom <= 0 {
			continue
		}
		add := surplus
		if add > headroom {
			add = headroom
		}
		counts[index] += add
		surplus -= add
		if surplus <= 0 {
			return
		}
	}
}

func reserveCountForCondition(cond *p.ChannelCondition, available int) int {
	takeCount := int(cond.Min)
	if max := cond.Max; max > 0 {
		if int(max) < available {
			takeCount = int(max)
		} else {
			takeCount = available
		}
	}
	if takeCount < int(cond.Min) {
		takeCount = int(cond.Min)
	}
	return takeCount
}

// channelReserveCounts returns per-channel reserved counts from ConditionResults.
func channelReserveCounts(results []p.ConditionResult) map[string]int32 {
	out := map[string]int32{}
	for _, cr := range results {
		if cr.Channel == nil || !cr.Channel.Satisfied || cr.Channel.ConsumedCount <= 0 {
			continue
		}
		out[cr.Channel.ChannelName] += cr.Channel.ConsumedCount
	}
	return out
}

// SpliceUnconsumed removes reserved message ranges for the given step IDs from
// unconsumed in-place, processing in descending executeMethodExeID order per
// channel to keep remaining offsets stable.
// return the channel names that have been spiced (so that caller can update DB)
func SpliceUnconsumed(
	removeStepExeIDs []string,
	activeSteps map[string]p.ActiveStepExecution,
	unconsumed map[string][]p.ChannelMessage,
) []string {
	reservations := getCurrentReservations(activeSteps)
	type reservation struct {
		stepExeID          string
		executeMethodExeID int64
		channel            string
		count              int32
	}
	var all []reservation
	seen := make(map[string]struct{}, len(removeStepExeIDs))
	for _, stepExeID := range removeStepExeIDs {
		if _, dup := seen[stepExeID]; dup {
			continue // dedup: a repeated id would splice its range twice (over-consume)
		}
		seen[stepExeID] = struct{}{}
		view, ok := reservations[stepExeID]
		if !ok {
			continue
		}
		for channelName, count := range channelReserveCounts(view.conditionResults) {
			all = append(all, reservation{
				stepExeID:          stepExeID,
				executeMethodExeID: view.executeMethodExeID,
				channel:            channelName,
				count:              count,
			})
		}
	}
	if len(all) == 0 {
		return nil
	}
	byChannel := map[string][]reservation{}
	for _, item := range all {
		byChannel[item.channel] = append(byChannel[item.channel], item)
	}
	var splicedChannels []string
	for channelName, channelReservations := range byChannel {
		// Descending executeMethodExeID: splice the highest-offset range first so
		// lower-offset ranges stay index-stable. reservationOffset must read the
		// full reservation table (it sums strictly-lower exeIDs), so we never
		// mutate `reservations` here — a step on multiple channels would otherwise
		// lose its offset contribution on the channels processed later.
		sort.Slice(channelReservations, func(i, j int) bool {
			return channelReservations[i].executeMethodExeID > channelReservations[j].executeMethodExeID
		})
		queue := unconsumed[channelName]
		spliced := false
		for _, item := range channelReservations {
			offset := reservationOffset(reservations, channelName, item.executeMethodExeID)
			count := int(item.count)
			if count <= 0 || offset >= len(queue) {
				continue
			}
			end := offset + count
			if end > len(queue) {
				end = len(queue)
			}
			queue = append(append([]p.ChannelMessage{}, queue[:offset]...), queue[end:]...)
			spliced = true
		}
		unconsumed[channelName] = queue
		if spliced {
			splicedChannels = append(splicedChannels, channelName)
		}
	}
	return splicedChannels
}

func reservedCountOnChannel(reservations map[string]stepReservation, channelName string) int {
	var total int
	for _, view := range reservations {
		total += int(channelReserveCounts(view.conditionResults)[channelName])
	}
	return total
}

// reservationOffset sums reserved counts with lower executeMethodExeID on the
// given channel — this determines where in the queue this step's slice begins.
func reservationOffset(reservations map[string]stepReservation, channelName string, myExeID int64) int {
	var offset int
	for _, view := range reservations {
		if view.executeMethodExeID == 0 || view.executeMethodExeID >= myExeID {
			continue
		}
		for _, cr := range view.conditionResults {
			if cr.Channel != nil && cr.Channel.Satisfied && cr.Channel.ChannelName == channelName {
				offset += int(cr.Channel.ConsumedCount)
			}
		}
	}
	return offset
}

func getCurrentReservations(activeSteps map[string]p.ActiveStepExecution) map[string]stepReservation {
	res := make(map[string]stepReservation)
	for stepExeID, step := range activeSteps {
		if step.ExecuteMethodExeID == 0 && len(step.ConditionResults) == 0 {
			continue
		}
		res[stepExeID] = stepReservation{
			executeMethodExeID: step.ExecuteMethodExeID,
			conditionResults:   step.ConditionResults,
		}
	}
	return res
}
