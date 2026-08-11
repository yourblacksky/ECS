package agentteams

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type RuntimeConfig struct {
	Metadata struct {
		Generation int64 `yaml:"generation"`
	} `yaml:"metadata"`
	Team struct {
		Name              string `yaml:"name"`
		LeaderName        string `yaml:"leaderName"`
		LeaderRuntimeName string `yaml:"leaderRuntimeName"`
		TeamRoomID        string `yaml:"teamRoomId"`
		Members           []struct {
			Name           string `yaml:"name"`
			RuntimeName    string `yaml:"runtimeName"`
			Role           string `yaml:"role"`
			MatrixUserID   string `yaml:"matrixUserId"`
			PersonalRoomID string `yaml:"personalRoomId"`
		} `yaml:"members"`
	} `yaml:"team"`
	Member struct {
		Name           string `yaml:"name"`
		RuntimeName    string `yaml:"runtimeName"`
		Role           string `yaml:"role"`
		Runtime        string `yaml:"runtime"`
		MatrixUserID   string `yaml:"matrixUserId"`
		PersonalRoomID string `yaml:"personalRoomId"`
	} `yaml:"member"`
	Matrix struct {
		AccessToken string `yaml:"accessToken"`
	} `yaml:"matrix"`
	Desired struct {
		Model struct {
			Model string `yaml:"model"`
		} `yaml:"model"`
		Identity     string   `yaml:"identity"`
		Soul         string   `yaml:"soul"`
		Instructions string   `yaml:"instructions"`
		Skills       []string `yaml:"skills"`
		Tools        []string `yaml:"tools"`
		State        string   `yaml:"state"`
	} `yaml:"desired"`
	Credentials struct {
		MatrixTokenEnv string `yaml:"matrixTokenEnv"`
	} `yaml:"credentials"`
}

func LoadRuntimeProfile(path string) (AgentProfile, RuntimeConfig, error) {
	if path == "" {
		return AgentProfile{}, RuntimeConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentProfile{}, RuntimeConfig{}, err
	}
	var runtimeConfig RuntimeConfig
	if err := yaml.Unmarshal(data, &runtimeConfig); err != nil {
		return AgentProfile{}, RuntimeConfig{}, fmt.Errorf("decode AgentTeams runtime config: %w", err)
	}
	name := runtimeConfig.Member.RuntimeName
	if name == "" {
		name = runtimeConfig.Member.Name
	}
	identity := runtimeConfig.Desired.Identity
	if runtimeConfig.Desired.Soul != "" {
		if identity != "" {
			identity += "\n"
		}
		identity += runtimeConfig.Desired.Soul
	}
	return AgentProfile{
		Name:          name,
		Role:          runtimeConfig.Member.Role,
		Identity:      identity,
		Instructions:  runtimeConfig.Desired.Instructions,
		Model:         runtimeConfig.Desired.Model.Model,
		AllowedSkills: append([]string(nil), runtimeConfig.Desired.Skills...),
		AllowedTools:  append([]string(nil), runtimeConfig.Desired.Tools...),
	}, runtimeConfig, nil
}
