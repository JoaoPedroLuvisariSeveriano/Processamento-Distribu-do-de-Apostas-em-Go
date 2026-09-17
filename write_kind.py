import os

kind_go = """\
// Package transaction implementa a entidade WagerTransaction e sua maquina de estados.
package transaction

// Kind representa o tipo de operacao externa enviada pelo provedor.
// Cada tipo tem regras especificas de movimentacao e condicoes de validade.
type Kind string

const (
	// KindOpening e reservado para abertura interna de carteira.
	// NUNCA deve ser enviado via HTTP ou SQS por providers externos.
	KindOpening Kind = "OPENING"

	// KindBet representa uma aposta (debito na carteira).
	// Exige: valor positivo, saldo suficiente.
	KindBet Kind = "BET"

	// KindWin representa um premio (credito na carteira).
	// Exige: valor positivo. Pode referenciar uma aposta da mesma rodada.
	KindWin Kind = "WIN"

	// KindLoss representa uma perda sem movimentacao de saldo.
	// Exige: amount = "0.00". Nao cria ledger entry nem altera versao da carteira.
	KindLoss Kind = "LOSS"

	// KindRefund representa estorno integral de uma aposta processada (credito).
	// Exige: referenceExternalTransactionId obrigatorio, valor identico ao original.
	KindRefund Kind = "REFUND"

	// KindRollback desfaz integralmente uma operacao anterior (BET, WIN ou REFUND).
	// A direcao do movimento e contraria a da transacao referenciada.
	KindRollback Kind = "ROLLBACK"
)

// IsExternal retorna true se o tipo pode ser enviado por providers externos (HTTP/SQS).
// OPENING e exclusivamente interno.
func (k Kind) IsExternal() bool {
	return k != KindOpening
}

// RequiresReference retorna true se o tipo OBRIGATORIAMENTE precisa de uma referencia.
func (k Kind) RequiresReference() bool {
	return k == KindRefund || k == KindRollback
}

// MayReference retorna true se o tipo PODE (opcionalmente) ter uma referencia.
func (k Kind) MayReference() bool {
	return k == KindWin
}

// CreatesLedger retorna true se o tipo gera um lancamento no ledger.
// LOSS nao gera ledger conforme spec.
func (k Kind) CreatesLedger() bool {
	return k != KindLoss
}

// String retorna a representacao string do Kind.
func (k Kind) String() string { return string(k) }

// IsValid verifica se o Kind e um valor conhecido.
func (k Kind) IsValid() bool {
	switch k {
	case KindOpening, KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return true
	}
	return false
}
"""

with open("internal/domain/transaction/kind.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(kind_go)

print("internal/domain/transaction/kind.go criado")
