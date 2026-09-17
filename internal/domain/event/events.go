// Package event define todos os eventos de dominio publicados via Outbox.
//
// Eventos de dominio representam fatos que ACONTECERAM no sistema.
// Eles sao imutaveis e servem como base para o Transactional Outbox Pattern:
// o evento e inserido na tabela outbox_events NA MESMA TRANSACAO SQL que
// altera o estado de dominio, garantindo que nenhum evento seja perdido.
//
// Estrutura de envelope:
//   - EventID:       UUID unico e estavel (preservado em republicas pelo outbox worker)
//   - EventType:     string discriminadora do tipo de evento
//   - AggregateID:   ID da entidade principal (ex: walletId para WalletBalanceChanged)
//   - CorrelationID: ID para rastrear a requisicao original de ponta a ponta
//   - CausationID:   ID do evento que causou este (opcional, para cadeias de eventos)
//   - OccurredAt:    instante do evento em UTC RFC 3339
//   - Version:       versao do schema do payload (para evolucao sem quebra)
//   - Data:          payload tipado e especifico por tipo de evento
package event

import (
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Envelope base
// =============================================================================

// Base contem os campos comuns a todos os eventos de dominio.
// Embarcamos Base em cada evento concreto para evitar repeticao.
type Base struct {
	// EventID e o identificador unico e ESTAVEL do evento.
	// O outbox worker preserva este ID ao republicar — o consumidor
	// pode usar EventID para deduplicacao no lado receptor.
	EventID uuid.UUID `json:"eventId"`

	// EventType e o discriminador do tipo de evento (ex: "WalletBalanceChanged").
	// Definido pelo construtor de cada evento — nao editavel.
	EventType string `json:"eventType"`

	// AggregateID e o ID da entidade raiz do agregado (ex: walletId).
	AggregateID uuid.UUID `json:"aggregateId"`

	// CorrelationID rastreia a requisicao original de ponta a ponta.
	// Propagado do header X-Correlation-ID ou gerado na borda.
	CorrelationID uuid.UUID `json:"correlationId"`

	// CausationID e o ID do evento que causou este (opcional).
	// Util para reconstruir cadeias causais de eventos.
	CausationID *uuid.UUID `json:"causationId,omitempty"`

	// OccurredAt e o instante em que o evento ocorreu (UTC, RFC 3339).
	// Nunca usar time.Now() aqui — deve ser o instante do commit SQL.
	OccurredAt time.Time `json:"occurredAt"`

	// Version e a versao do schema do payload deste evento.
	// Permite evolucao backward-compatible sem quebrar consumidores.
	Version int `json:"version"`
}

// MoneyPayload e a representacao JSON de Money nos payloads de eventos.
// Usamos strings para valores monetarios — nunca float.
type MoneyPayload struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// =============================================================================
// WagerTransactionProcessed
// =============================================================================

// WagerTransactionProcessed e publicado quando uma transacao e concluida com sucesso.
// Inclui tambem operacoes do tipo LOSS (sem movimentacao de saldo, mas concluidas).
const EventTypeWagerTransactionProcessed = "WagerTransactionProcessed"

// WagerTransactionProcessed e o evento de conclusao bem-sucedida.
type WagerTransactionProcessed struct {
	Base
	Data WagerTransactionProcessedData `json:"data"`
}

// WagerTransactionProcessedData e o payload do evento WagerTransactionProcessed.
type WagerTransactionProcessedData struct {
	TransactionID         uuid.UUID    `json:"transactionId"`
	ExternalTransactionID string       `json:"externalTransactionId"`
	ProviderID            string       `json:"providerId"`
	WalletID              uuid.UUID    `json:"walletId"`
	PlayerID              uuid.UUID    `json:"playerId"`
	RoundID               string       `json:"roundId"`
	GameID                string       `json:"gameId"`
	Kind                  string       `json:"kind"`
	Money                 MoneyPayload `json:"money"`
}

// NewWagerTransactionProcessed cria o evento de conclusao.
// O EventType e definido pelo construtor — nunca editavel pos-criacao.
func NewWagerTransactionProcessed(
	aggregateID uuid.UUID,
	correlationID uuid.UUID,
	data WagerTransactionProcessedData,
) *WagerTransactionProcessed {
	return &WagerTransactionProcessed{
		Base: Base{
			EventID:       uuid.New(),
			EventType:     EventTypeWagerTransactionProcessed,
			AggregateID:   aggregateID,
			CorrelationID: correlationID,
			OccurredAt:    time.Now().UTC(),
			Version:       1,
		},
		Data: data,
	}
}

// =============================================================================
// WagerTransactionRejected
// =============================================================================

const EventTypeWagerTransactionRejected = "WagerTransactionRejected"

// WagerTransactionRejected e publicado quando uma transacao e recusada definitivamente.
type WagerTransactionRejected struct {
	Base
	Data WagerTransactionRejectedData `json:"data"`
}

// WagerTransactionRejectedData e o payload do evento de rejeicao.
type WagerTransactionRejectedData struct {
	TransactionID         uuid.UUID    `json:"transactionId"`
	ExternalTransactionID string       `json:"externalTransactionId"`
	ProviderID            string       `json:"providerId"`
	WalletID              uuid.UUID    `json:"walletId"`
	PlayerID              uuid.UUID    `json:"playerId"`
	Kind                  string       `json:"kind"`
	Money                 MoneyPayload `json:"money"`
	// FailureCode e o codigo estavel e documentado da rejeicao.
	// Permite que consumers programem racionalmente contra erros de negocio.
	FailureCode string `json:"failureCode"`
}

func NewWagerTransactionRejected(
	aggregateID uuid.UUID,
	correlationID uuid.UUID,
	data WagerTransactionRejectedData,
) *WagerTransactionRejected {
	return &WagerTransactionRejected{
		Base: Base{
			EventID:       uuid.New(),
			EventType:     EventTypeWagerTransactionRejected,
			AggregateID:   aggregateID,
			CorrelationID: correlationID,
			OccurredAt:    time.Now().UTC(),
			Version:       1,
		},
		Data: data,
	}
}

// =============================================================================
// WalletBalanceChanged
// =============================================================================

const EventTypeWalletBalanceChanged = "WalletBalanceChanged"

// WalletBalanceChanged e publicado quando o saldo de uma carteira muda efetivamente.
// NAO e publicado para LOSS (sem movimentacao de saldo).
type WalletBalanceChanged struct {
	Base
	Data WalletBalanceChangedData `json:"data"`
}

// WalletBalanceChangedData e o payload detalhado da mudanca de saldo.
// Inclui o saldo antes e depois para permitir reconciliacao pelos consumidores.
type WalletBalanceChangedData struct {
	WalletID      uuid.UUID    `json:"walletId"`
	TransactionID uuid.UUID    `json:"transactionId"`
	// Direction: "CREDIT" ou "DEBIT"
	Direction     string       `json:"direction"`
	Money         MoneyPayload `json:"money"`
	BalanceBefore MoneyPayload `json:"balanceBefore"`
	BalanceAfter  MoneyPayload `json:"balanceAfter"`
	// WalletVersion e a versao da carteira APOS esta operacao.
	// Permite detectar atualizacoes perdidas ou fora de ordem.
	WalletVersion int64 `json:"walletVersion"`
}

func NewWalletBalanceChanged(
	walletID uuid.UUID,
	correlationID uuid.UUID,
	data WalletBalanceChangedData,
) *WalletBalanceChanged {
	return &WalletBalanceChanged{
		Base: Base{
			EventID:       uuid.New(),
			EventType:     EventTypeWalletBalanceChanged,
			AggregateID:   walletID,
			CorrelationID: correlationID,
			OccurredAt:    time.Now().UTC(),
			Version:       1,
		},
		Data: data,
	}
}

// =============================================================================
// WagerTransactionPendingReference
// =============================================================================

const EventTypeWagerTransactionPendingReference = "WagerTransactionPendingReference"

// WagerTransactionPendingReference e publicado quando uma operacao fica em espera
// por uma referencia ainda nao disponivel (ex: REFUND antes da BET correspondente).
type WagerTransactionPendingReference struct {
	Base
	Data WagerTransactionPendingReferenceData `json:"data"`
}

// WagerTransactionPendingReferenceData e o payload do evento de pendencia.
type WagerTransactionPendingReferenceData struct {
	TransactionID               uuid.UUID `json:"transactionId"`
	ExternalTransactionID       string    `json:"externalTransactionId"`
	ProviderID                  string    `json:"providerId"`
	WalletID                    uuid.UUID `json:"walletId"`
	Kind                        string    `json:"kind"`
	// ReferenceExternalTransactionID e o ID da transacao aguardada.
	ReferenceExternalTransactionID string `json:"referenceExternalTransactionId"`
	// Attempts e o numero de tentativas ja realizadas.
	Attempts int `json:"attempts"`
	// NextRetryAt e o proximo instante de retry (UTC, RFC 3339).
	NextRetryAt time.Time `json:"nextRetryAt"`
}

func NewWagerTransactionPendingReference(
	aggregateID uuid.UUID,
	correlationID uuid.UUID,
	data WagerTransactionPendingReferenceData,
) *WagerTransactionPendingReference {
	return &WagerTransactionPendingReference{
		Base: Base{
			EventID:       uuid.New(),
			EventType:     EventTypeWagerTransactionPendingReference,
			AggregateID:   aggregateID,
			CorrelationID: correlationID,
			OccurredAt:    time.Now().UTC(),
			Version:       1,
		},
		Data: data,
	}
}
