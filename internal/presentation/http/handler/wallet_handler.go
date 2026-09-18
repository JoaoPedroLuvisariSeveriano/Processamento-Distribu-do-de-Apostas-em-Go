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
	openWalletUC *usecase.OpenWalletUseCase
	log          *zap.Logger
}

func NewWalletHandler(openWalletUC *usecase.OpenWalletUseCase, log *zap.Logger) *WalletHandler {
	return &WalletHandler{
		openWalletUC: openWalletUC,
		log:          log,
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
