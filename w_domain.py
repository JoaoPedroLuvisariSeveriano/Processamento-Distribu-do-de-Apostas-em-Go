import os

outbox_go = """\
// Package outbox implementa a entidade OutboxEvent do Transactional Outbox Pattern.
//
// O padrao funciona assim:
//  1. A operacao de dominio (ex: Wallet.Debit) e confirmada no banco.
//  2. NA MESMA TRANSACAO SQL, um OutboxEvent e inserido em outbox_events.
//  3. Um worker separado le eventos pendentes e os publica no SQS.
//  4. Apos publicacao confirmada, marca o evento como publicado.
//
// Isso garante que NENHUM evento seja perdido mesmo se o processo crashar entre
// o commit do dominio e a publicacao no SQS (exatamente o que a spec exige).
package outbox

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OutboxEvent e o registro de um evento a ser publicado no SQS.
// O payload e um snapshot IMUTAVEL do evento no momento em que ocorreu.
// Republicas preservam o mesmo EventID — consumidores usam isso para deduplicacao.
type OutboxEvent struct {
	id             uuid.UUID
	aggregateID    uuid.UUID
	eventType      string
	payload        json.RawMessage // snapshot imutavel serializado
	occurredAt     time.Time
	attempts       int
	maxAttempts    int
	nextDeliveryAt time.Time
	publishedAt    *time.Time
	createdAt      time.Time
}

// NewOutboxEvent cria um OutboxEvent a partir de um evento de dominio serializado.
// Deve ser chamado DENTRO da transacao SQL que originou o evento.
func NewOutboxEvent(
	aggregateID uuid.UUID,
	eventType string,
	payload json.RawMessage,
	occurredAt time.Time,
) (*OutboxEvent, error) {
	if eventType == "" {
		return nil, fmt.Errorf("outbox: eventType nao pode ser vazio")
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("outbox: payload nao pode ser vazio")
	}

	now := time.Now().UTC()
	return &OutboxEvent{
		id:          uuid.New(),
		aggregateID: aggregateID,
		eventType:   eventType,
		payload:     payload,
		occurredAt:  occurredAt,
		attempts:    0,
		maxAttempts: 5,
		// nextDeliveryAt = NOW(): disponivel para entrega imediatamente.
		nextDeliveryAt: now,
		createdAt:      now,
	}, nil
}

// RehydrateOutboxEvent reconstroi um OutboxEvent a partir do banco.
func RehydrateOutboxEvent(
	id, aggregateID uuid.UUID,
	eventType string,
	payload json.RawMessage,
	occurredAt time.Time,
	attempts, maxAttempts int,
	nextDeliveryAt time.Time,
	publishedAt *time.Time,
	createdAt time.Time,
) *OutboxEvent {
	return &OutboxEvent{
		id:             id,
		aggregateID:    aggregateID,
		eventType:      eventType,
		payload:        payload,
		occurredAt:     occurredAt,
		attempts:       attempts,
		maxAttempts:    maxAttempts,
		nextDeliveryAt: nextDeliveryAt,
		publishedAt:    publishedAt,
		createdAt:      createdAt,
	}
}

// MarkAsPublished marca o evento como publicado com sucesso.
func (e *OutboxEvent) MarkAsPublished() {
	now := time.Now().UTC()
	e.publishedAt = &now
}

// RecordFailure registra uma falha de publicacao e calcula o proximo retry
// usando backoff exponencial: nextDelivery = NOW() + 2^attempts * baseDelay.
func (e *OutboxEvent) RecordFailure(baseDelay time.Duration) {
	e.attempts++
	// Backoff exponencial: 5s, 10s, 20s, 40s, 80s...
	delay := baseDelay * (1 << e.attempts)
	next := time.Now().UTC().Add(delay)
	e.nextDeliveryAt = next
}

// HasExceededMaxAttempts retorna true se o numero de tentativas excedeu o limite.
func (e *OutboxEvent) HasExceededMaxAttempts() bool {
	return e.attempts >= e.maxAttempts
}

// IsPublished retorna true se o evento ja foi publicado.
func (e *OutboxEvent) IsPublished() bool { return e.publishedAt != nil }

// Getters
func (e *OutboxEvent) ID() uuid.UUID              { return e.id }
func (e *OutboxEvent) AggregateID() uuid.UUID     { return e.aggregateID }
func (e *OutboxEvent) EventType() string          { return e.eventType }
func (e *OutboxEvent) Payload() json.RawMessage   { return e.payload }
func (e *OutboxEvent) OccurredAt() time.Time      { return e.occurredAt }
func (e *OutboxEvent) Attempts() int              { return e.attempts }
func (e *OutboxEvent) MaxAttempts() int           { return e.maxAttempts }
func (e *OutboxEvent) NextDeliveryAt() time.Time  { return e.nextDeliveryAt }
func (e *OutboxEvent) PublishedAt() *time.Time    { return e.publishedAt }
func (e *OutboxEvent) CreatedAt() time.Time       { return e.createdAt }
"""

