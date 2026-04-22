package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hiclaw/hiclaw-controller/internal/app"
	"github.com/hiclaw/hiclaw-controller/internal/config"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	ctrl.SetLogger(zap.New())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := config.LoadConfig()

	application, err := app.New(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("hiclaw-controller is running. Press Ctrl+C to stop.")

	if err := application.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "controller exited with error: %v\n", err)
		os.Exit(1)
	}
}
