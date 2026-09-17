import os

status_go = """\
package transaction

import (
	"fmt"

	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
)

// Status representa o estado atual de uma WagerTransaction.
// A maquina de estados e definida abaixo — transicoes invalidas sao rejeitadas.
type Status string

const (
	// StatusPending: registro aceito, processamento nao concluido.
	// E o estado inicial de TODA transacao ao ser criada.
	StatusPending Status = "PENDING"

	// StatusPendingReference: a operacao depende de uma referencia ainda nao disponivel.
	// Exemplo: REFUND chega antes da BET correspondente.
	// Um worker de referencias tenta resolver com backoff exponencial.
	StatusPendingReference Status = "PENDING_REFERENCE"

	// StatusProcessed: operacao concluida com sucesso. Estado TERMINAL.
	// Replay consulta este resultado sem reaplicar a operacao.
	StatusProcessed Status = "PROCESSED"

	// StatusRejected: operacao recusada por regra de negocio. Estado TERMINAL.
	// Exemplos: saldo insuficiente, referencia nao encontrada apos max retries.
	// Tem um failureCode estavel documentado.
	StatusRejected Status = "REJECTED"

	// StatusFailed: falha permanente de infraestrutura registrada para auditoria. TERMINAL.
	// Diferente de REJECTED: indica problema tecnico, nao regra de negocio.
	StatusFailed Status = "FAILED"
)

// IsTerminal retorna true se o status e um estado terminal (sem novas transicoes).
// Uma transacao terminal NAO pode ter seu estado modificado — qualquer tentativa
// retorna ErrTerminalTransaction. Isso garante a imutabilidade historica do ledger.
func (s Status) IsTerminal() bool {
	return s == StatusProcessed || s == StatusRejected || s == StatusFailed
}

// String retorna a representacao string do Status.
func (s Status) String() string { return string(s) }

// FailureCode representa codigos de falha estaveis e documentados para rejeicoes.
// Codigos estaveis permitem que clientes (providers) programem contra eles.
type FailureCode string

const (
	// FailCodeInsufficientBalance: saldo insuficiente para a aposta.
	FailCodeInsufficientBalance FailureCode = "INSUFFICIENT_BALANCE"

	// FailCodeInsufficientBalanceReversal: saldo insuficiente para a reversao.
	// Codigo DIFERENTE de INSUFFICIENT_BALANCE conforme spec (reversoes e apostas
	// tem contextos diferentes — um provider precisa distingui-las).
	FailCodeInsufficientBalanceReversal FailureCode = "INSUFFICIENT_BALANCE_REVERSAL"

	// FailCodeReferenceNotFound: referencia nao encontrada apos max retries.
	FailCodeReferenceNotFound FailureCode = "REFERENCE_NOT_FOUND"

	// FailCodeReferenceAlreadyReversed: referencia ja teve reversao aplicada.
	FailCodeReferenceAlreadyReversed FailureCode = "REFERENCE_ALREADY_REVERSED"

	// FailCodeReferencePending: referencia existe mas ainda esta pendente.
	FailCodeReferencePending FailureCode = "REFERENCE_PENDING"

	// FailCodeAmountMismatch: valor da reversao diferente do original.
	FailCodeAmountMismatch FailureCode = "AMOUNT_MISMATCH"

	// FailCodeCurrencyMismatch: moedas incompativeis entre operacao e referencia.
	FailCodeCurrencyMismatch FailureCode = "CURRENCY_MISMATCH"

	// FailCodeProviderMismatch: provider da operacao diferente do da referencia.
	FailCodeProviderMismatch FailureCode = "PROVIDER_MISMATCH"

	// FailCodeInvalidOperationType: OPENING enviado por fonte externa.
	FailCodeInvalidOperationType FailureCode = "INVALID_OPERATION_TYPE"

	// FailCodeInfrastructure: falha de infraestrutura permanente (nao e regra de negocio).
	FailCodeInfrastructure FailureCode = "INFRASTRUCTURE_ERROR"
)

// String retorna a representacao string do FailureCode.
func (f FailureCode) String() string { return string(f) }

// transitionTable define as transicoes validas da maquina de estados.
// Chave: estado atual. Valor: conjunto de estados de destino validos.
// Transicoes nao listadas sao invalidas e retornam ErrInvalidTransitionState.
//
// Diagrama de estados:
//
//	                    +------------------+
//	                    |                  |
//	[CRIACAO] -----> PENDING ------> PENDING_REFERENCE ------> REJECTED (terminal)
//	                    |                  |
//	                    |                  +---> PROCESSED (terminal)
//	                    |
//	                    +---> PROCESSED (terminal)
//	                    |
//	                    +---> REJECTED (terminal)
//	                    |
//	                    +---> FAILED (terminal)
var transitionTable = map[Status]map[Status]bool{
	StatusPending: {
		StatusPendingReference: true,
		StatusProcessed:        true,
		StatusRejected:         true,
		StatusFailed:           true,
	},
	StatusPendingReference: {
		StatusProcessed: true,
		StatusRejected:  true,
		StatusFailed:    true,
	},
	// Estados terminais: sem transicoes possiveis
	StatusProcessed: {},
	StatusRejected:  {},
	StatusFailed:    {},
}

// ValidateTransition verifica se a transicao de `from` para `to` e valida.
// Retorna ErrTerminalTransaction se `from` e terminal.
// Retorna ErrInvalidTransitionState para transicoes nao mapeadas.
func ValidateTransition(from, to Status) error {
	if from.IsTerminal() {
		return fmt.Errorf("%w: transacao em estado %s nao pode transicionar para %s",
			domain.ErrTerminalTransaction, from, to)
	}
	allowed, exists := transitionTable[from]
	if !exists {
		return fmt.Errorf("%w: estado de origem desconhecido: %s",
			domain.ErrInvalidTransitionState, from)
	}
	if !allowed[to] {
		return fmt.Errorf("%w: %s -> %s nao e uma transicao valida",
			domain.ErrInvalidTransitionState, from, to)
	}
	return nil
}
"""

with open("internal/domain/transaction/status.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(status_go)

print("internal/domain/transaction/status.go criado")
