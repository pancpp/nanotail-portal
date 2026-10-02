package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/mod/semver"
)

const (
	GITHUB_REPOSITORY         = "pancpp/nanotail-portal"
	GITHUB_REPOSITORY_API     = "https://api.github.com/repos/" + GITHUB_REPOSITORY
	GITHUB_API_VERSION        = "2026-03-10"
	MAX_GITHUB_METADATA_BYTES = 1 << 20
	MAX_GITHUB_REDIRECTS      = 5
)

var (
	ErrGitHubUnavailable   = errors.New("GitHub release information is unavailable")
	ErrGitHubRateLimited   = errors.New("GitHub has temporarily limited update requests")
	ErrNoCompatiblePackage = errors.New("The latest release has no package for this device")
)

// GitHubSource reads public releases from the fixed application repository.
// It never accepts credentials, repository overrides, or caller-supplied URLs.
type GitHubSource struct {
	metadata *http.Client
	download *http.Client
	mu       sync.Mutex
	selected *githubSelection
}

type githubSelection struct {
	release   Release
	releaseID int64
	assetID   int64
	targetOS  string
	arch      string
}

type githubRelease struct {
	ID          int64         `json:"id"`
	TagName     string        `json:"tag_name"`
	Body        string        `json:"body"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt string        `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Size  int64  `json:"size"`
}

func NewGitHubSource() *GitHubSource {
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  20 * time.Second,
		IdleConnTimeout:        90 * time.Second,
		MaxIdleConns:           4,
		MaxIdleConnsPerHost:    2,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &GitHubSource{
		metadata: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: githubRedirect(false)},
		download: &http.Client{Transport: transport, Timeout: 5 * time.Minute, CheckRedirect: githubRedirect(true)},
	}
}

func githubRedirect(asset bool) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, previous []*http.Request) error {
		u := request.URL
		if len(previous) >= MAX_GITHUB_REDIRECTS || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
			return errors.New("refused unsafe GitHub redirect")
		}
		host := strings.ToLower(u.Hostname())
		if host == "api.github.com" && len(previous) > 0 && u.EscapedPath() == previous[0].URL.EscapedPath() {
			return nil
		}
		if asset {
			switch host {
			case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
				return nil
			case "github.com":
				if strings.HasPrefix(u.EscapedPath(), "/"+GITHUB_REPOSITORY+"/releases/download/") {
					return nil
				}
			}
		}
		return errors.New("refused unsafe GitHub redirect")
	}
}

func githubRequest(ctx context.Context, endpoint, accept string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid request", ErrGitHubUnavailable)
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("X-GitHub-Api-Version", GITHUB_API_VERSION)
	request.Header.Set("User-Agent", "nanotail-portal-updater")
	return request, nil
}

// Never include transport errors or upstream bodies in returned errors: a
// failed asset request may contain a short-lived signed CDN URL.
func githubNetworkError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return fmt.Errorf("%w: request timed out", ErrGitHubUnavailable)
	}
	return fmt.Errorf("%w: could not complete the request", ErrGitHubUnavailable)
}

func githubStatusError(response *http.Response) error {
	if response.StatusCode == http.StatusTooManyRequests || (response.StatusCode == http.StatusForbidden && (response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != "")) {
		return ErrGitHubRateLimited
	}
	return fmt.Errorf("%w: HTTP %d", ErrGitHubUnavailable, response.StatusCode)
}

func (s *GitHubSource) readRelease(ctx context.Context, suffix string) (*githubRelease, error) {
	request, err := githubRequest(ctx, GITHUB_REPOSITORY_API+"/releases/"+suffix, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	response, err := s.metadata.Do(request)
	if err != nil {
		return nil, githubNetworkError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, githubStatusError(response)
	}
	if response.ContentLength > MAX_GITHUB_METADATA_BYTES {
		return nil, fmt.Errorf("%w: release metadata is too large", ErrGitHubUnavailable)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MAX_GITHUB_METADATA_BYTES+1))
	if err != nil {
		return nil, githubNetworkError(ctx, err)
	}
	if len(data) > MAX_GITHUB_METADATA_BYTES {
		return nil, fmt.Errorf("%w: release metadata is too large", ErrGitHubUnavailable)
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, fmt.Errorf("%w: invalid release metadata", ErrGitHubUnavailable)
	}
	return &release, nil
}

