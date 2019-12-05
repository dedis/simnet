package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/docker/docker/api/types"
	dockerapi "github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

func TestMonitor_MakeDockerClient(t *testing.T) {
	client, err := makeDockerClient()
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestMonitor_Start(t *testing.T) {
	monitor := newMonitor("bob")

	r, w := io.Pipe()
	monitor.clientFactory = func() (dockerapi.APIClient, error) {
		return &testDockerClient{writer: w, reader: r}, nil
	}
	require.NoError(t, monitor.Start())

	enc := json.NewEncoder(w)
	require.NoError(t, enc.Encode(&types.StatsJSON{}))

	stats := <-monitor.Stream()
	require.NotNil(t, stats)

	require.NoError(t, monitor.Stop())
}

func TestMonitor_StartNoContainer(t *testing.T) {
	monitor := newMonitor("abc")
	monitor.clientFactory = makeTestClientFactory

	err := monitor.Start()
	require.Error(t, err)
	require.Equal(t, "container not found", err.Error())
}

func TestMonitor_StartErrorFactory(t *testing.T) {
	monitor := newMonitor("")
	e := errors.New("factory error")
	monitor.clientFactory = func() (dockerapi.APIClient, error) {
		return nil, e
	}

	err := monitor.Start()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestMonitor_StartErrorList(t *testing.T) {
	monitor := newMonitor("")
	e := errors.New("list error")
	monitor.clientFactory = func() (dockerapi.APIClient, error) {
		return &testDockerClient{errList: e}, nil
	}

	err := monitor.Start()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestMonitor_StartErrorStats(t *testing.T) {
	monitor := newMonitor("bob")
	e := errors.New("stats error")
	monitor.clientFactory = func() (dockerapi.APIClient, error) {
		return &testDockerClient{errStats: e}, nil
	}

	err := monitor.Start()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestMonitor_CloseError(t *testing.T) {
	monitor := newMonitor("")
	monitor.closer = badCloser{}

	require.Error(t, monitor.Stop())
}

type testDockerClient struct {
	*dockerapi.Client
	writer   io.Writer
	reader   io.ReadCloser
	errList  error
	errStats error
}

func (dc *testDockerClient) ContainerList(ctx context.Context, options types.ContainerListOptions) ([]types.Container, error) {
	if dc.errList != nil {
		return nil, dc.errList
	}

	containers := []types.Container{
		{
			Labels: map[string]string{
				"random.label.1":         "",
				"io.kubernetes.pod.name": "bob",
				"random.label.2":         "",
			},
		},
		{
			Labels: map[string]string{
				"io.kubernetes.pod.name": "alice",
			},
		},
	}
	return containers, nil
}

func (dc *testDockerClient) ContainerStats(ctx context.Context, container string, stream bool) (types.ContainerStats, error) {
	ret := types.ContainerStats{Body: dc.reader}

	if dc.errStats != nil {
		return ret, dc.errStats
	}

	return ret, nil
}

func makeTestClientFactory() (dockerapi.APIClient, error) {
	r, w := io.Pipe()
	return &testDockerClient{writer: w, reader: r}, nil
}

type badCloser struct{}

func (c badCloser) Close() error {
	return errors.New("close error")
}
