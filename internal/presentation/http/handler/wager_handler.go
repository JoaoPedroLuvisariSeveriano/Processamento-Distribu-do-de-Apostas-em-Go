package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/usecase"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	"github.com/joaoluvisari/backend-challenge-go/internal/presentation/http/middleware"
)

type WagerHandler struct {
	processWagerUC *usecase.ProcessWagerUseCase
	queryUC        *usecase.QueryUseCase
	log            *zap.Logger
}

func NewWagerHandler(processWagerUC *usecase.ProcessWagerUseCase, queryUC *usecase.QueryUseCase, log *zap.Logger) *WagerHandler {
	return &WagerHandler{
		processWagerUC: processWagerUC,
		queryUC:        queryUC,
		log:            log,
	}
}

type ProcessWagerRequest struct {
	TransactionID       string  `json:"transactionId"` // external transaction id
	IdempotencyKey      string  `json:"idempotencyKey"`
	PlayerID            string  `json:"playerId"`
	WalletID            string  `json:"walletId"`
	RoundID             string  `json:"roundId"`
	GameID              string  `json:"gameId"`
	Kind                string  `json:"kind"` // BET, WIN, LOSS, REFUND, ROLLBACK
	Amount              string  `json:"amount"`
	Currency            string  `json:"currency"`
	ReferenceExternalID *string `json:"referenceExternalId,omitempty"`
	CorrelationID       string  `json:"correlationId"`
}

type ProcessWagerResponse struct {
	TransactionID    string  `json:"transactionId"` // internal transaction id
	Status           string  `json:"status"`
	Balance          *string `json:"balance,omitempty"`
	IdempotentReplay bool    `json:"idempotentReplay"`
	FailureCode      *string `json:"failureCode,omitempty"`
}

func (h *WagerHandler) HandleProcessWager(w http.ResponseWriter, r *http.Request) {
	providerID, err := middleware.GetProviderID(r.Context())
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req ProcessWagerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	playerID, err := uuid.Parse(req.PlayerID)
	if err != nil {
		http.Error(w, "Invalid playerId", http.StatusBadRequest)
		return
	}

	walletID, err := uuid.Parse(req.WalletID)
	if err != nil {
		http.Error(w, "Invalid walletId", http.StatusBadRequest)
		return
	}

	corrID, err := uuid.Parse(req.CorrelationID)
	if err != nil {
		corrID = uuid.New()
	}

	amount, err := money.Parse(req.Amount, req.Currency)
	if err != nil {
		http.Error(w, "Invalid amount or currency: "+err.Error(), http.StatusBadRequest)
		return
	}

	input := usecase.ProcessWagerInput{
		ExternalTransactionID: req.TransactionID,
		ProviderID:            providerID,
		IdempotencyKey:        req.IdempotencyKey,
		PlayerID:              playerID,
		WalletID:              walletID,
		RoundID:               req.RoundID,
		GameID:                req.GameID,
		Kind:                  transaction.Kind(req.Kind),
		Amount:                amount,
		ReferenceExternalID:   req.ReferenceExternalID,
		CorrelationID:         corrID,
	}

	out, err := h.processWagerUC.Execute(r.Context(), input)
	if err != nil {
		if errors.Is(err, usecase.ErrIdempotencyConflict) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, usecase.ErrInvalidOpeningKind) || errors.Is(err, usecase.ErrWalletOwnerMismatch) {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		
		h.log.Error("Failed to process wager", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	var balanceStr *string
	if out.Balance != nil {
		s := out.Balance.String()
		balanceStr = &s
	}

	var failCode *string
	if out.FailureCode != nil {
		s := string(*out.FailureCode)
		failCode = &s
	}

	json.NewEncoder(w).Encode(ProcessWagerResponse{
		TransactionID:    out.TransactionID.String(),
		Status:           string(out.Status),
		Balance:          balanceStr,
		IdempotentReplay: out.IdempotentReplay,
		FailureCode:      failCode,
	})
}

func (h *WagerHandler) HandleGetTransaction(w http.ResponseWriter, r *http.Request) {
	transactionIDStr := chi.URLParam(r, "transactionId")
	transactionID, err := uuid.Parse(transactionIDStr)
	if err != nil {
		http.Error(w, "Invalid transactionId", http.StatusBadRequest)
		return
	}

	t, err := h.queryUC.GetTransaction(r.Context(), transactionID)
	if err != nil {
		h.log.Error("Failed to get transaction", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"transactionId": t.ID().String(),
		"externalTransactionId": t.ExternalID(),
		"providerId": t.ProviderID(),
		"walletId": t.WalletID().String(),
		"playerId": t.PlayerID().String(),
		"roundId": t.RoundID(),
		"gameId": t.GameID(),
		"kind": t.Kind(),
		"amount": t.Amount().String(),
		"currency": t.Amount().Currency(),
		"status": t.Status(),
		"referenceExternalId": t.ReferenceExternalID(),
		"createdAt": t.CreatedAt(),
	})
}

func (h *WagerHandler) HandleGetProviderTransaction(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerId")
	externalID := chi.URLParam(r, "externalTransactionId")
	
	// Authentication context validation
	authProviderID, err := middleware.GetProviderID(r.Context())
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if authProviderID != providerID {
		http.Error(w, "Forbidden: you can only view your own transactions", http.StatusForbidden)
		return
	}

	t, err := h.queryUC.GetProviderTransaction(r.Context(), providerID, externalID)
	if err != nil {
		h.log.Error("Failed to get provider transaction", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"transactionId": t.ID().String(),
		"externalTransactionId": t.ExternalID(),
		"providerId": t.ProviderID(),
		"walletId": t.WalletID().String(),
		"playerId": t.PlayerID().String(),
		"roundId": t.RoundID(),
		"gameId": t.GameID(),
		"kind": t.Kind(),
		"amount": t.Amount().String(),
		"currency": t.Amount().Currency(),
		"status": t.Status(),
		"referenceExternalId": t.ReferenceExternalID(),
		"createdAt": t.CreatedAt(),
	})
}
