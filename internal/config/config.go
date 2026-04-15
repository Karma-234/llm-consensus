package config

import (
	"fmt"
	"os"
	"strings"

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
	Presets       map[string]Preset `yaml:"presets"`
	VirtualModels VirtualModels     `yaml:"virtual_models"`
	Output        struct {
		DefaultMode string `yaml:"default_mode"`
	} `yaml:"output"`
}

type Agent struct {
	Name     string `yaml:"name"`
	Role     string `yaml:"role"`
	Model    string `yaml:"model"`
	Provider string `yaml:"provider"`
	APIKey   string `yaml:"api_key"`
	BaseURL  string `yaml:"base_url,omitempty"`
}

type VirtualModels struct {
	Default string            `yaml:"default"`
	Presets map[string]string `yaml:"presets"`
}

type Preset struct {
	MaxRounds       int    `yaml:"max_rounds"`
	StrictUnanimity bool   `yaml:"strict_unanimity"`
	OutputMode      string `yaml:"output_mode"`
}

func (c *Config) GetPreset(modelName string) Preset {
	if presetName, ok := c.VirtualModels.Presets[modelName]; ok {
		if preset, found := c.Presets[presetName]; found {
			return preset
		}
	}

	name := strings.TrimPrefix(modelName, "llm-")
	if preset, ok := c.Presets[name]; ok {
		return preset
	}

	// Fallback to global defaults
	return Preset{
		MaxRounds:       c.Debate.MaxRounds,
		StrictUnanimity: c.Debate.StrictUnanimity,
		OutputMode:      c.Output.DefaultMode,
	}
}

func (c *Config) ValidatePresets() error {
	for name, preset := range c.Presets {
		if preset.MaxRounds <= 0 {
			return fmt.Errorf("preset '%s': max_rounds must be greater than 0, got %d", name, preset.MaxRounds)
		}
		if preset.OutputMode == "" {
			return fmt.Errorf("preset '%s': output_mode must be non-empty", name)
		}
	}
	return nil
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := os.ExpandEnv(string(data))
	var cfg Config
	err = yaml.Unmarshal([]byte(expanded), &cfg)
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidatePresets(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
