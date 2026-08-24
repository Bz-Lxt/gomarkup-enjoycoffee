package ws_test

import (
	"context"
	"testing"
	"time"

	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/config"
	"github.com/alkaid/enjoycoffee/internal/ws"
)

type shutdownPersister struct{}

func (shutdownPersister) AppendPourEvents(ctx context.Context, _ int64,
	incoming []brew.PourEvent) (*brew.PourCurve, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return &brew.PourCurve{}, len(incoming), nil
}

func (shutdownPersister) Get(_ context.Context, id int64) (*brew.View, error) {
	return &brew.View{ID: id}, nil
}

func TestStopAllReturnsWhileSimulationIsRunning(t *testing.T) {
	hub := ws.NewHub(shutdownPersister{}, config.PourSourceSimulator)
	if err := hub.StartSimulator(118); err != nil {
		t.Fatalf("start simulator: %v", err)
	}

	stopped := make(chan struct{})
	go func() {
		hub.StopAll()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("StopAll did not return while a simulation was running; graceful shutdown cannot finish")
	}

	if err := hub.StartSimulator(118); err != nil {
		t.Fatalf("start simulator again after StopAll: %v", err)
	}
	hub.StopAll()
}
