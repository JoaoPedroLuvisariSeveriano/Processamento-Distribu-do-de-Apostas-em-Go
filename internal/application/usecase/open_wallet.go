package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"

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
//  2. Criar o Aggregate Root Wallet via dominio (valida invariantes).
//  3. Se saldo inicial > 0: criar OPENING transaction + ledger entry + outbox events.
//  4. Persistir tudo atomicamente via RunInTx (UoW).
//
// A carteira + transacao + ledger + outbox sao inseridos NA MESMA TRANSACAO SQL.
// Qualquer falha reverte tudo — sem estados parciais persistidos.
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
// FLUXO DETALHADO:
//
//  1. [FORA DA TX] Verificar idempotencia de abertura por (playerID, currency).
//     Se existir -> retornar existente sem criar nada novo.
//
//  2. [DOMINIO] Criar Wallet em memoria validando invariantes.
//
//  3. [TRANSACAO ATOMICA] RunInTx:
//     a. INSERT wallet
//     b. Se saldo inicial > 0:
//        - Criar OPENING WagerTransaction (interna, ja PROCESSED)
//        - wallet.Credit() -> gera WalletLedgerEntry (invariante validada no dominio)
//        - INSERT ledger entry
//        - INSERT OutboxEvent(WagerTransactionProcessed)
//        - INSERT OutboxEvent(WalletBalanceChanged)
//     c. COMMIT (ou ROLLBACK automatico em caso de erro)
func (uc *OpenWalletUseCase) Execute(ctx context.Context, input OpenWalletInput) (*OpenWalletOutput, error) {
	// ==========================================================================
	// PASSO 1: Verificar idempotencia de abertura [leitura fora da TX]
	// A chave natural de uma carteira e (playerID, currency).
	// Sem tx aqui — leitura simples, sem lock.
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
	// PASSO 2: Criar Wallet no dominio [apenas em memoria]
	// Nenhuma query de banco aqui — o dominio valida invariantes e retorna erro
	// se a entrada for invalida (moeda invalida, saldo negativo, etc.).
	// ==========================================================================
	newWallet, err := wallet.NewWallet(input.PlayerID, input.Currency, input.InitialBalance)
	if err != nil {
		return nil, fmt.Errorf("criar wallet no dominio: %w", err)
	}

	// ==========================================================================
	// PASSO 3: Persistir atomicamente dentro de uma transacao SQL
	// RunInTx gerencia Begin/Commit/Rollback. Qualquer erro em doInsert
	// causa rollback automatico via defer no RunInTx.
	// ==========================================================================
	txErr := uc.runInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return uc.doInsert(ctx, tx, newWallet, input)
	})
	if txErr != nil {
		// Race condition: dois requests simultaneos podem passar pela
		// verificacao do PASSO 1. Apenas um inserira com sucesso;
		// o segundo recebe ErrWalletAlreadyExists via UNIQUE CONSTRAINT.
		// Tratamos isso retornando a carteira existente (idempotente).
		if errors.Is(txErr, domain.ErrWalletAlreadyExists) {
			existing, lookupErr := uc.walletRepo.FindByPlayerAndCurrency(ctx, input.PlayerID, input.Currency)
			if lookupErr != nil {
				return nil, fmt.Errorf("buscar carteira apos conflito de criacao: %w", lookupErr)
			}
			return &OpenWalletOutput{Wallet: existing, Created: false}, nil
		}
		return nil, fmt.Errorf("persistir abertura de carteira: %w", txErr)
	}

	uc.log.Info("carteira criada com sucesso",
		zap.String("walletId", newWallet.ID().String()),
		zap.String("playerId", input.PlayerID.String()),
		zap.String("currency", input.Currency),
		zap.Int64("balanceCents", newWallet.Balance().Amount()),
	)
	return &OpenWalletOutput{Wallet: newWallet, Created: true}, nil
}

