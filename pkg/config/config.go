package config

import (
	"fmt"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

var Config ProviderConfig

type ProviderConfig struct {
	DockerHost string `koanf:"docker_host"`
	// Runtime to use for the container (e.g., "sysbox-runc", "runc")
	// Defaults to "sysbox-runc" if not set.
	Runtime string `koanf:"runtime"`
	// Network to attach the container to. Defaults to "bridge".
	Network string `koanf:"network"`
	// RemoveVolumes indicates whether to remove volumes when deleting the container.
	RemoveVolumes bool `koanf:"remove_volumes"`
	// Privileged runs the container in privileged mode.
	// Required for Docker-in-Docker without Sysbox.
	Privileged bool `koanf:"privileged"`
	// Binds are bind mounts to add to all containers (e.g., "/host/path:/container/path:ro")
	Binds []string `koanf:"binds"`
	// AlwaysPull forces pulling the image before each container creation.
	// Useful to ensure runners always use the latest image.
	AlwaysPull bool `koanf:"always_pull"`
}

func NewConfig(path string) error {
	k := koanf.New(".")
	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
	}

	if err := k.Unmarshal("", &Config); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	setDefaults()
	return nil
}

func setDefaults() {
	if Config.DockerHost == "" {
		Config.DockerHost = "unix:///var/run/docker.sock"
	}
	if Config.Runtime == "" {
		Config.Runtime = "sysbox-runc"
	}
	if Config.Network == "" {
		Config.Network = "bridge"
	}
	// Default to removing volumes to keep things clean
	if !Config.RemoveVolumes {
		Config.RemoveVolumes = true
	}
}
