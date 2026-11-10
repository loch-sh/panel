package cron

import (
	"testing"
)

func TestJob_Validate(t *testing.T) {
	t.Run("NameRequired", func(t *testing.T) {
		j := &Job{Schedule: "* * * * *", Type: JobTypeHTTP, HTTP: &HTTPConfig{Method: "GET", URL: "http://example.com"}}
		if msg := j.Validate(); msg != "name is required" {
			t.Errorf("expected 'name is required', got %q", msg)
		}
	})

	t.Run("ScheduleRequired", func(t *testing.T) {
		j := &Job{Name: "test", Type: JobTypeHTTP, HTTP: &HTTPConfig{Method: "GET", URL: "http://example.com"}}
		if msg := j.Validate(); msg != "schedule is required" {
			t.Errorf("expected 'schedule is required', got %q", msg)
		}
	})

	t.Run("InvalidType", func(t *testing.T) {
		j := &Job{Name: "test", Schedule: "* * * * *", Type: "invalid"}
		if msg := j.Validate(); msg != "type must be docker_action, docker_exec, http, or function_invoke" {
			t.Errorf("expected type error, got %q", msg)
		}
	})

	// --- Docker Action ---

	t.Run("DockerAction_MissingConfig", func(t *testing.T) {
		j := &Job{Name: "test", Schedule: "* * * * *", Type: JobTypeDockerAction}
		if msg := j.Validate(); msg != "dockerAction config is required for type docker_action" {
			t.Errorf("expected missing config error, got %q", msg)
		}
	})

	t.Run("DockerAction_MissingContainerName", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeDockerAction,
			DockerAction: &DockerActionConfig{Action: "start"},
		}
		if msg := j.Validate(); msg != "dockerAction.containerName is required" {
			t.Errorf("expected containerName error, got %q", msg)
		}
	})

	t.Run("DockerAction_InvalidAction", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeDockerAction,
			DockerAction: &DockerActionConfig{ContainerName: "my-container", Action: "pause"},
		}
		if msg := j.Validate(); msg != "dockerAction.action must be start, stop, restart, or kill" {
			t.Errorf("expected invalid action error, got %q", msg)
		}
	})

	t.Run("DockerAction_ValidActions", func(t *testing.T) {
		for _, action := range []string{"start", "stop", "restart", "kill"} {
			t.Run(action, func(t *testing.T) {
				j := &Job{
					Name: "test", Schedule: "* * * * *", Type: JobTypeDockerAction,
					DockerAction: &DockerActionConfig{ContainerName: "my-container", Action: action},
				}
				if msg := j.Validate(); msg != "" {
					t.Errorf("expected valid for action %q, got %q", action, msg)
				}
			})
		}
	})

	// --- Docker Exec ---

	t.Run("DockerExec_MissingConfig", func(t *testing.T) {
		j := &Job{Name: "test", Schedule: "* * * * *", Type: JobTypeDockerExec}
		if msg := j.Validate(); msg != "dockerExec config is required for type docker_exec" {
			t.Errorf("expected missing config error, got %q", msg)
		}
	})

	t.Run("DockerExec_MissingContainerName", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeDockerExec,
			DockerExec: &DockerExecConfig{Command: []string{"ls"}},
		}
		if msg := j.Validate(); msg != "dockerExec.containerName is required" {
			t.Errorf("expected containerName error, got %q", msg)
		}
	})

	t.Run("DockerExec_EmptyCommand", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeDockerExec,
			DockerExec: &DockerExecConfig{ContainerName: "my-container", Command: []string{}},
		}
		if msg := j.Validate(); msg != "dockerExec.command is required" {
			t.Errorf("expected empty command error, got %q", msg)
		}
	})

	t.Run("DockerExec_Valid", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeDockerExec,
			DockerExec: &DockerExecConfig{
				ContainerName: "my-container",
				Command:       []string{"php", "bin/console", "cache:clear"},
				WorkingDir:    "/app",
			},
		}
		if msg := j.Validate(); msg != "" {
			t.Errorf("expected valid, got %q", msg)
		}
	})

	// --- HTTP ---

	t.Run("HTTP_MissingConfig", func(t *testing.T) {
		j := &Job{Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP}
		if msg := j.Validate(); msg != "http config is required for type http" {
			t.Errorf("expected missing config error, got %q", msg)
		}
	})

	t.Run("HTTP_MissingURL", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
			HTTP: &HTTPConfig{Method: "GET"},
		}
		if msg := j.Validate(); msg != "http.url is required" {
			t.Errorf("expected missing URL error, got %q", msg)
		}
	})

	t.Run("HTTP_MissingMethod", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
			HTTP: &HTTPConfig{URL: "http://example.com"},
		}
		if msg := j.Validate(); msg != "http.method is required" {
			t.Errorf("expected missing method error, got %q", msg)
		}
	})

	t.Run("HTTP_Valid", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
			HTTP: &HTTPConfig{
				Method:  "POST",
				URL:     "http://example.com/api",
				Body:    `{"key":"value"}`,
				Timeout: 60,
				Headers: map[string]string{"Content-Type": "application/json"},
			},
		}
		if msg := j.Validate(); msg != "" {
			t.Errorf("expected valid, got %q", msg)
		}
	})

	// --- Additional HTTP validations ---

	t.Run("HTTP_InvalidMethod", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
			HTTP: &HTTPConfig{Method: "TRACE", URL: "http://example.com"},
		}
		if msg := j.Validate(); msg == "" {
			t.Error("expected error for TRACE method")
		}
	})

	t.Run("HTTP_AllowedMethods", func(t *testing.T) {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
			t.Run(method, func(t *testing.T) {
				j := &Job{
					Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
					HTTP: &HTTPConfig{Method: method, URL: "http://example.com"},
				}
				if msg := j.Validate(); msg != "" {
					t.Errorf("method %s should be valid, got %q", method, msg)
				}
			})
		}
	})

	t.Run("HTTP_InvalidScheme_FTP", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
			HTTP: &HTTPConfig{Method: "GET", URL: "ftp://example.com/file"},
		}
		if msg := j.Validate(); msg == "" {
			t.Error("expected error for ftp scheme")
		}
	})

	t.Run("HTTP_InvalidScheme_File", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
			HTTP: &HTTPConfig{Method: "GET", URL: "file:///etc/passwd"},
		}
		if msg := j.Validate(); msg == "" {
			t.Error("expected error for file scheme")
		}
	})

	t.Run("HTTP_InvalidScheme_NoScheme", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
			HTTP: &HTTPConfig{Method: "GET", URL: "example.com"},
		}
		if msg := j.Validate(); msg == "" {
			t.Error("expected error for URL without scheme")
		}
	})

	t.Run("HTTP_ValidScheme_HTTPS", func(t *testing.T) {
		j := &Job{
			Name: "test", Schedule: "* * * * *", Type: JobTypeHTTP,
			HTTP: &HTTPConfig{Method: "GET", URL: "https://example.com"},
		}
		if msg := j.Validate(); msg != "" {
			t.Errorf("https should be valid, got %q", msg)
		}
	})
}

