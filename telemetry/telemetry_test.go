package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/speakeasy-api/generation-context/access"
	"github.com/speakeasy-api/generation-context/telemetry"
	"github.com/speakeasy-api/speakeasy-client-sdk-go/v3/pkg/models/shared"
)

type recordingLifecycle struct {
	event       *shared.CliEvent
	startErr    error
	finishErr   error
	finishPanic any

	finishCalls int
	finishCtx   context.Context
	callbackErr error
}

func (l *recordingLifecycle) Start(context.Context, shared.InteractionType) (*shared.CliEvent, error) {
	return l.event, l.startErr
}

func (l *recordingLifecycle) Finish(ctx context.Context, event *shared.CliEvent, callbackErr error) error {
	l.finishCalls++
	l.finishCtx = ctx
	l.callbackErr = callbackErr
	if l.finishPanic != nil {
		panic(l.finishPanic)
	}
	if event != l.event {
		return errors.New("finish received a different event")
	}
	return l.finishErr
}

func TestRunPreservesEventIdentityAndFinalizes(t *testing.T) {
	event := &shared.CliEvent{ID: "event-id"}
	lifecycle := &recordingLifecycle{event: event}
	ctx := telemetry.WithLifecycle(context.Background(), lifecycle)

	err := telemetry.Run(ctx, shared.InteractionTypeTargetGenerate, func(callbackCtx context.Context, callbackEvent *shared.CliEvent) error {
		if callbackEvent != event {
			t.Fatal("callback received a different event")
		}
		if telemetry.EventFromContext(callbackCtx) != event {
			t.Fatal("context contained a different event")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if lifecycle.finishCalls != 1 {
		t.Fatalf("expected one finish call, got %d", lifecycle.finishCalls)
	}
	if telemetry.EventFromContext(lifecycle.finishCtx) != event {
		t.Fatal("finish context contained a different event")
	}
	if lifecycle.callbackErr != nil {
		t.Fatalf("unexpected callback error at finish: %v", lifecycle.callbackErr)
	}
}

func TestRunStartFailuresSkipCallbackAndFinish(t *testing.T) {
	startErr := errors.New("start failed")
	testCases := []struct {
		name      string
		lifecycle *recordingLifecycle
		wantError error
	}{
		{name: "start error", lifecycle: &recordingLifecycle{startErr: startErr}, wantError: startErr},
		{name: "nil event", lifecycle: &recordingLifecycle{}, wantError: telemetry.ErrNilEvent},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			callbackRan := false
			err := telemetry.Run(
				telemetry.WithLifecycle(context.Background(), tt.lifecycle),
				shared.InteractionTypeTargetGenerate,
				func(context.Context, *shared.CliEvent) error {
					callbackRan = true
					return nil
				},
			)
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("expected %v, got %v", tt.wantError, err)
			}
			if tt.lifecycle.startErr != nil && !errors.Is(err, telemetry.ErrLifecycleStart) {
				t.Fatalf("expected lifecycle start classification, got %v", err)
			}
			if callbackRan {
				t.Fatal("callback ran after lifecycle start failure")
			}
			if tt.lifecycle.finishCalls != 0 {
				t.Fatalf("finish called %d times", tt.lifecycle.finishCalls)
			}
		})
	}
}

