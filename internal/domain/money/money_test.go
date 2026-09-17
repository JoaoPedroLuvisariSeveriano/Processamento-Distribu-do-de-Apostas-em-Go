package money_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
)

// =============================================================================
// Testes de Parse (entrada externa)
// =============================================================================

func TestParse_Valido(t *testing.T) {
	casos := []struct {
		input    string
		currency string
		wantCents int64
	}{
		{"25.00", "BRL", 2500},
		{"0.00", "BRL", 0},
		{"1000.00", "BRL", 100000},
		{"0.01", "BRL", 1},
		{"99.99", "BRL", 9999},
		{"100", "BRL", 10000},   // sem casas decimais — valido
		{"50.5", "BRL", 5050},   // 1 casa decimal — valido
		{"  25.00  ", "BRL", 2500}, // espacos — normalizados
		{"25.00", "usd", 2500},  // moeda minuscula — normalizada
	}

	for _, tc := range casos {
		t.Run(tc.input+"/"+tc.currency, func(t *testing.T) {
			m, err := money.Parse(tc.input, tc.currency)
			if err != nil {
				t.Fatalf("Parse(%q, %q) erro inesperado: %v", tc.input, tc.currency, err)
			}
			if m.Amount() != tc.wantCents {
				t.Errorf("Amount() = %d, quer %d", m.Amount(), tc.wantCents)
			}
		})
	}
}

func TestParse_Invalido(t *testing.T) {
	casos := []struct {
		input    string
		currency string
		wantErr  error
	}{
		// Valor vazio
		{"", "BRL", money.ErrEmptyAmount},
		{"   ", "BRL", money.ErrEmptyAmount},
		// Valor negativo
		{"-1.00", "BRL", money.ErrNegativeAmount},
		{"-0.01", "BRL", money.ErrNegativeAmount},
		// Escala excedida
		{"25.001", "BRL", money.ErrScaleExceeded},
		{"0.123", "BRL", money.ErrScaleExceeded},
		// Notacao cientifica
		{"1e2", "BRL", money.ErrInvalidAmount},
		{"1E+2", "BRL", money.ErrInvalidAmount},
		// NaN / Infinity
		{"NaN", "BRL", money.ErrInvalidAmount},
		{"Infinity", "BRL", money.ErrInvalidAmount},
		{"inf", "BRL", money.ErrInvalidAmount},
		// Texto invalido
		{"abc", "BRL", money.ErrInvalidAmount},
		{"25,00", "BRL", money.ErrInvalidAmount}, // virgula nao e separador decimal
		// Moeda invalida
		{"25.00", "BR", money.ErrInvalidCurrency},
		{"25.00", "BRLX", money.ErrInvalidCurrency},
		{"25.00", "12A", money.ErrInvalidCurrency},
		{"25.00", "", money.ErrInvalidCurrency},
	}

	for _, tc := range casos {
		t.Run(tc.input+"/"+tc.currency, func(t *testing.T) {
			_, err := money.Parse(tc.input, tc.currency)
			if err == nil {
				t.Fatalf("Parse(%q, %q) deveria retornar erro, mas nao retornou", tc.input, tc.currency)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Parse(%q, %q) erro = %v, quer Is(%v)", tc.input, tc.currency, err, tc.wantErr)
			}
		})
	}
}

// =============================================================================
// Testes de Add e Sub
// =============================================================================

func TestAdd_Sucesso(t *testing.T) {
	m1 := money.New(2500, "BRL") // R$25.00
	m2 := money.New(7500, "BRL") // R$75.00

	resultado, err := m1.Add(m2)
	if err != nil {
		t.Fatalf("Add() erro inesperado: %v", err)
	}
	if resultado.Amount() != 10000 {
		t.Errorf("Add() = %d, quer 10000", resultado.Amount())
	}
}

func TestAdd_MoedasDiferentes(t *testing.T) {
	m1 := money.New(2500, "BRL")
	m2 := money.New(2500, "USD")

	_, err := m1.Add(m2)
	if !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("Add() com moedas diferentes deveria retornar ErrCurrencyMismatch, got: %v", err)
	}
}

func TestAdd_Overflow(t *testing.T) {
	// Verificar que overflow e detectado antes de causar corrupcao silenciosa.
	m1 := money.New(math.MaxInt64, "BRL")
	m2 := money.New(1, "BRL")

	_, err := m1.Add(m2)
	if !errors.Is(err, money.ErrOverflow) {
		t.Errorf("Add() com MaxInt64+1 deveria retornar ErrOverflow, got: %v", err)
	}
}

func TestSub_Sucesso(t *testing.T) {
	m1 := money.New(10000, "BRL") // R$100.00
	m2 := money.New(2500, "BRL")  // R$25.00

	resultado, err := m1.Sub(m2)
	if err != nil {
		t.Fatalf("Sub() erro inesperado: %v", err)
	}
	// R$100.00 - R$25.00 = R$75.00 = 7500 centavos
	if resultado.Amount() != 7500 {
		t.Errorf("Sub() = %d, quer 7500", resultado.Amount())
	}
}

func TestSub_ResultadoNegativo(t *testing.T) {
	// Subtracao pode resultar em negativo em calculos internos (ex: diferenca de rollback)
	m1 := money.New(2500, "BRL")
	m2 := money.New(10000, "BRL")

	resultado, err := m1.Sub(m2)
	if err != nil {
		t.Fatalf("Sub() erro inesperado: %v", err)
	}
	if resultado.Amount() != -7500 {
		t.Errorf("Sub() = %d, quer -7500", resultado.Amount())
	}
	if !resultado.IsNegative() {
		t.Error("IsNegative() deveria ser true")
	}
}

