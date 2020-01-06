package docker

import "github.com/docker/docker/api/types/container"

import "go.dedis.ch/simnet/network"

type Options struct {
	Container container.Config
	Topology  network.Topology
}

func NewOptions(opts []Option) *Options {
	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}

	return options
}

type Option func(*Options)

func WithContainer(config container.Config) Option {
	return func(opts *Options) {
		opts.Container = config
	}
}

func WithTopology(topo network.Topology) Option {
	return func(opts *Options) {
		opts.Topology = topo
	}
}
