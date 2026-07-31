package access_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/speakeasy-api/generation-context/access"
	"github.com/speakeasy-api/speakeasy-client-sdk-go/v3/pkg/models/shared"
)

func TestWithDirect(t *testing.T) {
	testCases := []struct {
		name              string
		options           []access.DirectOption
		telemetryDisabled bool
	}{
		{name: "defaults telemetry enabled"},
		{name: "explicit telemetry opt-out", options: []access.DirectOption{access.DisableTelemetry()}, telemetryDisabled: true},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := access.WithDirect(context.Background(), tt.options...)
			state, ok := access.StateFromContext(ctx)
			if !ok {
				t.Fatal("expected direct state")
			}
			if state.Mode() != access.ModeDirect {
				t.Fatalf("expected direct mode, got %d", state.Mode())
			}
			if state.GeneratedLicense() != access.GeneratedLicenseAGPL {
				t.Fatalf("expected AGPL generated license, got %q", state.GeneratedLicense())
			}
			if state.TelemetryDisabled() != tt.telemetryDisabled {
				t.Fatalf("expected telemetry disabled=%t", tt.telemetryDisabled)
			}
			if _, ok := state.AccountType(); ok {
				t.Fatal("direct state must not expose an account type")
			}
			if _, ok := state.WorkspaceID(); ok {
				t.Fatal("direct state must not expose a workspace ID")
			}
			if state.WorkspaceCreatedAt() != nil {
				t.Fatal("direct state must not expose workspace creation time")
			}
			if state.BillingAddOns() != nil {
				t.Fatal("direct state must not expose billing add-ons")
			}
		})
	}
}

func TestWithAuthenticatedRoundTrip(t *testing.T) {
	createdAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	addOns := []shared.BillingAddOn{
		shared.BillingAddOnWebhooks,
		shared.BillingAddOnSDKTesting,
		shared.BillingAddOnCustomCodeRegions,
		shared.BillingAddOnSnippetAi,
	}
	ctx, err := access.WithAuthenticated(context.Background(), access.AuthenticatedInfo{
		AccountType:        shared.AccountTypeFree,
		BillingAddOns:      addOns,
		WorkspaceCreatedAt: &createdAt,
		WorkspaceID:        "workspace-id",
		TelemetryDisabled:  true,
		GeneratedLicense:   access.GeneratedLicenseCommercial,
	})
	if err != nil {
		t.Fatalf("with authenticated: %v", err)
	}

	state, ok := access.StateFromContext(ctx)
	if !ok {
		t.Fatal("expected authenticated state")
	}
	if state.Mode() != access.ModeAuthenticated {
		t.Fatalf("expected authenticated mode, got %d", state.Mode())
	}
	accountType, ok := state.AccountType()
	if !ok || accountType != shared.AccountTypeFree {
		t.Fatalf("expected authenticated Free account, got %q, %t", accountType, ok)
	}
	workspaceID, ok := state.WorkspaceID()
	if !ok || workspaceID != "workspace-id" {
		t.Fatalf("expected workspace ID, got %q, %t", workspaceID, ok)
	}
	if got := state.WorkspaceCreatedAt(); got == nil || !got.Equal(createdAt) {
		t.Fatalf("expected workspace creation time %s, got %v", createdAt, got)
	}
	if !state.TelemetryDisabled() {
		t.Fatal("expected authenticated telemetry opt-out")
	}
	if state.GeneratedLicense() != access.GeneratedLicenseCommercial {
		t.Fatalf("expected commercial generated license, got %q", state.GeneratedLicense())
	}
	for _, addOn := range addOns {
		if !state.HasBillingAddOn(addOn) {
			t.Fatalf("expected authenticated billing add-on %q", addOn)
		}
	}
	if state.HasBillingAddOn(shared.BillingAddOn("unknown")) {
		t.Fatal("unexpected billing add-on")
	}
}

func TestWithAuthenticatedDefensiveCopies(t *testing.T) {
	createdAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	addOns := []shared.BillingAddOn{shared.BillingAddOnWebhooks}
	ctx, err := access.WithAuthenticated(context.Background(), access.AuthenticatedInfo{
		AccountType:        shared.AccountTypeBusiness,
		BillingAddOns:      addOns,
		WorkspaceCreatedAt: &createdAt,
		WorkspaceID:        "workspace-id",
		GeneratedLicense:   access.GeneratedLicenseAGPL,
	})
	if err != nil {
		t.Fatalf("with authenticated: %v", err)
	}

	addOns[0] = shared.BillingAddOnSDKTesting
	createdAt = createdAt.Add(time.Hour)

	state, ok := access.StateFromContext(ctx)
	if !ok {
		t.Fatal("expected authenticated state")
	}
	if !state.HasBillingAddOn(shared.BillingAddOnWebhooks) || state.HasBillingAddOn(shared.BillingAddOnSDKTesting) {
		t.Fatal("constructor inputs mutated stored state")
	}
	if got := state.WorkspaceCreatedAt(); got == nil || got.Hour() != 3 {
		t.Fatalf("constructor time input mutated stored state: %v", got)
	}

	returnedAddOns := state.BillingAddOns()
	returnedAddOns[0] = shared.BillingAddOnSDKTesting
	returnedTime := state.WorkspaceCreatedAt()
	*returnedTime = returnedTime.Add(time.Hour)

	again, ok := access.StateFromContext(ctx)
	if !ok {
		t.Fatal("expected authenticated state on second read")
	}
	if !again.HasBillingAddOn(shared.BillingAddOnWebhooks) || again.HasBillingAddOn(shared.BillingAddOnSDKTesting) {
		t.Fatal("accessor output mutated stored state")
	}
	if got := again.WorkspaceCreatedAt(); got == nil || got.Hour() != 3 {
		t.Fatalf("accessor time output mutated stored state: %v", got)
	}
}

