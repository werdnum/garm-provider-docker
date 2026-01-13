package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/cloudbase/garm-provider-common/params"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/mercedes-benz/garm-provider-docker/internal/spec"
	"github.com/mercedes-benz/garm-provider-docker/pkg/config"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type DockerClient interface {
	ImagePull(ctx context.Context, ref string, options types.ImagePullOptions) (io.ReadCloser, error)
	ImageInspectWithRaw(ctx context.Context, imageID string) (types.ImageInspect, []byte, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *v1.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options types.ContainerStartOptions) error
	ContainerRemove(ctx context.Context, containerID string, options types.ContainerRemoveOptions) error
	ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error)
	ContainerList(ctx context.Context, options types.ContainerListOptions) ([]types.Container, error)
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
}

type Provider struct {
	ControllerID string
	PoolID       string
	DockerClient DockerClient
}

func NewDockerProvider(controllerID, poolID string) (*Provider, error) {
	cli, err := client.NewClientWithOpts(client.WithHost(config.Config.DockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &Provider{
		ControllerID: controllerID,
		PoolID:       poolID,
		DockerClient: cli,
	}, nil
}

func (p *Provider) CreateInstance(ctx context.Context, bootstrapParams params.BootstrapInstance) (params.ProviderInstance, error) {
	// 1. Check/Pull Image
	needsPull := config.Config.AlwaysPull
	if !needsPull {
		_, _, err := p.DockerClient.ImageInspectWithRaw(ctx, bootstrapParams.Image)
		if err != nil {
			if client.IsErrNotFound(err) {
				needsPull = true
			} else {
				return params.ProviderInstance{}, fmt.Errorf("failed to inspect image %s: %w", bootstrapParams.Image, err)
			}
		}
	}

	if needsPull {
		slog.Info("pulling image", "image", bootstrapParams.Image, "always_pull", config.Config.AlwaysPull)
		pullOpts := types.ImagePullOptions{}
		if authStr := getRegistryAuth(bootstrapParams.Image); authStr != "" {
			pullOpts.RegistryAuth = authStr
		}
		reader, err := p.DockerClient.ImagePull(ctx, bootstrapParams.Image, pullOpts)
		if err != nil {
			return params.ProviderInstance{}, fmt.Errorf("failed to pull image %s: %w", bootstrapParams.Image, err)
		}
		defer reader.Close()
		io.Copy(io.Discard, reader)
	} else {
		slog.Info("using local image", "image", bootstrapParams.Image)
	}

	// 2. Prepare Config
	envs, err := spec.GetRunnerEnvs(bootstrapParams)
	if err != nil {
		return params.ProviderInstance{}, fmt.Errorf("failed to generate envs: %w", err)
	}

	labels := spec.GetContainerLabels(p.ControllerID, bootstrapParams)

	containerConfig := &container.Config{
		Image: bootstrapParams.Image,
		Env:   envs,
		Labels: labels,
		// Ensure entrypoint/cmd is correct for the image. 
		// Garm runner images usually have an entrypoint that handles the bootstrap.
	}

	hostConfig := &container.HostConfig{
		Runtime:     spec.GetHostConfigRuntime(),
		NetworkMode: container.NetworkMode(config.Config.Network),
		Privileged:  config.Config.Privileged,
		Binds:       config.Config.Binds,
	}

	// For privileged containers running Docker-in-Docker:
	// - Use host cgroup namespace so systemd/KIND can work properly
	// - Mount /var/lib/docker as a volume so inner Docker can use overlayfs
	//   (avoids overlay-on-overlay issues when host uses overlayfs)
	if config.Config.Privileged {
		hostConfig.CgroupnsMode = container.CgroupnsModeHost
		hostConfig.Mounts = []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Target: "/var/lib/docker",
				// Anonymous volume - will be cleaned up with RemoveVolumes: true
			},
		}
	}

	// 3. Create Container
	resp, err := p.DockerClient.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, bootstrapParams.Name)
	if err != nil {
		return params.ProviderInstance{}, fmt.Errorf("failed to create container: %w", err)
	}

	// 4. Start Container
	if err := p.DockerClient.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return params.ProviderInstance{}, fmt.Errorf("failed to start container: %w", err)
	}

	// 5. Get Container Info (for IP)
	inspect, err := p.DockerClient.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return params.ProviderInstance{}, fmt.Errorf("failed to inspect container after start: %w", err)
	}

	// 6. Return Instance
	return params.ProviderInstance{
		ProviderID: inspect.ID,
		Name:       bootstrapParams.Name,
		Status:     params.InstanceRunning,
		OSType:     bootstrapParams.OSType,
		OSArch:     bootstrapParams.OSArch,
		OSName:     "linux",
		OSVersion:  "unknown",
		Addresses:  containerToAddresses(inspect),
	}, nil
}

