// Package telemetry owns generation event context identity and lifecycle orchestration.
package telemetry

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/speakeasy-api/speakeasy-client-sdk-go/v3/pkg/models/shared"
)

var (
	// ErrLifecycleStart indicates that a telemetry lifecycle could not start.
	ErrLifecycleStart = errors.New("start telemetry lifecycle")
	// ErrNilEvent indicates that a telemetry lifecycle returned no active event.
	ErrNilEvent = errors.New("telemetry lifecycle returned a nil event")
)

// Lifecycle constructs and finalizes generation events. Delivery-capable
// implementations remain responsible for honoring access telemetry opt-out state.
type Lifecycle interface {
	Start(context.Context, shared.InteractionType) (*shared.CliEvent, error)
	Finish(context.Context, *shared.CliEvent, error) error
}

type contextKey uint8

const (
	lifecycleKey contextKey = iota
	eventKey
)

// WithLifecycle selects the lifecycle used by Run. A nil lifecycle selects the
// default no-delivery lifecycle.
func WithLifecycle(ctx context.Context, lifecycle Lifecycle) context.Context {
	return context.WithValue(ctx, lifecycleKey, lifecycle)
}

// WithEvent stores the active event without changing its pointer identity.
func WithEvent(ctx context.Context, event *shared.CliEvent) context.Context {
	return context.WithValue(ctx, eventKey, event)
}

// EventFromContext returns the active event, if any.
func EventFromContext(ctx context.Context) *shared.CliEvent {
	if ctx == nil {
		return nil
	}
	event, _ := ctx.Value(eventKey).(*shared.CliEvent)
	return event
}

// Run starts an event, invokes the callback with the same event in its context,
// and finalizes the event after callback execution starts.
func Run(
	ctx context.Context,
	interactionType shared.InteractionType,
	callback func(context.Context, *shared.CliEvent) error,
) (resultErr error) {
	lifecycle := lifecycleFromContext(ctx)
	event, err := lifecycle.Start(ctx, interactionType)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLifecycleStart, err)
	}
	if event == nil {
		return ErrNilEvent
	}

	eventCtx := WithEvent(ctx, event)
	var callbackErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := panicError(recovered)
			// Finish failures must not replace the caller's original panic.
			_ = finishSafely(eventCtx, lifecycle, event, panicErr)
			panic(recovered)
		}

		finishErr := finishSafely(eventCtx, lifecycle, event, callbackErr)
		if callbackErr != nil {
			resultErr = callbackErr
			return
		}
		resultErr = finishErr
	}()

	callbackErr = callback(eventCtx, event)
	return
}

func lifecycleFromContext(ctx context.Context) Lifecycle {
	if ctx != nil {
		if lifecycle, ok := ctx.Value(lifecycleKey).(Lifecycle); ok && lifecycle != nil {
			return lifecycle
		}
	}
	return NoDeliveryLifecycle{}
}

func finishSafely(ctx context.Context, lifecycle Lifecycle, event *shared.CliEvent, callbackErr error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("finish telemetry lifecycle panic: %v", recovered)
		}
	}()
	return lifecycle.Finish(ctx, event, callbackErr)
}

// NoDeliveryLifecycle creates and finalizes real events without persistence or
// network delivery.
type NoDeliveryLifecycle struct{}

// Start creates an event using local time and cryptographically random IDs.
func (NoDeliveryLifecycle) Start(_ context.Context, interactionType shared.InteractionType) (*shared.CliEvent, error) {
	now := time.Now()
	eventID, err := randomID()
	if err != nil {
		return nil, err
	}
	executionID, err := randomID()
	if err != nil {
		return nil, err
	}
	return &shared.CliEvent{
		CreatedAt:       now,
		ExecutionID:     executionID,
		ID:              eventID,
		InteractionType: interactionType,
		LocalStartedAt:  now,
		Success:         false,
	}, nil
}

// Finish records completion once and performs no persistence or delivery. Event
// mutation is single-writer; shared.CliEvent is not concurrency-safe.
func (NoDeliveryLifecycle) Finish(_ context.Context, event *shared.CliEvent, callbackErr error) error {
	if event == nil || (event.LocalCompletedAt != nil && event.DurationMs != nil) {
		return nil
	}

	completedAt := time.Now()
	if event.LocalCompletedAt != nil {
		completedAt = *event.LocalCompletedAt
	} else {
		event.LocalCompletedAt = &completedAt
	}
	duration := completedAt.Sub(event.LocalStartedAt).Milliseconds()
	if event.LocalStartedAt.IsZero() || duration < 0 {
		duration = 0
	}
	event.DurationMs = &duration
	event.Success = callbackErr == nil
	if callbackErr != nil {
		errorMessage := callbackErr.Error()
		event.Error = &errorMessage
	}
	return nil
}

func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return fmt.Errorf("callback panic: %w", err)
	}
	return fmt.Errorf("callback panic: %v", recovered)
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
