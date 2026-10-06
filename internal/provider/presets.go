package provider

// Preset is a known third-party API that speaks the OpenAI chat-completions
// protocol. Choosing one fills the endpoint and where to get a key; model
// names are read from the provider's /models after the first test, so they
// never go stale here.
type Preset struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Group   string `json:"group"` // gateway | global | china | local
	BaseURL string `json:"base_url"`
	KeyURL  string `json:"key_url,omitempty"` // where to create an API key
	KeyEnv  string `json:"key_env,omitempty"` // the usual environment variable
	NeedKey bool   `json:"need_key"`
	Note    string `json:"note,omitempty"`
}

// Presets lists the catalog.
func Presets() []Preset {
	return []Preset{
		{ID: "openrouter", Name: "OpenRouter", Group: "gateway", BaseURL: "https://openrouter.ai/api/v1", KeyURL: "https://openrouter.ai/keys", KeyEnv: "OPENROUTER_API_KEY", NeedKey: true, Note: "Một key dùng hàng trăm model của nhiều hãng, có model miễn phí"},
		{ID: "gemini", Name: "Google Gemini", Group: "global", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", KeyURL: "https://aistudio.google.com/apikey", KeyEnv: "GEMINI_API_KEY", NeedKey: true, Note: "Có hạn mức miễn phí"},
		{ID: "deepseek", Name: "DeepSeek", Group: "global", BaseURL: "https://api.deepseek.com/v1", KeyURL: "https://platform.deepseek.com/api_keys", KeyEnv: "DEEPSEEK_API_KEY", NeedKey: true, Note: "Rẻ, mạnh về code"},
		{ID: "groq", Name: "Groq", Group: "global", BaseURL: "https://api.groq.com/openai/v1", KeyURL: "https://console.groq.com/keys", KeyEnv: "GROQ_API_KEY", NeedKey: true, Note: "Rất nhanh, có hạn mức miễn phí"},
		{ID: "xai", Name: "xAI Grok", Group: "global", BaseURL: "https://api.x.ai/v1", KeyURL: "https://console.x.ai", KeyEnv: "XAI_API_KEY", NeedKey: true},
		{ID: "mistral", Name: "Mistral", Group: "global", BaseURL: "https://api.mistral.ai/v1", KeyURL: "https://console.mistral.ai/api-keys", KeyEnv: "MISTRAL_API_KEY", NeedKey: true},
		{ID: "together", Name: "Together AI", Group: "gateway", BaseURL: "https://api.together.xyz/v1", KeyURL: "https://api.together.ai/settings/api-keys", KeyEnv: "TOGETHER_API_KEY", NeedKey: true, Note: "Model mã nguồn mở"},
		{ID: "fireworks", Name: "Fireworks", Group: "gateway", BaseURL: "https://api.fireworks.ai/inference/v1", KeyURL: "https://fireworks.ai/account/api-keys", KeyEnv: "FIREWORKS_API_KEY", NeedKey: true, Note: "Model mã nguồn mở"},
		{ID: "cerebras", Name: "Cerebras", Group: "global", BaseURL: "https://api.cerebras.ai/v1", KeyURL: "https://cloud.cerebras.ai", KeyEnv: "CEREBRAS_API_KEY", NeedKey: true, Note: "Rất nhanh, có hạn mức miễn phí"},
		{ID: "perplexity", Name: "Perplexity", Group: "global", BaseURL: "https://api.perplexity.ai", KeyURL: "https://www.perplexity.ai/settings/api", KeyEnv: "PERPLEXITY_API_KEY", NeedKey: true, Note: "Trả lời kèm tìm kiếm web"},
		{ID: "nvidia", Name: "NVIDIA NIM", Group: "gateway", BaseURL: "https://integrate.api.nvidia.com/v1", KeyURL: "https://build.nvidia.com", KeyEnv: "NVIDIA_API_KEY", NeedKey: true, Note: "Có credit miễn phí"},
		{ID: "zai", Name: "Z.ai (GLM)", Group: "china", BaseURL: "https://api.z.ai/api/paas/v4", KeyURL: "https://z.ai/manage-apikey/apikey-list", KeyEnv: "ZAI_API_KEY", NeedKey: true, Note: "GLM, giá rẻ, mạnh về code"},
		{ID: "moonshot", Name: "Moonshot (Kimi)", Group: "china", BaseURL: "https://api.moonshot.ai/v1", KeyURL: "https://platform.moonshot.ai/console/api-keys", KeyEnv: "MOONSHOT_API_KEY", NeedKey: true},
		{ID: "minimax", Name: "MiniMax", Group: "china", BaseURL: "https://api.minimax.io/v1", KeyURL: "https://www.minimax.io/platform", KeyEnv: "MINIMAX_API_KEY", NeedKey: true},
		{ID: "qwen", Name: "Qwen (Alibaba)", Group: "china", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", KeyURL: "https://modelstudio.console.alibabacloud.com", KeyEnv: "DASHSCOPE_API_KEY", NeedKey: true},
		{ID: "siliconflow", Name: "SiliconFlow", Group: "china", BaseURL: "https://api.siliconflow.com/v1", KeyURL: "https://cloud.siliconflow.com/account/ak", KeyEnv: "SILICONFLOW_API_KEY", NeedKey: true},
		{ID: "ollama", Name: "Ollama", Group: "local", BaseURL: "http://localhost:11434/v1", NeedKey: false, Note: "Model chạy trên máy, miễn phí"},
		{ID: "lmstudio", Name: "LM Studio", Group: "local", BaseURL: "http://localhost:1234/v1", NeedKey: false, Note: "Model chạy trên máy, miễn phí"},
	}
}

// PresetByID finds a preset.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
