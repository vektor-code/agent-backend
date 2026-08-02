package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	agent "github.com/kubetrace/agent-backend/internal/agent"
	"github.com/kubetrace/agent-backend/internal/vaultenv"
)

func main() {
	if err := vaultenv.Load(); err != nil {
		log.Printf("[warn] vault: %v (continuing with process env)", err)
	}

	cfg := agent.ParseConfig()
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	cfg.Log()

	service := agent.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service.StartForwarders(ctx, cfg.ForwarderWorkers)
	go service.RunController(ctx)

	grpcServer, err := agent.ServeGRPC(cfg.GRPCAddr, service)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	httpServer := agent.ServeHTTP(cfg.HTTPAddr, service)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down KubeTrace agent...")
	cancel()
	grpcServer.GracefulStop()

	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := httpServer.Shutdown(ctxShutdown); err != nil {
		log.Printf("[agent/error] HTTP server shutdown: %v", err)
	}
	log.Println("KubeTrace agent stopped.")
}
