package server

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/whywaita/rfid-poker/pkg/query"
	"github.com/whywaita/rfid-poker/pkg/store"
)

// equityRequests holds at most one pending request, so requests that arrive while a
// calculation is running collapse into a single follow-up run on the latest table state.
// Dealing 10 hands therefore costs two calculations instead of a queue of ten.
var equityRequests = make(chan struct{}, 1)

// requestEquityCalc asks the equity worker to recalculate; it never blocks.
func requestEquityCalc() {
	select {
	case equityRequests <- struct{}{}:
	default:
		// a recalculation is already pending and will see the latest state
	}
}

// runEquityWorker recalculates equity one request at a time and notifies clients after each run.
func runEquityWorker(ctx context.Context, conn *sql.DB) {
	logger := slog.With("method", "runEquityWorker")
	for {
		select {
		case <-ctx.Done():
			return
		case <-equityRequests:
			if err := store.CalcEquity(ctx, query.New(conn)); err != nil {
				logger.WarnContext(ctx, "store.CalcEquity", "error", err)
			}
			notifyClients()
		}
	}
}
