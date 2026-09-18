// Package port define as interfaces (ports) que o application layer usa
// para se comunicar com a infraestrutura (adapters).
//
// Este e o padrao Ports & Adapters (Hexagonal Architecture):
//   - Ports (aqui): interfaces definidas pelo DOMINIO/APPLICATION.
//   - Adapters (em internal/infrastructure): implementacoes concretas.
//
// O dominio e o application layer NUNCA importam pgx, SQS, etc.
// Eles apenas declaram o que precisam via estas interfaces.
// O Uber Fx injeta as implementacoes concretas em runtime.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domaininbox "github.com/joaoluvisari/backend-challenge-go/internal/domain/inbox"
	domainoutbox "github.com/joaoluvisari/backend-challenge-go/internal/domain/outbox"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// =============================================================================
// TxFunc e RunInTxFunc — Unit of Work
// =============================================================================

// TxFunc e o tipo da funcao executada dentro de uma transacao SQL.
// Recebe o pgx.Tx para que os repositorios possam participar da mesma transacao.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

// RunInTxFunc e o tipo da funcao que orquestra uma transacao SQL.
// Inicia a transacao, executa fn, e comita ou reverte baseado no resultado.
// O application layer recebe esta funcao via injecao de dependencia (Fx).
type RunInTxFunc func(ctx context.Context, fn TxFunc) error

// =============================================================================
// WalletRepository
// =============================================================================

// WalletRepository define as operacoes de persistencia da Wallet.
//
// Nota sobre o parametro pgx.Tx:
//   - Metodos de ESCRITA (Create, Update) recebem tx obrigatoriamente,
//     pois devem participar da transacao atomica junto com ledger e outbox.
//   - Metodos de LEITURA sem lock (FindByID) usam o pool diretamente
//     (sem tx) para evitar bloquear conexoes desnecessariamente.
//   - FindByIDWithLock requer tx porque o lock (FOR UPDATE) so faz sentido
//     dentro de uma transacao — o lock e liberado no COMMIT/ROLLBACK.
type WalletRepository interface {
	// Create insere uma nova Wallet no banco dentro da transacao fornecida.
	Create(ctx context.Context, tx pgx.Tx, w *wallet.Wallet) error

	// FindByID busca uma Wallet pelo ID sem adquirir lock.
	// Usa o pool de conexoes diretamente — adequado para leituras.
	FindByID(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error)

	// FindByIDWithLock busca a Wallet E ADQUIRE LOCK PESSIMISTA (SELECT FOR UPDATE).
	//
	// POR QUE LOCK PESSIMISTA?
	// Em um sistema distribuido com multiplas instancias e entrega at-least-once,
	// varias goroutines/processos podem tentar debitar da mesma carteira ao mesmo tempo.
	//
	// SELECT FOR UPDATE garante que:
	//  1. Apenas UMA transacao por vez pode modificar aquela linha.
	//  2. As demais transacoes AGUARDAM (block) ate a primeira comitar ou reverter.
	//  3. Apos a espera, cada transacao le o saldo JA ATUALIZADO.
	//
	// Isso evita "lost updates" (duas transacoes lerem o mesmo saldo, calcularem
	// independentemente e a segunda sobrescrever o resultado da primeira).
	//
	// Por que nao usar locks globais (sync.Mutex)?
	//   - sync.Mutex so funciona dentro de um UNICO PROCESSO.
	//   - Com multiplas instancias do servico, cada uma tem seu proprio mutex —
	//     eles nao se comunicam entre si.
	//   - O lock do PostgreSQL e o UNICO mecanismo que funciona entre processos.
	FindByIDWithLock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*wallet.Wallet, error)

	// FindByPlayerAndCurrency busca por (playerID, currency) — chave natural.
	FindByPlayerAndCurrency(ctx context.Context, playerID uuid.UUID, currency string) (*wallet.Wallet, error)

	// Update persiste o novo saldo e versao da Wallet.
	// Usa WHERE id = $1 AND version = $expected para deteccao de lost update.
	Update(ctx context.Context, tx pgx.Tx, w *wallet.Wallet) error
}

// =============================================================================
// WagerTransactionRepository
// =============================================================================

