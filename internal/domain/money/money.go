// Package money implementa o Value Object Money para representacao precisa
// de valores monetarios sem uso de ponto flutuante (float32/float64).
//
// Em DDD, um Value Object e definido pelo SEU VALOR, nao por identidade.
// Dois Money com mesmo amount e currency sao indistinguiveis.
// A imutabilidade e garantida em Go pelo fato de todos os metodos operarem
// sobre COPIAS do struct (receptor por valor, nao ponteiro) e retornarem
// novos valores em vez de modificar o receptor.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/shopspring/decimal"
)

// Erros de dominio para Money. Sao variaveis sentinela para uso com errors.Is.
// Envolvemos com fmt.Errorf("%w", Err...) para adicionar contexto sem perder
// a capacidade de comparacao via errors.Is.
var (
	ErrInvalidAmount    = errors.New("money: valor monetario invalido")
	ErrNegativeAmount   = errors.New("money: valor negativo nao permitido em entradas externas")
	ErrCurrencyMismatch = errors.New("money: moedas incompativeis")
	ErrInvalidCurrency  = errors.New("money: codigo de moeda ISO 4217 invalido")
	ErrScaleExceeded    = errors.New("money: escala excede 2 casas decimais")
	ErrOverflow         = errors.New("money: overflow aritmetico detectado")
	ErrEmptyAmount      = errors.New("money: valor nao pode ser vazio")
)

// Money e um Value Object imutavel que representa um valor monetario.
//
// REPRESENTACAO INTERNA: int64 em centavos (unidades minimas).
// Exemplos:
//   - R$ 25,00 -> amount = 2500
//   - R$ 100,00 -> amount = 10000
//   - R$ 0,01 -> amount = 1
//
// POR QUE int64 E NAO float64?
// float64 usa representacao binaria de ponto flutuante (IEEE 754).
// Numeros decimais como 0.1 nao tem representacao exata em binario.
// Resultado: 0.1 + 0.2 = 0.30000000000000004 (erro de precisao).
// Com int64 em centavos, 10 + 20 = 30 SEMPRE (aritmetica inteira exata).
//
// LIMITE: int64 suporta ate 9.223.372.036.854.775.807 centavos =
// R$ 92.233.720.368.547.758,07 (mais que suficiente para qualquer carteira).
type Money struct {
	// amount: valor em centavos. Pode ser negativo em calculos internos,
	// mas entradas externas (HTTP/SQS) DEVEM ser >= 0.
	amount int64

	// currency: codigo ISO 4217 normalizado em maiusculas (ex: "BRL").
	currency string
}

// Zero retorna um Money com valor zero para a moeda especificada.
// Util para inicializacao de saldos.
func Zero(currency string) Money {
	return Money{amount: 0, currency: strings.ToUpper(strings.TrimSpace(currency))}
}

// New cria um Money a partir de centavos (uso INTERNO — sem validacao de negatividade).
// Use esta funcao apenas dentro do dominio ou em testes.
// Para entrada externa (HTTP/SQS), sempre use Parse.
func New(amountCents int64, currency string) Money {
	return Money{amount: amountCents, currency: strings.ToUpper(strings.TrimSpace(currency))}
}

// Parse cria um Money a partir de uma string decimal e codigo de moeda.
// Esta e a UNICA porta de entrada para valores externos (HTTP/SQS boundary).
//
// Validacoes aplicadas (conforme spec):
//  1. Nao vazio ou apenas espacos
//  2. Sem notacao cientifica (ex: "1e2" e rejeitado)
//  3. Sem NaN ou Infinity
//  4. Nao negativo (entradas externas)
//  5. Maximo 2 casas decimais (sem arredondamento silencioso)
//  6. Codigo de moeda valido (3 letras maiusculas ISO 4217)
//
// shopspring/decimal e usado APENAS aqui no parsing — nunca em aritmetica interna.
func Parse(amountStr, currency string) (Money, error) {
	s := strings.TrimSpace(amountStr)
	if s == "" {
		return Money{}, ErrEmptyAmount
	}

	// Rejeitar notacao cientifica e valores especiais explicitamente.
	// decimal.NewFromString aceitaria "1e2" e o converteria para 100,
	// mas a spec exige rejeicao de notacao cientifica.
	lower := strings.ToLower(s)
	if lower == "nan" || lower == "infinity" || lower == "inf" || lower == "+inf" || lower == "-inf" {
		return Money{}, fmt.Errorf("%w: %q nao e um valor numerico valido", ErrInvalidAmount, s)
	}
	if strings.ContainsRune(lower, 'e') {
		return Money{}, fmt.Errorf("%w: notacao cientifica nao e permitida: %q", ErrInvalidAmount, s)
	}

	// Usar shopspring/decimal para parsing preciso (nao usa float internamente).
	d, err := decimal.NewFromString(s)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q — %s", ErrInvalidAmount, s, err.Error())
	}

	// Rejeitar valores negativos em entradas externas.
	if d.IsNegative() {
		return Money{}, fmt.Errorf("%w: %q", ErrNegativeAmount, s)
	}

	// Verificar escala: decimal.Exponent() retorna -N onde N e o numero de casas.
	// Ex: "25.00" -> Exponent = -2; "25.001" -> Exponent = -3 (rejeitado).
	// "25" e "25.0" sao aceitos (normalizados para "25.00" na saida).
	if d.Exponent() < -2 {
		return Money{}, fmt.Errorf("%w: %q tem %d casas decimais (maximo: 2)",
			ErrScaleExceeded, s, -d.Exponent())
	}

	// Converter para centavos: multiplicar por 100 usando aritmetica decimal.
	// Ex: "25.00" * 100 = 2500 (sem float).
	centsDec := d.Mul(decimal.NewFromInt(100))
	cents := centsDec.IntPart()

	cur, err := parseCurrency(currency)
	if err != nil {
		return Money{}, err
	}

	return Money{amount: cents, currency: cur}, nil
}

