// Generates ids from timestamp for events and tool calls.
package id

import (
	"crypto/rand"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/oklog/ulid/v2"
)

type ULIDGenerator struct {
	clock core.Clock
}

func BuildULIDGenerator(clock core.Clock) *ULIDGenerator {
	return &ULIDGenerator{clock: clock}
}

// the first 10 chars encode the time, the last 16 the random bytes
func (g *ULIDGenerator) GenerateID() core.ID {
	var entropy [10]byte
	_, _ = rand.Read(entropy[:])

	var generated ulid.ULID
	_ = generated.SetTime(ulid.Timestamp(g.clock.ReportCurrentTime()))
	_ = generated.SetEntropy(entropy[:])
	return core.ID(generated.String())
}
