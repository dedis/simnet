package network

import (
	"fmt"
	"time"
)

// Delay is a parameter of a rule that will add a delay to the outgoing
// traffic.
type Delay struct {
	Value  time.Duration
	Leeway time.Duration
}

func (d Delay) String() string {
	if d.Value <= 0 {
		return ""
	}

	return fmt.Sprintf("delay %dms", d.Value.Milliseconds())
}

// Loss is a parameter of a rule that will induce a percentage of loss to
// the outgoing traffic.
type Loss struct {
	Value float64
}

func (l Loss) String() string {
	if l.Value <= 0 || l.Value > 1 {
		return ""
	}

	return fmt.Sprintf("loss %.2f%%", l.Value*100)
}

// Rule is a set of parameters to apply to a topology link to change the
// properties like the RRT, bandwidth and so on.
type Rule struct {
	IP    string
	Delay Delay
	Loss  Loss
}

// NewDelayRule creates a rule that only adds delay to the link.
func NewDelayRule(ip string, delay time.Duration) Rule {
	return Rule{
		IP:    ip,
		Delay: Delay{Value: delay},
	}
}

// NewLossRule creates a rule that only adds loss to the link.
func NewLossRule(ip string, loss float64) Rule {
	return Rule{
		IP:   ip,
		Loss: Loss{Value: loss},
	}
}

// MatchAddr returns the match filter for the rule.
func (r Rule) MatchAddr() string {
	return fmt.Sprintf("%s/32", r.IP)
}
