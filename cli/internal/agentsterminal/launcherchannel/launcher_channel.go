// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package launcherchannel is dexcli's end of the terminal UI channel: the
// launch line, ready, local requests with their recovery, and the launcher's
// notifications.
package launcherchannel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
)

// ErrChannelEnded reports that the launcher closed the channel.
var ErrChannelEnded = errors.New("the launcher closed the terminal UI channel")

// ErrProtocol reports a malformed line from the launcher, which ends the
// contract.
var ErrProtocol = errors.New("the launcher broke the terminal UI contract")

// maxRequestsInFlight is the contract's limit of waiting requests other than
// get_request_result.
const maxRequestsInFlight = 4

// writeTimeout bounds one channel write, so a launcher that stops reading
// cannot block dexcli.
const writeTimeout = 5 * time.Second

// RequestTimeouts are how long dexcli waits during requests.
type RequestTimeouts struct {
	// MethodWaits maps each method to its wait before cancel; methods
	// without an entry use 15 s.
	MethodWaits map[string]time.Duration
	// CancelAnswer bounds the wait for the response after a cancel.
	CancelAnswer time.Duration
	// Recovery bounds get_request_result polling after the original request.
	Recovery time.Duration
	// RunningPoll is the pause after each running answer.
	RunningPoll time.Duration
}

// ContractRequestTimeouts returns the waits contract 1 sets.
func ContractRequestTimeouts() RequestTimeouts {
	return RequestTimeouts{
		MethodWaits: map[string]time.Duration{
			launchcontract.MethodPutImage:         30 * time.Second,
			launchcontract.MethodStartSession:     30 * time.Second,
			launchcontract.MethodListConnectors:   15 * time.Second,
			launchcontract.MethodOpenURL:          5 * time.Second,
			launchcontract.MethodGetRequestResult: 5 * time.Second,
		},
		CancelAnswer: 5 * time.Second,
		Recovery:     60 * time.Second,
		RunningPoll:  time.Second,
	}
}

func (timeouts RequestTimeouts) methodWait(method string) time.Duration {
	if wait, exists := timeouts.MethodWaits[method]; exists {
		return wait
	}
	return 15 * time.Second
}

// Notification is one notice or Worker state from the launcher.
type Notification struct {
	// Notice is set for a notice message.
	Notice *launchcontract.NoticeMessage
	// Worker is set for a worker message.
	Worker *launchcontract.WorkerMessage
}

// LauncherChannel is dexcli's end of the channel. It is safe for concurrent
// use after Start.
type LauncherChannel struct {
	connection net.Conn
	reader     *bufio.Reader
	timeouts   RequestTimeouts

	writeMutex sync.Mutex

	mutex                 sync.Mutex
	nextRequestID         int64
	waiting               map[int64]chan launchcontract.ResponseMessage
	inFlightCount         int
	methods               []string
	isGetRequestResultOff bool
	endErr                error

	notifications chan Notification
	ended         chan struct{}
	endOnce       sync.Once
	readySent     chan struct{}
	readyOnce     sync.Once
}

// NewLauncherChannel wraps the channel socket. It reads nothing until
// ReadLaunchLine.
func NewLauncherChannel(connection net.Conn, timeouts RequestTimeouts) *LauncherChannel {
	if connection == nil {
		panic("launcherchannel: NewLauncherChannel needs a connection")
	}
	return &LauncherChannel{
		connection:    connection,
		reader:        bufio.NewReaderSize(connection, 64*1024),
		timeouts:      timeouts,
		waiting:       map[int64]chan launchcontract.ResponseMessage{},
		notifications: make(chan Notification, 64),
		ended:         make(chan struct{}),
		readySent:     make(chan struct{}),
	}
}

// ReadLaunchLine reads the first channel line without its newline.
//
// A line longer than 1 MiB comes back cut to one byte over the limit, so the
// launch checks reject it. It returns an error when the channel ends before a
// newline.
func (channel *LauncherChannel) ReadLaunchLine() ([]byte, error) {
	line, isTooLong, err := channel.readLine()
	if isTooLong {
		return bytes.Repeat([]byte{' '}, launchcontract.MaxLineBytes+1), nil
	}
	return line, err
}

