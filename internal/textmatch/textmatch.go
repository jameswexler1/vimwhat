package textmatch

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Span struct {
	Start int
	End   int
}

func Contains(value, query string) bool {
	return len(FindAll(value, query)) > 0
}

func ContainsAny(values []string, query string) bool {
	for _, value := range values {
		if Contains(value, query) {
			return true
		}
	}
	return false
}

func FindAll(value, query string) []Span {
	query = strings.TrimSpace(query)
	if value == "" || query == "" {
		return nil
	}

	accentSensitive := HasDiacritics(query)
	valueRunes, valueSpans := normalizeForMatch(value, accentSensitive)
	queryRunes, _ := normalizeForMatch(query, accentSensitive)
	if len(queryRunes) == 0 || len(valueRunes) < len(queryRunes) {
		return nil
	}

	var spans []Span
	for i := 0; i <= len(valueRunes)-len(queryRunes); {
		if sameRunes(valueRunes[i:i+len(queryRunes)], queryRunes) {
			spans = append(spans, Span{
				Start: valueSpans[i].Start,
				End:   valueSpans[i+len(queryRunes)-1].End,
			})
			i += len(queryRunes)
			continue
		}
		i++
	}
	return spans
}

func HasDiacritics(value string) bool {
	for _, r := range norm.NFD.String(value) {
		if unicode.Is(unicode.Mn, r) {
			return true
		}
	}
	return false
}

func Fold(value string) string {
	runes, _ := normalizeForMatch(value, false)
	return string(runes)
}

func normalizeForMatch(value string, accentSensitive bool) ([]rune, []Span) {
	out := make([]rune, 0, len(value))
	spans := make([]Span, 0, len(value))

	for start, r := range value {
		end := start + len(string(r))
		decomposed := norm.NFD.String(string(r))
		for _, part := range decomposed {
			isMark := unicode.Is(unicode.Mn, part)
			if isMark && !accentSensitive {
				continue
			}
			if !isMark {
				part = unicode.ToLower(part)
			}
			out = append(out, part)
			spans = append(spans, Span{Start: start, End: end})
		}
	}

	return out, spans
}

func sameRunes(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
