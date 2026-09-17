# ARCHITECTURE.md - Decisoes Tecnicas

## 1. Money (Dinheiro)

Representacao interna: int64 em centavos.
float32/float64 sao proibidos (erros de precisao IEEE 754).
Parsing via shopspring/decimal apenas no boundary (entrada/saida).
Persistencia: coluna BIGINT (centavos) + CHAR(3) (moeda ISO 4217).

## 2. Controle de Concorrencia

Lock pessimista com SELECT ... FOR UPDATE por carteira.
Bloqueia apenas a linha especifica; carteiras distintas rodam em paralelo.
Lost updates prevenidos por FOR UPDATE + CHECK CONSTRAINT balance >= 0.

## 3. Idempotencia

Persistente via idempotency_key UNIQUE em wager_transactions.
Hash SHA-256 sobre JSON canonico dos campos de negocio.
Replay com hash identico: retorna resultado persistido.
Replay com hash diferente: retorna 409 Conflict.

## 4. Ledger

Append-only sem UPDATE ou DELETE.
LOSS nao gera ledger (sem movimentacao de saldo).
Constraint UNIQUE (wallet_id, transaction_id) previne duplicatas.

## 5. Transactional Outbox e Inbox

Outbox: eventos inseridos na mesma transacao SQL que altera saldo.
Worker usa SELECT FOR UPDATE SKIP LOCKED para multiplos publishers.
Inbox: inbox_messages com UNIQUE (consumer_name, message_id).
Inbox inserido na mesma transacao SQL das alteracoes de dominio.

## 6. Autenticacao

Keycloak com client_credentials (OAuth 2.0).
JWT validado via JWKS publico do Keycloak (go-oidc/v3).
clientId do token determina o providerId autorizado.

## 7. Uber Fx

Cada camada e um fx.Module independente.
Dominio nao importa Fx, HTTP, SQS ou pgx.
Lifecycle.OnStart/OnStop gerencia shutdown graceful.

## 8. Limitacoes

Apenas BRL nos cenarios principais (tipo Money carrega moeda).
Rollback parcial nao implementado (fora do escopo do desafio).
Partidas dobradas (double-entry) nao implementadas.
