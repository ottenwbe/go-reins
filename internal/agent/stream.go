// Streaming support for the agent loop: an observer that receives
// reply text as it arrives, and the gate that keeps the tool-call
// protocol out of what the operator sees.
package agent

import "strings"

// StreamObserver receives the model's reply text as it arrives,
// delta by delta. It only fires when the backend implements
// backend.Streamer and an observer was installed with
// WithStreamObserver. Deltas are pre-filtered: text that is (or
// could still become) a TOOLCALL line never reaches the observer -
// the operator sees prose, and the tool event covers the call.
type StreamObserver func(delta string)

// WithStreamObserver installs a listener for streamed reply text.
// It takes effect on every turn of every Run and Step; a backend
// without the streaming capability silently ignores it and a nil
// observer is not installed.
func WithStreamObserver(o StreamObserver) Option {
	return func(a *Agent) {
		if o != nil {
			a.stream = o
		}
	}
}

// streamGate filters reply deltas on their way to the observer.
// The agent's text protocol lets the model answer with a single
// TOOLCALL line (see toolCallPattern, which anchors the marker to
// the line start); such a line is protocol, not prose, so it is
// held back whole and dropped. Everything else streams through as
// soon as it can no longer be the marker.
//
// The gate tracks one line at a time. While the pending line is a
// prefix of "TOOLCALL" - or has the marker and is still forming -
// deltas are buffered. The moment a line diverges from the marker,
// the buffered text is flushed and the rest of that line streams
// unbuffered. A completed line is dropped when it matches the tool
// call pattern, and emitted with its newline otherwise.
type streamGate struct {
	observe func(string)
	line    strings.Builder // pending text of the current line
	holding bool            // the line could still be (or is) a TOOLCALL line
}

func newStreamGate(observe func(string)) *streamGate {
	return &streamGate{observe: observe, holding: true}
}

// push passes one delta through the gate.
func (g *streamGate) push(delta string) {
	for len(delta) > 0 {
		if !g.holding {
			// Known prose: stream through, and start over at the
			// next newline.
			if i := strings.IndexByte(delta, '\n'); i >= 0 {
				g.observe(delta[:i+1])
				delta = delta[i+1:]
				g.holding = true
			} else {
				g.observe(delta)
				return
			}
			continue
		}

		// The line may still become a tool call. Buffer up to the
		// newline, then settle the whole line at once.
		if i := strings.IndexByte(delta, '\n'); i >= 0 {
			g.line.WriteString(delta[:i])
			g.settleLine()
			delta = delta[i+1:]
			continue
		}
		g.line.WriteString(delta)

		// Not the marker after all: flush and stream the rest of
		// this line unbuffered.
		if !mayBeToolCall(g.line.String()) {
			g.observe(g.line.String())
			g.line.Reset()
			g.holding = false
			return
		}
		return
	}
}

// settleLine decides a completed buffered line: any line that
// starts with the marker is protocol and dropped - a well-formed
// call, or a malformed attempt the agent loop will correct - and
// everything else is emitted with its newline.
func (g *streamGate) settleLine() {
	line := g.line.String()
	g.line.Reset()
	if line == toolCallLine || strings.HasPrefix(line, toolCallLine+" ") {
		return // protocol, not prose: the operator never sees calls
	}
	g.observe(line + "\n")
}

// mayBeToolCall reports whether the partial line could still be a
// TOOLCALL line: a proper prefix of the marker, the marker itself,
// or the marker plus its arguments still forming. A line with
// leading whitespace is never a tool call - the pattern anchors
// the marker to the line start - so it streams as prose.
func mayBeToolCall(partial string) bool {
	return strings.HasPrefix(toolCallLine, partial) || strings.HasPrefix(partial, toolCallLine)
}
