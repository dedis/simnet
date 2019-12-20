package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	dockerapi "github.com/docker/docker/client"
)

var reRxBytes = regexp.MustCompile("RX bytes:([0-9]+)")
var reTxBytes = regexp.MustCompile("TX bytes:([0-9]+)")
var errNoMatch = errors.New("missing substring match")

type monitor interface {
	Start() error
	Stop() error
	Stream() <-chan *types.StatsJSON
}

type defaultMonitor struct {
	wg            sync.WaitGroup
	c             chan *types.StatsJSON
	closing       chan struct{}
	closer        io.Closer
	containerName string
	clientFactory func() (dockerapi.APIClient, error)
	netCmd        []string
}

func newMonitor(name string) *defaultMonitor {
	return &defaultMonitor{
		c:             make(chan *types.StatsJSON),
		closing:       make(chan struct{}),
		containerName: name,
		clientFactory: makeDockerClient,
		netCmd:        []string{"ifconfig", DockerNetworkInterface},
	}
}

func (m *defaultMonitor) Start() error {
	client, err := m.clientFactory()
	if err != nil {
		return err
	}

	list, err := client.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		return err
	}

	container, err := findContainer(list, m.containerName)
	if err != nil {
		return err
	}

	reply, err := client.ContainerStats(context.Background(), container.ID, true)
	if err != nil {
		return err
	}

	m.closer = reply.Body

	dec := json.NewDecoder(reply.Body)

	chanData := make(chan *types.StatsJSON, 1)
	chanErr := make(chan error, 1)

	// First Go routine that will wait for responses from the stream and
	// decode them. It stops by itself as soon as an error occured.
	go func() {
		for {
			data := &types.StatsJSON{}
			err = dec.Decode(data)
			if err != nil {
				chanErr <- err
				return
			}

			chanData <- data
		}
	}()

	m.wg.Add(1)

	// Second Go routine will listen to either closing request or data coming
	// from the stream. It also listen for stream errors and closes if it
	// happens.
	go func() {
		for {
			select {
			case <-m.closing:
				m.wg.Done()
				return
			case err := <-chanErr:
				fmt.Printf("Monitor error: %+v\n", err)
				m.wg.Done()
				return
			case data := <-chanData:
				// Network statistics need to be gather in a different way as
				// Kubernetes uses a different container to gather the *pod*
				// statistics.
				err := m.gatherNetworkStats(data)
				if err != nil {
					fmt.Printf("Error when reading network stats: %v\n", err)
				}

				m.c <- data
			}
		}
	}()

	return nil
}

func parseUint64(re *regexp.Regexp, out *bytes.Buffer) (uint64, error) {
	m := re.FindStringSubmatch(out.String())

	if len(m) < 2 {
		return 0, errNoMatch
	}

	return strconv.ParseUint(m[1], 10, 64)
}

func (m *defaultMonitor) gatherNetworkStats(data *types.StatsJSON) error {
	out := new(bytes.Buffer)

	cmd := exec.Command(m.netCmd[0], m.netCmd[1:]...)
	cmd.Stdout = out

	err := cmd.Run()
	if err != nil {
		return err
	}

	rx, err := parseUint64(reRxBytes, out)
	if err != nil {
		return err
	}

	tx, err := parseUint64(reTxBytes, out)
	if err != nil {
		return err
	}

	data.Networks = make(map[string]types.NetworkStats)
	data.Networks[DockerNetworkInterface] = types.NetworkStats{
		RxBytes: rx,
		TxBytes: tx,
	}

	return nil
}

func (m *defaultMonitor) Stop() error {
	// First close the loop listening for data to ignore the IO error.
	close(m.closing)

	// Wait for the go routines to complete so that we don't get an IO
	// misleading error.
	m.wg.Wait()

	err := m.closer.Close()
	if err != nil {
		return err
	}

	return nil
}

func (m *defaultMonitor) Stream() <-chan *types.StatsJSON {
	return m.c
}

func makeHTTPClient() *http.Client {
	t := &http.Transport{
		Dial: func(proto, addr string) (net.Conn, error) {
			return net.DialTimeout("unix", DockerSocketPath, 10*time.Second)
		},
	}

	return &http.Client{Transport: t}
}

func makeDockerClient() (dockerapi.APIClient, error) {
	return dockerapi.NewClient("unix://"+DockerSocketPath, "", makeHTTPClient(), nil)
}

func findContainer(list []types.Container, name string) (types.Container, error) {
	for _, c := range list {
		podName := c.Labels["io.kubernetes.pod.name"]

		if strings.HasPrefix(podName, name) {
			return c, nil
		}
	}

	return types.Container{}, errors.New("container not found")
}
