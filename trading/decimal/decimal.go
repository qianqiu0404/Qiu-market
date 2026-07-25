package decimal

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var ErrInvalidDecimal = errors.New("invalid non-negative decimal")

// Parse converts a base-10 decimal string into integer atoms. Scale must be a
// positive power of ten. Exponents, signs, whitespace and silent rounding are
// deliberately rejected at the API boundary.
func Parse(value string, scale int64) (int64, error) {
	precision, err := scalePrecision(scale)
	if err != nil {
		return 0, err
	}
	if value == "" || strings.TrimSpace(value) != value ||
		strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, fmt.Errorf("%w: %q", ErrInvalidDecimal, value)
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("%w: %q", ErrInvalidDecimal, value)
	}
	if !digitsOnly(parts[0]) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidDecimal, value)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || !digitsOnly(fraction) || len(fraction) > precision {
			return 0, fmt.Errorf("%w: %q exceeds scale %d", ErrInvalidDecimal, value, scale)
		}
	}

	whole, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || whole > uint64(math.MaxInt64)/uint64(scale) {
		return 0, fmt.Errorf("%w: %q overflows", ErrInvalidDecimal, value)
	}
	atoms := whole * uint64(scale)
	if fraction != "" {
		padded := fraction + strings.Repeat("0", precision-len(fraction))
		fractionAtoms, parseErr := strconv.ParseUint(padded, 10, 64)
		if parseErr != nil || atoms > uint64(math.MaxInt64)-fractionAtoms {
			return 0, fmt.Errorf("%w: %q overflows", ErrInvalidDecimal, value)
		}
		atoms += fractionAtoms
	}
	return int64(atoms), nil
}

// Format converts non-negative atoms to a canonical decimal string without
// scientific notation or unnecessary trailing zeroes.
func Format(atoms, scale int64) (string, error) {
	precision, err := scalePrecision(scale)
	if err != nil {
		return "", err
	}
	if atoms < 0 {
		return "", fmt.Errorf("%w: atoms must be non-negative", ErrInvalidDecimal)
	}
	whole := atoms / scale
	if precision == 0 {
		return strconv.FormatInt(whole, 10), nil
	}
	fraction := atoms % scale
	if fraction == 0 {
		return strconv.FormatInt(whole, 10), nil
	}
	result := fmt.Sprintf("%d.%0*d", whole, precision, fraction)
	return strings.TrimRight(result, "0"), nil
}

func scalePrecision(scale int64) (int, error) {
	if scale <= 0 {
		return 0, fmt.Errorf("%w: scale must be a positive power of ten", ErrInvalidDecimal)
	}
	precision := 0
	for scale > 1 {
		if scale%10 != 0 {
			return 0, fmt.Errorf("%w: scale must be a positive power of ten", ErrInvalidDecimal)
		}
		scale /= 10
		precision++
	}
	return precision, nil
}

func digitsOnly(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}
