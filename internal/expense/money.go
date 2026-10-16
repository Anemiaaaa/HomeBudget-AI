package expense

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const maxAmountKopecks int64 = 999_999_999_999

type Money struct {
	kopecks int64
}

func ParseMoney(raw string) (Money, error) {
	return parseMoney(raw, false)
}

func ParseMoneyOrZero(raw string) (Money, error) {
	return parseMoney(raw, true)
}

func parseMoney(raw string, allowZero bool) (Money, error) {
	value := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		if r == ',' {
			return '.'
		}
		return r
	}, strings.TrimSpace(raw))

	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return Money{}, errors.New("amount must be a positive decimal number")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return Money{}, errors.New("amount must use a decimal format")
	}
	if !digitsOnly(parts[0]) || (len(parts) == 2 && !digitsOnly(parts[1])) {
		return Money{}, errors.New("amount contains unsupported characters")
	}
	if len(parts) == 2 && len(parts[1]) > 2 {
		return Money{}, errors.New("amount must have no more than two decimal places")
	}

	rubles, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || rubles > maxAmountKopecks/100 {
		return Money{}, errors.New("amount is too large")
	}
	var fractional int64
	if len(parts) == 2 {
		fractionText := parts[1]
		if len(fractionText) == 1 {
			fractionText += "0"
		}
		fractional, err = strconv.ParseInt(fractionText, 10, 64)
		if err != nil {
			return Money{}, errors.New("amount has an invalid fractional part")
		}
	}

	kopecks := rubles*100 + fractional
	minimum := int64(1)
	if allowZero {
		minimum = 0
	}
	if kopecks < minimum || kopecks > maxAmountKopecks {
		return Money{}, fmt.Errorf("amount must be between 0.01 and %s", Money{kopecks: maxAmountKopecks}.String())
	}
	return Money{kopecks: kopecks}, nil
}

func MoneyFromKopecks(kopecks int64) (Money, error) {
	if kopecks < 0 || kopecks > maxAmountKopecks {
		return Money{}, errors.New("kopecks value is outside the supported range")
	}
	return Money{kopecks: kopecks}, nil
}

func (m Money) Kopecks() int64 { return m.kopecks }

func (m Money) String() string {
	return fmt.Sprintf("%d.%02d", m.kopecks/100, m.kopecks%100)
}

func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

func digitsOnly(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}
