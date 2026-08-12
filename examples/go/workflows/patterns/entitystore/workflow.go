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
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const StoreName = "entityStore"

var (
	DisplayName    = dex.DefineAttribute[string]("display_name", dex.SyncToAttributeStore())
	Email          = dex.DefineAttribute[string]("email", dex.SyncToAttributeStore())
	MarketingOptIn = dex.DefineAttribute[bool]("marketing_opt_in", dex.SyncToAttributeStore())
	Credits        = dex.DefineAttribute[int64]("credits", dex.SyncToAttributeStore())
	Weight         = dex.DefineAttribute[float64]("weight", dex.SyncToAttributeStore())
	LastLoggedIn   = dex.DefineAttribute[time.Time]("last_logged_in_time", dex.SyncToAttributeStore())
	Metadata       = dex.DefineAttribute[UserProfileMetadata]("metadata", dex.SyncToAttributeStore())
)

type UserProfileMetadata struct {
	Source string   `json:"source"`
	Tags   []string `json:"tags"`
}

type UserProfile struct {
	DisplayName    string              `json:"displayName"`
	Email          string              `json:"email"`
	MarketingOptIn bool                `json:"marketingOptIn"`
	Credits        int64               `json:"credits"`
	Weight         float64             `json:"weight"`
	LastLoggedIn   time.Time           `json:"lastLoggedInTime"`
	Metadata       UserProfileMetadata `json:"metadata"`
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
		Attributes: []dex.AttributeDef{
			DisplayName, Email, MarketingOptIn, Credits, Weight, LastLoggedIn, Metadata,
		},
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
	if err := Credits.Set(ctx, profile.Credits); err != nil {
		return nil, err
	}
	if err := Weight.Set(ctx, profile.Weight); err != nil {
		return nil, err
	}
	if err := LastLoggedIn.Set(ctx, profile.LastLoggedIn); err != nil {
		return nil, err
	}
	if err := Metadata.Set(ctx, profile.Metadata); err != nil {
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
	credits, err := Credits.Get(ctx)
	if err != nil {
		return nil, err
	}
	weight, err := Weight.Get(ctx)
	if err != nil {
		return nil, err
	}
	lastLoggedIn, err := LastLoggedIn.Get(ctx)
	if err != nil {
		return nil, err
	}
	metadata, err := Metadata.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[UserProfile]{Output: UserProfile{
		DisplayName:    displayName,
		Email:          email,
		MarketingOptIn: marketingOptIn,
		Credits:        credits,
		Weight:         weight,
		LastLoggedIn:   lastLoggedIn,
		Metadata:       metadata,
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
	if err := Credits.Delete(ctx); err != nil {
		return nil, err
	}
	if err := Weight.Delete(ctx); err != nil {
		return nil, err
	}
	if err := LastLoggedIn.Delete(ctx); err != nil {
		return nil, err
	}
	if err := Metadata.Delete(ctx); err != nil {
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
	credits, err := dex.InitialAttribute(Credits, profile.Credits)
	if err != nil {
		return nil, err
	}
	weight, err := dex.InitialAttribute(Weight, profile.Weight)
	if err != nil {
		return nil, err
	}
	lastLoggedIn, err := dex.InitialAttribute(LastLoggedIn, profile.LastLoggedIn)
	if err != nil {
		return nil, err
	}
	metadata, err := dex.InitialAttribute(Metadata, profile.Metadata)
	if err != nil {
		return nil, err
	}
	return []dex.InitialAttributeDef{
		displayName, email, marketingOptIn, credits, weight, lastLoggedIn, metadata,
	}, nil
}

func validateProfile(profile UserProfile) error {
	if profile.DisplayName == "" {
		return fmt.Errorf("display name is required")
	}
	if profile.Email == "" {
		return fmt.Errorf("email is required")
	}
	if profile.LastLoggedIn.IsZero() {
		return fmt.Errorf("last logged-in time is required")
	}
	if profile.Metadata.Source == "" {
		return fmt.Errorf("metadata is required")
	}
	return nil
}

var (
	_ dex.Flow                       = (*UserProfileFlow)(nil)
	_ dex.RPC[UserProfile, dex.None] = (*UserProfileFlow)(nil).UpdateProfile
	_ dex.RPC[dex.None, UserProfile] = (*UserProfileFlow)(nil).GetProfile
	_ dex.RPC[dex.None, dex.None]    = (*UserProfileFlow)(nil).ClearProfile
)
