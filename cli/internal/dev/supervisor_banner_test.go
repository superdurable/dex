// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type serverInfoTestClient struct {
	dexpb.FlowServiceClient
	serverInfo *dexpb.ServerInfo
	err        error
}

func (c *serverInfoTestClient) GetServerInfo(context.Context, *emptypb.Empty, ...grpc.CallOption) (*dexpb.ServerInfo, error) {
	return c.serverInfo, c.err
}

func TestDescribeServerVersion(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		client *serverInfoTestClient
		want   string
	}{
		{"range", &serverInfoTestClient{serverInfo: &dexpb.ServerInfo{
			ServerVersion: "0.1.34", MinimumSupportedProtocolVersion: 1, CurrentProtocolVersion: 7,
		}}, "0.1.34 (protocol 1-7)"},
		{"one protocol", &serverInfoTestClient{serverInfo: &dexpb.ServerInfo{
			ServerVersion: "0.1.34", MinimumSupportedProtocolVersion: 7, CurrentProtocolVersion: 7,
		}}, "0.1.34 (protocol 7)"},
		{"no protocol", &serverInfoTestClient{serverInfo: &dexpb.ServerInfo{ServerVersion: "0.1.34"}}, "0.1.34"},
		{"no version", &serverInfoTestClient{serverInfo: &dexpb.ServerInfo{CurrentProtocolVersion: 7}}, ""},
		{"unimplemented", &serverInfoTestClient{err: status.Error(codes.Unimplemented, "old server")}, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := describeServerVersion(context.Background(), testCase.client); got != testCase.want {
				t.Fatalf("server version = %q, want %q", got, testCase.want)
			}
		})
	}
}

// hangingServerInfoClient answers GetServerInfo only when the call's context ends.
type hangingServerInfoClient struct {
	dexpb.FlowServiceClient
}

func (hangingServerInfoClient) GetServerInfo(ctx context.Context, _ *emptypb.Empty, _ ...grpc.CallOption) (*dexpb.ServerInfo, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestServerVersionLookupNeverDelaysTheReadyMessage(t *testing.T) {
	lookupContext, cancel := context.WithCancel(context.Background())
	started := time.Now()
	lookup := startServerVersionLookup(lookupContext, hangingServerInfoClient{})
	if version := lookup.versionIfAnswered(); version != "" {
		t.Fatalf("server version = %q before the server answered", version)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("a hanging GetServerInfo delayed startup by %s", elapsed)
	}
	cancel()
	select {
	case version := <-lookup.result:
		if version != "" {
			t.Fatalf("canceled lookup returned %q", version)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the lookup did not end when its context ended")
	}
}

func TestServerVersionLookupReportsAnAnswerThatArrived(t *testing.T) {
	lookup := startServerVersionLookup(context.Background(), &serverInfoTestClient{serverInfo: &dexpb.ServerInfo{
		ServerVersion: "0.1.34", MinimumSupportedProtocolVersion: 1, CurrentProtocolVersion: 7,
	}})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if version := lookup.versionIfAnswered(); version != "" {
			if version != "0.1.34 (protocol 1-7)" {
				t.Fatalf("server version = %q", version)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the lookup never answered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPrintReadyShowsTheServerVersionOnlyWhenKnown(t *testing.T) {
	for _, testCase := range []struct {
		serverVersion string
		isShown       bool
	}{{"0.1.34 (protocol 1-7)", true}, {"", false}} {
		stdout := &bytes.Buffer{}
		banner := &supervisor{cfg: &Config{}, stdout: stdout, stderr: &bytes.Buffer{}}
		banner.printReady("http://127.0.0.1:8802", "127.0.0.1:8801", testCase.serverVersion, "/tmp/state", "/tmp/dexcli/dev-8801.log")
		if !strings.Contains(stdout.String(), "Logs:          /tmp/dexcli/dev-8801.log\n") {
			t.Fatalf("banner does not name the server log:\n%s", stdout.String())
		}
		line := "Server version: " + testCase.serverVersion
		if strings.Contains(stdout.String(), "Server version:") != testCase.isShown ||
			(testCase.isShown && !strings.Contains(stdout.String(), line)) {
			t.Fatalf("banner for %q:\n%s", testCase.serverVersion, stdout.String())
		}
	}
}
