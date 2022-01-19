package kubernetes

import (
	"bytes"
	"fmt"
	"io"

	"go.dedis.ch/simnet/sim"
	"golang.org/x/xerrors"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var newExecutor = remotecommand.NewSPDYExecutor

// IO is the interface that provides primitives to readand write
// in a given container's pod. It also provide a primitive to execute a
// command.
type IO interface {
	Read(pod, container, path string) (io.ReadCloser, error)
	Write(pod, container, path string, content io.Reader) error
	Exec(pod, container string, cmd []string, options sim.ExecOptions) error
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
		return nil, xerrors.Errorf("couldn't make executor: %v", err)
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
			if outErr.Len() > 0 {
				err = xerrors.Errorf("command stderr: %s", outErr.String())
			} else {
				err = xerrors.Errorf("command error: %v", err)
			}
		}
	}()

	return reader, nil
}

func (k kio) Write(pod, container, path string, content io.Reader) error {
	cmd := []string{"sh", "-c", fmt.Sprintf("cat - >> %s", path)}

	return k.Exec(pod, container, cmd, sim.ExecOptions{
		Stdin: content,
	})
}

func (k kio) Exec(pod, container string, cmd []string, options sim.ExecOptions) error {
	if len(cmd) < 1 {
		return xerrors.New("no command to execute")
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
			Stdin:     options.Stdin != nil,
			Stdout:    options.Stdout != nil,
			Stderr:    options.Stderr != nil,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := newExecutor(k.config, "POST", req.URL())
	if err != nil {
		return xerrors.Errorf("failed executing cmd '%s' on pod: %v",
			cmd[0], err)
	}

	done := make(chan error)

	go func() {
		err = exec.Stream(remotecommand.StreamOptions{
			Stdin:  options.Stdin,
			Stdout: options.Stdout,
			Stderr: options.Stderr,
			Tty:    false,
		})

		done <- err
	}()

	return <-done
}
