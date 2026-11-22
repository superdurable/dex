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

package managerapproval

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestRespondDecisionError(t *testing.T) {
	serviceError := &dex.ServiceError{Detail: "invoke refund decision"}
	tests := []struct {
		name            string
		err             error
		expectedStatus  int
		expectedMessage string
	}{
		{
			name: "unrelated Worker failure",
			err: &dex.WorkerInvocationError{
				ServiceError: serviceError,
				Worker:       &dex.WorkerError{Type: "DecodeError", Detail: notAwaitingManagerMessage},
			},
			expectedStatus:  http.StatusInternalServerError,
			expectedMessage: serviceError.Error(),
		},
		{
			name:            "Flow is closing",
			err:             &dex.FlowClosingError{ServiceError: serviceError},
			expectedStatus:  http.StatusConflict,
			expectedMessage: serviceError.Error(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request, _ := gin.CreateTestContext(response)
			respondDecisionError(request, test.err)
			require.Equal(t, test.expectedStatus, response.Code)
			require.JSONEq(t, `{"error":"`+test.expectedMessage+`"}`, response.Body.String())
		})
	}
}
