package internal

import (
	"context"
	docker "github.com/docker/docker/client"
	"log/slog"
)

func LaunchAgent(client *EtcdClient, conn *docker.Client, registryAuth map[string]string) {
	slog.Info("waiting for new builds!")
	client.WatchForBuild(context.Background(), func(config PublishedBuild) {
		slog.Info("received a new build", "build", config.Name, "version", config.Version)

		err := CleanupExistingContainers(config.Name, conn)
		if err != nil {
			slog.Error("failed to clean up previous containers", "name", config.Name, "version", config.Version, "err", err)
			return
		}

		id, err := RunContainer(config, conn, registryAuth)
		if err != nil {
			slog.Error("failed to run new container", "name", config.Name, "version", config.Version, "err", err)
			return
		}

		slog.Info("new container launched!", "name", config.Name, "version", config.Version, "id", *id)
	}, false)
}
