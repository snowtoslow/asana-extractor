package asana

import (
	"testing"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/config"
)

func TestNewOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		option Option
	}{
		{name: "negative retry count", option: WithRetries(-1, 0, 0)},
		{name: "negative wait", option: WithRetries(1, -time.Second, 0)},
		{name: "max less than wait", option: WithRetries(1, time.Second, 0)},
		{name: "negative max", option: WithRetries(1, 0, -time.Second)},
		{name: "nil logger", option: WithLogger(nil)},
		{name: "nil option", option: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{PAT: "synthetic-pat", WorkspaceGID: "123", BaseURL: "https://example.invalid"}
			if _, err := New(cfg, 0, tt.option); err == nil {
				t.Fatal("invalid option accepted")
			}
		})
	}
}
