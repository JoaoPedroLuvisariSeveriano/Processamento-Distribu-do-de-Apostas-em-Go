// Package domain define os erros sentinela compartilhados entre todos os
// subpacotes do dominio. Erros de dominio sao classificaveis via errors.Is
// e errors.As — nunca usamos panic para rejeicoes de negocio.
package domain

import "errors"

// Erros de carteira
var (
	// ErrInsufficientBalance e retornado quando um debito excede o saldo disponivel.
	// e um resultado de negocio definitivo (nao e um erro transitorio).
	ErrInsufficientBalance = errors.New("domain: saldo insuficiente para a operacao")

	// ErrWalletNotFound indica que a carteira solicitada nao existe.
	ErrWalletNotFound = errors.New("domain: carteira nao encontrada")

	// ErrWalletAlreadyExists indica tentativa de abrir carteira ja existente
	// para o mesmo (playerId, currency).
	ErrWalletAlreadyExists = errors.New("domain: carteira ja existe para este jogador e moeda")

	// ErrWalletCurrencyMismatch indica que a moeda da operacao nao corresponde
	// a moeda da carteira.
	ErrWalletCurrencyMismatch = errors.New("domain: moeda da operacao incompativel com a carteira")
)

// Erros de transacao
var (
	// ErrTransactionNotFound indica que a transacao referenciada nao existe.
	ErrTransactionNotFound = errors.New("domain: transacao nao encontrada")

	// ErrDuplicateTransaction indica tentativa de reprocessar uma transacao
	// ja processada com payload diferente (conflito de idempotencia).
	ErrDuplicateTransaction = errors.New("domain: conflito de idempotencia — payload diferente para a mesma chave")

	// ErrInvalidTransitionState indica uma transicao de estado invalida
	// na maquina de estados da WagerTransaction.
	ErrInvalidTransitionState = errors.New("domain: transicao de estado invalida para a transacao")

	// ErrTerminalTransaction indica tentativa de alterar uma transacao em
	// estado terminal (PROCESSED, REJECTED ou FAILED).
	ErrTerminalTransaction = errors.New("domain: transacao em estado terminal nao pode ser alterada")

	// ErrReferenceNotFound indica que a transacao referenciada (para REFUND/ROLLBACK)
	// nao foi encontrada ou nao e elegivel.
	ErrReferenceNotFound = errors.New("domain: transacao de referencia nao encontrada")

	// ErrReferenceAlreadyReversed indica que a referencia ja teve uma reversao aplicada.
	ErrReferenceAlreadyReversed = errors.New("domain: transacao de referencia ja foi revertida")

	// ErrReferencePending indica que a transacao referenciada ainda esta pendente.
	ErrReferencePending = errors.New("domain: transacao de referencia ainda esta pendente")

	// ErrAmountMismatch indica que o valor da reversao nao coincide com o original.
	ErrAmountMismatch = errors.New("domain: valor da operacao nao coincide com o valor da referencia")

	// ErrInvalidOperationType indica um tipo de operacao invalido para o contexto.
	// Ex: OPENING enviado via HTTP/SQS (uso exclusivo interno).
	ErrInvalidOperationType = errors.New("domain: tipo de operacao invalido para esta entrada")

	// ErrProviderMismatch indica que o provider da operacao nao confere com a referencia.
	ErrProviderMismatch = errors.New("domain: provider da operacao incompativel com a referencia")

	// ErrMaxRetriesExceeded indica que o numero maximo de tentativas foi atingido.
	ErrMaxRetriesExceeded = errors.New("domain: numero maximo de tentativas excedido")
)

// Erros de ledger
var (
	// ErrInvalidLedgerEntry indica que os valores de um lancamento contabil
	// nao satisfazem a invariante balanceAfter = balanceBefore +- amount.
	ErrInvalidLedgerEntry = errors.New("domain: lancamento contabil invalido — invariante de saldo violada")
)