// readLine reads one line. isTooLong reports a line over 1 MiB, whose rest
// was discarded.
func (channel *LauncherChannel) readLine() (line []byte, isTooLong bool, err error) {
	var buffer []byte
	for {
		fragment, readErr := channel.reader.ReadSlice('\n')
		if len(buffer)+len(fragment) > launchcontract.MaxLineBytes+1 {
			isTooLong = true
		} else {
			buffer = append(buffer, fragment...)
		}
		switch {
		case readErr == nil:
			if isTooLong {
				return nil, true, nil
			}
			return buffer[:len(buffer)-1], false, nil
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		default:
			return nil, isTooLong, readErr
		}
	}
}

// SendReady tells the launcher the terminal UI is usable. Requests wait for
// it, because ready is the first message the launcher reads.
func (channel *LauncherChannel) SendReady() error {
	err := channel.writeMessage(map[string]any{"type": launchcontract.MessageTypeReady})
	channel.readyOnce.Do(func() { close(channel.readySent) })
	return err
}

// SendLaunchRejected tells the launcher why the launch failed.
func (channel *LauncherChannel) SendLaunchRejected(rejection *launchcontract.LaunchRejection) error {
	return channel.writeMessage(map[string]any{
		"type": launchcontract.MessageTypeLaunchRejected, "code": rejection.Code, "message": rejection.Message,
	})
}

// Start begins reading the launcher's messages. methods are the launch
// document's methods.
func (channel *LauncherChannel) Start(methods []string) {
	channel.mutex.Lock()
	channel.methods = slices.Clone(methods)
	channel.mutex.Unlock()
	go channel.readMessages()
}

// Notifications returns the launcher's notices and Worker states. It closes
// when the channel ends.
func (channel *LauncherChannel) Notifications() <-chan Notification {
	return channel.notifications
}

// Ended closes when the channel ends.
func (channel *LauncherChannel) Ended() <-chan struct{} {
	return channel.ended
}

// EndErr returns ErrChannelEnded or ErrProtocol after Ended closes, or nil.
func (channel *LauncherChannel) EndErr() error {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	return channel.endErr
}

// Close closes the channel, which tells the launcher the terminal UI ended.
func (channel *LauncherChannel) Close() error {
	channel.end(ErrChannelEnded)
	if err := channel.connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("close the terminal UI channel: %w", err)
	}
	return nil
}

// HasMethod reports whether the launcher serves method.
func (channel *LauncherChannel) HasMethod(method string) bool {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	return slices.Contains(channel.methods, method)
}

// DisableMethod stops calling method for the rest of the process, after
// unknown_method.
func (channel *LauncherChannel) DisableMethod(method string) {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	channel.methods = slices.DeleteFunc(channel.methods, func(listed string) bool { return listed == method })
}

func (channel *LauncherChannel) readMessages() {
	defer close(channel.notifications)
	for {
		line, isTooLong, err := channel.readLine()
		if err != nil {
			channel.end(ErrChannelEnded)
			return
		}
		if isTooLong {
			channel.endWithProtocolError()
			return
		}
		object, err := launchcontract.DecodeChannelLine(line)
		if err != nil {
			channel.endWithProtocolError()
			return
		}
		message, err := launchcontract.ParseLauncherMessage(object)
		if err != nil {
			channel.endWithProtocolError()
			return
		}
		switch {
		case message.Response != nil:
			channel.deliverResponse(*message.Response)
		case message.Notice != nil:
			if !channel.notify(Notification{Notice: message.Notice}) {
				return
			}
		case message.Worker != nil:
			if !channel.notify(Notification{Worker: message.Worker}) {
				return
			}
		}
	}
}

func (channel *LauncherChannel) notify(notification Notification) bool {
	select {
	case channel.notifications <- notification:
		return true
	case <-channel.ended:
		return false
	}
}

func (channel *LauncherChannel) endWithProtocolError() {
	channel.end(ErrProtocol)
	// The contract ends: closing makes the launcher read EOF.
	if err := channel.connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		channel.end(fmt.Errorf("close the channel after a protocol error: %w", err))
	}
}

func (channel *LauncherChannel) end(reason error) {
	channel.endOnce.Do(func() {
		channel.mutex.Lock()
		channel.endErr = reason
		channel.mutex.Unlock()
		close(channel.ended)
	})
}

func (channel *LauncherChannel) deliverResponse(response launchcontract.ResponseMessage) {
	channel.mutex.Lock()
	waiter, exists := channel.waiting[response.ID]
	delete(channel.waiting, response.ID)
	channel.mutex.Unlock()
	if exists {
		waiter <- response
	}
}

