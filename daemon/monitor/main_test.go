package main

import (
	"io/ioutil"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/stretchr/testify/require"
)

func TestMain_MonitorFactory(t *testing.T) {
	m := monitorFactory("bob")
	require.NotNil(t, m)
}

func TestMain_Run(t *testing.T) {
	c := make(chan *types.StatsJSON)
	monitorFactory = func(string) monitor {
		return &testMonitor{c}
	}

	go func() {
		c <- &types.StatsJSON{}
		close(c)
	}()

	f, err := ioutil.TempFile(os.TempDir(), "monitor")
	require.NoError(t, err)

	f.Close()
	defer os.Remove(f.Name())

	os.Args = []string{os.Args[0], "-output", f.Name()}
	main()

	content, err := ioutil.ReadFile(f.Name())
	require.NoError(t, err)
	require.Contains(t, string(content), ",0,0,0\n")
}

func TestMain_RunFailures(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expect a panic")
		}
	}()

	os.Args = []string{os.Args[0], "-output", ""}
	main()
}

func TestMain_StopSignal(t *testing.T) {
	monitorFactory = makeTestMonitor

	done := make(chan struct{})

	f, err := ioutil.TempFile(os.TempDir(), "monitor")
	require.NoError(t, err)
	f.Close()
	defer os.Remove(f.Name())

	os.Args = []string{os.Args[0], "-output", f.Name()}
	go func() {
		defer close(done)
		main()
	}()

	timeout := time.After(1000 * time.Millisecond)
	for {
		select {
		case <-done:
			return
		case <-time.After(10 * time.Millisecond):
			syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		case <-timeout:
			t.Fatal("timeout")
		}
	}
}

type testMonitor struct {
	c chan *types.StatsJSON
}

func (m *testMonitor) Start() error {
	return nil
}

func (m *testMonitor) Stop() error {
	return nil
}

func (m *testMonitor) Stream() <-chan *types.StatsJSON {
	return m.c
}

func makeTestMonitor(name string) monitor {
	return &testMonitor{
		c: make(chan *types.StatsJSON),
	}
}
