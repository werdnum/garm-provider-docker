# Garm Provider Docker

This is an external provider for [Garm](https://github.com/cloudbase/garm) that creates GitHub Actions runners as local Docker containers. It is designed to work with [Sysbox](https://github.com/nestybox/sysbox) to enable running Docker-in-Docker workloads securely and efficiently within the runners.

## Requirements

- Docker Engine
- [Sysbox](https://github.com/nestybox/sysbox) (Recommended for Docker-in-Docker support)
- [Garm](https://github.com/cloudbase/garm)

## Configuration

Create a config file (e.g., `config.yaml` or `config.toml`):

```yaml
docker_host: "unix:///var/run/docker.sock"
runtime: "sysbox-runc" # Set to "runc" if you don't use Sysbox
network: "bridge"
remove_volumes: true
```

## Usage

1. Build the provider:
   ```bash
   go build -o garm-provider-docker cmd/garm-provider-docker/main.go
   ```

2. Register the provider in `garm`'s `config.toml`:

   ```toml
   [[provider]]
   name = "docker"
   description = "Local Docker Provider"
   provider_type = "external"
   [provider.external]
   config_file = "/path/to/garm-provider-docker/config.yaml"
   executable = "/path/to/garm-provider-docker/garm-provider-docker"
   ```

3. Create a pool in Garm using this provider.
