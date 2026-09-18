package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	domainevent "github.com/joaoluvisari/backend-challenge-go/internal/domain/event"
	domainoutbox "github.com/joaoluvisari/backend-challenge-go/internal/domain/outbox"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// =============================================================================
// DTOs — Input / Output dos Use Cases
// =============================================================================

// OpenWalletInput contem os dados necessarios para criar uma nova carteira.
type OpenWalletInput struct {
	PlayerID       uuid.UUID
	Currency       string
	InitialBalance money.Money    // Pode ser zero; se positivo, gera OPENING + ledger entry
	CorrelationID  uuid.UUID      // Para rastreio de ponta a ponta nos eventos
}

// OpenWalletOutput e o resultado de OpenWalletUseCase.
type OpenWalletOutput struct {
	Wallet  *wallet.Wallet
	Created bool // false = carteira ja existia (idempotente)
}

// ProcessWagerInput contem os dados de uma operacao financeira externa.
// Preenchido pelo handler HTTP ou pelo consumidor SQS antes de chamar o use case.
type ProcessWagerInput struct {
	// Identidade da operacao
	ExternalTransactionID string
	ProviderID            string
	IdempotencyKey        string

	// Contexto de negocio
	PlayerID  uuid.UUID
	WalletID  uuid.UUID
	RoundID   string
	GameID    string
	Kind      transaction.Kind
	Amount    money.Money
	ReferenceExternalID *string // Obrigatorio para REFUND e ROLLBACK

	// Rastreio
	CorrelationID uuid.UUID
}

// ProcessWagerOutput e o resultado de ProcessWagerUseCase.
type ProcessWagerOutput struct {
	TransactionID uuid.UUID
	Status        transaction.Status
	// Balance e o saldo observado no momento do processamento.
	// nil se PENDING_REFERENCE (ainda nao sabemos o resultado).
	// Para replays, retorna o saldo persistido no momento original.
	Balance         *money.Money
	IdempotentReplay bool                 // true = replay de operacao ja processada
	FailureCode      *transaction.FailureCode
}

// =============================================================================
// Hash de Payload (Idempotencia)
// =============================================================================

// payloadHashFields define a estrutura CANONICA para calculo do hash de idempotencia.
//
// POR QUE HASH CANONICO?
// O hash identifica de forma univoca o CONTEUDO SEMANTICO de uma operacao.
// Se o mesmo Idempotency-Key chegar com conteudo diferente (conflito), detectamos
// via hash antes de tentar processar.
//
// CAMPOS CANONICOS (em ordem alfabetica pelo nome JSON):
// Excluimos campos de transporte (headers, timestamps) — eles mudam entre envios.
// Incluimos apenas o que define a OPERACAO em si: quem, o que, quanto, para onde.
//
// ORDEM ALFABETICA NO JSON:
// Go marshala structs na ordem de declaracao dos campos. Declaramos os campos
// com nomes JSON em ordem alfabetica para garantir saida deterministica.
type payloadHashFields struct {
	ExternalTransactionID          string        `json:"externalTransactionId"`
	GameID                         string        `json:"gameId"`
	Kind                           string        `json:"kind"`
	Money                          moneyHashJSON `json:"money"`
	PlayerID                       string        `json:"playerId"`
	ProviderID                     string        `json:"providerId"`
	ReferenceExternalTransactionID *string       `json:"referenceExternalTransactionId,omitempty"`
	RoundID                        string        `json:"roundId"`
	WalletID                       string        `json:"walletId"`
}

type moneyHashJSON struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// ComputePayloadHash calcula SHA-256 do JSON canonico dos campos de negocio.
// Retorna a representacao hexadecimal do hash (64 caracteres).
func ComputePayloadHash(input ProcessWagerInput) (string, error) {
	fields := payloadHashFields{
		ExternalTransactionID: input.ExternalTransactionID,
		GameID:                input.GameID,
		Kind:                  string(input.Kind),
		Money: moneyHashJSON{
			Amount:   input.Amount.String(),   // "25.00" — nunca float
			Currency: input.Amount.Currency(), // "BRL"
		},
		PlayerID:                       input.PlayerID.String(),
		ProviderID:                     input.ProviderID,
		ReferenceExternalTransactionID: input.ReferenceExternalID,
		RoundID:                        input.RoundID,
		WalletID:                       input.WalletID.String(),
	}

	data, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("serializar payload para hash: %w", err)
	}

	// SHA-256: 32 bytes, representados como 64 chars hex.
	// Resistente a colisoes e adequado para este uso (nao e para criptografia).
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// =============================================================================
// Helpers de Outbox
// =============================================================================

// newOutboxEvent serializa um evento de dominio e cria um OutboxEvent.
// Deve ser chamado DENTRO da transacao SQL para garantir atomicidade.
//
// O payload e o snapshot imutavel do evento — preservado para republica.
// Se o worker de outbox falhar e tiver que republicar, envia exatamente
// o mesmo payload (mesmo EventID para deduplicacao no consumidor).
func newOutboxEvent(aggregateID uuid.UUID, eventType string, payload interface{}, occurredAt time.Time) (*domainoutbox.OutboxEvent, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("serializar evento %s para outbox: %w", eventType, err)
	}
	event, err := domainoutbox.NewOutboxEvent(aggregateID, eventType, json.RawMessage(data), occurredAt)
	if err != nil {
		return nil, fmt.Errorf("criar outbox event %s: %w", eventType, err)
	}
	return event, nil
}

// moneyToPayload converte Money para o formato do payload de eventos.
// Usa string para amount — nunca float no payload de eventos.
func moneyToPayload(m money.Money) domainevent.MoneyPayload {
	return domainevent.MoneyPayload{
		Amount:   m.String(),
		Currency: m.Currency(),
	}
}

// buildResultBalance constroi o Money de resultado a partir dos centavos persistidos.
// Retorna nil se resultBalanceCents for nil (ex: PENDING_REFERENCE).
func buildResultBalance(resultBalanceCents *int64, currency string) *money.Money {
	if resultBalanceCents == nil {
		return nil
	}
	m := money.New(*resultBalanceCents, currency)
	return &m
}

// contextWithTimeout e um helper nao exportado para criar contextos com timeout.
// Centralizado aqui para consistencia entre os use cases.
var _ = context.Background // garante que context e importado
