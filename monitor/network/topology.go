package network

import (
	"fmt"
	"time"
)

// Node is a peer of the topology.
type Node string

func (n Node) String() string {
	return string(n)
}

// Link is a network link from the host to the node. It defines the properties
// of the link like a delay or a percentage of loss.
type Link struct {
	Distant Node
	Delay   time.Duration
	Loss    float64
}

// Topology represents a network map where the links between nodes have defined
// properties.
type Topology struct {
	nodes []Node
	links map[Node][]Link
}

// NewSimpleTopology creates a simple topology where every link between two
// nodes has a small delay.
func NewSimpleTopology(n int, delay time.Duration) Topology {
	t := Topology{
		nodes: make([]Node, n),
		links: make(map[Node][]Link),
	}

	for i := range t.nodes {
		key := Node(fmt.Sprintf("node%d", i))
		t.nodes[i] = key
		t.links[key] = []Link{}

		if i != 0 {
			t.links[key] = []Link{
				{Distant: t.nodes[0], Delay: 200 * time.Millisecond, Loss: 0},
			}
		}
	}

	return t
}

// Len returns the number of nodes in the topology.
func (t Topology) Len() int {
	return len(t.nodes)
}

// GetNodes returns the nodes.
func (t Topology) GetNodes() []Node {
	return t.nodes
}

// Rules generate the rules associated to the node. It relies on the mapping
// provided to associate a node with an IP.
func (t Topology) Rules(node Node, mapping map[Node]string) []RuleJSON {
	rules := make([]RuleJSON, 0)
	for _, link := range t.links[node] {
		ip := mapping[link.Distant]

		if link.Delay > 0 {
			rules = append(rules, RuleJSON{Delay: NewDelayRule(ip, link.Delay)})
		}

		if link.Loss > 0 {
			rules = append(rules, RuleJSON{Loss: NewLossRule(ip, link.Loss)})
		}
	}

	return rules
}
