package transaction

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
)

// WagerTransaction representa uma operacao financeira de aposta.
//
// Esta entidade tem um ciclo de vida controlado pela maquina de estados
// definida em status.go. Toda transicao de estado e validada antes de
// ser aplicada — uma transacao em estado terminal nao pode ser alterada.
//
// A transacao serve como ponto central de idempotencia: se uma mesma
// operacao chegar multiplas vezes (HTTP ou SQS), o sistema consulta o
// estado persistido desta entidade e retorna o resultado original.
//
// Campos privados garantem que apenas os metodos desta entidade
// possam transicionar estados ou alterar dados.
type WagerTransaction struct {
	// --- Identidade ---
	id         uuid.UUID
	externalID string // ID fornecido pelo provider
	providerID string // ID do provider autenticado

	// --- Idempotencia ---
	idempotencyKey string // header Idempotency-Key recebido
	payloadHash    string // SHA-256 do JSON canonico dos campos de negocio

	// --- Contexto de negocio ---
	walletID uuid.UUID
	playerID uuid.UUID
	roundID  string
	gameID   string
	kind     Kind
	amount   money.Money

	// --- Referencia (para REFUND e ROLLBACK) ---
	referenceExternalID       *string    // ID externo da transacao referenciada
	referenceTransactionID    *uuid.UUID // ID interno resolvido (nil ate resolucao)

	// --- Estado ---
	status      Status
	failureCode *FailureCode // preenchido em REJECTED ou FAILED
	attempts    int          // numero de tentativas de processamento (para PENDING_REFERENCE)
	nextRetryAt *time.Time   // proximo instante de retry (backoff exponencial)

	// --- Resultado persistido (para replay) ---
	// Armazena o saldo observado NO MOMENTO do processamento.
	// Replays devem retornar este valor, mesmo que o saldo atual seja diferente.
	resultBalanceCents *int64

	// --- Timestamps ---
	createdAt   time.Time
	updatedAt   time.Time
	processedAt *time.Time
}

