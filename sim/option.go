package sim

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.dedis.ch/simnet/network"
)

var userHomeDir = os.UserHomeDir

const (
	// KB is 2³ bytes.
	KB = int64(1) << 10
	// MB is 2⁶ bytes.
	MB = KB << 10
	// GB is 2⁹ bytes.
	GB = MB << 10
	// TB is 2¹² bytes.
	TB = GB << 10
	// PB is 2¹⁶ bytes.
	PB = TB << 10
)

// TmpVolume stores the information about a tmpfs mount.
type TmpVolume struct {
	Destination string
	Size        int64
}

// Options contains the different options for a simulation execution.
type Options struct {
	OutputDir     string
	Topology      network.Topology
	Image         string
	Cmd           []string
	Args          []string
	Ports         []Port
	TmpFS         []TmpVolume
	VPNExecutable string
	Data          map[string]interface{}
}

// NewOptions creates empty options.
func NewOptions(opts []Option) *Options {
	o := &Options{
		Data:          make(map[string]interface{}),
		VPNExecutable: "openvpn",
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

	err := os.Mkdir(o.OutputDir, 0755)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		fmt.Println("failed creating simnet config dir: ", o.OutputDir, err)
		panic(err)
	}

	return o
}

// Option is a function that changes the global options.
type Option func(opts *Options)

// WithVPN is an option to change the default vpn executable path.
func WithVPN(exec string) Option {
	return func(opts *Options) {
		opts.VPNExecutable = exec
	}
}

// WithOutput is an option to change the default directory that will be used
// to write data about the simulation.
func WithOutput(dir string) Option {
	return func(opts *Options) {
		opts.OutputDir = dir
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
	if !strings.Contains(image, "/") {
		// The image comes from the Docker library.
		image = fmt.Sprintf("library/%s", image)
	}

	return func(opts *Options) {
		opts.Image = image
		opts.Cmd = cmd
		opts.Args = args
		opts.Ports = ports
	}
}

// WithTmpFS is an option for simulation engines to mount a tmpfs at the given
// destination.
func WithTmpFS(destination string, size int64) Option {
	return func(opts *Options) {
		opts.TmpFS = append(opts.TmpFS, TmpVolume{
			Destination: destination,
			Size:        size,
		})
	}
}