// =============================================================================
// Testes de comparacao
// =============================================================================

func TestEqual(t *testing.T) {
	m1 := money.New(2500, "BRL")
	m2 := money.New(2500, "BRL")
	m3 := money.New(3000, "BRL")

	eq, err := m1.Equal(m2)
	if err != nil || !eq {
		t.Errorf("Equal(m1, m2) = (%v, %v), quer (true, nil)", eq, err)
	}

	eq, err = m1.Equal(m3)
	if err != nil || eq {
		t.Errorf("Equal(m1, m3) = (%v, %v), quer (false, nil)", eq, err)
	}
}

func TestGreaterThan(t *testing.T) {
	m100 := money.New(10000, "BRL")
	m80 := money.New(8000, "BRL")

	gt, err := m100.GreaterThan(m80)
	if err != nil || !gt {
		t.Errorf("100 > 80 deveria ser true, got (%v, %v)", gt, err)
	}

	gt, err = m80.GreaterThan(m100)
	if err != nil || gt {
		t.Errorf("80 > 100 deveria ser false, got (%v, %v)", gt, err)
	}
}

// =============================================================================
// Testes de String e Neg
// =============================================================================

func TestString(t *testing.T) {
	casos := []struct {
		cents int64
		want  string
	}{
		{2500, "25.00"},
		{100000, "1000.00"},
		{1, "0.01"},
		{0, "0.00"},
		{-2500, "-25.00"},
		{-1, "-0.01"},
	}

	for _, tc := range casos {
		m := money.New(tc.cents, "BRL")
		if got := m.String(); got != tc.want {
			t.Errorf("New(%d).String() = %q, quer %q", tc.cents, got, tc.want)
		}
	}
}

func TestNeg(t *testing.T) {
	m := money.New(2500, "BRL")
	neg, err := m.Neg()
	if err != nil {
		t.Fatalf("Neg() erro inesperado: %v", err)
	}
	if neg.Amount() != -2500 {
		t.Errorf("Neg() = %d, quer -2500", neg.Amount())
	}

	// Negar MinInt64 deve retornar overflow
	minMoney := money.New(math.MinInt64, "BRL")
	_, err = minMoney.Neg()
	if !errors.Is(err, money.ErrOverflow) {
		t.Errorf("Neg(MinInt64) deveria retornar ErrOverflow, got: %v", err)
	}
}

// =============================================================================
// Testes de JSON
// =============================================================================

func TestMarshalJSON(t *testing.T) {
	m := money.New(2500, "BRL")
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("MarshalJSON() erro: %v", err)
	}
	got := string(data)
	want := `{"amount":"25.00","currency":"BRL"}`
	if got != want {
		t.Errorf("MarshalJSON() = %q, quer %q", got, want)
	}
}

func TestUnmarshalJSON_Valido(t *testing.T) {
	jsonStr := `{"amount":"25.00","currency":"BRL"}`
	var m money.Money
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		t.Fatalf("UnmarshalJSON() erro inesperado: %v", err)
	}
	if m.Amount() != 2500 {
		t.Errorf("Amount() = %d, quer 2500", m.Amount())
	}
	if m.Currency() != "BRL" {
		t.Errorf("Currency() = %q, quer BRL", m.Currency())
	}
}

func TestUnmarshalJSON_Invalido(t *testing.T) {
	// Valores negativos devem ser rejeitados
	jsonStr := `{"amount":"-1.00","currency":"BRL"}`
	var m money.Money
	err := json.Unmarshal([]byte(jsonStr), &m)
	if !errors.Is(err, money.ErrNegativeAmount) {
		t.Errorf("UnmarshalJSON(%q) deveria retornar ErrNegativeAmount, got: %v", jsonStr, err)
	}

	// Escala invalida deve ser rejeitada
	jsonStr2 := `{"amount":"25.001","currency":"BRL"}`
	err = json.Unmarshal([]byte(jsonStr2), &m)
	if !errors.Is(err, money.ErrScaleExceeded) {
		t.Errorf("UnmarshalJSON(%q) deveria retornar ErrScaleExceeded, got: %v", jsonStr2, err)
	}
}

func TestIsZero(t *testing.T) {
	if !money.Zero("BRL").IsZero() {
		t.Error("Zero(BRL).IsZero() deveria ser true")
	}
	if money.New(1, "BRL").IsZero() {
		t.Error("New(1, BRL).IsZero() deveria ser false")
	}
}

// TestImutabilidade verifica que operacoes nao modificam os operandos originais.
// Este e um teste fundamental de Value Object: imutabilidade.
func TestImutabilidade(t *testing.T) {
	original := money.New(2500, "BRL")
	adicionado := money.New(500, "BRL")

	resultado, _ := original.Add(adicionado)

	// O original nao deve ser modificado
	if original.Amount() != 2500 {
		t.Errorf("Add() modificou o receptor original: Amount() = %d, quer 2500", original.Amount())
	}
	// O resultado e um novo Money
	if resultado.Amount() != 3000 {
		t.Errorf("Add() resultado incorreto: Amount() = %d, quer 3000", resultado.Amount())
	}
}
