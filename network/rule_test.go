package network

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRule_Delay(t *testing.T) {
	delay := Delay{Value: time.Millisecond}
	require.Equal(t, "delay 1ms", delay.String())

	delay.Value = -1
	require.Equal(t, "", delay.String())
}

func TestRule_Loss(t *testing.T) {
	loss := Loss{Value: 0.123}
	require.Equal(t, "loss 12.30%", loss.String())

	loss.Value = -0.5
	require.Equal(t, "", loss.String())

	loss.Value = 5
	require.Equal(t, "", loss.String())
}

func TestRule_NewDelay(t *testing.T) {
	rule := NewDelayRule("1.2.3.4", 10*time.Millisecond)
	require.Equal(t, "1.2.3.4/32", rule.MatchAddr())
	require.Equal(t, "delay 10ms", rule.Delay.String())
	require.Equal(t, "", rule.Loss.String())
}

func TestRule_NewLoss(t *testing.T) {
	rule := NewLossRule("1.2.3.4", 0.5)
	require.Equal(t, "1.2.3.4/32", rule.MatchAddr())
	require.Equal(t, "", rule.Delay.String())
	require.Equal(t, "loss 50.00%", rule.Loss.String())
}
