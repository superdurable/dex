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

	"github.com/xcherryio/xcherry/server/persistence/data_models"
)

// I know, it looks a lot to have a heap. This is the standard way of using heap in Golang
// See https://pkg.go.dev/container/heap for more details

func NewTimerTaskPriorityQueue(tasks []data_models.TimerTask) TimerTaskPriorityQueue {
	hq := make(TimerTaskPriorityQueue, 0, len(tasks))
	for _, task := range tasks {
		t := task
		hq = append(hq, &t)
	}
	heap.Init(&hq)
	return hq
}

// A TimerTaskPriorityQueue implements heap.Interface and holds Items.
type TimerTaskPriorityQueue []*data_models.TimerTask

func (pq *TimerTaskPriorityQueue) Len() int { return len(*pq) }

func (pq *TimerTaskPriorityQueue) Less(i, j int) bool {
	// We want Pop to give us the lowest, not lowest, priority so we use less than here.
	return (*pq)[i].FireTimestampSeconds < (*pq)[j].FireTimestampSeconds
}

func (pq *TimerTaskPriorityQueue) Swap(i, j int) {
	(*pq)[i], (*pq)[j] = (*pq)[j], (*pq)[i]
}

func (pq *TimerTaskPriorityQueue) Push(x any) {
	item, ok := x.(*data_models.TimerTask)
	if !ok {
		panic("Pushed item is not a TimerTask")
	}
	*pq = append(*pq, item)
}

func (pq *TimerTaskPriorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	*pq = old[0 : n-1]
	return item
}
