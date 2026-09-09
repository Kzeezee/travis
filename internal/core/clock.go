package core

import "time"

type Clock interface {
	ReportCurrentTime() time.Time
}

type SystemClock struct{}

func (SystemClock) ReportCurrentTime() time.Time {
	return time.Now()
}

type FixedClock struct {
	Time time.Time
}

func (c FixedClock) ReportCurrentTime() time.Time {
	return c.Time
}
