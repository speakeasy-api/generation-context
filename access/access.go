// Package access stores validated, invocation-scoped generation access state.
package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/speakeasy-api/speakeasy-client-sdk-go/v3/pkg/models/shared"
)

// Mode identifies how a generation invocation was authorized.
type Mode uint8

const (
	ModeDirect Mode = iota + 1
	ModeAuthenticated
)

// GeneratedLicense identifies the license selected for generated output.
type GeneratedLicense string

const (
	GeneratedLicenseAGPL       GeneratedLicense = "agpl"
	GeneratedLicenseCommercial GeneratedLicense = "commercial"
)

// ErrInvalidAuthenticatedInfo indicates that authenticated state was incomplete
// or contained an unsupported value.
var ErrInvalidAuthenticatedInfo = errors.New("invalid authenticated generation access")

// AuthenticatedInfo contains metadata already validated or decided by the
// authenticating caller. It does not contain credentials or entitlement rules.
type AuthenticatedInfo struct {
	AccountType        shared.AccountType
	BillingAddOns      []shared.BillingAddOn
	WorkspaceCreatedAt *time.Time
	WorkspaceID        string
	TelemetryDisabled  bool
	GeneratedLicense   GeneratedLicense
}

// State is immutable generation access state. Construct it with WithDirect or
// WithAuthenticated and retrieve it with StateFromContext.
type State struct {
	mode               Mode
	accountType        shared.AccountType
	billingAddOns      []shared.BillingAddOn
	workspaceCreatedAt time.Time
	workspaceID        string
	telemetryDisabled  bool
	generatedLicense   GeneratedLicense
}

// Mode returns the explicitly selected invocation mode.
func (s State) Mode() Mode {
	return s.mode
}

// GeneratedLicense returns the caller-decided generated-output license.
// Direct state returns the zero value until the caller elects a license
// (ElectAGPL); consumers producing licensed output must refuse to generate
// while no election has been made.
func (s State) GeneratedLicense() GeneratedLicense {
	return s.generatedLicense
}

// TelemetryDisabled reports whether usage telemetry is disabled for this invocation.
func (s State) TelemetryDisabled() bool {
	return s.telemetryDisabled
}

// AccountType returns authenticated account metadata. Direct state has no account type.
func (s State) AccountType() (shared.AccountType, bool) {
	return s.accountType, s.mode == ModeAuthenticated
}

// BillingAddOns returns a defensive copy of authenticated billing add-ons.
func (s State) BillingAddOns() []shared.BillingAddOn {
	if s.mode != ModeAuthenticated {
		return nil
	}
	return append([]shared.BillingAddOn(nil), s.billingAddOns...)
}

// HasBillingAddOn checks authenticated billing add-ons without allocating a copy.
func (s State) HasBillingAddOn(addOn shared.BillingAddOn) bool {
	if s.mode != ModeAuthenticated {
		return false
	}
	for _, candidate := range s.billingAddOns {
		if candidate == addOn {
			return true
		}
	}
	return false
}

// WorkspaceCreatedAt returns a defensive copy of authenticated workspace metadata.
func (s State) WorkspaceCreatedAt() *time.Time {
	if s.mode != ModeAuthenticated {
		return nil
	}
	createdAt := s.workspaceCreatedAt
	return &createdAt
}

// WorkspaceID returns authenticated workspace identity. Direct state has no workspace ID.
func (s State) WorkspaceID() (string, bool) {
	return s.workspaceID, s.mode == ModeAuthenticated
}

type directConfig struct {
	telemetryDisabled bool
	generatedLicense  GeneratedLicense
}

// DirectOption configures explicit direct generation.
type DirectOption interface {
	applyDirect(*directConfig)
}

type directOption func(*directConfig)

func (option directOption) applyDirect(config *directConfig) {
	option(config)
}

// DisableTelemetry explicitly opts direct generation out of usage telemetry.
func DisableTelemetry() DirectOption {
	return directOption(func(config *directConfig) {
		config.telemetryDisabled = true
	})
}

// ElectAGPL records the caller's explicit acceptance of AGPL-3.0-only
// licensing for the generated output. Direct generation carries no license
// until the caller elects one; commercial output can never be elected here —
// it requires authenticated caller input (a validated license token).
func ElectAGPL() DirectOption {
	return directOption(func(config *directConfig) {
		config.generatedLicense = GeneratedLicenseAGPL
	})
}

