package observability

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry         *prometheus.Registry
	packetsReceived  *prometheus.CounterVec
	bytesReceived    *prometheus.CounterVec
	packetsForwarded *prometheus.CounterVec
	bytesForwarded   *prometheus.CounterVec
	packetsDropped   *prometheus.CounterVec
	sendErrors       *prometheus.CounterVec
	listenerErrors   *prometheus.CounterVec
	listenerUp       *prometheus.GaugeVec
	activePeers      *prometheus.GaugeVec
}

func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		packetsReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "udp_reflector",
			Name:      "packets_received_total",
			Help:      "Number of UDP packets received.",
		}, []string{"port", "family"}),
		bytesReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "udp_reflector",
			Name:      "bytes_received_total",
			Help:      "Number of UDP payload bytes received.",
		}, []string{"port", "family"}),
		packetsForwarded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "udp_reflector",
			Name:      "packets_forwarded_total",
			Help:      "Number of UDP packet copies forwarded.",
		}, []string{"port", "family"}),
		bytesForwarded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "udp_reflector",
			Name:      "bytes_forwarded_total",
			Help:      "Number of UDP payload bytes forwarded.",
		}, []string{"port", "family"}),
		packetsDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "udp_reflector",
			Name:      "packets_dropped_total",
			Help:      "Number of UDP packets dropped before forwarding.",
		}, []string{"port", "family", "reason"}),
		sendErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "udp_reflector",
			Name:      "send_errors_total",
			Help:      "Number of UDP forwarding write errors.",
		}, []string{"port", "family"}),
		listenerErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "udp_reflector",
			Name:      "listener_errors_total",
			Help:      "Number of unexpected UDP listener errors.",
		}, []string{"port", "family"}),
		listenerUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "udp_reflector",
			Name:      "listener_up",
			Help:      "Whether a UDP listener is running.",
		}, []string{"port", "family"}),
		activePeers: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "udp_reflector",
			Name:      "active_peers",
			Help:      "Number of active peers on a logical UDP port.",
		}, []string{"port"}),
	}

	metrics.registry.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
		metrics.packetsReceived,
		metrics.bytesReceived,
		metrics.packetsForwarded,
		metrics.bytesForwarded,
		metrics.packetsDropped,
		metrics.sendErrors,
		metrics.listenerErrors,
		metrics.listenerUp,
		metrics.activePeers,
	)
	return metrics
}

func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{})
}

func (metrics *Metrics) RecordReceived(port uint16, family string, payloadBytes int) {
	labels := []string{portLabel(port), family}
	metrics.packetsReceived.WithLabelValues(labels...).Inc()
	metrics.bytesReceived.WithLabelValues(labels...).Add(float64(payloadBytes))
}

func (metrics *Metrics) RecordForwarded(port uint16, family string, payloadBytes int) {
	labels := []string{portLabel(port), family}
	metrics.packetsForwarded.WithLabelValues(labels...).Inc()
	metrics.bytesForwarded.WithLabelValues(labels...).Add(float64(payloadBytes))
}

func (metrics *Metrics) RecordDrop(port uint16, family string, reason string) {
	metrics.packetsDropped.WithLabelValues(portLabel(port), family, reason).Inc()
}

func (metrics *Metrics) RecordSendError(port uint16, family string) {
	metrics.sendErrors.WithLabelValues(portLabel(port), family).Inc()
}

func (metrics *Metrics) RecordListenerError(port uint16, family string) {
	metrics.listenerErrors.WithLabelValues(portLabel(port), family).Inc()
}

func (metrics *Metrics) SetListenerUp(port uint16, family string, up bool) {
	value := 0.0
	if up {
		value = 1
	}
	metrics.listenerUp.WithLabelValues(portLabel(port), family).Set(value)
}

func (metrics *Metrics) SetActivePeers(port uint16, active int) {
	metrics.activePeers.WithLabelValues(portLabel(port)).Set(float64(active))
}

func portLabel(port uint16) string {
	return strconv.FormatUint(uint64(port), 10)
}
