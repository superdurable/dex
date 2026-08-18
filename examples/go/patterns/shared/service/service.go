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

package service

import (
	"encoding/json"
	"fmt"
)

type ServiceDependency interface {
	AttemptExternalAPICall(message string) (string, error)
	ExternalAPICall(message string) string
	UpdateExternalSystem(message string)
	SendEmail(subject, content string)
	Upsert(document any) error
}

type serviceDependencyImpl struct {
	readExternalCounter int
}

func NewServiceDependency() ServiceDependency {
	return &serviceDependencyImpl{}
}

func (service *serviceDependencyImpl) AttemptExternalAPICall(message string) (string, error) {
	fmt.Printf("Try external system call: (%d)\n", service.readExternalCounter)
	if service.readExternalCounter < 2 {
		service.readExternalCounter++
		return "", fmt.Errorf("there is an error when calling external system, retry it")
	}
	service.readExternalCounter = 0
	fmt.Printf("Data read from external system: (%s)\n", message)
	return "External data result", nil
}

func (service *serviceDependencyImpl) ExternalAPICall(message string) string {
	fmt.Printf("Data read from external system: (%s)\n", message)
	return "External data result"
}

func (*serviceDependencyImpl) UpdateExternalSystem(message string) {
	fmt.Printf(
		"update external system(like sending Kafka message or upsert to database): %s\n",
		message,
	)
}

func (*serviceDependencyImpl) SendEmail(subject, content string) {
	fmt.Printf("send an email to job seeker, title: %s, content: %s \n", subject, content)
}

func (*serviceDependencyImpl) Upsert(document any) error {
	serialized, err := json.Marshal(document)
	if err != nil {
		return err
	}
	fmt.Printf("upsert: %s \n", string(serialized))
	return nil
}
