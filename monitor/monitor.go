package monitor

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/dlukt/srtmiddleware/stats"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Configuration struct {
	ObsWsAddr, ObsWsPass string
	GrpcAddr             string

	SceneLive string
	SceneLBR  string
	SceneBRB  string
}

type Monitor struct {
	obsWsAddr, obsWsPass string
	grpcAddr             string

	sceneLive string
	sceneLBR  string
	sceneBRB  string

	obs *goobs.Client
}

func NewMonitor(config Configuration) *Monitor {
	return &Monitor{
		obsWsAddr: config.ObsWsAddr,
		obsWsPass: config.ObsWsPass,
		grpcAddr:  config.GrpcAddr,
		sceneLive: config.SceneLive,
		sceneLBR:  config.SceneLBR,
		sceneBRB:  config.SceneBRB,
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
