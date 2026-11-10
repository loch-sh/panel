package hosting

import "host-panel-api/internal/composekit"

// ComposeRunner runs Docker Compose commands for a stack directory.
type ComposeRunner = composekit.ComposeRunner

// ExecRunner runs the docker CLI (with the compose plugin) as a subprocess.
type ExecRunner = composekit.ExecRunner

func dockerEnv() []string { return composekit.DockerEnv() }

// Compose arguments.
func upArgs(pull bool) []string {
	pullPolicy := "missing"
	if pull {
		pullPolicy = "always"
	}
	// --force-recreate: snippets are bind-mounted single files; replacing them
	// atomically creates a new inode that a running container would not see.
	// --wait: fail (and roll back) when the container does not come up.
	return []string{"up", "-d", "--remove-orphans", "--force-recreate",
		"--pull", pullPolicy, "--wait", "--wait-timeout", "120"}
}

var downArgs = []string{"down", "--remove-orphans"}
