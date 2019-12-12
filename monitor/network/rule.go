package network

import (
	"fmt"
	"time"
)

// Rule provides the basic information about a topology rule.
type Rule interface {
	MatchAddr() string
}

// SingleAddrRule implements the rule interface. It will match a single
// IP.
type SingleAddrRule struct {
	ip string
}

// MatchAddr returns the match value for the IP of the rule and uses the mask
// 255.255.255.255 so that it only matches this one.
func (r SingleAddrRule) MatchAddr() string {
	return fmt.Sprintf("%s/32", r.ip)
}

// DelayRule extends the single address rule by adding values required to
// create a rule that will delay the traffic leaving for a given IP.
type DelayRule struct {
	SingleAddrRule

	Delay  time.Duration
	Leeway time.Duration
}

// NewDelayRule create the delay rule from an IP and the amount of delay to
// apply. The leeway will be zero thus creating a constant delay.
func NewDelayRule(ip string, delay time.Duration) *DelayRule {
	return &DelayRule{
		SingleAddrRule: SingleAddrRule{ip: ip},
		Delay:          delay,
		Leeway:         0,
	}
}

// LossRule extends the single address rule by adding values to
// create a rule that will produce loss for the traffic leaving
// for a given IP.
type LossRule struct {
	SingleAddrRule

	Loss float64
}

// NewLossRule creates the loss rule from an IP and the percentage of
// packets to drop.
func NewLossRule(ip string, loss float64) *LossRule {
	return &LossRule{
		SingleAddrRule: SingleAddrRule{ip: ip},
		Loss:           loss,
	}
}

// RuleJSON wraps a generic rule into its specific implementation so that it can
// be transmit over the network.
type RuleJSON struct {
	Delay *DelayRule
	Loss  *LossRule
}
