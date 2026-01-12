package provider

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/cloudbase/garm-provider-common/params"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/mercedes-benz/garm-provider-docker/internal/spec"
	"github.com/mercedes-benz/garm-provider-docker/pkg/config"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockDockerClient is a mock of the DockerClient interface
type MockDockerClient struct {
	mock.Mock
}

func (m *MockDockerClient) ImagePull(ctx context.Context, ref string, options types.ImagePullOptions) (io.ReadCloser, error) {
	args := m.Called(ctx, ref, options)
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockDockerClient) ImageInspectWithRaw(ctx context.Context, imageID string) (types.ImageInspect, []byte, error) {
	args := m.Called(ctx, imageID)
	return args.Get(0).(types.ImageInspect), args.Get(1).([]byte), args.Error(2)
}

func (m *MockDockerClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *v1.Platform, containerName string) (container.CreateResponse, error) {
	args := m.Called(ctx, config, hostConfig, networkingConfig, platform, containerName)
	return args.Get(0).(container.CreateResponse), args.Error(1)
}

func (m *MockDockerClient) ContainerStart(ctx context.Context, containerID string, options types.ContainerStartOptions) error {
	args := m.Called(ctx, containerID, options)
	return args.Error(0)
}

func (m *MockDockerClient) ContainerRemove(ctx context.Context, containerID string, options types.ContainerRemoveOptions) error {
	args := m.Called(ctx, containerID, options)
	return args.Error(0)
}

func (m *MockDockerClient) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	args := m.Called(ctx, containerID)
	return args.Get(0).(types.ContainerJSON), args.Error(1)
}

func (m *MockDockerClient) ContainerList(ctx context.Context, options types.ContainerListOptions) ([]types.Container, error) {
	args := m.Called(ctx, options)
	return args.Get(0).([]types.Container), args.Error(1)
}

func (m *MockDockerClient) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	args := m.Called(ctx, containerID, options)
	return args.Error(0)
}

func TestCreateInstance(t *testing.T) {
	mockClient := new(MockDockerClient)
	p := &Provider{
		ControllerID: "test-controller",
		PoolID:       "test-pool",
		DockerClient: mockClient,
	}

	bootstrapParams := params.BootstrapInstance{
		Name:  "test-runner",
		Image: "ubuntu:latest",
		RepoURL: "https://github.com/org/repo",
		PoolID: "test-pool",
		Labels: []string{"label1"},
	}
	
	// Set up config defaults
	config.Config.Runtime = "sysbox-runc"
	config.Config.Network = "bridge"

	// Mock ImageInspect (simulate not found)
	mockClient.On("ImageInspectWithRaw", mock.Anything, "ubuntu:latest").Return(types.ImageInspect{}, []byte{}, client.NewErrNotFound("image not found"))

	// Mock ImagePull
	mockClient.On("ImagePull", mock.Anything, "ubuntu:latest", mock.Anything).Return(io.NopCloser(strings.NewReader("")), nil)

	// Mock ContainerCreate
	expectedLabels := spec.GetContainerLabels("test-controller", bootstrapParams)
	mockClient.On("ContainerCreate", mock.Anything, mock.MatchedBy(func(c *container.Config) bool {
		// Check if all expected labels are present
		for k, v := range expectedLabels {
			if c.Labels[k] != v {
				return false
			}
		}
		return c.Image == "ubuntu:latest" && c.Labels[spec.GarmInstanceNameLabel] == "test-runner"
	}), mock.MatchedBy(func(h *container.HostConfig) bool {
		return h.Runtime == "sysbox-runc"
	}), (*network.NetworkingConfig)(nil), (*v1.Platform)(nil), "test-runner").Return(container.CreateResponse{ID: "container-id"}, nil)

	// Mock ContainerStart
	mockClient.On("ContainerStart", mock.Anything, "container-id", mock.Anything).Return(nil)

	instance, err := p.CreateInstance(context.Background(), bootstrapParams)

	assert.NoError(t, err)
	assert.Equal(t, "container-id", instance.ProviderID)
	assert.Equal(t, params.InstanceRunning, instance.Status)
	
	mockClient.AssertExpectations(t)
}

func TestDeleteInstance(t *testing.T) {
	mockClient := new(MockDockerClient)
	p := &Provider{
		DockerClient: mockClient,
	}
	
	config.Config.RemoveVolumes = true

	mockClient.On("ContainerRemove", mock.Anything, "container-id", types.ContainerRemoveOptions{Force: true, RemoveVolumes: true}).Return(nil)

	err := p.DeleteInstance(context.Background(), "container-id")
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestListInstances(t *testing.T) {
	mockClient := new(MockDockerClient)
	p := &Provider{
		ControllerID: "test-controller",
		DockerClient: mockClient,
	}

	mockClient.On("ContainerList", mock.Anything, mock.MatchedBy(func(opts types.ContainerListOptions) bool {
		return opts.All == true // We ask for all
	})).Return([]types.Container{
		{
			ID: "container-1",
			Names: []string{"/test-runner"},
			State: "running",
			Labels: map[string]string{
				spec.GarmControllerIDLabel: "test-controller",
				spec.GarmInstanceNameLabel: "test-runner",
			},
		},
	}, nil)

	instances, err := p.ListInstances(context.Background(), "pool-id")
	assert.NoError(t, err)
	assert.Len(t, instances, 1)
	assert.Equal(t, "container-1", instances[0].ProviderID)
	assert.Equal(t, params.InstanceRunning, instances[0].Status)
	
	mockClient.AssertExpectations(t)
}
