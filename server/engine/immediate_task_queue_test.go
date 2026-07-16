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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImmediateTaskPagesEmpty(t *testing.T) {
	var completedPages []*immediateTaskPage
	pages := mergeImmediateTaskPages(completedPages)

	assert.Equal(t, 0, len(pages))
}

func TestImmediateTaskPages(t *testing.T) {
	var completedPages []*immediateTaskPage
	completedPages = append(completedPages,
		&immediateTaskPage{
			minTaskSequence: 1,
			maxTaskSequence: 2,
		},
		&immediateTaskPage{
			minTaskSequence: 7,
			maxTaskSequence: 8,
		},
		&immediateTaskPage{
			minTaskSequence: 3,
			maxTaskSequence: 4,
		})
	pages := mergeImmediateTaskPages(completedPages)

	assert.Equal(t, 2, len(pages))
	assert.Equal(t, &immediateTaskPage{
		minTaskSequence: 1,
		maxTaskSequence: 4,
	}, pages[0])
	assert.Equal(t, &immediateTaskPage{
		minTaskSequence: 7,
		maxTaskSequence: 8,
	}, pages[1])
}
