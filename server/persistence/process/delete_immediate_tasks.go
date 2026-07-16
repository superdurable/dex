// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"

	"github.com/xcherryio/xcherry/server/extensions"
	"github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) DeleteImmediateTasks(
	ctx context.Context, request data_models.DeleteImmediateTasksRequest,
) error {
	return p.session.BatchDeleteImmediateTask(ctx, extensions.ImmediateTaskRangeDeleteFilter{
		ShardId:                  request.ShardId,
		MinTaskSequenceInclusive: request.MinTaskSequenceInclusive,
		MaxTaskSequenceInclusive: request.MaxTaskSequenceInclusive,
	})
}
