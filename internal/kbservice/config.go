package kbservice

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the OpenAI-compatible endpoint settings used for import
// embedding and question answering. apiKey is the in-memory plaintext; on disk
// it is encrypted (Windows DPAPI) into api_key_cipher so secrets are not stored
// raw.
type Config struct {
	Server     string `json:"server"` // base URL, must end in /v1
	APIKey     string `json:"-"`      // decrypted in memory only
	ChatModel  string `json:"chat_model,omitempty"`
	EmbedModel string `json:"embed_model,omitempty"`
	// Mock runs the built-in fake OpenAI server so no key/models are needed.
	Mock bool `json:"mock,omitempty"`
	// APIKeyCipher is the encrypted form of APIKey as persisted by SaveConfig.
	APIKeyCipher string `json:"api_key_cipher,omitempty"`
}

func configFilename(root string) string {
	return filepath.Join(root, "config.json")
}

// LoadConfig reads the endpoint config from the data root. Missing file returns
// a zero Config (not an error).
func LoadConfig(root string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(configFilename(root))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	_ = json.Unmarshal(data, &cfg)
	// Recover the plaintext key from its cipher, staying tolerant if it cannot.
	if cfg.APIKeyCipher != "" {
		if k, cerr := decryptSecret(cfg.APIKeyCipher); cerr == nil {
			cfg.APIKey = k
		}
	}
	if cfg.Server != "" && !strings.HasSuffix(cfg.Server, "/v1") && !strings.HasSuffix(cfg.Server, "/v1/") {
		cfg.Server = cfg.Server + "/v1"
	}
	return cfg, nil
}

// SaveConfig persists the endpoint config, encrypting the API key before it
// touches disk.
func SaveConfig(root string, cfg Config) error {
	if cfg.APIKey != "" {
		cipher, cerr := encryptSecret(cfg.APIKey)
		if cerr != nil {
			return cerr
		}
		cfg.APIKeyCipher = cipher
	} else {
		cfg.APIKeyCipher = ""
	}
	cfg.APIKey = "" // never write the plaintext
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configFilename(root), data, 0o600)
}

// Configured reports whether a usable (non-mock) endpoint is present.
func (c Config) Configured() bool {
	if c.Mock {
		return true
	}
	return c.Server != "" && (c.ChatModel != "" || c.EmbedModel != "")
}
