/*
Copyright © 2025 Darko Luketic <info@icod.de>
Twitch: DarqisLIve
Kick: Darqu
*/
package main

import "github.com/dlukt/srtmiddleware/cmd"

func main() {
	cmd.Execute()
}

//go:generate echo $PWD
//go:generate protoc -Iproto --go_opt=module=github.com/dlukt/srtmiddleware/stats --go-grpc_opt=module=github.com/dlukt/srtmiddleware/stats --go_out=stats --go-grpc_out=stats proto/stats.proto
