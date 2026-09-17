import os

ledger_go = """\
package wallet

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
)

// Direction representa a direcao de um lancamento contabil.
// Cada movimentacao financeira tem EXATAMENTE uma direcao.
type Direction string

const (
	// DirectionCredit representa uma entrada de valor na carteira (WIN, REFUND, OPENING).
	DirectionCredit Direction = "CREDIT"
	// DirectionDebit representa uma saida de valor da carteira (BET, ROLLBACK).
	DirectionDebit Direction = "DEBIT"
)

// WalletLedgerEntry representa um lancamento no razao (ledger) da carteira.
//
// O ledger e APPEND-ONLY: lancamentos nunca sao editados ou deletados.
// Correcoes financeiras exigem NOVOS lancamentos (ex: ROLLBACK cria um credito
// para desfazer um debito anterior, nao edita o debito original).
//
// INVARIANTE: balanceAfter = balanceBefore + amount (CREDIT)
//             balanceAfter = balanceBefore - amount (DEBIT)
// Esta invariante e verificada no construtor e imposta no banco via CHECK CONSTRAINT.
//
// Todos os campos sao PRIVADOS (imutabilidade pos-construcao).
type WalletLedgerEntry struct {
	id            uuid.UUID
	walletID      uuid.UUID
	transactionID uuid.UUID
	direction     Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

// NewWalletLedgerEntry cria um lancamento contabil com validacao da invariante.
//
// Esta funcao e chamada por Wallet.Credit() e Wallet.Debit() — nunca diretamente
// pelo application layer. A validacao aqui e a "primeira linha de defesa" antes
// de chegar ao banco.
func NewWalletLedgerEntry(
	walletID uuid.UUID,
	transactionID uuid.UUID,
	direction Direction,
	amount money.Money,
	balanceBefore money.Money,
	balanceAfter money.Money,
) (*WalletLedgerEntry, error) {
	// Verificar a invariante: balanceAfter deve ser a soma/diferenca correta.
	var expectedAfter money.Money
	var err error

	switch direction {
	case DirectionCredit:
		// CREDIT: balanceAfter = balanceBefore + amount
		expectedAfter, err = balanceBefore.Add(amount)
		if err != nil {
			return nil, fmt.Errorf("erro ao calcular invariante de ledger: %w", err)
		}
	case DirectionDebit:
		// DEBIT: balanceAfter = balanceBefore - amount
		expectedAfter, err = balanceBefore.Sub(amount)
		if err != nil {
			return nil, fmt.Errorf("erro ao calcular invariante de ledger: %w", err)
		}
	default:
		return nil, fmt.Errorf("%w: direcao desconhecida: %q", domain.ErrInvalidLedgerEntry, direction)
	}

	// Validar que o balanceAfter informado coincide com o calculado.
	eq, err := expectedAfter.Equal(balanceAfter)
	if err != nil {
		return nil, fmt.Errorf("erro ao comparar saldos do ledger: %w", err)
	}
	if !eq {
		return nil, fmt.Errorf(
			"%w: esperado %s, recebido %s",
			domain.ErrInvalidLedgerEntry,
			expectedAfter.String(),
			balanceAfter.String(),
		)
	}

	return &WalletLedgerEntry{
		id:            uuid.New(),
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     time.Now().UTC(),
	}, nil
}

// RehydrateLedgerEntry reconstroi um lancamento a partir de dados do banco.
// Nao revalida a invariante (assumida correta pois veio do banco com constraints).
func RehydrateLedgerEntry(
	id, walletID, transactionID uuid.UUID,
	direction Direction,
	amountCents, balanceBeforeCents, balanceAfterCents int64,
	currency string,
	createdAt time.Time,
) *WalletLedgerEntry {
	return &WalletLedgerEntry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        money.New(amountCents, currency),
		balanceBefore: money.New(balanceBeforeCents, currency),
		balanceAfter:  money.New(balanceAfterCents, currency),
		createdAt:     createdAt,
	}
}

// Getters do WalletLedgerEntry
func (e *WalletLedgerEntry) ID() uuid.UUID            { return e.id }
func (e *WalletLedgerEntry) WalletID() uuid.UUID      { return e.walletID }
func (e *WalletLedgerEntry) TransactionID() uuid.UUID { return e.transactionID }
func (e *WalletLedgerEntry) Direction() Direction     { return e.direction }
func (e *WalletLedgerEntry) Amount() money.Money      { return e.amount }
func (e *WalletLedgerEntry) BalanceBefore() money.Money { return e.balanceBefore }
func (e *WalletLedgerEntry) BalanceAfter() money.Money  { return e.balanceAfter }
func (e *WalletLedgerEntry) CreatedAt() time.Time     { return e.createdAt }
"""

with open("internal/domain/wallet/ledger.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(ledger_go)

print("internal/domain/wallet/ledger.go criado")
