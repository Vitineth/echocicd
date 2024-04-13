package configs

import (
	"fmt"
	"github.com/BurntSushi/toml"
	"os"
)

type GlobalProperties struct {
	Name string `toml:"name"`
	Repo string `toml:"repo"`
}

type BuilderProperties struct {
	Id      string                 `toml:"id"`
	Exclude []string               `toml:"exclude"`
	Args    map[string]interface{} `toml:"args"`
}

type VolumeMount struct {
	ReadOnly bool   `toml:"readonly"`
	Host     string `toml:"host"`
	BindTo   string `toml:"bindTo"`
	Mode     string `toml:"mode"`
}

type DomainConfiguration struct {
	Port int    `toml:"port" json:"port"`
	Host string `toml:"host" json:"host"`
}

type ExecProperties struct {
	Args    []string             `toml:"args" json:"args"`
	Ports   map[string]int       `toml:"ports" json:"ports"`
	Volumes []VolumeMount        `toml:"volumes" json:"volumes"`
	Domain  *DomainConfiguration `toml:"domain" json:"domain"`
}

type DeployConfig struct {
	Global  GlobalProperties  `toml:"global"`
	Builder BuilderProperties `toml:"builder"`
	Exec    ExecProperties    `toml:"exec"`
}

func LoadDeployConfigFromFile(path string) (*DeployConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read deploy config: %w", err)
	}

	return LoadDeployConfigFromString(string(content))
}

func LoadDeployConfigFromString(content string) (*DeployConfig, error) {
	var conf DeployConfig
	if _, err := toml.Decode(content, &conf); err != nil {
		return nil, fmt.Errorf("failed to parse toml config: %w", err)
	}

	return &conf, nil
}
