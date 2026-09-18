package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domaininbox "github.com/joaoluvisari/backend-challenge-go/internal/domain/inbox"
	domainoutbox "github.com/joaoluvisari/backend-challenge-go/internal/domain/outbox"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// =============================================================================
// LedgerRepository
// =============================================================================

// LedgerRepository implementa port.LedgerRepository.
// Apenas INSERT e SELECT — UPDATE/DELETE bloqueados por trigger no banco.
type LedgerRepository struct {
	pool *pgxpool.Pool
}

// NewLedgerRepository cria o repositorio de ledger.
func NewLedgerRepository(pool *pgxpool.Pool) *LedgerRepository {
	return &LedgerRepository{pool: pool}
}

// Create insere um lancamento contabil na mesma transacao SQL do dominio.
// A unicidade (wallet_id, transaction_id) no banco previne duplicatas.
func (r *LedgerRepository) Create(ctx context.Context, tx pgx.Tx, entry *wallet.WalletLedgerEntry) error {
	const query = `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id,
			direction, amount_cents,
			balance_before_cents, balance_after_cents,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := tx.Exec(ctx, query,
		entry.ID(),
		entry.WalletID(),
		entry.TransactionID(),
		string(entry.Direction()),
		entry.Amount().Amount(),
		entry.BalanceBefore().Amount(),
		entry.BalanceAfter().Amount(),
		entry.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("inserir ledger entry: %w", err)
	}
	return nil
}

// FindByWalletID lista lancamentos de uma carteira com paginacao por cursor.
// Ordenacao estavel: (created_at ASC, id ASC) garante cursor confiavel mesmo
// com lancamentos no mesmo millisegundo.
func (r *LedgerRepository) FindByWalletID(ctx context.Context, walletID uuid.UUID, afterID *uuid.UUID, limit int) ([]*wallet.WalletLedgerEntry, error) {
	var (
		rows pgx.Rows
		err  error
	)

	if afterID == nil {
		// Primeira pagina: sem cursor
		const query = `
			SELECT id, wallet_id, transaction_id, direction,
			       amount_cents, balance_before_cents, balance_after_cents, created_at
			FROM wallet_ledger_entries
			WHERE wallet_id = $1
			ORDER BY created_at ASC, id ASC
			LIMIT $2
		`
		rows, err = r.pool.Query(ctx, query, walletID, limit)
	} else {
		// Paginas subsequentes: cursor = ID do ultimo item da pagina anterior.
		// Busca entradas criadas DEPOIS do cursor (ou no mesmo instante com ID maior).
		const query = `
			SELECT id, wallet_id, transaction_id, direction,
			       amount_cents, balance_before_cents, balance_after_cents, created_at
			FROM wallet_ledger_entries
			WHERE wallet_id = $1
			  AND (created_at, id) > (
			      SELECT created_at, id FROM wallet_ledger_entries WHERE id = $2
			  )
			ORDER BY created_at ASC, id ASC
			LIMIT $3
		`
		rows, err = r.pool.Query(ctx, query, walletID, *afterID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("FindByWalletID: %w", err)
	}
	defer rows.Close()

	var entries []*wallet.WalletLedgerEntry
	for rows.Next() {
		var (
			id, wID, txID uuid.UUID
			direction     string
			amount, before, after int64
			createdAt     time.Time
		)
		if err := rows.Scan(&id, &wID, &txID, &direction, &amount, &before, &after, &createdAt); err != nil {
			return nil, fmt.Errorf("scan ledger entry: %w", err)
		}
		// Determinar currency a partir da wallet (simplificado: assumimos BRL).
		// Em producao real, juntariamos com a tabela wallets para obter a moeda.
		currency := "BRL"
		entries = append(entries, wallet.RehydrateLedgerEntry(
			id, wID, txID,
			wallet.Direction(direction),
			amount, before, after,
			currency,
			createdAt,
		))
	}
	return entries, rows.Err()
}

// SumByWalletID recalcula o saldo somando creditos menos debitos para reconciliacao.
// Implementa a operacao GET /wallets/:id/reconciliation.
func (r *LedgerRepository) SumByWalletID(ctx context.Context, walletID uuid.UUID) (creditsCents, debitsCents int64, err error) {
	const query = `
		SELECT
			COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount_cents ELSE 0 END), 0) AS credits,
			COALESCE(SUM(CASE WHEN direction = 'DEBIT'  THEN amount_cents ELSE 0 END), 0) AS debits
		FROM wallet_ledger_entries
		WHERE wallet_id = $1
	`
	row := r.pool.QueryRow(ctx, query, walletID)
	if err := row.Scan(&creditsCents, &debitsCents); err != nil {
		return 0, 0, fmt.Errorf("SumByWalletID: %w", err)
	}
	return creditsCents, debitsCents, nil
}

// =============================================================================
// OutboxRepository
// =============================================================================

// OutboxRepository implementa port.OutboxRepository.
// A feature principal e FetchPendingForUpdate com SKIP LOCKED.
type OutboxRepository struct {
	pool *pgxpool.Pool
}

// NewOutboxRepository cria o repositorio de outbox.
func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// Create insere um OutboxEvent dentro da transacao SQL do dominio.
// Esta e a garantia atomica do Outbox Pattern: o evento so existe se
// a transacao de dominio foi confirmada.
func (r *OutboxRepository) Create(ctx context.Context, tx pgx.Tx, event *domainoutbox.OutboxEvent) error {
	const query = `
		INSERT INTO outbox_events (
			id, aggregate_id, event_type, payload,
			occurred_at, attempts, max_attempts, next_delivery_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := tx.Exec(ctx, query,
		event.ID(),
		event.AggregateID(),
		event.EventType(),
		[]byte(event.Payload()), // JSONB: payload serializado
		event.OccurredAt(),
		event.Attempts(),
		event.MaxAttempts(),
		event.NextDeliveryAt(),
		event.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("inserir outbox_event: %w", err)
	}
	return nil
}

