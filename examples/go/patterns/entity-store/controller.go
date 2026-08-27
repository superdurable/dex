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

package entitystore

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type controller struct {
	client *sdk.Client
	flow   *UserProfileFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *UserProfileFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/entity-store")
	group.POST("/profile", controller.createProfile)
	group.POST("/profile/update", controller.updateProfile)
	group.GET("/profile", controller.getProfile)
	group.POST("/profile/clear", controller.clearProfile)
}

func patternStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

func (controller *controller) createProfile(request *gin.Context) {
	profileRequest, found := bindUserProfileRequest(request)
	if !found {
		return
	}
	options, err := entityStoreStartOptions(profileRequest.UserProfile)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		profileRequest.UserID,
		nil,
		options,
	)
	httputil.RespondString(request, runID, err)
}

func (controller *controller) updateProfile(request *gin.Context) {
	profileRequest, found := bindUserProfileRequest(request)
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		profileRequest.UserID,
		controller.flow.UpdateProfile,
		profileRequest.UserProfile,
		&none,
		sdk.InvokeOptions{},
	)
	httputil.RespondString(request, "Updated user profile", err)
}

func (controller *controller) getProfile(request *gin.Context) {
	userID, found := httputil.RequiredQuery(request, "userId")
	if !found {
		return
	}
	var profile UserProfile
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		userID,
		controller.flow.GetProfile,
		nil,
		&profile,
		sdk.InvokeOptions{},
	)
	if err != nil {
		httputil.RespondString(request, "", err)
		return
	}
	request.JSON(http.StatusOK, profile)
}

func (controller *controller) clearProfile(request *gin.Context) {
	userID, found := httputil.RequiredQuery(request, "userId")
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		userID,
		controller.flow.ClearProfile,
		nil,
		&none,
		sdk.InvokeOptions{},
	)
	httputil.RespondString(request, "Cleared user profile", err)
}

func bindUserProfileRequest(request *gin.Context) (UserProfileRequest, bool) {
	var profileRequest UserProfileRequest
	if err := request.ShouldBindJSON(&profileRequest); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return UserProfileRequest{}, false
	}
	if profileRequest.UserID == "" {
		request.JSON(http.StatusBadRequest, gin.H{"error": "userId is required"})
		return UserProfileRequest{}, false
	}
	return profileRequest, true
}

func entityStoreStartOptions(profile UserProfile) (sdk.StartFlowOptions, error) {
	attributes, err := InitialAttributes(profile)
	if err != nil {
		return sdk.StartFlowOptions{}, err
	}
	options := patternStartOptions()
	options.Attributes = attributes
	options.ConfigOverride = &sdk.FlowConfig{
		AttributeStoreNames: ptr.Any([]string{StoreName}),
	}
	return options, nil
}
