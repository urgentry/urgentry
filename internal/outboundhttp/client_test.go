package outboundhttp

import "testing"

func TestValidateTargetURL(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "public https", target: "https://example.com/hook"},
		{name: "public http", target: "http://example.com/hook"},
		{name: "localhost hostname", target: "https://localhost/hook", wantErr: true},
		{name: "localhost subdomain", target: "https://api.localhost/hook", wantErr: true},
		{name: "loopback ipv4", target: "https://127.0.0.1/hook", wantErr: true},
		{name: "loopback ipv6", target: "https://[::1]/hook", wantErr: true},
		{name: "private ipv4", target: "https://10.0.0.1/hook", wantErr: true},
		{name: "link local", target: "https://169.254.169.254/hook", wantErr: true},
		{name: "unsupported scheme", target: "ftp://example.com/hook", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateTargetURL(tt.target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTargetURL(%q) error = %v, wantErr %v", tt.target, err, tt.wantErr)
			}
		})
	}
}
