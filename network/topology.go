package network

import (
	"fmt"
	"math"
	"time"
)

const (
	// MaxSize is the maximum size allowed for a topology.
	MaxSize = 5000
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
	Delay   Delay
	Loss    Loss
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
	n = int(math.Min(MaxSize, math.Max(0, float64(n))))

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
				{
					Distant: t.nodes[0],
					Delay:   Delay{Value: delay},
					Loss:    Loss{Value: 0.05},
				},
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
func (t Topology) Rules(node Node, mapping map[Node]string) []Rule {
	rules := make([]Rule, 0)
	for _, link := range t.links[node] {
		ip := mapping[link.Distant]

		rules = append(rules, Rule{
			IP:    ip,
			Delay: link.Delay,
			Loss:  link.Loss,
		})
	}

	return rules
}
