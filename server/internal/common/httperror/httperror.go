// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package httperror

import (
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"

	"net/http"
)

func CheckHttpResponseAndError(err error, httpResp *http.Response, logger log.Logger) bool {
	status := 0
	if httpResp != nil {
		status = httpResp.StatusCode
	}
	logger.Debug("check http response and error", tag.Error(err), tag.StatusCode(status))

	if err != nil || (httpResp != nil && httpResp.StatusCode != http.StatusOK) {
		return true
	}
	return false
}
