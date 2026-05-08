package textmatch

import "testing"

func TestContainsFoldsAccentsOnlyForAccentFreeQueries(t *testing.T) {
	tests := []struct {
		name  string
		value string
		query string
		want  bool
	}{
		{name: "plain query matches accented value", value: "José", query: "Jose", want: true},
		{name: "accented query matches accented value", value: "José", query: "José", want: true},
		{name: "accented query does not match plain value", value: "Jose", query: "José", want: false},
		{name: "single plain letter matches accented", value: "olá", query: "a", want: true},
		{name: "single accented letter does not match plain", value: "ola", query: "á", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Contains(test.value, test.query); got != test.want {
				t.Fatalf("Contains(%q, %q) = %v, want %v", test.value, test.query, got, test.want)
			}
		})
	}
}

func TestFindAllReturnsOriginalByteSpans(t *testing.T) {
	spans := FindAll("olá, ola", "ola")
	if len(spans) != 2 {
		t.Fatalf("FindAll() = %+v, want two spans", spans)
	}
	if got := "olá, ola"[spans[0].Start:spans[0].End]; got != "olá" {
		t.Fatalf("first span = %q, want olá", got)
	}
	if got := "olá, ola"[spans[1].Start:spans[1].End]; got != "ola" {
		t.Fatalf("second span = %q, want ola", got)
	}
}

func TestFuzzyScoreHybridMatches(t *testing.T) {
	tests := []struct {
		name  string
		value string
		query string
		want  bool
	}{
		{name: "ordered subsequence", value: "José Silva", query: "js", want: true},
		{name: "skips query spaces", value: "José Silva", query: "j s", want: true},
		{name: "accent folded subsequence", value: "J Otávio", query: "otv", want: true},
		{name: "adjacent transposition typo", value: "José Silva", query: "Jsoe", want: true},
		{name: "single missing rune typo", value: "Jonathan", query: "Jonatan", want: true},
		{name: "accented query remains accent sensitive", value: "Jose", query: "José", want: false},
		{name: "short typo does not match", value: "Al", query: "Ax", want: false},
		{name: "unrelated query", value: "Ana", query: "zz", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, got := FuzzyScore(test.value, test.query)
			if got != test.want {
				t.Fatalf("FuzzyScore(%q, %q) ok = %v, want %v", test.value, test.query, got, test.want)
			}
		})
	}
}

func TestFuzzyScoreRanksExactBeforeFuzzy(t *testing.T) {
	prefix, ok := FuzzyScore("José Silva", "jos")
	if !ok {
		t.Fatal("FuzzyScore(prefix) did not match")
	}
	substring, ok := FuzzyScore("Ana José", "jos")
	if !ok {
		t.Fatal("FuzzyScore(substring) did not match")
	}
	subsequence, ok := FuzzyScore("José Silva", "js")
	if !ok {
		t.Fatal("FuzzyScore(subsequence) did not match")
	}
	typo, ok := FuzzyScore("José Silva", "jsoe")
	if !ok {
		t.Fatal("FuzzyScore(typo) did not match")
	}
	if !(prefix < substring && substring < subsequence && subsequence < typo) {
		t.Fatalf("scores = prefix %d substring %d subsequence %d typo %d, want exact before fuzzy", prefix, substring, subsequence, typo)
	}
}
