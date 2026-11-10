// Package services implements managed service instances (databases first)
// that users get resources on: a database and its dedicated user.
//
// An instance is a plain Docker Compose project stored in
// <StacksDir>/svc-<engine>-<version>/ (compose file, panel marker, admin
// secret, generated config files); its data lives in
// <DataDir>/svc-<engine>-<version>/. There is a single instance per engine and
// major version, shared by every site: it is created with its first resource
// and retired with its last one.
//
// The panel keeps no state of its own: instances are found by scanning the
// stacks directory and the resources are read from the engine catalog. Every
// instance boots with `docker compose up -d` without the panel.
//
// Engines (PostgreSQL, MariaDB, MySQL...) plug in through the Engine
// interface and an optional Provisioner capability; they are registered in a
// Registry at startup.
package services
