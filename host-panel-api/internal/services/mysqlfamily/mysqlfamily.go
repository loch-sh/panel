// Package mysqlfamily provides the MariaDB and MySQL engines of the services
// module. Both speak the same SQL with a few differences per flavor and
// version (authentication plugins, MySQL 5.6 syntax), kept in dialect.
package mysqlfamily

import (
	"net/url"
	"regexp"
	"strconv"

	"host-panel-api/internal/registry"
	"host-panel-api/internal/services"
)

type flavor int

const (
	flavorMariaDB flavor = iota
	flavorMySQL
)

// Engine runs the official mariadb or mysql images (uid 999).
type Engine struct {
	id      string
	flavor  flavor
	catalog []services.Version
}

const dataMount = "/var/lib/mysql"

// NewMariaDB returns the MariaDB engine
// (https://mariadb.org/about/#maintenance-policy).
func NewMariaDB() *Engine {
	e := &Engine{id: "mariadb", flavor: flavorMariaDB}
	v := func(version, until string) services.Version {
		out := e.VersionOf(version, version)
		out.SupportUntil = until
		return out
	}
	e.catalog = []services.Version{v("11.8", "2028-06-04"), v("11.4", "2029-05-29"), v("10.11", "2028-02-16")}
	return e
}

// NewMySQL returns the MySQL engine. 5.6 is long out of support (no security
// fix) and only published for amd64: it runs emulated on arm64 hosts; it is
// offered only when pinned (SERVICES_MYSQL_PINNED_VERSIONS).
func NewMySQL() *Engine {
	e := &Engine{id: "mysql", flavor: flavorMySQL}
	v := func(version, until string) services.Version {
		out := e.VersionOf(version, version)
		out.SupportUntil = until
		return out
	}
	e.catalog = []services.Version{v("8.4", "2032-04-30")}
	return e
}

// Releases: one tag per release series ("8.4", "11.4").
func (e *Engine) Releases() services.Releases {
	rel := services.Releases{Source: "docker.io/library/" + e.id, Tag: regexp.MustCompile(`\A([0-9]+\.[0-9]+)\z`),
		LineTag: "%s", EOL: e.id, Min: "10.11"}
	if e.flavor == flavorMySQL {
		rel.Min = "5.6"
	}
	return rel
}

// VersionOf: MySQL 5.6 is amd64 only, and limits user names to 16
// characters.
func (e *Engine) VersionOf(version, tag string) services.Version {
	v := services.Version{Version: version, Image: registry.Hub(e.id + ":" + tag), DataMount: dataMount}
	if e.flavor == flavorMySQL && services.CompareVersions(version, "5.7") < 0 {
		v.Platform, v.MaxNameLength = "linux/amd64", 16
	}
	return v
}

func (e *Engine) ID() string                        { return e.id }
func (*Engine) Kind() services.Kind                 { return services.KindDatabase }
func (*Engine) DefaultPort() int                    { return 3306 }
func (*Engine) Lifecycle() services.Lifecycle       { return services.OnDemand }
func (*Engine) DataOwner() (int, int)               { return 999, 999 }
func (e *Engine) Catalog() []services.Version       { return e.catalog }
func (e *Engine) Provisioner() services.Provisioner { return provisioner{e: e} }

// maxConnections bounds the connections of the instance; each user is
// limited to connectionLimit so one site cannot exhaust them.
const (
	maxConnections  = "200"
	connectionLimit = 20
)

func (e *Engine) Service(_ services.InstanceRef, v services.Version, _ services.Options) services.ServiceSpec {
	d := dialectFor(e.flavor, v.Version)
	common := []string{
		"--max-connections=" + maxConnections,
		// Users are '<name>'@'%': no DNS lookup on connect.
		"--skip-name-resolve",
		"--max-connect-errors=100000",
		// Clients may not make the server read their files.
		"--local-infile=0",
	}
	spec := services.ServiceSpec{}
	switch {
	case e.flavor == flavorMariaDB:
		spec.Command = append([]string{"mariadbd"}, common...)
		spec.Environment = map[string]string{
			"MARIADB_ROOT_PASSWORD_FILE": services.AdminSecretMount,
			// No root@'%': the admin only exists on the local socket.
			"MARIADB_ROOT_HOST":    "localhost",
			"MARIADB_AUTO_UPGRADE": "1",
		}
		spec.Healthcheck = []string{"CMD", "healthcheck.sh", "--connect", "--innodb_initialized"}
	case d.legacy:
		spec.Command = append([]string{"mysqld",
			// utf8mb4 indexes beyond 767 bytes need large prefixes (tables
			// must still use ROW_FORMAT=DYNAMIC on 5.6).
			"--innodb-large-prefix=1", "--innodb-file-format=Barracuda", "--innodb-file-per-table=1",
			"--character-set-server=utf8mb4", "--collation-server=utf8mb4_unicode_ci",
			// Unsupported version, tenants run arbitrary SQL: reduce the surface.
			"--skip-symbolic-links",
		}, common...)
		spec.Environment = mysqlEnv()
		spec.Healthcheck = []string{"CMD", "mysqladmin", "ping", "-h", "127.0.0.1", "--silent"}
	default:
		args := []string{"mysqld",
			// No replication: the binary log only costs disk, and it forbids
			// triggers to users without SUPER (CMS need them).
			"--disable-log-bin",
		}
		if d.nativePlugin {
			// Loaded for the users created with the legacy mechanism only:
			// the default stays caching_sha2_password. Removed in MySQL 9.
			args = append(args, "--mysql-native-password=ON")
		}
		spec.Command = append(args, common...)
		spec.Environment = mysqlEnv()
		spec.Healthcheck = []string{"CMD", "mysqladmin", "ping", "-h", "127.0.0.1", "--silent"}
	}
	return spec
}

// TLS requires TLS on every TCP connection (the local socket used by the
// admin commands is exempt). MySQL 5.6 has no require_secure_transport: it
// cannot require TLS (it is only published on loopback, being unsupported).
func (e *Engine) TLS(_ services.InstanceRef, v services.Version) (services.TLSSpec, bool) {
	if dialectFor(e.flavor, v.Version).legacy {
		return services.TLSSpec{}, false
	}
	return services.TLSSpec{Args: []string{
		"--ssl-cert=" + services.TLSMount + "/" + services.CertFile,
		"--ssl-key=" + services.TLSMount + "/" + services.KeyFile,
		"--require-secure-transport=ON",
	}}, true
}

// TLSOptional offers TLS without requiring it (SERVICES_ALLOW_TLS_OPTIONAL, chosen per exposure):
// clients choose; plain ones send their password in clear.
func (e *Engine) TLSOptional(_ services.InstanceRef, v services.Version) (services.TLSSpec, bool) {
	if dialectFor(e.flavor, v.Version).legacy {
		return services.TLSSpec{}, false
	}
	return services.TLSSpec{Args: []string{
		"--ssl-cert=" + services.TLSMount + "/" + services.CertFile,
		"--ssl-key=" + services.TLSMount + "/" + services.KeyFile,
	}}, true
}

func mysqlEnv() map[string]string {
	return map[string]string{
		"MYSQL_ROOT_PASSWORD_FILE": services.AdminSecretMount,
		"MYSQL_ROOT_HOST":          "localhost",
	}
}

func (*Engine) ConnectionInfo(p services.ConnParams) []services.Credential {
	host, port, name, password := p.Host, p.Port, p.Name, p.Password
	uri := url.URL{
		Scheme: "mysql",
		User:   url.UserPassword(name, password),
		Host:   host + ":" + strconv.Itoa(port),
		Path:   "/" + name,
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
