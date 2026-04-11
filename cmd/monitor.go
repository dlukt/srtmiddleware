/*
Copyright © 2025 Darko Luketic <info@icod.de>
Twitch: DarqisLIve
Kick: Darqu
*/
package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/dlukt/srtmiddleware/monitor"
	"github.com/spf13/cobra"
)

var (
	monitorConfigPath string

	grpcServerAddr string
	obsWsAddr      string
	obsWsPass      string
	sceneLive      string
	sceneLBR       string
	sceneBRB       string

	twitchEnabled       bool
	twitchClientID      string
	twitchClientSecret  string
	twitchRedirectURL   string
	twitchListenAddr    string
	twitchSceneCommands map[string]string
)

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "monitor the stream's bytes average",
	RunE: func(cmd *cobra.Command, args []string) error {
		fileConfig, resolvedPath, err := loadMonitorConfig(cmd)
		if err != nil {
			return err
		}
		runtimeConfig, err := fileConfig.Resolve(resolvedPath)
		if err != nil {
			return err
		}
		m := monitor.NewMonitor(runtimeConfig)
		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt)
			s := <-c
			fmt.Println("Got signal:", s)
			os.Exit(0)
		}()
		return m.Start()
	},
}

var monitorLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "run the Twitch OAuth flow and store monitor chat tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		fileConfig, resolvedPath, err := loadMonitorConfig(cmd)
		if err != nil {
			return err
		}
		logger := log.New(os.Stderr, "monitor login: ", log.LstdFlags)
		identity, err := monitor.Login(resolvedPath, fileConfig, logger)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Twitch login saved for broadcaster %s (%s) in %s\n", identity.Login, identity.UserID, resolvedPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(monitorCmd)
	monitorCmd.AddCommand(monitorLoginCmd)

	monitorCmd.PersistentFlags().StringVar(&monitorConfigPath, "config", "", "path to the monitor config file")
	monitorCmd.PersistentFlags().StringVar(&grpcServerAddr, "grpcaddr", "127.0.0.1:50051", "gRPC server address")
	monitorCmd.PersistentFlags().StringVar(&obsWsAddr, "wsaddr", "localhost:4455", "OBS websocket address")
	monitorCmd.PersistentFlags().StringVar(&obsWsPass, "wspass", "", "OBS websocket password")
	monitorCmd.PersistentFlags().StringVar(&sceneLive, "sceneLive", "Live", "Live scene name")
	monitorCmd.PersistentFlags().StringVar(&sceneLBR, "sceneLBR", "LBR", "LBR scene name")
	monitorCmd.PersistentFlags().StringVar(&sceneBRB, "sceneBRB", "BRB", "BRB scene name")
	monitorCmd.PersistentFlags().BoolVar(&twitchEnabled, "twitch-enabled", false, "enable Twitch chat control")
	monitorCmd.PersistentFlags().StringVar(&twitchClientID, "twitch-client-id", "", "Twitch application client ID")
	monitorCmd.PersistentFlags().StringVar(&twitchClientSecret, "twitch-client-secret", "", "Twitch application client secret")
	monitorCmd.PersistentFlags().StringVar(&twitchRedirectURL, "twitch-redirect-url", monitor.DefaultFileConfig().Monitor.Twitch.RedirectURL, "Twitch OAuth redirect URL")
	monitorCmd.PersistentFlags().StringVar(&twitchListenAddr, "twitch-listen-addr", monitor.DefaultFileConfig().Monitor.Twitch.ListenAddr, "local listen address for Twitch OAuth callback")
	monitorCmd.PersistentFlags().StringToStringVar(&twitchSceneCommands, "scene-command", nil, "Twitch scene command mapping in !command=Scene form; may be repeated")
}

func loadMonitorConfig(cmd *cobra.Command) (monitor.FileConfig, string, error) {
	fileConfig, resolvedPath, err := monitor.LoadFileConfig(monitorConfigPath)
	if err != nil {
		return monitor.FileConfig{}, "", err
	}
	applyMonitorFlagOverrides(cmd, &fileConfig)
	return fileConfig, resolvedPath, nil
}

func applyMonitorFlagOverrides(cmd *cobra.Command, fileConfig *monitor.FileConfig) {
	fileConfig.Normalize()
	flags := cmd.Flags()

	if flags.Changed("grpcaddr") {
		fileConfig.Monitor.GrpcAddr = grpcServerAddr
	}
	if flags.Changed("wsaddr") {
		fileConfig.Monitor.OBS.WsAddr = obsWsAddr
	}
	if flags.Changed("wspass") {
		fileConfig.Monitor.OBS.WsPass = obsWsPass
	}
	if flags.Changed("sceneLive") {
		fileConfig.Monitor.AutoScenes.Live = sceneLive
	}
	if flags.Changed("sceneLBR") {
		fileConfig.Monitor.AutoScenes.LBR = sceneLBR
	}
	if flags.Changed("sceneBRB") {
		fileConfig.Monitor.AutoScenes.BRB = sceneBRB
	}
	if flags.Changed("twitch-enabled") {
		fileConfig.Monitor.Twitch.Enabled = twitchEnabled
	}
	if flags.Changed("twitch-client-id") {
		fileConfig.Monitor.Twitch.ClientID = twitchClientID
	}
	if flags.Changed("twitch-client-secret") {
		fileConfig.Monitor.Twitch.ClientSecret = twitchClientSecret
	}
	if flags.Changed("twitch-redirect-url") {
		fileConfig.Monitor.Twitch.RedirectURL = twitchRedirectURL
	}
	if flags.Changed("twitch-listen-addr") {
		fileConfig.Monitor.Twitch.ListenAddr = twitchListenAddr
	}
	if flags.Changed("scene-command") {
		mergedCommands := make(map[string]string, len(fileConfig.Monitor.Twitch.SceneCommands)+len(twitchSceneCommands))
		for command, scene := range fileConfig.Monitor.Twitch.SceneCommands {
			mergedCommands[command] = scene
		}
		for command, scene := range twitchSceneCommands {
			mergedCommands[command] = scene
		}
		fileConfig.Monitor.Twitch.SceneCommands = mergedCommands
	}
}
