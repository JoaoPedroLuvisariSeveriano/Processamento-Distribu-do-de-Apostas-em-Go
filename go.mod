module github.com/joaoluvisari/backend-challenge-go

// Go 1.23 e a versao utilizada - escolhida por ter suporte LTS,
// melhorias de performance no scheduler e suporte moderno a generics.
go 1.23

require (
	// --- Framework de Injecao de Dependencias ---
	// Uber Fx orquestra toda a composicao da aplicacao:
	// providers, lifecycle hooks (OnStart/OnStop) e shutdown graceful.
	go.uber.org/fx v1.22.2

	// --- Roteador HTTP ---
	// chi e leve, 100% compativel com net/http e suporta middlewares
	// encadeados - ideal para autenticacao OIDC por middleware.
	github.com/go-chi/chi/v5 v5.1.0

	// --- Driver PostgreSQL ---
	// pgx/v5 e preferencial conforme README; oferece acesso nativo
	// ao protocolo Postgres, suporte a pgxpool e tipos Go nativos.
	github.com/jackc/pgx/v5 v5.7.1

	// --- AWS SDK v2 - SQS ---
	// Utilizado para integracao com LocalStack (SQS FIFO).
	github.com/aws/aws-sdk-go-v2 v1.32.2
	github.com/aws/aws-sdk-go-v2/config v1.28.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.36.2

	// --- Precisao Monetaria ---
	// shopspring/decimal fornece aritmetica decimal exata,
	// sem ponto flutuante, para parsing e serializacao de Money.
	github.com/shopspring/decimal v1.4.0

	// --- Validacao OIDC / JWT ---
	// coreos/go-oidc valida tokens JWT emitidos pelo Keycloak,
	// verificando assinatura, issuer, expiracao e claims.
	github.com/coreos/go-oidc/v3 v3.11.0

	// --- UUID v7 ---
	// google/uuid gera UUIDs v7 (monotonicos, ordenáveis por tempo),
	// usados como IDs de entidades de dominio.
	github.com/google/uuid v1.6.0

	// --- Migrations ---
	// golang-migrate aplica e reverte migrations versionadas do schema.
	github.com/golang-migrate/migrate/v4 v4.18.1

	// --- Configuracao ---
	// godotenv carrega variaveis de ambiente de .env em desenvolvimento.
	github.com/joho/godotenv v1.5.1

	// --- Logging estruturado ---
	// uber/zap fornece logs JSON de alta performance com campos tipados.
	go.uber.org/zap v1.27.0

	// --- OAuth2 ---
	// golang.org/x/oauth2 e usado pelo middleware OIDC para buscar
	// o JWKS (JSON Web Key Set) do Keycloak e renovar tokens.
	golang.org/x/oauth2 v0.23.0
)