func TestRunErrorPrecedence(t *testing.T) {
	callbackErr := errors.New("callback failed")
	finishErr := errors.New("finish failed")

	t.Run("callback error takes precedence", func(t *testing.T) {
		lifecycle := &recordingLifecycle{event: &shared.CliEvent{}, finishErr: finishErr}
		err := telemetry.Run(telemetry.WithLifecycle(context.Background(), lifecycle), shared.InteractionTypeTargetGenerate, func(context.Context, *shared.CliEvent) error {
			return callbackErr
		})
		if !errors.Is(err, callbackErr) || errors.Is(err, finishErr) {
			t.Fatalf("expected only callback error precedence, got %v", err)
		}
		if !errors.Is(lifecycle.callbackErr, callbackErr) {
			t.Fatalf("finish did not receive callback error: %v", lifecycle.callbackErr)
		}
	})

	t.Run("finish error returned after callback success", func(t *testing.T) {
		lifecycle := &recordingLifecycle{event: &shared.CliEvent{}, finishErr: finishErr}
		err := telemetry.Run(telemetry.WithLifecycle(context.Background(), lifecycle), shared.InteractionTypeTargetGenerate, func(context.Context, *shared.CliEvent) error {
			return nil
		})
		if !errors.Is(err, finishErr) {
			t.Fatalf("expected finish error, got %v", err)
		}
	})

	t.Run("finish panic becomes an error after callback success", func(t *testing.T) {
		lifecycle := &recordingLifecycle{event: &shared.CliEvent{}, finishPanic: "finish panic"}
		err := telemetry.Run(telemetry.WithLifecycle(context.Background(), lifecycle), shared.InteractionTypeTargetGenerate, func(context.Context, *shared.CliEvent) error {
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "finish panic") {
			t.Fatalf("expected converted finish panic, got %v", err)
		}
	})
}

func TestRunFinalizesAndRepanics(t *testing.T) {
	panicErr := errors.New("callback panic")
	lifecycle := &recordingLifecycle{event: &shared.CliEvent{}, finishPanic: "finish panic"}

	defer func() {
		recovered := recover()
		if recovered != panicErr {
			t.Fatalf("expected original panic, got %v", recovered)
		}
		if lifecycle.finishCalls != 1 {
			t.Fatalf("expected one finish call, got %d", lifecycle.finishCalls)
		}
		if !errors.Is(lifecycle.callbackErr, panicErr) {
			t.Fatalf("finish did not receive wrapped panic error: %v", lifecycle.callbackErr)
		}
	}()

	_ = telemetry.Run(telemetry.WithLifecycle(context.Background(), lifecycle), shared.InteractionTypeTargetGenerate, func(context.Context, *shared.CliEvent) error {
		panic(panicErr)
	})
}

func TestNilLifecycleUsesDefault(t *testing.T) {
	ctx := telemetry.WithLifecycle(context.Background(), nil)
	if err := telemetry.Run(ctx, shared.InteractionTypeTargetGenerate, func(_ context.Context, event *shared.CliEvent) error {
		if event == nil {
			t.Fatal("default lifecycle returned nil event")
		}
		return nil
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestDefaultLifecycleCreatesAndFinalizesEvent(t *testing.T) {
	var event *shared.CliEvent
	err := telemetry.Run(context.Background(), shared.InteractionTypeTargetGenerate, func(ctx context.Context, callbackEvent *shared.CliEvent) error {
		event = callbackEvent
		if telemetry.EventFromContext(ctx) != callbackEvent {
			t.Fatal("default lifecycle changed event identity")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if event == nil || event.ID == "" || event.ExecutionID == "" {
		t.Fatalf("expected event identifiers, got %#v", event)
	}
	if event.InteractionType != shared.InteractionTypeTargetGenerate {
		t.Fatalf("unexpected interaction type %q", event.InteractionType)
	}
	if event.LocalCompletedAt == nil || event.DurationMs == nil || !event.Success || event.Error != nil {
		t.Fatalf("event was not finalized successfully: %#v", event)
	}
}

func TestNoDeliveryLifecycleFinalization(t *testing.T) {
	lifecycle := telemetry.NoDeliveryLifecycle{}
	event, err := lifecycle.Start(context.Background(), shared.InteractionTypeLint)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	callbackErr := errors.New("generation failed")
	if err := lifecycle.Finish(context.Background(), event, callbackErr); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if event.Success || event.Error == nil || *event.Error != callbackErr.Error() {
		t.Fatalf("unexpected error completion: %#v", event)
	}
	completedAt := event.LocalCompletedAt
	duration := event.DurationMs
	if err := lifecycle.Finish(context.Background(), event, nil); err != nil {
		t.Fatalf("second finish: %v", err)
	}
	if event.LocalCompletedAt != completedAt || event.DurationMs != duration || event.Success {
		t.Fatal("second finish mutated an already completed event")
	}
}

func TestNoDeliveryLifecycleCompletesPartialCallbackState(t *testing.T) {
	lifecycle := telemetry.NoDeliveryLifecycle{}
	completedAt := time.Now()
	event := &shared.CliEvent{
		LocalStartedAt:   completedAt.Add(-time.Second),
		LocalCompletedAt: &completedAt,
	}

	if err := lifecycle.Finish(context.Background(), event, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if event.DurationMs == nil || *event.DurationMs < 0 || !event.Success {
		t.Fatalf("partial callback completion was not finalized: %#v", event)
	}
}

func TestNoDeliveryLifecycleFinalizesWhenCallbackSetsDuration(t *testing.T) {
	lifecycle := telemetry.NoDeliveryLifecycle{}
	callbackDuration := int64(1)
	event := &shared.CliEvent{
		LocalStartedAt: time.Now().Add(-time.Second),
		DurationMs:     &callbackDuration,
	}
	callbackErr := errors.New("generation failed")

	if err := lifecycle.Finish(context.Background(), event, callbackErr); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if event.LocalCompletedAt == nil || event.DurationMs == nil || event.Success || event.Error == nil || *event.Error != callbackErr.Error() {
		t.Fatalf("duration-only callback state was not finalized: %#v", event)
	}
}

func TestTelemetryOptOutStillRunsAndFinalizesCallback(t *testing.T) {
	ctx := access.WithDirect(context.Background(), access.DisableTelemetry())
	callbackRan := false
	var event *shared.CliEvent

	err := telemetry.Run(ctx, shared.InteractionTypeTargetGenerate, func(_ context.Context, callbackEvent *shared.CliEvent) error {
		callbackRan = true
		event = callbackEvent
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !callbackRan || event == nil || event.LocalCompletedAt == nil || !event.Success {
		t.Fatalf("opt-out suppressed callback or local finalization: %#v", event)
	}
}

func TestEventContextAndDefaultLifecycleConcurrentRuns(t *testing.T) {
	const workers = 32
	var waitGroup sync.WaitGroup
	ids := make(chan string, workers)
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			err := telemetry.Run(context.Background(), shared.InteractionTypeTargetGenerate, func(ctx context.Context, event *shared.CliEvent) error {
				if telemetry.EventFromContext(ctx) != event {
					t.Error("concurrent run changed event identity")
				}
				ids <- event.ID
				return nil
			})
			if err != nil {
				t.Errorf("run: %v", err)
			}
		}()
	}
	waitGroup.Wait()
	close(ids)

	seen := make(map[string]struct{}, workers)
	for id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate event ID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestEventFromContextMissing(t *testing.T) {
	if telemetry.EventFromContext(context.Background()) != nil {
		t.Fatal("unexpected event in empty context")
	}
	if telemetry.EventFromContext(nil) != nil {
		t.Fatal("unexpected event in nil context")
	}
}

func TestNoDeliveryLifecycleClampsInvalidDurations(t *testing.T) {
	lifecycle := telemetry.NoDeliveryLifecycle{}
	for _, event := range []*shared.CliEvent{
		{},
		{LocalStartedAt: time.Now().Add(time.Hour)},
	} {
		if err := lifecycle.Finish(context.Background(), event, nil); err != nil {
			t.Fatalf("finish: %v", err)
		}
		if event.DurationMs == nil || *event.DurationMs != 0 {
			t.Fatalf("expected zero duration, got %v", event.DurationMs)
		}
	}
}
