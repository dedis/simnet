package network

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestArea_New(t *testing.T) {
	ta := NewAreaTopology(&Area{N: 3, X: 0, Y: 0}, &Area{N: 2, X: 3, Y: 4})

	require.Len(t, ta.areas, 2)
	require.Len(t, ta.areas[0].nodes, 3)
	require.Len(t, ta.areas[1].nodes, 2)
	require.NotNil(t, ta.areas[0].nodes["node0"])
	require.Nil(t, ta.areas[1].nodes["node0"])
	require.NotNil(t, ta.areas[1].nodes["node3"])

	for _, area := range ta.areas {
		for _, links := range area.nodes {
			require.Len(t, links, 4)
			require.Equal(t, int64(0), links[0].Delay.Value.Milliseconds())
			require.Equal(t, int64(5), links[3].Delay.Value.Milliseconds())
		}
	}
}

func TestArea_Len(t *testing.T) {
	ta := NewAreaTopology(&Area{N: 3}, &Area{N: 5})

	require.Equal(t, ta.Len(), 8)
}

func TestArea_GetNodes(t *testing.T) {
	ta := NewAreaTopology(&Area{N: 1}, &Area{N: 2})

	require.Len(t, ta.GetNodes(), 3)
}

func TestArea_Rules(t *testing.T) {
	ta := NewAreaTopology(&Area{N: 2}, &Area{N: 3, X: 10})
	mapping := map[Node]string{
		Node("node0"): "127.0.0.1",
		Node("node1"): "127.0.0.2",
		Node("node2"): "127.0.0.3",
		Node("node3"): "127.0.0.4",
		Node("node4"): "127.0.0.5",
	}

	rules := ta.Rules(Node("node1"), mapping)

	require.Equal(t, rules[0], Rule{IP: "127.0.0.1"})
	require.Contains(t, rules, Rule{IP: "127.0.0.3", Delay: Delay{Value: 10 * time.Millisecond}})
	require.Contains(t, rules, Rule{IP: "127.0.0.4", Delay: Delay{Value: 10 * time.Millisecond}})
	require.Contains(t, rules, Rule{IP: "127.0.0.5", Delay: Delay{Value: 10 * time.Millisecond}})

	rules = ta.Rules(Node("abc"), nil)
	require.Nil(t, rules)
}
