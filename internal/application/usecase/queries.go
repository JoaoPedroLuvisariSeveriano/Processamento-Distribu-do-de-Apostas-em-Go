package usecase

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

type QueryUseCase struct {
	walletRepo port.WalletRepository
	ledgerRepo port.LedgerRepository
	txRepo     port.WagerTransactionRepository
}

func NewQueryUseCase(
	walletRepo port.WalletRepository,
	ledgerRepo port.LedgerRepository,
	txRepo port.WagerTransactionRepository,
) *QueryUseCase {
	return &QueryUseCase{
		walletRepo: walletRepo,
		ledgerRepo: ledgerRepo,
		txRepo:     txRepo,
	}
}

func (uc *QueryUseCase) GetWallet(ctx context.Context, walletID uuid.UUID) (*wallet.Wallet, error) {
	w, err := uc.walletRepo.FindByID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}
	return w, nil
}

func (uc *QueryUseCase) GetWalletLedger(ctx context.Context, walletID uuid.UUID, afterID *uuid.UUID, limit int) ([]*wallet.WalletLedgerEntry, error) {
	entries, err := uc.ledgerRepo.FindByWalletID(ctx, walletID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet ledger: %w", err)
	}
	return entries, nil
}

func (uc *QueryUseCase) GetTransaction(ctx context.Context, transactionID uuid.UUID) (*transaction.WagerTransaction, error) {
	t, err := uc.txRepo.FindByID(ctx, transactionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}
	return t, nil
}

func (uc *QueryUseCase) GetProviderTransaction(ctx context.Context, providerID, externalID string) (*transaction.WagerTransaction, error) {
	t, err := uc.txRepo.FindByProviderAndExternalID(ctx, providerID, externalID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider transaction: %w", err)
	}
	return t, nil
}
