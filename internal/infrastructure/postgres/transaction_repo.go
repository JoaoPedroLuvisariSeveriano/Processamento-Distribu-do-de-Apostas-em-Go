package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
)

// WagerTransactionRepository implementa port.WagerTransactionRepository.
type WagerTransactionRepository struct {
	pool *pgxpool.Pool
}

// NewWagerTransactionRepository cria o repositorio com o pool injetado.
func NewWagerTransactionRepository(pool *pgxpool.Pool) *WagerTransactionRepository {
	return &WagerTransactionRepository{pool: pool}
}

// Create insere uma nova WagerTransaction em estado PENDING dentro de uma transacao SQL.
// A insercao atomica com ledger, wallet e outbox garante idempotencia duravel.
func (r *WagerTransactionRepository) Create(ctx context.Context, tx pgx.Tx, t *transaction.WagerTransaction) error {
	const query = `
		INSERT INTO wager_transactions (
			id, external_id, provider_id, idempotency_key, payload_hash,
			wallet_id, player_id, round_id, game_id,
			kind, amount_cents, currency,
			reference_external_id, reference_transaction_id,
			status, failure_code, attempts, next_retry_at,
			result_balance_cents, created_at, updated_at, processed_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12,
			$13, $14,
			$15, $16, $17, $18,
			$19, $20, $21, $22
		)
	`
	var failureCode *string
	if t.FailureCode() != nil {
		s := t.FailureCode().String()
		failureCode = &s
	}

	_, err := tx.Exec(ctx, query,
		t.ID(),
		nullableString(t.ExternalID()),
		nullableString(t.ProviderID()),
		nullableString(t.IdempotencyKey()),
		nullableString(t.PayloadHash()),
		t.WalletID(),
		t.PlayerID(),
		nullableString(t.RoundID()),
		nullableString(t.GameID()),
		string(t.Kind()),
		t.Amount().Amount(),
		t.Amount().Currency(),
		t.ReferenceExternalID(),
		t.ReferenceTransactionID(),
		string(t.Status()),
		failureCode,
		t.Attempts(),
		t.NextRetryAt(),
		t.ResultBalanceCents(),
		t.CreatedAt(),
		t.UpdatedAt(),
		t.ProcessedAt(),
	)
	if err != nil {
		if IsDuplicateKeyError(err) {
			return fmt.Errorf("%w: key=%s", domain.ErrDuplicateTransaction, t.IdempotencyKey())
		}
		return fmt.Errorf("inserir wager_transaction: %w", err)
	}
	return nil
}

