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

package hashpartitionedattributemap

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *dex.Client
	flow   *CustomerDirectoryFlow
}

func RegisterRoutes(router gin.IRouter, client *dex.Client, flow *CustomerDirectoryFlow) {
	patternController := &controller{client: client, flow: flow}
	group := router.Group("/patterns/hash-partitioned-attribute-map")
	group.POST("/start", patternController.start)
	group.PUT("/customer-profile", patternController.upsertCustomerProfile)
	group.GET("/customer-profile", patternController.getCustomerProfile)
}

func (controller *controller) start(request *gin.Context) {
	httputil.StartFlow(
		request,
		controller.client,
		controller.flow,
		FlowID,
		nil,
		StartOptions(),
	)
}

func (controller *controller) upsertCustomerProfile(request *gin.Context) {
	var profile CustomerProfile
	if err := request.ShouldBindJSON(&profile); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_, partitionName, _, err := EmailPartition(profile.EmailAddress)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var savedProfile CustomerProfile
	err = controller.client.InvokeRPCWithOptions(
		request.Request.Context(),
		FlowID,
		controller.flow.UpsertCustomerProfile,
		profile,
		&savedProfile,
		dex.RPCInvokeOptions{
			LockAttributeMapInstances: []dex.AttributeLock{
				dex.LockAttributeMap(CustomerProfilesByEmailPartition, partitionName),
			},
			LoadAttributeMapInstances: []dex.AttributeMapLoad{
				CustomerProfilesByEmailPartition.Load(partitionName),
			},
		},
	)
	httputil.Respond(request, savedProfile, err)
}

func (controller *controller) getCustomerProfile(request *gin.Context) {
	emailAddress, found := httputil.RequiredQuery(request, "emailAddress")
	if !found {
		return
	}
	_, partitionName, _, err := EmailPartition(emailAddress)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var profile CustomerProfile
	err = controller.client.InvokeRPCWithOptions(
		request.Request.Context(),
		FlowID,
		controller.flow.GetCustomerProfileByEmail,
		emailAddress,
		&profile,
		dex.RPCInvokeOptions{
			LoadAttributeMapInstances: []dex.AttributeMapLoad{
				CustomerProfilesByEmailPartition.Load(partitionName),
			},
		},
	)
	httputil.Respond(request, profile, err)
}
