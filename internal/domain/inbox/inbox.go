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
