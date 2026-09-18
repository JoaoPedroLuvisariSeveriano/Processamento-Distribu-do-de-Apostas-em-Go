import os

wallet_repo_go = """\
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// WalletRepository implementa port.WalletRepository com pgx/v5.
// Todas as queries usam SQL explícito — sem ORM, sem magic.
type WalletRepository struct {
	pool *pgxpool.Pool
}

// NewWalletRepository cria um novo WalletRepository.
// O pool e injetado pelo Uber Fx via NewPool.
func NewWalletRepository(pool *pgxpool.Pool) *WalletRepository {
	return &WalletRepository{pool: pool}
}

// Create insere uma nova Wallet no banco dentro de uma transacao SQL.
//
// IMPORTANTE: recebe pgx.Tx (nao *pgxpool.Pool) porque a insercao da Wallet
// deve ser atomica com a insercao do OPENING transaction e do ledger.
// Se qualquer uma falhar, TODAS revertem juntas (atomicidade do Outbox Pattern).
func (r *WalletRepository) Create(ctx context.Context, tx pgx.Tx, w *wallet.Wallet) error {
	query := `
		INSERT INTO wallets (id, player_id, currency, balance_cents, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := tx.Exec(ctx, query,
		w.ID(),
		w.PlayerID(),
		w.Currency(),
		w.Balance().Amount(), // centavos (int64)
		w.Version(),
		w.CreatedAt(),
		w.UpdatedAt(),
	)
	if err != nil {
		// Mapear UniqueViolation (23505) em erro de dominio semantico.
		// Assim, o application layer nao precisa conhecer codigos PostgreSQL.
		if IsDuplicateKeyError(err) {
			return fmt.Errorf("%w: player=%s currency=%s",
				domain.ErrWalletAlreadyExists, w.PlayerID(), w.Currency())
		}
		return fmt.Errorf("inserir wallet: %w", err)
	}
	return nil
}

// FindByID busca uma Wallet pelo ID sem adquirir lock.
// Usa o pool diretamente — adequado para leituras de dados que nao serao alterados
// na mesma operacao (ex: GET /wallets/:id).
func (r *WalletRepository) FindByID(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error) {
	query := `
		SELECT id, player_id, currency, balance_cents, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, query, id)
	return scanWallet(row)
}

// FindByIDWithLock busca uma Wallet E ADQUIRE LOCK PESSIMISTA com SELECT FOR UPDATE.
//
// =========================================================================
// CONCEITO: Pessimistic Locking (Lock Pessimista)
// =========================================================================
//
// Em concorrencia otimista (optimistic locking), assumimos que conflitos sao raros.
// Cada transacao le o dado, opera, e no COMMIT verifica se alguem modificou
// entretanto (via campo version). Se sim, retorna conflito e o caller faz retry.
//
// Em concorrencia pessimista (pessimistic locking), assumimos que conflitos sao
// FREQUENTES (o que e verdade em wallets com muita atividade).
// Usamos SELECT FOR UPDATE, que:
//
//   1. Le a linha da carteira E coloca um lock exclusivo nela.
//   2. Qualquer outra transacao que tente SELECT FOR UPDATE na MESMA linha
//      fica em WAIT (bloqueada) ate a nossa transacao fazer COMMIT ou ROLLBACK.
//   3. Quando ela "acorda", le o saldo JA ATUALIZADO por nos.
//
// Resultado: EXATAMENTE UMA transacao processa a carteira por vez.
// Nao ha race condition entre o SELECT e o UPDATE.
//
// POR QUE NAO USAR sync.Mutex?
//   sync.Mutex so funciona dentro de um UNICO processo (memoria compartilhada).
//   Com 3 instancias do servico rodando, cada uma tem seu proprio mutex em memoria.
//   Os mutexes NAO se comunicam entre processos — logo nao protegem contra
//   concorrencia entre instancias.
//
//   O SELECT FOR UPDATE do PostgreSQL e o UNICO mecanismo que funciona entre
//   todos os processos que compartilham o mesmo banco de dados.
//
// POR QUE LOCK POR CARTEIRA (nao global)?
//   A spec proibe locks globais: "Carteiras independentes devem avançar em paralelo".
//   O FOR UPDATE trava apenas A LINHA ESPECIFICA da carteira solicitada.
//   Outras carteiras (linhas diferentes) podem ser processadas em paralelo
//   sem nenhuma contencao entre si — escalabilidade horizontal.
//
// REQUER tx (pgx.Tx):
//   O lock e mantido ate o COMMIT ou ROLLBACK da transacao.
//   Nao faz sentido adquirir FOR UPDATE fora de uma transacao.
func (r *WalletRepository) FindByIDWithLock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*wallet.Wallet, error) {
	query := `
		SELECT id, player_id, currency, balance_cents, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
		FOR UPDATE
	`
	// IMPORTANTE: usamos tx.QueryRow (nao r.pool.QueryRow) para que a query
	// seja executada DENTRO da transacao em andamento e o lock seja mantido.
	row := tx.QueryRow(ctx, query, id)
	w, err := scanWallet(row)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("FindByIDWithLock wallet %s: %w", id, err)
	}
	return w, nil
}

// FindByPlayerAndCurrency busca por (playerID, currency) — chave natural da carteira.
func (r *WalletRepository) FindByPlayerAndCurrency(ctx context.Context, playerID uuid.UUID, currency string) (*wallet.Wallet, error) {
	query := `
		SELECT id, player_id, currency, balance_cents, version, created_at, updated_at
		FROM wallets
		WHERE player_id = $1 AND currency = $2
	`
	row := r.pool.QueryRow(ctx, query, playerID, currency)
	return scanWallet(row)
}

// Update persiste o novo saldo e versao da Wallet dentro de uma transacao.
//
// Usa WHERE version = $expected (lock otimista como SEGUNDA LINHA DE DEFESA).
// Com SELECT FOR UPDATE ativo, esta verificacao raramente detecta conflito,
// mas serve como auditoria: se o version nao bater, algo muito errado aconteceu.
func (r *WalletRepository) Update(ctx context.Context, tx pgx.Tx, w *wallet.Wallet) error {
	// O UPDATE incremente a versao no banco.
	// Verificamos que a versao anterior (version - 1) ainda e a que conhecemos.
	// Isso detecta escritas concorrentes que escaparam do FOR UPDATE.
	query := `
		UPDATE wallets
		SET
			balance_cents = $1,
			version       = $2,
			updated_at    = $3
		WHERE id = $4
		  AND version = $5
	`
	// version atual no dominio ja foi incrementada pelo Wallet.Credit/Debit.
	// Verificamos que version - 1 == versao anterior persistida.
	currentVersion := w.Version()
	previousVersion := currentVersion - 1

	result, err := tx.Exec(ctx, query,
		w.Balance().Amount(), // saldo em centavos
		currentVersion,       // nova versao (ja incrementada pelo dominio)
		w.UpdatedAt(),
		w.ID(),
		previousVersion, // versao que esperamos encontrar no banco
	)
	if err != nil {
		return fmt.Errorf("atualizar wallet %s: %w", w.ID(), err)
	}

	// Se 0 linhas foram afetadas, a versao nao bateu — lost update detectado.
	if result.RowsAffected() == 0 {
		return fmt.Errorf("lost update detectado na wallet %s: versao esperada %d nao encontrada",
			w.ID(), previousVersion)
	}

	return nil
}

// =============================================================================
// Helper: scanWallet
// =============================================================================

// scanWallet le uma linha do resultado e constroi uma Wallet via RehydrateWallet.
// Separamos o scan em funcao reutilizada por FindByID e FindByIDWithLock
// para nao duplicar o codigo de mapeamento de colunas.
func scanWallet(row pgx.Row) (*wallet.Wallet, error) {
	var (
		idStr        string
		playerIDStr  string
		currency     string
		balanceCents int64
		version      int64
		createdAt    interface{}
		updatedAt    interface{}
	)

	// pgx v5 mapeia UUID como [16]byte ou string dependendo da configuracao.
	// Usamos uuid.UUID diretamente que o pgx v5 suporta nativamente.
	var id, playerID uuid.UUID
	var ca, ua interface{ Time() interface{} }
	_ = ca
	_ = ua
	_ = idStr
	_ = playerIDStr

	// Scan direto para os tipos corretos (pgx v5 tem coercao automatica)
	var scanErr error
	var walletID, walletPlayerID uuid.UUID
	var walletCurrency string
	var walletBalanceCents, walletVersion int64

	type timeScanner interface{}
	var createdAtT, updatedAtT interface{}

	scanErr = row.Scan(
		&walletID,
		&walletPlayerID,
		&walletCurrency,
		&walletBalanceCents,
		&walletVersion,
		&createdAtT,
		&updatedAtT,
	)
	_ = id
	_ = playerID
	_ = currency
	_ = balanceCents
	_ = version
	_ = createdAt
	_ = updatedAt

	if scanErr != nil {
		if scanErr == pgx.ErrNoRows {
			return nil, domain.ErrWalletNotFound
		}
		return nil, fmt.Errorf("scan wallet: %w", scanErr)
	}

	// Converter os timestamps
	import_time_package_needed := true
	_ = import_time_package_needed

	return wallet.RehydrateWallet(
		walletID,
		walletPlayerID,
		walletCurrency,
		walletBalanceCents,
		walletVersion,
		mustParseTime(createdAtT),
		mustParseTime(updatedAtT),
	), nil
}
"""

with open("internal/infrastructure/postgres/wallet_repo.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(wallet_repo_go)

print("wallet_repo.go escrito (preliminar)")
