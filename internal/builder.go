package internal

import (
	"bufio"
	"context"
	"echo-cicd/configs"
	"echo-cicd/util"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types"
	docker "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/go-git/go-git/v5"
	"github.com/plus3it/gorecurcopy"
	"github.com/xeipuuv/gojsonschema"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
)

type ErrorLine struct {
	Error       string      `json:"error"`
	ErrorDetail ErrorDetail `json:"errorDetail"`
}

type ErrorDetail struct {
	Message string `json:"message"`
}

func BuildInDir(directory string, expectedFileName string, buildersDir string, conn *docker.Client, registry *string, auth *string, etcd *EtcdClient) error {
	config, err := configs.LoadDeployConfigFromFile(path.Join(directory, expectedFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Error("failed to process deploy config - file could not be found")
		} else {
			slog.Error("failed to process deploy config - error loading file: %w", err)
		}
		return err
	}

	return BuildFromConfig(*config, buildersDir, directory, conn, registry, auth, etcd)
}

func BuildFromConfig(config configs.DeployConfig, buildersDir string, workingDir string, conn *docker.Client, registry *string, auth *string, etcd *EtcdClient) error {
	// Make sure this is a git repo so we can use a hash for identification
	repo, err := git.PlainOpen(workingDir)
	if err != nil {
		return fmt.Errorf("cannot build - is not a git repo: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("cannot build - could not get head: %w", err)
	}

	hash := head.Hash().String()

	// Check the builder exists
	builderDir := path.Join(buildersDir, config.Builder.Id)
	slog.Info("looking for builder", "path", builderDir, "id", config.Builder.Id)
	stat, err := os.Stat(builderDir)
	if err != nil {
		return fmt.Errorf("the builder could not be loaded by id: %w", err)
	}

	if !stat.IsDir() {
		return fmt.Errorf("the builder was invalid, expected dir")
	}
	// Load the CUE file from the builder if its present
	//   Validate the args in the config
	if err = ValidateArgsIfPresent(config, builderDir, config.Builder.Args); err != nil {
		return fmt.Errorf("failed to validate args: %w", err)
	}

	// Read .dockerignore from the project, in case we replace it now
	var existingIgnoreContent *string = nil
	existingIgnoreBytes, err := os.ReadFile(path.Join(workingDir, ".dockerignore"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not create ignore file: %w", err)
		}
	} else {
		v := string(existingIgnoreBytes)
		existingIgnoreContent = &v
	}

	// Copy files from the builder into the working directory
	dir, err := os.ReadDir(builderDir)
	if err != nil {
		return fmt.Errorf("failed to load directory listing: %w", err)
	}

	for _, entry := range dir {
		err = nil
		if entry.IsDir() {
			err = gorecurcopy.CopyDirectory(path.Join(builderDir, entry.Name()), path.Join(workingDir, entry.Name()))
		} else {
			err = gorecurcopy.Copy(path.Join(builderDir, entry.Name()), path.Join(workingDir, entry.Name()))
		}

		if err != nil {
			return fmt.Errorf("failed to copy from builder to working dir: %w", err)
		}
	}

	// Create .dockerignore file based on the config, merging values if required
	ignoreFile, err := os.OpenFile(path.Join(workingDir, ".dockerignore"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to create new dockerignore file: %w", err)
	}

	compiledIgnore := "\n\n"
	if existingIgnoreContent != nil {
		compiledIgnore += *existingIgnoreContent
	}

	_, err = ignoreFile.Write([]byte(compiledIgnore))
	if err != nil {
		return fmt.Errorf("failed to write to dockerignore: %w", err)
	}

	// Run docker build

	tag := ""
	if registry != nil {
		tag = *registry + "/"
	}
	tag += config.Global.Name
	slog.Info("tag prepared", "tag", tag)

	content, err := json.Marshal(config.Builder.Args)
	if err != nil {
		return fmt.Errorf("failed to marshall builder args: %w", err)
	}
	contentAsString := string(content)

	err = BuildImage(conn, workingDir, tag, hash, contentAsString)
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	if registry != nil {
		authReal := ""
		if auth != nil {
			authReal = *auth
		} else {
			if v, ok := os.LookupEnv("PUSH_AUTH"); ok {
				authReal = v
			}
		}

		response, err := conn.ImagePush(context.Background(), tag, types.ImagePushOptions{
			RegistryAuth: authReal,
		})
		if err != nil {
			return fmt.Errorf("failed to push image to registry: %w", err)
		}

		err = ScanForDockerError(response)
		if err != nil {
			return err
		}
	}

	rv := ""
	if registry != nil {
		rv = *registry
	}

	err = etcd.WriteBuildInfo(config.Global.Repo, config.Global.Name, hash, tag+":"+hash, rv, config.Exec)
	if err != nil {
		return fmt.Errorf("failed to write details to etcd: %w", err)
	}

	return nil
}
func ScanForDockerError(reader io.ReadCloser) error {
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			slog.Error("failed to close build response body", "err", err)
		}
	}(reader)

	var lastLine string

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		lastLine = scanner.Text()
		fmt.Println(scanner.Text())
	}

	errLine := &ErrorLine{}
	err := json.Unmarshal([]byte(lastLine), errLine)
	if err != nil {
		return fmt.Errorf("failed to unmarshall the final line: %w", err)
	}
	if errLine.Error != "" {
		return fmt.Errorf("failed: %w", errors.New(errLine.Error))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to scan: %w", err)
	}

	return nil
}

func BuildImage(conn *docker.Client, workingDir string, tag string, hash string, argsAsString string) error {
	tar, err := archive.TarWithOptions(workingDir, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("failed to tar working directory: %w", err)
	}
	defer func(tar io.ReadCloser) {
		err := tar.Close()
		if err != nil {
			slog.Error("failed to close tar", "err", err)
		}
	}(tar)

	response, err := conn.ImageBuild(context.Background(), tar, types.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{tag + ":" + hash, tag + ":latest"},
		BuildArgs: map[string]*string{
			"BUILDER_ARGS": &argsAsString,
		},
		Remove: true,
	})
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}
	err = ScanForDockerError(response.Body)
	if err != nil {
		return err
	}

	return nil
}

func ValidateArgsIfPresent(config configs.DeployConfig, builderDir string, args map[string]interface{}) error {
	content, err := os.ReadFile(path.Join(builderDir, "args.schema.json"))
	if err != nil {
		// Doesn't matter if files don't exist - just means we don't validate
		if errors.Is(err, os.ErrNotExist) {
			slog.Info("test file does not exist, skipping", "path", path.Join(builderDir, "args.schema.json"))
			return nil
		}

		// Everything else is a problem though
		return fmt.Errorf("could not load argument validate: %w", err)
	}

	schema := gojsonschema.NewStringLoader(string(content))
	data := gojsonschema.NewGoLoader(config.Builder.Args)

	validate, err := gojsonschema.Validate(schema, data)
	if err != nil {
		return fmt.Errorf("could not validate arguments: %w", err)
	}

	if !validate.Valid() {
		output := strings.Join(util.Map(validate.Errors(), func(i gojsonschema.ResultError) string {
			return i.String()
		}), "; ")
		return fmt.Errorf("args were invalid: %v", output)
	}

	return nil
}
