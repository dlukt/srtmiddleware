package monitor

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/dlukt/srtmiddleware/stats"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

/*
var (
	LBRThreshold     = 1 * time.Second
	OfflineThreshold = 4 * time.Second
)
*/

type Configuration struct {
	ObsWsAddr, ObsWsPass string
	GrpcAddr             string

	SceneLive string
	SceneLBR  string
	SceneBRB  string
	// SceneOffline string
}
type Monitor struct {
	obsWsAddr, obsWsPass string
	grpcAddr             string

	sceneLive string
	sceneLBR  string
	sceneBRB  string
	// sceneOffline string

	obs *goobs.Client

	lock sync.Mutex

	// lbrStart time.Time
	// offlineStart time.Time
}

func NewMonitor(config Configuration) *Monitor {
	return &Monitor{
		obsWsAddr: config.ObsWsAddr,
		obsWsPass: config.ObsWsPass,
		grpcAddr:  config.GrpcAddr,
		sceneLive: config.SceneLive,
		sceneLBR:  config.SceneLBR,
		sceneBRB:  config.SceneBRB,
		// sceneOffline: config.SceneOffline,
	}
}

func (m *Monitor) Start() error {
	obs, err := goobs.New(m.obsWsAddr, goobs.WithPassword(m.obsWsPass))
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "obs connected to", m.obsWsAddr)
	m.obs = obs
	defer obs.Disconnect()
	conn, err := grpc.NewClient(m.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := stats.NewStatsClient(conn)
	sc, err := client.GetBytesAverage(ctx, &stats.Request{})
	if err != nil {
		return err
	}
	for {
		rsp, err := sc.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// m.lock.Lock()
		ba := rsp.GetBytesAverage()
		if ba != 0.0 {
			if ba < 0.3 {
				if e := m.SwitchScene(m.sceneLBR); e != nil {
					return e
				}
			} else {
				if e := m.SwitchScene(m.sceneLive); e != nil {
					return e
				}
			}
		} else {
			if e := m.SwitchScene(m.sceneBRB); e != nil {
				return e
			}
		}
		/*
			if ba != 0.0 {
				m.offlineStart = time.Time{}
				if ba < 0.3 {
					if m.lbrStart.IsZero() {
						m.lbrStart = time.Now()
					}
				} else {
					m.lbrStart = time.Time{}
				}
			} else {
				if m.offlineStart.IsZero() {
					m.offlineStart = time.Now()
				}
			}
			if !m.lbrStart.IsZero() {
				if time.Since(m.lbrStart) >= LBRThreshold {
					if e := m.SwitchScene(m.sceneLBR); e != nil {
						return e
					}
				}
			} else {
				if e := m.SwitchScene(m.sceneLive); e != nil {
					return e
				}
			}
			if !m.offlineStart.IsZero() {
				if time.Since(m.offlineStart) >= OfflineThreshold {
					if e := m.SwitchScene(m.sceneOffline); e != nil {
						return e
					}
				}
			}
		*/
		// m.lock.Unlock()
		// fmt.Fprintln(os.Stdout, ba)
	}
	return nil
}

func (m *Monitor) SwitchScene(scene string) error {
	rsp, e := m.obs.Scenes.GetCurrentProgramScene(&scenes.GetCurrentProgramSceneParams{})
	if e != nil {
		return e
	}
	if rsp.SceneName == scene {
		// fmt.Fprintln(os.Stdout, "scene is not changed")
		return nil
	}
	_, e = m.obs.Scenes.SetCurrentProgramScene(scenes.NewSetCurrentProgramSceneParams().WithSceneName(scene))
	return e
}

// 3XNbALSVD2qtPzqo

// ffmpeg -f x11grab -follow_mouse centered -r 25 -s cif -i :0.0 -f mpegts srt://127.0.0.1:47123