func selectGitHubRelease(release *githubRelease, targetOS, targetArch string) (*githubSelection, error) {
	if !SupportedPlatform(targetOS, targetArch) {
		return nil, ErrNoCompatiblePackage
	}
	if release.ID <= 0 || release.Draft || release.Prerelease || !ValidVersion(release.TagName) || semver.Prerelease("v"+strings.TrimPrefix(release.TagName, "v")) != "" {
		return nil, fmt.Errorf("%w: latest release must have a stable semantic version", ErrGitHubUnavailable)
	}
	published, err := time.Parse(time.RFC3339, release.PublishedAt)
	if err != nil || published.IsZero() {
		return nil, fmt.Errorf("%w: invalid publication date", ErrGitHubUnavailable)
	}
	name := APPLICATION + "-" + release.TagName + "-" + targetOS + "-" + targetArch + ".tar.gz"
	var selected *githubAsset
	for i := range release.Assets {
		asset := &release.Assets[i]
		if asset.Name != name {
			continue
		}
		if selected != nil || asset.ID <= 0 || asset.State != "uploaded" || asset.Size <= 0 || asset.Size > MAX_PACKAGE_BYTES {
			return nil, fmt.Errorf("%w: invalid or ambiguous package asset", ErrNoCompatiblePackage)
		}
		selected = asset
	}
	if selected == nil {
		return nil, ErrNoCompatiblePackage
	}
	notes := release.Body
	if len(notes) > int(MAX_NOTES_BYTES) {
		notes = notes[:MAX_NOTES_BYTES]
		for !utf8.ValidString(notes) {
			notes = notes[:len(notes)-1]
		}
	}
	return &githubSelection{
		release: Release{
			Version: release.TagName, Notes: notes, PublishedAt: release.PublishedAt,
			PackageName: name, Size: selected.Size,
		},
		releaseID: release.ID, assetID: selected.ID, targetOS: targetOS, arch: targetArch,
	}, nil
}

func (s *GitHubSource) Latest(ctx context.Context, targetOS, targetArch string) (*Release, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !SupportedPlatform(targetOS, targetArch) {
		return nil, ErrNoCompatiblePackage
	}
	release, err := s.readRelease(ctx, "latest")
	if err != nil {
		return nil, err
	}
	var selection *githubSelection
	if release != nil {
		selection, err = selectGitHubRelease(release, targetOS, targetArch)
		if err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.selected = selection
	s.mu.Unlock()
	if selection == nil {
		// GitHub returns 404 when this public repository has no published release.
		return nil, nil
	}
	result := selection.release
	return &result, nil
}

func (s *GitHubSource) Open(ctx context.Context, release Release) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	selected := s.selected
	s.mu.Unlock()
	if selected == nil || release != selected.release {
		return nil, ErrVersionMismatch
	}
	// Re-read the selected tag, not "latest": publishing a newer release must
	// not silently change the package the administrator already selected.
	current, err := s.readRelease(ctx, "tags/"+url.PathEscape(release.Version))
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, ErrVersionMismatch
	}
	confirmed, err := selectGitHubRelease(current, selected.targetOS, selected.arch)
	if err != nil || confirmed.releaseID != selected.releaseID || confirmed.assetID != selected.assetID || confirmed.release.Version != release.Version || confirmed.release.Size != release.Size || confirmed.release.PackageName != release.PackageName {
		return nil, ErrVersionMismatch
	}
	// Asset IDs cannot be reused for replacement uploads. Ignore URLs from
	// release metadata and construct the fixed-repository API endpoint ourselves.
	request, err := githubRequest(ctx, GITHUB_REPOSITORY_API+"/releases/assets/"+strconv.FormatInt(selected.assetID, 10), "application/octet-stream")
	if err != nil {
		return nil, err
	}
	response, err := s.download.Do(request)
	if err != nil {
		return nil, githubNetworkError(ctx, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, ErrVersionMismatch
		}
		return nil, githubStatusError(response)
	}
	if response.ContentLength > MAX_PACKAGE_BYTES {
		response.Body.Close()
		return nil, ErrTooLarge
	}
	if response.ContentLength >= 0 && response.ContentLength != release.Size {
		response.Body.Close()
		return nil, ErrVersionMismatch
	}
	return &githubPackageReader{reader: io.LimitReader(response.Body, MAX_PACKAGE_BYTES+1), body: response.Body, ctx: ctx}, nil
}

type githubPackageReader struct {
	reader io.Reader
	body   io.ReadCloser
	ctx    context.Context
}

func (r *githubPackageReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	if err != nil && err != io.EOF {
		return n, githubNetworkError(r.ctx, err)
	}
	return n, err
}

func (r *githubPackageReader) Close() error { return r.body.Close() }
