package internal

import (
	"echo-cicd/configs"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	docker "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"golang.org/x/net/context"
	"log/slog"
	"slices"
	"strconv"
)

type PublishedBuild struct {
	Name      string
	Version   string
	Timestamp int
	Tag       string
	Registry  string
	Exec      configs.ExecProperties
}

func ConvertToPorts(ports map[string]int) nat.PortSet {
	result := map[nat.Port]struct{}{}
	for k, _ := range ports {
		result[nat.Port(k)] = struct{}{}
	}
	return result
}

func CleanupExistingContainers(name string, conn *docker.Client) error {
	args := filters.NewArgs()
	args.Add("label", "echo-project="+name)
	args.Add("label", "managed-by=echocicd")
	containers, err := conn.ContainerList(context.Background(), container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return fmt.Errorf("failed to list containers with filters: %w", err)
	}

	timeout := 60

	for _, t := range containers {
		if t.State == "running" || t.State == "restarting" {
			// Need to stop existing containers
			err = conn.ContainerStop(context.Background(), t.ID, container.StopOptions{
				Timeout: &timeout,
			})
			if err != nil {
				return fmt.Errorf("failed to stop container %v: %w", t.ID, err)
			}
			slog.Info("stopped container", "container", t.ID)
		}
		err := conn.ContainerRemove(context.Background(), t.ID, container.RemoveOptions{
			Force: true,
		})
		slog.Info("removed container", "container", t.ID)
		if err != nil {
			return fmt.Errorf("faield to remove container %v: %w", t.ID, err)
		}
	}

	return nil
}

func RunContainer(build PublishedBuild, conn *docker.Client, registryAuths map[string]string) (*string, error) {
	auth := ""
	if v, ok := registryAuths[build.Registry]; ok {
		auth = v
	}

	img, _, err := conn.ImageInspectWithRaw(context.Background(), build.Tag)
	if err != nil {
		slog.Info("could not find container with error, trying to pull", "err", err, "tag", build.Tag)
		response, err := conn.ImagePull(context.Background(), build.Tag, types.ImagePullOptions{RegistryAuth: auth})
		if err != nil {
			return nil, fmt.Errorf("failed to pull docker image: %w", err)
		}

		err = ScanForDockerError(response)
		if err != nil {
			return nil, fmt.Errorf("failed to pull docker image: %w", err)
		}

		img, _, err = conn.ImageInspectWithRaw(context.Background(), build.Tag)
		if err != nil {
			return nil, fmt.Errorf("could not inspect container: %w", err)
		}
	}

	if img.Config == nil {
		return nil, fmt.Errorf("image has no config, cannot determine a start command")
	}

	command := slices.Concat(img.Config.Cmd, build.Exec.Args)
	binds := make([]string, 0)
	for _, volume := range build.Exec.Volumes {
		bind := volume.Host + ":" + volume.BindTo
		if volume.ReadOnly {
			bind += ":ro"
		}
		binds = append(binds, bind)
	}

	ports := map[nat.Port][]nat.PortBinding{}
	for cnter, host := range build.Exec.Ports {
		ports[nat.Port(cnter)] = []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: strconv.Itoa(host),
			},
		}
	}

	labels := map[string]string{
		"managed-by":   "echocicd",
		"echo-project": build.Name,
	}
	if build.Exec.Domain != nil {
		labels["domain:"+build.Exec.Domain.Host] = strconv.Itoa(build.Exec.Domain.Port)
	}

	create, err := conn.ContainerCreate(context.Background(), &container.Config{
		AttachStdin:  false,
		AttachStdout: false,
		AttachStderr: false,
		ExposedPorts: ConvertToPorts(build.Exec.Ports),
		Volumes:      map[string]struct{}{},
		Cmd:          command,
		Image:        img.ID,
		Labels:       labels,
	}, &container.HostConfig{
		Binds:        binds,
		PortBindings: ports,
	}, &network.NetworkingConfig{}, nil, "")

	if err != nil {
		return nil, fmt.Errorf("failed to create the container: %w", err)
	}

	slog.Info("created container", "id", create.ID, "warnings", create.Warnings)

	if len(create.Warnings) > 0 {
		slog.Warn("got warnings while creating container, not progressing in case this is a problem", "warnings", create.Warnings)
		return nil, fmt.Errorf("received warnings while creating container: %v", create.Warnings)
	}

	err = conn.ContainerStart(context.Background(), create.ID, container.StartOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to start created container: %w", err)
	}

	return &create.ID, nil
}
