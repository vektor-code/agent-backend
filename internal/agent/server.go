package agent

import (
	"log"
	"net"
	"net/http"

	colpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

func ServeGRPC(addr string, agent *Agent) (*grpc.Server, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	server := grpc.NewServer()
	colpb.RegisterTraceServiceServer(server, agent)

	go func() {
		if err := server.Serve(lis); err != nil {
			log.Fatalf("gRPC server: %v", err)
		}
	}()

	return server, nil
}

func ServeHTTP(addr string, agent *Agent) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", agent.HandleHTTP)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server: %v", err)
		}
	}()

	return server
}
