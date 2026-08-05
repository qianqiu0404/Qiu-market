package marketidentity

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	exchangeCodePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	assetCodePattern    = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]*$`)
	marketTypePattern   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// GenerateMarketCode builds the globally auditable market identity:
// <exchange-code>:<BASE>/<QUOTE>:<market-type>.
func GenerateMarketCode(exchangeCode, symbolName, marketType string) (string, error) {
	exchangeCode = strings.TrimSpace(exchangeCode)
	marketType = strings.ToLower(strings.TrimSpace(marketType))
	pair := strings.Split(strings.ToUpper(strings.TrimSpace(symbolName)), "/")
	if !exchangeCodePattern.MatchString(exchangeCode) {
		return "", fmt.Errorf("invalid exchange code %q", exchangeCode)
	}
	if len(pair) != 2 || !assetCodePattern.MatchString(pair[0]) || !assetCodePattern.MatchString(pair[1]) {
		return "", fmt.Errorf("invalid BASE/QUOTE symbol %q", symbolName)
	}
	if !marketTypePattern.MatchString(marketType) {
		return "", fmt.Errorf("invalid market type %q", marketType)
	}
	return fmt.Sprintf("%s:%s/%s:%s", exchangeCode, pair[0], pair[1], marketType), nil
}
