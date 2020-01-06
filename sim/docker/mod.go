package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"go.dedis.ch/simnet/sim"
)

const (
	ContainerStopTimeout = 10 * time.Second
)

// Strategy implements the strategy interface for running simulations inside a
// Docker environment.
type Strategy struct {
	out        io.Writer
	cli        client.APIClient
	options    *Options
	containers []string
}

func newStrategy(opts ...Option) (*Strategy, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}

	return &Strategy{
		out:        os.Stdout,
		cli:        cli,
		options:    NewOptions(opts),
		containers: make([]string, 0),
	}, nil
}

func (s *Strategy) pullImage(ctx context.Context) error {
	ref := fmt.Sprintf("docker.io/%s", s.options.Container.Image)

	reader, err := s.cli.ImagePull(ctx, ref, types.ImagePullOptions{})
	if err != nil {
		return err
	}

	io.Copy(s.out, reader) // ignore potential errors.

	return nil
}

func (s *Strategy) createContainer(ctx context.Context) error {
	for _, node := range s.options.Topology.GetNodes() {
		resp, err := s.cli.ContainerCreate(ctx, &s.options.Container, nil, nil, node.String())
		if err != nil {
			return err
		}

		s.containers = append(s.containers, resp.ID)

		err = s.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

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

func (s *Strategy) Execute(sim.Round) error {
	return nil
}

func (s *Strategy) WriteStats(filename string) error {
	ctx := context.Background()

	for _, container := range s.containers {
		out, err := s.cli.ContainerLogs(ctx, container, types.ContainerLogsOptions{
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

func (s *Strategy) Clean() error {
	ctx := context.Background()
	errs := []error{}

	timeout := ContainerStopTimeout

	for _, container := range s.containers {
		err := s.cli.ContainerStop(ctx, container, &timeout)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = s.cli.ContainerRemove(ctx, container, types.ContainerRemoveOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("couldn't remove the containers: %v", errs)
	}

	return nil
}
