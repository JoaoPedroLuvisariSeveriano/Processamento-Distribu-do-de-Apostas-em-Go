import os

wallet_repo_go = """\
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
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// walletRow contem os valores escaneados de uma linha da tabela wallets.
// Usando uma struct intermediaria, o scan fica legivel e seguro.
type walletRow struct {
	id           uuid.UUID
	playerID     uuid.UUID
	currency     string
	balanceCents int64
	version      int64
	createdAt    time.Time
	updatedAt    time.Time
}

// WalletRepository implementa port.WalletRepository com pgx/v5.
// Todas as queries usam SQL explícito — sem ORM, sem gerador de query.
// Isso garante que JOINS, locks e constraints sejam visiveis e verificaveis.
type WalletRepository struct {
	pool *pgxpool.Pool
}

// NewWalletRepository cria um WalletRepository com o pool injetado pelo Fx.
func NewWalletRepository(pool *pgxpool.Pool) *WalletRepository {
	return &WalletRepository{pool: pool}
}

// Create insere uma nova Wallet no banco dentro de uma transacao SQL.
//
// Recebe pgx.Tx porque a insercao da Wallet deve ser atomica com:
//   - Insercao do OPENING WagerTransaction (se saldo inicial > 0)
//   - Insercao do WalletLedgerEntry (lancamento inicial)
//   - Insercao do OutboxEvent (evento WalletBalanceChanged)
func (r *WalletRepository) Create(ctx context.Context, tx pgx.Tx, w *wallet.Wallet) error {
	const query = `
		INSERT INTO wallets (id, player_id, currency, balance_cents, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := tx.Exec(ctx, query,
		w.ID(),
		w.PlayerID(),
		w.Currency(),
		w.Balance().Amount(), // centavos (int64) — NUNCA float
		w.Version(),
		w.CreatedAt(),
		w.UpdatedAt(),
	)
	if err != nil {
		if IsDuplicateKeyError(err) {
			return fmt.Errorf("%w: player=%s currency=%s",
				domain.ErrWalletAlreadyExists, w.PlayerID(), w.Currency())
		}
		return fmt.Errorf("criar wallet: %w", err)
	}
	return nil
}

// FindByID busca uma Wallet pelo ID sem adquirir lock.
// Usa o pool diretamente — ideal para leituras onde nao havera escrita subsequente.
func (r *WalletRepository) FindByID(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error) {
	const query = `
		SELECT id, player_id, currency, balance_cents, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, query, id)
	return scanWalletRow(row)
}

// FindByIDWithLock busca a Wallet e adquire lock pessimista via SELECT FOR UPDATE.
//
// =========================================================================
// POR QUE SELECT FOR UPDATE? (Pessimistic Locking)
// =========================================================================
//
// O problema sem lock:
//
//   Instancia A:  SELECT balance = 100   |
//   Instancia B:               | SELECT balance = 100
//   Instancia A:  saldo ok, debita 80    |
//   Instancia B:               | saldo ok, debita 80
//   Instancia A:  UPDATE balance = 20    |
//   Instancia B:               | UPDATE balance = 20  <- LOST UPDATE!
//   Resultado:  saldo = 20, mas TWO debits foram processados. CATASTROFICO.
//
// Com SELECT FOR UPDATE:
//
//   Instancia A:  SELECT ... FOR UPDATE  <- adquire lock
//   Instancia B:  SELECT ... FOR UPDATE  <- BLOQUEIA (wait)
//   Instancia A:  UPDATE balance = 20    |
//   Instancia A:  COMMIT                 <- libera lock
//   Instancia B:  "acorda", le balance = 20 (ja atualizado)
//   Instancia B:  20 < 80 -> REJEITA (saldo insuficiente)
//   Resultado:  saldo = 20, UM debito. CORRETO.
//
// O lock e POR LINHA (a linha especifica da carteira), nao global.
// Outras carteiras (outras linhas) continuam sendo processadas em paralelo.
//
// O lock e mantido ate o COMMIT ou ROLLBACK da transacao.
// Por isso, este metodo requer pgx.Tx (deve ser chamado dentro de RunInTx).
func (r *WalletRepository) FindByIDWithLock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*wallet.Wallet, error) {
	const query = `
		SELECT id, player_id, currency, balance_cents, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
		FOR UPDATE
	`
	// CRITICO: usamos tx.QueryRow (nao r.pool.QueryRow) para que a query
	// execute NA TRANSACAO EM ANDAMENTO e o lock seja mantido ate o Commit.
	row := tx.QueryRow(ctx, query, id)
	w, err := scanWalletRow(row)
	if err != nil {
		return nil, fmt.Errorf("FindByIDWithLock wallet=%s: %w", id, err)
	}
	return w, nil
}

// FindByPlayerAndCurrency busca por (playerID, currency) — chave natural.
func (r *WalletRepository) FindByPlayerAndCurrency(ctx context.Context, playerID uuid.UUID, currency string) (*wallet.Wallet, error) {
	const query = `
		SELECT id, player_id, currency, balance_cents, version, created_at, updated_at
		FROM wallets
		WHERE player_id = $1 AND currency = $2
	`
	row := r.pool.QueryRow(ctx, query, playerID, currency)
	return scanWalletRow(row)
}

// Update persiste o novo saldo e versao da Wallet dentro de uma transacao.
//
// WHERE version = $previousVersion previne lost updates:
// Se dois processos tentarem atualizar a mesma versao, apenas um tera sucesso.
// O outro recebera RowsAffected = 0 e saberemos que houve conflito.
//
// Com SELECT FOR UPDATE ativo, isso raramente acontece. Mas e nossa segunda
// linha de defesa para casos inesperados (bug, correcao manual no banco, etc.).
func (r *WalletRepository) Update(ctx context.Context, tx pgx.Tx, w *wallet.Wallet) error {
	const query = `
		UPDATE wallets
		SET
			balance_cents = $1,
			version       = $2,
			updated_at    = $3
		WHERE id      = $4
		  AND version = $5
	`
	// O dominio ja incrementou a versao em Credit/Debit.
	// Verificamos que a versao ANTERIOR (currentVersion - 1) e o que esta no banco.
	currentVersion := w.Version()
	previousVersion := currentVersion - 1

	tag, err := tx.Exec(ctx, query,
		w.Balance().Amount(), // novo saldo em centavos
		currentVersion,       // nova versao
		w.UpdatedAt(),        // timestamp de atualizacao
		w.ID(),
		previousVersion, // versao esperada no banco (defesa contra lost update)
	)
	if err != nil {
		return fmt.Errorf("atualizar wallet %s: %w", w.ID(), err)
	}
	if tag.RowsAffected() == 0 {
		// Nenhuma linha afetada = versao nao bateu = concorrencia detectada.
		// Com FOR UPDATE, isso nunca deveria ocorrer em producao, mas e auditavel.
		return fmt.Errorf("wallet %s: lost update detectado (versao esperada %d)", w.ID(), previousVersion)
	}
	return nil
}

// =============================================================================
// Helper: scanWalletRow
// =============================================================================

// scanWalletRow le uma linha da query e reconstroi a Wallet via RehydrateWallet.
// Centralizar o scan evita duplicacao e garante consistencia nos mapeamentos.
func scanWalletRow(row pgx.Row) (*wallet.Wallet, error) {
	var r walletRow
	err := row.Scan(
		&r.id,
		&r.playerID,
		&r.currency,
		&r.balanceCents,
		&r.version,
		&r.createdAt,
		&r.updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWalletNotFound
		}
		return nil, fmt.Errorf("scan wallet: %w", err)
	}
	// RehydrateWallet reconstroi o agregado sem disparar logica de negocio.
	// Nao chama NewWallet (que geraria novo UUID e resetaria versao).
	return wallet.RehydrateWallet(
		r.id,
		r.playerID,
		r.currency,
		r.balanceCents,
		r.version,
		r.createdAt,
		r.updatedAt,
	), nil
}
"""

with open("internal/infrastructure/postgres/wallet_repo.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(wallet_repo_go)

print("wallet_repo.go reescrito corretamente")
