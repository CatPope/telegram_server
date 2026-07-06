package adminui

import "testing"

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("ADMINUI_LISTEN_ADDR", "")
	t.Setenv("ADMINUI_PASSWORD", "pw")
	t.Setenv("ADMINUI_API_KEY", "key")
	t.Setenv("TELEGRAM_SERVER_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:8081" {
		t.Errorf("ListenAddr = %q, want default", cfg.ListenAddr)
	}
	if cfg.TelegramServerURL != "http://127.0.0.1:8080" {
		t.Errorf("TelegramServerURL = %q, want default", cfg.TelegramServerURL)
	}
}

func TestLoadRequiresPasswordAndAPIKey(t *testing.T) {
	t.Setenv("ADMINUI_PASSWORD", "")
	t.Setenv("ADMINUI_API_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when required env vars are missing")
	}

	t.Setenv("ADMINUI_PASSWORD", "pw")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error when ADMINUI_API_KEY is missing")
	}
}
