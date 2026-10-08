package broker_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

func TestBuildSessionEndpointNamesEachSessionUniquely(t *testing.T) {
	socketDir := t.TempDir()
	first, err := broker.BuildSessionEndpoint(socketDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := broker.BuildSessionEndpoint(socketDir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Address == second.Address || !strings.Contains(first.Address, "travis-") {
		t.Fatalf("endpoints %q and %q, want two distinct travis- names", first.Address, second.Address)
	}
}

// Until an OS's transport is built, a session has no broker, and so no tool can act (§5.12.5).
func TestStubbedTransportsRefuseToConnectOrServe(t *testing.T) {
	endpoint, err := broker.BuildSessionEndpoint(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.ConnectRemoteBroker(context.Background(), endpoint, 1); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("ConnectRemoteBroker error = %v, want ErrUnsupported", err)
	}
	err = broker.ServeSession(context.Background(), endpoint, 1, broker.BuildLocalBroker(t.TempDir()))
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("ServeSession error = %v, want ErrUnsupported", err)
	}
}