inbox_go = """\
// Package inbox implementa a entidade InboxMessage do Inbox Pattern.
//
// O padrao resolve o problema de "at-least-once delivery" do SQS:
// a mesma mensagem pode ser entregue multiplas vezes (ex: timeout de visibilidade).
//
// Fluxo:
//  1. Ao receber mensagem SQS, inserimos em inbox_messages com UNIQUE (consumer_name, message_id).
//  2. Se o INSERT ter sucesso -> mensagem nova, processar normalmente.
//  3. Se UNIQUE VIOLATION (23505) -> mensagem ja foi processada, ignorar (replay seguro).
//
// A insercao em inbox_messages acontece NA MESMA TRANSACAO SQL que altera o dominio,
// garantindo atomicidade: ou ambos comitam ou ambos revertem.
package inbox

import (
	"time"

	"github.com/google/uuid"
)

// InboxMessage representa o registro de uma mensagem SQS no banco.
// Garante processamento exatamente-uma-vez por (consumerName, messageID).
type InboxMessage struct {
	id           uuid.UUID
	consumerName string // ex: "sqs-wager-consumer"
	messageID    string // messageId do envelope SQS
	payloadHash  string // hash do corpo da mensagem para detectar corrupcao
	receivedAt   time.Time
	processedAt  *time.Time // nil = pendente; NOT NULL = concluido
}

// NewInboxMessage cria um registro de mensagem recebida.
// Deve ser chamado no inicio do processamento, antes de qualquer operacao de dominio.
func NewInboxMessage(consumerName, messageID, payloadHash string) *InboxMessage {
	return &InboxMessage{
		id:           uuid.New(),
		consumerName: consumerName,
		messageID:    messageID,
		payloadHash:  payloadHash,
		receivedAt:   time.Now().UTC(),
	}
}

// RehydrateInboxMessage reconstroi a partir do banco.
func RehydrateInboxMessage(
	id uuid.UUID,
	consumerName, messageID, payloadHash string,
	receivedAt time.Time,
	processedAt *time.Time,
) *InboxMessage {
	return &InboxMessage{
		id:           id,
		consumerName: consumerName,
		messageID:    messageID,
		payloadHash:  payloadHash,
		receivedAt:   receivedAt,
		processedAt:  processedAt,
	}
}

// MarkAsProcessed registra a conclusao do processamento.
func (m *InboxMessage) MarkAsProcessed() {
	now := time.Now().UTC()
	m.processedAt = &now
}

// IsProcessed retorna true se a mensagem ja foi processada.
func (m *InboxMessage) IsProcessed() bool { return m.processedAt != nil }

// Getters
func (m *InboxMessage) ID() uuid.UUID           { return m.id }
func (m *InboxMessage) ConsumerName() string    { return m.consumerName }
func (m *InboxMessage) MessageID() string       { return m.messageID }
func (m *InboxMessage) PayloadHash() string     { return m.payloadHash }
func (m *InboxMessage) ReceivedAt() time.Time   { return m.receivedAt }
func (m *InboxMessage) ProcessedAt() *time.Time { return m.processedAt }
"""

with open("internal/domain/outbox/outbox.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(outbox_go)

with open("internal/domain/inbox/inbox.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(inbox_go)

print("outbox.go e inbox.go criados")
