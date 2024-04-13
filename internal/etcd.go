package internal

import (
	"context"
	"echo-cicd/configs"
	"encoding/json"
	"errors"
	"fmt"
	"go.etcd.io/etcd/api/v3/mvccpb"
	etcd "go.etcd.io/etcd/client/v3"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type EtcdClient struct {
	client *etcd.Client
}

func NewClient(endpoints []string) (*EtcdClient, error) {
	client, err := etcd.New(etcd.Config{Endpoints: endpoints})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}
	if client == nil {
		return nil, errors.New("failed to generate client")
	}

	return &EtcdClient{client: client}, nil
}

func CollapseToErr[T any](_ T, err error) error {
	return err
}

func (client *EtcdClient) WatchForBuild(ctx context.Context, handler func(config PublishedBuild), async bool) {
	watcher := client.client.Watch(ctx, "echocicd", etcd.WithPrefix())
	buildKey := regexp.MustCompile("^echocicd/builds/[^/]+/exec$")
	for {
		select {
		case event := <-watcher:
			for _, e := range event.Events {
				if e.Type == mvccpb.PUT && buildKey.Match(e.Kv.Key) {
					slog.Debug("found a new build", "event", e)

					key := string(e.Kv.Key)[16:]
					key = key[:strings.Index(key, "/")]

					build, err := client.GetStoredConfig(ctx, key)
					if err != nil {
						slog.Error("failed to handle new build", "err", err)
						continue
					}

					slog.Debug("got new build, passing to handler")
					if async {
						go handler(*build)
					} else {
						handler(*build)
					}
				}
			}
		case <-ctx.Done():
			return
		}

	}
}

func (client *EtcdClient) GetStoredConfig(ctx context.Context, build string) (*PublishedBuild, error) {
	entries, err := client.client.Get(ctx, "echocicd/builds/"+build, etcd.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to query for build: %w", err)
	}

	keyMap := map[string]string{}
	for _, kv := range entries.Kvs {
		keyMap[string(kv.Key)] = string(kv.Value)
	}

	config := PublishedBuild{}
	if name, ok := keyMap["echocicd/builds/"+build+"/name"]; ok {
		config.Name = name
	} else {
		return nil, errors.New("failed to find build name")
	}

	if version, ok := keyMap["echocicd/builds/"+build+"/version"]; ok {
		config.Version = version
	} else {
		return nil, errors.New("failed to find build version")
	}

	if timestamp, ok := keyMap["echocicd/builds/"+build+"/timestamp"]; ok {
		t, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse timestamp %v: %w", timestamp, err)
		}
		config.Timestamp = int(t)
	} else {
		return nil, errors.New("failed to find build timestamp")
	}

	if tag, ok := keyMap["echocicd/builds/"+build+"/tag"]; ok {
		config.Tag = tag
	} else {
		return nil, errors.New("failed to find build tag")
	}

	if registry, ok := keyMap["echocicd/builds/"+build+"/registry"]; ok {
		config.Registry = registry
	} else {
		return nil, errors.New("failed to find build registry")
	}

	if execRaw, ok := keyMap["echocicd/builds/"+build+"/exec"]; ok {
		var execConfig configs.ExecProperties

		err = json.Unmarshal([]byte(execRaw), &execConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to parse exec config: %w", err)
		}

		config.Exec = execConfig
	} else {
		return nil, errors.New("failed to find build exec")
	}

	return &config, nil
}

func (client *EtcdClient) WriteBuildInfo(repo string, name string, hash string, tag string, registry string, config configs.ExecProperties) error {
	slog.Info("writing", "repo", repo, "name", name, "hash", hash, "tag", tag, "registry", registry, "client", client)

	j, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to serialise exec config: %w", err)
	}

	safeRepo := strings.ReplaceAll(repo, "/", "__")

	return errors.Join(
		CollapseToErr(client.client.Put(context.Background(), fmt.Sprintf("echocicd/builds/%v/name", safeRepo), name)),
		CollapseToErr(client.client.Put(context.Background(), fmt.Sprintf("echocicd/builds/%v/version", safeRepo), hash)),
		CollapseToErr(client.client.Put(context.Background(), fmt.Sprintf("echocicd/builds/%v/repo", safeRepo), repo)),
		CollapseToErr(client.client.Put(context.Background(), fmt.Sprintf("echocicd/builds/%v/timestamp", safeRepo), strconv.FormatInt(time.Now().UnixMilli(), 10))),
		CollapseToErr(client.client.Put(context.Background(), fmt.Sprintf("echocicd/builds/%v/tag", safeRepo), tag)),
		CollapseToErr(client.client.Put(context.Background(), fmt.Sprintf("echocicd/builds/%v/registry", safeRepo), registry)),
		CollapseToErr(client.client.Put(context.Background(), fmt.Sprintf("echocicd/builds/%v/exec", safeRepo), string(j))),
	)
}
