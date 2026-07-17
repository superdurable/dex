package remote

import "github.com/superdurable/dex/protos/gengo/dexpb"

type Routers interface {
	ForRunsService(address string) (dexpb.RunsServiceClient, error)
	ForTaskQueueService(address string) (dexpb.TaskQueueServiceClient, error)
	// EvictCachedConn is for when a remote instance is shutdown, we need to evict the cached connection to it
	EvictCachedConn(address string)
}
