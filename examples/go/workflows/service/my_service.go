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

import "fmt"

type MyService interface {
	SendEmail(recipient, subject, content string)
	ChargeUser(email, customerId string, amount int)
	UpdateExternalSystem(message string)
	CallAPI1(data string)
	CallAPI2(data string)
	CallAPI3(data string)
	CallAPI4(data string)

	CheckBalance(account string, amount int) bool
	Debit(account string, amount int) error
	Credit(account string, amount int) error
	CreateDebitMemo(account string, amount int, notes string) error
	CreateCreditMemo(account string, amount int, notes string) error

	UndoDebit(account string, amount int) error
	UndoCredit(account string, amount int) error
	UndoCreateDebitMemo(account string, amount int, notes string) error
	UndoCreateCreditMemo(account string, amount int, notes string) error
}

type myServiceImpl struct{}

func (m myServiceImpl) UpdateExternalSystem(message string) {
	fmt.Println("Update external system(like via RPC, or sending Kafka message or database):", message)
}

func (m myServiceImpl) SendEmail(recipient, subject, content string) {
	fmt.Printf("sending an email to %v, title: %v, content: %v \n", recipient, subject, content)
}

func (m myServiceImpl) ChargeUser(email, customerId string, amount int) {
	fmt.Printf("charege user customerId[%v] email[%v] for $%v \n", customerId, email, amount)
}

func (m myServiceImpl) CallAPI1(data string) {
	fmt.Println("call API1")
}

func (m myServiceImpl) CallAPI2(data string) {
	fmt.Println("call API2")
}

func (m myServiceImpl) CallAPI3(data string) {
	fmt.Println("call API3")
}

func (m myServiceImpl) CallAPI4(data string) {
	fmt.Println("call API4")
}

func (m myServiceImpl) CheckBalance(account string, amount int) bool {
	return true
}

func (m myServiceImpl) Debit(account string, amount int) error {
	// return some error here to test retry and failure handling mechanism
	return nil
}

func (m myServiceImpl) Credit(account string, amount int) error {
	// return some error here to test retry and failure handling mechanism
	return nil
}

func (m myServiceImpl) CreateDebitMemo(account string, amount int, notes string) error {
	// return some error here to test retry and failure handling mechanism
	return nil
}

func (m myServiceImpl) CreateCreditMemo(account string, amount int, notes string) error {
	// return some error here to test retry and failure handling mechanism
	return nil
}

func (m myServiceImpl) UndoDebit(account string, amount int) error {
	// return some error here to test retry and failure handling mechanism
	return nil
}

func (m myServiceImpl) UndoCredit(account string, amount int) error {
	// return some error here to test retry and failure handling mechanism
	return nil
}

func (m myServiceImpl) UndoCreateDebitMemo(account string, amount int, notes string) error {
	// return some error here to test retry and failure handling mechanism
	return nil
}

func (m myServiceImpl) UndoCreateCreditMemo(account string, amount int, notes string) error {
	// return some error here to test retry and failure handling mechanism
	return nil
}

func NewMyService() MyService {
	return &myServiceImpl{}
}