func TestValidateMinInterval(t *testing.T) {
	t.Run("StandardCron_NoLimit", func(t *testing.T) {
		if err := ValidateMinInterval("* * * * *"); err != nil {
			t.Errorf("standard cron expressions should not be limited: %v", err)
		}
	})

	t.Run("Every_1m_OK", func(t *testing.T) {
		if err := ValidateMinInterval("@every 1m"); err != nil {
			t.Errorf("@every 1m should be valid: %v", err)
		}
	})

	t.Run("Every_60s_OK", func(t *testing.T) {
		if err := ValidateMinInterval("@every 60s"); err != nil {
			t.Errorf("@every 60s should be valid: %v", err)
		}
	})

	t.Run("Every_5m_OK", func(t *testing.T) {
		if err := ValidateMinInterval("@every 5m"); err != nil {
			t.Errorf("@every 5m should be valid: %v", err)
		}
	})

	t.Run("Every_10s_TooFrequent", func(t *testing.T) {
		if err := ValidateMinInterval("@every 10s"); err == nil {
			t.Error("@every 10s should be rejected")
		}
	})

	t.Run("Every_1s_TooFrequent", func(t *testing.T) {
		if err := ValidateMinInterval("@every 1s"); err == nil {
			t.Error("@every 1s should be rejected")
		}
	})

	t.Run("Every_30s_TooFrequent", func(t *testing.T) {
		if err := ValidateMinInterval("@every 30s"); err == nil {
			t.Error("@every 30s should be rejected")
		}
	})

	t.Run("Descriptors_NoLimit", func(t *testing.T) {
		for _, expr := range []string{"@hourly", "@daily", "@weekly", "@monthly", "@yearly"} {
			if err := ValidateMinInterval(expr); err != nil {
				t.Errorf("%s should not be limited: %v", expr, err)
			}
		}
	})
}

func TestValidateCronExpression(t *testing.T) {
	t.Run("ValidStandard", func(t *testing.T) {
		valid := []string{
			"* * * * *",
			"0 3 * * *",
			"*/5 * * * *",
			"0 0 1 1 *",
			"30 4 1,15 * 1-5",
		}
		for _, expr := range valid {
			t.Run(expr, func(t *testing.T) {
				if err := ValidateCronExpression(expr); err != nil {
					t.Errorf("expression %q should be valid: %v", expr, err)
				}
			})
		}
	})

	t.Run("ValidDescriptors", func(t *testing.T) {
		descriptors := []string{
			"@yearly",
			"@annually",
			"@monthly",
			"@weekly",
			"@daily",
			"@midnight",
			"@hourly",
			"@every 5m",
			"@every 1h30m",
			"@every 10s",
		}
		for _, expr := range descriptors {
			t.Run(expr, func(t *testing.T) {
				if err := ValidateCronExpression(expr); err != nil {
					t.Errorf("descriptor %q should be valid: %v", expr, err)
				}
			})
		}
	})

	t.Run("Invalid", func(t *testing.T) {
		invalid := []string{
			"",
			"not a cron",
			"* * *",
			"60 * * * *",
			"* 25 * * *",
		}
		for _, expr := range invalid {
			t.Run(expr, func(t *testing.T) {
				if err := ValidateCronExpression(expr); err == nil {
					t.Errorf("expression %q should be invalid", expr)
				}
			})
		}
	})
}
