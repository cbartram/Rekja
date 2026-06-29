// Command rekja-sidecar starts the Rekja sidecar gRPC server.
//
// The sidecar runs alongside the Valheim dedicated server in a Kubernetes pod.
// It manages mod files on the shared PVC and exposes a gRPC API for the
// client TUI to control mod sync, server restart, and log retrieval.
//
// Environment variables:
//   REKJA_PLUGINS_DIR        - Path to the ValheimPlus plugins directory (default: /config/valheimplus/plugins)
//   REKJA_THUNDERSTORE_URL   - Thunderstore API base URL (default: https://valheim.thunderstore.io)
//   REKJA_GRPC_ADDR          - gRPC listen address (default: :50051)
//   REKJA_NAMESPACE          - Kubernetes namespace
//   REKJA_POD_NAME           - Target pod name
//   REKJA_CONTAINER          - Target container name (default: valheim)
//   REKJA_LABEL_SELECTOR     - Kubernetes label selector for target pod
package main

import (
	"fmt"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	"github.com/cbartram/rekja/internal/config"
	"github.com/cbartram/rekja/sidecar"
)

func main() {
	addr := os.Getenv("REKJA_GRPC_ADDR")
	if addr == "" {
		addr = ":50051"
	}

	pluginsDir := os.Getenv("REKJA_PLUGINS_DIR")
	if pluginsDir == "" {
		pluginsDir = config.Default().PluginsDir
	}

	thunderstoreURL := os.Getenv("REKJA_THUNDERSTORE_URL")
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

	cfg := sidecar.Config{
		PluginsDir:          pluginsDir,
		ThunderstoreBaseURL: thunderstoreURL,
		Kubernetes:          kubeCfg,
	}

	srv, err := sidecar.NewServer(cfg)
	if err != nil {
		log.Fatalf("failed to create sidecar: %v", err)
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer()
	srv.Register(grpcServer)

	fmt.Fprintf(os.Stdout, "rekja sidecar listening on %s\n", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
