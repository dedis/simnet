package network

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFullTopology_New(t *testing.T) {
	topo := NewFullTopology(
		FullInput{From: "node0", To: "node1", Latency: 1 * time.Millisecond},
		FullInput{From: "node0", To: "node2", Latency: 2 * time.Millisecond},
		FullInput{From: "node3", To: "node4", Latency: 3 * time.Millisecond},
		FullInput{From: "node2", To: "node0", Latency: 4 * time.Millisecond},
	)

	mapping := map[NodeID]string{
		"node0": "A",
		"node1": "B",
		"node2": "C",
		"node3": "D",
		"node4": "E",
	}

	require.Equal(t, 5, topo.Len())
	require.Len(t, topo.GetNodes(), 5)

	rules := []Rule{
		{IP: "B", Delay: Delay{Value: 1 * time.Millisecond}},
		{IP: "C", Delay: Delay{Value: 2 * time.Millisecond}},
	}
	require.Equal(t, rules, topo.Rules(NodeID("node0"), mapping))

	rules = []Rule{
		{IP: "E", Delay: Delay{Value: 3 * time.Millisecond}},
	}
	require.Equal(t, rules, topo.Rules(NodeID("node3"), mapping))

	rules = []Rule{
		{IP: "A", Delay: Delay{Value: 4 * time.Millisecond}},
	}
	require.Equal(t, rules, topo.Rules(NodeID("node2"), mapping))
}
