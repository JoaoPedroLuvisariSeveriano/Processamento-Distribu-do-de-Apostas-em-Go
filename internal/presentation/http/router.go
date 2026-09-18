package http

import (
	"net/http"

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
) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Rotas pblicas (Health check)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Rotas protegidas pelo Keycloak
	r.Group(func(r chi.Router) {
		r.Use(oidcMiddleware.Handle)

		r.Post("/wallets", walletHandler.HandleOpenWallet)
		r.Post("/wagering/transactions", wagerHandler.HandleProcessWager)
		// r.Get(...)
	})

	return r
}
