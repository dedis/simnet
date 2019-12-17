package network

import (
	"fmt"
	"math"
	"time"
)

// Area is a subset of the topology that have a given number of nodes inside it
// and a global location. A latency can be set for the members of the area and
// the latency for outsiders is computed based on the distance to other areas.
type Area struct {
	N       int
	Latency Delay
	X       float64
	Y       float64

	nodes map[Node][]Link
}

// AreaTopology is a network topology with multiple areas. Each members of an
// area have the same latency inside the area, and the same latency to members
// of other areas, case by case depending on the location.
// A unit of distance is 1ms thus coordinates can be adapted to fit a given
// latency.
type AreaTopology struct {
	areas []*Area
}

// NewAreaTopology creates a topology based on multiple areas.
func NewAreaTopology(areas ...*Area) *AreaTopology {
	t := &AreaTopology{areas: areas}

	counter := 0

	// Initialization phase to create the topology for each individual area.
	for _, area := range t.areas {
		area.nodes = make(map[Node][]Link)
		nodes := make([]Node, area.N)

		// 1. Create the nodes of the area
		for i := range nodes {
			nodes[i] = Node(fmt.Sprintf("node%d", counter))
			counter++
		}

		// 2. Create the links to the area members with the given latency.
		for i, node := range nodes {
			links := make([]Link, 0, area.N-1)

			for j, peer := range nodes {
				if i != j {
					links = append(links, Link{
						Distant: peer,
						Delay:   area.Latency,
					})
				}
			}

			area.nodes[node] = links
		}
	}

	// Then the links between the areas are calculated.
	for i, from := range t.areas {
		for j, to := range areas {
			if i != j {
				t.makeAreaLinks(from, to)
			}
		}
	}

	return t
}

// Len returns the number of nodes inside the topology.
func (t *AreaTopology) Len() int {
	n := 0
	for _, area := range t.areas {
		n += len(area.nodes)
	}
	return n
}

// GetNodes returns the list of identifiers for the nodes.
func (t *AreaTopology) GetNodes() []Node {
	nodes := make([]Node, 0)
	for _, area := range t.areas {
		for node := range area.nodes {
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// Rules returns the list of rules for a given node in the topology.
func (t *AreaTopology) Rules(target Node, mapping map[Node]string) []Rule {
	for _, area := range t.areas {
		for node, links := range area.nodes {
			if node == target {
				rules := make([]Rule, 0, len(links))
				for _, link := range links {
					rules = append(rules, Rule{
						IP:    mapping[link.Distant],
						Delay: link.Delay,
						Loss:  link.Loss,
					})
				}

				return rules
			}
		}
	}

	return nil
}

func (t *AreaTopology) makeAreaLinks(from, to *Area) {
	for node := range from.nodes {
		links := make([]Link, 0)
		for dst := range to.nodes {
			links = append(links, Link{
				Distant: dst,
				Delay:   calculateLatency(from.X, from.Y, to.X, to.Y),
			})
		}

		from.nodes[node] = append(from.nodes[node], links...)
	}
}

func calculateLatency(x1, y1, x2, y2 float64) Delay {
	distance := math.Sqrt(math.Pow(x1-x2, 2) + math.Pow(y1-y2, 2))

	return Delay{
		Value: time.Duration(distance) * time.Millisecond,
	}
}
