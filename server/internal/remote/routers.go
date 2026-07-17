package remote

import "github.com/superdurable/dex/protos/gengo/dexpb"

type Routers interface {
	ForRunsService(address string) (dexpb.RunsServiceClient, error)
	ForTaskQueueService(address string) (dexpb.TaskQueueServiceClient, error)
	EvictCachedConn(address string)
}