// FetchPendingForUpdate busca eventos pendentes com SELECT FOR UPDATE SKIP LOCKED.
//
// SKIP LOCKED e o mecanismo que permite multiplos workers de outbox disputarem
// registros sem se bloquearem mutuamente:
//
//   Worker A: SELECT ... FOR UPDATE SKIP LOCKED LIMIT 10
//     -> Adquire lock nas linhas 1-10
//   Worker B: SELECT ... FOR UPDATE SKIP LOCKED LIMIT 10
//     -> Linhas 1-10 estao LOCKED -> PULA para 11-20
//     -> Adquire lock nas linhas 11-20
//   Worker C: SELECT ... FOR UPDATE SKIP LOCKED LIMIT 10
//     -> Linhas 1-20 estao LOCKED -> PULA para 21-30
//
// Resultado: tres workers em paralelo, sem bloqueio, processando lotes disjuntos.
// Sem SKIP LOCKED, Worker B e C ficariam em WAIT ate Worker A terminar.
func (r *OutboxRepository) FetchPendingForUpdate(ctx context.Context, tx pgx.Tx, limit int) ([]*domainoutbox.OutboxEvent, error) {
	const query = `
		SELECT id, aggregate_id, event_type, payload,
		       occurred_at, attempts, max_attempts, next_delivery_at, published_at, created_at
		FROM outbox_events
		WHERE published_at IS NULL
		  AND next_delivery_at <= NOW()
		ORDER BY next_delivery_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`
	rows, err := tx.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("FetchPendingForUpdate: %w", err)
	}
	defer rows.Close()

	var events []*domainoutbox.OutboxEvent
	for rows.Next() {
		var (
			id             uuid.UUID
			aggregateID    uuid.UUID
			eventType      string
			payload        json.RawMessage
			occurredAt     time.Time
			attempts       int
			maxAttempts    int
			nextDeliveryAt time.Time
			publishedAt    *time.Time
			createdAt      time.Time
		)
		if err := rows.Scan(
			&id, &aggregateID, &eventType, &payload,
			&occurredAt, &attempts, &maxAttempts, &nextDeliveryAt, &publishedAt, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan outbox_event: %w", err)
		}
		events = append(events, domainoutbox.RehydrateOutboxEvent(
			id, aggregateID, eventType, payload,
			occurredAt, attempts, maxAttempts, nextDeliveryAt, publishedAt, createdAt,
		))
	}
	return events, rows.Err()
}

