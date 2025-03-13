package proxy

import (
	"context"
	"sync"
	"time"
)

var bytesAverage float64 = 0.0

func NewStats() *Stats {
	return &Stats{}
}

type Stats struct {
	bprev  uint64
	btotal uint64
	prev   uint64
	total  uint64

	lock sync.Mutex

	period time.Duration
	last   time.Time

	bytesAverageChan BytesAverageChan

	cancelTicker context.CancelFunc
}

func (s *Stats) Init(period time.Duration, bachan BytesAverageChan) {
	s.bprev = 0
	s.btotal = 0
	s.prev = 0
	s.total = 0

	s.period = period
	s.last = time.Now()

	s.bytesAverageChan = bachan

	var ctx context.Context
	ctx, s.cancelTicker = context.WithCancel(context.Background())
	go s.Tick(ctx)
}

func (s *Stats) Tick(ctx context.Context) {
	ticker := time.NewTicker(s.period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case c := <-ticker.C:
			s.lock.Lock()
			diff := c.Sub(s.last)

			bytesAverage = float64(s.btotal-s.bprev) * 8 / (1000 * 1000 * diff.Seconds())
			s.bytesAverageChan <- bytesAverage
			// avg := float64(s.total-s.prev) / diff.Seconds()
			s.bprev = s.btotal
			s.prev = s.total
			s.last = c

			s.lock.Unlock()
		}
	}
}

func (s *Stats) Update(n uint64) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.btotal += n
	s.total++
}

func (s *Stats) Close() {
	s.cancelTicker()
}
