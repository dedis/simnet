package kubernetes

import (
	"errors"
	"io/ioutil"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	restfake "k8s.io/client-go/rest/fake"
	"k8s.io/client-go/tools/remotecommand"
)

func TestIO_ReadPod(t *testing.T) {
	fs := kfs{
		restclient: &restfake.RESTClient{},
		makeExecutor: func(u *url.URL) (remotecommand.Executor, error) {
			return testExecutorFactory(nil, u)
		},
	}

	reader, err := fs.Read("pod", "container", "this/is/a/file")
	require.NoError(t, err)

	data, err := ioutil.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte("deadbeef"), data)
}

func TestIO_ReadPodFailure(t *testing.T) {
	e := errors.New("make executor error")
	fs := kfs{
		restclient: &restfake.RESTClient{},
		makeExecutor: func(u *url.URL) (remotecommand.Executor, error) {
			return testExecutorFactory(e, u)
		},
	}

	_, err := fs.Read("", "", "")
	require.Error(t, err)
	require.Equal(t, e, err)

	fs.makeExecutor = func(u *url.URL) (remotecommand.Executor, error) {
		return testFailingExecutorFactory(u)
	}
	reader, err := fs.Read("", "", "")
	require.NoError(t, err)

	_, err = reader.Read(make([]byte, 1))
	require.Error(t, err)
	require.Equal(t, "stream error", err.Error())
}

type fakeExecutor struct {
	url *url.URL
	err error
}

func testExecutorFactory(err error, u *url.URL) (remotecommand.Executor, error) {
	if err != nil {
		return nil, err
	}

	return &fakeExecutor{u, nil}, nil
}

func testFailingExecutorFactory(u *url.URL) (remotecommand.Executor, error) {
	return &fakeExecutor{u, errors.New("stream error")}, nil
}

func (e *fakeExecutor) Stream(options remotecommand.StreamOptions) error {
	if e.err != nil {
		options.Stderr.Write([]byte(e.err.Error()))
		return errors.New("command failed")
	}

	if options.Stdout != nil {
		_, err := options.Stdout.Write([]byte("deadbeef"))
		return err
	}

	return nil
}
