-- =============================================================================
-- Migration: 000001_create_initial_schema.up.sql
-- Criacao do schema inicial do servico de apostas distribuido.
--
-- DECISOES DE DESIGN:
--   1. BIGINT para valores monetarios (centavos) — float proibido por spec.
--   2. CHECK (balance_cents >= 0) — segunda linha de defesa alem do dominio Go.
--   3. SELECT FOR UPDATE (pessimistic lock) — coordenacao entre instancias.
--   4. SKIP LOCKED no outbox worker — multiplos publishers sem contencao.
--   5. Ledger append-only via trigger — imutabilidade garantida no banco.
--   6. Partial UNIQUE indexes — idempotencia para transacoes externas sem
--      afetar registros internos (OPENING) que nao tem idempotency_key.
-- =============================================================================

-- ---------------------------------------------------------------------------
-- Extensao para geração de UUIDs (pgcrypto para uuid_generate_v4 ou nativo)
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ---------------------------------------------------------------------------
-- Tabela: wallets
-- Aggregate Root financeiro. Armazena saldo atual e versao para lock otimista.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wallets (
    id           UUID        NOT NULL,
    player_id    UUID        NOT NULL,
    currency     CHAR(3)     NOT NULL,

    -- balance_cents armazena o saldo em centavos (int64).
    -- CHECK garante que o saldo nunca seja negativo NO BANCO,
    -- mesmo que algum bug na aplicacao tente violar esta invariante.
    -- Esta e a defesa em profundidade: dominio + banco.
    balance_cents BIGINT     NOT NULL DEFAULT 0,

    -- version e incrementada a cada mudanca de saldo.
    -- Usada para deteccao de escrita concorrente (lost update prevention).
    -- Com SELECT FOR UPDATE (pessimistic lock), raramente sera necessaria,
    -- mas serve como segunda linha de defesa auditavel.
    version      BIGINT      NOT NULL DEFAULT 1,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_wallets PRIMARY KEY (id),

    -- O par (player_id, currency) identifica unicamente uma carteira.
    -- Tentativa de criar segunda carteira para o mesmo jogador/moeda retorna
    -- UniqueViolation (23505) que o repositorio mapeia para ErrWalletAlreadyExists.
    CONSTRAINT uq_wallets_player_currency UNIQUE (player_id, currency),

    -- Invariante financeira critica: saldo nao pode ser negativo.
    -- Esta constraint e a ultima barreira antes de corromper dados.
    CONSTRAINT chk_wallets_balance_non_negative CHECK (balance_cents >= 0),

    -- versao deve ser positiva
    CONSTRAINT chk_wallets_version_positive CHECK (version >= 1)
);

CREATE INDEX IF NOT EXISTS idx_wallets_player_id ON wallets (player_id);

-- ---------------------------------------------------------------------------
-- Tabela: wager_transactions
-- Registra todas as operacoes financeiras (BET, WIN, LOSS, REFUND, ROLLBACK, OPENING).
-- E o ponto central de idempotencia: operacoes repetidas consultam esta tabela.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wager_transactions (
    id           UUID        NOT NULL,

    -- Campos de identidade externa (nulos para OPENING — operacao interna).
    -- external_id: ID fornecido pelo provider para esta operacao.
    -- provider_id: ID do provider autenticado via JWT do Keycloak.
    external_id  TEXT,
    provider_id  TEXT,

    -- idempotency_key: valor do header Idempotency-Key recebido pelo servidor.
    -- Formato esperado: "{providerId}:{externalTransactionId}"
    idempotency_key TEXT,

    -- payload_hash: SHA-256 do JSON canonico dos campos de negocio.
    -- Usado para detectar conflito de idempotencia (mesma key, payload diferente).
    payload_hash TEXT,

    -- Contexto de negocio
    wallet_id    UUID        NOT NULL,
    player_id    UUID        NOT NULL,
    round_id     TEXT,
    game_id      TEXT,

    -- kind: tipo da operacao (BET, WIN, LOSS, REFUND, ROLLBACK, OPENING)
    kind         TEXT        NOT NULL,

    -- amount_cents: valor em centavos. Para LOSS, deve ser 0.
    amount_cents BIGINT      NOT NULL DEFAULT 0,
    currency     CHAR(3)     NOT NULL,

    -- Referencia para REFUND e ROLLBACK
    reference_external_id    TEXT,
    reference_transaction_id UUID REFERENCES wager_transactions(id) DEFERRABLE INITIALLY DEFERRED,

    -- Maquina de estados: PENDING -> PENDING_REFERENCE | PROCESSED | REJECTED | FAILED
    status       TEXT        NOT NULL DEFAULT 'PENDING',

    -- failure_code: codigo estavel da rejeicao (ex: INSUFFICIENT_BALANCE).
    -- Preenchido apenas em REJECTED e FAILED.
    failure_code TEXT,

    -- Controle de retry para PENDING_REFERENCE
    attempts     INT         NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ,

    -- result_balance_cents: saldo no momento do processamento.
    -- Armazenado para que replays retornem o saldo ORIGINAL da operacao,
    -- mesmo que o saldo atual da carteira seja diferente.
    result_balance_cents BIGINT,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,

    CONSTRAINT pk_wager_transactions PRIMARY KEY (id),

    CONSTRAINT fk_wager_transactions_wallet
        FOREIGN KEY (wallet_id) REFERENCES wallets(id),

    -- Invariante: amount nao pode ser negativo.
    CONSTRAINT chk_wager_transactions_amount_non_negative
        CHECK (amount_cents >= 0),

    -- Validacao do status contra o conjunto de valores permitidos.
    CONSTRAINT chk_wager_transactions_status
        CHECK (status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),

    -- Validacao do kind contra o conjunto de valores permitidos.
    CONSTRAINT chk_wager_transactions_kind
        CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK'))
);

