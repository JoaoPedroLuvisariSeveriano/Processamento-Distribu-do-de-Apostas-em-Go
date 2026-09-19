#!/usr/bin/env bash
# =============================================================================
# scripts/migrate.sh
# Script auxiliar para operacoes de migration no desenvolvimento local.
#
# Usa a mesma imagem migrate/migrate:v4.18.1 do docker-compose.yml,
# garantindo identidade total com o ambiente de CI/CD.
#
# USO:
#   ./scripts/migrate.sh up              # aplica todas as migrations pendentes
#   ./scripts/migrate.sh down            # reverte a ultima migration
#   ./scripts/migrate.sh version         # exibe a versao atual do schema
#   ./scripts/migrate.sh force VERSION   # forcca versao (ex: apos erro dirty)
#   ./scripts/migrate.sh drop            # CUIDADO: apaga tudo (apenas dev)
#
# PREREQUISITOS:
#   - Docker rodando
#   - PostgreSQL acessivel em DATABASE_URL (ou no padrao localhost:5432)
#
# VARIAVEL DE AMBIENTE:
#   DATABASE_URL  URL de conexao ao PostgreSQL.
#                 Padrao: postgres://postgres:123@localhost:5432/betting_db?sslmode=disable
# =============================================================================
set -euo pipefail

# Diretorio de migrations relativo a raiz do projeto
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MIGRATIONS_DIR="$(cd "${SCRIPT_DIR}/../db/migrations" && pwd)"

# URL do banco: usa variavel de ambiente ou fallback para o padrao local
DB_URL="${DATABASE_URL:-postgres://postgres:123@localhost:5432/betting_db?sslmode=disable}"

if [[ $# -lt 1 ]]; then
  echo "Uso: $0 <comando> [args]"
  echo "Comandos: up | down | version | force VERSION | drop"
  exit 1
fi

echo ">>> migrate/migrate:v4.18.1 | db: ${DB_URL%%@*}@***"
echo ">>> migrations: ${MIGRATIONS_DIR}"
echo ">>> comando: $*"
echo ""

docker run --rm \
  --network host \
  -v "${MIGRATIONS_DIR}:/migrations:ro" \
  migrate/migrate:v4.18.1 \
  -path=/migrations \
  -database="${DB_URL}" \
  "$@"

echo ""
echo ">>> Concluido."
