// Package releases reads what the services module needs to know about the
// published versions of an engine: the tags of its repository on the origin
// registry, the digest of an image on the registry it is pulled from (the
// mirror), and the end of support dates published by endoflife.date.
//
// Every answer is untrusted: callers match tags against strict patterns and
// dates against YYYY-MM-DD. Responses are size-bounded, hosts are fixed by
// the engines (never taken from a response, except the token realm of a
// registry, which must be HTTPS).
package releases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// maxBody bounds a response (the tag list of a busy repository is a few
	// hundred KB).
	maxBody = 8 << 20
	// maxPages bounds the pagination of a tag list, maxTags its size.
	maxPages = 20
	maxTags  = 50000
	// requestTimeout bounds one HTTP request.
	requestTimeout = 30 * time.Second
	// userAgent identifies the panel to registries.
	userAgent = "loch-panel-releases/1"
)

// manifestAccept lists the manifest types a digest is asked for: the index
// first, so that the digest is the one Docker records in RepoDigests when it
// pulls the tag.
var manifestAccept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Client queries registries and endoflife.date.
type Client struct {
	HTTP *http.Client
	// Scheme is "https" (tests use "http").
	Scheme string
	// EOLBase is the endoflife.date API ("https://endoflife.date/api/v1").
	EOLBase string
}

// tokenHosts are the hosts allowed to issue registry tokens, besides the
// registry itself (a challenge could otherwise point the panel anywhere).
var tokenHosts = map[string]bool{"auth.docker.io": true, "ghcr.io": true}

// redirectHosts may serve a redirected registry request: the blob storage
// of the registries (a manifest HEAD never needs more).
var redirectHosts = map[string]bool{"auth.docker.io": true, "ghcr.io": true, "pkg-containers.githubusercontent.com": true, "endoflife.date": true}

// checkRedirect follows HTTPS redirects to the same host or to a known
// registry host only (a taken-over endpoint must not make the panel reach
// internal hosts).
func checkRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" || len(via) >= 5 {
		return errors.New("redirect refused")
	}
	if req.URL.Host != via[0].URL.Host && !redirectHosts[req.URL.Hostname()] {
		return errors.New("redirect to another host refused")
	}
	return nil
}

// New returns a client with bounded timeouts, following HTTPS redirects to
// known hosts only.
func New() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: requestTimeout, CheckRedirect: checkRedirect},
		Scheme:  "https",
		EOLBase: "https://endoflife.date/api/v1",
	}
}

// ErrNotFound: the repository, the tag or the product does not exist.
var ErrNotFound = errors.New("not found")

