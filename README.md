# Processamento Distribuído de Apostas em Go

Este repositório contém a solução completa para o Desafio Backend de Processamento Distribuído de Apostas. A aplicação foi construída em **Go 1.23** focada em resiliência, escalabilidade e altíssima segurança financeira.

##  Como Executar o Projeto

O projeto utiliza o **Docker Compose** para orquestrar todas as dependências locais (PostgreSQL, Keycloak e LocalStack para SQS). Não é necessário ter Go instalado na máquina host para subir a aplicação.

### 1. Subir a Infraestrutura e a Aplicação
Execute o comando abaixo na raiz do repositório:
```sh
docker compose up --build -d
```
> O Docker Compose fará o build da aplicação Go e inicializará o banco de dados (já rodando as *migrations* originais via container efêmero), o broker SQS simulado e o servidor Keycloak.

### 2. Rotas e Verificações de Saúde (Health Checks)
Com a aplicação no ar, os health checks estarão disponíveis em:
- **Liveness:** `curl http://localhost:3000/health/live` (Retorna `{"status": "ok"}`)
- **Readiness:** `curl http://localhost:3000/health/ready` (Garante conexão ativa com Banco e SQS)
- **Métricas:** `curl http://localhost:3000/metrics`

### 3. Autenticação (Keycloak)
A segurança é garantida via OAuth2 / OIDC Client Credentials. Para testar as rotas protegidas, solicite o token da aplicação no Keycloak:
```sh
curl -X POST http://localhost:8080/realms/betting-realm/protocol/openid-connect/token \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'client_id=backend-challenge' \
  -d 'client_secret=SEU_SECRET_AQUI' \
  -d 'grant_type=client_credentials'
```
*Observação: As credenciais default para inicialização estão no `.env` do projeto e nos scripts de bootstrap.*

---

##  Como Executar os Testes

Os testes garantem a integridade atômica da aplicação e a ausência de *Race Conditions*, validando concorrência agressiva de até 50 transações simultâneas na mesma carteira.

### 1. Suíte Completa de Testes
Para rodar a bateria de testes unitários e de integração dentro do container (sem precisar instalar dependências no host):
```sh
docker run --rm -v "${PWD}:/app" -w /app \
  --network backend-challenge-go_app-net \
  -e DATABASE_URL="postgres://postgres:123@betting_postgres:5432/betting_db?sslmode=disable" \
  -e OIDC_ISSUER_URL="http://betting_keycloak:8080/realms/betting-realm" \
  golang:1.23-alpine go test ./... -v
```

### 2. Testes de Concorrência Rigorosa (-race)
```sh
docker run --rm -v "${PWD}:/app" -w /app \
  --network backend-challenge-go_app-net \
  -e CGO_ENABLED=1 \
  -e DATABASE_URL="postgres://postgres:123@betting_postgres:5432/betting_db?sslmode=disable" \
  -e OIDC_ISSUER_URL="http://betting_keycloak:8080/realms/betting-realm" \
  golang:1.23-alpine go test -race ./...
```
*(Nota: Para `-race` rodar no Alpine pode requerer pacote build-base/gcc)*

### 3. Linter e Verificação Estática
```sh
docker run --rm -v "${PWD}:/app" -w /app golang:1.23-alpine go vet ./...
```

---

##  Documentação da Arquitetura
Consulte o arquivo [ARCHITECTURE.md](ARCHITECTURE.md) na raiz do repositório para mergulhar nas decisões de design (Domain-Driven Design), modelagem do tipo monetário blindado (`Money` object), garantias de Idempotência e estratégias de *Pessimistic Locking* de banco.

##  Estrutura de Diretórios
- `cmd/server/`: Ponto de entrada (Main) e injeção (Fx).
- `internal/application/`: Portas (interfaces) e Casos de Uso.
- `internal/domain/`: Aggregates, Value Objects (`Money`) e regras brutas de negócio.
- `internal/infrastructure/`: Adapters de banco (Pgx), mensageria (AWS SDK) e Workers de background.
- `internal/presentation/`: Handlers HTTP e Middlewares de validação (Zap/OIDC).
- `db/migrations/`: Versões de DDL do schema Postgres.
