package di

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	"github.com/joaoluvisari/backend-challenge-go/internal/application/usecase"
	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/config"
	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/postgres"
	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/worker"
	myhttp "github.com/joaoluvisari/backend-challenge-go/internal/presentation/http"
	"github.com/joaoluvisari/backend-challenge-go/internal/presentation/http/handler"
	auth "github.com/joaoluvisari/backend-challenge-go/internal/presentation/http/middleware"
)

// OIDCProvider cria o middleware de autenticacao usando as configuracoes globais.
func OIDCProvider(cfg *config.Config, log *zap.Logger) (*auth.OIDCMiddleware, error) {
	// Fallbacks para URL e ClientID caso a struct de config nao tenha metodos diretos,
	// adaptamos conforme a estrutura.
	providerURL := cfg.OIDC.ProviderURL
	if providerURL == "" {
		providerURL = "http://localhost:8080/realms/jungle"
	}
	clientID := cfg.OIDC.ClientID
	if clientID == "" {
		clientID = "backend-challenge"
	}
	return auth.NewOIDCMiddleware(providerURL, clientID, log)
}

// StartHTTPServer gerencia o ciclo de vida do servidor HTTP integrado ao Uber Fx.
func StartHTTPServer(lc fx.Lifecycle, router *chi.Mux, cfg *config.Config, log *zap.Logger) {
	portStr := cfg.HTTP.Port
	if portStr == "" {
		portStr = "3000"
	}

	server := &http.Server{
		Addr:    ":" + portStr,
		Handler: router,
	}

	// fx.Lifecycle orquestra start/stop da aplicacao (graceful shutdown)
	lc.Append(fx.Hook{
		// OnStart sobe o servidor HTTP em background (goroutine) para nao bloquear
		OnStart: func(ctx context.Context) error {
			log.Info("Starting HTTP server", zap.String("port", server.Addr))
			go func() {
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Fatal("HTTP server failed", zap.Error(err))
				}
			}()
			return nil
		},
		// OnStop executa o graceful shutdown limitando o tempo para as requests ativas terminarem
		OnStop: func(ctx context.Context) error {
			log.Info("Shutting down HTTP server")
			
			// 5 segundos de timeout para requests pendentes finalizarem
			shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			
			return server.Shutdown(shutdownCtx)
		},
	})
}

// Module reune todas as dependencias da aplicacao.
var Module = fx.Options(
	// 1. Core (Config & Logger)
	fx.Provide(
		config.Load,
		zap.NewProduction,
	),

	// 2. Infraestrutura (Banco de Dados e Connection Pool)
	// postgres.NewPool depende de fx.Lifecycle, *config.Config e *zap.Logger
	fx.Provide(
		postgres.NewPool,
		postgres.NewWalletRepository,
		postgres.NewWagerTransactionRepository,
		postgres.NewLedgerRepository,
		postgres.NewOutboxRepository,
		postgres.NewInboxRepository,
		postgres.RunInTx,
	),

	// 3. Mapeamento de interfaces concretas para os ports dos Use Cases
	fx.Provide(
		fx.Annotate(
			postgres.NewWalletRepository,
			fx.As(new(port.WalletRepository)),
		),
		fx.Annotate(
			postgres.NewWagerTransactionRepository,
			fx.As(new(port.WagerTransactionRepository)),
		),
		fx.Annotate(
			postgres.NewLedgerRepository,
			fx.As(new(port.LedgerRepository)),
		),
		fx.Annotate(
			postgres.NewOutboxRepository,
			fx.As(new(port.OutboxRepository)),
		),
		fx.Annotate(
			postgres.NewInboxRepository,
			fx.As(new(port.InboxRepository)),
		),
	),

	// 4. Use Cases
	fx.Provide(
		usecase.NewOpenWalletUseCase,
		usecase.NewProcessWagerUseCase,
	),

	// 5. Apresentacao (HTTP) e Middlewares
	fx.Provide(
		OIDCProvider,
		handler.NewWalletHandler,
		handler.NewWagerHandler,
		myhttp.NewRouter,
	),

	// 6. Invoke para iniciar servicos de background/servidores
	// fx.Invoke forca a construcao dos modulos e executa os ciclos de vida OnStart
	fx.Invoke(StartHTTPServer),

	// 7. Workers background
	worker.Module,
)
