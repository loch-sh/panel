// Package hosting implements the "web hosting" (self-service stacks) feature.
//
// A hosted stack is a plain Docker Compose project stored in
// <StacksDir>/hosted-<name>/compose.yaml. The compose file is the single source
// of truth: it embeds the settings entered in the panel under the "x-loch"
// extension key (ignored by Docker Compose), so every stack can be booted with
// `docker compose up -d` without the panel running (no vendor lock-in).
package hosting

import "time"

// StackPrefix prefixes the compose project and directory of every hosted
// stack. It keeps hosted stacks apart from the ones deployed by CI in the same
// directory and is never shown in the web interface.
const StackPrefix = "hosted-"

// SpecVersion is the version of the x-loch format written in compose files.
// Bump it whenever Render output changes, so stacks written by a previous
// version are not reported as edited by hand (they are refreshed on their
// next update).
//   - 2: init, tty and stdin_open on the web service.
//   - 3: WEB_PHP_TIMEOUT, PHP-FPM pool overrides (web memory limit).
//   - 4: Apache framework rules deny PHP files other than index.php and
//     pass the Authorization header.
//   - 5: images pulled from the Docker Hub mirror (mirror.gcr.io).
//   - 6: private networks (networks replaces servicesNetwork), their
//     creation commands in the header.
const SpecVersion = 6

// Template is the kind of web service of a stack.
type Template string

const (
	TemplatePHPNginx  Template = "php-nginx"
	TemplatePHPApache Template = "php-apache"
	TemplateNginx     Template = "nginx"
	TemplateApache    Template = "apache"
)

// Templates lists the supported templates, in display order.
var Templates = []Template{TemplatePHPNginx, TemplatePHPApache, TemplateNginx, TemplateApache}

// HasPHP reports whether the template runs PHP.
func (t Template) HasPHP() bool {
	return t == TemplatePHPNginx || t == TemplatePHPApache
}

// UsesNginx reports whether the web server of the template is Nginx.
func (t Template) UsesNginx() bool {
	return t == TemplatePHPNginx || t == TemplateNginx
}

// Valid reports whether t is a supported template.
func (t Template) Valid() bool {
	for _, known := range Templates {
		if t == known {
			return true
		}
	}
	return false
}

// Spec is the user-facing definition of a stack, stored under x-loch.
type Spec struct {
	Version       int          `yaml:"version" json:"-"`
	Name          string       `yaml:"name" json:"name"`
	Template      Template     `yaml:"template" json:"template"`
	PHPVersion    string       `yaml:"phpVersion,omitempty" json:"phpVersion,omitempty"`
	Site          string       `yaml:"site" json:"site"`
	DocumentRoot  string       `yaml:"documentRoot" json:"documentRoot"`
	Domains       []string     `yaml:"domains" json:"domains"`
	HTTPS         bool         `yaml:"https" json:"https"`
	RedirectHTTPS bool         `yaml:"redirectHttps" json:"redirectHttps"`
	PHP           *PHPSettings `yaml:"php,omitempty" json:"php,omitempty"`
	// Routing: "" (image default), "symfony" or "custom" (see routing.go).
	Routing string `yaml:"routing,omitempty" json:"routing"`
	// AssetDirs (Symfony routing) are served without front controller fallback.
	AssetDirs []string  `yaml:"assetDirs,omitempty" json:"assetDirs"`
	Resources Resources `yaml:"resources" json:"resources"`
	Env       []EnvVar  `yaml:"env,omitempty" json:"env"`
	// Networks are the panel networks the web service joins (registry
	// names, internal/networks), sorted.
	Networks []string `yaml:"networks,omitempty" json:"networks"`
}

// PHPSettings are the PHP settings exposed as form fields. They are applied
// through the environment variables of the webdevops images.
type PHPSettings struct {
	// MemoryLimit applies to the PHP CLI (crons, console) and is the default
	// for web requests.
	MemoryLimit string `yaml:"memoryLimit" json:"memoryLimit"`
	// WebMemoryLimit overrides MemoryLimit for web requests (PHP-FPM); "" =
	// same as MemoryLimit.
	WebMemoryLimit    string `yaml:"webMemoryLimit,omitempty" json:"webMemoryLimit"`
	UploadMaxFilesize string `yaml:"uploadMaxFilesize" json:"uploadMaxFilesize"`
	PostMaxSize       string `yaml:"postMaxSize" json:"postMaxSize"`
	// MaxExecutionTime only applies to web requests: the PHP CLI always
	// ignores it.
	MaxExecutionTime int    `yaml:"maxExecutionTime" json:"maxExecutionTime"`
	DisplayErrors    bool   `yaml:"displayErrors" json:"displayErrors"`
	Timezone         string `yaml:"timezone" json:"timezone"`
	// DisabledModules are passed to the image as PHP_DISMOD.
	DisabledModules []string `yaml:"disabledModules,omitempty" json:"disabledModules"`
}

// Resources are the container limits of the web service. 0 means no limit.
type Resources struct {
	MemoryMB int     `yaml:"memoryMB" json:"memoryMB"`
	CPUs     float64 `yaml:"cpus" json:"cpus"`
}

// EnvVar is a free environment variable passed to the web service.
type EnvVar struct {
	Name  string `yaml:"name" json:"name"`
	Value string `yaml:"value" json:"value"`
}

// Snippets are raw configuration files written next to the compose file and
// mounted read-only in the container at fixed paths.
type Snippets struct {
	PHPIni string `json:"phpIni"`
	Vhost  string `json:"vhost"`
	// RoutingRules are the routing rules of the custom routing mode.
	RoutingRules string `json:"routingRules"`
}

// ProjectName returns the compose project name (and directory name) of a stack.
func ProjectName(name string) string {
	return StackPrefix + name
}

// Operation tracks an asynchronous action (create, update, deploy, delete).
type Operation struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	State      string     `json:"state"` // running | succeeded | failed
	Error      string     `json:"error,omitempty"`
	RolledBack bool       `json:"rolledBack,omitempty"`
	Output     string     `json:"output,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Operation kinds and states.
const (
	OpCreate = "create"
	OpUpdate = "update"
	OpDeploy = "deploy"
	OpDelete = "delete"

	OpRunning   = "running"
	OpSucceeded = "succeeded"
	OpFailed    = "failed"
)
