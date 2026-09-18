import os

uow_go = """\
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
)

// RunInTx executa a funcao fn dentro de uma transacao PostgreSQL atomica.
//
// GARANTIA DE ATOMICIDADE:
// Todas as operacoes dentro de fn sao confirmadas (COMMIT) ou revertidas
// (ROLLBACK) juntas. Nao ha estado intermediario visivel para outras conexoes.
//
// Por que isso e critico para o nosso sistema?
// Uma operacao de BET precisa:
//   1. Inserir WagerTransaction (idempotencia)
//   2. Atualizar saldo da Wallet (debito)
//   3. Inserir WalletLedgerEntry (auditoria)
//   4. Inserir OutboxEvent (publicacao posterior)
//   5. Inserir InboxMessage (se veio do SQS)
//
// Se QUALQUER uma dessas etapas falhar, TODAS devem ser revertidas.
// RunInTx garante que ou TUDO comita ou NADA comita — sem estados parciais.
//
// Fluxo:
//   pool.Acquire -> tx.Begin -> fn(ctx, tx) -> tx.Commit/Rollback
//
// O defer tx.Rollback() e seguro mesmo apos tx.Commit():
//   - Apos um Commit bem-sucedido, Rollback retorna ErrTxClosed (ignorado).
//   - Se fn retornar erro, Rollback e executado pelo defer.
//   - Se o processo crashar entre Commit e o return, o banco faz rollback automatico.
func RunInTx(pool *pgxpool.Pool) port.RunInTxFunc {
	return func(ctx context.Context, fn port.TxFunc) error {
		// Adquirir uma conexao do pool para esta transacao.
		// A conexao e devolvida ao pool apos o defer conn.Release().
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("adquirir conexao do pool: %w", err)
		}
		defer conn.Release()

		// Iniciar a transacao com nivel de isolamento READ COMMITTED (padrao PostgreSQL).
		// READ COMMITTED e suficiente porque usamos SELECT FOR UPDATE (pessimistic lock)
		// para coordenar escritas concorrentes — o nivel SERIALIZABLE adicionaria
		// overhead sem beneficio adicional para nosso caso de uso.
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("iniciar transacao: %w", err)
		}

		// defer: garante rollback se fn retornar erro OU se houver panic.
		// pgx.ErrTxClosed e retornado apos Commit — pode ser ignorado.
		defer func() {
			// Tenta rollback; se a tx ja foi commitada, o erro e inofensivo.
			_ = tx.Rollback(ctx)
		}()

		// Executar a logica de negocio dentro da transacao.
		// fn recebe tx para que todos os repositorios participem da mesma transacao.
		if err := fn(ctx, tx); err != nil {
			// O defer acima fara o rollback.
			return err
		}

		// Confirmar a transacao atomicamente.
		// Se Commit falhar (ex: rede, constraint violation), o defer fara rollback.
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("comitar transacao: %w", err)
		}

		return nil
	}
}

// IsDuplicateKeyError retorna true se o erro do pgx e uma violacao de chave unica.
// Usado pelos repositorios para mapear erros do banco em erros de dominio.
//
// O codigo de erro PostgreSQL 23505 e o codigo oficial para unique_violation.
// Mapeamos isso para erros de dominio (ex: ErrWalletAlreadyExists, ErrDuplicateTransaction)
// em vez de expor codigos de banco para as camadas superiores.
//
// pgconn.PgError e o tipo concreto retornado pelo driver pgx/pgconn para erros
// originados no servidor PostgreSQL. Usamos errors.As para percorrer a chain de wrapping.
func IsDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// Codigo 23505 = unique_violation (RFC PostgreSQL Error Codes)
		return pgErr.Code == "23505"
	}
	return false
}

// IsNoRowsError retorna true se o erro indica que nenhuma linha foi encontrada.
func IsNoRowsError(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
"""

with open("internal/infrastructure/postgres/unit_of_work.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(uow_go)

print("unit_of_work.go reescrito com pgconn.PgError correto")
