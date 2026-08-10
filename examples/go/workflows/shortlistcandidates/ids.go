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

package shortlistcandidates

import (
	"context"
	"errors"

	"github.com/superdurable/dex/sdk-go/dex"
)

func EmployerOptInFlowID(employerID string) string {
	return "shortlist_candidates_opt_in_" + employerID
}

func ShortlistFlowID(employerID, candidateID string) string {
	return "shortlist_candidates_shortlist_" + employerID + "_" + candidateID
}

func IsOptedIn(
	ctx context.Context,
	client *dex.Client,
	employerOptIn *EmployerOptInFlow,
	employerID string,
) (bool, error) {
	var optedIn bool
	err := client.InvokeRPC(
		ctx,
		EmployerOptInFlowID(employerID),
		employerOptIn.IsOptedIn,
		nil,
		&optedIn,
		dex.InvokeOptions{},
	)
	if err != nil {
		var inactive *dex.FlowNotActiveError
		if errors.As(err, &inactive) {
			return false, nil
		}
		return false, err
	}
	return optedIn, nil
}
