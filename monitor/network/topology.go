package network

import (
	"fmt"
)

type Node string

func (n Node) String() string {
	return string(n)
}

type Link struct {
	Distant Node
	Delay   int
	Loss    float64
}

type Topology struct {
	nodes []Node
	links map[Node][]Link
}

func NewSimpleTopology(n int) Topology {
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
				{Distant: t.nodes[0], Delay: 300, Loss: 0},
			}
		}
	}

	return t
}

func (t Topology) Len() int {
	return len(t.nodes)
}

func (t Topology) GetNodes() []Node {
	return t.nodes
}

func (t Topology) Parse(ips map[Node]string) map[Node][]string {
	rules := make(map[Node][]string)
	for node, links := range t.links {
		cmds := []string{
			"tc qdisc add dev eth0 root handle 1: htb",
			"tc class add dev eth0 parent 1: classid 1:1 htb rate 1000Mbps",
		}

		for i, link := range links {
			minor := (i + 1) * 10
			dst := ips[link.Distant]

			cmds = append(
				cmds,
				fmt.Sprintf("tc class add dev eth0 parent 1:1 classid 1:%d htb rate 1000Mbps", minor),
				fmt.Sprintf("tc qdisc add dev eth0 parent 1:%d handle %d: netem delay %dms", minor, minor+1, link.Delay),
				fmt.Sprintf("tc filter add dev eth0 protocol ip parent 1:0 prio 1 u32 match ip dst %s/32 flowid 1:%d", dst, minor),
			)
		}

		rules[node] = cmds
	}

	return rules
}
