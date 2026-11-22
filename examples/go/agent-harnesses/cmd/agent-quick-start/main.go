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

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS.

// Command agent-quick-start runs one turn of a quick start agent session from the command line.
//
//	go run ./cmd/agent-quick-start eino "Cancel order 1002"
//	go run ./cmd/agent-quick-start eino --session-id ID
//
// The second form resumes a session, for example after you stopped this
// program while a tool call waited for your approval.
//
// Pass --approve to approve each pending call without asking, as a coding
// agent does. Without a terminal and without --approve, the program leaves the
// call pending in Dex and exits, which is the same as stopping it at the prompt.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
	"golang.org/x/term"

	"github.com/superdurable/dex/examples/go/agent-harnesses/harnessconfig"
	"github.com/superdurable/dex/examples/go/agent-harnesses/orderdesk"
	"github.com/superdurable/dex/examples/go/agent-harnesses/quickstart"
)

// The time a running tool call gets to finish after Ctrl+C.
const workerDrainTimeout = 30 * time.Second

var frameworks = []string{"eino"}

// approvalLeftPendingError means no one can answer the prompt, so the call keeps waiting in Dex.
type approvalLeftPendingError struct {
	interrupt dexagents.AgentInterrupt
}

func (pending *approvalLeftPendingError) Error() string {
	return pending.interrupt.ToolName + " waits for approval"
}

type commandLine struct {
	framework  string
	message    *string
	sessionID  string
	isApproved bool
}

func main() {
	arguments, err := parseCommandLine(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, arguments); err != nil {
		if ctx.Err() != nil {
			fmt.Println("\nStopped. The session waits in Dex; run again with --session-id to resume.")
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments commandLine) (err error) {
	config, err := harnessconfig.ConfigFromEnvironment()
	if err != nil {
		return err
	}
	chatModel, err := harnessconfig.NewEinoChatModel(ctx, config.EinoModel)
	if err != nil {
		return err
	}
	sessionFlow, err := quickstart.NewDurableEinoAgent(orderdesk.NewOrderDesk(config.OrderDeskToolDelay), chatModel)
	if err != nil {
		return err
	}
	app, err := quickstart.StartQuickStartApp(ctx, sessionFlow, config.FlowServiceAddress, config.BlobCacheDir)
	if err != nil {
		return err
	}
	defer func() {
		// A running tool call finishes first, as Ctrl+C promises.
		drainContext, cancel := context.WithTimeout(context.Background(), workerDrainTimeout)
		defer cancel()
		err = errors.Join(err, app.Stop(drainContext))
	}()

	sessionID := arguments.sessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("%s-quick-start-%s", arguments.framework, randomHex())
		if _, err := app.Sessions.StartSession(ctx, sessionID, nil, dex.StartFlowOptions{}); err != nil {
			return err
		}
	}
	fmt.Println("Session " + sessionID)
	decideApproval := askForApproval
	if arguments.isApproved {
		decideApproval = approveWithoutAsking
	}
	replies, err := quickstart.RunTurn(ctx, app.Sessions, sessionID, arguments.message, decideApproval)
	var pending *approvalLeftPendingError
	if errors.As(err, &pending) {
		fmt.Printf("%s waits for approval. Run again with --session-id %s --approve to approve it.\n",
			pending.interrupt.ToolName, sessionID)
		return nil
	}
	if err != nil {
		return err
	}
	for _, reply := range replies {
		fmt.Println("Agent: " + reply)
	}
	return nil
}

func askForApproval(ctx context.Context, interrupt dexagents.AgentInterrupt) (bool, error) {
	if !isTerminal(os.Stdin) {
		return false, &approvalLeftPendingError{interrupt: interrupt}
	}
	fmt.Printf("%s wants to run with %s. Approve? [y/N] ", interrupt.ToolName, interrupt.Arguments)
	answers := make(chan string, 1)
	go func() {
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		answers <- answer
	}()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case answer := <-answers:
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes", nil
	}
}

func approveWithoutAsking(_ context.Context, interrupt dexagents.AgentInterrupt) (bool, error) {
	fmt.Printf("Approved %s with %s.\n", interrupt.ToolName, interrupt.Arguments)
	return true, nil
}

// parseCommandLine accepts the flags before, between, or after the framework and the message.
func parseCommandLine(rawArguments []string) (commandLine, error) {
	flags := flag.NewFlagSet("agent-quick-start", flag.ContinueOnError)
	sessionID := flags.String("session-id", "", "resume this session instead of starting one")
	isApproved := flags.Bool("approve", false, "approve each pending call without asking")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "usage: agent-quick-start {%s} [message] [--session-id ID] [--approve]\n",
			strings.Join(frameworks, ","))
		flags.PrintDefaults()
	}
	var positional []string
	for {
		if err := flags.Parse(rawArguments); err != nil {
			return commandLine{}, err
		}
		rawArguments = flags.Args()
		if len(rawArguments) == 0 {
			break
		}
		positional = append(positional, rawArguments[0])
		rawArguments = rawArguments[1:]
	}
	if len(positional) == 0 || len(positional) > 2 {
		flags.Usage()
		return commandLine{}, errors.New("pass a framework, then a message or --session-id")
	}
	arguments := commandLine{framework: positional[0], sessionID: *sessionID, isApproved: *isApproved}
	if !slices.Contains(frameworks, arguments.framework) {
		return commandLine{}, fmt.Errorf("unknown framework %q; use one of %s", arguments.framework, strings.Join(frameworks, ", "))
	}
	if len(positional) == 2 {
		arguments.message = &positional[1]
	}
	if arguments.message == nil && arguments.sessionID == "" {
		return commandLine{}, errors.New("pass a message, or --session-id to resume a session")
	}
	return arguments, nil
}

func isTerminal(file *os.File) bool {
	return term.IsTerminal(int(file.Fd()))
}

func randomHex() string {
	buffer := make([]byte, 4)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}
