package spec

import (
	"fmt"
	"net/url"
	"strings"
	"github.com/cloudbase/garm-provider-common/params"
	"github.com/mercedes-benz/garm-provider-docker/pkg/config"
)

const (
	GarmInstanceNameLabel = "garm.runner/instance-name"
	GarmControllerIDLabel = "garm.runner/controller-id"
	GarmPoolIDLabel       = "garm.runner/pool-id"
	GarmFlavorLabel       = "garm.runner/flavor"
	GarmOSTypeLabel       = "garm.runner/os-type"
	GarmOSArchLabel       = "garm.runner/os-arch"
)

type GitHubScopeDetails struct {
	BaseURL    string
	Repo       string
	Org        string
	Enterprise string
}

func GetRunnerEnvs(bootstrapParams params.BootstrapInstance) ([]string, error) {
	gitHubScope, err := ExtractGitHubScopeDetails(bootstrapParams.RepoURL)
	if err != nil {
		return nil, err
	}

	envs := []string{
		fmt.Sprintf("RUNNER_ORG=%s", gitHubScope.Org),
		fmt.Sprintf("RUNNER_REPO=%s", gitHubScope.Repo),
		fmt.Sprintf("RUNNER_ENTERPRISE=%s", gitHubScope.Enterprise),
		fmt.Sprintf("RUNNER_GROUP=%s", bootstrapParams.GitHubRunnerGroup),
		fmt.Sprintf("RUNNER_NAME=%s", bootstrapParams.Name),
		fmt.Sprintf("RUNNER_LABELS=%s", strings.Join(bootstrapParams.Labels, ",")),
		"RUNNER_NO_DEFAULT_LABELS=true",
		"DISABLE_RUNNER_UPDATE=true",
		"RUNNER_WORKDIR=/runner/_work/",
		fmt.Sprintf("GITHUB_URL=%s", gitHubScope.BaseURL),
		"RUNNER_EPHEMERAL=true",
		"RUNNER_TOKEN=dummy", // Garm handles the token via metadata/callbacks usually, or it's passed differently. k8s provider sets it to dummy.
		fmt.Sprintf("METADATA_URL=%s", bootstrapParams.MetadataURL),
		fmt.Sprintf("BEARER_TOKEN=%s", bootstrapParams.InstanceToken),
		fmt.Sprintf("CALLBACK_URL=%s", bootstrapParams.CallbackURL),
		// JIT config enabled might be needed if supported by the image
	}
	return envs, nil
}

func ExtractGitHubScopeDetails(gitRepoURL string) (GitHubScopeDetails, error) {
	if gitRepoURL == "" {
		return GitHubScopeDetails{}, fmt.Errorf("no gitRepoURL supplied")
	}
	u, err := url.Parse(gitRepoURL)
	if err != nil {
		return GitHubScopeDetails{}, fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme == "" || u.Host == "" {
		return GitHubScopeDetails{}, fmt.Errorf("invalid URL: %s", gitRepoURL)
	}

	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")

	scope := GitHubScopeDetails{
		BaseURL: u.Scheme + "://" + u.Host,
	}

	switch {
	case len(pathParts) == 1:
		scope.Org = pathParts[0]
	case len(pathParts) == 2 && pathParts[0] == "enterprises":
		scope.Enterprise = pathParts[1]
	case len(pathParts) == 2:
		scope.Org = pathParts[0]
		scope.Repo = pathParts[1]
	default:
		return GitHubScopeDetails{}, fmt.Errorf("URL does not match the expected patterns")
	}

	return scope, nil
}

func GetContainerLabels(controllerID string, bootstrapParams params.BootstrapInstance) map[string]string {
	labels := make(map[string]string)
	labels[GarmInstanceNameLabel] = bootstrapParams.Name
	labels[GarmControllerIDLabel] = controllerID
	labels[GarmPoolIDLabel] = bootstrapParams.PoolID
	labels[GarmFlavorLabel] = bootstrapParams.Flavor
	labels[GarmOSTypeLabel] = string(bootstrapParams.OSType)
	labels[GarmOSArchLabel] = string(bootstrapParams.OSArch)
	return labels
}

// GetHostConfigRuntime returns the runtime string to be used for the container
func GetHostConfigRuntime() string {
	return config.Config.Runtime
}
