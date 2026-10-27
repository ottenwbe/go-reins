package tui

import (
	"fmt"

	"go-reins/internal/agent"
)

// replyMetrics accumulates the token-rate statistics of a session's
// model replies: how many replies were measured, how many completion
// tokens they carried, and the min/avg/max generation rate. Every
// backend round trip of every step is a data point - tool turns and
// retries included. It backs the chat's /metrics command; the
// per-answer line under each answer (usageLine) reports the single
// newest data point, this rolls the whole session up.
//
// The zero value is an empty accumulator: record and the rendering
// are safe before anything has been measured.
type replyMetrics struct {
	replies int     // measured model replies
	tokens  int     // completion tokens across them
	minRate float64 // slowest measured reply, tok/s
	maxRate float64 // fastest measured reply, tok/s
	rateSum float64 // sum of the measured rates, for the average
}

// record folds one finished step into the accumulator - every reply
// the step generated, tool turns and retries included, because the
// session's throughput is the throughput of all model replies, not
// of the answers alone.
func (m *replyMetrics) record(res agent.RunResult) {
	for _, r := range res.Replies {
		m.add(r)
	}
}

// add folds one measured reply into the accumulator. A reply the
// backend reported no usage for is not measured and counts nowhere -
// a missing data point must not drag the averages down.
func (m *replyMetrics) add(r agent.ReplyStat) {
	rate := r.TokensPerSecond()
	if r.Usage.CompletionTokens <= 0 || rate <= 0 {
		return
	}
	if m.replies == 0 || rate < m.minRate {
		m.minRate = rate
	}
	if rate > m.maxRate {
		m.maxRate = rate
	}
	m.rateSum += rate
	m.replies++
	m.tokens += r.Usage.CompletionTokens
}

// average is the mean measured rate in tok/s, 0 when nothing has
// been measured.
func (m *replyMetrics) average() float64 {
	if m.replies == 0 {
		return 0
	}
	return m.rateSum / float64(m.replies)
}

// metricsLines renders the /metrics report for the transcript: one
// line of counts, one of rates. Before any measured reply it says
// so, the same way /models reports an empty listing.
func (m *chatModel) metricsLines() string {
	if m.metrics.replies == 0 {
		return "(no token metrics yet - the backend reported no usage)"
	}
	return fmt.Sprintf("%d model replies · %d completion tokens\ntok/s: min %.1f · avg %.1f · max %.1f",
		m.metrics.replies, m.metrics.tokens,
		m.metrics.minRate, m.metrics.average(), m.metrics.maxRate)
}
