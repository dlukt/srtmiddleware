package proxy

import (
	"github.com/dlukt/srtmiddleware/stats"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type BytesAverageChan chan float64
type StatServer struct {
	stats.StatsServer
	BytesAverageChannel BytesAverageChan
}

func NewStatServer() *StatServer {
	return &StatServer{
		BytesAverageChannel: make(chan float64),
	}
}

func (s *StatServer) GetBytesAverage(_ *stats.Request, stream stats.Stats_GetBytesAverageServer) error {
	for {
		select {
		case <-stream.Context().Done():
			return status.Errorf(codes.Canceled, "server context cancelled")
		case ba := <-s.BytesAverageChannel:
			if err := stream.Send(&stats.BytesAverage{BytesAverage: ba}); err != nil {
				return err
			}
		}
	}
}
