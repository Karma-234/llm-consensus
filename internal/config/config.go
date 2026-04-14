package config

import (
	"os"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"server"`
	Agents []Agent `yaml:"agents"`
	Debate struct {
		MaxRounds          int  `yaml:"max_rounds"`
		StrictUnanimity    bool `yaml:"strict_unanimity"`
		FallbackOnDeadlock bool `yaml:"fallback_on_deadlock"`
	} `yaml:"debate"`
	Output struct {
		DefaultMode string `yaml:"default_mode"`
	} `yaml:"output"`
}

type Agent struct {
	Name     string `yaml:"name"`
	Role     string `yaml:"role"`
	Model    string `yaml:"model"`
	Provider string `yaml:"provider"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}
