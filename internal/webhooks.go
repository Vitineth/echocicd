package internal

import (
	"encoding/json"
	docker "github.com/docker/docker/client"
	"github.com/go-git/go-git/v5"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"slices"
)

type Repository struct {
	CloneUrl string `json:"clone_url"`
	FullName string `json:"full_name"`
}

type PushPayload struct {
	Repository Repository `json:"repository"`
	Ref        string     `json:"ref"`
}

type WebhookConfiguration struct {
	BuildersDir string
	Conn        *docker.Client
	Registry    *string
	PushAuth    *string
	Bind        string
	Etcd        *EtcdClient
}

func ProcessEvent(event PushPayload, configuration WebhookConfiguration) {
	temp, err := os.MkdirTemp("", "echocicd-")
	if err != nil {
		slog.Error("could not create temp dir to clone into", "err", err)
		return
	}

	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {
			slog.Error("failed to cleanup temp dir", "err", err)
		}
	}(temp)

	_, err = git.PlainClone(temp, false, &git.CloneOptions{
		URL:      event.Repository.CloneUrl,
		Progress: os.Stdout,
	})
	if err != nil {
		slog.Error("failed to clone project", "err", err)
		return
	}

	stat, err := os.Stat(path.Join(temp, ".deploy-config.toml"))
	if err != nil {
		slog.Error("could not find deploy config in this project", "err", err)
		return
	}

	if stat.IsDir() {
		slog.Error("deploy config was not a file", "stat", stat)
		return
	}

	err = BuildInDir(
		temp,
		".deploy-config.toml",
		configuration.BuildersDir,
		configuration.Conn,
		configuration.Registry,
		configuration.PushAuth,
		configuration.Etcd,
	)
	if err != nil {
		slog.Error("failed to build!", "err", err)
		return
	}

	slog.Info("successfully built and maybe pushed!", "ref", event.Ref, "repo", event.Repository)
}

func LaunchProcessor(events chan PushPayload, configuration WebhookConfiguration) {
	for {
		event := <-events
		ProcessEvent(event, configuration)
	}
}

func LaunchWebhookServer(configuration WebhookConfiguration, allowedRefs map[string][]string) {
	channel := make(chan PushPayload, 100)

	http.HandleFunc("/hook", func(writer http.ResponseWriter, request *http.Request) {
		slog.Info("got request", "request", request)
		defer func(Body io.ReadCloser) {
			err := Body.Close()
			if err != nil {
				slog.Error("could not close request body", "err, err")
			}
		}(request.Body)

		if request.Method != http.MethodPost {
			slog.Error("didn't get a push event", "method", request.Method)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(request.Body)
		if err != nil {
			slog.Error("failed to read body", "err", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}

		slog.Info("got payload", "payload", body)

		var payloadBody PushPayload
		err = json.Unmarshal(body, &payloadBody)
		if err != nil {
			slog.Error("failed to unmarshall body", "err", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}

		repoValidRefs, okRepo := allowedRefs[payloadBody.Repository.FullName]
		allValidRefs, okAll := allowedRefs["*"]
		if !okRepo && !okAll {
			slog.Error("ref was not allowlisted anywhere")
			writer.WriteHeader(http.StatusForbidden)
			return
		}

		if !slices.Contains(repoValidRefs, payloadBody.Ref) && !slices.Contains(allValidRefs, payloadBody.Ref) {
			slog.Error("ref was not allowlisted", "repo-valid", repoValidRefs, "all-valid", allValidRefs)
			writer.WriteHeader(http.StatusForbidden)
			return
		}

		githubEvent := request.Header.Get("X-GitHub-Event")
		if githubEvent != "push" {
			slog.Error("didn't get a push event", "event", githubEvent)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}

		channel <- payloadBody
		writer.WriteHeader(http.StatusOK)
	})

	go LaunchProcessor(channel, configuration)
	err := http.ListenAndServe(configuration.Bind, nil)
	if err != nil {
		slog.Error("failed to launch the server", "err", err)
	}
}
