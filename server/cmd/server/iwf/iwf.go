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

package iwf

import (
	"context"
	"crypto/tls"
	"fmt"
	rawLog "log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/superdurable/iwf/config"
	isvc "github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/api"
	uclient "github.com/superdurable/iwf/service/client"
	cadenceapi "github.com/superdurable/iwf/service/client/cadence"
	temporalapi "github.com/superdurable/iwf/service/client/temporal"
	"github.com/superdurable/iwf/service/common/blobstore"
	iwfconverter "github.com/superdurable/iwf/service/common/converter"
	"github.com/superdurable/iwf/service/common/log"
	"github.com/superdurable/iwf/service/common/log/loggerimpl"
	"github.com/superdurable/iwf/service/common/log/tag"
	"github.com/superdurable/iwf/service/interpreter/cadence"
	"github.com/superdurable/iwf/service/interpreter/temporal"
	"github.com/uber-go/tally/v4"
	"github.com/uber-go/tally/v4/prometheus"
	apiv1 "github.com/uber/cadence-idl/go/proto/api/v1"
	"github.com/urfave/cli"
	"go.temporal.io/sdk/client"
	sdktally "go.temporal.io/sdk/contrib/tally"
	"go.temporal.io/sdk/converter"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	cclient "go.uber.org/cadence/client"
	"go.uber.org/cadence/compatibility"
	"go.uber.org/cadence/encoded"
	"go.uber.org/yarpc"
	"go.uber.org/yarpc/transport/grpc"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const serviceAPI = "api"
const serviceInterpreter = "interpreter"

// BuildCLI is the main entry point for the iwf server
func BuildCLI() *cli.App {
	app := cli.NewApp()
	app.Name = "iwf service"
	app.Usage = "iwf service"
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
			Usage:  "start iwf notification service",
			Action: start,
		},
	}
	return app
}

const DefaultCadenceDomain = "default"
const DefaultCadenceHostPort = "127.0.0.1:7833"

func start(c *cli.Context) {
	configPath := c.GlobalString("config")
	config, err := config.NewConfig(configPath)
	if err != nil {
		rawLog.Fatalf("Unable to load config for path %v because of error %v", configPath, err)
	}
	zapLogger, err := config.Log.NewZapLogger()
	if err != nil {
		rawLog.Fatalf("Unable to create a new zap logger %v", err)
	}
	logger := loggerimpl.NewLogger(zapLogger)

	services := getServices(c)

	// The client is a heavyweight object that should be created once per process.
	var unifiedClient uclient.UnifiedClient
	if config.Interpreter.Temporal != nil {
		temporalConfig := config.Interpreter.Temporal

		clientOptions := client.Options{
			HostPort:  temporalConfig.HostPort,
			Namespace: temporalConfig.Namespace,
		}

		metrics := client.MetricsNopHandler
		if temporalConfig.Prometheus != nil {
			pscope := newPrometheusScope(*temporalConfig.Prometheus, logger)
			metrics = sdktally.NewMetricsHandler(pscope)
			clientOptions.MetricsHandler = metrics
		}

		if temporalConfig.CloudAPIKey != "" {
			clientOptions.Credentials = client.NewAPIKeyStaticCredentials(temporalConfig.CloudAPIKey)
			// NOTE: this connectionOptions can be removed when upgrading temporal SDK to latest
			// see https://docs.temporal.io/cloud/api-keys#sdk
			clientOptions.ConnectionOptions = client.ConnectionOptions{
				TLS: &tls.Config{},
				DialOptions: []ggrpc.DialOption{
					ggrpc.WithUnaryInterceptor(
						func(
							ctx context.Context, method string, req any, reply any, cc *ggrpc.ClientConn,
							invoker ggrpc.UnaryInvoker, opts ...ggrpc.CallOption,
						) error {
							return invoker(
								metadata.AppendToOutgoingContext(ctx, "temporal-namespace", temporalConfig.Namespace),
								method,
								req,
								reply,
								cc,
								opts...,
							)
						},
					),
				},
			}
		}

		dataConverter := iwfconverter.NewTemporalDataConverter()
		clientOptions.DataConverter = dataConverter
		temporalClient, err := client.Dial(clientOptions)

		if err != nil {
			rawLog.Fatalf("Unable to connect to Temporal because of error %v", err)
		}
		unifiedClient = temporalapi.NewTemporalClient(
			temporalClient,
			temporalConfig.Namespace,
			dataConverter,
			false,
			&config.Api.QueryWorkflowFailedRetryPolicy,
		)

		for _, svcName := range services {
			go launchTemporalService(svcName, *config, unifiedClient, temporalClient, dataConverter, logger, metrics)
		}
	} else if config.Interpreter.Cadence != nil {
		hostPort := DefaultCadenceHostPort
		domain := DefaultCadenceDomain
		if config.Interpreter.Cadence.HostPort != "" {
			hostPort = config.Interpreter.Cadence.HostPort
		}
		if config.Interpreter.Cadence.Domain != "" {
			domain = config.Interpreter.Cadence.Domain
		}
		serviceClient, closeFunc, err := BuildCadenceServiceClient(hostPort)
		if err != nil {
			rawLog.Fatalf("Unable to connect to Cadence because of error %v", err)
		}
		dataConverter := iwfconverter.NewCadenceDataConverter()
		cadenceClient, err := BuildCadenceClient(serviceClient, domain, dataConverter)
		if err != nil {
			rawLog.Fatalf("Unable to connect to Cadence because of error %v", err)
		}

		unifiedClient = cadenceapi.NewCadenceClient(
			domain,
			cadenceClient,
			serviceClient,
			dataConverter,
			closeFunc,
			&config.Api.QueryWorkflowFailedRetryPolicy,
		)

		for _, svcName := range services {
			go launchCadenceService(
				svcName,
				*config,
				unifiedClient,
				serviceClient,
				domain,
				closeFunc,
				dataConverter,
				logger,
			)
		}
	} else {
		panic("must provide either Cadence or Temporal config")
	}

	// TODO improve the waiting with process signal
	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}

