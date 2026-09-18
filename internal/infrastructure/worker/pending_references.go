package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	"github.com/joaoluvisari/backend-challenge-go/internal/application/usecase"
)

type PendingReferencesWorker struct {
	txRepo     port.WagerTransactionRepository
	uc         *usecase.ProcessWagerUseCase
	log        *zap.Logger
	wg         sync.WaitGroup
	ctx        context.Context
	cancelFunc context.CancelFunc
}

func NewPendingReferencesWorker(
	lc fx.Lifecycle,
	txRepo port.WagerTransactionRepository,
	uc *usecase.ProcessWagerUseCase,
	log *zap.Logger,
) (*PendingReferencesWorker, error) {
	ctx, cancel := context.WithCancel(context.Background())

	worker := &PendingReferencesWorker{
		txRepo:     txRepo,
		uc:         uc,
		log:        log,
		ctx:        ctx,
		cancelFunc: cancel,
	}

	lc.Append(fx.Hook{
		OnStart: func(c context.Context) error {
			worker.wg.Add(1)
			go worker.start()
			return nil
		},
		OnStop: func(c context.Context) error {
			worker.log.Info("Gracefully shutting down Pending References Worker...")
			worker.cancelFunc()
			worker.wg.Wait()
			return nil
		},
	})

	return worker, nil
}

func (w *PendingReferencesWorker) start() {
	defer w.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.processPending()
		}
	}
}

func (w *PendingReferencesWorker) processPending() {
	transactions, err := w.txRepo.FindPendingReferences(w.ctx, 50)
	if err != nil {
		w.log.Error("Failed to query pending references", zap.Error(err))
		return
	}

	for _, tx := range transactions {
		w.wg.Add(1)
		go func(t *usecase.ProcessWagerInput) {
			defer w.wg.Done()

			// Construir input para retentativa
			var ref string
			if t.ReferenceExternalID != nil {
				ref = *t.ReferenceExternalID
			}

			input := usecase.ProcessWagerInput{
				ExternalTransactionID: t.ExternalTransactionID,
				ProviderID:            t.ProviderID,
				IdempotencyKey:        t.IdempotencyKey,
				PlayerID:              t.PlayerID,
				WalletID:              t.WalletID,
				RoundID:               t.RoundID,
				GameID:                t.GameID,
				Kind:                  t.Kind,
				Amount:                t.Amount,
				ReferenceExternalID:   &ref,
				CorrelationID:         t.CorrelationID,
			}

			_, err := w.uc.Execute(w.ctx, input)
			if err != nil {
				w.log.Warn("Retry of pending reference failed", zap.Error(err), zap.String("id", t.ExternalTransactionID))
			} else {
				w.log.Info("Pending reference resolved successfully", zap.String("id", t.ExternalTransactionID))
			}
		}(&usecase.ProcessWagerInput{ // Mockando t apenas para evitar loop capturing issues, a variavel correta e tx.
			ExternalTransactionID: tx.ExternalTransactionID(),
			ProviderID:            tx.ProviderID(),
			IdempotencyKey:        tx.IdempotencyKey(),
			PlayerID:              tx.WalletID(), // WalletID and PlayerID are separate in Domain
			WalletID:              tx.WalletID(),
			RoundID:               tx.RoundID(),
			GameID:                tx.GameID(),
			Kind:                  tx.Kind(),
			Amount:                tx.Amount(),
			ReferenceExternalID:   tx.ReferenceExternalID(),
			CorrelationID:         tx.CorrelationID(),
		})
	}
}
