package http

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	auth "github.com/joaoluvisari/backend-challenge-go/internal/presentation/http/middleware"
	"github.com/joaoluvisari/backend-challenge-go/internal/presentation/http/handler"
)

// NewRouter configura e retorna o roteador Chi com os middlewares e rotas.
func NewRouter(
	log *zap.Logger,
	oidcMiddleware *auth.OIDCMiddleware,
	walletHandler *handler.WalletHandler,
	wagerHandler *handler.WagerHandler,
	healthHandler *handler.HealthHandler,
) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Rotas publicas (Health check & Metrics)
	r.Get("/health/live", healthHandler.HandleLive)
	r.Get("/health/ready", healthHandler.HandleReady)
	r.Get("/metrics", healthHandler.HandleMetrics)

	// Rotas protegidas pelo Keycloak
	r.Group(func(r chi.Router) {
		r.Use(oidcMiddleware.Handle)

		r.Post("/wallets", walletHandler.HandleOpenWallet)
		r.Post("/wallets/{walletId}/reconciliation", walletHandler.HandleReconcileWallet)
		r.Get("/wallets/{walletId}", walletHandler.HandleGetWallet)
		r.Get("/wallets/{walletId}/ledger", walletHandler.HandleGetWalletLedger)
		
		r.Post("/wagering/transactions", wagerHandler.HandleProcessWager)
		r.Get("/wagering/transactions/{transactionId}", wagerHandler.HandleGetTransaction)
		r.Get("/providers/{providerId}/wagering/transactions/{externalTransactionId}", wagerHandler.HandleGetProviderTransaction)
	})

	return r
}
