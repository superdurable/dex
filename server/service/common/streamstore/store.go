// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package streamstore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/log"
	"google.golang.org/protobuf/proto"
)

var (
	ErrDisabled           = errors.New("Stream Store is disabled")
	ErrMessageTooLarge    = errors.New("serialized Stream Value exceeds configured maxMessageBytes")
	ErrCapacityExceeded   = errors.New("Stream capacity is exhausted; retry after background trimming")
	ErrWaitTimeout        = errors.New("Stream read timed out")
	ErrInvalidResumeToken = errors.New("invalid Stream resume token")
	ErrUnavailable        = errors.New("Stream Redis is unavailable")
)

const resumeTokenVersion = 1

type WriteInput struct {
	FlowID               string
	FlowType             string
	StreamName           string
	MaxEstimatedBytes    int64
	Value                *dexpb.Value
	InternalIdentity     string
	PublicIdempotencyKey string
}

type Message struct {
	Value          *dexpb.Value
	RedisID        string
	CreatedTime    time.Time
	IdempotencyKey string
}

type Store struct {
	cfg         *config.StreamStoreConfig
	client      *redis.Client
	coordinator *trimCoordinator
}

type resumeToken struct {
	Version    int    `json:"v"`
	FlowID     string `json:"f"`
	FlowType   string `json:"t"`
	StreamName string `json:"s"`
	RedisID    string `json:"i"`
}

func New(cfg *config.StreamStoreConfig, logger log.Logger) (*Store, error) {
	if cfg == nil {
		panic("Stream Store config must not be nil")
	}
	if logger == nil {
		panic("Stream Store logger must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	store := &Store{cfg: cfg}
	if cfg.RedisURL == "" {
		return store, nil
	}
	options, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse Stream Store Redis URL: %w", err)
	}
	store.client = redis.NewClient(options)
	store.coordinator = newTrimCoordinator(cfg, store.client, logger)
	return store, nil
}

func (s *Store) Close() error {
	if s.coordinator != nil {
		s.coordinator.Close()
	}
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *Store) Write(ctx context.Context, input WriteInput) error {
	if s.client == nil {
		return ErrDisabled
	}
	payload, err := proto.Marshal(input.Value)
	if err != nil {
		return fmt.Errorf("marshal Stream Value: %w", err)
	}
	if int64(len(payload)) > s.cfg.EffectiveMaxMessageBytes() {
		return ErrMessageTooLarge
	}
	chargedBytes, err := s.chargedBytes(input, len(payload))
	if err != nil {
		return err
	}
	trimTrigger := percentageOf(input.MaxEstimatedBytes, s.cfg.EffectiveTrimTriggerPercent())
	baseTrimTarget := percentageOf(input.MaxEstimatedBytes, s.cfg.EffectiveTrimTargetPercent())
	messageTrimTarget := baseTrimTarget
	if messageTrimTarget < chargedBytes {
		messageTrimTarget = chargedBytes
	}
	scriptOutput, err := runWriteScript(ctx, s.client, writeScriptInput{
		keys:                   streamKeys(input.FlowType, input.StreamName, input.FlowID),
		internalIdentity:       input.InternalIdentity,
		publicIdempotencyKey:   input.PublicIdempotencyKey,
		payload:                payload,
		chargedBytes:           chargedBytes,
		capacityBytes:          input.MaxEstimatedBytes,
		trimTriggerBytes:       trimTrigger,
		baseTrimTargetBytes:    baseTrimTarget,
		messageTrimTargetBytes: messageTrimTarget,
	})
	if err != nil {
		return err
	}
	if scriptOutput.needsTrim {
		s.coordinator.Schedule(input.FlowType, input.StreamName, scriptOutput.trimTargetBytes)
	}
	switch scriptOutput.status {
	case writeScriptStatusSucceeded:
		return nil
	case writeScriptStatusCapacityExceeded:
		return ErrCapacityExceeded
	default:
		panic("validated Stream write status was not handled")
	}
}

func (s *Store) Read(
	ctx context.Context,
	flowType string,
	flowID string,
	streamName string,
	encodedResumeToken string,
) (*Message, error) {
	if s.client == nil {
		return nil, ErrDisabled
	}
	redisID, err := decodeResumeToken(encodedResumeToken, flowType, flowID, streamName)
	if err != nil {
		return nil, err
	}
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		panic("Stream read context requires a deadline")
	}
	blockDuration := time.Until(deadline)
	if blockDuration <= 0 {
		return nil, ErrWaitTimeout
	}
	result, err := s.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{streamKeys(flowType, streamName, flowID).instance, redisID},
		Count:   1,
		Block:   blockDuration,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) || errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrWaitTimeout
		}
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: read message: %v", ErrUnavailable, err)
	}
	if len(result) != 1 || len(result[0].Messages) != 1 {
		return nil, fmt.Errorf("unexpected Redis XREAD result")
	}
	redisMessage := result[0].Messages[0]
	payload, err := redisFieldBytes(redisMessage.Values, "v")
	if err != nil {
		return nil, err
	}
	publicKey, err := redisFieldString(redisMessage.Values, "k")
	if err != nil {
		return nil, err
	}
	value := &dexpb.Value{}
	if err := proto.Unmarshal(payload, value); err != nil {
		return nil, fmt.Errorf("unmarshal retained Stream Value: %w", err)
	}
	createdTime, err := createdTimeFromRedisID(redisMessage.ID)
	if err != nil {
		return nil, err
	}
	return &Message{
		Value:          value,
		RedisID:        redisMessage.ID,
		CreatedTime:    createdTime,
		IdempotencyKey: publicKey,
	}, nil
}

