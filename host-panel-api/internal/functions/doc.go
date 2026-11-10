// Package functions is the serverless module of the panel: standard AWS
// Lambda handlers (zip of code + an official AWS Lambda base image) run as
// one panel-rendered compose stack per function, under the same hardening as
// the dedicated service instances.
//
// Design (decision log §20 of docs/design/services-decisions.md):
//
//   - A function is the compose project fn-<name> in HOSTING_STACKS_DIRECTORY,
//     startable with `docker compose up -d` without the panel. The panel only
//     runs a compose file identical to what it renders from the private
//     reference function.json (FUNCTIONS_DATA_DIRECTORY/.loch/fn-<name>/,
//     0700, never in the stacks directory that CI can write).
//   - The invocation endpoint is loch's own Lambda Runtime API
//     (loch-lambda-runtime), a static binary bind-mounted read-only at
//     /loch/lambda-runtime and used as the entrypoint; the AWS image is
//     unchanged. The binary is trusted through a table embedded in the panel
//     build and re-hashed before every start.
//   - The code is a zip validated and extracted by the panel (zip.go) into a
//     versioned, read-only deployment directory mounted at /var/task.
//   - A running container is used only if it matches the reference exactly
//     (verify.go), as for the service instances.
//
// The Manager (manager.go, apply.go) runs the functions: creation with
// quotas and name reservation, deployments and rollbacks, configuration,
// secrets, invoke credentials, networks, start / stop, deletion and purge.
// Every compose run goes through apply, which re-verifies the artifacts
// and refuses a function edited outside the panel. Invocations go through
// docker exec (invoke_ops.go), never through a network bridge. The
// download of the runtime binary, the runtime updates and the interface
// come in later parts.
package functions
