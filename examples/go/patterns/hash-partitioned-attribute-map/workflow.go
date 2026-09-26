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

package hashpartitionedattributemap

import (
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	FlowID           = "customer-directory"
	PartitionCount   = uint32(1000)
	fnvOffsetBasis32 = uint32(2166136261)
	fnvPrime32       = uint32(16777619)
)

type CustomerProfile struct {
	EmailAddress string `json:"emailAddress"`
	FullName     string `json:"fullName"`
	CompanyName  string `json:"companyName"`
	CustomerTier string `json:"customerTier"`
}

type CustomerProfilePartition struct {
	ProfilesByCanonicalEmail map[string]CustomerProfile `json:"profilesByCanonicalEmail"`
}

type CustomerDirectoryFlow struct {
	dex.FlowDefaults
}

var CustomerProfilesByEmailPartition = dex.DefineAttributeMap[CustomerProfilePartition](
	"customer_profiles_by_email_partition",
)

func NewCustomerDirectoryFlow() *CustomerDirectoryFlow {
	return &CustomerDirectoryFlow{}
}

func (*CustomerDirectoryFlow) GetFlowType() string {
	return "CustomerDirectoryFlow"
}

func (*CustomerDirectoryFlow) GetSteps() []dex.StepDef {
	return nil
}

func (flow *CustomerDirectoryFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.UpsertCustomerProfile, nil),
		dex.DefineRPC(flow.GetCustomerProfileByEmail, nil),
	}
}

func (*CustomerDirectoryFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{CustomerProfilesByEmailPartition},
	}
}

func (*CustomerDirectoryFlow) UpsertCustomerProfile(
	ctx dex.Context,
	profile CustomerProfile,
) (*dex.RPCResult[CustomerProfile], error) {
	canonicalEmail, partitionName, _, err := EmailPartition(profile.EmailAddress)
	if err != nil {
		return nil, err
	}
	profile.EmailAddress = canonicalEmail
	partition, err := CustomerProfilesByEmailPartition.Get(ctx, partitionName)
	if err != nil {
		var notFound *dex.AttributeNotFoundError
		if !errors.As(err, &notFound) {
			return nil, err
		}
		partition = CustomerProfilePartition{
			ProfilesByCanonicalEmail: map[string]CustomerProfile{},
		}
	}
	if partition.ProfilesByCanonicalEmail == nil {
		partition.ProfilesByCanonicalEmail = map[string]CustomerProfile{}
	}
	partition.ProfilesByCanonicalEmail[canonicalEmail] = profile
	if err := CustomerProfilesByEmailPartition.Set(ctx, partitionName, partition); err != nil {
		return nil, err
	}
	return &dex.RPCResult[CustomerProfile]{Output: profile}, nil
}

func (*CustomerDirectoryFlow) GetCustomerProfileByEmail(
	ctx dex.Context,
	emailAddress string,
) (*dex.RPCResult[CustomerProfile], error) {
	canonicalEmail, partitionName, _, err := EmailPartition(emailAddress)
	if err != nil {
		return nil, err
	}
	partition, err := CustomerProfilesByEmailPartition.Get(ctx, partitionName)
	if err != nil {
		return nil, fmt.Errorf("customer profile %q not found: %w", canonicalEmail, err)
	}
	profile, found := partition.ProfilesByCanonicalEmail[canonicalEmail]
	if !found {
		return nil, fmt.Errorf("customer profile %q not found", canonicalEmail)
	}
	return &dex.RPCResult[CustomerProfile]{Output: profile}, nil
}

func StartOptions() dex.StartFlowOptions {
	return dex.StartFlowOptions{
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
	}
}

func EmailPartition(emailAddress string) (string, string, uint32, error) {
	canonicalEmail, err := CanonicalEmailAddress(emailAddress)
	if err != nil {
		return "", "", 0, err
	}
	hash := FNV1a32([]byte(canonicalEmail))
	return canonicalEmail, fmt.Sprintf("partition-%03d", hash%PartitionCount), hash, nil
}

func CanonicalEmailAddress(emailAddress string) (string, error) {
	bytes := []byte(emailAddress)
	for _, value := range bytes {
		if value > 0x7f {
			return "", fmt.Errorf("emailAddress must contain only ASCII characters")
		}
	}
	start := 0
	for start < len(bytes) && isASCIIWhitespace(bytes[start]) {
		start++
	}
	end := len(bytes)
	for end > start && isASCIIWhitespace(bytes[end-1]) {
		end--
	}
	if start == end {
		return "", fmt.Errorf("emailAddress is required")
	}
	canonical := append([]byte(nil), bytes[start:end]...)
	for index, value := range canonical {
		if value >= 'A' && value <= 'Z' {
			canonical[index] = value + ('a' - 'A')
		}
	}
	return string(canonical), nil
}

func FNV1a32(value []byte) uint32 {
	hash := fnvOffsetBasis32
	for _, octet := range value {
		hash ^= uint32(octet)
		hash *= fnvPrime32
	}
	return hash
}

func isASCIIWhitespace(value byte) bool {
	return value == ' ' || (value >= '\t' && value <= '\r')
}

var (
	_ dex.Flow                                  = (*CustomerDirectoryFlow)(nil)
	_ dex.RPC[CustomerProfile, CustomerProfile] = (*CustomerDirectoryFlow)(nil).UpsertCustomerProfile
	_ dex.RPC[string, CustomerProfile]          = (*CustomerDirectoryFlow)(nil).GetCustomerProfileByEmail
)
