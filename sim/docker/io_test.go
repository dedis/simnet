package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/ioutil"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/sim"
)

func TestIO_Tag(t *testing.T) {
	dio := newTestDockerIO(&testIOClient{})

	dio.Tag("A")
	dio.Tag("B")
	require.Len(t, dio.stats.Tags, 2)
}

func TestIO_Read(t *testing.T) {
	buffer := bytes.NewBufferString("abc")
	dio := newTestDockerIO(&testIOClient{buffer: buffer})

	reader, err := dio.Read("", "")
	require.NoError(t, err)

	out, err := ioutil.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, buffer.String(), string(out))
}

func TestIO_ReadFailures(t *testing.T) {
	client := &testIOClient{}
	dio := newTestDockerIO(client)

	e := errors.New("copy error")
	client.errCopyFromContainer = e

	_, err := dio.Read("", "")
	require.Error(t, err)
	require.Equal(t, errors.Unwrap(err), e)

	e = errors.New("tar error")
	client.errCopyFromContainer = nil
	client.errTar = e

	_, err = dio.Read("", "")
	require.Error(t, err)
}

func TestIO_Write(t *testing.T) {
	client := &testIOClient{buffer: new(bytes.Buffer)}
	dio := newTestDockerIO(client)

	in := bytes.NewBufferString("example of input to write")

	err := dio.Write("", "", bytes.NewBuffer(in.Bytes()))
	require.NoError(t, err)
	require.Equal(t, in.String(), client.buffer.String())
}

func TestIO_WriteFailures(t *testing.T) {
	client := &testIOClient{}
	dio := newTestDockerIO(client)

	r, _ := io.Pipe()
	r.Close()
	err := dio.Write("", "", r)
	require.Error(t, err)

	e := errors.New("copy error")
	client.errCopyToContainer = e

	err = dio.Write("", "", new(bytes.Buffer))
	require.Error(t, err)
}

func makeMessage(code string) events.Message {
	return events.Message{
		Status: "exec_die",
		Actor: events.Actor{
			Attributes: map[string]string{
				"exitCode": code,
				"execID":   "",
			},
		},
	}
}

func TestIO_Exec(t *testing.T) {
	msgCh := make(chan events.Message, 1)
	msgCh <- makeMessage("0")

	client := &testIOClient{
		msgCh:  msgCh,
		buffer: new(bytes.Buffer),
	}
	dio := newTestDockerIO(client)

	err := dio.Exec("", []string{}, sim.ExecOptions{})
	require.NoError(t, err)

	reader, writer := io.Pipe()
	in := "abc"
	go func() {
		writer.Write([]byte(in))
		writer.Close()
		msgCh <- makeMessage("0")
	}()

	err = dio.Exec("", []string{}, sim.ExecOptions{
		Stdin: reader,
	})
	require.NoError(t, err)
	require.Equal(t, in, client.buffer.String())
}

func TestIO_ExecFailures(t *testing.T) {
	client := &testIOClient{}
	dio := newTestDockerIO(client)

	e := errors.New("exec create error")
	client.errContainerExecCreate = e

	err := dio.Exec("", []string{}, sim.ExecOptions{})
	require.Error(t, err)
	require.Equal(t, errors.Unwrap(err), e)

	e = errors.New("exec attach error")
	client.errContainerExecCreate = nil
	client.errContainerExecAttach = e

	err = dio.Exec("", []string{}, sim.ExecOptions{})
	require.Error(t, err)
	require.Equal(t, errors.Unwrap(err), e)

	e = errors.New("exec start error")
	client.errContainerExecAttach = nil
	client.errContainerExecStart = e

	err = dio.Exec("", []string{}, sim.ExecOptions{})
	require.Error(t, err)
	require.Equal(t, errors.Unwrap(err), e)

	client.errContainerExecStart = nil
	client.msgCh = make(chan events.Message, 1)
	client.msgCh <- makeMessage("1")

	err = dio.Exec("", []string{}, sim.ExecOptions{})
	require.Error(t, err)
	require.EqualError(t, err, "exited with 1")

	client.errCh = make(chan error, 1)
	e = errors.New("event error")
	client.errCh <- e

	err = dio.Exec("", []string{}, sim.ExecOptions{})
	require.Error(t, err)
	require.Equal(t, errors.Unwrap(err), e)

	r, _ := io.Pipe()
	r.Close()
	err = dio.Exec("", []string{}, sim.ExecOptions{Stdin: r})
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't write to stdin")
}

func newTestDockerIO(client *testIOClient) dockerio {
	stats := metrics.NewStats()
	return dockerio{
		cli:   client,
		stats: &stats,
	}
}

type testIOClient struct {
	*client.Client

	buffer *bytes.Buffer
	msgCh  chan events.Message
	errCh  chan error

	errCopyFromContainer   error
	errCopyToContainer     error
	errTar                 error
	errContainerExecCreate error
	errContainerExecAttach error
	errContainerExecStart  error
}

func (c *testIOClient) CopyFromContainer(context.Context, string, string) (io.ReadCloser, types.ContainerPathStat, error) {
	reader, writer := io.Pipe()
	tw := tar.NewWriter(writer)

	if c.errTar == nil {
		go func() {
			content := c.buffer.String()
			tw.WriteHeader(&tar.Header{Name: "abc", Mode: 0600, Size: int64(len(content))})
			tw.Write([]byte(content))
			tw.Close()
		}()
	} else {
		reader.CloseWithError(c.errTar)
	}

	return reader, types.ContainerPathStat{}, c.errCopyFromContainer
}

func (c *testIOClient) CopyToContainer(ctx context.Context, n string, p string, in io.Reader, o types.CopyToContainerOptions) error {
	if c.errCopyToContainer != nil {
		return c.errCopyToContainer
	}

	tr := tar.NewReader(in)

	if _, err := tr.Next(); err != nil {
		return err
	}

	if _, err := io.Copy(c.buffer, tr); err != nil {
		return err
	}

	return nil
}

func (c *testIOClient) Events(context.Context, types.EventsOptions) (<-chan events.Message, <-chan error) {
	return c.msgCh, c.errCh
}

func (c *testIOClient) ContainerExecCreate(context.Context, string, types.ExecConfig) (types.IDResponse, error) {
	return types.IDResponse{}, c.errContainerExecCreate
}

func (c *testIOClient) ContainerExecAttach(context.Context, string, types.ExecConfig) (types.HijackedResponse, error) {
	conn := &testConn{
		buffer: c.buffer,
	}

	return types.HijackedResponse{Conn: conn, Reader: bufio.NewReader(new(bytes.Buffer))}, c.errContainerExecAttach
}

func (c *testIOClient) ContainerExecStart(context.Context, string, types.ExecStartCheck) error {
	return c.errContainerExecStart
}
