// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

//go:generate mockgen -source=./communication.go -package=dextest -destination=../dextest/communication.go

import "github.com/superdurable/dex/sdk-go/gen/dexpb"

type Communication interface {
	// PublishInternalChannel publishes a value to an interstate Channel
	PublishInternalChannel(channelName string, value interface{})

	// TriggerStateMovements trigger new state movements as the RPC results
	// NOTE: closing workflows like completing/failing are not supported
	// NOTE: Only used in RPC -- cannot be used in state APIs
	TriggerStateMovements(movements ...StateMovement)

	// below is for internal implementation
	communicationInternal
}

type communicationInternal interface {
	GetToPublishInternalChannel() map[string][]dexpb.EncodedObject
	GetToTriggerStateMovements() []StateMovement
}
