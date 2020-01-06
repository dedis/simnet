package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
)

const (
	// ContainerStopTimeout is the maximum amount of time given to a container
	// to stop.
	ContainerStopTimeout = 10 * time.Second
)

// Strategy implements the strategy interface for running simulations inside a
// Docker environment.
type Strategy struct {
	out        io.Writer
	cli        client.APIClient
	options    *sim.Options
	containers []types.ContainerJSON
}

func newStrategy(opts ...sim.Option) (*Strategy, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}

	return &Strategy{
		out:        os.Stdout,
		cli:        cli,
		options:    sim.NewOptions(opts),
		containers: make([]types.ContainerJSON, 0),
	}, nil
}

func (s *Strategy) pullImage(ctx context.Context) error {
	ref := fmt.Sprintf("docker.io/%s", s.options.Image)

	reader, err := s.cli.ImagePull(ctx, ref, types.ImagePullOptions{})
	if err != nil {
		return err
	}

	io.Copy(s.out, reader) // ignore potential errors.

	return nil
}

func (s *Strategy) createContainer(ctx context.Context) error {
	ports := nat.PortSet{}
	for _, port := range s.options.Ports {
		// TODO: handle protocol
		ports[nat.Port(fmt.Sprintf("%d", port.Value()))] = struct{}{}
	}

	cfg := &container.Config{
		Image:        s.options.Image,
		Cmd:          append(append([]string{}, s.options.Cmd...), s.options.Args...),
		ExposedPorts: ports,
	}

	for _, node := range s.options.Topology.GetNodes() {
		resp, err := s.cli.ContainerCreate(ctx, cfg, nil, nil, node.String())
		if err != nil {
			return err
		}

		err = s.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
		if err != nil {
			return err
		}

		container, err := s.cli.ContainerInspect(ctx, resp.ID)
		if err != nil {
			return err
		}

		s.containers = append(s.containers, container)
	}

	return nil
}

// Deploy pulls the application image and starts a container per node.
func (s *Strategy) Deploy() error {
	ctx := context.Background()

	err := s.pullImage(ctx)
	if err != nil {
		return fmt.Errorf("couldn't pull the image: %v", err)
	}

	err = s.createContainer(ctx)
	if err != nil {
		return fmt.Errorf("couldn't create the container: %v", err)
	}

	time.Sleep(1 * time.Second)

	return nil
}

func (s *Strategy) makeExecutionContext() (context.Context, error) {
	ctx := context.Background()

	for key, fm := range s.options.Files {
		files := make(sim.Files)

		for i, container := range s.containers {
			reader, _, err := s.cli.CopyFromContainer(ctx, container.ID, "/root/.config/conode/private.toml")
			if err != nil {
				return nil, err
			}

			ident := sim.Identifier{
				Index: i,
				ID:    network.Node(container.Name),
				IP:    container.NetworkSettings.DefaultNetworkSettings.IPAddress,
			}

			files[ident], err = fm.Mapper(reader)
			if err != nil {
				return nil, err
			}
		}

		ctx = context.WithValue(ctx, key, files)
	}

	return ctx, nil
}

// Execute takes the round and execute it against the context created from the
// options.
func (s *Strategy) Execute(round sim.Round) error {
	ctx, err := s.makeExecutionContext()
	if err != nil {
		return err
	}

	err = round.Execute(ctx)
	if err != nil {
		return err
	}

	return nil
}

// WriteStats writes the statistics of the nodes to the file.
func (s *Strategy) WriteStats(filename string) error {
	ctx := context.Background()

	for _, container := range s.containers {
		out, err := s.cli.ContainerLogs(ctx, container.ID, types.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
		})
		if err != nil {
			return fmt.Errorf("couldn't get the logs: %v", err)
		}

		stdcopy.StdCopy(os.Stdout, os.Stderr, out)
	}

	return nil
}

// Clean stops and removes all the containers created by the simulation.
func (s *Strategy) Clean() error {
	ctx := context.Background()
	errs := []error{}

	timeout := ContainerStopTimeout

	for _, container := range s.containers {
		err := s.cli.ContainerStop(ctx, container.ID, &timeout)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = s.cli.ContainerRemove(ctx, container.ID, types.ContainerRemoveOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("couldn't remove the containers: %v", errs)
	}

	return nil
}
