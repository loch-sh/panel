// Package mongodb is the MongoDB engine of the services module.
package mongodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/registry"
	"host-panel-api/internal/services"
)

// Engine runs the official mongo images (uid 999). A resource is a database
// and a user of the same name, owner of that database only.
type Engine struct{}

// New returns the MongoDB engine.
func New() *Engine { return &Engine{} }

func (*Engine) ID() string                        { return "mongodb" }
func (*Engine) Kind() services.Kind               { return services.KindDatabase }
func (*Engine) DefaultPort() int                  { return 27017 }
func (*Engine) Lifecycle() services.Lifecycle     { return services.OnDemand }
func (*Engine) DataOwner() (int, int)             { return 999, 999 }
func (*Engine) Provisioner() services.Provisioner { return provisioner{} }

// Catalog lists the supported versions with their end of life
// (https://www.mongodb.com/legal/support-policy/lifecycles).
func (e *Engine) Catalog() []services.Version {
	v := func(version, until string) services.Version {
		out := e.VersionOf(version, version)
		out.SupportUntil = until
		return out
	}
	return []services.Version{v("8.0", "2029-10-31"), v("7.0", "2027-08-31"), v("6.0", "2025-07-31")}
}

// Releases: versions from 6.0 (authenticationRestrictions, mongosh).
func (*Engine) Releases() services.Releases {
	return services.Releases{Source: "docker.io/library/mongo", Tag: regexp.MustCompile(`\A([0-9]+\.[0-9]+)\z`),
		LineTag: "%s", EOL: "mongodb", Min: "6.0"}
}

func (*Engine) VersionOf(version, tag string) services.Version {
	return services.Version{Version: version, Image: registry.Hub("mongo:" + tag), DataMount: "/data/db"}
}

const maxConnections = "200"

func (*Engine) Service(services.InstanceRef, services.Version, services.Options) services.ServiceSpec {
	return services.ServiceSpec{
		// The entrypoint adds --auth and --bind_ip_all.
		Command: []string{"mongod", "--maxConns", maxConnections},
		Environment: map[string]string{
			"MONGO_INITDB_ROOT_USERNAME":      adminUser,
			"MONGO_INITDB_ROOT_PASSWORD_FILE": services.AdminSecretMount,
		},
		// The image declares /data/configdb (config servers only): tmpfs
		// rather than an anonymous volume.
		Tmpfs: []string{"/data/configdb:noexec,nosuid,nodev,size=1m"},
		// ping needs no authentication.
		Healthcheck: []string{"CMD", "mongosh", "--quiet", "--norc", "--eval", "quit(db.adminCommand('ping').ok ? 0 : 1)"},
	}
}

func (*Engine) ConnectionInfo(p services.ConnParams) []services.Credential {
	host, port, name, password := p.Host, p.Port, p.Name, p.Password
	uri := url.URL{
		Scheme:   "mongodb",
		User:     url.UserPassword(name, password),
		Host:     host + ":" + strconv.Itoa(port),
		Path:     "/" + name,
		RawQuery: "authSource=" + url.QueryEscape(name),
	}
	return []services.Credential{
		{Key: "host", Value: host},
		{Key: "port", Value: strconv.Itoa(port)},
		{Key: "database", Value: name},
		{Key: "user", Value: name},
		{Key: "password", Value: password, Secret: true},
		{Key: "uri", Value: uri.String(), Secret: true},
	}
}

const (
	adminUser        = "root"
	managedMark      = "loch-managed"
	execTimeout      = 30 * time.Second
	killAfterSeconds = "25"
	// resultPrefix marks the line printed by the scripts: the shell may
	// print other things.
	resultPrefix = "LOCH:"
)

// adminCmd connects as root on the loopback address. The root password is
// read from the secret file inside the container by a constant shell string:
// it is in the argv of mongosh inside the container (readable by root of the
// container only), never in the API nor in the exec request. The script goes
// through stdin.
var adminCmd = []string{
	"timeout", "-s", "KILL", killAfterSeconds,
	"sh", "-c", `exec mongosh --quiet --norc --host 127.0.0.1 -u ` + adminUser +
		` -p "$(cat ` + services.AdminSecretMount + `)" --authenticationDatabase admin`,
}

