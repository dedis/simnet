package network

import (
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNode_String(t *testing.T) {
	node := Node{Name: NodeID("node0")}
	require.Equal(t, "node0", node.String())
}

func TestTopology_NewSimple(t *testing.T) {
	topo := NewSimpleTopology(3, 20*time.Millisecond)

	require.Equal(t, Node{Name: NodeID("node0")}, topo.nodes[0])
	require.Equal(t, Node{Name: NodeID("node2")}, topo.nodes[2])
	require.Equal(t, int64(20), topo.links[NodeID("node1")][0].Delay.Value.Milliseconds())
}

func TestTopology_NewSimplePropertyCheck(t *testing.T) {
	f := func(n int) bool {
		topo := NewSimpleTopology(n, 20*time.Millisecond)

		if n < 0 {
			return topo.Len() == 0 && len(topo.links) == 0
		}

		if n > MaxSize {
			return topo.Len() == MaxSize && len(topo.links) == MaxSize
		}

		return n == topo.Len() && n == len(topo.links)
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestTopology_Len(t *testing.T) {
	topo := NewSimpleTopology(3, 0)
	require.Equal(t, 3, topo.Len())
}

func TestTopology_GetNodes(t *testing.T) {
	topo := NewSimpleTopology(3, 0)
	require.Equal(t, 3, len(topo.GetNodes()))
}

func TestTopology_Rules(t *testing.T) {
	topo := NewSimpleTopology(3, 25*time.Millisecond)
	mapping := map[NodeID]string{
		NodeID("node0"): "127.0.0.1",
		NodeID("node1"): "127.0.0.2",
		NodeID("node2"): "127.0.0.3",
	}

	rules := topo.Rules(NodeID("node1"), mapping)
	require.Equal(t, 1, len(rules))
	require.Equal(t, mapping[NodeID("node0")], rules[0].IP)
	require.Equal(t, int64(25), rules[0].Delay.Value.Milliseconds())
}