func EncodeResumeToken(flowType string, flowID string, streamName string, redisID string) (string, error) {
	if _, err := createdTimeFromRedisID(redisID); err != nil {
		return "", err
	}
	payload, err := json.Marshal(resumeToken{
		Version:    resumeTokenVersion,
		FlowID:     flowID,
		FlowType:   flowType,
		StreamName: streamName,
		RedisID:    redisID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal Stream resume token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func (s *Store) chargedBytes(input WriteInput, payloadBytes int) (int64, error) {
	parts := []int64{
		int64(payloadBytes),
		int64(len(input.FlowID)),
		int64(len(input.InternalIdentity)),
		int64(len(input.PublicIdempotencyKey)),
		s.cfg.EffectiveEstimatedMessageOverheadBytes(),
	}
	var total int64
	for _, part := range parts {
		if part > math.MaxInt64-total {
			return 0, ErrMessageTooLarge
		}
		total += part
	}
	return total, nil
}

func percentageOf(capacity int64, percent int32) int64 {
	percentage := int64(percent)
	return (capacity/100)*percentage + (capacity%100)*percentage/100
}

func decodeResumeToken(encoded string, flowType string, flowID string, streamName string) (string, error) {
	if encoded == "" {
		return "0-0", nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrInvalidResumeToken
	}
	var token resumeToken
	if err := json.Unmarshal(payload, &token); err != nil {
		return "", ErrInvalidResumeToken
	}
	if token.Version != resumeTokenVersion ||
		token.FlowID != flowID ||
		token.FlowType != flowType ||
		token.StreamName != streamName {
		return "", ErrInvalidResumeToken
	}
	if _, err := createdTimeFromRedisID(token.RedisID); err != nil {
		return "", ErrInvalidResumeToken
	}
	return token.RedisID, nil
}

func createdTimeFromRedisID(redisID string) (time.Time, error) {
	parts := strings.Split(redisID, "-")
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid Redis Stream ID")
	}
	milliseconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || milliseconds < 0 {
		return time.Time{}, fmt.Errorf("invalid Redis Stream ID")
	}
	if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
		return time.Time{}, fmt.Errorf("invalid Redis Stream ID")
	}
	return time.UnixMilli(milliseconds), nil
}

type redisKeys struct {
	fifo         string
	chargedBytes string
	idempotency  string
	instance     string
	lease        string
}

func streamKeys(flowType string, streamName string, flowID string) redisKeys {
	streamScope := fmt.Sprintf("%d:%s%d:%s", len(flowType), flowType, len(streamName), streamName)
	streamDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(streamScope)))
	base := "dex:stream:v1:" + streamDigest
	flowDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(flowID)))
	return redisKeys{
		fifo:         base + ":fifo",
		chargedBytes: base + ":charged",
		idempotency:  base + ":idem",
		instance:     base + ":instance:" + flowDigest,
		lease:        base + ":trim-lease",
	}
}

func redisFieldBytes(values map[string]any, field string) ([]byte, error) {
	value, ok := values[field]
	if !ok {
		return nil, fmt.Errorf("retained Stream message missing %s", field)
	}
	switch typed := value.(type) {
	case string:
		return []byte(typed), nil
	case []byte:
		return typed, nil
	default:
		return nil, fmt.Errorf("retained Stream field %s has type %T", field, value)
	}
}

func redisFieldString(values map[string]any, field string) (string, error) {
	value, err := redisFieldBytes(values, field)
	if err != nil {
		return "", err
	}
	return string(value), nil
}
