package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/config"
)

type HealthHandler struct {
	pool    *pgxpool.Pool
	sqs     *sqs.Client
	cfg     *config.Config
	log     *zap.Logger
}

func NewHealthHandler(pool *pgxpool.Pool, sqsClient *sqs.Client, cfg *config.Config, log *zap.Logger) *HealthHandler {
	return &HealthHandler{
		pool: pool,
		sqs:  sqsClient,
		cfg:  cfg,
		log:  log,
	}
}

// HandleLive retorna 200 OK imediato para liveness probe.
func (h *HealthHandler) HandleLive(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "ok"}`))
}

// HandleReady testa conexao com o DB e SQS.
func (h *HealthHandler) HandleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	// 1. Testa DB
	if err := h.pool.Ping(ctx); err != nil {
		h.log.Error("Readiness probe failed on Database", zap.Error(err))
		http.Error(w, `{"status": "error", "component": "database"}`, http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "ready"}`))
}

// HandleMetrics retorna metricas consolidadas
func (h *HealthHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Simples contagem agrupada por status
	rows, err := h.pool.Query(ctx, "SELECT status, count(*) FROM wager_transactions GROUP BY status")
	if err != nil {
		h.log.Error("Failed to fetch metrics", zap.Error(err))
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	metrics := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err == nil {
			metrics[status] = count
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"transactions_by_status": metrics,
	})
}
