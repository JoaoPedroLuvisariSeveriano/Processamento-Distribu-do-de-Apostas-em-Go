package di

import (
	"os"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	"github.com/joaoluvisari/backend-challenge-go/internal/application/usecase"
	"github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/postgres"
	"github.com/joaoluvisari/backend-challenge-go/internal/presentation/http"
	"github.com/joaoluvisari/backend-challenge-go/internal/presentation/http/handler"
	auth "github.com/joaoluvisari/backend-challenge-go/internal/presentation/http/middleware"
)

// OIDCProvider cria o middleware de autenticao lendo variveis de ambiente.
func OIDCProvider(log *zap.Logger) (*auth.OIDCMiddleware, error) {
	providerURL := os.Getenv("OIDC_PROVIDER_URL")
	if providerURL == "" {
		providerURL = "http://localhost:8080/realms/jungle" // default
	}
	clientID := os.Getenv("OIDC_CLIENT_ID")
	if clientID == "" {
		clientID = "backend-challenge" // default
	}
	return auth.NewOIDCMiddleware(providerURL, clientID, log)
}

// Module rene todas as dependncias da aplicao
var Module = fx.Options(
	// 1. Logger
	fx.Provide(zap.NewProduction),

	// 2. Infraestrutura (Banco de Dados)
	// Aqui assumimos que postgres.NewPool j existe e l a config/env.
	// Por simplicidade, vamos usar os construtores dos repositrios.
	fx.Provide(
		postgres.NewWalletRepository,
		postgres.NewWagerTransactionRepository,
		postgres.NewLedgerRepository,
		postgres.NewOutboxRepository,
		postgres.NewInboxRepository,
		postgres.RunInTx,
	),

	// 3. Mapeamento de interfaces para os Use Cases
	// fx.Provide injeta as interfaces concretas onde so esperadas interfaces (ports).
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

	// 5. Apresentao (HTTP)
	fx.Provide(
		OIDCProvider,
		handler.NewWalletHandler,
		handler.NewWagerHandler,
		http.NewRouter,
	),
)