func (channel *LauncherChannel) writeMessage(message map[string]any) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode the %v message: %w", message["type"], err)
	}
	channel.writeMutex.Lock()
	defer channel.writeMutex.Unlock()
	if err := channel.connection.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return fmt.Errorf("set the channel write deadline: %w", err)
	}
	if _, err := channel.connection.Write(append(encoded, '\n')); err != nil {
		if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
			return ErrChannelEnded
		}
		return fmt.Errorf("write the %v message: %w", message["type"], err)
	}
	return nil
}

// OutcomeKind is how a local request ended.
type OutcomeKind int

const (
	// OutcomeDone means the request returned Result.
	OutcomeDone OutcomeKind = iota
	// OutcomeFailed means the request returned Error.
	OutcomeFailed
	// OutcomeUnknown means the request may or may not have run.
	OutcomeUnknown
	// OutcomeHostStopped means the Worker host is gone.
	OutcomeHostStopped
	// OutcomeChannelEnded means the channel ended before an answer.
	OutcomeChannelEnded
)

// RequestOutcome is the end of one local request.
type RequestOutcome struct {
	// Kind is how the request ended.
	Kind OutcomeKind
	// Result is the result object with OutcomeDone.
	Result json.RawMessage
	// Error is the error with OutcomeFailed.
	Error *launchcontract.ChannelError
}

// Call sends one request with key and waits for its answer, once.
//
// When the method's wait ends, it sends cancel and takes whatever response
// follows within CancelAnswer: a result that raced the cancel still counts.
func (channel *LauncherChannel) Call(ctx context.Context, method string, key string, params any) RequestOutcome {
	select {
	case <-channel.readySent:
	case <-channel.ended:
		return RequestOutcome{Kind: OutcomeChannelEnded}
	case <-ctx.Done():
		return RequestOutcome{Kind: OutcomeUnknown}
	}
	isCounted := method != launchcontract.MethodGetRequestResult
	requestID, waiter, outcome, isSent := channel.register(isCounted)
	if !isSent {
		return outcome
	}
	defer channel.unregister(requestID, isCounted)
	message := map[string]any{"type": launchcontract.MessageTypeRequest, "id": requestID, "method": method, "params": params}
	if key != "" {
		message["idempotency_key"] = key
	}
	if err := channel.writeMessage(message); err != nil {
		return RequestOutcome{Kind: OutcomeChannelEnded}
	}
	timer := time.NewTimer(channel.timeouts.methodWait(method))
	defer timer.Stop()
	select {
	case response := <-waiter:
		return classifyResponse(response)
	case <-channel.ended:
		return RequestOutcome{Kind: OutcomeChannelEnded}
	case <-ctx.Done():
		if err := channel.sendCancel(requestID); err != nil {
			return RequestOutcome{Kind: OutcomeChannelEnded}
		}
		return RequestOutcome{Kind: OutcomeUnknown}
	case <-timer.C:
	}
	if err := channel.sendCancel(requestID); err != nil {
		return RequestOutcome{Kind: OutcomeChannelEnded}
	}
	cancelTimer := time.NewTimer(channel.timeouts.CancelAnswer)
	defer cancelTimer.Stop()
	select {
	case response := <-waiter:
		return classifyResponse(response)
	case <-channel.ended:
		return RequestOutcome{Kind: OutcomeChannelEnded}
	case <-ctx.Done():
	case <-cancelTimer.C:
	}
	return RequestOutcome{Kind: OutcomeUnknown}
}

func (channel *LauncherChannel) register(isCounted bool) (int64, chan launchcontract.ResponseMessage, RequestOutcome, bool) {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	if channel.endErr != nil {
		return 0, nil, RequestOutcome{Kind: OutcomeChannelEnded}, false
	}
	if isCounted && channel.inFlightCount >= maxRequestsInFlight {
		busy := &launchcontract.ChannelError{Code: launchcontract.ErrorCodeBusy, Message: "Too many local requests wait for an answer."}
		return 0, nil, RequestOutcome{Kind: OutcomeFailed, Error: busy}, false
	}
	channel.nextRequestID++
	waiter := make(chan launchcontract.ResponseMessage, 1)
	channel.waiting[channel.nextRequestID] = waiter
	if isCounted {
		channel.inFlightCount++
	}
	return channel.nextRequestID, waiter, RequestOutcome{}, true
}

func (channel *LauncherChannel) unregister(requestID int64, isCounted bool) {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	delete(channel.waiting, requestID)
	if isCounted {
		channel.inFlightCount--
	}
}

