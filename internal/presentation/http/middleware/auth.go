package middleware

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.uber.org/zap"
)

// ProviderIDContextKey é o tipo para a chave do contexto
type ProviderIDContextKey struct{}

// OIDCMiddleware é o middleware que valida o token JWT do Keycloak
type OIDCMiddleware struct {
	verifier *oidc.IDTokenVerifier
	log      *zap.Logger
}

// Parâmetros de retry para a inicialização do provider OIDC.
//
// Por que retry com backoff exponencial?
//   - O Keycloak é uma aplicação Java pesada que, especialmente no Windows via Docker,
//     pode levar mais de 1 minuto para subir a JVM, conectar ao PostgreSQL e importar o realm.
//   - O Uber Fx executa os construtores (fx.Provide) de forma síncrona antes de qualquer
//     OnStart, então oidc.NewProvider é chamado na fase de wiring da aplicação.
//   - Sem retry, um único 404 (realm ainda não importado) faz o Fx abortar tudo —
//     inclusive as migrations do PostgreSQL.
//   - Com backoff exponencial, a API aguarda pacientemente o Keycloak ficar pronto
//     sem consumir CPU em loop apertado.
const (
	// oidcMaxAttempts é o número máximo de tentativas antes de desistir.
	// 20 tentativas × máximo 30s ≈ ~10 minutos de janela de tolerância.
	oidcMaxAttempts = 20

	// oidcBaseDelay é o delay inicial entre tentativas (dobra a cada falha).
	oidcBaseDelay = 3 * time.Second

	// oidcMaxDelay é o teto do backoff exponencial — evita esperas excessivamente longas.
	oidcMaxDelay = 30 * time.Second
)

// newProviderWithRetry tenta criar o provider OIDC com backoff exponencial.
//
// Sequência de delays (aproximada):
//
//	Tentativa 1 → falhou → espera 3s
//	Tentativa 2 → falhou → espera 6s
//	Tentativa 3 → falhou → espera 12s
//	Tentativa 4 → falhou → espera 24s
//	Tentativa 5+ → falhou → espera 30s (capped)
//
// O ctx permite cancelamento externo (e.g., SIGTERM durante o startup).
func newProviderWithRetry(ctx context.Context, providerURL string, log *zap.Logger) (*oidc.Provider, error) {
	var lastErr error

	for attempt := 0; attempt < oidcMaxAttempts; attempt++ {
		// Aguarda o backoff antes de tentar (exceto na primeira tentativa).
		if attempt > 0 {
			delay := time.Duration(math.Min(
				float64(oidcBaseDelay)*math.Pow(2, float64(attempt-1)),
				float64(oidcMaxDelay),
			))

			log.Warn("OIDC provider indisponivel, aguardando Keycloak...",
				zap.String("issuerURL", providerURL),
				zap.Int("tentativa", attempt),
				zap.Int("maxTentativas", oidcMaxAttempts),
				zap.Duration("proximaTentativaEm", delay),
				zap.Error(lastErr),
			)

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("contexto cancelado aguardando OIDC provider: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		provider, err := oidc.NewProvider(ctx, providerURL)
		if err == nil {
			log.Info("OIDC provider inicializado com sucesso",
				zap.String("issuerURL", providerURL),
				zap.Int("tentativas", attempt+1),
			)
			return provider, nil
		}

		lastErr = err
	}

	return nil, fmt.Errorf("OIDC provider indisponivel apos %d tentativas (issuer=%s): %w",
		oidcMaxAttempts, providerURL, lastErr)
}

// NewOIDCMiddleware cria uma nova instância do middleware.
//
// Utiliza retry com backoff exponencial para tolerar a janela de inicialização do Keycloak.
// Em produção, a URL do provider OIDC e ClientID vêm da configuração via Uber Fx.
func NewOIDCMiddleware(providerURL string, clientID string, log *zap.Logger) (*OIDCMiddleware, error) {
	ctx := context.Background()

	provider, err := newProviderWithRetry(ctx, providerURL, log)
	if err != nil {
		return nil, err
	}

	oidcConfig := &oidc.Config{
		ClientID: clientID,
	}
	verifier := provider.Verifier(oidcConfig)

	return &OIDCMiddleware{
		verifier: verifier,
		log:      log,
	}, nil
}

// Handle valida o token JWT e injeta o providerId no contexto.
func (m *OIDCMiddleware) Handle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]
		idToken, err := m.verifier.Verify(r.Context(), tokenString)
		if err != nil {
			m.log.Warn("Token verification failed", zap.Error(err))
			http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}

		// Extrair as claims do token (Keycloak usa 'clientId' ou 'azp' para client_credentials)
		var claims struct {
			ClientID string `json:"clientId"`
			Azp      string `json:"azp"`
		}
		if err := idToken.Claims(&claims); err != nil {
			http.Error(w, "Failed to parse token claims", http.StatusInternalServerError)
			return
		}

		providerID := claims.ClientID
		if providerID == "" {
			providerID = claims.Azp
		}

		if providerID == "" {
			http.Error(w, "Could not determine provider ID from token", http.StatusForbidden)
			return
		}

		// Injetar providerID no contexto
		ctx := context.WithValue(r.Context(), ProviderIDContextKey{}, providerID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetProviderID helper para extrair o providerId do contexto
func GetProviderID(ctx context.Context) (string, error) {
	providerID, ok := ctx.Value(ProviderIDContextKey{}).(string)
	if !ok || providerID == "" {
		return "", errors.New("providerID not found in context")
	}
	return providerID, nil
}