func TestWithAuthenticatedValidation(t *testing.T) {
	createdAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	valid := access.AuthenticatedInfo{
		AccountType:        shared.AccountTypeBusiness,
		WorkspaceCreatedAt: &createdAt,
		WorkspaceID:        "workspace-id",
		GeneratedLicense:   access.GeneratedLicenseCommercial,
	}

	testCases := []struct {
		name   string
		mutate func(*access.AuthenticatedInfo)
	}{
		{name: "missing account type", mutate: func(info *access.AuthenticatedInfo) { info.AccountType = "" }},
		{name: "unknown account type", mutate: func(info *access.AuthenticatedInfo) { info.AccountType = "partner" }},
		{name: "missing workspace ID", mutate: func(info *access.AuthenticatedInfo) { info.WorkspaceID = "  " }},
		{name: "workspace ID with surrounding whitespace", mutate: func(info *access.AuthenticatedInfo) { info.WorkspaceID = " workspace-id " }},
		{name: "missing workspace creation time", mutate: func(info *access.AuthenticatedInfo) { info.WorkspaceCreatedAt = nil }},
		{name: "zero workspace creation time", mutate: func(info *access.AuthenticatedInfo) { zero := time.Time{}; info.WorkspaceCreatedAt = &zero }},
		{name: "missing generated license", mutate: func(info *access.AuthenticatedInfo) { info.GeneratedLicense = "" }},
		{name: "unknown generated license", mutate: func(info *access.AuthenticatedInfo) { info.GeneratedLicense = "proprietary" }},
		{name: "unknown billing add-on", mutate: func(info *access.AuthenticatedInfo) { info.BillingAddOns = []shared.BillingAddOn{"unknown"} }},
		{name: "duplicate billing add-on", mutate: func(info *access.AuthenticatedInfo) {
			info.BillingAddOns = []shared.BillingAddOn{shared.BillingAddOnWebhooks, shared.BillingAddOnWebhooks}
		}},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			info := valid
			tt.mutate(&info)
			original := context.WithValue(context.Background(), testContextKey{}, "unchanged")

			ctx, err := access.WithAuthenticated(original, info)
			if !errors.Is(err, access.ErrInvalidAuthenticatedInfo) {
				t.Fatalf("expected validation error, got %v", err)
			}
			if ctx != original {
				t.Fatal("validation failure must return the original context")
			}
			if _, ok := access.StateFromContext(ctx); ok {
				t.Fatal("validation failure must not insert partial state")
			}
		})
	}
}

func TestAuthenticatedAccountTypes(t *testing.T) {
	createdAt := time.Now()
	for _, accountType := range []shared.AccountType{
		shared.AccountTypeFree,
		shared.AccountTypeScaleUp,
		shared.AccountTypeBusiness,
		shared.AccountTypeEnterprise,
	} {
		ctx, err := access.WithAuthenticated(context.Background(), access.AuthenticatedInfo{
			AccountType:        accountType,
			WorkspaceCreatedAt: &createdAt,
			WorkspaceID:        "workspace-id",
			GeneratedLicense:   access.GeneratedLicenseCommercial,
		})
		if err != nil {
			t.Fatalf("account type %q: %v", accountType, err)
		}
		state, ok := access.StateFromContext(ctx)
		if !ok {
			t.Fatalf("account type %q: missing state", accountType)
		}
		got, ok := state.AccountType()
		if !ok || got != accountType {
			t.Fatalf("account type %q: got %q, %t", accountType, got, ok)
		}
	}
}

func TestStateConcurrentReads(t *testing.T) {
	createdAt := time.Now()
	ctx, err := access.WithAuthenticated(context.Background(), access.AuthenticatedInfo{
		AccountType:        shared.AccountTypeEnterprise,
		BillingAddOns:      []shared.BillingAddOn{shared.BillingAddOnWebhooks},
		WorkspaceCreatedAt: &createdAt,
		WorkspaceID:        "workspace-id",
		GeneratedLicense:   access.GeneratedLicenseCommercial,
	})
	if err != nil {
		t.Fatalf("with authenticated: %v", err)
	}

	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				state, ok := access.StateFromContext(ctx)
				if !ok || !state.HasBillingAddOn(shared.BillingAddOnWebhooks) {
					t.Errorf("unexpected state read")
					return
				}
				_ = state.BillingAddOns()
				_ = state.WorkspaceCreatedAt()
			}
		}()
	}
	workers.Wait()
}

func TestStateFromContextMissing(t *testing.T) {
	if _, ok := access.StateFromContext(context.Background()); ok {
		t.Fatal("unexpected state in empty context")
	}
	if _, ok := access.StateFromContext(nil); ok {
		t.Fatal("unexpected state in nil context")
	}
}

type testContextKey struct{}
