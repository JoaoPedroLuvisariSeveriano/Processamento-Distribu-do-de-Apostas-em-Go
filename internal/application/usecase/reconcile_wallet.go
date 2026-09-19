package usecase

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
)

type ReconcileWalletUseCase struct {
	walletRepo port.WalletRepository
	ledgerRepo port.LedgerRepository
	log        *zap.Logger
}

func NewReconcileWalletUseCase(walletRepo port.WalletRepository, ledgerRepo port.LedgerRepository, log *zap.Logger) *ReconcileWalletUseCase {
	return &ReconcileWalletUseCase{
		walletRepo: walletRepo,
		ledgerRepo: ledgerRepo,
		log:        log,
	}
}

type ReconcileWalletOutput struct {
	WalletID      uuid.UUID
	CurrentBalance money.Money
	LedgerCredits money.Money
	LedgerDebits  money.Money
	Reconstructed money.Money
	IsConsistent  bool
}

func (uc *ReconcileWalletUseCase) Execute(ctx context.Context, walletID uuid.UUID) (*ReconcileWalletOutput, error) {
	// 1. Fetch wallet to get current balance and currency
	w, err := uc.walletRepo.FindByID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("failed to find wallet %s: %w", walletID, err)
	}
	if w == nil {
		return nil, fmt.Errorf("wallet not found")
	}

	// 2. Sum credits and debits from ledger
	creditsCents, debitsCents, err := uc.ledgerRepo.SumByWalletID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("failed to sum ledger entries: %w", err)
	}

	currency := w.Currency()
	credits := money.New(creditsCents, currency)
	debits := money.New(debitsCents, currency)

	// 3. Reconstruct balance: credits - debits
	reconstructed, err := credits.Sub(debits)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate reconstructed balance: %w", err)
	}

	// 4. Check consistency
	isConsistent, _ := reconstructed.Equal(w.Balance())

	uc.log.Info("Wallet reconciliation completed",
		zap.String("walletId", walletID.String()),
		zap.Bool("isConsistent", isConsistent),
		zap.Int64("currentBalanceCents", w.Balance().Amount()),
		zap.Int64("reconstructedCents", reconstructed.Amount()),
	)

	return &ReconcileWalletOutput{
		WalletID:       w.ID(),
		CurrentBalance: w.Balance(),
		LedgerCredits:  credits,
		LedgerDebits:   debits,
		Reconstructed:  reconstructed,
		IsConsistent:   isConsistent,
	}, nil
}