// Tags lists the tags of a repository ("docker.io/library/redis",
// "ghcr.io/loch-sh/s3").
func (c *Client) Tags(ctx context.Context, source string) ([]string, error) {
	host, repo, err := splitRepository(source)
	if err != nil {
		return nil, err
	}
	next := c.Scheme + "://" + host + "/v2/" + repo + "/tags/list?n=10000"
	var out []string
	tok := &token{}
	for page := 0; next != "" && page < maxPages; page++ {
		resp, err := c.do(ctx, http.MethodGet, next, "application/json", repo, tok)
		if err != nil {
			return nil, err
		}
		var body struct {
			Tags []string `json:"tags"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("tags of %s: %w", source, err)
		}
		out = append(out, body.Tags...)
		if len(out) > maxTags {
			return nil, fmt.Errorf("tags of %s: more than %d tags", source, maxTags)
		}
		next = nextPage(c.Scheme, host, link)
	}
	// A list cut by the page cap would silently miss the newest tags.
	if next != "" {
		return nil, fmt.Errorf("tags of %s: more than %d pages", source, maxPages)
	}
	return out, nil
}

// Digest returns the digest of an image reference ("mirror.gcr.io/library/
// redis:8.4-alpine") on its registry, without downloading it. ErrNotFound
// when the registry does not have it.
func (c *Client) Digest(ctx context.Context, image string) (string, error) {
	repoRef, tag, ok := cutTag(image)
	if !ok {
		return "", fmt.Errorf("image %q has no tag", image)
	}
	host, repo, err := splitRepository(repoRef)
	if err != nil {
		return "", err
	}
	resp, err := c.do(ctx, http.MethodHead, c.Scheme+"://"+host+"/v2/"+repo+"/manifests/"+url.PathEscape(tag), manifestAccept, repo, &token{})
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	d := resp.Header.Get("Docker-Content-Digest")
	if !digestRe.MatchString(d) {
		return "", fmt.Errorf("image %s: no valid digest in the response", image)
	}
	return d, nil
}

var digestRe = regexp.MustCompile(`\Asha256:[0-9a-f]{64}\z`)

// ValidDigest reports whether d is a sha256 digest.
func ValidDigest(d string) bool { return digestRe.MatchString(d) }

// Support is the end of support of a release cycle (YYYY-MM-DD, "" unknown).
type Support struct {
	// Active: end of the active support (bug fixes).
	Active string
	// Security: end of the security fixes (end of life).
	Security string
}

var dateRe = regexp.MustCompile(`\A[0-9]{4}-[0-9]{2}-[0-9]{2}\z`)

// Support returns the end of support dates of the release cycles of an
// endoflife.date product, by cycle name ("8.4", "17").
func (c *Client) Support(ctx context.Context, product string) (map[string]Support, error) {
	if !productRe.MatchString(product) {
		return nil, fmt.Errorf("invalid product %q", product)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.EOLBase+"/products/"+product, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("endoflife.date %s: status %d", product, resp.StatusCode)
	}
	var body struct {
		Result struct {
			Releases []struct {
				Name     string  `json:"name"`
				EOASFrom *string `json:"eoasFrom"`
				EOLFrom  *string `json:"eolFrom"`
			} `json:"releases"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		return nil, fmt.Errorf("endoflife.date %s: %w", product, err)
	}
	out := map[string]Support{}
	date := func(p *string) string {
		if p != nil && dateRe.MatchString(*p) {
			return *p
		}
		return ""
	}
	for _, r := range body.Result.Releases {
		out[r.Name] = Support{Active: date(r.EOASFrom), Security: date(r.EOLFrom)}
	}
	return out, nil
}

var productRe = regexp.MustCompile(`\A[a-z0-9][a-z0-9-]{0,40}\z`)

// token caches the bearer token of one repository during a call.
type token struct{ value string }

// do sends a request, answering the bearer challenge of the registry once.
func (c *Client) do(ctx context.Context, method, target, accept, repo string, tok *token) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		rctx, cancel := context.WithTimeout(ctx, requestTimeout)
		req, err := http.NewRequestWithContext(rctx, method, target, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", userAgent)
		if tok.value != "" {
			req.Header.Set("Authorization", "Bearer "+tok.value)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			cancel()
			return nil, err
		}
		resp.Body = cancelOnClose{resp.Body, cancel}
		switch {
		case resp.StatusCode == http.StatusOK:
			return resp, nil
		case resp.StatusCode == http.StatusUnauthorized && attempt == 0 && tok.value == "":
			challenge := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			v, err := c.fetchToken(ctx, challenge, repo, req.URL.Host)
			if err != nil {
				return nil, err
			}
			tok.value = v
			continue
		case resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			return nil, ErrNotFound
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("registry %s: status %d", req.URL.Host, resp.StatusCode)
		}
	}
	return nil, errors.New("registry: authentication failed")
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error { err := c.ReadCloser.Close(); c.cancel(); return err }

// fetchToken gets an anonymous pull token from the realm of a challenge
// (`Bearer realm="https://auth.docker.io/token",service="registry.docker.io"`).
func (c *Client) fetchToken(ctx context.Context, challenge, repo, registryHost string) (string, error) {
	params := parseChallenge(challenge)
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Host == "" || (realm.Scheme != "https" && realm.Scheme != c.Scheme) ||
		(realm.Host != registryHost && !tokenHosts[realm.Hostname()]) {
		return "", errors.New("registry: invalid authentication challenge")
	}
	q := realm.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	q.Set("scope", "repository:"+repo+":pull")
	realm.RawQuery = q.Encode()
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry token: status %d", resp.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", errors.New("registry token: empty")
}

// parseChallenge parses the parameters of a Bearer challenge.
func parseChallenge(h string) map[string]string {
	out := map[string]string{}
	scheme, rest, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return out
	}
	for _, part := range splitParams(rest) {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			out[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return out
}

// splitParams splits on commas outside quotes.
func splitParams(s string) []string {
	var out []string
	quoted, start := false, 0
	for i, r := range s {
		switch r {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// nextPage returns the next page of a tag list from its Link header
// (`</v2/library/redis/tags/list?last=x&n=100>; rel="next"`), on the same
// host only.
func nextPage(scheme, host, link string) string {
	if link == "" || !strings.Contains(link, `rel="next"`) {
		return ""
	}
	start, end := strings.Index(link, "<"), strings.Index(link, ">")
	if start < 0 || end <= start+1 {
		return ""
	}
	u, err := url.Parse(link[start+1 : end])
	if err != nil || (u.Host != "" && u.Host != host) || !strings.HasPrefix(u.Path, "/v2/") {
		return ""
	}
	return scheme + "://" + host + u.RequestURI()
}

var (
	hostRe = regexp.MustCompile(`\A[a-z0-9.-]+(:[0-9]{1,5})?\z`)
	repoRe = regexp.MustCompile(`\A[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*){0,3}\z`)
)

// splitRepository splits "host/repo/path" and maps docker.io to the
// registry API host.
func splitRepository(ref string) (string, string, error) {
	host, repo, ok := strings.Cut(ref, "/")
	if !ok || !hostRe.MatchString(host) || !repoRe.MatchString(repo) {
		return "", "", fmt.Errorf("invalid repository %q", ref)
	}
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	return host, repo, nil
}

// cutTag splits "host/repo:tag" (the host may carry a port).
func cutTag(image string) (string, string, bool) {
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i:], "/") {
		return "", "", false
	}
	return image[:i], image[i+1:], image[i+1:] != ""
}
