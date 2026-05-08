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

func FuzzyScore(value, query string) (int, bool) {
	query = strings.TrimSpace(query)
	if value == "" || query == "" {
		return 0, false
	}

	accentSensitive := HasDiacritics(query)
	valueRunes, _ := normalizeForMatch(value, accentSensitive)
	queryRunes, _ := normalizeForMatch(query, accentSensitive)
	queryRunes = stripSpaces(queryRunes)
	if len(valueRunes) == 0 || len(queryRunes) == 0 {
		return 0, false
	}

	if score, ok := contiguousScore(valueRunes, queryRunes); ok {
		return score, true
	}
	if score, ok := subsequenceScore(valueRunes, queryRunes); ok {
		return 1000 + score, true
	}
	if !accentSensitive {
		if score, ok := typoScore(valueRunes, queryRunes); ok {
			return 2000 + score, true
		}
	}
	return 0, false
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

func stripSpaces(runes []rune) []rune {
	out := runes[:0]
	for _, r := range runes {
		if unicode.IsSpace(r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func contiguousScore(valueRunes, queryRunes []rune) (int, bool) {
	if len(valueRunes) < len(queryRunes) {
		return 0, false
	}
	best := -1
	for i := 0; i <= len(valueRunes)-len(queryRunes); i++ {
		if !sameRunes(valueRunes[i:i+len(queryRunes)], queryRunes) {
			continue
		}
		score := i
		if i > 0 {
			if isWordBoundary(valueRunes, i) {
				score += 50
			} else {
				score += 150
			}
		}
		if best < 0 || score < best {
			best = score
		}
	}
	return best, best >= 0
}

func subsequenceScore(valueRunes, queryRunes []rune) (int, bool) {
	if len(valueRunes) < len(queryRunes) {
		return 0, false
	}
	best := -1
	for start, r := range valueRunes {
		if r != queryRunes[0] {
			continue
		}
		queryIndex := 1
		previous := start
		gaps := 0
		for i := start + 1; i < len(valueRunes) && queryIndex < len(queryRunes); i++ {
			if valueRunes[i] != queryRunes[queryIndex] {
				continue
			}
			gaps += i - previous - 1
			previous = i
			queryIndex++
		}
		if queryIndex != len(queryRunes) {
			continue
		}
		score := start + gaps*10
		if start > 0 {
			if isWordBoundary(valueRunes, start) {
				score += 50
			} else {
				score += 150
			}
		}
		if best < 0 || score < best {
			best = score
		}
	}
	return best, best >= 0
}

func typoScore(valueRunes, queryRunes []rune) (int, bool) {
	queryRunes = compactAlphaNumeric(queryRunes)
	if len(queryRunes) < 3 {
		return 0, false
	}
	best := -1
	for _, term := range fuzzyTerms(valueRunes) {
		if abs(len(term.runes)-len(queryRunes)) > 1 {
			continue
		}
		distance, ok := editDistanceAtMost(term.runes, queryRunes, 1)
		if !ok {
			continue
		}
		score := term.start + distance*100
		if best < 0 || score < best {
			best = score
		}
	}
	return best, best >= 0
}

type fuzzyTerm struct {
	runes []rune
	start int
}

func fuzzyTerms(valueRunes []rune) []fuzzyTerm {
	var terms []fuzzyTerm
	start := -1
	for i, r := range valueRunes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			terms = append(terms, fuzzyTerm{runes: valueRunes[start:i], start: start})
			start = -1
		}
	}
	if start >= 0 {
		terms = append(terms, fuzzyTerm{runes: valueRunes[start:], start: start})
	}
	return terms
}

func compactAlphaNumeric(runes []rune) []rune {
	out := runes[:0]
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		}
	}
	return out
}

func editDistanceAtMost(left, right []rune, maxDistance int) (int, bool) {
	if abs(len(left)-len(right)) > maxDistance {
		return 0, false
	}
	if sameRunes(left, right) {
		return 0, true
	}
	if maxDistance < 1 {
		return 0, false
	}
	if isSingleInsertDeleteOrReplace(left, right) || isAdjacentTransposition(left, right) {
		return 1, true
	}
	return 0, false
}

func isSingleInsertDeleteOrReplace(left, right []rune) bool {
	if len(left) == len(right) {
		differences := 0
		for i := range left {
			if left[i] == right[i] {
				continue
			}
			differences++
			if differences > 1 {
				return false
			}
		}
		return differences == 1
	}
	if len(left)+1 == len(right) {
		return isSingleMissingRune(left, right)
	}
	if len(right)+1 == len(left) {
		return isSingleMissingRune(right, left)
	}
	return false
}

func isSingleMissingRune(shorter, longer []rune) bool {
	i, j := 0, 0
	skipped := false
	for i < len(shorter) && j < len(longer) {
		if shorter[i] == longer[j] {
			i++
			j++
			continue
		}
		if skipped {
			return false
		}
		skipped = true
		j++
	}
	return true
}

func isAdjacentTransposition(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for i := 0; i < len(left)-1; i++ {
		if left[i] == right[i] {
			continue
		}
		if left[i] != right[i+1] || left[i+1] != right[i] {
			return false
		}
		for j := i + 2; j < len(left); j++ {
			if left[j] != right[j] {
				return false
			}
		}
		return true
	}
	return false
}

func isWordBoundary(runes []rune, index int) bool {
	if index <= 0 {
		return true
	}
	previous := runes[index-1]
	return !unicode.IsLetter(previous) && !unicode.IsDigit(previous)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
