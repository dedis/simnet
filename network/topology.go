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

// Topology provides the primitive to get information about the mapping of
// multiple nodes connected together and the properties of their links.
type Topology interface {
	Len() int
	GetNodes() []Node
	Rules(Node, map[Node]string) []Rule
}

// SimpleTopology represents a network map where the links between nodes have defined
// properties.
type SimpleTopology struct {
	nodes []Node
	links map[Node][]Link
}

// NewSimpleTopology creates a simple topology with a delay for traffic going
// to node0.
func NewSimpleTopology(n int, delay time.Duration) SimpleTopology {
	n = int(math.Min(MaxSize, math.Max(0, float64(n))))

	t := SimpleTopology{
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
				},
			}
		}
	}

	return t
}

// Len returns the number of nodes in the topology.
func (t SimpleTopology) Len() int {
	return len(t.nodes)
}

// GetNodes returns the nodes.
func (t SimpleTopology) GetNodes() []Node {
	return t.nodes
}

// Rules generate the rules associated to the node. It relies on the mapping
// provided to associate a node with an IP.
func (t SimpleTopology) Rules(node Node, mapping map[Node]string) []Rule {
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
