package config

import (
	"os"
	"testing"
)

func TestLoadConfig_Valid(t *testing.T) {
	ResetConfigForTest()
	tmp := "test_config.json"
	raw := []byte(`{
		"server": {
			"host": "localhost",
			"port": 8080,
			"subpath": "/api",
			"jwtSecret": "mysecret"
		},
		"postgres": {
			"dsn": "postgres://user:pass@localhost:5432/db"
		},
		"redis": {
			"addr": "localhost:6379",
			"password": "",
			"db": 0
		},
		"llms": [
			{"name": "llama.cpp", "url": "http://localhost:8000"}
		],
		"searxng": {
			"url": "http://localhost:8080"
		}
	}`)
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		t.Fatalf("write tmp config: %v", err)
	}
	defer os.Remove(tmp)

	cfg, err := LoadConfig(tmp)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.Server.Host != "localhost" || cfg.Server.Port != 8080 {
		t.Errorf("unexpected server config: %+v", cfg.Server)
	}
	if cfg.LLMs[0].Name != "llama.cpp" {
		t.Errorf("llms config not loaded")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	ResetConfigForTest()
	_, err := LoadConfig("no_such_config.json")
	if err == nil {
		t.Errorf("expected error for missing file")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	ResetConfigForTest()
	tmp := "test_invalid_config.json"
	raw := []byte(`{this is not json}`)
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		t.Fatalf("write tmp config: %v", err)
	}
	defer os.Remove(tmp)

	_, err := LoadConfig(tmp)
	if err == nil {
		t.Errorf("expected error for malformed JSON")
	}
}

// Optional: Only if LoadConfig validates required fields!
// func TestLoadConfig_MissingRequiredFields(t *testing.T) {
// 	ResetConfigForTest()
// 	tmp := "test_missing_fields_config.json"
// 	raw := []byte(`{
// 		"server": {},
// 		"postgres": {},
// 		"redis": {},
// 		"llms": [],
// 		"searxng": {}
// 	}`)
// 	if err := os.WriteFile(tmp, raw, 0644); err != nil {
// 		t.Fatalf("write tmp config: %v", err)
// 	}
// 	defer os.Remove(tmp)
//
// 	_, err := LoadConfig(tmp)
// 	// If your loader validates required fields, this should fail.
// 	// If not, you can remove or adjust this test.
// }