func containerToAddresses(c types.ContainerJSON) []params.Address {
	addrs := []params.Address{}
	if c.NetworkSettings == nil {
		return addrs
	}

	// Add IP from each network
	for _, settings := range c.NetworkSettings.Networks {
		if settings.IPAddress != "" {
			addrs = append(addrs, params.Address{
				Address: settings.IPAddress,
				Type:    params.PrivateAddress,
			})
		}
		if settings.GlobalIPv6Address != "" {
			addrs = append(addrs, params.Address{
				Address: settings.GlobalIPv6Address,
				Type:    params.PrivateAddress,
			})
		}
	}
	return addrs
}

func (p *Provider) DeleteInstance(ctx context.Context, instance string) error {
	// Instance arg here is the ProviderID (Container ID) or Name. 
	// Garm usually passes the ProviderID if available, or Name if not.
	// We can try to find by ID first, then name. But ContainerRemove handles both usually.
	
	err := p.DockerClient.ContainerRemove(ctx, instance, types.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: config.Config.RemoveVolumes,
	})
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to remove container %s: %w", instance, err)
	}
	return nil
}

func (p *Provider) GetInstance(ctx context.Context, instance string) (params.ProviderInstance, error) {
	json, err := p.DockerClient.ContainerInspect(ctx, instance)
	if err != nil {
		return params.ProviderInstance{}, fmt.Errorf("failed to inspect container %s: %w", instance, err)
	}

	return containerToInstance(json), nil
}