// doInsert executa as insercoes dentro de uma transacao SQL ativa (pgx.Tx).
// Responsabilidades:
//  - Inserir a Wallet
//  - Se saldo inicial > 0: inserir OPENING tx, ledger entry e outbox events
func (uc *OpenWalletUseCase) doInsert(ctx context.Context, tx pgx.Tx, w *wallet.Wallet, input OpenWalletInput) error {
	now := time.Now().UTC()

	// --- 3a. Inserir a Wallet no banco ---
	if err := uc.walletRepo.Create(ctx, tx, w); err != nil {
		return fmt.Errorf("inserir wallet: %w", err)
	}

	// Se saldo zero: abertura concluida aqui. Sem movimentacao financeira,
	// sem ledger entry e sem outbox event.
	if input.InitialBalance.IsZero() {
		return nil
	}

	// --- 3b. Criar OPENING WagerTransaction (interna) ---
	// A OPENING registra o deposito inicial no historico contabil.
	// Ja nasce como PROCESSED — nao precisa de processamento assincrono.
	openingTx := transaction.NewOpeningTransaction(w.ID(), w.PlayerID(), input.InitialBalance)
	if err := openingTx.MarkAsProcessed(w.Balance().Amount()); err != nil {
		return fmt.Errorf("marcar OPENING como processada: %w", err)
	}
	if _, err := uc.txRepo.TryCreate(ctx, tx, openingTx); err != nil {
		return fmt.Errorf("inserir OPENING transaction: %w", err)
	}

	// --- 3c. Gerar WalletLedgerEntry via dominio ---
	// Criamos uma wallet temporaria com saldo ZERO para que o ledger entry
	// tenha balanceBefore=0 e balanceAfter=initialBalance.
	// A invariante (balanceAfter = balanceBefore + amount) e verificada no dominio.
	// money.Zero(currency) cria um Money com valor 0 para a moeda da carteira.
	zeroBalance := money.Zero(w.Currency())
	tmpWallet, _ := wallet.NewWallet(w.PlayerID(), w.Currency(), zeroBalance)
	ledgerEntry, err := tmpWallet.Credit(input.InitialBalance, openingTx.ID())
	if err != nil {
		return fmt.Errorf("gerar ledger entry OPENING: %w", err)
	}

	// Reconstruir o ledger entry com o ID correto da carteira real.
	// tmpWallet tem um UUID diferente de w; o ledger deve referenciar w.
	correctEntry := wallet.RehydrateLedgerEntry(
		ledgerEntry.ID(),
		w.ID(), // ID da carteira real (nao do tmpWallet)
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

	// --- 3d. OutboxEvent: WagerTransactionProcessed ---
	// Notifica consumidores que a operacao OPENING foi concluida.
	processedEvt := domainevent.NewWagerTransactionProcessed(w.ID(), input.CorrelationID,
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
	outboxEvt1, err := newOutboxEvent(w.ID(), domainevent.EventTypeWagerTransactionProcessed, processedEvt, now)
	if err != nil {
		return err
	}
	if err := uc.outbox.Create(ctx, tx, outboxEvt1); err != nil {
		return fmt.Errorf("inserir outbox WagerTransactionProcessed: %w", err)
	}

	// --- 3e. OutboxEvent: WalletBalanceChanged ---
	// Notifica que o saldo da carteira foi alterado (de 0 para initialBalance).
	balanceEvt := domainevent.NewWalletBalanceChanged(w.ID(), input.CorrelationID,
		domainevent.WalletBalanceChangedData{
			WalletID:      w.ID(),
			TransactionID: openingTx.ID(),
			Direction:     string(wallet.DirectionCredit),
			Money:         moneyToPayload(input.InitialBalance),
			BalanceBefore: moneyToPayload(money.Zero(w.Currency())),
			BalanceAfter:  moneyToPayload(w.Balance()),
			WalletVersion: w.Version(),
		},
	)
	outboxEvt2, err := newOutboxEvent(w.ID(), domainevent.EventTypeWalletBalanceChanged, balanceEvt, now)
	if err != nil {
		return err
	}
	if err := uc.outbox.Create(ctx, tx, outboxEvt2); err != nil {
		return fmt.Errorf("inserir outbox WalletBalanceChanged: %w", err)
	}

	return nil
}
