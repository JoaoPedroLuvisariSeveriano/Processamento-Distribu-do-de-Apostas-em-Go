module github.com/joaoluvisari/backend-challenge-go

// Go 1.23 e a versao utilizada - escolhida por ter suporte LTS,
// melhorias de performance no scheduler e suporte moderno a generics.
go 1.23

require (

	// --- AWS SDK v2 - SQS ---
	// Utilizado para integracao com LocalStack (SQS FIFO).
	github.com/aws/aws-sdk-go-v2 v1.32.2
	github.com/aws/aws-sdk-go-v2/config v1.28.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.36.2

	// --- Validacao OIDC / JWT ---
	// coreos/go-oidc valida tokens JWT emitidos pelo Keycloak,
	// verificando assinatura, issuer, expiracao e claims.
	github.com/coreos/go-oidc/v3 v3.11.0

	// --- Roteador HTTP ---
	// chi e leve, 100% compativel com net/http e suporta middlewares
	// encadeados - ideal para autenticacao OIDC por middleware.
	github.com/go-chi/chi/v5 v5.1.0

	// --- Migrations ---
	// golang-migrate aplica e reverte migrations versionadas do schema.
	github.com/golang-migrate/migrate/v4 v4.18.1

	// --- UUID v7 ---
	// google/uuid gera UUIDs v7 (monotonicos, ordenáveis por tempo),
	// usados como IDs de entidades de dominio.
	github.com/google/uuid v1.6.0
	github.com/jackc/pgconn v1.14.3

	// --- Driver PostgreSQL ---
	// pgx/v5 e preferencial conforme README; oferece acesso nativo
	// ao protocolo Postgres, suporte a pgxpool e tipos Go nativos.
	github.com/jackc/pgx/v5 v5.7.1

	// --- Configuracao ---
	// godotenv carrega variaveis de ambiente de .env em desenvolvimento.
	github.com/joho/godotenv v1.5.1

	// --- Precisao Monetaria ---
	// shopspring/decimal fornece aritmetica decimal exata,
	// sem ponto flutuante, para parsing e serializacao de Money.
	github.com/shopspring/decimal v1.4.0
	// --- Framework de Injecao de Dependencias ---
	// Uber Fx orquestra toda a composicao da aplicacao:
	// providers, lifecycle hooks (OnStart/OnStop) e shutdown graceful.
	go.uber.org/fx v1.22.2

	// --- Logging estruturado ---
	// uber/zap fornece logs JSON de alta performance com campos tipados.
	go.uber.org/zap v1.27.0
)

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.17.41 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.17 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.24.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.28.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.32.2 // indirect
	github.com/aws/smithy-go v1.22.0 // indirect
	github.com/go-jose/go-jose/v4 v4.0.2 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/jackc/chunkreader/v2 v2.0.1 // indirect
	github.com/jackc/pgerrcode v0.0.0-20220416144525-469b46aa5efa // indirect
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgproto3/v2 v2.3.3 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/dig v1.18.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/crypto v0.27.0 // indirect

	// --- OAuth2 ---
	// golang.org/x/oauth2 e usado pelo middleware OIDC para buscar
	// o JWKS (JSON Web Key Set) do Keycloak e renovar tokens.
	golang.org/x/oauth2 v0.23.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.18.0 // indirect
)
