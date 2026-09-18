package usecase_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/usecase"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/config"
	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/postgres"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

// TestProcessWagerUseCase_Concurrency testa o Lock Pessimista simulando 50
// requisicoes simultaneas debitando saldo da mesma carteira.
// O teste exige um banco de dados real configurado em DATABASE_URL.
func TestProcessWagerUseCase_Concurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrency integration test in short mode")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Skipf("Skipping test due to missing config (ensure DATABASE_URL is set): %v", err)
	}

	log, _ := zap.NewDevelopment()
	lc := fxtest.NewLifecycle(t)

	pool, err := postgres.NewPool(lc, cfg, log)
	if err != nil {
		t.Fatalf("Failed to create db pool: %v", err)
	}
	
	// Iniciar hooks (conectar ao db)
	ctx := context.Background()
	if err := lc.Start(ctx); err != nil {
		t.Fatalf("Failed to start lifecycle: %v", err)
	}
	defer lc.Stop(ctx)

	walletRepo := postgres.NewWalletRepository(pool)
	txRepo := postgres.NewWagerTransactionRepository(pool)
	ledgerRepo := postgres.NewLedgerRepository(pool)
	outboxRepo := postgres.NewOutboxRepository(pool)
	runInTx := postgres.RunInTx(pool)

	openWalletUC := usecase.NewOpenWalletUseCase(walletRepo, txRepo, ledgerRepo, outboxRepo, runInTx, log)
	processWagerUC := usecase.NewProcessWagerUseCase(walletRepo, txRepo, ledgerRepo, outboxRepo, runInTx, log)

	// 1. Criar uma carteira com saldo inicial de 50000 (ex: $500.00)
	playerID := uuid.New()
	initialBalance := money.New(50000, "BRL")
	openOut, err := openWalletUC.Execute(ctx, usecase.OpenWalletInput{
		PlayerID:       playerID,
		Currency:       "BRL",
		InitialBalance: initialBalance,
		CorrelationID:  uuid.New(),
	})
	if err != nil {
		t.Fatalf("Failed to open wallet: %v", err)
	}

	walletID := openOut.Wallet.ID()

	// 2. Disparar 50 apostas simultaneas de 1000 (ex: $10.00)
	const numRequests = 50
	var wg sync.WaitGroup
	wg.Add(numRequests)

	successCount := 0
	errorCount := 0
	var mu sync.Mutex

	betAmount := money.New(1000, "BRL")

	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			defer wg.Done()

			input := usecase.ProcessWagerInput{
				ExternalTransactionID: fmt.Sprintf("ext-tx-test-%d", idx),
				ProviderID:            "test-provider",
				IdempotencyKey:        uuid.New().String(),
				PlayerID:              playerID,
				WalletID:              walletID,
				RoundID:               "round-1",
				GameID:                "game-1",
				Kind:                  transaction.KindBet,
				Amount:                betAmount,
				CorrelationID:         uuid.New(),
			}

			// Simula latencia variavel de redes diferentes
			time.Sleep(time.Duration(idx%5) * time.Millisecond)

			_, err := processWagerUC.Execute(ctx, input)
			
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errorCount++
			} else {
				successCount++
			}
		}(i)
	}

	wg.Wait()

	// 3. Validar estado final (Saldo = 50000 - (50 * 1000) = 0)
	finalWallet, err := walletRepo.FindByID(ctx, walletID)
	if err != nil {
		t.Fatalf("Failed to fetch final wallet: %v", err)
	}

	expectedBalance := money.New(0, "BRL")

	ok, err := finalWallet.Balance().Equal(expectedBalance)
	if err != nil || !ok {
		t.Errorf("Expected balance %s, got %s (err: %v)", expectedBalance, finalWallet.Balance(), err)
	}

	if successCount != numRequests {
		t.Errorf("Expected %d successful bets, but got %d (Errors: %d)", numRequests, successCount, errorCount)
	}

	t.Logf("Concurrency test passed: %d simultaneous bets processed safely.", successCount)
}
