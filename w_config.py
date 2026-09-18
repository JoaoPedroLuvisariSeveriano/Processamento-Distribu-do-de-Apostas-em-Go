import os

config_go = """\
// Package config carrega e valida todas as variaveis de ambiente da aplicacao.
//
// Centralizar a configuracao aqui garante que:
//   1. A aplicacao falha CEDO (no startup, antes de servir requests) se uma
//      variavel obrigatoria estiver faltando — "fail fast" e uma boa pratica.
//   2. O dominio e o application layer nunca acessam os.Getenv diretamente
//      (evita dependencia implicita de ambiente).
//   3. O Uber Fx pode injetar Config como dependencia em qualquer componente.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config agrupa toda a configuracao da aplicacao.
// Cada campo corresponde a uma variavel de ambiente documentada em .env.example.
type Config struct {
	HTTP     HTTPConfig
	Database DatabaseConfig
	SQS      SQSConfig
	OIDC     OIDCConfig
	Workers  WorkersConfig
	Env      string
	LogLevel string
}

// HTTPConfig contem parametros do servidor HTTP.
type HTTPConfig struct {
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// DatabaseConfig contem parametros de conexao com o PostgreSQL.
type DatabaseConfig struct {
	URL             string
	MaxOpenConns    int32
	MaxIdleConns    int32
	ConnMaxLifetime time.Duration
}

// SQSConfig contem URLs e parametros das filas SQS.
type SQSConfig struct {
	Region                  string
	EndpointURL             string
	WagerTransactionsURL    string
	WagerEventsURL          string
	DLQURL                  string
	MaxMessages             int32
	VisibilityTimeout       int32
	WaitTimeSeconds         int32
	MaxRetries              int
}

// OIDCConfig contem parametros de autenticacao OIDC (Keycloak).
type OIDCConfig struct {
	IssuerURL string
	ClientID  string
}

// WorkersConfig contem intervalos dos workers de background.
type WorkersConfig struct {
	OutboxInterval    time.Duration
	OutboxBatchSize   int
	PendingRefInterval time.Duration
	PendingRefMaxAttempts int
}

// Load carrega e valida a configuracao a partir de variaveis de ambiente.
// Retorna erro se qualquer variavel obrigatoria estiver ausente ou invalida.
// Chamado pelo Fx no startup da aplicacao — falha antes de iniciar qualquer servico.
func Load() (*Config, error) {
	cfg := &Config{}

	// --- HTTP ---
	cfg.HTTP.Port = getEnvOrDefault("HTTP_PORT", "3000")
	cfg.HTTP.ReadTimeout = parseDuration("HTTP_READ_TIMEOUT", 30*time.Second)
	cfg.HTTP.WriteTimeout = parseDuration("HTTP_WRITE_TIMEOUT", 30*time.Second)
	cfg.HTTP.IdleTimeout = parseDuration("HTTP_IDLE_TIMEOUT", 120*time.Second)

	// --- Database ---
	dbURL, err := requireEnv("DATABASE_URL")
	if err != nil {
		return nil, err
	}
	cfg.Database.URL = dbURL
	cfg.Database.MaxOpenConns = int32(parseInt("DATABASE_MAX_OPEN_CONNS", 25))
	cfg.Database.MaxIdleConns = int32(parseInt("DATABASE_MAX_IDLE_CONNS", 5))
	cfg.Database.ConnMaxLifetime = parseDuration("DATABASE_CONN_MAX_LIFETIME", 5*time.Minute)

	// --- SQS ---
	cfg.SQS.Region = getEnvOrDefault("AWS_REGION", "us-east-1")
	cfg.SQS.EndpointURL = getEnvOrDefault("AWS_ENDPOINT_URL", "http://localhost:4566")
	cfg.SQS.WagerTransactionsURL = getEnvOrDefault("SQS_WAGER_TRANSACTIONS_URL", "")
	cfg.SQS.WagerEventsURL = getEnvOrDefault("SQS_WAGER_EVENTS_URL", "")
	cfg.SQS.DLQURL = getEnvOrDefault("SQS_DLQ_URL", "")
	cfg.SQS.MaxMessages = int32(parseInt("SQS_MAX_MESSAGES", 10))
	cfg.SQS.VisibilityTimeout = int32(parseInt("SQS_VISIBILITY_TIMEOUT", 30))
	cfg.SQS.WaitTimeSeconds = int32(parseInt("SQS_WAIT_TIME_SECONDS", 20))
	cfg.SQS.MaxRetries = parseInt("SQS_MAX_RETRIES", 5)

	// --- OIDC ---
	oidcIssuer, err := requireEnv("OIDC_ISSUER_URL")
	if err != nil {
		return nil, err
	}
	cfg.OIDC.IssuerURL = oidcIssuer
	cfg.OIDC.ClientID = getEnvOrDefault("OIDC_CLIENT_ID", "betting-service")

	// --- Workers ---
	cfg.Workers.OutboxInterval = parseDuration("OUTBOX_WORKER_INTERVAL", 5*time.Second)
	cfg.Workers.OutboxBatchSize = parseInt("OUTBOX_WORKER_BATCH_SIZE", 100)
	cfg.Workers.PendingRefInterval = parseDuration("PENDING_REF_WORKER_INTERVAL", 10*time.Second)
	cfg.Workers.PendingRefMaxAttempts = parseInt("PENDING_REF_MAX_ATTEMPTS", 5)

	// --- Geral ---
	cfg.Env = getEnvOrDefault("ENV", "development")
	cfg.LogLevel = getEnvOrDefault("LOG_LEVEL", "info")

	return cfg, nil
}

// requireEnv retorna erro se a variavel de ambiente nao estiver definida.
func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("variavel de ambiente obrigatoria ausente: %s", key)
	}
	return v, nil
}

// getEnvOrDefault retorna o valor da variavel ou o default se nao estiver definida.
func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// parseInt parseia uma variavel como int com fallback para default.
func parseInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultValue
}

// parseDuration parseia uma variavel como time.Duration com fallback.
func parseDuration(key string, defaultValue time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultValue
}
"""

with open("internal/infrastructure/config/config.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(config_go)

print("config.go criado")
