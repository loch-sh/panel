package registry

import "testing"

func TestHub(t *testing.T) {
	for in, want := range map[string]string{
		"postgres:17":              "mirror.gcr.io/library/postgres:17",
		"redis:8.4-alpine":         "mirror.gcr.io/library/redis:8.4-alpine",
		"valkey/valkey:9.0-alpine": "mirror.gcr.io/valkey/valkey:9.0-alpine",
		"webdevops/php-nginx:8.4":  "mirror.gcr.io/webdevops/php-nginx:8.4",
	} {
		if got := Hub(in); got != want {
			t.Errorf("Hub(%q) = %q, want %q", in, got, want)
		}
	}
}
