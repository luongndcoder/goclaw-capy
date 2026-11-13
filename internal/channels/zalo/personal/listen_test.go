package personal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/config"
)

func TestChannelRestartDelay_Progression(t *testing.T) {
	expectedDelays := []time.Duration{
		2 * time.Second,  // attempt 0 (1st)
		4 * time.Second,  // attempt 1 (2nd)
		8 * time.Second,  // attempt 2 (3rd)
		16 * time.Second, // attempt 3 (4th)
		32 * time.Second, // attempt 4 (5th)
		60 * time.Second, // attempt 5 (6th, capped at maxChannelBackoff)
		60 * time.Second, // attempt 6
		60 * time.Second, // attempt 7
		60 * time.Second, // attempt 8
		60 * time.Second, // attempt 9 (10th)
		5 * time.Minute,  // attempt 10 (11th, extendedChannelBackoff)
		5 * time.Minute,  // attempt 11
		5 * time.Minute,  // attempt 20
	}

	for attempt, want := range expectedDelays {
		got := channelRestartDelay(attempt)
		if got != want {
			t.Errorf("attempt %d: got delay %v, want %v", attempt, got, want)
		}
	}
}

func TestRestartWithBackoff_ContinuesPastMaxRestarts(t *testing.T) {
	ch, err := New(config.ZaloPersonalConfig{
		DMPolicy:    "open",
		GroupPolicy: "open",
	}, bus.New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var attempts int
	var recordedDelays []time.Duration

	// Mock sleep to be instantaneous and record requested durations
	ch.sleepFn = func(ctx context.Context, d time.Duration) bool {
		recordedDelays = append(recordedDelays, d)
		return true
	}

	// Fail first 12 attempts, succeed on attempt 13 (index 12)
	ch.restartFn = func(ctx context.Context) error {
		attempts++
		if attempts < 13 {
			return errors.New("transient upstream failure")
		}
		return nil
	}

	ok := ch.restartWithBackoff(context.Background())
	if !ok {
		t.Fatalf("expected restartWithBackoff to succeed eventually, got false")
	}

	if attempts != 13 {
		t.Errorf("expected 13 attempts, got %d", attempts)
	}

	if len(recordedDelays) != 13 {
		t.Fatalf("expected 13 recorded delays, got %d", len(recordedDelays))
	}

	// Verify delays for attempts 0 to 9 are exponential up to 60s
	for i := range maxChannelRestarts {
		want := channelRestartDelay(i)
		if recordedDelays[i] != want {
			t.Errorf("delay[%d] = %v, want %v", i, recordedDelays[i], want)
		}
	}

	// Verify delays for attempts 10+ are extendedChannelBackoff (5m)
	for i := maxChannelRestarts; i < 13; i++ {
		if recordedDelays[i] != extendedChannelBackoff {
			t.Errorf("delay[%d] = %v, want extendedChannelBackoff (%v)", i, recordedDelays[i], extendedChannelBackoff)
		}
	}
}

func TestRestartWithBackoff_ExitsOnContextDone(t *testing.T) {
	ch, err := New(config.ZaloPersonalConfig{
		DMPolicy:    "open",
		GroupPolicy: "open",
	}, bus.New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled immediately

	ch.restartFn = func(ctx context.Context) error {
		t.Fatal("restartFn should not be called when context is already canceled")
		return nil
	}

	ok := ch.restartWithBackoff(ctx)
	if ok {
		t.Errorf("expected restartWithBackoff to return false on canceled context")
	}
}

func TestRestartWithBackoff_ExitsOnStopCh(t *testing.T) {
	ch, err := New(config.ZaloPersonalConfig{
		DMPolicy:    "open",
		GroupPolicy: "open",
	}, bus.New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	close(ch.stopCh)

	ch.restartFn = func(ctx context.Context) error {
		t.Fatal("restartFn should not be called when channel is stopped")
		return nil
	}

	ok := ch.restartWithBackoff(context.Background())
	if ok {
		t.Errorf("expected restartWithBackoff to return false when stopCh is closed")
	}
}

func TestSleepContext_DefaultSelect(t *testing.T) {
	ch, err := New(config.ZaloPersonalConfig{
		DMPolicy:    "open",
		GroupPolicy: "open",
	}, bus.New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Sleep with expired context returns false immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ch.sleepContext(ctx, time.Hour) {
		t.Errorf("expected false for canceled context")
	}

	// Sleep with stopCh closed returns false immediately
	close(ch.stopCh)
	if ch.sleepContext(context.Background(), time.Hour) {
		t.Errorf("expected false for closed stopCh")
	}
}