-- Partial UNIQUE index para idempotency_key (NULL para OPENING — nao afetado).
-- Partial indexes ignoram linhas onde a condicao WHERE e falsa.
-- Isso permite multiplos NULLs sem violar unicidade.
CREATE UNIQUE INDEX IF NOT EXISTS uq_wager_transactions_idempotency_key
    ON wager_transactions (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Partial UNIQUE index para (provider_id, external_id).
-- Previne que o mesmo provider envie a mesma external_id duas vezes com
-- idempotency_keys diferentes (tentativa de contornar idempotencia).
CREATE UNIQUE INDEX IF NOT EXISTS uq_wager_transactions_provider_external
    ON wager_transactions (provider_id, external_id)
    WHERE provider_id IS NOT NULL AND external_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_wager_transactions_wallet_id
    ON wager_transactions (wallet_id);
CREATE INDEX IF NOT EXISTS idx_wager_transactions_status_retry
    ON wager_transactions (status, next_retry_at)
    WHERE status = 'PENDING_REFERENCE';

-- ---------------------------------------------------------------------------
-- Tabela: wallet_ledger_entries
-- Razao contabil append-only. Cada movimentacao financeira gera um lancamento.
-- LOSS nao gera lancamento (sem movimentacao de saldo).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wallet_ledger_entries (
    id             UUID        NOT NULL,
    wallet_id      UUID        NOT NULL,
    transaction_id UUID        NOT NULL,

    -- direction: CREDIT (entrada) ou DEBIT (saida)
    direction      TEXT        NOT NULL,

    -- Todos os valores em centavos (BIGINT)
    amount_cents        BIGINT NOT NULL,
    balance_before_cents BIGINT NOT NULL,
    balance_after_cents  BIGINT NOT NULL,

    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_wallet_ledger_entries PRIMARY KEY (id),

    CONSTRAINT fk_ledger_wallet
        FOREIGN KEY (wallet_id) REFERENCES wallets(id),
    CONSTRAINT fk_ledger_transaction
        FOREIGN KEY (transaction_id) REFERENCES wager_transactions(id),

    -- Unicidade: uma transacao so pode gerar UM lancamento por carteira.
    -- Previne duplicatas mesmo que o application layer tente inserir duas vezes.
    CONSTRAINT uq_ledger_wallet_transaction
        UNIQUE (wallet_id, transaction_id),

    -- Constraints de dominio impostas no banco como defesa em profundidade:
    CONSTRAINT chk_ledger_direction
        CHECK (direction IN ('CREDIT', 'DEBIT')),
    CONSTRAINT chk_ledger_amount_positive
        CHECK (amount_cents > 0),
    CONSTRAINT chk_ledger_balance_before_non_negative
        CHECK (balance_before_cents >= 0),
    CONSTRAINT chk_ledger_balance_after_non_negative
        CHECK (balance_after_cents >= 0)
);

CREATE INDEX IF NOT EXISTS idx_ledger_wallet_id
    ON wallet_ledger_entries (wallet_id, created_at);

-- Trigger para tornar o ledger VERDADEIRAMENTE append-only no banco.
-- Impede UPDATE e DELETE mesmo que um bug na aplicacao tente executar.
-- Esta e a garantia mais forte de imutabilidade — nivel de banco de dados.
CREATE OR REPLACE FUNCTION prevent_ledger_modification()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION
        'wallet_ledger_entries e append-only: UPDATE e DELETE sao proibidos. '
        'Codigo de erro: ledger_immutable';
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_ledger_update
    BEFORE UPDATE ON wallet_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION prevent_ledger_modification();

CREATE TRIGGER trg_prevent_ledger_delete
    BEFORE DELETE ON wallet_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION prevent_ledger_modification();

-- ---------------------------------------------------------------------------
-- Tabela: outbox_events
-- Implementacao do Transactional Outbox Pattern.
-- Eventos sao inseridos NA MESMA TRANSACAO que modifica o dominio.
-- Um worker separado le e publica no SQS com SELECT FOR UPDATE SKIP LOCKED.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS outbox_events (
    id              UUID        NOT NULL,

    -- aggregate_id: ID da entidade principal do evento (ex: wallet_id).
    aggregate_id    UUID        NOT NULL,

    -- event_type: discriminador do tipo (ex: "WalletBalanceChanged").
    event_type      TEXT        NOT NULL,

    -- payload: snapshot imutavel do evento em JSON.
    -- Imutavel pois representa um fato que JA ACONTECEU.
    payload         JSONB       NOT NULL,

    occurred_at     TIMESTAMPTZ NOT NULL,

    -- Controle de retry com backoff exponencial
    attempts        INT         NOT NULL DEFAULT 0,
    max_attempts    INT         NOT NULL DEFAULT 5,

    -- next_delivery_at: proximo instante em que o worker deve tentar publicar.
    -- Aumenta exponencialmente a cada falha (1s, 2s, 4s, 8s...).
    next_delivery_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- published_at: preenchido quando o evento for publicado com sucesso.
    -- NULL = pendente; NOT NULL = publicado.
    published_at    TIMESTAMPTZ,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_outbox_events PRIMARY KEY (id)
);

-- Indice para o worker de outbox: buscar eventos pendentes ordenados por entrega.
-- published_at IS NULL = ainda nao publicado.
-- next_delivery_at <= NOW() = pronto para entrega (sem backoff ativo).
CREATE INDEX IF NOT EXISTS idx_outbox_pending
    ON outbox_events (next_delivery_at)
    WHERE published_at IS NULL;

-- ---------------------------------------------------------------------------
-- Tabela: inbox_messages
-- Implementacao do Inbox Pattern para mensagens SQS.
-- Garante exatamente-uma-vez no processamento de mensagens SQS (at-least-once).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS inbox_messages (
    id            UUID        NOT NULL,

    -- consumer_name: identificador do consumidor (ex: "sqs-wager-consumer").
    -- Permite que diferentes consumidores reprocessem a mesma mensagem.
    consumer_name TEXT        NOT NULL,

    -- message_id: ID unico da mensagem no SQS (do envelope da mensagem).
    message_id    TEXT        NOT NULL,

    -- payload_hash: hash do corpo da mensagem para detectar reenvios com
    -- conteudo diferente (potencial corrupcao ou erro do producer).
    payload_hash  TEXT        NOT NULL,

    received_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- processed_at: preenchido quando o processamento e concluido com sucesso.
    -- Inserido NA MESMA TRANSACAO que as alteracoes de dominio.
    processed_at  TIMESTAMPTZ,

    CONSTRAINT pk_inbox_messages PRIMARY KEY (id),

    -- Unicidade por (consumer_name, message_id): o mesmo consumidor nao processa
    -- a mesma mensagem duas vezes. Violacao desta constraint = replay detectado.
    CONSTRAINT uq_inbox_consumer_message
        UNIQUE (consumer_name, message_id)
);

CREATE INDEX IF NOT EXISTS idx_inbox_message_id
    ON inbox_messages (consumer_name, message_id);
