package network

import "time"

// FullInput is a wrapper for parameters to create a full topology.
type FullInput struct {
	From    string
	To      string
	Latency time.Duration
}

// FullTopology is a topology where the links are defined one by one.
type FullTopology struct {
	SimpleTopology
}

// NewFullTopology creates a new topology.
func NewFullTopology(inputs ...FullInput) FullTopology {
	nodes := make([]Node, 0)
	links := make(map[NodeID][]Link)

	for _, input := range inputs {
		src := Node{Name: NodeID(input.From)}
		dst := Node{Name: NodeID(input.To)}

		_, ok := links[src.Name]
		if !ok {
			nodes = append(nodes, src)
			links[src.Name] = []Link{}
		}

		_, ok = links[dst.Name]
		if !ok {
			nodes = append(nodes, dst)
			links[dst.Name] = []Link{}
		}

		links[src.Name] = append(links[src.Name], Link{
			Distant: dst,
			Delay:   Delay{Value: input.Latency},
		})
	}

	return FullTopology{
		SimpleTopology: SimpleTopology{
			nodes: nodes,
			links: links,
		},
	}
}
