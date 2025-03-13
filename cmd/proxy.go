/*
Copyright © 2025 Darko Luketic <info@icod.de>
Twitch: DarqisLIve
Kick: Darqu
*/
package cmd

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	srt "github.com/datarhei/gosrt"
	"github.com/dlukt/srtmiddleware/proxy"
	"github.com/dlukt/srtmiddleware/stats"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	from      string = ""
	to        string = ""
	grpcaddr  string = ""
	logtopics string
)

// proxyCmd represents the proxy command
var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "A brief description of your command",
	Run: func(cmd *cobra.Command, args []string) {
		var logger srt.Logger

		lis, err := net.Listen("tcp", grpcaddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: listening on grpcaddr %v\n", err)
			os.Exit(1)
		}
		grpcServer := grpc.NewServer()
		statsServer := proxy.NewStatServer()
		stats.RegisterStatsServer(grpcServer, statsServer)
		reflection.Register(grpcServer)
		go grpcServer.Serve(lis)
		defer grpcServer.GracefulStop()

	BEGIN:
		if len(logtopics) != 0 {
			logger = srt.NewLogger(strings.Split(logtopics, ","))
		}

		go func() {
			if logger == nil {
				return
			}

			for m := range logger.Listen() {
				fmt.Fprintf(os.Stderr, "%#08x %s (in %s:%d)\n%s \n", m.SocketId, m.Topic, m.File, m.Line, m.Message)
			}
		}()

		r, err := proxy.OpenReader(from, logger)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: from: %v\n", err)
			os.Exit(1)
		}
		w, err := proxy.OpenWriter(to, logger)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: to: %v\n", err)
			os.Exit(1)
		}

		doneChan := make(chan error)

		go func() {
			buffer := make([]byte, 2048)

			s := proxy.NewStats()
			s.Init(200*time.Millisecond, statsServer.BytesAverageChannel)

			for {
				n, err := r.Read(buffer)
				if err != nil {
					doneChan <- fmt.Errorf("read: %w", err)
					s.Close()
					return
				}

				s.Update(uint64(n))

				if _, err := w.Write(buffer[:n]); err != nil {
					doneChan <- fmt.Errorf("write: %w", err)
					s.Close()
					return
				}
			}
		}()

		go func() {
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, os.Interrupt)
			<-quit

			doneChan <- nil
			os.Exit(0)
		}()

		if err := <-doneChan; err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Fprint(os.Stderr, "\n")
		}
		w.Close()
		r.Close()

		if logger != nil {
			logger.Close()
		}
		goto BEGIN
	},
}

func init() {
	rootCmd.AddCommand(proxyCmd)

	proxyCmd.Flags().StringVar(&from, "from", "srt://0.0.0.0:47123?mode=listener", "Address to read from, sources: srt://")
	proxyCmd.Flags().StringVar(&to, "to", "srt://127.0.0.1:10080?mode=listener", "Address to write to, targets: srt://")
	proxyCmd.Flags().StringVar(&logtopics, "logtopics", "", "topics for the log output")
	proxyCmd.Flags().StringVar(&grpcaddr, "grpcaddr", "localhost:50051", "grpc address to provide bytes average stats")
}
