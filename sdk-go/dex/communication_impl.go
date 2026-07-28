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

import "github.com/superdurable/dex/sdk-go/gen/dexpb"

type communicationImpl struct {
	internalChannelNames     map[string]bool
	toPublishInternalChannel map[string][]dexpb.EncodedObject
	stateMovements           []StateMovement
	encoder                  ObjectEncoder
}

func (c *communicationImpl) GetToTriggerStateMovements() []StateMovement {
	return c.stateMovements
}

func (c *communicationImpl) TriggerStateMovements(movements ...StateMovement) {
	c.stateMovements = append(c.stateMovements, movements...)
}

func (c *communicationImpl) GetToPublishInternalChannel() map[string][]dexpb.EncodedObject {
	return c.toPublishInternalChannel
}

func (c *communicationImpl) PublishInternalChannel(channelName string, value interface{}) {
	if !c.internalChannelNames[channelName] {
		panic(NewWorkflowDefinitionErrorFmt("channelName %v is not registered", channelName))
	}
	l := c.toPublishInternalChannel[channelName]
	obj, err := c.encoder.Encode(value)
	if err != nil {
		panic(err)
	}
	l = append(l, *obj)
	c.toPublishInternalChannel[channelName] = l
}

func newCommunication(encoder ObjectEncoder, internalChannelNames map[string]bool) Communication {
	return &communicationImpl{
		encoder:                  encoder,
		internalChannelNames:     internalChannelNames,
		toPublishInternalChannel: make(map[string][]dexpb.EncodedObject),
	}
}
