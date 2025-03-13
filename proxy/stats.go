package proxy

import (
	"context"
	"sync"
	"time"
)

/*
type statJson struct {
	BytesAverage     float64 `json:"bavg"`
	PacketsPerSecond float64 `json:"avg"`
}

*/

// var jsonEnc = json.NewEncoder(os.Stdout)

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

			/*
				jsonEnc.Encode(statJson{
					BytesAverage:     bavg,
					PacketsPerSecond: avg,
				})
			*/

			/*
				if bavg >= 0.5 {
					if s.currentScene != LiveScene {
						s.SetScene(LiveScene)
						fmt.Fprintln(os.Stdout, "Scene changed to:", LiveScene)
					}
				} else if bavg < 0.5 && bavg != 0.0 {
					if s.currentScene != LBRScene {
						s.SetScene(LBRScene)
						fmt.Fprintln(os.Stdout, "Scene changed to:", LBRScene)
					}
				} else {
					if s.currentScene != BRBScene {
						s.SetScene(BRBScene)
						fmt.Fprintln(os.Stdout, "Scene changed to:", BRBScene)
					}
				}
			*/
			// fmt.Fprintf(os.Stderr, "\r%-54s: %8.3f kpackets (%8.3f packets/s), %8.3f mbytes (%8.3f Mbps)", c, float64(s.total)/1024, avg, float64(s.btotal)/1024/1024, bavg)

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