// FindByID busca uma transacao pelo ID interno.
func (r *WagerTransactionRepository) FindByID(ctx context.Context, id uuid.UUID) (*transaction.WagerTransaction, error) {
	const query = `
		SELECT id, external_id, provider_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id,
		       kind, amount_cents, currency,
		       reference_external_id, reference_transaction_id,
		       status, failure_code, attempts, next_retry_at,
		       result_balance_cents, created_at, updated_at, processed_at
		FROM wager_transactions
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, query, id)
	t, err := scanTransaction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTransactionNotFound
		}
		return nil, fmt.Errorf("FindByID transaction %s: %w", id, err)
	}
	return t, nil
}

// FindByIdempotencyKey busca pelo idempotency_key para deteccao de replay.
// Retorna (nil, nil) se nao encontrado.
func (r *WagerTransactionRepository) FindByIdempotencyKey(ctx context.Context, key string) (*transaction.WagerTransaction, error) {
	const query = `
		SELECT id, external_id, provider_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id,
		       kind, amount_cents, currency,
		       reference_external_id, reference_transaction_id,
		       status, failure_code, attempts, next_retry_at,
		       result_balance_cents, created_at, updated_at, processed_at
		FROM wager_transactions
		WHERE idempotency_key = $1
	`
	row := r.pool.QueryRow(ctx, query, key)
	t, err := scanTransaction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // nao encontrado — nao e erro
		}
		return nil, fmt.Errorf("FindByIdempotencyKey %q: %w", key, err)
	}
	return t, nil
}

// FindByProviderAndExternalID busca por (providerId, externalId).
func (r *WagerTransactionRepository) FindByProviderAndExternalID(ctx context.Context, providerID, externalID string) (*transaction.WagerTransaction, error) {
	const query = `
		SELECT id, external_id, provider_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id,
		       kind, amount_cents, currency,
		       reference_external_id, reference_transaction_id,
		       status, failure_code, attempts, next_retry_at,
		       result_balance_cents, created_at, updated_at, processed_at
		FROM wager_transactions
		WHERE provider_id = $1 AND external_id = $2
	`
	row := r.pool.QueryRow(ctx, query, providerID, externalID)
	t, err := scanTransaction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("FindByProviderAndExternalID: %w", err)
	}
	return t, nil
}

// Update persiste mudancas de estado de uma WagerTransaction.
func (r *WagerTransactionRepository) Update(ctx context.Context, tx pgx.Tx, t *transaction.WagerTransaction) error {
	const query = `
		UPDATE wager_transactions
		SET
			status               = $1,
			failure_code         = $2,
			attempts             = $3,
			next_retry_at        = $4,
			result_balance_cents = $5,
			reference_transaction_id = $6,
			updated_at           = $7,
			processed_at         = $8
		WHERE id = $9
	`
	var failureCode *string
	if t.FailureCode() != nil {
		s := t.FailureCode().String()
		failureCode = &s
	}

	_, err := tx.Exec(ctx, query,
		string(t.Status()),
		failureCode,
		t.Attempts(),
		t.NextRetryAt(),
		t.ResultBalanceCents(),
		t.ReferenceTransactionID(),
		t.UpdatedAt(),
		t.ProcessedAt(),
		t.ID(),
	)
	if err != nil {
		return fmt.Errorf("atualizar wager_transaction %s: %w", t.ID(), err)
	}
	return nil
}

// FindPendingReferences busca transacoes PENDING_REFERENCE prontas para retry.
func (r *WagerTransactionRepository) FindPendingReferences(ctx context.Context, limit int) ([]*transaction.WagerTransaction, error) {
	const query = `
		SELECT id, external_id, provider_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id,
		       kind, amount_cents, currency,
		       reference_external_id, reference_transaction_id,
		       status, failure_code, attempts, next_retry_at,
		       result_balance_cents, created_at, updated_at, processed_at
		FROM wager_transactions
		WHERE status = 'PENDING_REFERENCE'
		  AND next_retry_at <= NOW()
		ORDER BY next_retry_at ASC
		LIMIT $1
	`
	rows, err := r.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("FindPendingReferences: %w", err)
	}
	defer rows.Close()

	var result []*transaction.WagerTransaction
	for rows.Next() {
		t, err := scanTransactionFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scan pending reference: %w", err)
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// =============================================================================
// Helpers de scan e conversao
// =============================================================================

// nullableString converte string vazia em nil para campos opcionais no banco.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// scanTransaction le uma linha e constroi WagerTransaction via Rehydrate.
func scanTransaction(row pgx.Row) (*transaction.WagerTransaction, error) {
	var (
		id                    uuid.UUID
		externalID            *string
		providerID            *string
		idempotencyKey        *string
		payloadHash           *string
		walletID              uuid.UUID
		playerID              uuid.UUID
		roundID               *string
		gameID                *string
		kind                  string
		amountCents           int64
		currency              string
		referenceExternalID   *string
		referenceTransactionID *uuid.UUID
		status                string
		failureCode           *string
		attempts              int
		nextRetryAt           *time.Time
		resultBalanceCents    *int64
		createdAt             time.Time
		updatedAt             time.Time
		processedAt           *time.Time
	)

	err := row.Scan(
		&id, &externalID, &providerID, &idempotencyKey, &payloadHash,
		&walletID, &playerID, &roundID, &gameID,
		&kind, &amountCents, &currency,
		&referenceExternalID, &referenceTransactionID,
		&status, &failureCode, &attempts, &nextRetryAt,
		&resultBalanceCents, &createdAt, &updatedAt, &processedAt,
	)
	if err != nil {
		return nil, err
	}

	return buildTransaction(
		id, externalID, providerID, idempotencyKey, payloadHash,
		walletID, playerID, roundID, gameID,
		kind, amountCents, currency,
		referenceExternalID, referenceTransactionID,
		status, failureCode, attempts, nextRetryAt,
		resultBalanceCents, createdAt, updatedAt, processedAt,
	), nil
}

// scanTransactionFromRows le de pgx.Rows (para queries que retornam multiplas linhas).
func scanTransactionFromRows(rows pgx.Rows) (*transaction.WagerTransaction, error) {
	var (
		id                    uuid.UUID
		externalID            *string
		providerID            *string
		idempotencyKey        *string
		payloadHash           *string
		walletID              uuid.UUID
		playerID              uuid.UUID
		roundID               *string
		gameID                *string
		kind                  string
		amountCents           int64
		currency              string
		referenceExternalID   *string
		referenceTransactionID *uuid.UUID
		status                string
		failureCode           *string
		attempts              int
		nextRetryAt           *time.Time
		resultBalanceCents    *int64
		createdAt             time.Time
		updatedAt             time.Time
		processedAt           *time.Time
	)

	err := rows.Scan(
		&id, &externalID, &providerID, &idempotencyKey, &payloadHash,
		&walletID, &playerID, &roundID, &gameID,
		&kind, &amountCents, &currency,
		&referenceExternalID, &referenceTransactionID,
		&status, &failureCode, &attempts, &nextRetryAt,
		&resultBalanceCents, &createdAt, &updatedAt, &processedAt,
	)
	if err != nil {
		return nil, err
	}

	return buildTransaction(
		id, externalID, providerID, idempotencyKey, payloadHash,
		walletID, playerID, roundID, gameID,
		kind, amountCents, currency,
		referenceExternalID, referenceTransactionID,
		status, failureCode, attempts, nextRetryAt,
		resultBalanceCents, createdAt, updatedAt, processedAt,
	), nil
}

// buildTransaction centraliza a construcao de WagerTransaction a partir dos campos escaneados.
func buildTransaction(
	id uuid.UUID,
	externalID, providerID, idempotencyKey, payloadHash *string,
	walletID, playerID uuid.UUID,
	roundID, gameID *string,
	kind string, amountCents int64, currency string,
	referenceExternalID *string,
	referenceTransactionID *uuid.UUID,
	status string, failureCode *string,
	attempts int, nextRetryAt *time.Time,
	resultBalanceCents *int64,
	createdAt, updatedAt time.Time,
	processedAt *time.Time,
) *transaction.WagerTransaction {
	var fc *transaction.FailureCode
	if failureCode != nil {
		code := transaction.FailureCode(*failureCode)
		fc = &code
	}

	return transaction.RehydrateTransaction(
		id,
		derefString(externalID),
		derefString(providerID),
		derefString(idempotencyKey),
		derefString(payloadHash),
		walletID, playerID,
		derefString(roundID),
		derefString(gameID),
		transaction.Kind(kind),
		amountCents, currency,
		referenceExternalID,
		referenceTransactionID,
		transaction.Status(status),
		fc,
		attempts,
		nextRetryAt,
		resultBalanceCents,
		createdAt, updatedAt,
		processedAt,
	)
}

// derefString desreferencia um *string, retornando "" se nil.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// mustParseTime converte interface{} para time.Time (usado no scan de timestamps pgx).
func mustParseTime(v interface{}) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	return time.Time{}
}

// assegura que money package e importado via uso indireto
var _ = money.Zero