// parseCurrency normaliza e valida o codigo de moeda ISO 4217.
func parseCurrency(currency string) (string, error) {
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if len(cur) != 3 {
		return "", fmt.Errorf("%w: %q deve ter exatamente 3 letras", ErrInvalidCurrency, currency)
	}
	for _, c := range cur {
		if c < 'A' || c > 'Z' {
			return "", fmt.Errorf("%w: %q contem caracteres invalidos", ErrInvalidCurrency, currency)
		}
	}
	return cur, nil
}

// Amount retorna o valor em centavos (uso interno/persistencia).
func (m Money) Amount() int64 { return m.amount }

// Currency retorna o codigo ISO 4217 da moeda.
func (m Money) Currency() string { return m.currency }

// IsZero retorna true se o valor for zero.
func (m Money) IsZero() bool { return m.amount == 0 }

// IsPositive retorna true se o valor for estritamente maior que zero.
func (m Money) IsPositive() bool { return m.amount > 0 }

// IsNegative retorna true se o valor for menor que zero.
// Valores negativos sao permitidos apenas em calculos internos e diferencas.
func (m Money) IsNegative() bool { return m.amount < 0 }

// Equal compara igualdade entre dois Money.
// Retorna ErrCurrencyMismatch se as moedas forem diferentes.
// Operacoes entre moedas diferentes sao sempre um erro de dominio.
func (m Money) Equal(other Money) (bool, error) {
	if m.currency != other.currency {
		return false, fmt.Errorf("%w: %s != %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	return m.amount == other.amount, nil
}

// GreaterThan retorna (m > other).
func (m Money) GreaterThan(other Money) (bool, error) {
	if m.currency != other.currency {
		return false, fmt.Errorf("%w: %s != %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	return m.amount > other.amount, nil
}

// GreaterThanOrEqual retorna (m >= other).
func (m Money) GreaterThanOrEqual(other Money) (bool, error) {
	if m.currency != other.currency {
		return false, fmt.Errorf("%w: %s != %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	return m.amount >= other.amount, nil
}

// LessThan retorna (m < other).
func (m Money) LessThan(other Money) (bool, error) {
	if m.currency != other.currency {
		return false, fmt.Errorf("%w: %s != %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	return m.amount < other.amount, nil
}

// Add soma dois Money e retorna um NOVO Money (imutabilidade preservada).
// Detecta overflow de int64 para garantir seguranca numerica.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("%w: %s != %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	// Verificar overflow antes da operacao.
	// Se other.amount > 0 e m.amount > MaxInt64 - other.amount, ocorreria overflow positivo.
	// Se other.amount < 0 e m.amount < MinInt64 - other.amount, ocorreria overflow negativo.
	if other.amount > 0 && m.amount > math.MaxInt64-other.amount {
		return Money{}, fmt.Errorf("%w: %d + %d excede int64", ErrOverflow, m.amount, other.amount)
	}
	if other.amount < 0 && m.amount < math.MinInt64-other.amount {
		return Money{}, fmt.Errorf("%w: %d + %d subflui int64", ErrOverflow, m.amount, other.amount)
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

// Sub subtrai other de m e retorna um NOVO Money.
func (m Money) Sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("%w: %s != %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	// MinInt64 nao pode ser negado sem overflow: -MinInt64 > MaxInt64.
	if other.amount == math.MinInt64 {
		return Money{}, fmt.Errorf("%w: nao e possivel negar MinInt64", ErrOverflow)
	}
	// Reusar Add com o valor negado (evita duplicacao de logica de overflow).
	return m.Add(Money{amount: -other.amount, currency: other.currency})
}

// Neg retorna o negativo do Money (ex: 2500 -> -2500).
// Util para calcular diferencas em reversoes (ROLLBACK, REFUND).
func (m Money) Neg() (Money, error) {
	if m.amount == math.MinInt64 {
		return Money{}, fmt.Errorf("%w: nao e possivel negar MinInt64", ErrOverflow)
	}
	return Money{amount: -m.amount, currency: m.currency}, nil
}

// String retorna a representacao decimal com exatamente 2 casas decimais.
// Ex: Money{amount: 2500, currency: "BRL"}.String() == "25.00"
// Ex: Money{amount: -100, currency: "BRL"}.String() == "-1.00"
func (m Money) String() string {
	neg := m.amount < 0
	abs := m.amount
	if neg {
		abs = -abs
	}
	whole := abs / 100
	cents := abs % 100
	s := fmt.Sprintf("%d.%02d", whole, cents)
	if neg {
		return "-" + s
	}
	return s
}

// moneyJSON e a estrutura intermediaria para serializar/desserializar JSON.
type moneyJSON struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// MarshalJSON serializa Money como {"amount":"25.00","currency":"BRL"}.
// NUNCA usa float — o valor e formatado como string decimal.
// O contrato externo (HTTP/SQS) sempre usa strings para valores monetarios.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(moneyJSON{
		Amount:   m.String(),
		Currency: m.currency,
	})
}

// UnmarshalJSON desserializa {"amount":"25.00","currency":"BRL"}.
// Delega para Parse, aplicando todas as validacoes de entrada.
// Um JSON invalido ou com valor fora das regras sera rejeitado com erro claro.
func (m *Money) UnmarshalJSON(data []byte) error {
	var raw moneyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%w: json invalido para Money — %s", ErrInvalidAmount, err.Error())
	}
	parsed, err := Parse(raw.Amount, raw.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
