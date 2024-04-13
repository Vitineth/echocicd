package main

import (
	"echo-cicd/configs"
	"echo-cicd/internal"
	"encoding/json"
	"errors"
	"github.com/alecthomas/kong"
	docker "github.com/docker/docker/client"
	"log/slog"
	"os"
)

type Agent struct {
	DockerHost   string            `help:"The docker host, defaults to unix:///var/run/docker.sock" default:"unix:///var/run/docker.sock"`
	RegistryAuth map[string]string `help:"Authentication strings to use when authenticating against various registries"`
}

func (a Agent) Run() error {
	etcd, err := internal.NewClient(cli.EtcdEndpoints)
	conn, _ := docker.NewClientWithOpts(docker.WithHost(a.DockerHost))
	if err != nil {
		slog.Error("could not connect to etcd server", "err", err)
		return err
	}
	internal.LaunchAgent(etcd, conn, a.RegistryAuth)
	return nil
}

type Webhook struct {
	PushAuth        *string `help:"The authentication to pass to the push command if required"`
	Registry        *string `help:"The registry to which this image will be pushed if relevant"`
	DockerHost      string  `help:"The docker host, defaults to unix:///var/run/docker.sock" default:"unix:///var/run/docker.sock"`
	BuilderDir      string  `help:"The folder in which to look for builders, defaults to /builders" default:"/builders"`
	BindAddress     string  `help:"The address and port on which the server should bind" default:"0.0.0.0:15342"`
	AllowedRefsFile []byte  `help:"The file containing the JSON list of allowed refs" type:"filecontent"`
}

func (w Webhook) Run() error {
	conn, err := docker.NewClientWithOpts(docker.WithHost(w.DockerHost))
	if err != nil {
		slog.Error("failed to build - could not connect to docker host", "err", err)
		return err
	}

	var allowedRefs map[string][]string
	err = json.Unmarshal(w.AllowedRefsFile, &allowedRefs)
	if err != nil {
		slog.Error("could not parse the allowed refs file - the json unmarshall could not validate", "err", err)
		return err
	}

	etcd, err := internal.NewClient(cli.EtcdEndpoints)
	if err != nil {
		slog.Error("could not connect to etcd server", "err", err)
		return err
	}

	config := internal.WebhookConfiguration{
		BuildersDir: w.BuilderDir,
		Conn:        conn,
		Registry:    w.Registry,
		PushAuth:    w.PushAuth,
		Bind:        w.BindAddress,
		Etcd:        etcd,
	}

	slog.Info("launching webhook server", "bind", w.BindAddress)
	internal.LaunchWebhookServer(config, allowedRefs)
	return nil
}

type Build struct {
	PushAuth     *string `help:"The authentication to pass to the push command if required"`
	Registry     *string `help:"The registry to which this image will be pushed if relevant"`
	DockerHost   string  `help:"The docker host, defaults to unix:///var/run/docker.sock" default:"unix:///var/run/docker.sock"`
	BuilderDir   string  `help:"The folder in which to look for builders, defaults to /builders" default:"/builders"`
	DeployConfig string  `help:"The deploy config file to use, defaults to deploy-config.toml" default:"deploy-config.toml" type:"path"`
}

func (receiver Build) Run() error {
	conn, err := docker.NewClientWithOpts(docker.WithHost(receiver.DockerHost))
	if err != nil {
		slog.Error("failed to build - could not connect to docker host", "err", err)
		return err
	}

	config, err := configs.LoadDeployConfigFromFile(receiver.DeployConfig)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Error("failed to process deploy config - file could not be found")
		} else {
			slog.Error("failed to process deploy config - error loading file: %w", err)
		}
		return err
	}

	etcd, err := internal.NewClient(cli.EtcdEndpoints)
	if err != nil {
		slog.Error("could not connect to etcd server", "err", err)
		return err
	}

	err = internal.BuildFromConfig(*config, cli.Build.BuilderDir, cli.WorkingDir, conn, receiver.Registry, receiver.PushAuth, etcd)
	if err != nil {
		slog.Error("failed to build", "err", err)
		return err
	}

	return nil
}

var cli struct {
	EtcdEndpoints []string `help:"The etcd endpoints to which values should be read / written"`
	WorkingDir    string   `help:"The directory to operate in" default:"."`
	Debug         bool     `help:"Enable debug mode - adds verbose logging"`
	Build         Build    `cmd:"" help:"Trigger a build in the current folder"`
	WebhookServer Webhook  `cmd:"" help:"Launch the webhook server"`
	Agent         Agent    `cmd:"" help:"Launch the agent which will be responsible for starting containers"`
}

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run(nil)
	ctx.FatalIfErrorf(err)
}
