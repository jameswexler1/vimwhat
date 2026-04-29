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
