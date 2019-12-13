package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	dockerapi "github.com/docker/docker/client"
)

type monitor interface {
	Start() error
	Stop() error
	Stream() <-chan *types.StatsJSON
}

type defaultMonitor struct {
	c             chan *types.StatsJSON
	closer        io.Closer
	containerName string
	clientFactory func() (dockerapi.APIClient, error)
}

func newMonitor(name string) *defaultMonitor {
	return &defaultMonitor{
		c:             make(chan *types.StatsJSON),
		containerName: name,
		clientFactory: makeDockerClient,
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

	data := &types.StatsJSON{}
	dec := json.NewDecoder(reply.Body)

	go func() {
		for {
			// TODO: improve the closing
			err = dec.Decode(data)
			if err != nil {
				fmt.Printf("Error: %+v\n", err)
				return
			}

			m.c <- data
		}
	}()

	return nil
}

func (m *defaultMonitor) Stop() error {
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
