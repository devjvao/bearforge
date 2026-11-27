package domain

import (
	"strconv"
	"unicode"
	"unicode/utf8"
)

// DisplayRef makes bytes and invisible characters that a terminal could hide
// explicit, while keeping printable branch names readable.
func DisplayRef(ref string) string {
	if !utf8.ValidString(ref) {
		return strconv.QuoteToASCII(ref)
	}
	for _, r := range ref {
		if !unicode.IsPrint(r) {
			return strconv.QuoteToASCII(ref)
		}
	}
	return ref
}
