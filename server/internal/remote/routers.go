package remote

import "github.com/superdurable/dex/protos/gengo/dexpb"

type Routers interface {
	ForEngineService(address string) (dexpb.EngineServiceClient, error)
	ForTaskQueueService(address string) (dexpb.TaskQueueServiceClient, error)
	// EvictCachedConn is for when a remote instance is shutdown, we need to evict the cached connection to it
	EvictCachedConn(address string)
}
