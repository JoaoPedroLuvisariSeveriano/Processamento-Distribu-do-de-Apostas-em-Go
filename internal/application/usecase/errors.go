// Package usecase contem os casos de uso da aplicacao.
// Erros deste pacote representam resultados de negocio — devem ser mapeados
// para status HTTP / codigos de resposta pelo handler de transporte.
package usecase

import "errors"

// Erros de aplicacao — distintos dos erros de dominio (internal/domain/errors.go).
// Enquanto erros de dominio protegem invariantes, erros de aplicacao representam
// resultados de orquestracao (idempotencia, autorizacao, configuracao).
var (
	// ErrIdempotencyConflict: mesma idempotency_key com payload diferente.
	// Indica que o caller esta tentando reusar uma key para operacoes distintas —
	// violacao do contrato de idempotencia. HTTP 409 Conflict.
	ErrIdempotencyConflict = errors.New("usecase: conflito de idempotencia — mesmo key, payload diferente")

	// ErrWalletOwnerMismatch: playerID do JWT nao confere com o dono da carteira.
	// HTTP 403 Forbidden.
	ErrWalletOwnerMismatch = errors.New("usecase: jogador nao e dono desta carteira")

	// ErrProviderMismatch: provider da operacao nao confere com o da referencia.
	// HTTP 422 Unprocessable Entity.
	ErrProviderMismatch = errors.New("usecase: provider da operacao nao confere com a referencia")

	// ErrAmountMismatch: valor da reversao nao coincide com o da referencia.
	// HTTP 422 Unprocessable Entity.
	ErrAmountMismatch = errors.New("usecase: valor da operacao nao coincide com a referencia")

	// ErrReferenceAlreadyReversed: referencia ja possui reversao processada.
	// HTTP 422 Unprocessable Entity.
	ErrReferenceAlreadyReversed = errors.New("usecase: referencia ja foi revertida com sucesso")

	// ErrReferencePending: referencia existe mas ainda nao foi processada.
	// O caller deve aguardar ou a transacao vai para PENDING_REFERENCE.
	ErrReferencePending = errors.New("usecase: referencia existe mas ainda nao foi processada")

	// ErrReferenceRejected: referencia foi rejeitada — reversao impossivel.
	// HTTP 422 Unprocessable Entity.
	ErrReferenceRejected = errors.New("usecase: referencia foi rejeitada e nao pode ser revertida")

	// ErrInvalidOpeningKind: tentativa de enviar OPENING via HTTP/SQS externo.
	// HTTP 400 Bad Request.
	ErrInvalidOpeningKind = errors.New("usecase: tipo OPENING e exclusivo de abertura interna de carteira")
)
