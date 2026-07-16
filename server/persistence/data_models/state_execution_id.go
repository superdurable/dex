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

package data_models

import (
	"fmt"
	"strconv"
	"strings"
)

type StateExecutionId struct {
	StateId         string
	StateIdSequence int32
}

func (s StateExecutionId) GetStateExecutionId() string {
	return fmt.Sprintf("%v-%v", s.StateId, s.StateIdSequence)
}

func NewStateExecutionIdFromString(s string) (*StateExecutionId, error) {
	lastHyphenIndex := strings.LastIndex(s, "-")
	if lastHyphenIndex == -1 {
		return nil, fmt.Errorf("invalid format: %s", s)
	}

	stateId := s[:lastHyphenIndex]
	stateIdSequence, err := strconv.ParseInt(s[lastHyphenIndex+1:], 10, 32)
	if err != nil {
		return nil, err
	}

	return &StateExecutionId{StateId: stateId, StateIdSequence: int32(stateIdSequence)}, nil
}
