package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/sim"
)

type dockerio struct {
	cli   client.APIClient
	stats *metrics.Stats
}

func newDockerIO(cli client.APIClient, stats *metrics.Stats) dockerio {
	return dockerio{
		cli:   cli,
		stats: stats,
	}
}

// Tag saves the tag using the current timestamp as the key. It allows the
// drawing of plots with points in time tagged.
func (dio dockerio) Tag(name string) {
	key := time.Now().UnixNano()
	dio.stats.Tags[key] = name
}

// Read reads a file in the container at the given path. It returns a reader
// that will eventually deliver the content of the file. The caller is
// responsible for closing the stream.
func (dio dockerio) Read(container, path string) (io.ReadCloser, error) {
	ctx := context.Background()

	reader, _, err := dio.cli.CopyFromContainer(ctx, container, path)
	if err != nil {
		return nil, fmt.Errorf("couldn't open stream: %w", err)
	}

	tr := tar.NewReader(reader)
	if _, err = tr.Next(); err != nil {
		return nil, fmt.Errorf("couldn't untar: %w", err)
	}

	return ioutil.NopCloser(tr), nil
}

// Write writes a file in the container at the given path using the content. It
// will read until it reaches EOF and it will return an error if something bad
// has happened.
func (dio dockerio) Write(container, path string, content io.Reader) error {
	ctx := context.Background()

	reader, writer := io.Pipe()
	tw := tar.NewWriter(writer)

	// The size needs to be known beforehands so the content is read first.
	buffer := new(bytes.Buffer)
	_, err := io.Copy(buffer, content)
	if err != nil {
		return err
	}

	go func() {
		tw.WriteHeader(&tar.Header{
			Size: int64(buffer.Len()),
		})

		io.Copy(tw, buffer)
		writer.Close()
	}()

	err = dio.cli.CopyToContainer(ctx, container, path, reader, types.CopyToContainerOptions{})
	if err != nil {
		return err
	}

	return nil
}

// Exec executes a command in the container.
func (dio dockerio) Exec(container string, cmd []string, options sim.ExecOptions) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := dio.cli.ContainerExecCreate(ctx, container, types.ExecConfig{
		AttachStdin:  options.Stdin != nil,
		AttachStdout: options.Stdout != nil,
		AttachStderr: options.Stderr != nil,
		Cmd:          cmd,
	})
	if err != nil {
		return fmt.Errorf("couldn't create exec: %w", err)
	}

	msgCh, errCh := dio.cli.Events(ctx, types.EventsOptions{})

	conn, err := dio.cli.ContainerExecAttach(ctx, resp.ID, types.ExecConfig{})
	if err != nil {
		return fmt.Errorf("couldn't attach exec: %w", err)
	}

	defer conn.Close()

	// We want a chance to catch errors happening when writing to the standard
	// input so this channel is used to synchronized the result.
	done := make(chan error, 1)
	if options.Stdin != nil {
		go func() {
			defer close(done)

			_, err := io.Copy(conn.Conn, options.Stdin)
			if err != nil {
				done <- err
			}

			conn.CloseWrite()
		}()
	} else {
		// But ignore if the caller does not need stdin.
		close(done)
	}

	if options.Stdout == nil {
		options.Stdout = ioutil.Discard
	}
	if options.Stderr == nil {
		options.Stderr = ioutil.Discard
	}

	// Docker provides a function to correctly split stdout and stderr from
	// the incoming reader. The Go routine will close when the hijacked
	// connection is.
	go stdcopy.StdCopy(options.Stdout, options.Stderr, conn.Reader)

	err = dio.cli.ContainerExecStart(ctx, resp.ID, types.ExecStartCheck{})
	if err != nil {
		return fmt.Errorf("couldn't start exec: %w", err)
	}

	// Check if something bad happened when writing to stdin.
	err, ok := <-done
	if err != nil && ok {
		return fmt.Errorf("couldn't write to stdin: %v", err)
	}

	// Everything's good so far so let's wait for the command to be completly done
	// to check the return status.
	for {
		select {
		case msg := <-msgCh:
			if msg.Status == "exec_die" && msg.Actor.Attributes["execID"] == resp.ID {
				code := msg.Actor.Attributes["exitCode"]
				if code != "0" {
					return fmt.Errorf("exited with %s", code)
				}

				return nil
			}
		case err := <-errCh:
			return fmt.Errorf("received error event: %w", err)
		}
	}
}

func (dio dockerio) Disconnect(src, dst string) error {
	// TODO: implement
	return nil
}
