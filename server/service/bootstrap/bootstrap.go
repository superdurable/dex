// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/service/api"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/blobstore"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/log/loggerimpl"
	"github.com/superdurable/dex/service/common/log/tag"
	"github.com/superdurable/dex/service/common/workerclient"
	"google.golang.org/grpc"
)

const DefaultShutdownTimeout = 10 * time.Second

type Services struct {
	// API defaults false and enables the Dex gRPC API.
	API bool
	// Interpreter defaults false and enables the workflow interpreter.
	Interpreter bool
}

type Options struct {
	// Services has no default; at least one service must be enabled.
	Services Services
	// APIListener defaults nil and uses the configured API port.
	APIListener net.Listener
	// ShutdownTimeout defaults to 10 seconds for graceful API shutdown.
	ShutdownTimeout time.Duration
}

type Runtime struct {
	cfg           *config.Config
	options       *Options
	apiServer     *api.Server
	worker        interpreterWorker
	workerPool    *workerclient.WorkerClientPool
	logger        log.Logger
	metricsCloser io.Closer
	serveError    chan error
	shutdownOnce  sync.Once
}

type interpreterWorker interface {
	Start() error
	Close()
}

func New(cfg *config.Config, options *Options) (*Runtime, error) {
	if cfg == nil {
		panic("runtime config must not be nil")
	}
	if options == nil {
		panic("runtime options must not be nil")
	}
	if !options.Services.API && !options.Services.Interpreter {
		return nil, fmt.Errorf("at least one Dex service is required")
	}
	if cfg.Interpreter.Temporal == nil && cfg.Interpreter.Cadence == nil {
		return nil, fmt.Errorf("Temporal or Cadence config is required")
	}
	if cfg.Interpreter.Temporal != nil && cfg.Interpreter.Cadence != nil {
		return nil, fmt.Errorf("Temporal and Cadence configs are mutually exclusive")
	}

	zapLogger, err := cfg.Log.NewZapLogger()
	if err != nil {
		return nil, fmt.Errorf("create logger: %w", err)
	}
	logger := loggerimpl.NewLogger(zapLogger)
	workerPool, err := workerclient.NewWorkerClientPool(cfg)
	if err != nil {
		return nil, fmt.Errorf("create WorkerService client pool: %w", err)
	}

	runtime := &Runtime{
		cfg:        cfg,
		options:    options,
		workerPool: workerPool,
		logger:     logger,
		serveError: make(chan error, 1),
	}
	if err := runtime.createServices(); err != nil {
		runtime.shutdown()
		return nil, err
	}
	return runtime, nil
}

func (r *Runtime) createServices() error {
	var (
		client uclient.UnifiedClient
		store  blobstore.BlobStore
		err    error
	)
	if r.cfg.Interpreter.Temporal != nil {
		client, store, r.worker, err = r.createTemporalServices()
	} else {
		client, store, r.worker, err = r.createCadenceServices()
	}
	if err != nil {
		return err
	}
	if r.options.Services.API {
		r.apiServer = api.NewServer(
			&r.cfg.Api,
			&r.cfg.ExternalStorage,
			&r.cfg.Interpreter,
			client,
			r.logger.WithTags(tag.Service("api")),
			store,
			nil,
			r.workerPool,
		)
	}
	return nil
}

func (r *Runtime) Run(ctx context.Context) error {
	if r.worker != nil {
		if err := r.worker.Start(); err != nil {
			r.shutdown()
			return err
		}
	}
	if r.apiServer != nil {
		go func() {
			if r.options.APIListener != nil {
				r.serveError <- r.apiServer.Serve(r.options.APIListener)
				return
			}
			r.serveError <- r.apiServer.Run()
		}()
	}

	if r.apiServer == nil {
		<-ctx.Done()
		r.shutdown()
		return nil
	}

	select {
	case <-ctx.Done():
		r.shutdown()
		err := <-r.serveError
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve Dex API: %w", err)
		}
		return nil
	case err := <-r.serveError:
		r.shutdown()
		if err == nil || errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("Dex API stopped unexpectedly")
		}
		return fmt.Errorf("serve Dex API: %w", err)
	}
}

func (r *Runtime) Close() {
	r.shutdown()
}

func (r *Runtime) shutdown() {
	r.shutdownOnce.Do(func() {
		if r.apiServer != nil {
			timeout := r.options.ShutdownTimeout
			if timeout <= 0 {
				timeout = DefaultShutdownTimeout
			}
			r.apiServer.StopServing(timeout)
		}
		if r.worker != nil {
			r.worker.Close()
		}
		if r.apiServer != nil {
			r.apiServer.Close()
		}
		r.workerPool.Close()
		if r.metricsCloser != nil {
			if err := r.metricsCloser.Close(); err != nil {
				r.logger.Error("close metrics scope", tag.Error(err))
			}
		}
	})
}