// MarkAsPublished atualiza o evento como publicado.
func (r *OutboxRepository) MarkAsPublished(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, publishedAt time.Time) error {
	const query = `UPDATE outbox_events SET published_at = $1 WHERE id = $2`
	_, err := tx.Exec(ctx, query, publishedAt, eventID)
	return err
}

// RecordFailure atualiza tentativas e proximo retry.
func (r *OutboxRepository) RecordFailure(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, attempts int, nextDeliveryAt time.Time) error {
	const query = `UPDATE outbox_events SET attempts = $1, next_delivery_at = $2 WHERE id = $3`
	_, err := tx.Exec(ctx, query, attempts, nextDeliveryAt, eventID)
	return err
}

// =============================================================================
// InboxRepository
// =============================================================================

// InboxRepository implementa port.InboxRepository.
type InboxRepository struct {
	pool *pgxpool.Pool
}

// NewInboxRepository cria o repositorio de inbox.
func NewInboxRepository(pool *pgxpool.Pool) *InboxRepository {
	return &InboxRepository{pool: pool}
}

// TryInsert tenta inserir um registro de inbox na transacao SQL.
// Retorna (true, nil) se mensagem nova; (false, nil) se ja existia (replay).
// A UNIQUE CONSTRAINT (consumer_name, message_id) garante que mesmo duas goroutines
// tentando simultaneamente, apenas uma tera sucesso — a outra recebe UniqueViolation.
func (r *InboxRepository) TryInsert(ctx context.Context, tx pgx.Tx, msg *domaininbox.InboxMessage) (bool, error) {
	const query = `
		INSERT INTO inbox_messages (id, consumer_name, message_id, payload_hash, received_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (consumer_name, message_id) DO NOTHING
	`
	// ON CONFLICT DO NOTHING: nao retorna erro em duplicata, mas RowsAffected = 0.
	// Isso e mais eficiente que capturar a excecao 23505.
	tag, err := tx.Exec(ctx, query,
		msg.ID(),
		msg.ConsumerName(),
		msg.MessageID(),
		msg.PayloadHash(),
		msg.ReceivedAt(),
	)
	if err != nil {
		return false, fmt.Errorf("TryInsert inbox_message: %w", err)
	}
	// RowsAffected = 1 -> mensagem nova; = 0 -> replay (ja existia)
	return tag.RowsAffected() == 1, nil
}

// FindByMessageID busca um registro de inbox para verificar o hash do payload.
func (r *InboxRepository) FindByMessageID(ctx context.Context, consumerName, messageID string) (*domaininbox.InboxMessage, error) {
	const query = `
		SELECT id, consumer_name, message_id, payload_hash, received_at, processed_at
		FROM inbox_messages
		WHERE consumer_name = $1 AND message_id = $2
	`
	var (
		id           uuid.UUID
		cn, mid, ph  string
		receivedAt   time.Time
		processedAt  *time.Time
	)
	row := r.pool.QueryRow(ctx, query, consumerName, messageID)
	err := row.Scan(&id, &cn, &mid, &ph, &receivedAt, &processedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("FindByMessageID: %w", err)
	}
	return domaininbox.RehydrateInboxMessage(id, cn, mid, ph, receivedAt, processedAt), nil
}