type contextKey struct{}

// WithDirect records explicit direct generation. Direct state carries NO
// generated-output license until the caller elects one with ElectAGPL —
// consumers that produce licensed output must treat an empty
// GeneratedLicense as "no election made" and refuse to generate. Commercial
// output requires authenticated caller input and cannot be elected here.
func WithDirect(ctx context.Context, options ...DirectOption) context.Context {
	config := directConfig{}
	for _, option := range options {
		if option != nil {
			option.applyDirect(&config)
		}
	}

	return context.WithValue(ctx, contextKey{}, State{
		mode:              ModeDirect,
		telemetryDisabled: config.telemetryDisabled,
		generatedLicense:  config.generatedLicense,
	})
}

// WithAuthenticated validates and records authenticated generation state. On
// validation failure it returns the original context unchanged.
func WithAuthenticated(ctx context.Context, info AuthenticatedInfo) (context.Context, error) {
	if err := validateAuthenticatedInfo(info); err != nil {
		return ctx, err
	}

	return context.WithValue(ctx, contextKey{}, State{
		mode:               ModeAuthenticated,
		accountType:        info.AccountType,
		billingAddOns:      append([]shared.BillingAddOn(nil), info.BillingAddOns...),
		workspaceCreatedAt: *info.WorkspaceCreatedAt,
		workspaceID:        info.WorkspaceID,
		telemetryDisabled:  info.TelemetryDisabled,
		generatedLicense:   info.GeneratedLicense,
	}), nil
}

// StateFromContext returns a defensive copy of the explicit access state.
func StateFromContext(ctx context.Context) (State, bool) {
	if ctx == nil {
		return State{}, false
	}
	state, ok := ctx.Value(contextKey{}).(State)
	if !ok {
		return State{}, false
	}
	state.billingAddOns = append([]shared.BillingAddOn(nil), state.billingAddOns...)
	return state, true
}

func validateAuthenticatedInfo(info AuthenticatedInfo) error {
	if !validAccountType(info.AccountType) {
		return fmt.Errorf("%w: unsupported account type %q", ErrInvalidAuthenticatedInfo, info.AccountType)
	}
	if strings.TrimSpace(info.WorkspaceID) == "" {
		return fmt.Errorf("%w: workspace ID is required", ErrInvalidAuthenticatedInfo)
	}
	if strings.TrimSpace(info.WorkspaceID) != info.WorkspaceID {
		return fmt.Errorf("%w: workspace ID must not contain surrounding whitespace", ErrInvalidAuthenticatedInfo)
	}
	if info.WorkspaceCreatedAt == nil || info.WorkspaceCreatedAt.IsZero() {
		return fmt.Errorf("%w: workspace creation time is required", ErrInvalidAuthenticatedInfo)
	}
	if !validGeneratedLicense(info.GeneratedLicense) {
		return fmt.Errorf("%w: unsupported generated license %q", ErrInvalidAuthenticatedInfo, info.GeneratedLicense)
	}

	seen := make(map[shared.BillingAddOn]struct{}, len(info.BillingAddOns))
	for _, addOn := range info.BillingAddOns {
		if !validBillingAddOn(addOn) {
			return fmt.Errorf("%w: unsupported billing add-on %q", ErrInvalidAuthenticatedInfo, addOn)
		}
		if _, exists := seen[addOn]; exists {
			return fmt.Errorf("%w: duplicate billing add-on %q", ErrInvalidAuthenticatedInfo, addOn)
		}
		seen[addOn] = struct{}{}
	}

	return nil
}

func validAccountType(accountType shared.AccountType) bool {
	switch accountType {
	case shared.AccountTypeFree,
		shared.AccountTypeScaleUp,
		shared.AccountTypeBusiness,
		shared.AccountTypeEnterprise:
		return true
	default:
		return false
	}
}

func validBillingAddOn(addOn shared.BillingAddOn) bool {
	switch addOn {
	case shared.BillingAddOnWebhooks,
		shared.BillingAddOnSDKTesting,
		shared.BillingAddOnCustomCodeRegions,
		shared.BillingAddOnSnippetAi:
		return true
	default:
		return false
	}
}

func validGeneratedLicense(license GeneratedLicense) bool {
	switch license {
	case GeneratedLicenseAGPL, GeneratedLicenseCommercial:
		return true
	default:
		return false
	}
}
