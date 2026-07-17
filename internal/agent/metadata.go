package agent

import (
	"net"
	"os"
	"runtime"
	"strconv"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// getHostMetadata collects system details to tag onto all outgoing spans
func getHostMetadata() []*commonpb.KeyValue {
	attrs := make([]*commonpb.KeyValue, 0, 6)

	// 1. Hostname
	if hostname, err := os.Hostname(); err == nil {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   "host.name",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: hostname}},
		})
	}

	// 2. OS Type
	attrs = append(attrs, &commonpb.KeyValue{
		Key:   "os.type",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: runtime.GOOS}},
	})

	// 3. CPU Architecture
	attrs = append(attrs, &commonpb.KeyValue{
		Key:   "host.arch",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: runtime.GOARCH}},
	})

	// 4. CPU Cores Count
	cores := strconv.Itoa(runtime.NumCPU())
	attrs = append(attrs, &commonpb.KeyValue{
		Key:   "host.cpu.count",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: cores}},
	})

	// 5. Local IP Address
	if ip := getLocalIP(); ip != "" {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   "host.ip",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: ip}},
		})
	}

	// 6. Agent Information
	attrs = append(attrs, &commonpb.KeyValue{
		Key:   "agent.name",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "kubetrace-agent"}},
	})

	// 7. Cluster Name
	clusterName := os.Getenv("CLUSTER_NAME")
	if clusterName == "" {
		clusterName = "default"
	}
	attrs = append(attrs, &commonpb.KeyValue{
		Key:   "k8s.cluster.name",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: clusterName}},
	})

	return attrs
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}
