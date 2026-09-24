// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package temporal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	cloudservice "go.temporal.io/cloud-sdk/api/cloudservice/v1"
	namespacepb "go.temporal.io/cloud-sdk/api/namespace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type fakeCloudService struct {
	cloudservice.CloudServiceClient
	namespace      *namespacepb.Namespace
	getResponses   []*namespacepb.Namespace
	updateErr      error
	updateRequests []*cloudservice.UpdateNamespaceRequest
}

func (f *fakeCloudService) GetNamespace(
	context.Context,
	*cloudservice.GetNamespaceRequest,
	...grpc.CallOption,
) (*cloudservice.GetNamespaceResponse, error) {
	namespace := f.namespace
	if len(f.getResponses) > 0 {
		namespace = f.getResponses[0]
		f.getResponses = f.getResponses[1:]
	}
	return &cloudservice.GetNamespaceResponse{Namespace: proto.Clone(namespace).(*namespacepb.Namespace)}, nil
}

func (f *fakeCloudService) UpdateNamespace(
	_ context.Context,
	request *cloudservice.UpdateNamespaceRequest,
	_ ...grpc.CallOption,
) (*cloudservice.UpdateNamespaceResponse, error) {
	f.updateRequests = append(f.updateRequests, proto.Clone(request).(*cloudservice.UpdateNamespaceRequest))
	return &cloudservice.UpdateNamespaceResponse{}, f.updateErr
}

func TestCloudAttributeIndexClientListsAllTypes(t *testing.T) {
	service := &fakeCloudService{namespace: testCloudNamespace("7", map[string]namespacepb.NamespaceSpec_SearchAttributeType{
		"text":         namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_TEXT,
		"keyword":      namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_KEYWORD,
		"keyword-list": namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_KEYWORD_LIST,
		"int":          namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_INT,
		"double":       namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_DOUBLE,
		"bool":         namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_BOOL,
		"datetime":     namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_DATETIME,
	})}
	client := newCloudAttributeIndexClient("test.namespace", service, nil)

	indexes, err := client.ListAttributeIndexes(context.Background())

	require.NoError(t, err)
	require.Equal(t, map[string]dexpb.IndexType{
		"text":         dexpb.IndexType_INDEX_TYPE_TEXT,
		"keyword":      dexpb.IndexType_INDEX_TYPE_KEYWORD,
		"keyword-list": dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
		"int":          dexpb.IndexType_INDEX_TYPE_INT,
		"double":       dexpb.IndexType_INDEX_TYPE_DOUBLE,
		"bool":         dexpb.IndexType_INDEX_TYPE_BOOL,
		"datetime":     dexpb.IndexType_INDEX_TYPE_DATETIME,
	}, indexes)
}

func TestCloudAttributeIndexClientAddsIndexesWithoutReplacingNamespaceSpec(t *testing.T) {
	service := &fakeCloudService{namespace: testCloudNamespace("7", map[string]namespacepb.NamespaceSpec_SearchAttributeType{
		"existing": namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_KEYWORD,
	})}
	client := newCloudAttributeIndexClient("test.namespace", service, nil)

	err := client.AddAttributeIndexes(context.Background(), map[string]dexpb.IndexType{
		"existing": dexpb.IndexType_INDEX_TYPE_KEYWORD,
		"new":      dexpb.IndexType_INDEX_TYPE_BOOL,
	})

	require.NoError(t, err)
	require.Len(t, service.updateRequests, 1)
	request := service.updateRequests[0]
	require.Equal(t, "test.namespace", request.GetNamespace())
	require.Equal(t, "7", request.GetResourceVersion())
	require.Equal(t, int32(30), request.GetSpec().GetRetentionDays())
	require.Equal(t, namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_BOOL, request.GetSpec().GetSearchAttributes()["new"])
	require.NotSame(t, service.namespace.GetSpec(), request.GetSpec())
}

func TestCloudAttributeIndexClientRejectsTypeConflict(t *testing.T) {
	service := &fakeCloudService{namespace: testCloudNamespace("7", map[string]namespacepb.NamespaceSpec_SearchAttributeType{
		"state": namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_KEYWORD,
	})}
	client := newCloudAttributeIndexClient("test.namespace", service, nil)

	err := client.AddAttributeIndexes(context.Background(), map[string]dexpb.IndexType{
		"state": dexpb.IndexType_INDEX_TYPE_TEXT,
	})

	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Empty(t, service.updateRequests)
}

func TestCloudAttributeIndexClientAcceptsConcurrentRegistration(t *testing.T) {
	service := &fakeCloudService{
		getResponses: []*namespacepb.Namespace{
			testCloudNamespace("7", nil),
			testCloudNamespace("8", map[string]namespacepb.NamespaceSpec_SearchAttributeType{
				"state": namespacepb.NamespaceSpec_SEARCH_ATTRIBUTE_TYPE_KEYWORD,
			}),
		},
		updateErr: status.Error(codes.Aborted, "resource version changed"),
	}
	client := newCloudAttributeIndexClient("test.namespace", service, nil)

	err := client.AddAttributeIndexes(context.Background(), map[string]dexpb.IndexType{
		"state": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.NoError(t, err)
}

func TestCloudAttributeIndexClientReportsConcurrentUnrelatedUpdate(t *testing.T) {
	service := &fakeCloudService{
		getResponses: []*namespacepb.Namespace{
			testCloudNamespace("7", nil),
			testCloudNamespace("8", nil),
		},
		updateErr: status.Error(codes.Aborted, "resource version changed"),
	}
	client := newCloudAttributeIndexClient("test.namespace", service, nil)

	err := client.AddAttributeIndexes(context.Background(), map[string]dexpb.IndexType{
		"state": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.Equal(t, codes.Aborted, status.Code(err))
}

func testCloudNamespace(
	resourceVersion string,
	searchAttributes map[string]namespacepb.NamespaceSpec_SearchAttributeType,
) *namespacepb.Namespace {
	return &namespacepb.Namespace{
		Namespace:       "test.namespace",
		ResourceVersion: resourceVersion,
		Spec: &namespacepb.NamespaceSpec{
			Name:             "test",
			RetentionDays:    30,
			SearchAttributes: searchAttributes,
		},
	}
}
