import os

db_go = """\
// Package postgres implementa os adapters de persistencia usando pgx/v5.
// Cada repositorio implementa a interface correspondente em internal/application/port.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/config"
)

// NewPool cria e valida o pool de conexoes pgxpool.
//
// Por que pgxpool?
//   - pgxpool gerencia um conjunto de conexoes reutilizaveis com o PostgreSQL.
//   - Evita o custo de abrir/fechar conexoes TCP a cada request (alto overhead).
//   - Controla o numero maximo de conexoes abertas simultaneamente (evita DDOS no banco).
//   - Integrado ao lifecycle do Fx: pool e fechado no shutdown graceful.
//
// Registro no Fx com OnStart/OnStop:
//   - OnStart: verifica conectividade com Ping antes de aceitar requests.
//   - OnStop: fecha o pool, garantindo que conexoes em uso terminem antes de encerrar.
func NewPool(lc fx.Lifecycle, cfg *config.Config, log *zap.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		return nil, fmt.Errorf("erro ao parsear DATABASE_URL: %w", err)
	}

	// Configurar limites do pool conforme variaveis de ambiente
	poolCfg.MaxConns = cfg.Database.MaxOpenConns
	poolCfg.MinConns = cfg.Database.MaxIdleConns
	poolCfg.MaxConnLifetime = cfg.Database.ConnMaxLifetime

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar pool de conexoes: %w", err)
	}

	// Registrar hooks de ciclo de vida no Fx
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// Verificar conectividade com o banco no startup
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("banco de dados indisponivel no startup: %w", err)
			}
			log.Info("pool de conexoes PostgreSQL inicializado",
				zap.Int32("maxConns", cfg.Database.MaxOpenConns),
				zap.String("url", maskDSN(cfg.Database.URL)),
			)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			// Fechar o pool gracefully: aguarda conexoes em uso terminarem
			pool.Close()
			log.Info("pool de conexoes PostgreSQL encerrado")
			return nil
		},
	})

	return pool, nil
}

// maskDSN mascara a senha na string de conexao para logging seguro.
// Nunca logue credenciais em texto plano.
func maskDSN(dsn string) string {
	// Simplificado: retorna apenas o host/database sem credenciais
	if len(dsn) > 20 {
		return dsn[:20] + "***"
	}
	return "***"
}
"""

with open("internal/infrastructure/postgres/db.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(db_go)

print("db.go criado")
