package mutation

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/internal/engine/evaluate"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func spliceConsumedMessages(
	runUnconsumed map[string][]p.ChannelMessage,
	activeSteps map[string]p.ActiveStepExecution,
	req *pb.StepExecuteCompletedRequest,
) []string {
	completing := make([]string, 0, 1+len(req.CanceledStepExecutions))
	completing = append(completing, req.StepExeId)
	completing = append(completing, req.CanceledStepExecutions...)
	return evaluate.SpliceUnconsumed(completing, activeSteps, runUnconsumed)
}

func applyChannelPublishes(
	update *p.RunRowUpdate,
	runUnconsumed map[string][]p.ChannelMessage,
	channelPubs map[string][]p.ChannelMessage,
	splicedChannels []string,
) {
	splicedSet := make(map[string]bool, len(splicedChannels))
	for _, channelName := range splicedChannels {
		splicedSet[channelName] = true
	}
	if len(channelPubs) == 0 && len(splicedChannels) == 0 {
		return
	}
	if update.ReplaceUnconsumedChannels == nil {
		update.ReplaceUnconsumedChannels = make(map[string][]p.ChannelMessage)
	}
	for channelName, messages := range channelPubs {
		delete(splicedSet, channelName)
		base, ok := update.ReplaceUnconsumedChannels[channelName]
		if !ok {
			base = runUnconsumed[channelName]
		}
		update.ReplaceUnconsumedChannels[channelName] = append(base, messages...)
	}
	for channelName := range splicedSet {
		update.ReplaceUnconsumedChannels[channelName] = runUnconsumed[channelName]
	}
}
