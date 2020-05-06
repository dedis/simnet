package network

import "fmt"

// CloudTopology is a topology that will create a node on each of the regions
// provided so that the topology will have latencies from the real world.
type CloudTopology struct {
	NodeSelectorKey string
	nodes           []Node
}

// NewCloudTopology returns a new cloud topology from the list of regions.
func NewCloudTopology(key string, values []string) CloudTopology {
	nodes := make([]Node, len(values))
	for i, region := range values {
		nodes[i] = Node{
			Name:         NodeID(fmt.Sprintf("node%d", i)),
			NodeSelector: region,
		}
	}

	return CloudTopology{
		NodeSelectorKey: key,
		nodes:           nodes,
	}
}

// Len returns the length of the topology.
func (t CloudTopology) Len() int {
	return len(t.nodes)
}

// GetNodes returns the list of nodes.
func (t CloudTopology) GetNodes() []Node {
	return t.nodes
}

// Rules always returns nil as there is no rule in a cloud topology.
func (t CloudTopology) Rules(NodeID, map[NodeID]string) []Rule {
	return nil
}
