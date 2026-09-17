# =============================================================================
# Dockerfile multi-stage para o servico de apostas
# =============================================================================
# Stage 1 (builder): compila o binario Go com todas as dependencias.
# Stage 2 (runtime): imagem minima (distroless) apenas com o binario.
#
# A separacao em stages garante que o container de producao nao contenha
# ferramentas de build (gcc, go toolchain), reduzindo a superficie de ataque.
# =============================================================================

# --- Stage 1: Builder ---
FROM golang:1.23-alpine AS builder

# Instalar dependencias necessarias para CGO (pgx usa driver nativo)
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copiar go.mod e go.sum primeiro para aproveitar o cache de layers do Docker.
# Se apenas o codigo mudar (sem novas dependencias), esta layer nao e rebuild.
COPY go.mod go.sum ./
RUN go mod download

# Copiar o codigo fonte e compilar
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o /bin/betting-service \
    ./cmd/server

# --- Stage 2: Runtime ---
FROM gcr.io/distroless/static-debian12 AS runtime

WORKDIR /app

# Copiar apenas o binario compilado (sem toolchain Go)
COPY --from=builder /bin/betting-service /app/betting-service

# Porta HTTP do servico
EXPOSE 3000

# Executar como usuario nao-root (seguranca)
USER nonroot:nonroot

ENTRYPOINT ["/app/betting-service"]
