package core

import (
	"strconv"
	"sync/atomic"
)

type ID string

func (id ID) String() string {
	return string(id)
}

// sortable identifiers for events and tool calls
type IDGenerator interface{ GenerateID() ID }

// predictable IDs for tests
type SequentialIDGenerator struct {
	Prefix string
	next   atomic.Uint64
}

func (g *SequentialIDGenerator) GenerateID() ID {
	return ID(g.Prefix + strconv.FormatUint(g.next.Add(1), 10))
}
