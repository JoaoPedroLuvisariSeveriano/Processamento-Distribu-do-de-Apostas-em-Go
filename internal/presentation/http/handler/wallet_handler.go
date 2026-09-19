package handler

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/usecase"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
)

type WalletHandler struct {
	openWalletUC       *usecase.OpenWalletUseCase
	reconcileWalletUC  *usecase.ReconcileWalletUseCase
	queryUC            *usecase.QueryUseCase
	log                *zap.Logger
}

func NewWalletHandler(
	openWalletUC *usecase.OpenWalletUseCase,
	reconcileWalletUC *usecase.ReconcileWalletUseCase,
	queryUC *usecase.QueryUseCase,
	log *zap.Logger,
) *WalletHandler {
	return &WalletHandler{
		openWalletUC:      openWalletUC,
		reconcileWalletUC: reconcileWalletUC,
		queryUC:           queryUC,
		log:               log,
	}
}

type OpenWalletRequest struct {
	PlayerID      string `json:"playerId"`
	Currency      string `json:"currency"`
	Balance       string `json:"balance"`
	CorrelationID string `json:"correlationId"`
}

type OpenWalletResponse struct {
	WalletID string `json:"walletId"`
	Balance  string `json:"balance"`
	Currency string `json:"currency"`
	Created  bool   `json:"created"`
}

func (h *WalletHandler) HandleOpenWallet(w http.ResponseWriter, r *http.Request) {
	var req OpenWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	playerID, err := uuid.Parse(req.PlayerID)
	if err != nil {
		http.Error(w, "Invalid playerId", http.StatusBadRequest)
		return
	}

	corrID, err := uuid.Parse(req.CorrelationID)
	if err != nil {
		// Se nao fornecido, cria um novo
		corrID = uuid.New()
	}

	initialBalance, err := money.Parse(req.Balance, req.Currency)
	if err != nil {
		http.Error(w, "Invalid balance or currency: "+err.Error(), http.StatusBadRequest)
		return
	}

	input := usecase.OpenWalletInput{
		PlayerID:       playerID,
		Currency:       req.Currency,
		InitialBalance: initialBalance,
		CorrelationID:  corrID,
	}

	out, err := h.openWalletUC.Execute(r.Context(), input)
	if err != nil {
		if err == domain.ErrWalletAlreadyExists {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		h.log.Error("Failed to open wallet", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if out.Created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	json.NewEncoder(w).Encode(OpenWalletResponse{
		WalletID: out.Wallet.ID().String(),
		Balance:  out.Wallet.Balance().String(),
		Currency: out.Wallet.Currency(),
		Created:  out.Created,
	})
}

func (h *WalletHandler) HandleReconcileWallet(w http.ResponseWriter, r *http.Request) {
	walletIDStr := chi.URLParam(r, "walletId")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		http.Error(w, "Invalid walletId", http.StatusBadRequest)
		return
	}

	out, err := h.reconcileWalletUC.Execute(r.Context(), walletID)
	if err != nil {
		h.log.Error("Failed to reconcile wallet", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"walletId": out.WalletID.String(),
		"currentBalance": out.CurrentBalance.String(),
		"ledgerCredits": out.LedgerCredits.String(),
		"ledgerDebits": out.LedgerDebits.String(),
		"reconstructedBalance": out.Reconstructed.String(),
		"isConsistent": out.IsConsistent,
	})
}

func (h *WalletHandler) HandleGetWallet(w http.ResponseWriter, r *http.Request) {
	walletIDStr := chi.URLParam(r, "walletId")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		http.Error(w, "Invalid walletId", http.StatusBadRequest)
		return
	}

	wallet, err := h.queryUC.GetWallet(r.Context(), walletID)
	if err != nil {
		h.log.Error("Failed to get wallet", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"walletId": wallet.ID().String(),
		"balance": wallet.Balance().String(),
		"currency": wallet.Currency(),
	})
}

func (h *WalletHandler) HandleGetWalletLedger(w http.ResponseWriter, r *http.Request) {
	walletIDStr := chi.URLParam(r, "walletId")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		http.Error(w, "Invalid walletId", http.StatusBadRequest)
		return
	}

	var afterID *uuid.UUID
	cursorStr := r.URL.Query().Get("cursor")
	if cursorStr != "" {
		id, err := uuid.Parse(cursorStr)
		if err == nil {
			afterID = &id
		}
	}

	limit := 50 // Default limit

	entries, err := h.queryUC.GetWalletLedger(r.Context(), walletID, afterID, limit)
	if err != nil {
		h.log.Error("Failed to get wallet ledger", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var responseEntries []map[string]interface{}
	for _, e := range entries {
		responseEntries = append(responseEntries, map[string]interface{}{
			"id": e.ID().String(),
			"transactionId": e.TransactionID().String(),
			"direction": e.Direction(),
			"amount": e.Amount().String(),
			"balanceBefore": e.BalanceBefore().String(),
			"balanceAfter": e.BalanceAfter().String(),
			"createdAt": e.CreatedAt(),
		})
	}

	var nextCursor *string
	if len(entries) > 0 {
		lastID := entries[len(entries)-1].ID().String()
		nextCursor = &lastID
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entries": responseEntries,
		"nextCursor": nextCursor,
	})
}