func launchTemporalService(
	svcName string, config config.Config, unifiedClient uclient.UnifiedClient, temporalClient client.Client,
	dataConverter converter.DataConverter, logger log.Logger, metrics client.MetricsHandler,
) {
	s3Client := CreateS3Client(config, context.Background())
	blobStore := blobstore.NewBlobStore(
		s3Client,
		config.Interpreter.Temporal.Namespace,
		config.ExternalStorage,
		logger,
		metrics,
	)
	switch svcName {
	case serviceAPI:
		svc := api.NewServer(
			&config.Api,
			&config.ExternalStorage,
			&config.Interpreter,
			unifiedClient,
			logger.WithTags(tag.Service(svcName)),
			blobStore,
			func(ctx context.Context) error {
				return nil
			},
		)
		rawLog.Fatal(svc.Run())
	case serviceInterpreter:
		interpreter := temporal.NewInterpreterWorker(
			&config.Api,
			&config.ExternalStorage,
			&config.Interpreter,
			config.Interpreter.Temporal,
			temporalClient,
			isvc.TaskQueue,
			dataConverter,
			unifiedClient,
			blobStore,
		)
		interpreter.Start()
	default:
		rawLog.Fatalf("Invalid service: %v", svcName)
	}
}

func launchCadenceService(
	svcName string,
	config config.Config,
	unifiedClient uclient.UnifiedClient,
	service workflowserviceclient.Interface,
	domain string,
	closeFunc func(),
	dataConverter encoded.DataConverter,
	logger log.Logger,
) {
	s3Client := CreateS3Client(config, context.Background())
	blobStore := blobstore.NewBlobStore(
		s3Client,
		config.Interpreter.Cadence.Domain,
		config.ExternalStorage,
		logger,
		client.MetricsNopHandler,
	)
	switch svcName {
	case serviceAPI:
		svc := api.NewServer(
			&config.Api,
			&config.ExternalStorage,
			&config.Interpreter,
			unifiedClient,
			logger.WithTags(tag.Service(svcName)),
			blobStore,
			func(ctx context.Context) error {
				return nil
			},
		)
		rawLog.Fatal(svc.Run())
	case serviceInterpreter:
		interpreter := cadence.NewInterpreterWorker(
			&config.Api,
			&config.ExternalStorage,
			&config.Interpreter,
			config.Interpreter.Cadence,
			service,
			domain,
			isvc.TaskQueue,
			closeFunc,
			dataConverter,
			unifiedClient,
			blobStore,
		)
		interpreter.Start()
	default:
		rawLog.Fatalf("Invalid service: %v", svcName)
	}
}

func getServices(c *cli.Context) []string {
	val := strings.TrimSpace(c.String("services"))
	tokens := strings.Split(val, ",")

	if len(tokens) == 0 {
		rawLog.Fatal("No services specified for starting")
	}

	var services []string
	for _, token := range tokens {
		t := strings.TrimSpace(token)
		services = append(services, t)
	}

	return services
}

const _cadenceFrontendService = "cadence-frontend"
const _cadenceClientName = "cadence-client"

func BuildCadenceClient(
	service workflowserviceclient.Interface,
	domain string,
	dataConverter encoded.DataConverter,
) (cclient.Client, error) {
	return cclient.NewClient(
		service,
		domain,
		&cclient.Options{
			DataConverter: dataConverter,
			FeatureFlags: cclient.FeatureFlags{
				WorkflowExecutionAlreadyCompletedErrorEnabled: true,
			},
		}), nil
}

