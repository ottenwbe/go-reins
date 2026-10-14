package tui

import "regexp"

var (
	// ansiRe matches terminal escape sequences: CSI (ESC [ ...
	// final byte), OSC (ESC ] ... BEL or ST), and the two-character
	// ESC sequences (ESC followed by one control character).
	ansiRe = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
	// ctlRe matches what remains once the sequences are gone: C0
	// control characters except tab, newline, and carriage return,
	// DEL, and the 8-bit C1 range, which UTF-8 terminals can still
	// interpret.
	ctlRe = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f\x{0080}-\x{009f}]`)
)

// Sanitize strips terminal control characters and escape sequences
// from text before it reaches a terminal. Model output - answers,
// streamed reply text, tool-call names and arguments, backend
// error messages - is untrusted: a reply containing escape
// sequences could rewrite the screen or forge an approval prompt,
// and the operator would confirm a call the display invented.
// Visible text, newlines, tabs, and legitimate Unicode are
// untouched.
func Sanitize(s string) string {
	return ctlRe.ReplaceAllString(ansiRe.ReplaceAllString(s, ""), "")
}
