/*
Copyright © 2025 Darko Luketic <info@icod.de>
Twitch: DarqisLIve
Kick: Darqu
*/
package cmd

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/dlukt/srtmiddleware/monitor"
	"github.com/spf13/cobra"
)

var (
	grpcServerAddr string
	obsWsAddr      string
	obsWsPass      string
	sceneLive      string
	sceneLBR       string
	sceneBRB       string
	// sceneOffline   string
)

// monitorCmd represents the monitor command
var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "monitor the stream's bytes average",

	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("monitor called")
		config := monitor.Configuration{
			ObsWsAddr: obsWsAddr,
			ObsWsPass: obsWsPass,
			GrpcAddr:  grpcServerAddr,
			SceneLive: sceneLive,
			SceneLBR:  sceneLBR,
			SceneBRB:  sceneBRB,
		}
		m := monitor.NewMonitor(config)
		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt)

			// Block until a signal is received.
			s := <-c
			fmt.Println("Got signal:", s)
			os.Exit(0)
		}()
		return m.Start()
	},
}

func init() {
	rootCmd.AddCommand(monitorCmd)

	monitorCmd.Flags().StringVar(&grpcServerAddr, "grpcaddr", "127.0.0.1:50051", "gRPC server address")
	monitorCmd.Flags().StringVar(&obsWsAddr, "wsaddr", "localhost:4455", "obs websocket address")
	monitorCmd.Flags().StringVar(&obsWsPass, "wspass", "", "obs websocket password")

	monitorCmd.Flags().StringVar(&sceneLive, "sceneLive", "Live", "Live scene name")
	monitorCmd.Flags().StringVar(&sceneLBR, "sceneLBR", "LBR", "LBR scene name")
	monitorCmd.Flags().StringVar(&sceneBRB, "sceneBRB", "BRB", "BRB scene name")
}
