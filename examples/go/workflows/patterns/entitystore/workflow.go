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

package entitystore

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

const StoreName = "entityStore"

var (
	DisplayName    = dex.DefineAttribute[string]("display_name", dex.SyncToAttributeStore())
	Email          = dex.DefineAttribute[string]("email", dex.SyncToAttributeStore())
	MarketingOptIn = dex.DefineAttribute[bool]("marketing_opt_in", dex.SyncToAttributeStore())
)

type UserProfile struct {
	DisplayName    string `json:"displayName"`
	Email          string `json:"email"`
	MarketingOptIn bool   `json:"marketingOptIn"`
}

type UserProfileRequest struct {
	UserID string `json:"userId"`
	UserProfile
}

type UserProfileFlow struct {
	dex.FlowDefaults
}

func NewUserProfileFlow() *UserProfileFlow {
	return &UserProfileFlow{}
}

func (*UserProfileFlow) GetFlowType() string {
	return "UserProfileFlow"
}

func (*UserProfileFlow) GetSteps() []dex.StepDef {
	return nil
}

func (*UserProfileFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{DisplayName, Email, MarketingOptIn},
	}
}

func (*UserProfileFlow) UpdateProfile(
	ctx dex.Context,
	profile UserProfile,
) (*dex.RPCResult[dex.None], error) {
	if err := validateProfile(profile); err != nil {
		return nil, err
	}
	if err := DisplayName.Set(ctx, profile.DisplayName); err != nil {
		return nil, err
	}
	if err := Email.Set(ctx, profile.Email); err != nil {
		return nil, err
	}
	if err := MarketingOptIn.Set(ctx, profile.MarketingOptIn); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*UserProfileFlow) GetProfile(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[UserProfile], error) {
	displayName, err := DisplayName.Get(ctx)
	if err != nil {
		return nil, err
	}
	email, err := Email.Get(ctx)
	if err != nil {
		return nil, err
	}
	marketingOptIn, err := MarketingOptIn.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[UserProfile]{Output: UserProfile{
		DisplayName:    displayName,
		Email:          email,
		MarketingOptIn: marketingOptIn,
	}}, nil
}

func (*UserProfileFlow) ClearProfile(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := DisplayName.Delete(ctx); err != nil {
		return nil, err
	}
	if err := Email.Delete(ctx); err != nil {
		return nil, err
	}
	if err := MarketingOptIn.Delete(ctx); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func InitialAttributes(profile UserProfile) ([]dex.InitialAttributeDef, error) {
	if err := validateProfile(profile); err != nil {
		return nil, err
	}
	displayName, err := dex.InitialAttribute(DisplayName, profile.DisplayName)
	if err != nil {
		return nil, err
	}
	email, err := dex.InitialAttribute(Email, profile.Email)
	if err != nil {
		return nil, err
	}
	marketingOptIn, err := dex.InitialAttribute(MarketingOptIn, profile.MarketingOptIn)
	if err != nil {
		return nil, err
	}
	return []dex.InitialAttributeDef{displayName, email, marketingOptIn}, nil
}

func validateProfile(profile UserProfile) error {
	if profile.DisplayName == "" {
		return fmt.Errorf("display name is required")
	}
	if profile.Email == "" {
		return fmt.Errorf("email is required")
	}
	return nil
}

var (
	_ dex.Flow                       = (*UserProfileFlow)(nil)
	_ dex.RPC[UserProfile, dex.None] = (*UserProfileFlow)(nil).UpdateProfile
	_ dex.RPC[dex.None, UserProfile] = (*UserProfileFlow)(nil).GetProfile
	_ dex.RPC[dex.None, dex.None]    = (*UserProfileFlow)(nil).ClearProfile
)
