package wallet_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/wallet"
)

// novaCarteira e um helper de teste que cria uma Wallet com saldo inicial.
func novaCarteira(t *testing.T, saldoCentavos int64) *wallet.Wallet {
	t.Helper()
	balance := money.New(saldoCentavos, "BRL")
	w, err := wallet.NewWallet(uuid.New(), "BRL", balance)
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
	balance := money.New(10000, "USD") // saldo em USD
	_, err := wallet.NewWallet(uuid.New(), "BRL", balance) // carteira BRL
	if !errors.Is(err, domain.ErrWalletCurrencyMismatch) {
		t.Errorf("NewWallet() deveria retornar ErrWalletCurrencyMismatch, got: %v", err)
	}
}

func TestNewWallet_SaldoZero(t *testing.T) {
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

	entry, err := w.Debit(money.New(8000, "BRL"), txID)
	if err != nil {
		t.Fatalf("Debit() erro inesperado: %v", err)
	}
	if w.Balance().Amount() != 2000 {
		t.Errorf("Balance() apos Debit = %d, quer 2000", w.Balance().Amount())
	}
	if w.Version() != 2 {
		t.Errorf("Version() apos Debit = %d, quer 2", w.Version())
	}
	if entry.Direction() != wallet.DirectionDebit {
		t.Errorf("Direction() = %v, quer DEBIT", entry.Direction())
	}
	if entry.Amount().Amount() != 8000 {
		t.Errorf("Amount() = %d, quer 8000", entry.Amount().Amount())
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

	_, err := w.Debit(money.New(20000, "BRL"), uuid.New())
	if !errors.Is(err, domain.ErrInsufficientBalance) {
		t.Errorf("Debit() deveria retornar ErrInsufficientBalance, got: %v", err)
	}
	// Estado nao deve ser alterado apos erro
	if w.Balance().Amount() != 10000 {
		t.Errorf("Balance() apos erro = %d, quer 10000 (imutavel)", w.Balance().Amount())
	}
	if w.Version() != 1 {
		t.Errorf("Version() apos erro = %d, quer 1", w.Version())
	}
}

// TestDebit_CenarioCritico simula o cenario obrigatorio da spec:
// Carteira com R$100.00, duas apostas de R$80.00 sequenciais.
// Resultado esperado: 1 aprovada, 1 rejeitada, saldo final R$20.00.
func TestDebit_CenarioCritico_DuasApostas(t *testing.T) {
	w := novaCarteira(t, 10000) // R$100.00

	// Primeira aposta R$80.00 — DEVE ser aprovada
	_, err := w.Debit(money.New(8000, "BRL"), uuid.New())
	if err != nil {
		t.Fatalf("1a aposta (R$80.00) deveria ser aprovada: %v", err)
	}
	if w.Balance().Amount() != 2000 {
		t.Errorf("Saldo apos 1a aposta = %d, quer 2000 (R$20.00)", w.Balance().Amount())
	}

	// Segunda aposta R$80.00 — DEVE ser rejeitada (saldo insuficiente)
	_, err = w.Debit(money.New(8000, "BRL"), uuid.New())
	if !errors.Is(err, domain.ErrInsufficientBalance) {
		t.Errorf("2a aposta deveria ser rejeitada com ErrInsufficientBalance, got: %v", err)
	}
	// Saldo permanece R$20.00 (1 unico debito no ledger)
	if w.Balance().Amount() != 2000 {
		t.Errorf("Saldo apos rejeicao = %d, quer 2000 (R$20.00)", w.Balance().Amount())
	}
}

func TestDebit_SaldoExato(t *testing.T) {
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
	w := novaCarteira(t, 10000)
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
	entry, err := w.Credit(money.New(5000, "BRL"), uuid.New())
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
	if entry.BalanceBefore().Amount() != 0 {
		t.Errorf("BalanceBefore() = %d, quer 0", entry.BalanceBefore().Amount())
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
	createdAt, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")
	updatedAt, _ := time.Parse(time.RFC3339, "2024-06-15T12:00:00Z")

	w := wallet.RehydrateWallet(id, playerID, "BRL", 97500, 5, createdAt, updatedAt)

	if w.ID() != id {
		t.Errorf("ID() = %v, quer %v", w.ID(), id)
	}
	if w.PlayerID() != playerID {
		t.Errorf("PlayerID() = %v, quer %v", w.PlayerID(), playerID)
	}
	if w.Balance().Amount() != 97500 {
		t.Errorf("Balance().Amount() = %d, quer 97500", w.Balance().Amount())
	}
	if w.Version() != 5 {
		t.Errorf("Version() = %d, quer 5", w.Version())
	}
	// Timestamps preservados exatamente
	if !w.CreatedAt().Equal(createdAt) {
		t.Errorf("CreatedAt() = %v, quer %v", w.CreatedAt(), createdAt)
	}
}

// TestRehydrate_NaoDisparaEventos verifica que reidratacao nao incrementa versao.
// Diferentemente de NewWallet, RehydrateWallet nao chama logica de negocio.
func TestRehydrate_NaoIncrementaVersao(t *testing.T) {
	createdAt := time.Now().UTC()
	w := wallet.RehydrateWallet(uuid.New(), uuid.New(), "BRL", 5000, 7, createdAt, createdAt)
	if w.Version() != 7 {
		t.Errorf("RehydrateWallet nao deve alterar versao: got %d, quer 7", w.Version())
	}
}

// =============================================================================
// Testes de WalletLedgerEntry
// =============================================================================

func TestLedgerEntry_InvarianteViolada(t *testing.T) {
	_, err := wallet.NewWalletLedgerEntry(
		uuid.New(), uuid.New(),
		wallet.DirectionCredit,
		money.New(1000, "BRL"),  // amount: R$10.00
		money.New(10000, "BRL"), // balanceBefore: R$100.00
		money.New(20000, "BRL"), // balanceAfter: R$200.00 (ERRADO: deveria ser R$110.00)
	)
	if !errors.Is(err, domain.ErrInvalidLedgerEntry) {
		t.Errorf("LedgerEntry com invariante violada deveria retornar ErrInvalidLedgerEntry, got: %v", err)
	}
}

func TestLedgerEntry_CreditInvarianteCorreta(t *testing.T) {
	// R$100.00 + R$25.00 = R$125.00
	entry, err := wallet.NewWalletLedgerEntry(
		uuid.New(), uuid.New(),
		wallet.DirectionCredit,
		money.New(2500, "BRL"),  // R$25.00
		money.New(10000, "BRL"), // antes: R$100.00
		money.New(12500, "BRL"), // depois: R$125.00
	)
	if err != nil {
		t.Fatalf("LedgerEntry correto nao deve falhar: %v", err)
	}
	if entry.ID() == uuid.Nil {
		t.Error("ID() nao deve ser nil")
	}
}

func TestLedgerEntry_DebitInvarianteCorreta(t *testing.T) {
	// R$100.00 - R$80.00 = R$20.00
	entry, err := wallet.NewWalletLedgerEntry(
		uuid.New(), uuid.New(),
		wallet.DirectionDebit,
		money.New(8000, "BRL"),  // R$80.00
		money.New(10000, "BRL"), // antes: R$100.00
		money.New(2000, "BRL"),  // depois: R$20.00
	)
	if err != nil {
		t.Fatalf("LedgerEntry de debito correto nao deve falhar: %v", err)
	}
	if entry.Direction() != wallet.DirectionDebit {
		t.Errorf("Direction() = %v, quer DEBIT", entry.Direction())
	}
}
