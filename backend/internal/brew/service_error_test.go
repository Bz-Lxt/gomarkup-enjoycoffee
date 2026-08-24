package brew_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

type oneShotPourReadFailureRepo struct {
	brew.Repository
	record    *brew.Brew
	events    []brew.PourEvent
	readErr   error
	failReads int
}

func (r *oneShotPourReadFailureRepo) Get(_ context.Context, id int64) (*brew.Brew, error) {
	if r.record == nil || r.record.ID != id {
		return nil, fmt.Errorf("brew %d not found", id)
	}
	copyOfRecord := *r.record
	return &copyOfRecord, nil
}

func (r *oneShotPourReadFailureRepo) PourEvents(_ context.Context, _ int64) ([]brew.PourEvent, error) {
	if r.failReads > 0 {
		r.failReads--
		return nil, fmt.Errorf("read pour events: %w", r.readErr)
	}
	events := make([]brew.PourEvent, len(r.events))
	copy(events, r.events)
	return events, nil
}

func (r *oneShotPourReadFailureRepo) ReplacePourEvents(_ context.Context, brewID int64, events []brew.PourEvent) error {
	r.events = make([]brew.PourEvent, len(events))
	copy(r.events, events)
	for i := range r.events {
		r.events[i].BrewID = brewID
	}
	return nil
}

func TestAppendPourEventsReadFailureDoesNotOverwriteExistingCurve(t *testing.T) {
	readErr := errors.New("temporary read timeout")
	repo := &oneShotPourReadFailureRepo{
		record:  &brew.Brew{ID: 81, DoseMg: fixed.Mass(18_000)},
		readErr: readErr,
		events: []brew.PourEvent{
			{BrewID: 81, OffsetMs: 0, CumulativeMg: 0, Source: brew.SourceDevice, IdempotencyKey: "start"},
			{BrewID: 81, OffsetMs: 30_000, CumulativeMg: fixed.Mass(45_000), Source: brew.SourceDevice, IdempotencyKey: "bloom"},
		},
		failReads: 1,
	}
	service := brew.NewService(repo, nil, nil, nil)
	incoming := []brew.PourEvent{
		{BrewID: 81, OffsetMs: 60_000, CumulativeMg: fixed.Mass(120_000), Source: brew.SourceDevice, IdempotencyKey: "offline-tail"},
	}

	curve, accepted, err := service.AppendPourEvents(context.Background(), 81, incoming)
	if !errors.Is(err, readErr) {
		t.Errorf("read failure must be returned so the client can retry, got %v", err)
	}
	if curve != nil {
		t.Errorf("failed append must not return a curve, got %+v", curve)
	}
	if accepted != 0 {
		t.Errorf("failed append must not acknowledge events, accepted=%d", accepted)
	}

	retryCurve, retryAccepted, err := service.AppendPourEvents(context.Background(), 81, incoming)
	if err != nil {
		t.Fatalf("retry after the temporary read failure: %v", err)
	}
	if retryAccepted != 1 {
		t.Errorf("retry should accept the unsaved event exactly once, accepted=%d", retryAccepted)
	}
	if retryCurve == nil {
		t.Fatal("retry should return the complete persisted curve")
	}
	wantOffsets := []int{0, 30_000, 60_000}
	if len(retryCurve.Points) != len(wantOffsets) {
		t.Fatalf("retry curve should preserve two existing points and append one, got %d points", len(retryCurve.Points))
	}
	for i, want := range wantOffsets {
		if got := retryCurve.Points[i].OffsetMs; got != want {
			t.Errorf("point %d offset=%d, want %d", i, got, want)
		}
	}
}
