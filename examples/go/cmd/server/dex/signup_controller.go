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

package dex

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/signup"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type signupController struct {
	client *sdk.Client
}

func newSignupController(client *sdk.Client) *signupController {
	return &signupController{client: client}
}

func (controller *signupController) registerRoutes(router *gin.Engine) {
	router.GET("/signup/submit", controller.submit)
	router.GET("/signup/verify", controller.verify)
}

func (controller *signupController) submit(request *gin.Context) {
	username, found := requiredQuery(request, "username")
	if !found {
		return
	}
	email, found := requiredQuery(request, "email")
	if !found {
		return
	}
	form := signup.SignupForm{
		Username:  username,
		Email:     email,
		FirstName: "Test",
		LastName:  "Test",
	}
	timeout := time.Hour
	_, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.Signup,
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
		respond(request, nil, err)
		return
	}
	respond(request, gin.H{"flowID": username}, nil)
}

func (controller *signupController) verify(request *gin.Context) {
	username, found := requiredQuery(request, "username")
	if !found {
		return
	}
	var output string
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		username,
		workflows.Signup.Verify,
		nil,
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}
