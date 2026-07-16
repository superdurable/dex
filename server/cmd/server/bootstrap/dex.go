// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package bootstrap

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/persistence/process"
	"github.com/superdurable/dex/server/persistence/visibility"
	api2 "github.com/superdurable/dex/server/service/api"
	async2 "github.com/superdurable/dex/server/service/async"

	rawLog "log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
	"go.uber.org/multierr"
)

const ApiServiceName = "api"
const AsyncServiceName = "async"

const FlagConfig = "config"
const FlagService = "service"

func StartDexServerCli(c *cli.Context) {
	// register interrupt signal for graceful shutdown
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	configPath := c.String("config")
	services := getServices(c)

	cfg, err := config.NewConfig(configPath)
	if err != nil {
		rawLog.Fatalf("Unable to load config for path %v because of error %v", configPath, err)
	}
	shutdownFunc := StartDexServer(rootCtx, cfg, services)
	// wait for os signals
	<-rootCtx.Done()

	ctx, cancF := context.WithTimeout(context.Background(), time.Second*10)
	defer cancF()
	err = shutdownFunc(ctx)
	if err != nil {
		fmt.Println("shutdown error:", err)
	}
}

type GracefulShutdown func(ctx context.Context) error

func StartDexServer(rootCtx context.Context, cfg *config.Config, services map[string]bool) GracefulShutdown {
	if len(services) == 0 {
		services = map[string]bool{ApiServiceName: true, AsyncServiceName: true}
	}

	zapLogger, err := cfg.Log.NewZapLogger()
	if err != nil {
		rawLog.Fatalf("Unable to create a new zap logger %v", err)
	}
	logger := log.NewLogger(zapLogger)
	logger.Info("config is loaded", tag.Value(cfg.String()))
	err = cfg.ValidateAndSetDefaults()
	if err != nil {
		logger.Fatal("config is invalid", tag.Error(err))
	}

	processStore, err := process.NewSQLProcessStore(*cfg.Database.ProcessStoreConfig, logger)
	if err != nil {
		logger.Fatal("error on persistence setup", tag.Error(err))
	}

	visibilityStore, err := visibility.NewSqlVisibilityStore(*cfg.Database.VisibilityStoreConfig, logger)
	if err != nil {
		logger.Fatal("error on visibility setup", tag.Error(err))
	}

	var apiServer api2.Server
	if services[ApiServiceName] {
		apiServer = api2.NewDefaultAPIServerWithGin(
			rootCtx, *cfg, processStore, visibilityStore, logger.WithTags(tag.Service(ApiServiceName)))
		err = apiServer.Start()
		if err != nil {
			logger.Fatal("Failed to start api server", tag.Error(err))
		}
	}

	var asyncServer async2.Server
	if services[AsyncServiceName] {
		asyncServer = async2.NewDefaultAsyncServerWithGin(
			rootCtx, *cfg, processStore, visibilityStore, logger.WithTags(tag.Service(AsyncServiceName)))
		err = asyncServer.Start()
		if err != nil {
			logger.Fatal("Failed to start async server", tag.Error(err))
		}
	}

	return func(ctx context.Context) error {
		// graceful shutdown
		var errs error
		// first stop api server
		if apiServer != nil {
			err := apiServer.Stop(ctx)
			if err != nil {
				errs = multierr.Append(errs, err)
			}
		}
		if asyncServer != nil {
			err := asyncServer.Stop(ctx)
			if err != nil {
				errs = multierr.Append(errs, err)
			}
		}
		// stop processStore and visibilityStore
		err := processStore.Close()
		if err != nil {
			errs = multierr.Append(errs, err)
		}
		err = visibilityStore.Close()
		if err != nil {
			errs = multierr.Append(errs, err)
		}
		return errs
	}
}

func getServices(c *cli.Context) map[string]bool {
	val := strings.TrimSpace(c.String(FlagService))
	tokens := strings.Split(val, ",")

	if len(tokens) == 0 {
		rawLog.Fatal("No services specified for starting")
	}

	services := map[string]bool{}
	for _, token := range tokens {
		t := strings.TrimSpace(token)
		services[t] = true
	}

	return services
}
