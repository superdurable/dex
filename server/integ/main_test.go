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

package integ

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/cmd/server/dex"
	"github.com/superdurable/dex/service/common/ptr"
	"go.temporal.io/sdk/client"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/.gen/go/shared"
)

func TestMain(m *testing.M) {
	flag.Parse()
	var err error

	if len(os.Args) > 0 {
		lastArg := os.Args[len(os.Args)-1]
		// check if lastArg is the regex  pattern of ^\QTest.*Temporal.*\E$
		matched, err := regexp.MatchString(`Test.*Temporal.*`, lastArg)
		if err == nil && matched {
			*temporalIntegTest = true
			*cadenceIntegTest = false
		} else {
			matched, err := regexp.MatchString(`Test.*Cadence.*`, lastArg)
			if err == nil && matched {
				*temporalIntegTest = false
				*cadenceIntegTest = true
			}
		}
	}

	fmt.Println("*temporalIntegTest, *cadenceIntegTest", *temporalIntegTest, *cadenceIntegTest)

	if *temporalIntegTest {
		var temporalClient client.Client
		for i := 0; i < *dependencyWaitSeconds; i++ {
			temporalClient, err = client.Dial(client.Options{
				HostPort:  *temporalHostPort,
				Namespace: testNamespace,
			})
			if err != nil {
				fmt.Println("wait for Temporal to be up...last err: ", err)
				time.Sleep(time.Second)

			} else {
				break
			}
		}
		if err != nil {
			log.Fatalf("unable to connect to Temporal %v", err)
		}
		temporalClient.Close()
		fmt.Println("connected to Temporal namespace")
		if *dependencyWaitSeconds > 0 {
			time.Sleep(time.Second * 1) // see https://github.com/temporalio/temporal/issues/4160
		}
	}

	if *cadenceIntegTest {
		for i := 0; i < *dependencyWaitSeconds; i++ {
			_, _, err = dex.BuildCadenceServiceClient(dex.DefaultCadenceHostPort)
			if err != nil {
				fmt.Println("wait for Cadence to be up...last err: ", err)
				time.Sleep(time.Second)
			} else {
				break
			}
		}
		if err != nil {
			log.Fatalf("cannot connnect to Cadence service%v", err)
		}
		fmt.Println("connected to Cadence service")

		var closeFunc func()
		var serviceClient workflowserviceclient.Interface
		serviceClient, closeFunc, err = dex.BuildCadenceServiceClient(dex.DefaultCadenceHostPort)
		for i := 0; i < *dependencyWaitSeconds; i++ {
			ctx, _ := context.WithTimeout(context.Background(), time.Second*10)
			_, err = serviceClient.DescribeDomain(ctx, &shared.DescribeDomainRequest{
				Name: ptr.Any(dex.DefaultCadenceDomain),
			})
			if err != nil {
				fmt.Println("wait for Cadence domain to be ready...", err)
				time.Sleep(time.Second)
			} else {
				break
			}
		}
		if err != nil {
			log.Fatalf("Cadence service is not ready %v", err)
		}
		fmt.Println("Cadence service is now ready")
		closeFunc()
	}

	// disable caching for now as it makes it difficult to test memo
	//var integCadenceUclientCloseFunc, integTemporalUclientCloseFunc func()
	//if !(*cadenceIntegTest && *temporalIntegTest) {
	//	// hack for ci test to save the performance
	//	// only start connection once
	//	// TODO need to do this for outside of ci as well
	//	if *cadenceIntegTest {
	//		integCadenceUclientCached, integCadenceUclientCloseFunc = doStartDexServiceWithClient(service.BackendTypeCadence)
	//		defer func() {
	//			fmt.Println("shutdown cadence client and dex server")
	//			integCadenceUclientCloseFunc()
	//		}()
	//		fmt.Println("cached cadence client and dex server")
	//	} else {
	//		integTemporalUclientCached, integTemporalUclientCloseFunc = doStartDexServiceWithClient(service.BackendTypeTemporal)
	//		defer func() {
	//			fmt.Println("shutdown temporal client and dex server")
	//			integTemporalUclientCloseFunc()
	//		}()
	//		fmt.Println("cached temporal client and dex server")
	//	}
	//}

	code := m.Run()
	fmt.Println("finished running integ test with status code", code)
	os.Exit(code)
}

func newRequestID() string {
	return uuid.NewString()
}
