package engine

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Tunnel is an interface that will create tunnels to node of the simulation so
// that simulation running on a private network can be accessible on a per
// needed basis.
type Tunnel interface {
	Create(base int, ipaddr string, exec func(addr string)) error
}

// Round is executed during the simulation.
type Round interface {
	Execute(ctx context.Context, tun Tunnel)
}

// NodeStats contains the array of data that represents a timeline of the
// resource usage of a node.
type NodeStats struct {
	Timestamps []int64
	RxBytes    []uint64
	TxBytes    []uint64
	CPU        []uint64
	Memory     []uint64
}

func parseLine(line string, ns *NodeStats) error {
	values := strings.Split(line, ",")
	if len(values) < 3 {
		return errors.New("missing columns in line")
	}

	timestamp, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return err
	}

	rx, err := strconv.ParseUint(values[1], 10, 64)
	if err != nil {
		return err
	}

	tx, err := strconv.ParseUint(values[2], 10, 64)
	if err != nil {
		return err
	}

	cpu, err := strconv.ParseUint(values[3], 10, 64)
	if err != nil {
		return err
	}

	mem, err := strconv.ParseUint(values[4], 10, 64)
	if err != nil {
		return err
	}

	ns.Timestamps = append(ns.Timestamps, timestamp)
	ns.RxBytes = append(ns.RxBytes, rx)
	ns.TxBytes = append(ns.TxBytes, tx)
	ns.CPU = append(ns.CPU, cpu)
	ns.Memory = append(ns.Memory, mem)

	return nil
}

// NewNodeStats creates statistics for a node by reading the reader line by line.
func NewNodeStats(reader io.Reader) NodeStats {
	ns := NodeStats{}

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		parseLine(line, &ns)
	}

	return ns
}

// Stats represents the JSON structure of the statistics written for each node.
type Stats struct {
	Timestamp int64
	Nodes     map[string]NodeStats
}

// SimulationEngine provides the primitives to run a simulation from the
// deployment, to the execution of the simulation round and finally the
// cleaning.
type SimulationEngine interface {
	Deploy() error
	Execute(Round) error
	WriteStats(filepath string) error
	Clean() error
}

// Options contains the different options for a simulation execution.
type Options struct {
	files map[interface{}]FileMapper
}

// NewOptions creates empty options.
func NewOptions(opts []Option) *Options {
	o := &Options{
		files: make(map[interface{}]FileMapper),
	}

	for _, f := range opts {
		f(o)
	}

	return o
}

// Option is a function that changes the global options.
type Option func(opts *Options)

// FilesKey is the kind of key that will be used to retrieve the files
// inside the execution context.
type FilesKey string

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
		opts.files[key] = fm
	}
}
