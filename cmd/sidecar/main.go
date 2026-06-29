// Command sidecar starts the Rekja sidecar gRPC server.
//
// The sidecar runs alongside the Valheim dedicated server in a Kubernetes pod.
// It manages mod files on the shared PVC and exposes a gRPC API for the
// client TUI to control mod sync, server restart, and log retrieval.
//
// Environment variables:
//
//	REKJA_PLUGINS_DIR        - Path to the ValheimPlus plugins directory (default: /config/valheimplus/plugins)
//	REKJA_THUNDERSTORE_BASE_URL   - Thunderstore API base URL (default: https://valheim.thunderstore.io)
//	REKJA_GRPC_ADDR          - gRPC listen address (default: :8080)
//	REKJA_NAMESPACE          - Kubernetes namespace
//	REKJA_POD_NAME           - Target pod name
//	REKJA_CONTAINER          - Target container name (default: valheim)
//	REKJA_LABEL_SELECTOR     - Kubernetes label selector for target pod
package main

import (
	"fmt"
	"net"
	"os"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/cbartram/rekja/internal/config"
)

func main() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	addr := os.Getenv("REKJA_GRPC_ADDR")
	if addr == "" {
		addr = ":8080"
	} else {
		if !strings.HasPrefix(addr, ":") {
			addr = ":" + addr
		}
	}

	pluginsDir := os.Getenv("REKJA_PLUGINS_DIR")
	if pluginsDir == "" {
		pluginsDir = config.Default().PluginsDir
	}

	thunderstoreURL := os.Getenv("REKJA_THUNDERSTORE_BASE_URL")
	if thunderstoreURL == "" {
		thunderstoreURL = config.Default().ThunderstoreBaseURL
	}

	kubeCfg := config.KubernetesConfig{
		Namespace:     os.Getenv("REKJA_NAMESPACE"),
		PodName:       os.Getenv("REKJA_POD_NAME"),
		ContainerName: os.Getenv("REKJA_CONTAINER"),
		LabelSelector: os.Getenv("REKJA_LABEL_SELECTOR"),
	}
	if kubeCfg.ContainerName == "" {
		kubeCfg.ContainerName = "valheim"
	}

	cfg := Config{
		PluginsDir:          pluginsDir,
		ThunderstoreBaseURL: thunderstoreURL,
		Kubernetes:          kubeCfg,
		Logger:              logger,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		logger.Fatal("failed to create sidecar", zap.Error(err))
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatal("failed to listen", zap.String("addr", addr), zap.Error(err))
	}

	grpcServer := grpc.NewServer()
	srv.Register(grpcServer)

	logger.Info("Rekja sidecar listening on ", zap.String("addr", addr))
	if err := grpcServer.Serve(lis); err != nil {
		logger.Error("gRPC Rekjs server failed to start", zap.Error(err))
	}
}