// NewWagerTransaction cria uma nova transacao em estado PENDING.
// Este e o ponto de entrada para todas as operacoes externas (HTTP/SQS).
//
// Validacoes aplicadas:
//  1. Kind nao pode ser OPENING (uso exclusivo interno)
//  2. Campos obrigatorios nao podem ser vazios
//  3. Para REFUND e ROLLBACK, referenceExternalID e obrigatorio
//  4. Para LOSS, amount deve ser zero
//  5. Para outros tipos, amount deve ser positivo
func NewWagerTransaction(
	externalID string,
	providerID string,
	idempotencyKey string,
	payloadHash string,
	walletID uuid.UUID,
	playerID uuid.UUID,
	roundID string,
	gameID string,
	kind Kind,
	amount money.Money,
	referenceExternalID *string,
) (*WagerTransaction, error) {
	// Regra: OPENING so pode ser criado internamente (via abertura de carteira).
	if kind == KindOpening {
		return nil, fmt.Errorf("%w: OPENING nao pode ser enviado por providers externos",
			domain.ErrInvalidOperationType)
	}
	if !kind.IsValid() {
		return nil, fmt.Errorf("%w: tipo desconhecido: %q",
			domain.ErrInvalidOperationType, kind)
	}

	// Validar campos de identidade nao-vazios
	if externalID == "" || providerID == "" || idempotencyKey == "" || payloadHash == "" {
		return nil, fmt.Errorf("campos de identidade nao podem ser vazios")
	}

	// Validar referencia obrigatoria para REFUND e ROLLBACK
	if kind.RequiresReference() && (referenceExternalID == nil || *referenceExternalID == "") {
		return nil, fmt.Errorf("referenceExternalTransactionId e obrigatorio para %s", kind)
	}

	// Validar amount conforme tipo:
	//   - LOSS: exige amount = 0
	//   - Demais tipos externos: exige amount > 0
	if kind == KindLoss && !amount.IsZero() {
		return nil, fmt.Errorf("LOSS exige amount = 0.00, recebeu %s", amount.String())
	}
	if kind != KindLoss && !amount.IsPositive() {
		return nil, fmt.Errorf("%s exige valor positivo, recebeu %s", kind, amount.String())
	}

	now := time.Now().UTC()
	return &WagerTransaction{
		id:                  uuid.New(),
		externalID:          externalID,
		providerID:          providerID,
		idempotencyKey:      idempotencyKey,
		payloadHash:         payloadHash,
		walletID:            walletID,
		playerID:            playerID,
		roundID:             roundID,
		gameID:              gameID,
		kind:                kind,
		amount:              amount,
		referenceExternalID: referenceExternalID,
		// Toda transacao comeca em PENDING
		status:    StatusPending,
		attempts:  0,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// NewOpeningTransaction cria uma transacao OPENING para abertura de carteira.
// Esta funcao e de USO EXCLUSIVO INTERNO — nao deve ser exposta via HTTP/SQS.
// OPENING tem um conjunto diferente de campos (sem provider, external ID, etc.)
func NewOpeningTransaction(
	walletID uuid.UUID,
	playerID uuid.UUID,
	amount money.Money,
) *WagerTransaction {
	now := time.Now().UTC()
	return &WagerTransaction{
		id:        uuid.New(),
		walletID:  walletID,
		playerID:  playerID,
		kind:      KindOpening,
		amount:    amount,
		status:    StatusPending,
		createdAt: now,
		updatedAt: now,
	}
}

// RehydrateTransaction reconstroi uma WagerTransaction a partir do banco.
// NAO reaplica logica de negocio — apenas restaura o estado persistido.
func RehydrateTransaction(
	id uuid.UUID,
	externalID, providerID, idempotencyKey, payloadHash string,
	walletID, playerID uuid.UUID,
	roundID, gameID string,
	kind Kind,
	amountCents int64, currency string,
	referenceExternalID *string,
	referenceTransactionID *uuid.UUID,
	status Status,
	failureCode *FailureCode,
	attempts int,
	nextRetryAt *time.Time,
	resultBalanceCents *int64,
	createdAt, updatedAt time.Time,
	processedAt *time.Time,
) *WagerTransaction {
	return &WagerTransaction{
		id:                     id,
		externalID:             externalID,
		providerID:             providerID,
		idempotencyKey:         idempotencyKey,
		payloadHash:            payloadHash,
		walletID:               walletID,
		playerID:               playerID,
		roundID:                roundID,
		gameID:                 gameID,
		kind:                   kind,
		amount:                 money.New(amountCents, currency),
		referenceExternalID:    referenceExternalID,
		referenceTransactionID: referenceTransactionID,
		status:                 status,
		failureCode:            failureCode,
		attempts:               attempts,
		nextRetryAt:            nextRetryAt,
		resultBalanceCents:     resultBalanceCents,
		createdAt:              createdAt,
		updatedAt:              updatedAt,
		processedAt:            processedAt,
	}
}

// =============================================================================
// Transicoes de Estado
// =============================================================================

// MarkAsProcessed transiciona a transacao para PROCESSED e persiste o saldo observado.
// O saldo observado e fundamental para replay: replays devem retornar este valor
// mesmo que o saldo atual da carteira seja diferente.
func (t *WagerTransaction) MarkAsProcessed(resultBalanceCents int64) error {
	if err := ValidateTransition(t.status, StatusProcessed); err != nil {
		return err
	}
	now := time.Now().UTC()
	t.status = StatusProcessed
	t.resultBalanceCents = &resultBalanceCents
	t.processedAt = &now
	t.updatedAt = now
	return nil
}

// MarkAsRejected transiciona para REJECTED com um codigo de falha estavel.
// REJECTED e um estado terminal — indica rejeicao definitiva por regra de negocio.
func (t *WagerTransaction) MarkAsRejected(code FailureCode) error {
	if err := ValidateTransition(t.status, StatusRejected); err != nil {
		return err
	}
	now := time.Now().UTC()
	t.status = StatusRejected
	t.failureCode = &code
	t.updatedAt = now
	return nil
}

// MarkAsFailed transiciona para FAILED com um codigo de falha.
// FAILED e para falhas permanentes de infraestrutura (nao regras de negocio).
func (t *WagerTransaction) MarkAsFailed(code FailureCode) error {
	if err := ValidateTransition(t.status, StatusFailed); err != nil {
		return err
	}
	now := time.Now().UTC()
	t.status = StatusFailed
	t.failureCode = &code
	t.updatedAt = now
	return nil
}

// MarkAsPendingReference transiciona para PENDING_REFERENCE.
// Usado quando REFUND/ROLLBACK chega antes da transacao referenciada.
// O worker de referencias tentara resolver com backoff exponencial.
func (t *WagerTransaction) MarkAsPendingReference(nextRetryAt time.Time) error {
	if err := ValidateTransition(t.status, StatusPendingReference); err != nil {
		return err
	}
	t.status = StatusPendingReference
	t.attempts++
	t.nextRetryAt = &nextRetryAt
	t.updatedAt = time.Now().UTC()
	return nil
}

// IncrementAttempt registra uma nova tentativa de resolucao de referencia pendente.
// Chamado pelo worker de referencias a cada ciclo de retry.
func (t *WagerTransaction) IncrementAttempt(nextRetryAt time.Time) error {
	if t.status != StatusPendingReference {
		return fmt.Errorf("%w: IncrementAttempt requer status PENDING_REFERENCE, tem %s",
			domain.ErrInvalidTransitionState, t.status)
	}
	t.attempts++
	t.nextRetryAt = &nextRetryAt
	t.updatedAt = time.Now().UTC()
	return nil
}

// ResolveReference define a referencia interna resolvida (ID interno da transacao original).
// Chamado quando a transacao referenciada e encontrada.
func (t *WagerTransaction) ResolveReference(internalRefID uuid.UUID) {
	t.referenceTransactionID = &internalRefID
	t.updatedAt = time.Now().UTC()
}

// =============================================================================
// Getters
// =============================================================================

func (t *WagerTransaction) ID() uuid.UUID               { return t.id }
func (t *WagerTransaction) ExternalID() string           { return t.externalID }
func (t *WagerTransaction) ProviderID() string           { return t.providerID }
func (t *WagerTransaction) IdempotencyKey() string       { return t.idempotencyKey }
func (t *WagerTransaction) PayloadHash() string          { return t.payloadHash }
func (t *WagerTransaction) WalletID() uuid.UUID          { return t.walletID }
func (t *WagerTransaction) PlayerID() uuid.UUID          { return t.playerID }
func (t *WagerTransaction) RoundID() string              { return t.roundID }
func (t *WagerTransaction) GameID() string               { return t.gameID }
func (t *WagerTransaction) Kind() Kind                   { return t.kind }
func (t *WagerTransaction) Amount() money.Money          { return t.amount }
func (t *WagerTransaction) Status() Status               { return t.status }
func (t *WagerTransaction) FailureCode() *FailureCode    { return t.failureCode }
func (t *WagerTransaction) Attempts() int                { return t.attempts }
func (t *WagerTransaction) NextRetryAt() *time.Time      { return t.nextRetryAt }
func (t *WagerTransaction) ResultBalanceCents() *int64   { return t.resultBalanceCents }
func (t *WagerTransaction) CreatedAt() time.Time         { return t.createdAt }
func (t *WagerTransaction) UpdatedAt() time.Time         { return t.updatedAt }
func (t *WagerTransaction) ProcessedAt() *time.Time      { return t.processedAt }
func (t *WagerTransaction) ReferenceExternalID() *string { return t.referenceExternalID }
func (t *WagerTransaction) ReferenceTransactionID() *uuid.UUID {
	return t.referenceTransactionID
}
