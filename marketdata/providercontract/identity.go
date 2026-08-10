package providercontract

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var (
	slugPattern        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	assetSymbolPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]*$`)
	opaqueIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	signalKindPattern  = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
)

func NormalizeProviderID(value string) (ProviderID, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if !slugPattern.MatchString(normalized) {
		return "", NewError(
			ErrorInvalidIdentity, "", "normalize_provider",
			fmt.Errorf("invalid provider ID %q", value),
		)
	}
	return ProviderID(normalized), nil
}

func NormalizeProviderIdentity(value ProviderIdentity) (ProviderIdentity, error) {
	provider, err := NormalizeProviderID(string(value.ID))
	if err != nil {
		return ProviderIdentity{}, err
	}
	capabilities, err := NormalizeCapabilities(value.Capabilities)
	if err != nil {
		return ProviderIdentity{}, NewError(
			ErrorBadPayload, provider, "normalize_identity", err,
		)
	}
	if len(capabilities) == 0 {
		return ProviderIdentity{}, NewError(
			ErrorBadPayload, provider, "normalize_identity",
			fmt.Errorf("at least one capability is required"),
		)
	}
	value.ID = provider
	value.DisplayName = strings.TrimSpace(value.DisplayName)
	value.Capabilities = capabilities
	return value, nil
}

func NormalizeCapabilities(values []Capability) ([]Capability, error) {
	seen := make(map[Capability]struct{}, len(values))
	result := make([]Capability, 0, len(values))
	for _, value := range values {
		if !validCapability(value) {
			return nil, fmt.Errorf("unsupported capability %q", value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func NormalizeSourceRef(value SourceRef) (SourceRef, error) {
	provider, err := NormalizeProviderID(string(value.Provider))
	if err != nil {
		return SourceRef{}, err
	}
	value.Provider = provider
	value.Key = strings.ToLower(strings.TrimSpace(value.Key))
	value.SourceID = strings.TrimSpace(value.SourceID)
	value.URL = strings.TrimSpace(value.URL)
	if !slugPattern.MatchString(value.Key) {
		return SourceRef{}, NewError(
			ErrorInvalidIdentity, provider, "normalize_source",
			fmt.Errorf("invalid source key %q", value.Key),
		)
	}
	if value.SourceID == "" && value.URL == "" {
		return SourceRef{}, NewError(
			ErrorInvalidIdentity, provider, "normalize_source",
			fmt.Errorf("source_id or url is required"),
		)
	}
	if value.SourceID != "" && !validOpaqueText(value.SourceID, 512) {
		return SourceRef{}, NewError(
			ErrorInvalidIdentity, provider, "normalize_source",
			fmt.Errorf("invalid source ID"),
		)
	}
	if value.URL != "" {
		parsed, parseErr := url.Parse(value.URL)
		if parseErr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
			parsed.Host == "" || parsed.User != nil {
			return SourceRef{}, NewError(
				ErrorInvalidIdentity, provider, "normalize_source",
				fmt.Errorf("invalid auditable source URL"),
			)
		}
		parsed.Fragment = ""
		value.URL = parsed.String()
	}
	return value, nil
}

func NormalizeAsset(value Asset) (Asset, error) {
	value.ID = strings.ToLower(strings.TrimSpace(value.ID))
	value.Symbol = strings.ToUpper(strings.TrimSpace(value.Symbol))
	if !opaqueIDPattern.MatchString(value.ID) {
		return Asset{}, NewError(
			ErrorInvalidIdentity, "", "normalize_asset",
			fmt.Errorf("invalid asset ID %q", value.ID),
		)
	}
	if !assetSymbolPattern.MatchString(value.Symbol) {
		return Asset{}, NewError(
			ErrorInvalidIdentity, "", "normalize_asset",
			fmt.Errorf("invalid asset symbol %q", value.Symbol),
		)
	}
	return value, nil
}

func NormalizeMarket(value Market) (Market, error) {
	value.ID = strings.TrimSpace(value.ID)
	if !opaqueIDPattern.MatchString(value.ID) {
		return Market{}, NewError(
			ErrorInvalidIdentity, "", "normalize_market",
			fmt.Errorf("invalid market ID %q", value.ID),
		)
	}
	venue, err := NormalizeProviderID(value.Venue)
	if err != nil {
		return Market{}, err
	}
	base, err := NormalizeAsset(value.Base)
	if err != nil {
		return Market{}, err
	}
	quote, err := NormalizeAsset(value.Quote)
	if err != nil {
		return Market{}, err
	}
	if base.ID == quote.ID {
		return Market{}, NewError(
			ErrorInvalidIdentity, "", "normalize_market",
			fmt.Errorf("base and quote assets must differ"),
		)
	}
	if value.Type != MarketTypeSpot && value.Type != MarketTypePerp {
		return Market{}, NewError(
			ErrorInvalidIdentity, "", "normalize_market",
			fmt.Errorf("unsupported market type %q", value.Type),
		)
	}
	code := CanonicalMarketCode(string(venue), base.Symbol, quote.Symbol, value.Type)
	if strings.TrimSpace(value.Code) != "" && value.Code != code {
		return Market{}, NewError(
			ErrorConflict, "", "normalize_market",
			fmt.Errorf("market code %q conflicts with canonical %q", value.Code, code),
		)
	}
	value.Venue = string(venue)
	value.Base = base
	value.Quote = quote
	value.Code = code
	return value, nil
}

func CanonicalMarketCode(venue, base, quote string, marketType MarketType) string {
	return fmt.Sprintf(
		"%s:%s/%s:%s",
		strings.ToLower(strings.TrimSpace(venue)),
		strings.ToUpper(strings.TrimSpace(base)),
		strings.ToUpper(strings.TrimSpace(quote)),
		marketType,
	)
}

func NormalizeRequest(value Request) (Request, error) {
	value.Key = strings.TrimSpace(value.Key)
	if !validCapability(value.Capability) {
		return Request{}, NewError(
			ErrorUnsupported, "", "normalize_request",
			fmt.Errorf("unsupported capability %q", value.Capability),
		)
	}
	if value.Key == "" || !validOpaqueText(value.Key, 512) {
		return Request{}, payloadError("normalize_request", "key", "required or invalid")
	}
	parameters := append([]Parameter(nil), value.Parameters...)
	for index := range parameters {
		parameters[index].Key = strings.ToLower(strings.TrimSpace(parameters[index].Key))
		parameters[index].Value = strings.TrimSpace(parameters[index].Value)
		if !slugPattern.MatchString(parameters[index].Key) ||
			!validOpaqueText(parameters[index].Value, 512) {
			return Request{}, payloadError(
				"normalize_request", "parameters", "invalid key or value",
			)
		}
	}
	sort.Slice(parameters, func(i, j int) bool {
		if parameters[i].Key != parameters[j].Key {
			return parameters[i].Key < parameters[j].Key
		}
		return parameters[i].Value < parameters[j].Value
	})
	for index := 1; index < len(parameters); index++ {
		if parameters[index-1].Key == parameters[index].Key {
			return Request{}, NewError(
				ErrorConflict, "", "normalize_request",
				fmt.Errorf("duplicate parameter key %q", parameters[index].Key),
			)
		}
	}
	value.Parameters = parameters
	return value, nil
}

func validCapability(value Capability) bool {
	switch value {
	case CapabilitySpotTicker, CapabilityOHLCV, CapabilityDerivatives, CapabilitySignals:
		return true
	default:
		return false
	}
}

func validOpaqueText(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
