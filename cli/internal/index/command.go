// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package index

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v3"
)

const defaultAddress = "localhost:8801"

type schemaFile struct {
	DefinitionVersion int32         `yaml:"definitionVersion"`
	Attributes        []schemaField `yaml:"attributes"`
}

type schemaField struct {
	Name             string `yaml:"name"`
	Type             string `yaml:"type"`
	VectorDimensions int32  `yaml:"vectorDimensions"`
	VectorMetric     string `yaml:"vectorMetric"`
}

func Execute(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) (retErr error) {
	if len(args) == 0 || args[0] != "apply" {
		return fmt.Errorf("usage: dexcli index apply --file <yaml> [--address <dex-server>]")
	}
	flags := flag.NewFlagSet("dexcli index apply", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var path string
	var address string
	flags.StringVar(&path, "file", "", "flow index schema YAML")
	flags.StringVar(&address, "address", defaultAddress, "Dex Server gRPC address")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("--file is required")
	}
	request, err := loadSchema(path)
	if err != nil {
		return err
	}
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to Dex Server: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, connection.Close())
	}()
	response, err := dexpb.NewAdminServiceClient(connection).ApplyFlowIndexSchema(ctx, request)
	if err != nil {
		return fmt.Errorf("apply flow index schema: %w", err)
	}
	fmt.Fprintf(stdout, "Flow index schema version %d", response.GetSchemaVersion())
	if response.GetChanged() {
		fmt.Fprintf(stdout, " applied; added: %s", strings.Join(response.GetAddedFields(), ", "))
	} else {
		fmt.Fprint(stdout, " is already applied")
	}
	fmt.Fprintln(stdout)
	return nil
}

func loadSchema(path string) (*dexpb.ApplyFlowIndexSchemaRequest, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read flow index schema: %w", err)
	}
	var definition schemaFile
	decoder := yaml.NewDecoder(strings.NewReader(string(payload)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&definition); err != nil {
		return nil, fmt.Errorf("decode flow index schema: %w", err)
	}
	request := &dexpb.ApplyFlowIndexSchemaRequest{DefinitionVersion: definition.DefinitionVersion}
	for _, field := range definition.Attributes {
		indexType, err := parseIndexType(field.Type)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field.Name, err)
		}
		metric, err := parseVectorMetric(field.VectorMetric)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field.Name, err)
		}
		request.Attributes = append(request.Attributes, &dexpb.FlowIndexField{
			Name:                 field.Name,
			Type:                 indexType,
			VectorDimensions:     field.VectorDimensions,
			VectorDistanceMetric: metric,
		})
	}
	return request, nil
}

func parseIndexType(value string) (dexpb.IndexType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "keyword":
		return dexpb.IndexType_INDEX_TYPE_KEYWORD, nil
	case "text":
		return dexpb.IndexType_INDEX_TYPE_TEXT, nil
	case "keyword_array":
		return dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY, nil
	case "int":
		return dexpb.IndexType_INDEX_TYPE_INT, nil
	case "double":
		return dexpb.IndexType_INDEX_TYPE_DOUBLE, nil
	case "bool":
		return dexpb.IndexType_INDEX_TYPE_BOOL, nil
	case "datetime":
		return dexpb.IndexType_INDEX_TYPE_DATETIME, nil
	case "vector":
		return dexpb.IndexType_INDEX_TYPE_VECTOR, nil
	default:
		return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED, fmt.Errorf("unsupported index type %q", value)
	}
}

func parseVectorMetric(value string) (dexpb.VectorDistanceMetric, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_UNSPECIFIED, nil
	case "l2":
		return dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_L2, nil
	case "cosine":
		return dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_COSINE, nil
	case "inner_product":
		return dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_INNER_PRODUCT, nil
	default:
		return dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_UNSPECIFIED, fmt.Errorf("unsupported vector metric %q", value)
	}
}
