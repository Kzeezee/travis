package broker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
// or through an override, so another program cannot substitute it by name (R-23).
// StartSessionBroker then checks its hash before exec.
func ResolveBrokerExecutable() (string, error) {
	travisExecutable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the travis binary: %w", err)
	}
	if travisExecutable, err = filepath.EvalSymlinks(travisExecutable); err != nil {
		return "", fmt.Errorf("locate the travis binary: %w", err)
	}
	brokerName := "travis-broker"
	if runtime.GOOS == "windows" {
		brokerName += ".exe"
	}
	brokerExecutable := filepath.Join(filepath.Dir(travisExecutable), brokerName)
	if _, err := os.Stat(brokerExecutable); err != nil {
		return "", fmt.Errorf("travis-broker must sit next to travis at %q: %w", brokerExecutable, errors.Join(core.ErrNotFound, err))
	}
	return brokerExecutable, nil
}

// SessionLaunch is what one session's broker is started with.
type SessionLaunch struct {
	Executable string
	// ExpectedSHA256 is the hex hash travis was built with; Executable must match it (R-23).
	ExpectedSHA256 string
	WorkspaceDir   string
	RuntimeDir     string
	Profile        string
	// Diagnostics receives the broker's stderr; nil discards it.
	Diagnostics io.Writer
	// ReadyTimeout bounds the wait for the broker to start listening; zero means the default.
	ReadyTimeout time.Duration
	// Client is where the connected RemoteBroker reports denials and asks for approvals.
	Client RemoteOptions
	// Env is the broker's environment; nil inherits travis's own.
	Env []string
}

// SessionBroker is one running broker process and travis's connection to it.
type SessionBroker struct {
	Remote        *RemoteBroker
	brokerProcess *exec.Cmd
	// brokerExited delivers the broker's exit, once; the session reports it the moment it happens.
	brokerExited   <-chan error
	removeEndpoint func()
}

// StartSessionBroker launches the session's broker in the workspace, waits until it is
// listening, and connects after checking the peer is that child (D-12). Any failure leaves no
// process and no endpoint behind.
func StartSessionBroker(ctx context.Context, launch SessionLaunch) (*SessionBroker, error) {
	if err := verifyBrokerBinary(launch.Executable, launch.ExpectedSHA256); err != nil {
		return nil, err
	}
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
	brokerProcess.Env = launch.Env
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
	brokerExited := make(chan error, 1)
	go func() {
		exitErr := brokerProcess.Wait()
		remoteBroker.reportBrokerExit(exitErr)
		brokerExited <- exitErr
	}()
	return &SessionBroker{Remote: remoteBroker, brokerProcess: brokerProcess, brokerExited: brokerExited, removeEndpoint: removeEndpoint}, nil
}

// verifyBrokerBinary refuses a broker that is not the one travis was built with (R-23): a
// swapped binary would inherit every grant while the UI still shows a normal profile. A travis
// built without a stamped hash cannot tell, so it refuses too rather than trusting the disk.
// ponytail: hash-then-exec leaves a TOCTOU window; exec the hashed fd (fexecve) if the threat model needs it.
func verifyBrokerBinary(path, wantSHA256 string) error {
	if wantSHA256 == "" {
		return fmt.Errorf("this travis has no broker hash built in, so %q cannot be verified; build with `make build`: %w", path, core.ErrDenied)
	}
	binary, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("verify the broker binary: %w", err)
	}
	defer binary.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, binary); err != nil {
		return fmt.Errorf("verify the broker binary: %w", err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != strings.ToLower(wantSHA256) {
		return fmt.Errorf("%q has sha256 %s but travis was built with %s; refusing a broker that is not ours: %w", path, got, wantSHA256, core.ErrDenied)
	}
	return nil
}

// Close ends the session: the broker sees end-of-file and exits, and one that lingers is killed.
// The returned error describes how the broker exited; the session itself is already over.
func (s *SessionBroker) Close() error {
	_ = s.Remote.Close()
	var exitErr error
	select {
	case exitErr = <-s.brokerExited:
	case <-time.After(brokerShutdownTimeout):
		_ = s.brokerProcess.Process.Kill()
		exitErr = fmt.Errorf("broker did not exit within %s and was killed: %w", brokerShutdownTimeout, <-s.brokerExited)
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
