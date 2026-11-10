// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Command fake-dex-connectors-mcp speaks the dex-connectors-mcp serve launch contract 1 for dexcli dev tests.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

type repeatedFlag []string

func (values *repeatedFlag) String() string { return strings.Join(*values, ",") }

func (values *repeatedFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: fake-dex-connectors-mcp serve [flags]")
		os.Exit(2)
	}
	if err := serve(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(arguments []string) error {
	recordStart(arguments)
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:8803", "")
	connectionsFile := flags.String("connections-file", "", "")
	stateDirectory := flags.String("state-dir", "", "")
	output := flags.String("output", "text", "")
	exitOnStdinClose := flags.Bool("exit-on-stdin-close", false, "")
	var connections repeatedFlag
	flags.Var(&connections, "connection", "")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *output != "json" {
		return errors.New("the fake supports only --output json")
	}
	if os.Getenv("FAKE_CONNECTORS_MCP_MODE") == "error" {
		fmt.Println(`{"type":"error","message":"fake startup failure"}`)
		os.Exit(1)
	}
	if err := os.MkdirAll(*stateDirectory, 0o700); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(filepath.Join(*stateDirectory, "server.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fmt.Fprintln(os.Stderr, "another server holds the state directory")
		os.Exit(3)
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	instanceBytes := make([]byte, 8)
	if _, err := rand.Read(instanceBytes); err != nil {
		return err
	}
	instanceID := hex.EncodeToString(instanceBytes)
	baseURL := "http://" + listener.Addr().String()
	announcement := map[string]any{
		"type":            "ready",
		"url":             baseURL + "/mcp",
		"healthUrl":       baseURL + "/healthz",
		"version":         "0.1.0",
		"launchContract":  1,
		"instanceId":      instanceID,
		"pid":             os.Getpid(),
		"stateDir":        *stateDirectory,
		"connectionsFile": *connectionsFile,
		"tools":           []string{"fake_echo"},
		"warnings":        []any{},
	}
	digest, err := connectionsConfigDigest(connections)
	if err != nil {
		return err
	}
	announcement["connectionsConfigDigest"] = digest
	line, err := json.Marshal(announcement)
	if err != nil {
		return err
	}
	serverFile := filepath.Join(*stateDirectory, "server.json")
	if err := os.WriteFile(serverFile, line, 0o600); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, request *http.Request) {
		if !isLoopbackHost(request.Host) {
			http.Error(response, "host not allowed", http.StatusForbidden)
			return
		}
		writeJSON(response, map[string]any{
			"status": "ok", "version": "0.1.0", "launchContract": 1, "instanceId": instanceID, "toolCount": 1,
		})
	})
	crossOriginProtection := http.NewCrossOriginProtection()
	mux.HandleFunc("/mcp", func(response http.ResponseWriter, request *http.Request) {
		if !isLoopbackHost(request.Host) {
			http.Error(response, "host not allowed", http.StatusForbidden)
			return
		}
		if err := crossOriginProtection.Check(request); err != nil {
			http.Error(response, "cross-origin request refused", http.StatusForbidden)
			return
		}
		if request.Method != http.MethodPost {
			http.Error(response, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var call struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&call); err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(response, map[string]any{
			"jsonrpc": "2.0",
			"id":      call.ID,
			"result": map[string]any{
				"instanceId": instanceID, "pid": os.Getpid(), "method": call.Method, "host": request.Host,
				"connections": connections,
			},
		})
	})
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()
	fmt.Println(string(line))
	fmt.Fprintln(os.Stderr, "fake connectors MCP server ready")
	stop := make(chan string, 2)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	go func() { stop <- "signal " + (<-signals).String() }()
	if *exitOnStdinClose {
		go func() {
			if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			stop <- "stdin closed"
		}()
	}
	fmt.Fprintln(os.Stderr, "fake connectors MCP server stopping:", <-stop)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return errors.Join(httpServer.Shutdown(shutdownCtx), os.Remove(serverFile))
}

// connectionsConfigDigest returns nil without a selection, else the sha256 of the canonical selection JSON.
func connectionsConfigDigest(connections []string) (any, error) {
	if len(connections) == 0 {
		return nil, nil
	}
	items := make([]map[string]any, 0, len(connections))
	for _, entry := range connections {
		connection, toolList, hasTools := strings.Cut(entry, "=")
		item := map[string]any{"connection": connection, "tools": nil}
		if hasTools {
			tools := slices.Compact(slices.Sorted(slices.Values(strings.Split(toolList, ","))))
			item["tools"] = tools
		}
		items = append(items, item)
	}
	slices.SortFunc(items, func(left map[string]any, right map[string]any) int {
		return strings.Compare(left["connection"].(string), right["connection"].(string))
	})
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"connections": items}); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(canonical.Bytes(), []byte("\n")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func recordStart(arguments []string) {
	directory := os.Getenv("FAKE_CONNECTORS_MCP_RECORD_DIR")
	if directory == "" {
		return
	}
	record, err := json.Marshal(map[string]any{"pid": os.Getpid(), "args": arguments})
	if err != nil {
		panic(err)
	}
	file, err := os.OpenFile(filepath.Join(directory, "starts.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	if _, err := file.Write(append(record, '\n')); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
}

func isLoopbackHost(hostHeader string) bool {
	host := hostHeader
	if splitHost, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = splitHost
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
