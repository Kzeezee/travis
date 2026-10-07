package core

import "time"

type Clock interface {
	ReportCurrentTime() time.Time
	// After delivers once the duration has passed, like time.After.
	After(time.Duration) <-chan time.Time
}

type SystemClock struct{}

func (SystemClock) ReportCurrentTime() time.Time {
	return time.Now()
}

func (SystemClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

type FixedClock struct {
	Time time.Time
}

func (c FixedClock) ReportCurrentTime() time.Time {
	return c.Time
}

// After fires immediately: time never moves on a FixedClock, so nothing waiting on it should either.
func (c FixedClock) After(time.Duration) <-chan time.Time {
	fired := make(chan time.Time, 1)
	fired <- c.Time
	return fired
}
