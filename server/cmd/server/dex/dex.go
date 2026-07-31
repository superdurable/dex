// Copyright (c) 2021 Cadence workflow OSS organization
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

package dex

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/superdurable/dex/config"
	serviceruntime "github.com/superdurable/dex/service/runtime"
	"github.com/urfave/cli"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/client"
	"go.uber.org/cadence/encoded"
)

const serviceAPI = "api"
const serviceInterpreter = "interpreter"

const DefaultCadenceDomain = serviceruntime.DefaultCadenceDomain
const DefaultCadenceHostPort = serviceruntime.DefaultCadenceHostPort

// BuildCLI is the main entry point for the dex server
func BuildCLI() *cli.App {
	app := cli.NewApp()
	app.Name = "dex service"
	app.Usage = "dex service"
	app.Version = "beta"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config, c",
			Value: "config/development.yaml",
			Usage: "config path is a path relative to root, or an absolute path",
		},
	}
	app.Commands = []cli.Command{
		{
			Name:    "start",
			Aliases: []string{""},
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "services",
					Value: fmt.Sprintf("%s, %s", serviceAPI, serviceInterpreter),
					Usage: "start services/components in this project",
				},
			},
			Usage:  "start dex notification service",
			Action: start,
		},
	}
	return app
}

func start(cliContext *cli.Context) error {
	cfg, err := config.NewConfig(cliContext.GlobalString("config"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	services, err := getServices(cliContext)
	if err != nil {
		return err
	}
	runtime, err := serviceruntime.New(cfg, &serviceruntime.Options{Services: services})
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGHUP,
	)
	defer cancel()
	return runtime.Run(ctx)
}

func getServices(cliContext *cli.Context) (serviceruntime.Services, error) {
	value := strings.TrimSpace(cliContext.String("services"))
	if value == "" {
		return serviceruntime.Services{}, fmt.Errorf("no services specified for starting")
	}
	var services serviceruntime.Services
	for _, token := range strings.Split(value, ",") {
		switch strings.TrimSpace(token) {
		case serviceAPI:
			services.API = true
		case serviceInterpreter:
			services.Interpreter = true
		default:
			return serviceruntime.Services{}, fmt.Errorf("invalid service %q", token)
		}
	}
	return services, nil
}

func BuildCadenceClient(
	serviceClient workflowserviceclient.Interface,
	domain string,
	dataConverter encoded.DataConverter,
) (client.Client, error) {
	return serviceruntime.BuildCadenceClient(serviceClient, domain, dataConverter)
}

func BuildCadenceServiceClient(hostPort string) (workflowserviceclient.Interface, func(), error) {
	return serviceruntime.BuildCadenceServiceClient(hostPort)
}

func CreateS3Client(cfg config.Config, ctx context.Context) (*s3.Client, error) {
	return serviceruntime.CreateS3Client(ctx, &cfg)
}
