package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"udpreflector/internal/config"
	"udpreflector/internal/observability"
	"udpreflector/internal/reflector"
)

const shutdownTimeout = 5 * time.Second

type componentResult struct {
	name string
	err  error
}

func Run(ctx context.Context, configuration config.Config, logger *slog.Logger) error {
	metrics := observability.NewMetrics()
	var metricsListener net.Listener
	if configuration.MetricsAddress != "" {
		var err error
		metricsListener, err = net.Listen("tcp", configuration.MetricsAddress)
		if err != nil {
			return fmt.Errorf("listen for Prometheus metrics on %s: %w", configuration.MetricsAddress, err)
		}
	}

	server, err := reflector.NewServer(reflector.Settings{
		PortStart:       configuration.PortStart,
		PortEnd:         configuration.PortEnd,
		PeerTimeout:     configuration.PeerTimeout,
		MaxPeersPerPort: configuration.MaxPeersPerPort,
		UDP4Address:     configuration.UDP4Address,
		UDP6Address:     configuration.UDP6Address,
	}, metrics, logger)
	if err != nil {
		if metricsListener != nil {
			metricsListener.Close()
		}
		return err
	}

	serviceContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan componentResult, 2)
	componentCount := 1
	go func() {
		results <- componentResult{name: "UDP reflector", err: server.Run(serviceContext)}
	}()

	var httpServer *http.Server
	if metricsListener != nil {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", metrics.Handler())
		httpServer = &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		componentCount++
		go func() {
			err := httpServer.Serve(metricsListener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			results <- componentResult{name: "metrics server", err: err}
		}()
	}

	logger.Info("UDP reflector started",
		"port_start", configuration.PortStart,
		"port_end", configuration.PortEnd,
		"peer_timeout", configuration.PeerTimeout,
		"max_peers_per_port", configuration.MaxPeersPerPort,
		"udp4_address", configuration.UDP4Address,
		"udp6_address", configuration.UDP6Address,
		"metrics_enabled", metricsListener != nil,
		"metrics_address", configuration.MetricsAddress,
	)

	var runError error
	completed := 0
	select {
	case <-ctx.Done():
	case result := <-results:
		completed = 1
		runError = unexpectedComponentError(ctx, result)
	}

	cancel()
	if httpServer != nil {
		shutdownContext, stopShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
		defer stopShutdown()
		if err := httpServer.Shutdown(shutdownContext); err != nil && runError == nil {
			runError = fmt.Errorf("shut down metrics server: %w", err)
		}
	}
	for completed < componentCount {
		result := <-results
		completed++
		if err := unexpectedComponentError(ctx, result); err != nil && runError == nil {
			runError = err
		}
	}

	logger.Info("UDP reflector stopped")
	return runError
}

func unexpectedComponentError(ctx context.Context, result componentResult) error {
	if ctx.Err() != nil {
		return nil
	}
	if result.err != nil {
		return fmt.Errorf("%s failed: %w", result.name, result.err)
	}
	return fmt.Errorf("%s stopped unexpectedly", result.name)
}
