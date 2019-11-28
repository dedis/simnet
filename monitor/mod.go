package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	dockerapi "github.com/docker/docker/client"
)

const (
	// MeasureFileName is the name of the file created to write the measures.
	MeasureFileName = "data"
	// DockerSocketPath is the path of the socket used by Docker on Unix machines.
	DockerSocketPath = "/var/run/docker.sock"
	// DockerNetworkInterface is the name of the network interface to measure.
	DockerNetworkInterface = "eth0"
)

func main() {
	name := flag.String("container", "", "container name prefix")
	flag.Parse()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT)

	client, err := dockerapi.NewClient("unix://"+DockerSocketPath, "", makeHTTPClient(), nil)
	if err != nil {
		panic(err)
	}

	list, err := client.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		panic(err)
	}

	container, err := findContainer(list, *name)
	if err != nil {
		// Print the list of containers for debugging when the expected container
		// cannot be found.
		for _, container := range list {
			fmt.Printf("%v %v\n", container.Names, container.Labels)
		}
		panic(err)
	}

	f, err := os.Create(MeasureFileName)
	if err != nil {
		panic(err)
	}

	writer := bufio.NewWriter(f)

	c := make(chan *types.StatsJSON)
	go streamContainerStats(client, container.ID, c)

	for {
		select {
		case <-sigc:
			// Close the stream with the docker API.
			close(c)

			f.Close()
			return
		case stats, ok := <-c:
			if !ok {
				return
			}

			netstat := stats.Networks[DockerNetworkInterface]

			writer.WriteString(fmt.Sprintf("%d,%d,%d\n", time.Now().Unix(), netstat.RxBytes, netstat.TxBytes))
			writer.Flush()

			fmt.Printf("%d: %s -- %s\n", time.Now().Unix(), stats.Name, stats.ID)
		}
	}
}

func makeHTTPClient() *http.Client {
	t := &http.Transport{
		Dial: func(proto, addr string) (net.Conn, error) {
			return net.DialTimeout("unix", DockerSocketPath, 10*time.Second)
		},
	}

	return &http.Client{Transport: t}
}

func findContainer(list []types.Container, name string) (types.Container, error) {
	for _, c := range list {
		podName := c.Labels["io.kubernetes.pod.name"]

		if podName == "" {
			return types.Container{}, errors.New("missing label with pod name")
		}

		if strings.HasPrefix(podName, name) {
			return c, nil
		}
	}

	return types.Container{}, errors.New("container not found")
}

func streamContainerStats(client *dockerapi.Client, id string, c chan *types.StatsJSON) {
	reply, err := client.ContainerStats(context.Background(), id, true)
	if err != nil {
		close(c)
	}

	defer reply.Body.Close()

	// TODO: dangerous ?
	data := &types.StatsJSON{}
	dec := json.NewDecoder(reply.Body)

	for {
		err = dec.Decode(data)
		if err != nil {
			return
		}

		c <- data
	}
}
