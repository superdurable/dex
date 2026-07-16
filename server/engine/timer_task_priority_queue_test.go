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

package engine

import (
	"container/heap"

	"github.com/stretchr/testify/assert"
	"github.com/xcherryio/xcherry/server/persistence/data_models"

	"testing"
)

func TestTimerTaskPriorityQueue(t *testing.T) {
	pq := NewTimerTaskPriorityQueue([]data_models.TimerTask{
		{FireTimestampSeconds: 6},
		{FireTimestampSeconds: 7},
		{FireTimestampSeconds: 5},
		{FireTimestampSeconds: 8},
	})

	heap.Init(&pq)

	heap.Push(&pq, &data_models.TimerTask{FireTimestampSeconds: 3})
	heap.Push(&pq, &data_models.TimerTask{FireTimestampSeconds: 1})
	heap.Push(&pq, &data_models.TimerTask{FireTimestampSeconds: 2})
	heap.Push(&pq, &data_models.TimerTask{FireTimestampSeconds: 4})

	for i := 0; i < 8; i++ {
		task0 := pq[0]
		task := heap.Pop(&pq)
		assert.Equal(t, task0, task)
		task1, ok := task.(*data_models.TimerTask)
		assert.Equal(t, true, ok)

		assert.Equal(t, int64(i+1), task1.FireTimestampSeconds)
	}
}