func (channel *LauncherChannel) sendCancel(requestID int64) error {
	return channel.writeMessage(map[string]any{"type": launchcontract.MessageTypeCancel, "id": requestID})
}

func classifyResponse(response launchcontract.ResponseMessage) RequestOutcome {
	if response.Error == nil {
		return RequestOutcome{Kind: OutcomeDone, Result: response.Result}
	}
	switch response.Error.Code {
	case launchcontract.ErrorCodeCancelled, launchcontract.ErrorCodeTimeout:
		return RequestOutcome{Kind: OutcomeUnknown, Error: response.Error}
	case launchcontract.ErrorCodeHostStopped:
		return RequestOutcome{Kind: OutcomeHostStopped, Error: response.Error}
	}
	return RequestOutcome{Kind: OutcomeFailed, Error: response.Error}
}

// CallWithRecoveryKey runs a request whose outcome must be learned, such as
// put_image or start_session, with the contract's recovery. key is a new
// idempotency key for this user action.
//
// After an unknown outcome it asks get_request_result with the same key,
// once a second while the answer is running and for at most Recovery after
// the first send, then resends the request once with the same key and
// params. onStillRunning, when not nil, runs at the first running answer. It
// never resends after host_stopped or the end of the channel.
func (channel *LauncherChannel) CallWithRecoveryKey(ctx context.Context, method string, key string, params any, onStillRunning func()) RequestOutcome {
	sentAt := time.Now()
	outcome := channel.Call(ctx, method, key, params)
	if outcome.Kind != OutcomeUnknown || ctx.Err() != nil {
		return outcome
	}
	recovered, shouldResend := channel.recoverOutcome(ctx, key, sentAt, onStillRunning)
	if !shouldResend {
		return recovered
	}
	return channel.Call(ctx, method, key, params)
}

// recoverOutcome asks get_request_result. It returns the outcome, or
// shouldResend for step 2 of the contract's recovery.
func (channel *LauncherChannel) recoverOutcome(ctx context.Context, key string, sentAt time.Time, onStillRunning func()) (RequestOutcome, bool) {
	isRunningReported := false
	for channel.isGetRequestResultUsable() {
		answer := channel.Call(ctx, launchcontract.MethodGetRequestResult, "", map[string]any{"idempotency_key": key})
		switch answer.Kind {
		case OutcomeHostStopped, OutcomeChannelEnded:
			return answer, false
		case OutcomeUnknown:
			return RequestOutcome{Kind: OutcomeUnknown}, true
		case OutcomeFailed:
			if answer.Error.Code == launchcontract.ErrorCodeUnknownMethod {
				channel.turnOffGetRequestResult()
			}
			return RequestOutcome{Kind: OutcomeUnknown}, true
		}
		var stored struct {
			State  string                       `json:"state"`
			Result json.RawMessage              `json:"result"`
			Error  *launchcontract.ChannelError `json:"error"`
		}
		if err := json.Unmarshal(answer.Result, &stored); err != nil {
			return RequestOutcome{Kind: OutcomeUnknown}, true
		}
		switch stored.State {
		case "done":
			if stored.Error != nil {
				return classifyResponse(launchcontract.ResponseMessage{Error: stored.Error}), false
			}
			return RequestOutcome{Kind: OutcomeDone, Result: stored.Result}, false
		case "running":
			if !isRunningReported && onStillRunning != nil {
				onStillRunning()
			}
			isRunningReported = true
			if time.Since(sentAt) >= channel.timeouts.Recovery {
				return RequestOutcome{Kind: OutcomeUnknown}, false
			}
			if !sleepContext(ctx, channel.timeouts.RunningPoll, channel.ended) {
				return RequestOutcome{Kind: OutcomeUnknown}, false
			}
		default:
			return RequestOutcome{Kind: OutcomeUnknown}, true
		}
	}
	return RequestOutcome{Kind: OutcomeUnknown}, true
}

func (channel *LauncherChannel) isGetRequestResultUsable() bool {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	return !channel.isGetRequestResultOff && slices.Contains(channel.methods, launchcontract.MethodGetRequestResult)
}

func (channel *LauncherChannel) turnOffGetRequestResult() {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	channel.isGetRequestResultOff = true
}

func sleepContext(ctx context.Context, delay time.Duration, ended <-chan struct{}) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	case <-ended:
		return false
	}
}
