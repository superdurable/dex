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

package signup

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *UserSignupFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *UserSignupFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/products/signup")
	group.GET("/submit", controller.submit)
	group.GET("/verify", controller.verify)
}

func (controller *controller) submit(request *gin.Context) {
	username, found := httputil.RequiredQuery(request, "username")
	if !found {
		return
	}
	email, found := httputil.RequiredQuery(request, "email")
	if !found {
		return
	}
	form := SignupForm{
		Username:  username,
		Email:     email,
		FirstName: "Test",
		LastName:  "Test",
	}
	timeout := time.Hour
	_, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		username,
		form,
		sdk.StartFlowOptions{Timeout: &timeout},
	)
	if err != nil {
		var duplicate *sdk.FlowAlreadyStartedError
		if errors.As(err, &duplicate) {
			request.JSON(http.StatusOK, "username already started registry")
			return
		}
		httputil.Respond(request, nil, err)
		return
	}
	httputil.Respond(request, gin.H{"flowID": username}, nil)
}

func (controller *controller) verify(request *gin.Context) {
	username, found := httputil.RequiredQuery(request, "username")
	if !found {
		return
	}
	var output string
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		username,
		controller.flow.Verify,
		nil,
		&output,
		sdk.InvokeOptions{},
	)
	httputil.Respond(request, output, err)
}
