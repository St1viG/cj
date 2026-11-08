package simulator

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/stream"
)

// Chaos is a stream.Faults that drops, duplicates, delays and reorders
// messages with the given probabilities, in both directions (reordering
// only outbound). One Chaos serves one agent.
type Chaos struct {
	Drop      float64
	Duplicate float64
	Reorder   float64
	MaxDelay  time.Duration

	mu   sync.Mutex
	rng  *rand.Rand
	held *agentv1.AgentMessage

	// Counters, for test logs.
	Dropped, Duplicated, Reordered, Delayed atomic.Int64
}

var _ stream.Faults = (*Chaos)(nil)

// NewChaos returns a Chaos seeded with seed.
func NewChaos(seed uint64, drop, duplicate, reorder float64, maxDelay time.Duration) *Chaos {
	return &Chaos{Drop: drop, Duplicate: duplicate, Reorder: reorder, MaxDelay: maxDelay,
		rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))} //nolint:gosec // simulation
}

func (c *Chaos) roll(p float64) bool { return p > 0 && c.rng.Float64() < p }

func (c *Chaos) delay() {
	if c.MaxDelay <= 0 {
		return
	}
	c.Delayed.Add(1)
	time.Sleep(time.Duration(c.rng.Int64N(int64(c.MaxDelay))))
}

// Outbound implements stream.Faults.
func (c *Chaos) Outbound(m *agentv1.AgentMessage) []*agentv1.AgentMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*agentv1.AgentMessage
	switch {
	case c.roll(c.Drop):
		c.Dropped.Add(1)
	case c.roll(c.Duplicate):
		c.Duplicated.Add(1)
		out = []*agentv1.AgentMessage{m, m}
	case c.held == nil && m.GetSeq() > 0 && c.roll(c.Reorder):
		c.Reordered.Add(1)
		c.held = m // sent after the next message
		return nil
	default:
		out = []*agentv1.AgentMessage{m}
	}
	if c.held != nil {
		out = append(out, c.held)
		c.held = nil
	}
	c.delay()
	return out
}

// Inbound implements stream.Faults.
func (c *Chaos) Inbound(m *agentv1.ControlMessage) []*agentv1.ControlMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.roll(c.Drop):
		c.Dropped.Add(1)
		return nil
	case c.roll(c.Duplicate):
		c.Duplicated.Add(1)
		return []*agentv1.ControlMessage{m, m}
	}
	return []*agentv1.ControlMessage{m}
}
