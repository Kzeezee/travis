//go:build !linux && !darwin && !windows

package broker_test

import (
	"errors"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// Until an OS's transport is built, no session endpoint exists, so travis cannot start a chat
// and no tool can act unenforced (§5.12.5).
func TestStubbedTransportsRefuseToCreateAnEndpoint(t *testing.T) {
	if _, _, err := broker.CreateSessionEndpoint(t.TempDir()); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("CreateSessionEndpoint error = %v, want ErrUnsupported", err)
	}
}