func (p *Provider) ListInstances(ctx context.Context, poolID string) ([]params.ProviderInstance, error) {
	filtersArgs := filters.NewArgs()
	filtersArgs.Add("label", fmt.Sprintf("%s=%s", spec.GarmControllerIDLabel, p.ControllerID))
	if poolID != "" {
		filtersArgs.Add("label", fmt.Sprintf("%s=%s", spec.GarmPoolIDLabel, poolID))
	}

	containers, err := p.DockerClient.ContainerList(ctx, types.ContainerListOptions{
		Filters: filtersArgs,
		All:     true, // List stopped ones too? Garm might want to know if they stopped.
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	instances := make([]params.ProviderInstance, len(containers))
	for i, c := range containers {
		// List returns a summary, not full inspect. We need to map what we have.
		// Or we can inspect each one if needed, but summary usually has labels and status.
		instances[i] = containerSummaryToInstance(c)
	}

	return instances, nil
}

func (p *Provider) RemoveAllInstances(ctx context.Context) error {
	filtersArgs := filters.NewArgs()
	filtersArgs.Add("label", fmt.Sprintf("%s=%s", spec.GarmControllerIDLabel, p.ControllerID))

	containers, err := p.DockerClient.ContainerList(ctx, types.ContainerListOptions{
		Filters: filtersArgs,
		All:     true,
	})
	if err != nil {
		return fmt.Errorf("failed to list containers for removal: %w", err)
	}

	for _, c := range containers {
		err := p.DockerClient.ContainerRemove(ctx, c.ID, types.ContainerRemoveOptions{
			Force:         true,
			RemoveVolumes: config.Config.RemoveVolumes,
		})
		if err != nil {
			slog.Error("failed to remove container", "id", c.ID, "error", err)
		}
	}
	return nil
}

func (p *Provider) Stop(ctx context.Context, instance string, force bool) error {
	// Garm calls this.
	timeout := 10 // seconds
	if force {
		timeout = 0
	}
	
	// ContainerStop expects *int for timeout in newer SDKs, or just int in older.
	// In v24 it's ContainerStop(ctx, containerID, ContainerStopOptions)
	// But let's check the signature for the version we imported.
	// Since we are using `github.com/docker/docker/client` v24.0.7:
	// interface: ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	
	// Wait, checking the exact signature for v24.0.7...
	// It seems to be `ContainerStop(ctx context.Context, containerID string, timeout container.StopOptions) error`
	
	stopOptions := container.StopOptions{
		Timeout: &timeout,
	}

	err := p.DockerClient.ContainerStop(ctx, instance, stopOptions)
	if err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}
	return nil
}

func (p *Provider) Start(ctx context.Context, instance string) error {
	err := p.DockerClient.ContainerStart(ctx, instance, types.ContainerStartOptions{})
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	return nil
}

// Helpers

func containerToInstance(c types.ContainerJSON) params.ProviderInstance {
	status := params.InstanceStatusUnknown
	if c.State != nil {
		if c.State.Running {
			status = params.InstanceRunning
		} else if c.State.Paused {
			status = params.InstanceStopped // or paused? Garm doesn't have paused.
		} else if c.State.Dead || c.State.OOMKilled {
			status = params.InstanceError
		} else {
			status = params.InstanceStopped
		}
	}

	return params.ProviderInstance{
		ProviderID: c.ID,
		Name:       c.Name, // This usually has a slash /name
		Status:     status,
		OSType:     params.OSType(c.Config.Labels[spec.GarmOSTypeLabel]),
		OSArch:     params.OSArch(c.Config.Labels[spec.GarmOSArchLabel]),
	}
}

func containerSummaryToInstance(c types.Container) params.ProviderInstance {
	status := params.InstanceStatusUnknown
	if c.State == "running" {
		status = params.InstanceRunning
	} else if c.State == "exited" {
		status = params.InstanceStopped
	}

	// Name in summary is a list /name
	name := ""
	if len(c.Names) > 0 {
		name = c.Names[0]
		// remove leading slash
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}
	}

	return params.ProviderInstance{
		ProviderID: c.ID,
		Name:       name,
		Status:     status,
		OSType:     params.OSType(c.Labels[spec.GarmOSTypeLabel]),
		OSArch:     params.OSArch(c.Labels[spec.GarmOSArchLabel]),
	}
}

// dockerConfig represents the structure of ~/.docker/config.json
type dockerConfig struct {
	Auths map[string]dockerAuthEntry `json:"auths"`
}

type dockerAuthEntry struct {
	Auth string `json:"auth"`
}

// getRegistryAuth returns the base64-encoded auth string for the registry of the given image.
// It reads from the Docker config file specified in config.Config.DockerConfigPath,
// or ~/.docker/config.json if not specified.
func getRegistryAuth(image string) string {
	configPath := config.Config.DockerConfigPath
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			slog.Debug("failed to get home dir for docker config", "error", err)
			return ""
		}
		configPath = home + "/.docker/config.json"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		slog.Debug("failed to read docker config", "path", configPath, "error", err)
		return ""
	}

	var cfg dockerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		slog.Debug("failed to parse docker config", "error", err)
		return ""
	}

	// Extract registry from image (e.g., "ghcr.io/user/image:tag" -> "ghcr.io")
	registryHost := "docker.io" // default
	if strings.Contains(image, "/") {
		parts := strings.SplitN(image, "/", 2)
		if strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") {
			registryHost = parts[0]
		}
	}

	if entry, ok := cfg.Auths[registryHost]; ok {
		// The auth in config.json is already base64(username:password)
		// Docker API expects base64(json(AuthConfig)), so we need to re-encode
		decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			slog.Debug("failed to decode auth from docker config", "error", err)
			return ""
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			slog.Debug("invalid auth format in docker config")
			return ""
		}
		authConfig := registry.AuthConfig{
			Username: parts[0],
			Password: parts[1],
		}
		authJSON, err := json.Marshal(authConfig)
		if err != nil {
			slog.Debug("failed to encode auth config", "error", err)
			return ""
		}
		return base64.URLEncoding.EncodeToString(authJSON)
	}

	return ""
}
