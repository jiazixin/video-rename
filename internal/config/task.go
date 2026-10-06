package config

import (
	"strconv"
	"time"
)

// TaskConfig is built from persisted model and frame snapshots for one run.
// Deployment paths and login credentials never enter the processing pipeline.
type TaskConfig struct {
	Server          ServerConfig
	Prompt          string
	FailureKeywords []string
	Workers         int
	Sampling        SamplingConfig
	Images          ImagesConfig
	Endpoint        string
	TimeoutDuration time.Duration
}

// PromptHash 返回 prompt 的短哈希,用于日志复现;不记录 prompt 原文。
func (c *TaskConfig) PromptHash() string {
	return shortHash(c.Prompt)
}

func shortHash(s string) string {
	var h uint64 = 14695981039346656037
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return strconv.FormatUint(h, 16)
}
