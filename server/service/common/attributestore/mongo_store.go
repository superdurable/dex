// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package attributestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/log/tag"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const mongoDisconnectTimeout = 10 * time.Second

type mongoStore struct {
	client     *mongo.Client
	collection *mongo.Collection
	logger     log.Logger
	dsn        string
}

func openMongoStore(
	ctx context.Context,
	cfg config.AttributeStoreConfigEntry,
	logger log.Logger,
) (*mongoStore, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.DSN))
	if err != nil {
		return nil, fmt.Errorf("open connection: %w", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return nil, disconnectMongoOnError(client, fmt.Errorf("ping database: %w", err))
	}
	database := client.Database(cfg.DatabaseName)
	collectionNames, err := database.ListCollectionNames(ctx, bson.D{{Key: "name", Value: cfg.CollectionName}})
	if err != nil {
		return nil, disconnectMongoOnError(client, fmt.Errorf("list MongoDB collections: %w", err))
	}
	if len(collectionNames) != 1 || collectionNames[0] != cfg.CollectionName {
		return nil, disconnectMongoOnError(client, fmt.Errorf("MongoDB collection does not exist"))
	}
	return &mongoStore{
		client:     client,
		collection: database.Collection(cfg.CollectionName),
		logger:     logger,
		dsn:        cfg.DSN,
	}, nil
}

func disconnectMongoOnError(client *mongo.Client, sourceErr error) error {
	disconnectCtx, cancel := context.WithTimeout(context.Background(), mongoDisconnectTimeout)
	defer cancel()
	if disconnectErr := client.Disconnect(disconnectCtx); disconnectErr != nil {
		return errors.Join(sourceErr, disconnectErr)
	}
	return sourceErr
}

func (s *mongoStore) writeBatch(
	ctx context.Context,
	flowID string,
	items []*dexpb.AttributeSyncItem,
) error {
	latest := make(map[string]*dexpb.Value, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		latest[item.GetKey()] = item.GetValue()
	}
	updates := bson.M{}
	for name, value := range latest {
		if !isValidMongoAttributeName(name) {
			s.logger.Error("skip Attribute Store item: invalid MongoDB field", tag.AttributeName(name))
			continue
		}
		converted, err := convertMongoValue(value)
		if err != nil {
			s.logger.Error("skip incompatible Attribute Store item", tag.AttributeName(name), tag.Error(err))
			continue
		}
		updates[name] = converted
	}
	if len(updates) == 0 {
		return nil
	}
	_, err := s.collection.UpdateOne(
		ctx,
		bson.D{{Key: "_id", Value: flowID}},
		bson.D{{Key: "$set", Value: updates}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return redactStorageSecret(fmt.Errorf("execute Attribute Store upsert: %w", err), s.dsn)
	}
	return nil
}

func isValidMongoAttributeName(name string) bool {
	return name != "" && name != "_id" && !strings.HasPrefix(name, "$") &&
		!strings.Contains(name, ".") && strings.IndexByte(name, 0) < 0
}

func convertMongoValue(value *dexpb.Value) (any, error) {
	if value == nil || value.GetKind() == nil {
		return nil, fmt.Errorf("value is missing")
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_NullValue:
		return nil, nil
	case *dexpb.Value_StringValue:
		return kind.StringValue, nil
	case *dexpb.Value_IntValue:
		return kind.IntValue, nil
	case *dexpb.Value_DoubleValue:
		if math.IsNaN(kind.DoubleValue) || math.IsInf(kind.DoubleValue, 0) {
			return nil, fmt.Errorf("MongoDB cannot store non-finite double value")
		}
		return kind.DoubleValue, nil
	case *dexpb.Value_BoolValue:
		return kind.BoolValue, nil
	case *dexpb.Value_ObjValue:
		return convertMongoObject(kind.ObjValue)
	case *dexpb.Value_InternalBlobIdForStringValue, *dexpb.Value_InternalBlobIdForObjValue:
		return nil, fmt.Errorf("blob-backed value was not hydrated")
	default:
		return nil, fmt.Errorf("unsupported Attribute value")
	}
}

func convertMongoObject(object *dexpb.EncodedObject) (any, error) {
	if object == nil {
		return nil, fmt.Errorf("object is missing")
	}
	if object.GetEncoding() != "json" {
		return bson.Binary{Subtype: 0, Data: object.GetPayload()}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(object.GetPayload()))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("JSON payload is invalid: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("JSON payload contains multiple values")
		}
		return nil, fmt.Errorf("JSON payload is invalid: %w", err)
	}
	return convertMongoJSONValue(value)
}

func convertMongoJSONValue(value any) (any, error) {
	switch typed := value.(type) {
	case nil, string, bool:
		return typed, nil
	case json.Number:
		if !strings.ContainsAny(typed.String(), ".eE") {
			integer, err := strconv.ParseInt(typed.String(), 10, 64)
			if err == nil {
				return integer, nil
			}
		}
		double, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil || math.IsInf(double, 0) || math.IsNaN(double) {
			return nil, fmt.Errorf("JSON number is outside MongoDB numeric range")
		}
		return double, nil
	case []any:
		converted := make(bson.A, len(typed))
		for index, item := range typed {
			value, err := convertMongoJSONValue(item)
			if err != nil {
				return nil, err
			}
			converted[index] = value
		}
		return converted, nil
	case map[string]any:
		converted := bson.M{}
		for key, item := range typed {
			value, err := convertMongoJSONValue(item)
			if err != nil {
				return nil, err
			}
			converted[key] = value
		}
		return converted, nil
	default:
		return nil, fmt.Errorf("unsupported JSON value %T", value)
	}
}

func (s *mongoStore) close() error {
	disconnectCtx, cancel := context.WithTimeout(context.Background(), mongoDisconnectTimeout)
	defer cancel()
	return redactStorageSecret(s.client.Disconnect(disconnectCtx), s.dsn)
}
