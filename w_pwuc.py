import os

process_wager_go = """\
// Package usecase implementa os casos de uso de negocio do servico de apostas.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	domainevent "github.com/joaoluvisari/backend-challenge-go/internal/domain/event"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// ProcessWagerUseCase orquestra o processamento de uma operacao financeira de aposta.
//
// E o caso de uso mais critico do sistema — lida com:
//   - Idempotencia duravel (replay seguro de HTTP e SQS)
//   - Concorrencia entre instancias (lock pessimista)
//   - Integridade financeira (debito, credito, reversoes)
//   - Auditoria imutavel (ledger append-only)
//   - Publicacao garantida de eventos (Transactional Outbox)
//
// Suporta os tipos: BET, WIN, LOSS, REFUND, ROLLBACK.
type ProcessWagerUseCase struct {
	walletRepo port.WalletRepository
	txRepo     port.WagerTransactionRepository
	ledger     port.LedgerRepository
	outbox     port.OutboxRepository
	runInTx    port.RunInTxFunc
	log        *zap.Logger
}

// NewProcessWagerUseCase cria o use case com dependencias injetadas pelo Uber Fx.
func NewProcessWagerUseCase(
	walletRepo port.WalletRepository,
	txRepo port.WagerTransactionRepository,
	ledger port.LedgerRepository,
	outbox port.OutboxRepository,
	runInTx port.RunInTxFunc,
	log *zap.Logger,
) *ProcessWagerUseCase {
	return &ProcessWagerUseCase{
		walletRepo: walletRepo,
		txRepo:     txRepo,
		ledger:     ledger,
		outbox:     outbox,
		runInTx:    runInTx,
		log:        log,
	}
}

// Execute processa uma operacao de wager de ponta a ponta.
//
// =============================================================================
// ORDEM DAS OPERACOES (COMENTADA PARA ENTREVISTA):
// =============================================================================
//
//  [FORA DA TRANSACAO — leituras sem lock, para performance]
//  PASSO 1: Calcular hash do payload para idempotencia
//           O hash identifica unicamente o CONTEUDO SEMANTICO da operacao.
//           Excluimos metadados de transporte (headers, timestamps).
//
//  PASSO 2: Verificar idempotencia (lookup rapido por idempotency_key)
//           Se encontrado com hash igual -> replay: retorna resultado original.
//           Se encontrado com hash diferente -> conflito: HTTP 409.
//           Se nao encontrado -> operacao nova, prosseguir.
//
//  PASSO 3: Validar tipo de operacao
//           OPENING nao pode vir de fora. Valida Kind.
//
//  [DENTRO DA TRANSACAO — atomicidade total]
//  PASSO 4: Iniciar transacao SQL (BEGIN)
//           Tudo daqui para frente e atomico: ou tudo comita, ou tudo reverte.
//
//  PASSO 5: Adquirir lock pessimista na Wallet (SELECT FOR UPDATE)
//           Bloqueia outras transacoes de ler/escrever esta carteira.
//           Garante que o saldo lido aqui e o saldo real — sem race condition.
//
//  PASSO 6: Claim da idempotency_key (INSERT ... ON CONFLICT DO NOTHING)
//           Dentro da tx, com o lock ativo, tentamos "reclamar" a key.
//           Se 0 rows afetadas: outra transacao ganhou a corrida (race resolvido).
//           Se 1 row afetada: somos os donos desta operacao, prosseguir.
//
//  PASSO 7: Aplicar logica de negocio por tipo (BET, WIN, LOSS, REFUND, ROLLBACK)
//           - BET/WIN/LOSS: aplica diretamente na Wallet.
//           - REFUND/ROLLBACK: busca referencia, valida e aplica reversao.
//           - Se referencia nao encontrada: PENDING_REFERENCE (retry posterior).
//
//  PASSO 8: Salvar resultados (UPDATE wallet + INSERT ledger + UPDATE tx)
//           Persistir estado final dentro da transacao.
//
//  PASSO 9: Inserir OutboxEvent(s) na mesma transacao
//           Garante que eventos sejam publicados EXATAMENTE UMA VEZ,
//           mesmo se o processo crashar entre o commit e a publicacao SQS.
//
//  PASSO 10: COMMIT
//            Torna todas as mudancas visiveis atomicamente.
//            O defer Rollback e inofensivo apos commit bem-sucedido.
func (uc *ProcessWagerUseCase) Execute(ctx context.Context, input ProcessWagerInput) (*ProcessWagerOutput, error) {
	// ==========================================================================
	// PASSO 1: Calcular hash do payload canonico para idempotencia
	//
	// O hash e calculado sobre os campos de NEGOCIO (o que a operacao FAZ),
	// excluindo metadados de transporte (quando chegou, qual instancia processou).
	// Isso garante que dois envios identicos da mesma operacao producam o mesmo hash.
	// ==========================================================================
	payloadHash, err := ComputePayloadHash(input)
	if err != nil {
		return nil, fmt.Errorf("calcular hash do payload: %w", err)
	}

	uc.log.Debug("processando wager",
		zap.String("externalId", input.ExternalTransactionID),
		zap.String("providerId", input.ProviderID),
		zap.String("idempotencyKey", input.IdempotencyKey),
		zap.String("payloadHash", payloadHash[:8]+"..."),
		zap.String("kind", string(input.Kind)),
	)

	// ==========================================================================
	// PASSO 2: Verificar idempotencia [leitura fora da TX — caminho rapido]
	//
	// Esta verificacao evita processar desnecessariamente operacoes ja concluidas.
	// E o "fast path" — a maioria dos replays e capturada aqui sem abrir TX.
	//
	// IMPORTANTE: esta verificacao nao e atomica com o INSERT posterior.
	// Uma race condition e possivel: dois processos podem passar daqui
	// e tentar criar a mesma transacao. Isso e resolvido no PASSO 6 (TryCreate).
	// ==========================================================================
	existing, err := uc.txRepo.FindByIdempotencyKey(ctx, input.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("verificar idempotencia: %w", err)
	}
	if existing != nil {
		return uc.handleExistingTransaction(ctx, existing, payloadHash, input)
	}

	// ==========================================================================
	// PASSO 3: Validar tipo de operacao
	// OPENING e exclusivo de abertura interna — nao pode vir de HTTP/SQS externo.
	// ==========================================================================
	if input.Kind == transaction.KindOpening {
		return nil, ErrInvalidOpeningKind
	}

	// ==========================================================================
	// PASSOS 4-10: Executar dentro de uma transacao SQL atomica
	// RunInTx chama Begin, executa a funcao, e chama Commit ou Rollback.
	// ==========================================================================
	var output *ProcessWagerOutput
	txErr := uc.runInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var innerErr error
		output, innerErr = uc.executeInTransaction(ctx, tx, input, payloadHash)
		return innerErr
	})
	if txErr != nil {
		return nil, txErr
	}
	return output, nil
}

// executeInTransaction executa os passos 4-10 dentro da transacao SQL ativa.
func (uc *ProcessWagerUseCase) executeInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	input ProcessWagerInput,
	payloadHash string,
) (*ProcessWagerOutput, error) {
	now := time.Now().UTC()

	// ==========================================================================
	// PASSO 5: Adquirir lock pessimista na Wallet (SELECT FOR UPDATE)
	//
	// Esta e a operacao mais importante para garantir corretude em ambiente
	// distribuido. O SELECT FOR UPDATE:
	//   - Le o saldo ATUAL da carteira
	//   - Coloca um lock exclusivo na LINHA da carteira no banco
	//   - Bloqueia qualquer outra transacao que tente SELECT FOR UPDATE
	//     na mesma linha ate que esta transacao termine (COMMIT ou ROLLBACK)
	//
	// Por que fazer ANTES de inserir a WagerTransaction?
	// Precisamos garantir que o saldo lido e o saldo real no momento da decisao.
	// Se fizessemos o INSERT antes, e outra TX com FOR UPDATE fosse commitada
	// no meio, leriamos um saldo desatualizado.
	//
	// Ordem consistente (evitar deadlock):
	// SEMPRE adquirimos o lock na Wallet ANTES de qualquer outra operacao na TX.
	// Isso garante que dois processos competindo pela mesma carteira nunca
	// adquirem locks em ordem inversa (o que causaria deadlock).
	// ==========================================================================
	w, err := uc.walletRepo.FindByIDWithLock(ctx, tx, input.WalletID)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			return nil, fmt.Errorf("carteira nao encontrada: %w", err)
		}
		return nil, fmt.Errorf("adquirir lock na carteira: %w", err)
	}

	// Validar que a carteira pertence ao jogador autenticado.
	// Esta verificacao ocorre APOS o lock para garantir que lemos o estado real.
	if w.PlayerID() != input.PlayerID {
		return nil, ErrWalletOwnerMismatch
	}

	// Validar que a moeda da operacao corresponde a moeda da carteira.
	if input.Amount.Currency() != w.Currency() {
		return nil, fmt.Errorf("%w: operacao em %s, carteira em %s",
			domain.ErrWalletCurrencyMismatch, input.Amount.Currency(), w.Currency())
	}

	// ==========================================================================
	// PASSO 6: Claim atomico da idempotency_key (INSERT ... ON CONFLICT DO NOTHING)
	//
	// Por que inserir a WagerTransaction aqui (dentro da TX com lock ativo)?
	//
	// Cenario de race condition que este passo resolve:
	//   - Request A e Request B chegam ao mesmo tempo com o mesmo idempotency_key.
	//   - Ambos passam pelo PASSO 2 (sem encontrar a tx no banco).
	//   - Request A adquire o lock (PASSO 5) primeiro.
	//   - Request B fica em WAIT no SELECT FOR UPDATE (bloqueado pelo lock de A).
	//   - Request A insere a WagerTransaction e comita.
	//   - Request B "acorda", le saldo atualizado, tenta INSERT -> ON CONFLICT DO NOTHING.
	//   - TryCreate retorna inserted=false -> voltamos ao caminho de replay.
	//
	// Isso garante que EXATAMENTE UMA execucao da operacao seja bem-sucedida,
	// mesmo com concorrencia real entre multiplas instancias.
	// ==========================================================================
	pendingTx, err := transaction.NewWagerTransaction(
		input.ExternalTransactionID,
		input.ProviderID,
		input.IdempotencyKey,
		payloadHash,
		input.WalletID,
		input.PlayerID,
		input.RoundID,
		input.GameID,
		input.Kind,
		input.Amount,
		input.ReferenceExternalID,
	)
	if err != nil {
		return nil, fmt.Errorf("construir wager transaction no dominio: %w", err)
	}

	inserted, err := uc.txRepo.TryCreate(ctx, tx, pendingTx)
	if err != nil {
		return nil, fmt.Errorf("claim da idempotency_key: %w", err)
	}
	if !inserted {
		// Race condition resolvida: outra instancia processou esta operacao
		// enquanto aguardavamos o lock. Buscar o resultado dela.
		existing, lookupErr := uc.txRepo.FindByIdempotencyKey(ctx, input.IdempotencyKey)
		if lookupErr != nil || existing == nil {
			return nil, fmt.Errorf("buscar transacao apos race condition: %w", lookupErr)
		}
		return uc.handleExistingTransaction(ctx, existing, payloadHash, input)
	}

	// ==========================================================================
	// PASSO 7: Aplicar logica de negocio por tipo de operacao
	//
	// Agora que temos:
	//  - O saldo real (lido com lock)
	//  - A posse exclusiva da idempotency_key (TryCreate bem-sucedido)
	//  - O lock na carteira (ninguem mais pode modificar enquanto estamos aqui)
	//
	// Podemos aplicar a logica de negocio com seguranca total.
	// ==========================================================================
	result, err := uc.applyBusinessLogic(ctx, tx, w, pendingTx, input, now)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// applyBusinessLogic aplica a logica especifica do Kind e persiste os resultados.
func (uc *ProcessWagerUseCase) applyBusinessLogic(
	ctx context.Context,
	tx pgx.Tx,
	w *wallet.Wallet,
	pendingTx *transaction.WagerTransaction,
	input ProcessWagerInput,
	now time.Time,
) (*ProcessWagerOutput, error) {
	switch input.Kind {

	case transaction.KindBet:
		// BET: debitar da carteira.
		// Wallet.Debit valida saldo suficiente e gera ledger entry.
		return uc.processBet(ctx, tx, w, pendingTx, input, now)

	case transaction.KindWin:
		// WIN: creditar na carteira.
		// Wallet.Credit valida que amount > 0 e gera ledger entry.
		return uc.processWin(ctx, tx, w, pendingTx, input, now)

	case transaction.KindLoss:
		// LOSS: sem movimentacao de saldo (amount deve ser 0).
		// Apenas registra a perda no historico — sem ledger entry.
		return uc.processLoss(ctx, tx, w, pendingTx, input, now)

	case transaction.KindRefund:
		// REFUND: estorno de uma BET processada.
		// Busca a referencia, valida e credita o valor.
		return uc.processRefund(ctx, tx, w, pendingTx, input, now)

	case transaction.KindRollback:
		// ROLLBACK: desfaz integralmente qualquer operacao anterior.
		// A direcao e contraria a da transacao referenciada.
		return uc.processRollback(ctx, tx, w, pendingTx, input, now)

	default:
		return nil, fmt.Errorf("%w: kind desconhecido: %s", ErrInvalidOpeningKind, input.Kind)
	}
}

// =============================================================================
// Processamento por Tipo
// =============================================================================

// processBet debita da carteira e persiste os resultados.
func (uc *ProcessWagerUseCase) processBet(ctx context.Context, tx pgx.Tx, w *wallet.Wallet, pendingTx *transaction.WagerTransaction, input ProcessWagerInput, now time.Time) (*ProcessWagerOutput, error) {
	// Wallet.Debit verifica saldo suficiente e retorna ErrInsufficientBalance se nao.
	// Gera WalletLedgerEntry com invariante balanceAfter = balanceBefore - amount.
	ledgerEntry, err := w.Debit(input.Amount, pendingTx.ID())
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientBalance) {
			// Saldo insuficiente: REJEITAR a transacao com codigo estavel.
			return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeInsufficientBalance, input, now)
		}
		return nil, fmt.Errorf("debitar wallet: %w", err)
	}
	return uc.commitSuccess(ctx, tx, w, pendingTx, ledgerEntry, input, now)
}

// processWin credita na carteira e persiste os resultados.
func (uc *ProcessWagerUseCase) processWin(ctx context.Context, tx pgx.Tx, w *wallet.Wallet, pendingTx *transaction.WagerTransaction, input ProcessWagerInput, now time.Time) (*ProcessWagerOutput, error) {
	ledgerEntry, err := w.Credit(input.Amount, pendingTx.ID())
	if err != nil {
		return nil, fmt.Errorf("creditar wallet: %w", err)
	}
	return uc.commitSuccess(ctx, tx, w, pendingTx, ledgerEntry, input, now)
}

// processLoss registra a perda sem movimentacao de saldo.
func (uc *ProcessWagerUseCase) processLoss(ctx context.Context, tx pgx.Tx, w *wallet.Wallet, pendingTx *transaction.WagerTransaction, input ProcessWagerInput, now time.Time) (*ProcessWagerOutput, error) {
	// LOSS: sem ledger entry (sem movimentacao financeira).
	// Apenas marca a transacao como PROCESSED com o saldo ATUAL (nao alterado).
	if err := pendingTx.MarkAsProcessed(w.Balance().Amount()); err != nil {
		return nil, fmt.Errorf("marcar LOSS como PROCESSED: %w", err)
	}
	if err := uc.txRepo.Update(ctx, tx, pendingTx); err != nil {
		return nil, fmt.Errorf("atualizar LOSS transaction: %w", err)
	}

	// Para LOSS, publicamos WagerTransactionProcessed mas NAO WalletBalanceChanged.
	if err := uc.publishProcessedEvent(ctx, tx, pendingTx, w, input, now); err != nil {
		return nil, err
	}

	currentBalance := w.Balance()
	return &ProcessWagerOutput{
		TransactionID: pendingTx.ID(),
		Status:        transaction.StatusProcessed,
		Balance:       &currentBalance,
	}, nil
}

// processRefund aplica um estorno de BET via credito.
func (uc *ProcessWagerUseCase) processRefund(ctx context.Context, tx pgx.Tx, w *wallet.Wallet, pendingTx *transaction.WagerTransaction, input ProcessWagerInput, now time.Time) (*ProcessWagerOutput, error) {
	// Buscar transacao referenciada (a BET original) fora da TX interna
	// (a referencia ja foi comitada — leitura consistente).
	refExtID := *input.ReferenceExternalID
	ref, err := uc.txRepo.FindByProviderAndExternalID(ctx, input.ProviderID, refExtID)
	if err != nil {
		return nil, fmt.Errorf("buscar referencia REFUND: %w", err)
	}

	// Referencia nao encontrada: marcar como PENDING_REFERENCE e agendar retry.
	if ref == nil || ref.Status() == transaction.StatusPending || ref.Status() == transaction.StatusPendingReference {
		return uc.markPendingReference(ctx, tx, w, pendingTx, input, now)
	}

	// Referencia rejeitada: nao ha o que estornar.
	if ref.Status() == transaction.StatusRejected || ref.Status() == transaction.StatusFailed {
		return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeReferenceNotFound, input, now)
	}

	// Validar: referencia deve ser uma BET do mesmo provider.
	if ref.Kind() != transaction.KindBet {
		return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeReferenceNotFound, input, now)
	}

	// Validar: sem reversao ja processada para esta referencia.
	existing, err := uc.txRepo.FindProcessedReversal(ctx, input.ProviderID, refExtID, transaction.KindRefund)
	if err != nil {
		return nil, fmt.Errorf("verificar reversao existente: %w", err)
	}
	if existing != nil {
		return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeReferenceAlreadyReversed, input, now)
	}

	// Validar: valor do REFUND deve ser igual ao valor da BET original.
	eq, _ := input.Amount.Equal(ref.Amount())
	if !eq {
		return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeAmountMismatch, input, now)
	}

	// Resolver referencia interna na transacao pendente.
	refID := ref.ID()
	pendingTx.ResolveReference(refID)

	// Creditar o estorno na carteira.
	ledgerEntry, err := w.Credit(input.Amount, pendingTx.ID())
	if err != nil {
		return nil, fmt.Errorf("creditar REFUND: %w", err)
	}
	return uc.commitSuccess(ctx, tx, w, pendingTx, ledgerEntry, input, now)
}

// processRollback desfaz integralmente uma operacao anterior.
func (uc *ProcessWagerUseCase) processRollback(ctx context.Context, tx pgx.Tx, w *wallet.Wallet, pendingTx *transaction.WagerTransaction, input ProcessWagerInput, now time.Time) (*ProcessWagerOutput, error) {
	refExtID := *input.ReferenceExternalID
	ref, err := uc.txRepo.FindByProviderAndExternalID(ctx, input.ProviderID, refExtID)
	if err != nil {
		return nil, fmt.Errorf("buscar referencia ROLLBACK: %w", err)
	}

	if ref == nil || ref.Status() == transaction.StatusPending || ref.Status() == transaction.StatusPendingReference {
		return uc.markPendingReference(ctx, tx, w, pendingTx, input, now)
	}

	if ref.Status() == transaction.StatusRejected || ref.Status() == transaction.StatusFailed {
		return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeReferenceNotFound, input, now)
	}

	// ROLLBACK so pode referenciar BET, WIN ou REFUND (nao outro ROLLBACK).
	if ref.Kind() == transaction.KindRollback || ref.Kind() == transaction.KindLoss {
		return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeReferenceNotFound, input, now)
	}

	// Verificar duplo ROLLBACK.
	existingRollback, err := uc.txRepo.FindProcessedReversal(ctx, input.ProviderID, refExtID, transaction.KindRollback)
	if err != nil {
		return nil, fmt.Errorf("verificar rollback existente: %w", err)
	}
	if existingRollback != nil {
		return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeReferenceAlreadyReversed, input, now)
	}

	refID := ref.ID()
	pendingTx.ResolveReference(refID)

	// Determinar direcao contraria a da referencia:
	// - BET (debit) -> ROLLBACK = credit
	// - WIN (credit) -> ROLLBACK = debit
	// - REFUND (credit) -> ROLLBACK = debit
	var ledgerEntry *wallet.WalletLedgerEntry
	switch ref.Kind() {
	case transaction.KindBet:
		// Reverter debito: creditar
		ledgerEntry, err = w.Credit(ref.Amount(), pendingTx.ID())
	case transaction.KindWin, transaction.KindRefund:
		// Reverter credito: debitar
		ledgerEntry, err = w.Debit(ref.Amount(), pendingTx.ID())
		if errors.Is(err, domain.ErrInsufficientBalance) {
			return uc.rejectTransaction(ctx, tx, w, pendingTx, transaction.FailCodeInsufficientBalanceReversal, input, now)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("aplicar movimento do ROLLBACK: %w", err)
	}

	return uc.commitSuccess(ctx, tx, w, pendingTx, ledgerEntry, input, now)
}

// =============================================================================
// Helpers de persistencia e publicacao
// =============================================================================

// commitSuccess persiste o resultado de uma operacao bem-sucedida.
//
// PASSO 8 + PASSO 9 combinados:
//  - Atualizar WagerTransaction para PROCESSED
//  - Atualizar saldo da Wallet
//  - Inserir WalletLedgerEntry
//  - Inserir OutboxEvents (WagerTransactionProcessed + WalletBalanceChanged)
func (uc *ProcessWagerUseCase) commitSuccess(
	ctx context.Context,
	tx pgx.Tx,
	w *wallet.Wallet,
	pendingTx *transaction.WagerTransaction,
	ledgerEntry *wallet.WalletLedgerEntry,
	input ProcessWagerInput,
	now time.Time,
) (*ProcessWagerOutput, error) {
	// --- 8a. Marcar transacao como PROCESSED com saldo observado ---
	// O saldo observado e armazenado para replays: mesmo que o saldo mude depois,
	// o replay retorna o saldo DO MOMENTO do processamento original.
	if err := pendingTx.MarkAsProcessed(w.Balance().Amount()); err != nil {
		return nil, fmt.Errorf("marcar transacao como PROCESSED: %w", err)
	}
	if err := uc.txRepo.Update(ctx, tx, pendingTx); err != nil {
		return nil, fmt.Errorf("persistir transacao PROCESSED: %w", err)
	}

	// --- 8b. Atualizar saldo da Wallet (com verificacao de versao) ---
	if err := uc.walletRepo.Update(ctx, tx, w); err != nil {
		return nil, fmt.Errorf("atualizar saldo da wallet: %w", err)
	}

	// --- 8c. Inserir WalletLedgerEntry (append-only, imutavel) ---
	if err := uc.ledger.Create(ctx, tx, ledgerEntry); err != nil {
		return nil, fmt.Errorf("inserir ledger entry: %w", err)
	}

	// --- 9. Inserir OutboxEvents (dentro da mesma TX — garantia atomica) ---
	// ==========================================================================
	// PASSO 9: Outbox Pattern — a garantia de publicacao exatamente-uma-vez
	//
	// Por que inserimos eventos NA MESMA TRANSACAO?
	// Se commitassemos o dominio e depois tentar publicar no SQS:
	//   - Crash entre commit e publicacao -> evento PERDIDO PARA SEMPRE.
	//
	// Com Outbox: o evento e inserido NA MESMA TX que o dominio.
	//   - Se o processo crasha antes do commit -> tudo reverte (nenhuma inconsistencia).
	//   - Se crasha apos o commit -> o worker de outbox encontra o evento e publica.
	//   - O worker usa FOR UPDATE SKIP LOCKED para evitar publicacao duplicada
	//     entre multiplos workers rodando em paralelo.
	// ==========================================================================
	if err := uc.publishProcessedEvent(ctx, tx, pendingTx, w, input, now); err != nil {
		return nil, err
	}

	// WalletBalanceChanged: publicar SOMENTE se houve movimentacao real de saldo.
	// LOSS nao chama commitSuccess, entao aqui sempre temos um ledger entry valido.
	if err := uc.publishBalanceChangedEvent(ctx, tx, w, pendingTx, ledgerEntry, input, now); err != nil {
		return nil, err
	}

	currentBalance := w.Balance()
	return &ProcessWagerOutput{
		TransactionID: pendingTx.ID(),
		Status:        transaction.StatusProcessed,
		Balance:       &currentBalance,
	}, nil
}

// rejectTransaction marca a transacao como REJECTED e publica o evento correspondente.
func (uc *ProcessWagerUseCase) rejectTransaction(
	ctx context.Context,
	tx pgx.Tx,
	w *wallet.Wallet,
	pendingTx *transaction.WagerTransaction,
	code transaction.FailureCode,
	input ProcessWagerInput,
	now time.Time,
) (*ProcessWagerOutput, error) {
	if err := pendingTx.MarkAsRejected(code); err != nil {
		return nil, fmt.Errorf("marcar transacao como REJECTED: %w", err)
	}
	if err := uc.txRepo.Update(ctx, tx, pendingTx); err != nil {
		return nil, fmt.Errorf("persistir transacao REJECTED: %w", err)
	}

	// Publicar WagerTransactionRejected no outbox.
	rejectedEvt := domainevent.NewWagerTransactionRejected(
		w.ID(), input.CorrelationID,
		domainevent.WagerTransactionRejectedData{
			TransactionID:         pendingTx.ID(),
			ExternalTransactionID: input.ExternalTransactionID,
			ProviderID:            input.ProviderID,
			WalletID:              w.ID(),
			PlayerID:              input.PlayerID,
			Kind:                  string(input.Kind),
			Money:                 moneyToPayload(input.Amount),
			FailureCode:           code.String(),
		},
	)
	outboxEvt, err := newOutboxEvent(w.ID(), domainevent.EventTypeWagerTransactionRejected, rejectedEvt, now)
	if err != nil {
		return nil, err
	}
	if err := uc.outbox.Create(ctx, tx, outboxEvt); err != nil {
		return nil, fmt.Errorf("inserir outbox WagerTransactionRejected: %w", err)
	}

	fc := code
	currentBalance := w.Balance()
	return &ProcessWagerOutput{
		TransactionID: pendingTx.ID(),
		Status:        transaction.StatusRejected,
		Balance:       &currentBalance,
		FailureCode:   &fc,
	}, nil
}

// markPendingReference agenda a resolucao posterior de referencia nao disponivel.
func (uc *ProcessWagerUseCase) markPendingReference(
	ctx context.Context,
	tx pgx.Tx,
	w *wallet.Wallet,
	pendingTx *transaction.WagerTransaction,
	input ProcessWagerInput,
	now time.Time,
) (*ProcessWagerOutput, error) {
	// Primeiro retry em 5 segundos (backoff exponencial: 5s, 10s, 20s, 40s...).
	nextRetry := now.Add(5 * time.Second)
	if err := pendingTx.MarkAsPendingReference(nextRetry); err != nil {
		return nil, fmt.Errorf("marcar transacao como PENDING_REFERENCE: %w", err)
	}
	if err := uc.txRepo.Update(ctx, tx, pendingTx); err != nil {
		return nil, fmt.Errorf("persistir transacao PENDING_REFERENCE: %w", err)
	}

	// Publicar WagerTransactionPendingReference para rastreio.
	refID := ""
	if input.ReferenceExternalID != nil {
		refID = *input.ReferenceExternalID
	}
	pendingEvt := domainevent.NewWagerTransactionPendingReference(
		w.ID(), input.CorrelationID,
		domainevent.WagerTransactionPendingReferenceData{
			TransactionID:                  pendingTx.ID(),
			ExternalTransactionID:          input.ExternalTransactionID,
			ProviderID:                     input.ProviderID,
			WalletID:                       w.ID(),
			Kind:                           string(input.Kind),
			ReferenceExternalTransactionID: refID,
			Attempts:                       pendingTx.Attempts(),
			NextRetryAt:                    nextRetry,
		},
	)
	outboxEvt, err := newOutboxEvent(w.ID(), domainevent.EventTypeWagerTransactionPendingReference, pendingEvt, now)
	if err != nil {
		return nil, err
	}
	if err := uc.outbox.Create(ctx, tx, outboxEvt); err != nil {
		return nil, fmt.Errorf("inserir outbox WagerTransactionPendingReference: %w", err)
	}

	return &ProcessWagerOutput{
		TransactionID: pendingTx.ID(),
		Status:        transaction.StatusPendingReference,
		Balance:       nil, // saldo nao alterado — resultado ainda indefinido
	}, nil
}

// handleExistingTransaction lida com uma transacao ja existente (replay).
func (uc *ProcessWagerUseCase) handleExistingTransaction(
	ctx context.Context,
	existing *transaction.WagerTransaction,
	payloadHash string,
	input ProcessWagerInput,
) (*ProcessWagerOutput, error) {
	// Verificar conflito de idempotencia: mesma key, conteudo diferente.
	if existing.PayloadHash() != payloadHash {
		uc.log.Warn("conflito de idempotencia detectado",
			zap.String("idempotencyKey", input.IdempotencyKey),
			zap.String("hashExistente", existing.PayloadHash()[:8]+"..."),
			zap.String("hashNovo", payloadHash[:8]+"..."),
		)
		return nil, ErrIdempotencyConflict
	}

	// Hash identico = mesmo conteudo = replay legitimo.
	uc.log.Info("replay de operacao ja processada",
		zap.String("transactionId", existing.ID().String()),
		zap.String("status", string(existing.Status())),
	)

	var resultBalance *money.Money
	if existing.ResultBalanceCents() != nil {
		bal := money.New(*existing.ResultBalanceCents(), existing.Amount().Currency())
		resultBalance = &bal
	}

	return &ProcessWagerOutput{
		TransactionID:    existing.ID(),
		Status:           existing.Status(),
		Balance:          resultBalance,
		IdempotentReplay: true,
		FailureCode:      existing.FailureCode(),
	}, nil
}

// publishProcessedEvent insere o OutboxEvent de WagerTransactionProcessed.
func (uc *ProcessWagerUseCase) publishProcessedEvent(
	ctx context.Context,
	tx pgx.Tx,
	pendingTx *transaction.WagerTransaction,
	w *wallet.Wallet,
	input ProcessWagerInput,
	now time.Time,
) error {
	evt := domainevent.NewWagerTransactionProcessed(
		w.ID(), input.CorrelationID,
		domainevent.WagerTransactionProcessedData{
			TransactionID:         pendingTx.ID(),
			ExternalTransactionID: input.ExternalTransactionID,
			ProviderID:            input.ProviderID,
			WalletID:              w.ID(),
			PlayerID:              input.PlayerID,
			RoundID:               input.RoundID,
			GameID:                input.GameID,
			Kind:                  string(input.Kind),
			Money:                 moneyToPayload(input.Amount),
		},
	)
	outboxEvt, err := newOutboxEvent(w.ID(), domainevent.EventTypeWagerTransactionProcessed, evt, now)
	if err != nil {
		return err
	}
	return uc.outbox.Create(ctx, tx, outboxEvt)
}

// publishBalanceChangedEvent insere o OutboxEvent de WalletBalanceChanged.
func (uc *ProcessWagerUseCase) publishBalanceChangedEvent(
	ctx context.Context,
	tx pgx.Tx,
	w *wallet.Wallet,
	pendingTx *transaction.WagerTransaction,
	entry *wallet.WalletLedgerEntry,
	input ProcessWagerInput,
	now time.Time,
) error {
	evt := domainevent.NewWalletBalanceChanged(
		w.ID(), input.CorrelationID,
		domainevent.WalletBalanceChangedData{
			WalletID:      w.ID(),
			TransactionID: pendingTx.ID(),
			Direction:     string(entry.Direction()),
			Money:         moneyToPayload(entry.Amount()),
			BalanceBefore: moneyToPayload(entry.BalanceBefore()),
			BalanceAfter:  moneyToPayload(entry.BalanceAfter()),
			WalletVersion: w.Version(),
		},
	)
	outboxEvt, err := newOutboxEvent(w.ID(), domainevent.EventTypeWalletBalanceChanged, evt, now)
	if err != nil {
		return err
	}
	return uc.outbox.Create(ctx, tx, outboxEvt)
}
"""

with open("internal/application/usecase/process_wager.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(process_wager_go)

print("process_wager.go criado")
