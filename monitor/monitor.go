package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/dlukt/srtmiddleware/stats"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Monitor struct {
	config Configuration
	state  controlState
	obs    *goobs.Client
	twitch *TwitchClient
	logger *log.Logger
}

type monitorEvent interface{}

type autoSceneEvent struct {
	Scene string
}

type chatCommandEvent struct {
	Command ChatCommand
}

func NewMonitor(config Configuration) *Monitor {
	return &Monitor{
		config: config,
		logger: log.New(os.Stderr, "monitor: ", log.LstdFlags),
	}
}

func (m *Monitor) Start() error {
	obsClient, err := goobs.New(m.config.OBS.WsAddr, goobs.WithPassword(m.config.OBS.WsPass))
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "obs connected to", m.config.OBS.WsAddr)
	m.obs = obsClient
	defer obsClient.Disconnect()

	conn, err := grpc.NewClient(m.config.GrpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh := make(chan monitorEvent, 32)
	statsErrCh := make(chan error, 1)
	go m.runStatsLoop(ctx, conn, eventCh, statsErrCh)

	if m.config.Twitch.Enabled {
		twitchClient, err := NewTwitchClient(m.config, m.logger)
		if err != nil {
			return err
		}
		m.twitch = twitchClient
		if err := twitchClient.Start(ctx, eventCh); err != nil {
			return err
		}
	}

	for {
		select {
		case err := <-statsErrCh:
			if err == nil {
				return nil
			}
			return err
		case evt := <-eventCh:
			switch e := evt.(type) {
			case autoSceneEvent:
				if err := m.state.applyAutoScene(e.Scene, m); err != nil {
					return err
				}
			case chatCommandEvent:
				outcome, err := m.state.handleChatCommand(e.Command, m)
				if err != nil {
					m.logger.Printf("chat command %s failed: %v", e.Command.Command, err)
				}
				if m.twitch != nil && e.Command.ReplyParentID != "" {
					if replyErr := m.twitch.SendReply(ctx, outcome.reply, e.Command.ReplyParentID); replyErr != nil {
						m.logger.Printf("chat reply failed: %v", replyErr)
					}
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *Monitor) runStatsLoop(ctx context.Context, conn *grpc.ClientConn, eventCh chan<- monitorEvent, errCh chan<- error) {
	client := stats.NewStatsClient(conn)
	stream, err := client.GetBytesAverage(ctx, &stats.Request{})
	if err != nil {
		errCh <- err
		return
	}
	for {
		rsp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			errCh <- nil
			return
		}
		if err != nil {
			errCh <- err
			return
		}
		scene := m.sceneForBytesAverage(rsp.GetBytesAverage())
		select {
		case eventCh <- autoSceneEvent{Scene: scene}:
		case <-ctx.Done():
			errCh <- ctx.Err()
			return
		}
	}
}

func (m *Monitor) sceneForBytesAverage(bytesAverage float64) string {
	if bytesAverage == 0 {
		return m.config.AutoScenes.BRB
	}
	if bytesAverage < 0.3 {
		return m.config.AutoScenes.LBR
	}
	return m.config.AutoScenes.Live
}

func (m *Monitor) SwitchScene(scene string) (SceneSwitchResult, error) {
	rsp, err := m.obs.Scenes.GetCurrentProgramScene(&scenes.GetCurrentProgramSceneParams{})
	if err != nil {
		return SceneSwitchChanged, err
	}
	if rsp.SceneName == scene {
		return SceneSwitchNoop, nil
	}
	_, err = m.obs.Scenes.SetCurrentProgramScene(scenes.NewSetCurrentProgramSceneParams().WithSceneName(scene))
	if err != nil {
		return SceneSwitchChanged, err
	}
	return SceneSwitchChanged, nil
}
