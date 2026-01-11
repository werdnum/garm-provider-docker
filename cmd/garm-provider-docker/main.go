package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudbase/garm-provider-common/execution"
	"github.com/mercedes-benz/garm-provider-docker/internal/provider"
	"github.com/mercedes-benz/garm-provider-docker/pkg/config"
)

var signals = []os.Signal{
	os.Interrupt,
	syscall.SIGTERM,
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), signals...)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("provider execution failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	executionEnv, err := execution.GetEnvironment()
	if err != nil {
		return fmt.Errorf("failed to get execution environment: %w", err)
	}

	configPath := flag.String("configpath", "", "path to the config file")
	flag.Parse()

	if *configPath == "" {
		*configPath = executionEnv.ProviderConfigFile
	}

	if err := config.NewConfig(*configPath); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	prov, err := provider.NewDockerProvider(executionEnv.ControllerID, executionEnv.PoolID)
	if err != nil {
		return fmt.Errorf("failed to create docker provider: %w", err)
	}

	result, err := execution.Run(ctx, prov, executionEnv)
	if err != nil {
		return fmt.Errorf("failed to run command: %w", err)
	}

	if len(result) > 0 {
		fmt.Fprint(os.Stdout, result)
	}
	return nil
}
