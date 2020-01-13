package sim

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.dedis.ch/simnet/network"
)

var userHomeDir = os.UserHomeDir

// Options contains the different options for a simulation execution.
type Options struct {
	OutputDir string
	Files     map[FilesKey]FileMapper
	Topology  network.Topology
	Image     string
	Cmd       []string
	Args      []string
	Ports     []Port
}

// NewOptions creates empty options.
func NewOptions(opts []Option) *Options {
	o := &Options{
		Files: make(map[FilesKey]FileMapper),
	}

	for _, f := range opts {
		f(o)
	}

	if o.Topology == nil {
		o.Topology = network.NewSimpleTopology(3, 0)
	}

	if o.OutputDir == "" {
		homeDir, err := userHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Couldn't get the user home directory: %v\n", err)
			// Default to relative folder if the home directory cannot be found.
			homeDir = ""
		}

		o.OutputDir = filepath.Join(homeDir, ".config", "simnet")
	}

	return o
}

// Option is a function that changes the global options.
type Option func(opts *Options)

// WithOutput is an option to change the default directory that will be used
// to write data about the simulation.
func WithOutput(dir string) Option {
	return func(opts *Options) {
		opts.OutputDir = dir
	}
}

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

// Protocol is the type of the keys for the protocols.
type Protocol string

func (p Protocol) String() string {
	return strings.ToLower(string(p))
}

const (
	// TCP is the key for the TCP protocol.
	TCP = Protocol("TCP")
	// UDP is the key for the UDP protocol.
	UDP = Protocol("UDP")
)

// Port is a parameter for the container image to open a port with a given
// protocol. It can also be a range.
type Port interface {
	Protocol() Protocol
	Value() int32
}

// TCPPort is a single TCPPort port.
type TCPPort struct {
	port int32
}

// NewTCP creates a new tcp port.
func NewTCP(port int32) TCPPort {
	return TCPPort{port: port}
}

// Protocol returns the protocol key for TCP.
func (p TCPPort) Protocol() Protocol {
	return TCP
}

// Value returns the integer value of the port.
func (p TCPPort) Value() int32 {
	return p.port
}

// UDPPort is a single udp port.
type UDPPort struct {
	port int32
}

// NewUDP creates a new udp port.
func NewUDP(port int32) UDPPort {
	return UDPPort{port: port}
}

// Protocol returns the protocol key for UDP.
func (p UDPPort) Protocol() Protocol {
	return UDP
}

// Value returns the integer value of the port.
func (p UDPPort) Value() int32 {
	return p.port
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
