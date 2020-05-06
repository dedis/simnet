package network

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloudTopology_Len(t *testing.T) {
	cloud := NewCloudTopology("key", []string{"A", "B", "C"})
	require.Equal(t, 3, cloud.Len())

	cloud = NewCloudTopology("key", []string{"A"})
	require.Equal(t, 1, cloud.Len())
}

func TestCloudTopology_GetNodes(t *testing.T) {
	cloud := NewCloudTopology("key", []string{"A", "B", "C"})

	nodes := cloud.GetNodes()
	require.Len(t, nodes, 3)
	require.Equal(t, NodeID("node0"), nodes[0].Name)
	require.Equal(t, "A", nodes[0].NodeSelector)
}

func TestCloudTopology_Rules(t *testing.T) {
	cloud := NewCloudTopology("key", []string{"A", "B", "C"})
	require.Nil(t, cloud.Rules(NodeID("node0"), nil))
}
