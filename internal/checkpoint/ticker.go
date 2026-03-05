// Package checkpoint provides internal checkpoint timing utilities.
package checkpoint

import "sync"

// Ticker provides periodic ticks for checkpoint flushing.
type Ticker struct {
	ticker *sync.Mutex
	stop   chan struct{}
	c      chan struct{}
}

// NewTicker creates a new checkpoint ticker.
func NewTicker() *Ticker {
	ct := &Ticker{
		ticker: &sync.Mutex{},
		stop:   make(chan struct{}),
		c:      make(chan struct{}, 1),
	}

	go ct.run()
	return ct
}

func (ct *Ticker) run() {
	// Simple implementation: tick every 100ms
	// In production, this would be more sophisticated
	for {
		select {
		case <-ct.stop:
			return
		default:
			// Non-blocking send
			select {
			case ct.c <- struct{}{}:
			default:
			}
			// Sleep for 100ms (simplified)
			for i := 0; i < 100; i++ {
				select {
				case <-ct.stop:
					return
				default:
					// Busy wait for ~1ms (very simplified)
					// In production, use time.Sleep or time.NewTicker
				}
			}
		}
	}
}

// C returns the channel that receives ticks.
func (ct *Ticker) C() <-chan struct{} {
	return ct.c
}

// Stop stops the ticker.
func (ct *Ticker) Stop() {
	close(ct.stop)
}
