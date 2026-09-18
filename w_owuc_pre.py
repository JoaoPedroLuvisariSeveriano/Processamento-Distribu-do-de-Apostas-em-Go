import os

open_wallet_go = """\
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	domainevent "github.com/joaoluvisari/backend-challenge-go/internal/domain/event"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// OpenWalletUseCase orquestra a criacao de uma nova carteira de jogador.
//
// Responsabilidades:
//  1. Verificar se a carteira ja existe (idempotencia de abertura).
//  2. Criar o Aggregate Root Wallet via dominio.
//  3. Se saldo inicial > 0: criar OPENING transaction + ledger entry + outbox events.
//  4. Persistir tudo atomicamente via RunInTx.
//
// A carteira + transacao + ledger + outbox sao inseridos NA MESMA TRANSACAO SQL.
// Se qualquer etapa falhar, NADA e persistido (atomicidade garantida).
type OpenWalletUseCase struct {
	walletRepo port.WalletRepository
	txRepo     port.WagerTransactionRepository
	ledger     port.LedgerRepository
	outbox     port.OutboxRepository
	runInTx    port.RunInTxFunc
	log        *zap.Logger
}

// NewOpenWalletUseCase cria o use case com dependencias injetadas pelo Uber Fx.
func NewOpenWalletUseCase(
	walletRepo port.WalletRepository,
	txRepo port.WagerTransactionRepository,
	ledger port.LedgerRepository,
	outbox port.OutboxRepository,
	runInTx port.RunInTxFunc,
	log *zap.Logger,
) *OpenWalletUseCase {
	return &OpenWalletUseCase{
		walletRepo: walletRepo,
		txRepo:     txRepo,
		ledger:     ledger,
		outbox:     outbox,
		runInTx:    runInTx,
		log:        log,
	}
}

// Execute executa o caso de uso de abertura de carteira.
//
// FLUXO:
//  1. Verificar se (playerID, currency) ja existe -> retornar existente (idempotente).
//  2. Criar Wallet via dominio (valida invariantes, gera UUID, seta versao=1).
//  3. RunInTx:
//     a. Inserir Wallet no banco.
//     b. Se saldo inicial > 0:
//        - Criar OPENING WagerTransaction (internal, ja marcada como PROCESSED).
//        - Wallet.Credit(initialBalance) -> gera WalletLedgerEntry.
//        - Inserir ledger entry.
//        - Inserir OutboxEvent(WagerTransactionProcessed).
//        - Inserir OutboxEvent(WalletBalanceChanged).
//  4. Retornar Wallet criada.
func (uc *OpenWalletUseCase) Execute(ctx context.Context, input OpenWalletInput) (*OpenWalletOutput, error) {
	// ==========================================================================
	// PASSO 1: Verificar idempotencia de abertura
	// A chave natural de uma carteira e (playerID, currency).
	// Se ja existir, retornamos sem criar duplicata.
	// ==========================================================================
	existing, err := uc.walletRepo.FindByPlayerAndCurrency(ctx, input.PlayerID, input.Currency)
	if err != nil && !errors.Is(err, domain.ErrWalletNotFound) {
		return nil, fmt.Errorf("verificar carteira existente: %w", err)
	}
	if existing != nil {
		uc.log.Info("carteira ja existe — retornando idempotente",
			zap.String("walletId", existing.ID().String()),
			zap.String("playerId", input.PlayerID.String()),
		)
		return &OpenWalletOutput{Wallet: existing, Created: false}, nil
	}

	// ==========================================================================
	// PASSO 2: Criar o Aggregate Root via dominio
	// O dominio valida invariantes: saldo nao negativo, currency valida, etc.
	// Nenhuma logica de banco ainda — apenas objetos em memoria.
	// ==========================================================================
	newWallet, err := wallet.NewWallet(input.PlayerID, input.Currency, input.InitialBalance)
	if err != nil {
		return nil, fmt.Errorf("criar wallet no dominio: %w", err)
	}

	// ==========================================================================
	// PASSO 3: Persistir atomicamente via RunInTx
	// Tudo que acontece dentro desta funcao e commitado ou revertido junto.
	// ==========================================================================
	var resultWallet *wallet.Wallet

	err = uc.runInTx(ctx, func(ctx context.Context, tx interface{ /* pgx.Tx */ }) error {
		return uc.persistWalletOpening(ctx, tx, newWallet, input)
	})
	if err != nil {
		// Tratar tentativa concorrente de criar a mesma carteira (race condition).
		// Dois requests simultaneos podem passar pela verificacao do PASSO 1 antes
		// de qualquer um inserir. O segundo recebera ErrWalletAlreadyExists.
		if errors.Is(err, domain.ErrWalletAlreadyExists) {
			existing, lookupErr := uc.walletRepo.FindByPlayerAndCurrency(ctx, input.PlayerID, input.Currency)
			if lookupErr != nil {
				return nil, fmt.Errorf("buscar carteira apos conflito de criacao: %w", lookupErr)
			}
			return &OpenWalletOutput{Wallet: existing, Created: false}, nil
		}
		return nil, fmt.Errorf("persistir abertura de carteira: %w", err)
	}

	resultWallet = newWallet

	uc.log.Info("carteira criada com sucesso",
		zap.String("walletId", resultWallet.ID().String()),
		zap.String("playerId", input.PlayerID.String()),
		zap.String("currency", input.Currency),
		zap.Int64("balanceCents", resultWallet.Balance().Amount()),
	)

	return &OpenWalletOutput{Wallet: resultWallet, Created: true}, nil
}

// persistWalletOpening executa as insercoes dentro da transacao SQL.
// Separado do Execute para manter o fluxo principal legivel.
func (uc *OpenWalletUseCase) persistWalletOpening(ctx context.Context, tx interface{}, w *wallet.Wallet, input OpenWalletInput) error {
	// Esta funcao e chamada por runInTx que passa pgx.Tx.
	// Usamos a assinatura com port.TxFunc para compatibilidade.
	// A implementacao real recebe pgx.Tx via RunInTx — aqui fazemos um cast.
	return uc.runInTx(ctx, func(ctx context.Context, pgxTx port.PgxTx) error {
		return uc.doInsert(ctx, pgxTx, w, input)
	})
}

// doInsert e chamado com pgx.Tx concreto e executa as insercoes.
func (uc *OpenWalletUseCase) doInsert(ctx context.Context, tx port.PgxTx, w *wallet.Wallet, input OpenWalletInput) error {
	now := time.Now().UTC()

	// --- 3a. Inserir a Wallet ---
	if err := uc.walletRepo.Create(ctx, tx, w); err != nil {
		return fmt.Errorf("inserir wallet: %w", err)
	}

	// Se o saldo inicial e zero, a abertura e concluida aqui.
	// Nao criamos ledger entry nem outbox event (nenhuma movimentacao ocorreu).
	if input.InitialBalance.IsZero() {
		return nil
	}

	// --- 3b. Criar OPENING WagerTransaction (uso exclusivo interno) ---
	// A OPENING registra o saldo inicial no historico contabil.
	// Ja nasce como PROCESSED (nao precisa de processamento assincrono).
	openingTx := transaction.NewOpeningTransaction(w.ID(), w.PlayerID(), input.InitialBalance)
	if err := openingTx.MarkAsProcessed(w.Balance().Amount()); err != nil {
		return fmt.Errorf("marcar OPENING como processada: %w", err)
	}

	// A OPENING usa TryCreate (nao TryCreate com ON CONFLICT) pois nunca
	// havera conflito de idempotency_key (OPENING nao tem key externa).
	if _, err := uc.txRepo.TryCreate(ctx, tx, openingTx); err != nil {
		return fmt.Errorf("inserir OPENING transaction: %w", err)
	}

	// --- 3c. Creditar saldo inicial na Wallet (gera WalletLedgerEntry) ---
	// Aqui usamos a VERSAO COM SALDO ZERO para gerar o ledger entry correto.
	// A wallet em memoria ja tem o saldo inicial, entao precisamos de uma
	// wallet zerada para gerar o ledger entry com balanceBefore=0.
	zeroWallet, _ := wallet.NewWallet(w.PlayerID(), w.Currency(), input.InitialBalance.Zero())
	ledgerEntry, err := zeroWallet.Credit(input.InitialBalance, openingTx.ID())
	if err != nil {
		return fmt.Errorf("gerar ledger entry OPENING: %w", err)
	}

	// Reseitar o walletID correto no ledger entry (o zeroWallet tem ID diferente)
	// Como WalletLedgerEntry e imutavel, reconstruimos via RehydrateLedgerEntry.
	correctEntry := wallet.RehydrateLedgerEntry(
		ledgerEntry.ID(),
		w.ID(), // ID correto da carteira real
		openingTx.ID(),
		wallet.DirectionCredit,
		ledgerEntry.Amount().Amount(),
		ledgerEntry.BalanceBefore().Amount(),
		ledgerEntry.BalanceAfter().Amount(),
		w.Currency(),
		now,
	)

	if err := uc.ledger.Create(ctx, tx, correctEntry); err != nil {
		return fmt.Errorf("inserir ledger entry OPENING: %w", err)
	}

	// --- 3d. Inserir OutboxEvent: WagerTransactionProcessed ---
	// Notifica outros sistemas que a OPENING foi concluida.
	processedEvent := domainevent.NewWagerTransactionProcessed(
		w.ID(),
		input.CorrelationID,
		domainevent.WagerTransactionProcessedData{
			TransactionID:         openingTx.ID(),
			ExternalTransactionID: "",
			ProviderID:            "",
			WalletID:              w.ID(),
			PlayerID:              w.PlayerID(),
			RoundID:               "",
			GameID:                "",
			Kind:                  string(transaction.KindOpening),
			Money:                 moneyToPayload(input.InitialBalance),
		},
	)
	outboxProcessed, err := newOutboxEvent(w.ID(), domainevent.EventTypeWagerTransactionProcessed, processedEvent, now)
	if err != nil {
		return fmt.Errorf("criar outbox event WagerTransactionProcessed: %w", err)
	}
	if err := uc.outbox.Create(ctx, tx, outboxProcessed); err != nil {
		return fmt.Errorf("inserir outbox event WagerTransactionProcessed: %w", err)
	}

	// --- 3e. Inserir OutboxEvent: WalletBalanceChanged ---
	// Notifica listeners que o saldo da carteira mudou.
	balanceEvent := domainevent.NewWalletBalanceChanged(
		w.ID(),
		input.CorrelationID,
		domainevent.WalletBalanceChangedData{
			WalletID:      w.ID(),
			TransactionID: openingTx.ID(),
			Direction:     string(wallet.DirectionCredit),
			Money:         moneyToPayload(input.InitialBalance),
			BalanceBefore: moneyToPayload(input.InitialBalance.Zero()),
			BalanceAfter:  moneyToPayload(w.Balance()),
			WalletVersion: w.Version(),
		},
	)
	outboxBalance, err := newOutboxEvent(w.ID(), domainevent.EventTypeWalletBalanceChanged, balanceEvent, now)
	if err != nil {
		return fmt.Errorf("criar outbox event WalletBalanceChanged: %w", err)
	}
	if err := uc.outbox.Create(ctx, tx, outboxBalance); err != nil {
		return fmt.Errorf("inserir outbox event WalletBalanceChanged: %w", err)
	}

	return nil
}
"""

with open("internal/application/usecase/open_wallet.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(open_wallet_go)

print("open_wallet.go criado (versao preliminar)")
