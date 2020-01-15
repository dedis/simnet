package kubernetes

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var newExecutor = remotecommand.NewSPDYExecutor

// Stream is the type returned by IO functions.
type Stream struct {
	writer io.WriteCloser
	O      <-chan []byte
	E      <-chan error
}

func (s *Stream) Write(p []byte) (n int, err error) {
	return s.writer.Write(p)
}

// Close closes the stream.
func (s *Stream) Close() error {
	return s.writer.Close()
}

// IO is the interface that provides primitives to readand write
// in a given container's pod. It also provide a primitive to execute a
// command.
type IO interface {
	Read(pod, container, path string) (io.ReadCloser, error)
	Write(pod, container, path string) (*Stream, error)
	Exec(pod, container string, cmd []string) (*Stream, error)
}

type kio struct {
	restclient rest.Interface
	namespace  string
	config     *rest.Config
}

func (k kio) Read(pod, container, path string) (io.ReadCloser, error) {
	reader, outStream := io.Pipe()

	req := k.restclient.
		Get().
		Namespace(k.namespace).
		Resource("pods").
		Name(pod).
		SubResource("exec").
		VersionedParams(&apiv1.PodExecOptions{
			Container: container,
			Command:   []string{"cat", path},
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := newExecutor(k.config, "POST", req.URL())
	if err != nil {
		return nil, err
	}

	go func() {
		var err error
		defer func() {
			if err != nil {
				outStream.CloseWithError(err)
			} else {
				outStream.Close()
			}
		}()

		// Any error coming from the standard error will be written in that
		// buffer so that a better explanation can be provided to any error
		// occuring.
		outErr := new(bytes.Buffer)

		err = exec.Stream(remotecommand.StreamOptions{
			Stdout: outStream,
			Stderr: outErr,
			Tty:    false,
		})

		if err != nil {
			// Replace the generic command error message by a better message.
			err = errors.New(outErr.String())
		}
	}()

	return reader, nil
}

func (k kio) Write(pod, container, path string) (*Stream, error) {
	return k.Exec(pod, container, []string{"sh", "-c", fmt.Sprintf("cat - >> %s", path)})
}

func (k kio) Exec(pod, container string, cmd []string) (*Stream, error) {
	reader, writer := io.Pipe()
	errCh := make(chan error, 1)
	outCh := make(chan []byte, 1)
	stream := &Stream{
		writer: writer,
		O:      outCh,
		E:      errCh,
	}

	req := k.restclient.
		Post().
		Namespace(k.namespace).
		Resource("pods").
		Name(pod).
		SubResource("exec").
		VersionedParams(&apiv1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := newExecutor(k.config, "POST", req.URL())
	if err != nil {
		return nil, err
	}

	errOut := new(bytes.Buffer)
	out := new(bytes.Buffer)

	go func() {
		err = exec.Stream(remotecommand.StreamOptions{
			Stdin:  reader,
			Stdout: out,
			Stderr: errOut,
			Tty:    false,
		})

		if err != nil {
			err = fmt.Errorf("%s: %s%s", err.Error(), errOut, out)
		}

		outCh <- out.Bytes()
		errCh <- err
	}()

	return stream, nil
}
