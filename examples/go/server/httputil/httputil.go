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

package httputil

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

func AllowCORS() gin.HandlerFunc {
	return func(request *gin.Context) {
		request.Header("Access-Control-Allow-Origin", "*")
		request.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		request.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
		if request.Request.Method == http.MethodOptions {
			request.AbortWithStatus(http.StatusNoContent)
			return
		}
		request.Next()
	}
}

func RequiredQuery(request *gin.Context, name string) (string, bool) {
	value := request.Query(name)
	if value == "" {
		request.JSON(http.StatusBadRequest, gin.H{"error": name + " is required"})
		return "", false
	}
	return value, true
}

func Respond(request *gin.Context, value any, err error) {
	if err != nil {
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	request.JSON(http.StatusOK, value)
}

func RespondString(request *gin.Context, value string, err error) {
	if err != nil {
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	request.String(http.StatusOK, value)
}

func StartFlow(
	request *gin.Context,
	client *sdk.Client,
	flow sdk.Flow,
	flowID string,
	input any,
	options sdk.StartFlowOptions,
) {
	runID, err := client.StartFlow(
		request.Request.Context(),
		flow,
		flowID,
		input,
		options,
	)
	Respond(request, gin.H{"flowID": flowID, "runID": runID}, err)
}

func NewFlowID(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
