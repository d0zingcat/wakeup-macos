package tailscale

import (
	"testing"
)

func TestParseIPv4(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{"valid ip", "100.64.1.5\n", "100.64.1.5", false},
		{"with trailing whitespace", "  100.64.1.5  \n", "100.64.1.5", false},
		{"empty output", "", "", true},
		{"whitespace only", "  \n", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIPv4Output(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIPv4Output() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseIPv4Output() = %q, want %q", got, tt.want)
			}
		})
	}
}
