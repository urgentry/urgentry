package testutil

import (
	"errors"
	"time"
)

var ErrTimeout = errors.New("condition not met before deadline")

func Wait(timeout, interval time.Duration, fn func() (bool, error)) error {
	if timeout <= 0 {
		timeout = time.Second
	}
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ok, err := fn()
		if ok {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return ErrTimeout
		}
		time.Sleep(interval)
	}
}
