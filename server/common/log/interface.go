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

package log

import (
	"github.com/xcherryio/xcherry/server/common/log/tag"
)

// Logger is our abstraction for logging
// Usage examples:
//
//	 import "github.com/uber/cadence/common/log/tag"
//	 1) logger = logger.WithTags(
//	         tag.WorkflowNextEventID( 123),
//	         tag.WorkflowActionWorkflowStarted,
//	         tag.WorkflowDomainID("test-domain-id"))
//	    logger.Info("hello world")
//	 2) logger.Info("hello world",
//	         tag.WorkflowNextEventID( 123),
//	         tag.WorkflowActionWorkflowStarted,
//	         tag.WorkflowDomainID("test-domain-id"))
//		   )
//	 Note: msg should be static, it is not recommended to use fmt.Sprintf() for msg.
//	       Anything dynamic should be tagged.
type Logger interface {
	Debug(msg string, tags ...tag.Tag)
	Info(msg string, tags ...tag.Tag)
	Warn(msg string, tags ...tag.Tag)
	Error(msg string, tags ...tag.Tag)
	Fatal(msg string, tags ...tag.Tag)
	WithTags(tags ...tag.Tag) Logger
}
