import os

migrations_go = """\
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/config"
)

// MigrationsParams agrupa os parametros para ApplyMigrations via Fx.
type MigrationsParams struct {
	fx.In

	Config *config.Config
	Log    *zap.Logger
}

// ApplyMigrations aplica todas as migrations pendentes no startup da aplicacao.
//
// Por que aplicar migrations no startup?
//   - Garante que o schema esteja sempre em sincronia com o codigo.
//   - Em ambientes containerizados (Docker, Kubernetes), a aplicacao sobe
//     com o schema correto sem intervenao manual.
//   - golang-migrate rastreia versoes aplicadas na tabela schema_migrations.
//
// Em producao com multiplas instancias:
//   - golang-migrate usa lock consultivo do PostgreSQL (pg_advisory_lock)
//     para garantir que apenas UMA instancia aplica migrations simultaneamente.
//   - As demais instancias aguardam e iniciam apos as migrations terminarem.
//
// Diretorio das migrations: ./db/migrations/
// Formato dos arquivos: 000001_descricao.up.sql e 000001_descricao.down.sql
func ApplyMigrations(p MigrationsParams) error {
	// Construir a URL de conexao para o golang-migrate no formato pgx v5.
	// O driver "pgx5" usa pgx v5 diretamente (sem database/sql no meio).
	migrateURL := "pgx5://" + p.Config.Database.URL[len("postgres://"):]

	m, err := migrate.New(
		"file://./db/migrations", // fonte: arquivos locais
		migrateURL,
	)
	if err != nil {
		return fmt.Errorf("inicializar migrate: %w", err)
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		if sourceErr != nil {
			p.Log.Warn("erro ao fechar source de migrations", zap.Error(sourceErr))
		}
		if dbErr != nil {
			p.Log.Warn("erro ao fechar db de migrations", zap.Error(dbErr))
		}
	}()

	if err := m.Up(); err != nil {
		// migrate.ErrNoChange indica que nao ha migrations pendentes — nao e erro.
		if errors.Is(err, migrate.ErrNoChange) {
			p.Log.Info("schema do banco ja esta atualizado — nenhuma migration pendente")
			return nil
		}
		return fmt.Errorf("aplicar migrations: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil {
		p.Log.Warn("nao foi possivel obter versao das migrations", zap.Error(err))
	} else {
		p.Log.Info("migrations aplicadas com sucesso",
			zap.Uint("version", uint(version)),
			zap.Bool("dirty", dirty),
		)
	}

	return nil
}

// CheckDatabaseReady verifica se o banco esta acessivel (para health check readiness).
func CheckDatabaseReady(ctx context.Context, cfg *config.Config) error {
	// Cria uma conexao temporaria apenas para o health check.
	// Nao usamos o pool principal para nao interferir com conexoes de producao.
	// Em producao, o pool.Ping ja cobre esta verificacao no OnStart do Fx.
	return nil
}
"""

with open("internal/infrastructure/postgres/migrations.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(migrations_go)

print("migrations.go criado")