var identRe = regexp.MustCompile(`\A[a-z][a-z0-9_]{0,31}\z`)

// js encodes a validated name or a generated password as a JavaScript
// string literal (JSON). It panics on anything else.
func js(s string) string {
	if !identRe.MatchString(s) && !services.IsGeneratedPassword(s) {
		panic(fmt.Sprintf("mongodb: refusing to quote %q", s))
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// script wraps statements so that the result (or the error code) is printed
// on a marked line. It is a single line: the shell evaluates stdin line by
// line.
func script(body string) string {
	return "try { " + body + " } catch (e) { print('" + resultPrefix + "' + JSON.stringify({error: e.code || -1})); quit(3) }\n"
}

func ok(value string) string {
	return "print('" + resultPrefix + "' + JSON.stringify(" + value + "));"
}

type result struct {
	Error     int             `json:"error"`
	Resources *[]listedObject `json:"resources"`
}

type listedObject struct {
	Name    string `json:"name"`
	Managed bool   `json:"managed"`
}

// MongoDB error codes.
const (
	codeUserNotFound  = 11
	codeDuplicateUser = 51003
)

// run executes a script as root. Its errors never include the output.
func run(ctx context.Context, x services.Executor, op, body string) (result, error) {
	res, err := x.Exec(ctx, docker.ExecRequest{Cmd: adminCmd, Stdin: []byte(script(body)), Timeout: execTimeout})
	if errors.Is(err, docker.ErrExecTimeout) || errors.Is(err, docker.ErrExecStillRunning) {
		return result{}, fmt.Errorf("mongodb %s: %w", op, services.ErrOutcomeUnknown)
	}
	if err != nil {
		return result{}, &services.EngineError{Op: op, Detail: "exec: " + err.Error()}
	}
	if res.ExitCode == 137 {
		return result{}, fmt.Errorf("mongodb %s: %w", op, services.ErrOutcomeUnknown)
	}
	if res.Truncated {
		return result{}, &services.EngineError{Op: op, Detail: "output truncated"}
	}
	var out result
	found := false
	for _, line := range bytes.Split(res.Stdout, []byte("\n")) {
		// The shell may prefix its output with its prompt ("> ").
		if i := bytes.Index(line, []byte(resultPrefix)); i >= 0 {
			if found {
				return result{}, &services.EngineError{Op: op, Detail: "several results"}
			}
			rest := bytes.TrimSpace(line[i+len(resultPrefix):])
			if json.Unmarshal(rest, &out) != nil {
				return result{}, &services.EngineError{Op: op, Detail: "unexpected output"}
			}
			found = true
		}
	}
	if !found {
		return result{}, &services.EngineError{Op: op, Detail: fmt.Sprintf("exit code %d", res.ExitCode)}
	}
	switch out.Error {
	case 0:
		return out, nil
	case codeDuplicateUser:
		return out, fmt.Errorf("mongodb %s: %w", op, services.ErrResourceExists)
	case codeUserNotFound:
		return out, fmt.Errorf("mongodb %s: %w", op, services.ErrResourceNotFound)
	}
	return out, &services.EngineError{Op: op, Detail: "error " + strconv.Itoa(out.Error)}
}

// systemDatabases are never listed.
var systemDatabases = []string{"admin", "config", "local"}

// listBody lists every user-level object: each user (one resource per
// database holding users, plus each extra user of admin) and each database
// without users. A database is managed when its user of the same name
// carries the panel mark. Retirement deletes the data when this is empty.
var listBody = `const sys = ` + mustJSON(systemDatabases) + `;` +
	`const users = db.getSiblingDB('admin').system.users.find({}, {user: 1, db: 1, customData: 1}).toArray();` +
	`const out = Object.create(null); ` +
	`users.forEach(u => { if (u.db === 'admin') { if (u.user !== '` + adminUser + `') out['admin.' + u.user] = false; return; }` +
	` const managed = u.user === u.db && !!u.customData && u.customData.loch === '` + managedMark + `'; out[u.db] = (out[u.db] || false) || managed; });` +
	`db.adminCommand({listDatabases: 1, nameOnly: true}).databases.forEach(d => { if (!sys.includes(d.name) && !Object.prototype.hasOwnProperty.call(out, d.name)) out[d.name] = false; });` +
	ok(`{resources: Object.keys(out).sort().map(n => ({name: n, managed: out[n]}))}`)

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// initBody (idempotent) restricts root to the loopback addresses and lets it
// close the sessions of the tenants (killAllSessionsByPattern on other
// users needs impersonate, which the root role lacks).
const initBody = `const adm = db.getSiblingDB('admin');` +
	`if (!adm.getRole('lochSessions')) adm.createRole({role: 'lochSessions', privileges: [{resource: {cluster: true}, actions: ['impersonate', 'killAnySession']}], roles: []});` +
	`adm.grantRolesToUser('` + adminUser + `', ['lochSessions']);` +
	`adm.updateUser('` + adminUser + `', {authenticationRestrictions: [{clientSource: ['127.0.0.1', '::1']}]});` +
	`print('` + resultPrefix + `{}');`

func killSessions(name string) string {
	return `db.adminCommand({killAllSessionsByPattern: [{users: [{user: ` + js(name) + `, db: ` + js(name) + `}]}]});`
}

type provisioner struct{}

// reserved: the system databases.
var reserved = services.ReservedNames(systemDatabases, []string{"system"})

func (provisioner) ValidateName(v services.Version, name string) *services.Error {
	if len(name) > v.NameLimit() {
		return services.NameTooLong(v.NameLimit())
	}
	return reserved(name)
}

func (provisioner) Init(ctx context.Context, t services.Target) error {
	_, err := run(ctx, t, "init", initBody)
	return err
}

func (provisioner) List(ctx context.Context, t services.Target) ([]services.Resource, error) {
	out, err := run(ctx, t, "list", listBody)
	if err != nil {
		return nil, err
	}
	if out.Resources == nil {
		// Never an empty list by default: it would retire the instance.
		return nil, &services.EngineError{Op: "list", Detail: "no list in the output"}
	}
	list := make([]services.Resource, 0, len(*out.Resources))
	for _, r := range *out.Resources {
		list = append(list, services.Resource{Name: r.Name, Managed: r.Managed && services.IsValidName(r.Name)})
	}
	return list, nil
}

func checkPassword(p string) error {
	if !services.IsGeneratedPassword(p) {
		return errors.New("mongodb: refusing a password that was not generated by the panel")
	}
	return nil
}

// createUser creates the user of a resource: read/write and administration
// of its database, but no user administration (dbOwner would let the tenant
// create users that survive the deletion and the rotation, or remove the
// panel mark).
func createUser(name, password string) string {
	n, p := js(name), js(password)
	return `db.getSiblingDB(` + n + `).createUser({user: ` + n + `, pwd: ` + p +
		`, roles: [{role: 'readWrite', db: ` + n + `}, {role: 'dbAdmin', db: ` + n + `}], mechanisms: ['SCRAM-SHA-256'], customData: {loch: '` + managedMark + `'}});`
}

// Provision creates the user in its own database. The database itself
// appears with its first write.
func (provisioner) Provision(ctx context.Context, t services.Target, name, password string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	_, err := run(ctx, t, "create user", createUser(name, password)+ok(`{}`))
	return err
}

// Deprovision drops the user, closes its sessions, then drops the database
// (idempotent: a missing user is fine).
func (provisioner) Deprovision(ctx context.Context, t services.Target, name string) error {
	n := js(name)
	// Every user and role of the database goes (dropping a database leaves
	// them in admin.system.users).
	_, err := run(ctx, t, "drop", killSessions(name)+`db.getSiblingDB(`+n+`).dropAllUsers(); db.getSiblingDB(`+n+`).dropAllRoles();`+
		`db.getSiblingDB(`+n+`).dropDatabase();`+ok(`{}`))
	return err
}

// SetPassword replaces the user: updateUser would leave the connections
// authenticated with the old password open, dropUser closes them. The user
// is recreated in the same command (a brief gap, no other state lost: the
// roles and the mark are the panel's).
func (provisioner) SetPassword(ctx context.Context, t services.Target, name, password string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	n := js(name)
	_, err := run(ctx, t, "set password", `db.getSiblingDB(`+n+`).dropUser(`+n+`);`+killSessions(name)+createUser(name, password)+ok(`{}`))
	return err
}
