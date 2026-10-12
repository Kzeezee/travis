package broker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

// BrokerReadyLine is the one line travis-broker writes to stdout, once it is listening.
const BrokerReadyLine = "ready"

const (
	defaultBrokerReadyTimeout = 10 * time.Second
	brokerShutdownTimeout     = 2 * time.Second
)

// ResolveBrokerExecutable finds travis-broker beside the running travis binary, never on PATH
// or through an override, so another program cannot substitute it by name (R-23). Verifying
// the binary's hash before exec is still to come.
func ResolveBrokerExecutable() (string, error) {
	travisExecutable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the travis binary: %w", err)
	}
	if travisExecutable, err = filepath.EvalSymlinks(travisExecutable); err != nil {
		return "", fmt.Errorf("locate the travis binary: %w", err)
	}
	brokerExecutable := filepath.Join(filepath.Dir(travisExecutable), "travis-broker"+brokerExecutableSuffix)
	if _, err := os.Stat(brokerExecutable); err != nil {
		return "", fmt.Errorf("travis-broker must sit next to travis at %q: %w", brokerExecutable, errors.Join(core.ErrNotFound, err))
	}
	return brokerExecutable, nil
}

// SessionLaunch is what one session's broker is started with.
type SessionLaunch struct {
	Executable   string
	WorkspaceDir string
	RuntimeDir   string
	Profile      string
	// Diagnostics receives the broker's stderr; nil discards it.
	Diagnostics io.Writer
	// ReadyTimeout bounds the wait for the broker to start listening; zero means the default.
	ReadyTimeout time.Duration
	// Client is where the connected RemoteBroker reports denials and asks for approvals.
	Client RemoteOptions
}

// SessionBroker is one running broker process and travis's connection to it.
type SessionBroker struct {
	Remote         *RemoteBroker
	brokerProcess  *exec.Cmd
	removeEndpoint func()
}

// StartSessionBroker launches the session's broker in the workspace, waits until it is
// listening, and connects after checking the peer is that child (D-12). Any failure leaves no
// process and no endpoint behind.
func StartSessionBroker(ctx context.Context, launch SessionLaunch) (*SessionBroker, error) {
	endpoint, removeEndpoint, err := CreateSessionEndpoint(launch.RuntimeDir)
	if err != nil {
		return nil, err
	}
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		removeEndpoint()
		return nil, fmt.Errorf("broker ready pipe: %w", err)
	}
	defer readyReader.Close()

	// The broker outlives the start-up context for the whole session; Close ends it.
	brokerProcess := exec.CommandContext(context.WithoutCancel(ctx), launch.Executable, //nolint:gosec // the path comes from ResolveBrokerExecutable or a test, never from the model or config
		"-endpoint", string(endpoint),
		"-client-pid", strconv.Itoa(os.Getpid()),
		"-profile", launch.Profile)
	brokerProcess.Dir = launch.WorkspaceDir
	brokerProcess.Stdout = readyWriter
	brokerProcess.Stderr = launch.Diagnostics
	startErr := brokerProcess.Start()
	_ = readyWriter.Close()
	if startErr != nil {
		removeEndpoint()
		return nil, fmt.Errorf("start %q: %w", launch.Executable, startErr)
	}

	abandon := func(cause error) (*SessionBroker, error) {
		_ = brokerProcess.Process.Kill()
		_ = brokerProcess.Wait()
		removeEndpoint()
		return nil, cause
	}
	readyTimeout := launch.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = defaultBrokerReadyTimeout
	}
	if err := waitForReadyLine(ctx, readyReader, readyTimeout); err != nil {
		return abandon(fmt.Errorf("broker did not become ready: %w", err))
	}
	remoteBroker, err := ConnectRemoteBroker(ctx, endpoint, brokerProcess.Process.Pid, launch.Client)
	if err != nil {
		return abandon(err)
	}
	return &SessionBroker{Remote: remoteBroker, brokerProcess: brokerProcess, removeEndpoint: removeEndpoint}, nil
}

// Close ends the session: the broker sees end-of-file and exits, and one that lingers is killed.
// The returned error describes how the broker exited; the session itself is already over.
func (s *SessionBroker) Close() error {
	_ = s.Remote.Close()
	brokerExited := make(chan error, 1)
	go func() { brokerExited <- s.brokerProcess.Wait() }()

	var exitErr error
	select {
	case exitErr = <-brokerExited:
	case <-time.After(brokerShutdownTimeout):
		_ = s.brokerProcess.Process.Kill()
		exitErr = fmt.Errorf("broker did not exit within %s and was killed: %w", brokerShutdownTimeout, <-brokerExited)
	}
	s.removeEndpoint()
	if exitErr != nil {
		return fmt.Errorf("broker exit: %w", exitErr)
	}
	return nil
}

// waitForReadyLine returns once the broker reports it is listening. End-of-file first means
// the broker exited, and it has already said why on stderr.
func waitForReadyLine(ctx context.Context, brokerStdout io.Reader, readyTimeout time.Duration) error {
	firstLine := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(brokerStdout).ReadString('\n')
		switch {
		case strings.TrimSpace(line) == BrokerReadyLine:
			firstLine <- nil
		case err != nil:
			firstLine <- fmt.Errorf("it exited first (see its diagnostics): %w", err)
		default:
			firstLine <- fmt.Errorf("unexpected first line %q: %w", strings.TrimSpace(line), core.ErrInvalid)
		}
	}()

	readyTimer := time.NewTimer(readyTimeout)
	defer readyTimer.Stop()
	select {
	case err := <-firstLine:
		return err
	case <-readyTimer.C:
		return fmt.Errorf("no ready line within %s", readyTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}
