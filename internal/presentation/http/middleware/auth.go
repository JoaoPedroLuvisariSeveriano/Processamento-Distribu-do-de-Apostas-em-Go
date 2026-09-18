package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

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

// NewOIDCMiddleware cria uma nova instância do middleware.
// Em produção, a URL do provider OIDC e ClientID vêm da configuração.
func NewOIDCMiddleware(providerURL string, clientID string, log *zap.Logger) (*OIDCMiddleware, error) {
	ctx := context.Background()
	provider, err := oidc.NewProvider(ctx, providerURL)
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
