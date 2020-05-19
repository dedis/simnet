package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/sim"
	"golang.org/x/xerrors"
)

type dockerio struct {
	cli       client.APIClient
	stats     metrics.Stats
	statsLock sync.Mutex
}

func newDockerIO(cli client.APIClient) *dockerio {
	return &dockerio{
		cli:   cli,
		stats: metrics.NewStats(),
	}
}

// Tag saves the tag using the current timestamp as the key. It allows the
// drawing of plots with points in time tagged.
func (dio *dockerio) Tag(name string) {
	dio.statsLock.Lock()
	key := time.Now().UnixNano()
	dio.stats.Tags[key] = name
	dio.statsLock.Unlock()
}

// Read reads a file in the container at the given path. It returns a reader
// that will eventually deliver the content of the file. The caller is
// responsible for closing the stream.
func (dio *dockerio) Read(container, path string) (io.ReadCloser, error) {
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
func (dio *dockerio) Write(container, path string, content io.Reader) error {
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
func (dio *dockerio) Exec(container string, cmd []string, options sim.ExecOptions) error {
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

func (dio *dockerio) Disconnect(src, dst string) error {
	// TODO: implement
	return nil
}

func (dio *dockerio) Reconnect(node string) error {
	return nil
}

func (dio *dockerio) FetchStats(from, end time.Time, filename string) error {
	dio.statsLock.Lock()
	defer dio.statsLock.Unlock()

	dir, _ := filepath.Split(filename)

	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return xerrors.Errorf("couldn't create directory: %v", err)
	}

	file, err := os.Create(filename)
	if err != nil {
		return xerrors.Errorf("couldn't create file: %v", err)
	}

	// TODO: time range
	enc := json.NewEncoder(file)
	err = enc.Encode(&dio.stats)
	if err != nil {
		return xerrors.Errorf("couldn't encode the stats: %w", err)
	}

	return nil
}

func (dio *dockerio) monitorContainers(ctx context.Context, containers []types.Container) (func(), error) {
	dio.statsLock.Lock()
	dio.stats.Timestamp = time.Now().Unix()
	dio.statsLock.Unlock()

	closers := make([]func(), 0, len(containers))

	globalCloser := func() {
		for _, closer := range closers {
			closer()
		}
	}

	for _, container := range containers {
		closer, err := dio.monitorContainer(ctx, container)
		if err != nil {
			globalCloser()
			return nil, xerrors.Errorf("couldn't listen stats: %v", err)
		}

		closers = append(closers, closer)
	}

	return globalCloser, nil
}

func (dio *dockerio) monitorContainer(ctx context.Context, container types.Container) (func(), error) {
	resp, err := dio.cli.ContainerStats(ctx, container.ID, true)
	if err != nil {
		return nil, xerrors.Errorf("couldn't get stats: %v", err)
	}

	dec := json.NewDecoder(resp.Body)
	ns := &metrics.NodeStats{}
	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		for {
			data := &types.StatsJSON{}
			err := dec.Decode(data)
			if err != nil {
				wg.Done()
				return
			}

			ns.Timestamps = append(ns.Timestamps, time.Now().Unix())
			ns.RxBytes = append(ns.RxBytes, data.Networks["eth0"].RxBytes)
			ns.TxBytes = append(ns.TxBytes, data.Networks["eth0"].TxBytes)

			prev := data.PreCPUStats.CPUUsage.TotalUsage
			prevSys := data.PreCPUStats.SystemUsage
			ns.CPU = append(ns.CPU, uint64(calculateCPUPercent(prev, prevSys, data)))
			ns.Memory = append(ns.Memory, data.MemoryStats.Usage)

			dio.statsLock.Lock()
			dio.stats.Nodes[containerName(container)] = *ns
			dio.statsLock.Unlock()
		}
	}()

	closer := func() {
		resp.Body.Close()
		wg.Wait()
	}

	return closer, nil
}

// https://github.com/moby/moby/blob/eb131c5383db8cac633919f82abad86c99bffbe5/cli/command/container/stats_helpers.go#L175-L188
func calculateCPUPercent(previousCPU, previousSystem uint64, v *types.StatsJSON) int {
	var (
		cpuPercent = 0.0
		// calculate the change for the cpu usage of the container in between readings
		cpuDelta = float64(v.CPUStats.CPUUsage.TotalUsage) - float64(previousCPU)
		// calculate the change for the entire system between readings
		systemDelta = float64(v.CPUStats.SystemUsage) - float64(previousSystem)
	)

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(len(v.CPUStats.CPUUsage.PercpuUsage)) * 100.0
	}
	return int(math.Ceil(cpuPercent * 100))
}
