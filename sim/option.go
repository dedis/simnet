package sim

import (
	"fmt"
	"io"

	"go.dedis.ch/simnet/network"
)

// Options contains the different options for a simulation execution.
type Options struct {
	Files    map[interface{}]FileMapper
	Topology network.Topology
	Image    string
	Cmd      []string
	Args     []string
	Ports    []Port
}

// NewOptions creates empty options.
func NewOptions(opts []Option) *Options {
	o := &Options{
		Files: make(map[interface{}]FileMapper),
	}

	for _, f := range opts {
		f(o)
	}

	if o.Topology == nil {
		o.Topology = network.NewSimpleTopology(3, 0)
	}

	return o
}

// Option is a function that changes the global options.
type Option func(opts *Options)

// FilesKey is the kind of key that will be used to retrieve the files
// inside the execution context.
type FilesKey string

// Identifier stores information about a node that can uniquely identify it.
type Identifier struct {
	Index int
	ID    network.Node
	IP    string
}

func (id Identifier) String() string {
	return fmt.Sprintf("%s@%s", id.ID, id.IP)
}

// Files is the structure stored in the context for the file mappers.
type Files map[Identifier]interface{}

// FileMapper gives a file path and a map function so that the engine can read
// a file from the simulation node and map the content to a generic instance.
type FileMapper struct {
	Path   string
	Mapper func(io.Reader) (interface{}, error)
}

// WithFileMapper is an option for simulation engines to read files on the
// simulation nodes and convert the content. It will then be available in the
// execution context.
func WithFileMapper(key FilesKey, fm FileMapper) Option {
	return func(opts *Options) {
		opts.Files[key] = fm
	}
}

// WithTopology is an option for simulation engines to use the topology when
// deploying the nodes.
func WithTopology(topo network.Topology) Option {
	return func(opts *Options) {
		opts.Topology = topo
	}
}

// Port is a parameter for the container image to open a port with a given
// protocol. It can also be a range.
type Port interface {
	Protocol() string
	Value() int32
}

// TCP is a single TCP port.
type TCP struct {
	port int32
}

// NewTCP creates a new tcp port.
func NewTCP(port int32) TCP {
	return TCP{port: port}
}

func (tcp TCP) Protocol() string {
	return "TCP"
}

func (tcp TCP) Value() int32 {
	return tcp.port
}

// UDP is a single udp port.
type UDP struct {
	port int32
}

// NewUDP creates a new udp port.
func NewUDP(port int32) UDP {
	return UDP{port: port}
}

func (udp UDP) Protocol() string {
	return "UDP"
}

func (udp UDP) Value() int32 {
	return udp.port
}

// WithImage is an option for simulation engines to use this Docker image as
// the base application to run.
func WithImage(image string, cmd, args []string, ports ...Port) Option {
	return func(opts *Options) {
		opts.Image = image
		opts.Cmd = cmd
		opts.Args = args
		opts.Ports = ports
	}
}
