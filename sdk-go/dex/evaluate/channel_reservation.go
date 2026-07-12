package evaluate

import (
	"sort"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

type stepReservation struct {
	executeMethodExeID int64
	conditionResults   []*pb.ConditionResult
}

// based on all current reservations, check if remaining unconsumed has enough messages to meet the given all channel conditions
// return:
//
//	bool as whether or not the conditions are all met
//	[]int as how many messages can be reserved for each channel condition from the given list
func HasMetAllChannelConditions(
	conds []*pb.ChannelCondition, activeStepExes map[string]*pb.ActiveStepExecution, unconsumed map[string][]*pb.Value,
) (bool, []int) {
	if len(conds) == 0 {
		return true, []int{}
	}

	reservations := getCurrentReservations(activeStepExes)

	type channelPlan struct {
		totalMin int32
		maxTake  int32
	}
	plans := map[string]*channelPlan{}
	for _, cond := range conds {
		if cond == nil {
			return false, nil
		}
		plan, ok := plans[cond.GetChannelName()]
		if !ok {
			plan = &channelPlan{}
			plans[cond.GetChannelName()] = plan
		}
		plan.totalMin += cond.GetMin()
		if cond.GetMax() > 0 {
			plan.maxTake += cond.GetMax()
		} else {
			plan.maxTake += cond.GetMin()
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

func distributeChannelTakeCounts(
	conds []*pb.ChannelCondition, channelName string, takeCount int, counts []int,
) {
	surplus := takeCount
	for index, cond := range conds {
		if cond == nil || cond.GetChannelName() != channelName {
			continue
		}
		counts[index] = int(cond.GetMin())
		surplus -= counts[index]
	}
	if surplus <= 0 {
		return
	}
	for index, cond := range conds {
		if cond == nil || cond.GetChannelName() != channelName {
			continue
		}
		max := cond.GetMax()
		if max <= 0 {
			continue
		}
		headroom := int(max) - counts[index]
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

// based on all current reservations, check if remaining unconsumed has enough messages to meet the given channel condition
// return:
//
//	bool as whether or not the condition is met
//	int as how many messages can be reserved
// extraReserved is the count already taken from this channel by earlier
// satisfied branches of the SAME greedy AnyOf evaluation, so two conditions on
// one channel cannot reserve the same messages twice.
func HasMetSingleChannelCondition(
	cond *pb.ChannelCondition, activeStepExes map[string]*pb.ActiveStepExecution, unconsumed map[string][]*pb.Value, extraReserved int,
) (bool, int) {
	if cond == nil {
		return false, 0
	}
	reservations := getCurrentReservations(activeStepExes)
	reserved := reservedCountOnChannel(reservations, cond.GetChannelName())
	available := len(unconsumed[cond.GetChannelName()]) - reserved - extraReserved
	if available < int(cond.GetMin()) {
		return false, 0
	}
	return true, reserveCountForCondition(cond, available)
}

func reserveCountForCondition(cond *pb.ChannelCondition, available int) int {
	takeCount := int(cond.GetMin())
	if max := cond.GetMax(); max > 0 {
		if int(max) < available {
			takeCount = int(max)
		} else {
			takeCount = available
		}
	}
	if takeCount < int(cond.GetMin()) {
		takeCount = int(cond.GetMin())
	}
	return takeCount
}

func reservedCountOnChannel(reservations map[string]stepReservation, channelName string) int {
	var total int
	for _, view := range reservations {
		total += int(ChannelReserveCountsFromProto(view.conditionResults)[channelName])
	}
	return total
}

// return false, nil if the stepExeId doesn't have an reservation
func GetStepExeReservedMessages(
	stepExeID string, activeStepExes map[string]*pb.ActiveStepExecution, unconsumed map[string][]*pb.Value,
) map[string][]*pb.Value {
	stepReservations := getCurrentReservations(activeStepExes)
	view, ok := stepReservations[stepExeID]
	if !ok {
		return nil
	}
	reserved := map[string][]*pb.Value{}
	for _, result := range view.conditionResults {
		if channel := result.GetChannel(); channel != nil && channel.Satisfied && channel.ConsumedCount > 0 {
			reserved[channel.ChannelName] = messagesForStep(
				unconsumed, stepReservations, stepExeID, channel.ChannelName, channel.ConsumedCount,
			)
		}
	}
	return reserved
}

// SpliceUnconsumed will actually consume it from the unconsumed
func SpliceUnconsumed(
	removeStepExeIDs []string, activeStepExes map[string]*pb.ActiveStepExecution, unconsumed map[string][]*pb.Value,
) {
	stepReservations := getCurrentReservations(activeStepExes)
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
		view, ok := stepReservations[stepExeID]
		if !ok {
			continue
		}
		for channelName, count := range ChannelReserveCountsFromProto(view.conditionResults) {
			all = append(all, reservation{
				stepExeID:          stepExeID,
				executeMethodExeID: view.executeMethodExeID,
				channel:            channelName,
				count:              count,
			})
		}
	}
	if len(all) == 0 {
		return
	}
	byChannel := map[string][]reservation{}
	for _, item := range all {
		byChannel[item.channel] = append(byChannel[item.channel], item)
	}
	for channelName, channelReservations := range byChannel {
		sort.Slice(channelReservations, func(i, j int) bool {
			return channelReservations[i].executeMethodExeID > channelReservations[j].executeMethodExeID
		})
		queue := unconsumed[channelName]
		for _, item := range channelReservations {
			offset := reservationOffsetPb(stepReservations, channelName, item.executeMethodExeID)
			count := int(item.count)
			if count <= 0 || offset >= len(queue) {
				continue
			}
			end := offset + count
			if end > len(queue) {
				end = len(queue)
			}
			queue = append(append([]*pb.Value{}, queue[:offset]...), queue[end:]...)
		}
		unconsumed[channelName] = queue
	}
}

func ChannelReserveCountsFromProto(results []*pb.ConditionResult) map[string]int32 {
	out := map[string]int32{}
	for _, result := range results {
		channel := result.GetChannel()
		if channel == nil || !channel.Satisfied || channel.ConsumedCount <= 0 {
			continue
		}
		out[channel.ChannelName] += channel.ConsumedCount
	}
	return out
}

func messagesForStep(
	unconsumed map[string][]*pb.Value,
	reservations map[string]stepReservation,
	stepExeID, channelName string,
	count int32,
) []*pb.Value {
	view, ok := reservations[stepExeID]
	if !ok || count <= 0 {
		return nil
	}
	queue := unconsumed[channelName]
	offset := reservationOffsetPb(reservations, channelName, view.executeMethodExeID)
	if offset >= len(queue) {
		return nil
	}
	end := offset + int(count)
	if end > len(queue) {
		end = len(queue)
	}
	return queue[offset:end]
}

func reservationOffsetPb(
	reservations map[string]stepReservation,
	channelName string,
	myExecuteMethodExeID int64,
) int {
	var offset int
	for _, view := range reservations {
		if view.executeMethodExeID == 0 || view.executeMethodExeID >= myExecuteMethodExeID {
			continue
		}
		for _, result := range view.conditionResults {
			channel := result.GetChannel()
			if channel != nil && channel.Satisfied && channel.ChannelName == channelName {
				offset += int(channel.ConsumedCount)
			}
		}
	}
	return offset
}

func getCurrentReservations(activeStepExes map[string]*pb.ActiveStepExecution) map[string]stepReservation {
	res := make(map[string]stepReservation)
	for stepExeID, step := range activeStepExes {
		if step.ExecuteMethodExeId == 0 && len(step.ConditionResults) == 0 {
			continue
		}
		res[stepExeID] = stepReservation{
			executeMethodExeID: step.ExecuteMethodExeId,
			conditionResults:   step.ConditionResults,
		}
	}
	return res
}
