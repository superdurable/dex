// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build attributestore_integration

package integ

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/internal/testconfig"
	"google.golang.org/protobuf/proto"
)

var (
	syncedSecretAttribute   = dex.DefineAttribute[string]("secret", dex.SyncToAttributeStore())
	syncedStatusAttribute   = dex.DefineAttribute[string]("status", dex.SyncToAttributeStore(), dex.Plaintext())
	syncedDocumentAttribute = dex.DefineAttribute[syncedDocument]("document", dex.SyncToAttributeStore(), dex.Plaintext())
)

type syncedDocument struct {
	Source string `json:"source"`
}

// encryptedAttributeSyncFlow has no Steps; StartFlow only writes its initial Attributes.
type encryptedAttributeSyncFlow struct {
	dex.FlowDefaults
}

func (encryptedAttributeSyncFlow) GetSteps() []dex.StepDef {
	return nil
}

func (encryptedAttributeSyncFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{
		syncedSecretAttribute, syncedStatusAttribute, syncedDocumentAttribute,
	}}
}

// TestAttributeSyncEncryptedPayloadsTemporal syncs an encrypted Attribute to a bytea column and
// Plaintext Attributes to text and jsonb columns.
func TestAttributeSyncEncryptedPayloadsTemporal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	postgresDSN := os.Getenv("DEX_ATTRIBUTE_STORE_POSTGRES_DSN")
	if postgresDSN == "" {
		postgresDSN = "postgres://dex:dex@127.0.0.1:55432/dex?sslmode=disable"
	}
	database, err := sql.Open("pgx", postgresDSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	tableName := "flow_attributes_encrypted_" + strings.ReplaceAll(newRequestID(), "-", "")
	_, err = database.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE public.%s (flow_id TEXT PRIMARY KEY, secret BYTEA, status TEXT, document JSONB)`,
		tableName,
	))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := database.ExecContext(context.Background(), "DROP TABLE IF EXISTS public."+tableName)
		require.NoError(t, dropErr)
	})
	runtime := startDexService(t, withAttributeSyncBlobCache(t, serverZDTestConfig{
		AttributeStore: testconfig.AttributeStoreConfig{
			Stores: map[string]testconfig.AttributeStoreConfigEntry{
				"reporting": {
					Type:      testconfig.AttributeStoreTypePostgres,
					DSN:       postgresDSN,
					TableName: "public." + tableName,
				},
			},
		},
	}))

	codec := newAttributeSyncPayloadCodec(t)
	client := newAttributeSyncPayloadClient(t, runtime.PublicAddress, codec)
	secret := "card-" + newRequestID()
	secretInitial, err := dex.InitialAttribute(syncedSecretAttribute, secret)
	require.NoError(t, err)
	statusInitial, err := dex.InitialAttribute(syncedStatusAttribute, "open")
	require.NoError(t, err)
	documentInitial, err := dex.InitialAttribute(syncedDocumentAttribute, syncedDocument{Source: "plaintext"})
	require.NoError(t, err)
	flowID := "attribute-sync-encrypted-" + newRequestID()
	_, err = client.StartFlow(ctx, encryptedAttributeSyncFlow{}, flowID, nil, dex.StartFlowOptions{
		Attributes:     []dex.InitialAttributeDef{secretInitial, statusInitial, documentInitial},
		ConfigOverride: &dex.FlowConfig{AttributeStoreNames: []string{"reporting"}},
	})
	require.NoError(t, err)

	var storedSecret []byte
	var storedStatus string
	var storedDocument []byte
	require.Eventually(t, func() bool {
		row := database.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT secret, status, document FROM public.%s WHERE flow_id = $1`, tableName,
		), flowID)
		var status sql.NullString
		if scanErr := row.Scan(&storedSecret, &status, &storedDocument); scanErr != nil {
			return false
		}
		storedStatus = status.String
		return len(storedSecret) > 0 && status.Valid && len(storedDocument) > 0
	}, 30*time.Second, 50*time.Millisecond)

	require.NotContains(t, string(storedSecret), secret)
	envelope := &dexpb.PayloadEnvelope{}
	require.NoError(t, proto.Unmarshal(storedSecret, envelope), "the bytea column holds the serialized envelope")
	plaintexts, err := codec.Decode(ctx, dex.PayloadContext{FlowID: flowID}, []*dexpb.PayloadEnvelope{envelope})
	require.NoError(t, err)
	decrypted := &dexpb.Value{}
	require.NoError(t, proto.Unmarshal(plaintexts[0], decrypted))
	require.Equal(t, secret, decrypted.GetStringValue())

	require.Equal(t, "open", storedStatus)
	var document syncedDocument
	require.NoError(t, json.Unmarshal(storedDocument, &document))
	require.Equal(t, syncedDocument{Source: "plaintext"}, document)
}

func newAttributeSyncPayloadCodec(t *testing.T) *dex.AESGCMPayloadCodec {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	content, err := json.Marshal(map[string]any{
		"current_key_id": "k1",
		"keys":           map[string]string{"k1": base64.StdEncoding.EncodeToString(key)},
	})
	require.NoError(t, err)
	keyFile := filepath.Join(t.TempDir(), "keys.json")
	require.NoError(t, os.WriteFile(keyFile, content, 0o600))
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFile)
	require.NoError(t, err)
	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
	require.NoError(t, err)
	return codec
}

func newAttributeSyncPayloadClient(t *testing.T, address string, codec dex.PayloadCodec) *dex.Client {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{encryptedAttributeSyncFlow{}})
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 16 << 20})
	require.NoError(t, err)
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: address,
		PayloadEncryption:  dex.PayloadEncryptionOptions{Codec: codec},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, errors.Join(client.Close(), cache.Close()))
	})
	return client
}
