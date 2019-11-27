package engine

import (
	"context"
	"io"
)

// Tunnel is an interface that will create tunnels to node of the simulation so
// that simulation running on a private network can be accessible on a per
// needed basis.
type Tunnel interface {
	Create(ipaddr string, exec func(addr string)) error
}

// Round is executed during the simulation.
type Round interface {
	Execute(ctx context.Context, tun Tunnel)
}

// SimulationEngine provides the primitives to run a simulation from the
// deployment, to the execution of the simulation round and finally the
// cleaning.
type SimulationEngine interface {
	Deploy() error
	Execute(Round) error
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
