import os

wallet_test = """\
package wallet_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// novaCarteira e um helper que cria uma Wallet de teste com saldo inicial.
func novaCarteira(t *testing.T, saldoCentavos int64) *wallet.Wallet {
	t.Helper()
	playerID := uuid.New()
	balance := money.New(saldoCentavos, "BRL")
	w, err := wallet.NewWallet(playerID, "BRL", balance)
	if err != nil {
		t.Fatalf("NewWallet() erro inesperado: %v", err)
	}
	return w
}

// =============================================================================
// Testes de NewWallet
// =============================================================================

func TestNewWallet_Sucesso(t *testing.T) {
	playerID := uuid.New()
	initialBalance := money.New(100000, "BRL") // R$1000.00

	w, err := wallet.NewWallet(playerID, "BRL", initialBalance)
	if err != nil {
		t.Fatalf("NewWallet() erro inesperado: %v", err)
	}

	// Verificar estado inicial
	if w.ID() == uuid.Nil {
		t.Error("ID() nao deve ser nil")
	}
	if w.PlayerID() != playerID {
		t.Errorf("PlayerID() = %v, quer %v", w.PlayerID(), playerID)
	}
	if w.Currency() != "BRL" {
		t.Errorf("Currency() = %q, quer BRL", w.Currency())
	}
	if w.Balance().Amount() != 100000 {
		t.Errorf("Balance().Amount() = %d, quer 100000", w.Balance().Amount())
	}
	// Versao inicial deve ser 1 conforme spec
	if w.Version() != 1 {
		t.Errorf("Version() = %d, quer 1", w.Version())
	}
}

func TestNewWallet_MoedaIncompativel(t *testing.T) {
	playerID := uuid.New()
	// Criar carteira BRL com saldo em USD — deve falhar
	balance := money.New(10000, "USD")
	_, err := wallet.NewWallet(playerID, "BRL", balance)
	if !errors.Is(err, domain.ErrWalletCurrencyMismatch) {
		t.Errorf("NewWallet() deveria retornar ErrWalletCurrencyMismatch, got: %v", err)
	}
}

func TestNewWallet_SaldoZero(t *testing.T) {
	// Saldo zero e valido (nenhum OPENING sera criado, mas a carteira existe)
	w, err := wallet.NewWallet(uuid.New(), "BRL", money.Zero("BRL"))
	if err != nil {
		t.Fatalf("NewWallet() com saldo zero nao deve falhar: %v", err)
	}
	if !w.Balance().IsZero() {
		t.Error("Balance().IsZero() deveria ser true")
	}
}

// =============================================================================
// Testes de Debit
// =============================================================================

func TestDebit_Sucesso(t *testing.T) {
	w := novaCarteira(t, 10000) // R$100.00
	txID := uuid.New()

	entry, err := w.Debit(money.New(8000, "BRL"), txID) // Debitar R$80.00
	if err != nil {
		t.Fatalf("Debit() erro inesperado: %v", err)
	}

	// Saldo deve ser R$20.00 = 2000 centavos
	if w.Balance().Amount() != 2000 {
		t.Errorf("Balance() apos Debit = %d, quer 2000", w.Balance().Amount())
	}

	// Versao deve ser incrementada
	if w.Version() != 2 {
		t.Errorf("Version() apos Debit = %d, quer 2", w.Version())
	}

	// Verificar ledger entry
	if entry.Direction() != wallet.DirectionDebit {
		t.Errorf("Direction() = %v, quer DEBIT", entry.Direction())
	}
	if entry.Amount().Amount() != 8000 {
		t.Errorf("Amount().Amount() = %d, quer 8000", entry.Amount().Amount())
	}
	if entry.BalanceBefore().Amount() != 10000 {
		t.Errorf("BalanceBefore() = %d, quer 10000", entry.BalanceBefore().Amount())
	}
	if entry.BalanceAfter().Amount() != 2000 {
		t.Errorf("BalanceAfter() = %d, quer 2000", entry.BalanceAfter().Amount())
	}
}

func TestDebit_SaldoInsuficiente(t *testing.T) {
	w := novaCarteira(t, 10000) // R$100.00
	txID := uuid.New()

	// Tentar debitar R$200.00 de uma carteira com R$100.00
	_, err := w.Debit(money.New(20000, "BRL"), txID)
	if !errors.Is(err, domain.ErrInsufficientBalance) {
		t.Errorf("Debit() com saldo insuficiente deveria retornar ErrInsufficientBalance, got: %v", err)
	}

	// Saldo NAO deve ser alterado apos erro
	if w.Balance().Amount() != 10000 {
		t.Errorf("Balance() apos Debit com erro = %d, quer 10000 (imutavel)", w.Balance().Amount())
	}
	// Versao NAO deve ser incrementada apos erro
	if w.Version() != 1 {
		t.Errorf("Version() apos Debit com erro = %d, quer 1", w.Version())
	}
}

// TestDebit_CenarioCritico simula o cenario obrigatorio do desafio:
// Uma carteira com R$100.00 recebe duas apostas de R$80.00.
// Apenas uma deve ser aceita; a outra deve ser rejeitada por saldo insuficiente.
// Este teste nao testa concorrencia (isso e feito nos testes de integracao),
// mas garante que as invariantes de dominio estao corretas sequencialmente.
func TestDebit_CenarioCritico_DuasApostas(t *testing.T) {
	w := novaCarteira(t, 10000) // R$100.00

	// Primeira aposta: R$80.00 — deve ser aprovada
	_, err := w.Debit(money.New(8000, "BRL"), uuid.New())
	if err != nil {
		t.Fatalf("Primeira aposta de R$80.00 deveria ser aprovada: %v", err)
	}
	// Saldo: R$20.00
	if w.Balance().Amount() != 2000 {
		t.Errorf("Saldo apos 1a aposta = %d, quer 2000", w.Balance().Amount())
	}

	// Segunda aposta: R$80.00 — deve ser REJEITADA (saldo insuficiente)
	_, err = w.Debit(money.New(8000, "BRL"), uuid.New())
	if !errors.Is(err, domain.ErrInsufficientBalance) {
		t.Errorf("Segunda aposta deveria ser rejeitada por saldo insuficiente, got: %v", err)
	}
	// Saldo permanece R$20.00
	if w.Balance().Amount() != 2000 {
		t.Errorf("Saldo apos rejeicao = %d, quer 2000", w.Balance().Amount())
	}
}

func TestDebit_SaldoExato(t *testing.T) {
	// Debitar exatamente o saldo disponivel deve funcionar (saldo = R$0.00)
	w := novaCarteira(t, 10000) // R$100.00

	_, err := w.Debit(money.New(10000, "BRL"), uuid.New())
	if err != nil {
		t.Fatalf("Debit() com saldo exato nao deve falhar: %v", err)
	}
	if !w.Balance().IsZero() {
		t.Errorf("Balance() apos debito exato deveria ser zero, got: %d", w.Balance().Amount())
	}
}

func TestDebit_MoedaIncompativel(t *testing.T) {
	w := novaCarteira(t, 10000) // carteira BRL

	_, err := w.Debit(money.New(1000, "USD"), uuid.New())
	if !errors.Is(err, domain.ErrWalletCurrencyMismatch) {
		t.Errorf("Debit() com moeda errada deveria retornar ErrWalletCurrencyMismatch, got: %v", err)
	}
}

// =============================================================================
// Testes de Credit
// =============================================================================

func TestCredit_Sucesso(t *testing.T) {
	w := novaCarteira(t, 0) // R$0.00
	txID := uuid.New()

	entry, err := w.Credit(money.New(5000, "BRL"), txID) // Creditar R$50.00
	if err != nil {
		t.Fatalf("Credit() erro inesperado: %v", err)
	}

	if w.Balance().Amount() != 5000 {
		t.Errorf("Balance() apos Credit = %d, quer 5000", w.Balance().Amount())
	}
	if w.Version() != 2 {
		t.Errorf("Version() apos Credit = %d, quer 2", w.Version())
	}
	if entry.Direction() != wallet.DirectionCredit {
		t.Errorf("Direction() = %v, quer CREDIT", entry.Direction())
	}
	if entry.BalanceAfter().Amount() != 5000 {
		t.Errorf("BalanceAfter() = %d, quer 5000", entry.BalanceAfter().Amount())
	}
}

func TestCredit_MoedaIncompativel(t *testing.T) {
	w := novaCarteira(t, 0)

	_, err := w.Credit(money.New(1000, "USD"), uuid.New())
	if !errors.Is(err, domain.ErrWalletCurrencyMismatch) {
		t.Errorf("Credit() com moeda errada deveria retornar ErrWalletCurrencyMismatch, got: %v", err)
	}
}

// =============================================================================
// Testes de RehydrateWallet
// =============================================================================

func TestRehydrateWallet(t *testing.T) {
	id := uuid.New()
	playerID := uuid.New()
	import_time := "2024-01-01T00:00:00Z"
	_ = import_time

	import (
		"time"
	)

	createdAt, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")
	updatedAt, _ := time.Parse(time.RFC3339, "2024-06-15T12:00:00Z")

	w := wallet.RehydrateWallet(id, playerID, "BRL", 97500, 5, createdAt, updatedAt)

	if w.ID() != id {
		t.Errorf("ID() = %v, quer %v", w.ID(), id)
	}
	if w.Balance().Amount() != 97500 {
		t.Errorf("Balance().Amount() = %d, quer 97500", w.Balance().Amount())
	}
	if w.Version() != 5 {
		t.Errorf("Version() = %d, quer 5", w.Version())
	}
}

// =============================================================================
// Testes de WalletLedgerEntry
// =============================================================================

func TestLedgerEntry_InvarianteViolada(t *testing.T) {
	// Criar um ledger entry com invariante violada deve falhar
	walletID := uuid.New()
	txID := uuid.New()

	// Credit de R$10.00 com balanceBefore=R$100.00 e balanceAfter=R$200.00 (incorreto!)
	// Correto seria: 10000 + 1000 = 11000
	_, err := wallet.NewWalletLedgerEntry(
		walletID, txID,
		wallet.DirectionCredit,
		money.New(1000, "BRL"),  // amount: R$10.00
		money.New(10000, "BRL"), // balanceBefore: R$100.00
		money.New(20000, "BRL"), // balanceAfter: R$200.00 -- ERRADO!
	)
	if !errors.Is(err, domain.ErrInvalidLedgerEntry) {
		t.Errorf("NewWalletLedgerEntry() com invariante violada deveria retornar ErrInvalidLedgerEntry, got: %v", err)
	}
}

func TestLedgerEntry_InvarianteCorreta(t *testing.T) {
	walletID := uuid.New()
	txID := uuid.New()

	// Credit: R$100.00 + R$25.00 = R$125.00
	entry, err := wallet.NewWalletLedgerEntry(
		walletID, txID,
		wallet.DirectionCredit,
		money.New(2500, "BRL"),  // amount: R$25.00
		money.New(10000, "BRL"), // balanceBefore: R$100.00
		money.New(12500, "BRL"), // balanceAfter: R$125.00 -- CORRETO
	)
	if err != nil {
		t.Fatalf("NewWalletLedgerEntry() com invariante correta nao deve falhar: %v", err)
	}
	if entry.ID() == uuid.Nil {
		t.Error("ID() nao deve ser nil")
	}
}
"""

with open("internal/domain/wallet/wallet_test.go", "w", encoding="utf-8", newline="\n") as f:
    f.write(wallet_test)

print("internal/domain/wallet/wallet_test.go criado")