// WagerTransactionRepository define as operacoes de persistencia de WagerTransaction.
type WagerTransactionRepository interface {
	// Create insere uma nova transacao em estado PENDING.
	Create(ctx context.Context, tx pgx.Tx, t *transaction.WagerTransaction) error

	// FindByID busca pelo ID interno.
	FindByID(ctx context.Context, id uuid.UUID) (*transaction.WagerTransaction, error)

	// FindByIdempotencyKey busca pela chave de idempotencia.
	// Retorna nil, nil se nao encontrado.
	FindByIdempotencyKey(ctx context.Context, key string) (*transaction.WagerTransaction, error)

	// FindByProviderAndExternalID busca por (providerId, externalId).
	// Previne reprocessamento via chave diferente para a mesma operacao.
	FindByProviderAndExternalID(ctx context.Context, providerID, externalID string) (*transaction.WagerTransaction, error)

	// Update persiste mudancas de estado (status, failureCode, etc.).
	Update(ctx context.Context, tx pgx.Tx, t *transaction.WagerTransaction) error

	// FindPendingReferences busca transacoes PENDING_REFERENCE prontas para retry.
	// next_retry_at <= NOW() e attempts < max_attempts.
	FindPendingReferences(ctx context.Context, limit int) ([]*transaction.WagerTransaction, error)

	// FindProcessedReversal verifica se ja existe uma reversao PROCESSADA para uma
	// referencia especifica. Previne que a mesma operacao seja revertida duas vezes.
	// kind deve ser REFUND ou ROLLBACK. Retorna nil, nil se nao encontrado.
	FindProcessedReversal(ctx context.Context, providerID, referenceExternalID string, kind transaction.Kind) (*transaction.WagerTransaction, error)

	// CreateWithinTx insere a transacao DENTRO de uma transacao SQL existente.
	// Diferente de Create (que aceita pgx.Tx), este usa ON CONFLICT DO NOTHING
	// e retorna (true, nil) se inserido ou (false, nil) em caso de conflito.
	// Usado para claim atomico da idempotency_key com deteccao de race.
	TryCreate(ctx context.Context, tx pgx.Tx, t *transaction.WagerTransaction) (inserted bool, err error)
}

// =============================================================================
// LedgerRepository
// =============================================================================

// LedgerRepository define as operacoes de persistencia do ledger.
// Apenas INSERT e SELECT — UPDATE e DELETE sao bloqueados por trigger no banco.
type LedgerRepository interface {
	// Create insere um novo lancamento contabil na mesma transacao SQL.
	Create(ctx context.Context, tx pgx.Tx, entry *wallet.WalletLedgerEntry) error

	// FindByWalletID lista lancamentos de uma carteira com cursor opaco para paginacao.
	// Ordenacao estavel por created_at, id para cursor confiavel.
	FindByWalletID(ctx context.Context, walletID uuid.UUID, afterID *uuid.UUID, limit int) ([]*wallet.WalletLedgerEntry, error)

	// SumByWalletID recalcula o saldo somando creditos menos debitos (reconciliacao).
	SumByWalletID(ctx context.Context, walletID uuid.UUID) (creditsCents, debitsCents int64, err error)
}

// =============================================================================
// OutboxRepository
// =============================================================================

// OutboxRepository define as operacoes de persistencia de OutboxEvent.
type OutboxRepository interface {
	// Create insere um evento de outbox dentro da transacao SQL do dominio.
	// Esta e a garantia atomica do Outbox Pattern.
	Create(ctx context.Context, tx pgx.Tx, event *domainoutbox.OutboxEvent) error

	// FetchPendingForUpdate busca eventos pendentes com SELECT FOR UPDATE SKIP LOCKED.
	//
	// SKIP LOCKED e o mecanismo que permite multiplos publishers disputarem
	// a outbox sem se bloquearem mutuamente:
	//   - Cada worker adquire o lock de UM SUBCONJUNTO de linhas.
	//   - Linhas ja travadas por outro worker sao PULADAS (skip), nao bloqueiam.
	//   - Isso permite paralelismo real entre publishers.
	FetchPendingForUpdate(ctx context.Context, tx pgx.Tx, limit int) ([]*domainoutbox.OutboxEvent, error)

	// MarkAsPublished atualiza um evento como publicado.
	MarkAsPublished(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, publishedAt time.Time) error

	// RecordFailure atualiza tentativas e proximo retry apos falha de publicacao.
	RecordFailure(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, attempts int, nextDeliveryAt time.Time) error
}

// =============================================================================
// InboxRepository
// =============================================================================

// InboxRepository define as operacoes de persistencia de InboxMessage.
type InboxRepository interface {
	// TryInsert tenta inserir um registro de inbox. Se a UNIQUE VIOLATION ocorrer
	// (consumer_name, message_id ja existe), retorna (false, nil) — replay detectado.
	// Retorna (true, nil) se o insert foi bem-sucedido (mensagem nova).
	TryInsert(ctx context.Context, tx pgx.Tx, msg *domaininbox.InboxMessage) (bool, error)

	// FindByMessageID busca um registro de inbox para verificar o hash do payload.
	FindByMessageID(ctx context.Context, consumerName, messageID string) (*domaininbox.InboxMessage, error)
}
