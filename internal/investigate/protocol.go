package investigate

import (
	"net"
	"strconv"
	"strings"
)

// ProbeMode is how the agent is allowed to touch a destination.
// HTTP GET against a database port is how we used to produce
// "connected but received no data" on PostgreSQL.
type ProbeMode string

const (
	ProbeHTTP ProbeMode = "http"
	ProbeTCP  ProbeMode = "tcp"
	ProbeNone ProbeMode = "none"
)

// Protocol is a well-known remote system inferred from port / intent.
type Protocol struct {
	ID     string
	Kind   string // database, cache, messaging, rpc, http
	Label  string
	Mode   ProbeMode
	Source string
}

var wellKnownPorts = map[string]Protocol{
	"5432":  {ID: "postgresql", Kind: "database", Label: "PostgreSQL", Mode: ProbeTCP},
	"5433":  {ID: "postgresql", Kind: "database", Label: "PostgreSQL", Mode: ProbeTCP},
	"6432":  {ID: "postgresql", Kind: "database", Label: "PostgreSQL", Mode: ProbeTCP},
	"26257": {ID: "cockroachdb", Kind: "database", Label: "CockroachDB", Mode: ProbeTCP},
	"3306":  {ID: "mysql", Kind: "database", Label: "MySQL", Mode: ProbeTCP},
	"33060": {ID: "mysql", Kind: "database", Label: "MySQL", Mode: ProbeTCP},
	"1433":  {ID: "mssql", Kind: "database", Label: "Microsoft SQL Server", Mode: ProbeTCP},
	"1521":  {ID: "oracle", Kind: "database", Label: "Oracle", Mode: ProbeTCP},
	"2484":  {ID: "oracle", Kind: "database", Label: "Oracle", Mode: ProbeTCP},
	"6379":  {ID: "redis", Kind: "cache", Label: "Redis", Mode: ProbeTCP},
	"6380":  {ID: "redis", Kind: "cache", Label: "Redis", Mode: ProbeTCP},
	"11211": {ID: "memcached", Kind: "cache", Label: "Memcached", Mode: ProbeTCP},
	"27017": {ID: "mongodb", Kind: "database", Label: "MongoDB", Mode: ProbeTCP},
	"27018": {ID: "mongodb", Kind: "database", Label: "MongoDB", Mode: ProbeTCP},
	"27019": {ID: "mongodb", Kind: "database", Label: "MongoDB", Mode: ProbeTCP},
	"9042":  {ID: "cassandra", Kind: "database", Label: "Cassandra", Mode: ProbeTCP},
	"8123":  {ID: "clickhouse", Kind: "database", Label: "ClickHouse", Mode: ProbeTCP},
	"9000":  {ID: "clickhouse", Kind: "database", Label: "ClickHouse", Mode: ProbeTCP},
	"9440":  {ID: "clickhouse", Kind: "database", Label: "ClickHouse", Mode: ProbeTCP},
	"9200":  {ID: "elasticsearch", Kind: "database", Label: "Elasticsearch", Mode: ProbeTCP},
	"9300":  {ID: "elasticsearch", Kind: "database", Label: "Elasticsearch", Mode: ProbeTCP},
	"7687":  {ID: "neo4j", Kind: "database", Label: "Neo4j", Mode: ProbeTCP},
	"8086":  {ID: "influxdb", Kind: "database", Label: "InfluxDB", Mode: ProbeTCP},
	"9092":  {ID: "kafka", Kind: "messaging", Label: "Kafka", Mode: ProbeTCP},
	"9093":  {ID: "kafka", Kind: "messaging", Label: "Kafka", Mode: ProbeTCP},
	"5672":  {ID: "rabbitmq", Kind: "messaging", Label: "RabbitMQ", Mode: ProbeTCP},
	"5671":  {ID: "rabbitmq", Kind: "messaging", Label: "RabbitMQ", Mode: ProbeTCP},
	"4222":  {ID: "nats", Kind: "messaging", Label: "NATS", Mode: ProbeTCP},
	"2379":  {ID: "etcd", Kind: "discovery", Label: "etcd", Mode: ProbeTCP},
	"8500":  {ID: "consul", Kind: "discovery", Label: "Consul", Mode: ProbeTCP},
	"8200":  {ID: "vault", Kind: "secrets", Label: "Vault", Mode: ProbeTCP},
	"50051": {ID: "grpc", Kind: "rpc", Label: "gRPC", Mode: ProbeTCP},
	"22":    {ID: "ssh", Kind: "rpc", Label: "SSH", Mode: ProbeTCP},
	"53":    {ID: "dns", Kind: "discovery", Label: "DNS", Mode: ProbeTCP},
	"25":    {ID: "smtp", Kind: "messaging", Label: "SMTP", Mode: ProbeTCP},
	"587":   {ID: "smtp", Kind: "messaging", Label: "SMTP", Mode: ProbeTCP},
}

func protocolOf(in Intent) Protocol {
	if p := protocolFromID(in.DestinationProtocol); p.ID != "" {
		p.Source = "intent"
		return p
	}
	_, port := splitHostPort(in.Destination)
	if p, ok := wellKnownPorts[port]; ok {
		p.Source = "port " + port
		return p
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.DestinationURL)), "https://") ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.DestinationURL)), "http://") {
		if port == "5432" || wellKnownPorts[port].Mode == ProbeTCP {
			// URL was synthesised as http://ip:5432/ — ignore it.
		} else {
			return Protocol{ID: "http", Kind: "http", Label: "HTTP", Mode: ProbeHTTP, Source: "url"}
		}
	}
	if port == "443" {
		return Protocol{ID: "https", Kind: "http", Label: "HTTPS", Mode: ProbeHTTP, Source: "port 443"}
	}
	if port == "80" || port == "8080" || port == "8443" || port == "8000" {
		return Protocol{ID: "http", Kind: "http", Label: "HTTP", Mode: ProbeHTTP, Source: "port " + port}
	}
	return Protocol{ID: "tcp", Kind: "rpc", Label: "TCP", Mode: ProbeTCP, Source: "default"}
}

func protocolFromID(id string) Protocol {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return Protocol{}
	}
	for _, p := range wellKnownPorts {
		if p.ID == id {
			return p
		}
	}
	switch id {
	case "http", "https":
		return Protocol{ID: id, Kind: "http", Label: strings.ToUpper(id), Mode: ProbeHTTP}
	case "grpc":
		return Protocol{ID: "grpc", Kind: "rpc", Label: "gRPC", Mode: ProbeTCP}
	case "tcp":
		return Protocol{ID: "tcp", Kind: "rpc", Label: "TCP", Mode: ProbeTCP}
	}
	return Protocol{}
}

func validProbeHostPort(host, port string) bool {
	if host == "" || port == "" {
		return false
	}
	if _, err := strconv.Atoi(port); err != nil {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !forbiddenProbeHost(host)
	}
	h := strings.TrimSpace(host)
	if len(h) == 0 || len(h) > 253 {
		return false
	}
	for _, part := range strings.Split(h, ".") {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return false
		}
	}
	return !forbiddenProbeHost(h)
}

func forbiddenProbeHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.Trim(h, "[]")
	switch h {
	case "metadata.google.internal", "metadata.google.com", "instance-data":
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	return ip.Equal(net.ParseIP("fd00:ec2::254"))
}