func BuildCadenceServiceClient(hostPort string) (workflowserviceclient.Interface, func(), error) {

	dispatcher := yarpc.NewDispatcher(yarpc.Config{
		Name: _cadenceClientName,
		Outbounds: yarpc.Outbounds{
			_cadenceFrontendService: {Unary: grpc.NewTransport().NewSingleOutbound(hostPort)},
		},
	})

	if dispatcher != nil {
		if err := dispatcher.Start(); err != nil {
			rawLog.Fatal("Failed to create outbound transport channel", err)
		}
	}

	if dispatcher == nil {
		rawLog.Fatal("No RPC dispatcher provided to create a connection to Cadence Service")
	}

	clientConfig := dispatcher.ClientConfig(_cadenceFrontendService)
	return compatibility.NewThrift2ProtoAdapter(
			apiv1.NewDomainAPIYARPCClient(clientConfig),
			apiv1.NewWorkflowAPIYARPCClient(clientConfig),
			apiv1.NewWorkerAPIYARPCClient(clientConfig),
			apiv1.NewVisibilityAPIYARPCClient(clientConfig),
		), func() {
			dispatcher.Stop()
		}, nil
}

// tally sanitizer options that satisfy Prometheus restrictions.
// This will rename metrics at the tally emission level, so metrics name we
// use maybe different from what gets emitted. In the current implementation
// it will replace - and . with _
var (
	safeCharacters = []rune{'_'}

	sanitizeOptions = tally.SanitizeOptions{
		NameCharacters: tally.ValidCharacters{
			Ranges:     tally.AlphanumericRange,
			Characters: safeCharacters,
		},
		KeyCharacters: tally.ValidCharacters{
			Ranges:     tally.AlphanumericRange,
			Characters: safeCharacters,
		},
		ValueCharacters: tally.ValidCharacters{
			Ranges:     tally.AlphanumericRange,
			Characters: safeCharacters,
		},
		ReplacementCharacter: tally.DefaultReplacementCharacter,
	}
)

func newPrometheusScope(c prometheus.Configuration, logger log.Logger) tally.Scope {
	reporter, err := c.NewReporter(
		prometheus.ConfigurationOptions{
			Registry: prom.NewRegistry(),
			OnError: func(err error) {
				logger.Error("error in prometheus reporter", tag.Error(err))
			},
		},
	)
	if err != nil {
		logger.Fatal("error creating prometheus reporter", tag.Error(err))
	}
	scopeOpts := tally.ScopeOptions{
		CachedReporter:  reporter,
		Separator:       prometheus.DefaultSeparator,
		SanitizeOptions: &sanitizeOptions,
		Prefix:          "temporal_samples",
	}
	scope, _ := tally.NewRootScope(scopeOpts, time.Second)

	logger.Info("prometheus metrics scope created")
	return scope
}

func CreateS3Client(cfg config.Config, ctx context.Context) *s3.Client {

	if !cfg.ExternalStorage.Enabled {
		return nil
	}

	// get the first active storage
	var activeStorage *config.BlobStorageConfig
	for _, storage := range cfg.ExternalStorage.SupportedStorages {
		if storage.Status == config.StorageStatusActive {
			activeStorage = &storage
			break
		}
	}
	if activeStorage == nil {
		rawLog.Fatal("no active storage found")
	}

	if activeStorage.StorageType != "s3" {
		rawLog.Fatal("only s3 is supported for external storage")
	}

	// Create custom resolver for MinIO endpoint
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {

		if service == s3.ServiceID {
			return aws.Endpoint{
				URL:               activeStorage.S3Endpoint,
				HostnameImmutable: true,
				Source:            aws.EndpointSourceCustom,
			}, nil
		}
		return aws.Endpoint{}, fmt.Errorf("unknown endpoint requested")
	})

	// Load AWS config with custom credentials and endpoint
	cfg2, err := awsConfig.LoadDefaultConfig(ctx,
		awsConfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(activeStorage.S3AccessKey, activeStorage.S3SecretKey, "")),
		awsConfig.WithRegion(activeStorage.S3Region),
		awsConfig.WithEndpointResolverWithOptions(customResolver),
	)
	if err != nil {
		rawLog.Fatal("failed to load AWS config", tag.Error(err))
	}

	// Create S3 client with path-style addressing (required for MinIO)
	client := s3.NewFromConfig(cfg2, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	createBucketIfNotExists(ctx, client, activeStorage.S3Bucket)

	return client
}

func createBucketIfNotExists(ctx context.Context, client *s3.Client, bucketName string) {
	// Check if bucket exists
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})

	if err != nil {
		// Bucket doesn't exist, create it
		_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: aws.String(bucketName),
		})

		if err != nil {
			bucketCreateError := err
			// Its posible creating a bucket failed because the bucket was created by another service (api or interpreter)
			// check the bucket still does not exist
			_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
				Bucket: aws.String(bucketName),
			})

			if err != nil {
				rawLog.Fatal("failed to create bucket", tag.Error(bucketCreateError))
			}
		}
		rawLog.Printf("bucket created successfully: %s", bucketName)
	} else {
		rawLog.Printf("bucket already exists: %s", bucketName)
	}
}
