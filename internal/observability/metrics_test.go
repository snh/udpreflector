package observability

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsExposeReflectorState(t *testing.T) {
	metrics := NewMetrics()
	metrics.RecordReceived(40000, "ipv4", 12)
	metrics.RecordForwarded(40000, "ipv6", 12)
	metrics.RecordDrop(40000, "ipv4", "peer_capacity")
	metrics.RecordSendError(40000, "ipv6")
	metrics.RecordListenerError(40000, "ipv4")
	metrics.SetListenerUp(40000, "ipv4", true)
	metrics.SetActivePeers(40000, 2)

	request := httptest.NewRequest("GET", "/metrics", nil)
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()

	if response.StatusCode != 200 {
		t.Fatalf("metrics status = %d, want 200", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading metrics response: %v", err)
	}
	exposition := string(body)
	wanted := []string{
		`udp_reflector_packets_received_total{family="ipv4",port="40000"} 1`,
		`udp_reflector_bytes_received_total{family="ipv4",port="40000"} 12`,
		`udp_reflector_packets_forwarded_total{family="ipv6",port="40000"} 1`,
		`udp_reflector_bytes_forwarded_total{family="ipv6",port="40000"} 12`,
		`udp_reflector_packets_dropped_total{family="ipv4",port="40000",reason="peer_capacity"} 1`,
		`udp_reflector_send_errors_total{family="ipv6",port="40000"} 1`,
		`udp_reflector_listener_errors_total{family="ipv4",port="40000"} 1`,
		`udp_reflector_listener_up{family="ipv4",port="40000"} 1`,
		`udp_reflector_active_peers{port="40000"} 2`,
	}
	for _, metric := range wanted {
		if !strings.Contains(exposition, metric) {
			t.Errorf("metrics response does not contain %q", metric)
		}
	}
}

func TestMetricsDoNotExposePeerLabels(t *testing.T) {
	metrics := NewMetrics()
	metrics.RecordReceived(40000, "ipv4", 1)

	request := httptest.NewRequest("GET", "/metrics", nil)
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, request)
	exposition := recorder.Body.String()

	for _, forbidden := range []string{"peer=", "address=", "endpoint="} {
		if strings.Contains(exposition, forbidden) {
			t.Errorf("metrics response unexpectedly contains %q", forbidden)
		}
	}
}
