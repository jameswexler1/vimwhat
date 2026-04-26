//go:build windows

package commandline

import (
	"fmt"
	"strings"
)

func Split(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	inQuotes := false
	backslashes := 0

	flushBackslashes := func() {
		if backslashes == 0 {
			return
		}
		current.WriteString(strings.Repeat(`\`, backslashes))
		backslashes = 0
	}
	flushArg := func() {
		if current.Len() == 0 {
			return
		}
		args = append(args, current.String())
		current.Reset()
	}

	for _, r := range input {
		switch r {
		case '\\':
			backslashes++
			continue
		case '"':
			current.WriteString(strings.Repeat(`\`, backslashes/2))
			if backslashes%2 == 0 {
				inQuotes = !inQuotes
			} else {
				current.WriteRune('"')
			}
			backslashes = 0
			continue
		case ' ', '\t', '\n':
			flushBackslashes()
			if inQuotes {
				current.WriteRune(r)
				continue
			}
			flushArg()
			continue
		default:
			flushBackslashes()
			current.WriteRune(r)
		}
	}
	flushBackslashes()
	if inQuotes {
		return nil, fmt.Errorf("unterminated quote in command")
	}
	flushArg()

	return args, nil
}
