import os

wallet_go = """\
// Package wallet implementa o Aggregate Root Wallet e o WalletLedgerEntry.
//
// Em DDD, um Aggregate Root e a unica "porta de entrada" para um conjunto de
// entidades relacionadas. Toda modificacao de estado deve passar pelos metodos
// publicos do aggregate, que garantem as invariantes de negocio.
//
// Invariantes da Wallet:
//  1. Saldo nunca fica negativo (ErrInsufficientBalance antes de debitar)
//  2. A moeda de cada operacao deve coincidir com a da carteira
//  3. A versao e incrementada a cada mudanca de saldo (para lock otimista)
//  4. Cada mudanca de saldo gera exatamente um WalletLedgerEntry correspondente
package wallet

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
)

// Wallet e o Aggregate Root do contexto financeiro.
//
// Todos os campos sao PRIVADOS (lowercase) para garantir encapsulamento total.
// O estado so pode ser lido via getters e modificado via metodos de dominio.
// Isso evita que camadas externas (infra, application) violem invariantes.
type Wallet struct {
	id        uuid.UUID
	playerID  uuid.UUID
	currency  string
	balance   money.Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

// NewWallet cria uma nova Wallet com saldo inicial.
// Esta e a fabrica de criacao — usada quando um jogador abre uma carteira.
//
// Separamos criacao (NewWallet) de reidratacao (RehydrateWallet) porque:
//   - Na criacao, geramos um novo UUID e definimos os timestamps.
//   - Na reidratacao (carregar do banco), apenas restauramos o estado persistido
//     sem disparar eventos ou reincrementar versao.
func NewWallet(playerID uuid.UUID, currency string, initialBalance money.Money) (*Wallet, error) {
	// Validar que a moeda do saldo inicial corresponde a moeda da carteira.
	// O par (playerId, currency) identifica unicamente uma carteira.
	if initialBalance.Currency() != currency {
		return nil, fmt.Errorf("%w: saldo inicial em %s, carteira em %s",
			domain.ErrWalletCurrencyMismatch, initialBalance.Currency(), currency)
	}

	// Saldo inicial pode ser zero (nenhum lancamento de ledger sera criado).
	// Saldo inicial negativo e impossivel pois money.Parse rejeita negativos.
	now := time.Now().UTC()
	return &Wallet{
		id:        uuid.New(),
		playerID:  playerID,
		currency:  currency,
		balance:   initialBalance,
		// Versao inicial e 1 conforme spec.
		// A versao e usada para lock otimista: cada UPDATE no banco verifica
		// WHERE version = $versaoConhecida para detectar escrita concorrente.
		version:   1,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// RehydrateWallet reconstroi uma Wallet a partir de dados persistidos.
// NAO deve reaplicar logica de negocio — apenas restaura o estado.
//
// A separacao criacao/reidratacao e fundamental em DDD:
// Se chamassemos NewWallet ao carregar do banco, gerariamos IDs novos,
// reincrementariamos versao e perderiamos o historico.
func RehydrateWallet(
	id, playerID uuid.UUID,
	currency string,
	balanceCents int64,
	version int64,
	createdAt, updatedAt time.Time,
) *Wallet {
	return &Wallet{
		id:        id,
		playerID:  playerID,
		currency:  currency,
		// Reconstruimos Money diretamente em centavos, sem passar por Parse,
		// pois o banco ja armazena o valor validado.
		balance:   money.New(balanceCents, currency),
		version:   version,
		createdAt: createdAt,
		updatedAt: updatedAt,
	}
}

// =============================================================================
// Operacoes financeiras
// =============================================================================

// Credit adiciona um valor ao saldo da carteira e retorna o LedgerEntry correspondente.
//
// O metodo modifica o estado interno da Wallet (balance, version, updatedAt).
// A persistencia e responsabilidade do repositorio — o dominio apenas calcula.
//
// Fluxo:
//  1. Validar moeda
//  2. Calcular novo saldo
//  3. Criar WalletLedgerEntry (valida invariante balanceAfter = balanceBefore + amount)
//  4. Atualizar estado interno
func (w *Wallet) Credit(amount money.Money, transactionID uuid.UUID) (*WalletLedgerEntry, error) {
	if err := w.validateCurrency(amount); err != nil {
		return nil, err
	}
	if !amount.IsPositive() {
		return nil, fmt.Errorf("%w: credito exige valor positivo, recebeu %s",
			domain.ErrInsufficientBalance, amount.String())
	}

	newBalance, err := w.balance.Add(amount)
	if err != nil {
		return nil, fmt.Errorf("erro ao calcular novo saldo: %w", err)
	}

	// Criar o lancamento contabil ANTES de modificar o estado interno.
	// Se NewWalletLedgerEntry retornar erro (invariante violada), o estado
	// da Wallet nao sera alterado (fail-fast sem efeito colateral).
	entry, err := NewWalletLedgerEntry(
		w.id,
		transactionID,
		DirectionCredit,
		amount,
		w.balance,  // saldo ANTES do credito
		newBalance, // saldo APOS o credito
	)
	if err != nil {
		return nil, err
	}

	// Atualizar estado interno apenas apos validacoes passarem.
	w.balance = newBalance
	w.version++
	w.updatedAt = time.Now().UTC()

	return entry, nil
}

// Debit subtrai um valor do saldo da carteira e retorna o LedgerEntry.
//
// INVARIANTE CRITICA: Saldo nunca pode ficar negativo.
// Esta validacao e feita no dominio (primeira linha de defesa) E no banco
// (CHECK CONSTRAINT balance >= 0 — segunda linha de defesa).
// A duplicidade e intencional: garante corretude mesmo que a logica de dominio
// seja contornada por codigo de infraestrutura.
func (w *Wallet) Debit(amount money.Money, transactionID uuid.UUID) (*WalletLedgerEntry, error) {
	if err := w.validateCurrency(amount); err != nil {
		return nil, err
	}
	if !amount.IsPositive() {
		return nil, fmt.Errorf("%w: debito exige valor positivo, recebeu %s",
			domain.ErrInsufficientBalance, amount.String())
	}

	// Verificar se ha saldo suficiente ANTES de debitar.
	// Aqui usamos GreaterThanOrEqual para cobrir o caso exato (saldo = valor do debito).
	sufficient, err := w.balance.GreaterThanOrEqual(amount)
	if err != nil {
		return nil, err
	}
	if !sufficient {
		return nil, fmt.Errorf("%w: saldo %s < debito %s",
			domain.ErrInsufficientBalance, w.balance.String(), amount.String())
	}

	newBalance, err := w.balance.Sub(amount)
	if err != nil {
		return nil, fmt.Errorf("erro ao calcular novo saldo: %w", err)
	}

	entry, err := NewWalletLedgerEntry(
		w.id,
		transactionID,
		DirectionDebit,
		amount,
		w.balance,  // saldo ANTES do debito
		newBalance, // saldo APOS o debito (nunca negativo)
	)
	if err != nil {
		return nil, err
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = time.Now().UTC()

	return entry, nil
}

// =============================================================================
// Getters (acesso somente-leitura ao estado encapsulado)
// =============================================================================

// ID retorna o identificador unico da carteira.
func (w *Wallet) ID() uuid.UUID { return w.id }

// PlayerID retorna o identificador do jogador dono da carteira.
func (w *Wallet) PlayerID() uuid.UUID { return w.playerID }

// Currency retorna o codigo ISO 4217 da moeda da carteira.
func (w *Wallet) Currency() string { return w.currency }

// Balance retorna o saldo atual como Money (imutavel).
func (w *Wallet) Balance() money.Money { return w.balance }

// Version retorna a versao atual (usada para deteccao de escrita concorrente).
func (w *Wallet) Version() int64 { return w.version }

// CreatedAt retorna o instante de criacao da carteira (UTC).
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }

// UpdatedAt retorna o instante da ultima modificacao (UTC).
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }

// =============================================================================
// Helpers internos
// =============================================================================

// validateCurrency verifica que a moeda da operacao coincide com a da carteira.
func (w *Wallet) validateCurrency(amount money.Money) error {
	if amount.Currency() != w.currency {
		return fmt.Errorf("%w: operacao em %s, carteira em %s",
			domain.ErrWalletCurrencyMismatch, amount.Currency(), w.currency)
	}
	return nil
}
"""

with open("internal/domain/wallet/wallet.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(wallet_go)

print("internal/domain/wallet/wallet.go criado")
