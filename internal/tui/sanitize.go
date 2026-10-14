package tui

import (
	"regexp"
	"strings"
)

var (
	// ansiRe matches terminal escape sequences: CSI (ESC [ ...
	// final byte), OSC (ESC ] ... BEL or ST), and the two-character
	// ESC sequences (ESC followed by a single control character).
	ansiRe = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
	// ctlRe matches what remains once the sequences are gone: C0
	// control characters except tab and newline, DEL, and the 8-bit
	// C1 range, which UTF-8 terminals can still interpret. The
	// carriage return is in here on purpose: a lone \r moves the
	// cursor to the start of the line and lets whatever follows
	// overwrite the display - CRLF pairs are normalized to \n
	// first so real line breaks survive.
	ctlRe = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f\x{0080}-\x{009f}]`)
)

// Sanitize strips terminal control characters and escape sequences
// from text before it reaches a terminal. Model output - answers,
// streamed reply text, tool-call names and arguments, backend
// error messages - is untrusted: a reply containing escape
// sequences could rewrite the screen or forge an approval prompt,
// and the operator would confirm a call the display invented.
// Visible text, line breaks, tabs, and legitimate Unicode are
// untouched.
func Sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return ctlRe.ReplaceAllString(ansiRe.ReplaceAllString(s, ""), "")
}
