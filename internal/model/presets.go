package model

// Preset is a known provider endpoint. Name is the English provider name;
// the web client shows its own label for the ID.
type Preset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Protocol string `json:"protocol"`
}

// Presets lists the known provider endpoints.
var Presets = []Preset{
	{"zhipu-coding", "Zhipu Coding Plan", "https://open.bigmodel.cn/api/coding/paas/v4", "openai"},
	{"zhipu", "Zhipu", "https://open.bigmodel.cn/api/paas/v4", "openai"},
	{"deepseek", "DeepSeek", "https://api.deepseek.com/v1", "openai"},
	{"openai", "OpenAI", "https://api.openai.com/v1", "openai"},
	{"claude", "Claude", "https://api.anthropic.com/v1", "anthropic"},
	{"ollama", "Ollama", "http://127.0.0.1:11434/v1", "openai"},
	{"custom", "Custom", "", "openai"},
}
