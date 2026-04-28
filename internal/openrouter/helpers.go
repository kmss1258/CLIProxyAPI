package openrouter

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	ProviderName   = "openrouter"
	DefaultBaseURL = "https://openrouter.ai/api/v1"
)

func IsProviderName(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), ProviderName)
}

func IsOpenRouterCompat(provider, compatName, providerKey string) bool {
	return IsProviderName(provider) || IsProviderName(compatName) || IsProviderName(providerKey)
}

func NormalizeUSDString(raw string) string {
	micros, ok := ParseUSDMicrosString(raw)
	if !ok {
		return ""
	}
	return FormatUSDMicros(micros)
}

func ParseUSDMicrosAny(value any) (int64, bool) {
	switch typed := value.(type) {
	case nil:
		return 0, false
	case string:
		return ParseUSDMicrosString(typed)
	case json.Number:
		return ParseUSDMicrosString(typed.String())
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		return int64(math.Round(typed * 1_000_000)), true
	case float32:
		return ParseUSDMicrosAny(float64(typed))
	case int:
		return int64(typed) * 1_000_000, true
	case int64:
		return typed * 1_000_000, true
	case int32:
		return int64(typed) * 1_000_000, true
	case uint:
		return int64(typed) * 1_000_000, true
	case uint64:
		if typed > math.MaxInt64/1_000_000 {
			return 0, false
		}
		return int64(typed) * 1_000_000, true
	default:
		return 0, false
	}
}

func ParseUSDMicrosString(raw string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	negative := false
	if strings.HasPrefix(value, "-") {
		negative = true
		value = strings.TrimPrefix(value, "-")
	}
	parts := strings.SplitN(value, ".", 2)
	wholeText := strings.TrimSpace(parts[0])
	if wholeText == "" {
		wholeText = "0"
	}
	whole, err := strconv.ParseInt(wholeText, 10, 64)
	if err != nil {
		return 0, false
	}
	frac := int64(0)
	if len(parts) == 2 {
		fractionText := strings.TrimSpace(parts[1])
		for _, ch := range fractionText {
			if ch < '0' || ch > '9' {
				return 0, false
			}
		}
		if len(fractionText) > 6 {
			fractionText = fractionText[:6]
		}
		for len(fractionText) < 6 {
			fractionText += "0"
		}
		if fractionText != "" {
			frac, err = strconv.ParseInt(fractionText, 10, 64)
			if err != nil {
				return 0, false
			}
		}
	}
	result := whole*1_000_000 + frac
	if negative {
		result = -result
	}
	return result, true
}

func FormatUSDMicros(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	whole := value / 1_000_000
	fraction := value % 1_000_000
	if fraction == 0 {
		if negative {
			return fmt.Sprintf("-%d", whole)
		}
		return strconv.FormatInt(whole, 10)
	}
	fractionText := strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
	if negative {
		return fmt.Sprintf("-%d.%s", whole, fractionText)
	}
	return fmt.Sprintf("%d.%s", whole, fractionText)
}
