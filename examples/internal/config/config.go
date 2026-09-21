// Package config 读取 examples/config.yaml，供示例程序共享。
//
// 只用 stdlib 解析扁平的 key: value 配置，不引入 YAML 依赖，
// 与「零依赖核心」的取向保持一致。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultPath 是相对 module 根的默认配置路径。
const DefaultPath = "examples/config.yaml"

// Config 是示例程序的本地配置。
type Config struct {
	APIKey        string
	BaseURL       string
	Model         string
	System        string
	MaxIterations int
	ContextBudget int
	Timeout       string
}

// Load 读取配置文件；path 为空时使用 DefaultPath。
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	defer f.Close()

	cfg := &Config{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "api_key":
			cfg.APIKey = val
		case "base_url":
			cfg.BaseURL = val
		case "model":
			cfg.Model = val
		case "system":
			cfg.System = val
		case "max_iterations":
			cfg.MaxIterations = atoi(val)
		case "context_budget":
			cfg.ContextBudget = atoi(val)
		case "timeout":
			cfg.Timeout = val
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	return cfg, nil
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
