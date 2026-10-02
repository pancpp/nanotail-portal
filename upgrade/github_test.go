package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type githubTestTransport func(*http.Request) (*http.Response, error)

func (transport githubTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func githubTestSource(transport githubTestTransport) *GitHubSource {
	source := NewGitHubSource()
	source.metadata.Transport = transport
	source.download.Transport = transport
	return source
}

func githubTestResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)), Request: request,
	}
}

func githubTestRelease() githubRelease {
	return githubRelease{
		ID: 123, TagName: "v1.2.3", Body: "New portal release", PublishedAt: "2026-10-02T01:00:00Z",
		Assets: []githubAsset{{ID: 456, Name: "nanotail-portal-v1.2.3-linux-arm64.tar.gz", State: "uploaded", Size: 7}},
	}
}

func githubTestJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGitHubCheckAndDownloadSelectedAsset(t *testing.T) {
	release := githubTestRelease()
	release.Assets = append(release.Assets, githubAsset{ID: 789, Name: "nanotail-portal-v1.2.3-linux-amd64.tar.gz", State: "uploaded", Size: 7})
	var paths []string
	source := githubTestSource(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "api.github.com" || request.Header.Get("Authorization") != "" || request.Header.Get("User-Agent") == "" || request.Header.Get("X-GitHub-Api-Version") != GITHUB_API_VERSION {
			t.Fatalf("unexpected GitHub request: %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		if strings.HasSuffix(request.URL.Path, "/assets/456") {
			if request.Header.Get("Accept") != "application/octet-stream" || request.Header.Get("Accept-Encoding") != "identity" {
				t.Fatal("asset download did not request exact binary content")
			}
			return githubTestResponse(request, http.StatusOK, "package"), nil
		}
		if request.Header.Get("Accept") != "application/vnd.github+json" {
			t.Fatal("metadata request used wrong accept header")
		}
		return githubTestResponse(request, http.StatusOK, githubTestJSON(t, release)), nil
	})
	selected, err := source.Latest(t.Context(), "linux", "arm64")
	if err != nil || selected == nil || selected.Version != "v1.2.3" || selected.PackageName != release.Assets[0].Name || selected.Size != 7 || selected.Notes != release.Body {
		t.Fatalf("unexpected selected release: %+v, %v", selected, err)
	}
	reader, err := source.Open(t.Context(), *selected)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "package" {
		t.Fatalf("asset download: %q, %v", data, err)
	}
	if len(paths) != 3 || paths[0] != "/repos/pancpp/nanotail-portal/releases/latest" || paths[1] != "/repos/pancpp/nanotail-portal/releases/tags/v1.2.3" || paths[2] != "/repos/pancpp/nanotail-portal/releases/assets/456" {
		t.Fatalf("download did not revalidate selected immutable asset: %v", paths)
	}
}

func TestGitHubReleaseMetadataValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*githubRelease)
		want   error
	}{
		{"draft", func(r *githubRelease) { r.Draft = true }, ErrGitHubUnavailable},
		{"prerelease flag", func(r *githubRelease) { r.Prerelease = true }, ErrGitHubUnavailable},
		{"prerelease version", func(r *githubRelease) { r.TagName = "v2.0.0-rc.1" }, ErrGitHubUnavailable},
		{"non semantic version", func(r *githubRelease) { r.TagName = "latest" }, ErrGitHubUnavailable},
		{"malformed version", func(r *githubRelease) { r.TagName = "v01.2.3" }, ErrGitHubUnavailable},
		{"invalid release id", func(r *githubRelease) { r.ID = 0 }, ErrGitHubUnavailable},
		{"missing timestamp", func(r *githubRelease) { r.PublishedAt = "" }, ErrGitHubUnavailable},
		{"malformed timestamp", func(r *githubRelease) { r.PublishedAt = "yesterday" }, ErrGitHubUnavailable},
		{"missing asset", func(r *githubRelease) { r.Assets = nil }, ErrNoCompatiblePackage},
		{"wrong architecture", func(r *githubRelease) { r.Assets[0].Name = "nanotail-portal-v1.2.3-linux-amd64.tar.gz" }, ErrNoCompatiblePackage},
		{"wrong version", func(r *githubRelease) { r.Assets[0].Name = "nanotail-portal-v1.2.4-linux-arm64.tar.gz" }, ErrNoCompatiblePackage},
		{"wrong filename", func(r *githubRelease) { r.Assets[0].Name += ".exe" }, ErrNoCompatiblePackage},
		{"zero size", func(r *githubRelease) { r.Assets[0].Size = 0 }, ErrNoCompatiblePackage},
		{"negative size", func(r *githubRelease) { r.Assets[0].Size = -1 }, ErrNoCompatiblePackage},
		{"oversize", func(r *githubRelease) { r.Assets[0].Size = MAX_PACKAGE_BYTES + 1 }, ErrNoCompatiblePackage},
		{"not uploaded", func(r *githubRelease) { r.Assets[0].State = "starter" }, ErrNoCompatiblePackage},
		{"invalid asset id", func(r *githubRelease) { r.Assets[0].ID = 0 }, ErrNoCompatiblePackage},
		{"duplicate asset", func(r *githubRelease) { r.Assets = append(r.Assets, r.Assets[0]) }, ErrNoCompatiblePackage},
	} {
		t.Run(test.name, func(t *testing.T) {
			release := githubTestRelease()
			test.mutate(&release)
			source := githubTestSource(func(request *http.Request) (*http.Response, error) {
				return githubTestResponse(request, http.StatusOK, githubTestJSON(t, release)), nil
			})
			if selected, err := source.Latest(t.Context(), "linux", "arm64"); selected != nil || !errors.Is(err, test.want) {
				t.Fatalf("accepted bad metadata: %+v, %v", selected, err)
			}
		})
	}
}

func TestGitHubMetadataBoundsAndHTTPFailures(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		header     http.Header
		want       error
	}{
		{"no release", "", http.StatusNotFound, nil, nil},
		{"rate limited", "sensitive upstream diagnostic", http.StatusTooManyRequests, nil, ErrGitHubRateLimited},
		{"primary limit", "", http.StatusForbidden, http.Header{"X-Ratelimit-Remaining": {"0"}}, ErrGitHubRateLimited},
		{"secondary limit", "", http.StatusForbidden, http.Header{"Retry-After": {"60"}}, ErrGitHubRateLimited},
		{"forbidden", "sensitive upstream diagnostic", http.StatusForbidden, nil, ErrGitHubUnavailable},
		{"server failure", "", http.StatusBadGateway, nil, ErrGitHubUnavailable},
		{"malformed JSON", "not JSON", http.StatusOK, nil, ErrGitHubUnavailable},
		{"JSON suffix", githubTestJSON(t, githubTestRelease()) + " {}", http.StatusOK, nil, ErrGitHubUnavailable},
		{"large body", strings.Repeat(" ", MAX_GITHUB_METADATA_BYTES+1), http.StatusOK, nil, ErrGitHubUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := githubTestSource(func(request *http.Request) (*http.Response, error) {
				response := githubTestResponse(request, test.status, test.body)
				response.ContentLength = -1 // Also exercise the streaming size bound.
				if test.header != nil {
					response.Header = test.header
				}
				return response, nil
			})
			selected, err := source.Latest(t.Context(), "linux", "arm64")
			if selected != nil || !errors.Is(err, test.want) {
				t.Fatalf("wrong upstream failure: %+v, %v", selected, err)
			}
			if err != nil && strings.Contains(err.Error(), "sensitive") {
				t.Fatal("error leaked upstream body")
			}
		})
	}
}

func TestGitHubOpenRejectsChangedSelection(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*githubRelease)
	}{
		{"replaced asset", func(r *githubRelease) { r.Assets[0].ID++ }},
		{"replaced release", func(r *githubRelease) { r.ID++ }},
		{"changed asset size", func(r *githubRelease) { r.Assets[0].Size++ }},
		{"changed asset name", func(r *githubRelease) { r.Assets[0].Name = "other.tar.gz" }},
		{"changed tag", func(r *githubRelease) { r.TagName = "v1.2.4" }},
		{"unpublished", func(r *githubRelease) { r.Draft = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			release := githubTestRelease()
			source := githubTestSource(func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "/assets/") {
					t.Fatal("opened an asset after selection changed")
				}
				return githubTestResponse(request, http.StatusOK, githubTestJSON(t, release)), nil
			})
			selected, err := source.Latest(t.Context(), "linux", "arm64")
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&release)
			if _, err := source.Open(t.Context(), *selected); !errors.Is(err, ErrVersionMismatch) {
				t.Fatalf("changed asset accepted: %v", err)
			}
		})
	}
	source := githubTestSource(func(*http.Request) (*http.Response, error) {
		t.Fatal("unchecked selection caused a request")
		return nil, nil
	})
	if _, err := source.Open(t.Context(), Release{Version: "../../untrusted"}); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("unchecked selection accepted: %v", err)
	}
}

func TestGitHubRedirectPolicy(t *testing.T) {
	for _, destination := range []string{
		"http://release-assets.githubusercontent.com/package",
		"https://127.0.0.1/package",
		"https://169.254.169.254/latest/meta-data",
		"https://release-assets.githubusercontent.com.evil.invalid/package",
		"https://release-assets.githubusercontent.com:444/package",
		"https://user:secret@release-assets.githubusercontent.com/package",
		"https://api.github.com/repos/other/repository/releases/assets/456",
		"https://github.com/other/repository/releases/download/v1.2.3/package",
	} {
		t.Run(destination, func(t *testing.T) {
			release := githubTestRelease()
			source := githubTestSource(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "api.github.com" || !strings.HasPrefix(request.URL.Path, "/repos/"+GITHUB_REPOSITORY+"/") {
					t.Fatalf("followed unsafe redirect: %s", request.URL)
				}
				if strings.Contains(request.URL.Path, "/assets/") {
					response := githubTestResponse(request, http.StatusFound, "")
					response.Header.Set("Location", destination)
					return response, nil
				}
				return githubTestResponse(request, http.StatusOK, githubTestJSON(t, release)), nil
			})
			selected, err := source.Latest(t.Context(), "linux", "arm64")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.Open(t.Context(), *selected); !errors.Is(err, ErrGitHubUnavailable) {
				t.Fatalf("unsafe redirect accepted: %v", err)
			}
		})
	}
	for _, host := range []string{"release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com"} {
		t.Run(host, func(t *testing.T) {
			source := githubTestSource(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == host {
					return githubTestResponse(request, http.StatusOK, "package"), nil
				}
				if strings.Contains(request.URL.Path, "/assets/") {
					response := githubTestResponse(request, http.StatusFound, "")
					response.Header.Set("Location", "https://"+host+"/package?token=temporary")
					return response, nil
				}
				return githubTestResponse(request, http.StatusOK, githubTestJSON(t, githubTestRelease())), nil
			})
			selected, err := source.Latest(t.Context(), "linux", "arm64")
			if err != nil {
				t.Fatal(err)
			}
			reader, err := source.Open(t.Context(), *selected)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if data, err := io.ReadAll(reader); err != nil || string(data) != "package" {
				t.Fatalf("trusted CDN download failed: %q, %v", data, err)
			}
		})
	}
}

func TestGitHubCancellationTimeoutAndSafeErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	source := githubTestSource(func(request *http.Request) (*http.Response, error) {
		cancel()
		return nil, request.Context().Err()
	})
	if _, err := source.Latest(ctx, "linux", "arm64"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	source = githubTestSource(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	source.metadata.Timeout = time.Millisecond
	if _, err := source.Latest(t.Context(), "linux", "arm64"); !errors.Is(err, ErrGitHubUnavailable) {
		t.Fatalf("metadata timeout was not bounded: %v", err)
	}
	source = githubTestSource(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("https://release-assets.githubusercontent.com/package?secret=do-not-log")
	})
	if _, err := source.Latest(t.Context(), "linux", "arm64"); !errors.Is(err, ErrGitHubUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe network diagnostic: %v", err)
	}
}

func TestGitHubNotesBoundAndIgnoredURLs(t *testing.T) {
	release := githubTestRelease()
	release.Body = strings.Repeat("界", int(MAX_NOTES_BYTES))
	metadata := githubTestJSON(t, release)
	// Arbitrary URL fields in GitHub metadata never become request targets.
	metadata = strings.TrimSuffix(metadata, "}") + `,"url":"http://127.0.0.1/","browser_download_url":"http://169.254.169.254/"}`
	source := githubTestSource(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.github.com" {
			t.Fatalf("requested URL supplied in metadata: %s", request.URL)
		}
		if strings.Contains(request.URL.Path, "/assets/") {
			return githubTestResponse(request, http.StatusOK, "package"), nil
		}
		return githubTestResponse(request, http.StatusOK, metadata), nil
	})
	selected, err := source.Latest(t.Context(), "linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Notes) > int(MAX_NOTES_BYTES) || !utf8.ValidString(selected.Notes) {
		t.Fatal("release notes did not preserve bounded valid UTF-8")
	}
	reader, err := source.Open(t.Context(), *selected)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
}

func TestGitHubDownloadFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		size   int64
		want   error
	}{
		{"removed asset", http.StatusNotFound, 0, ErrVersionMismatch},
		{"rate limited", http.StatusTooManyRequests, 0, ErrGitHubRateLimited},
		{"server failure", http.StatusBadGateway, 0, ErrGitHubUnavailable},
		{"wrong content length", http.StatusOK, 8, ErrVersionMismatch},
		{"too large", http.StatusOK, MAX_PACKAGE_BYTES + 1, ErrTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := githubTestSource(func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "/assets/") {
					response := githubTestResponse(request, test.status, "")
					response.ContentLength = test.size
					return response, nil
				}
				return githubTestResponse(request, http.StatusOK, githubTestJSON(t, githubTestRelease())), nil
			})
			selected, err := source.Latest(t.Context(), "linux", "arm64")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.Open(t.Context(), *selected); !errors.Is(err, test.want) {
				t.Fatalf("wrong download error: %v", err)
			}
		})
	}
}

type githubFailingBody struct {
	err    error
	closed bool
}

func (body *githubFailingBody) Read([]byte) (int, error) { return 0, body.err }
func (body *githubFailingBody) Close() error             { body.closed = true; return nil }

func TestGitHubDownloadBodyErrors(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		body := &githubFailingBody{err: errors.New("failed https://cdn.invalid/file?secret=do-not-log")}
		source := githubTestSource(func(request *http.Request) (*http.Response, error) {
			if strings.Contains(request.URL.Path, "/assets/") {
				response := githubTestResponse(request, http.StatusOK, "package")
				response.Body = body
				return response, nil
			}
			return githubTestResponse(request, http.StatusOK, githubTestJSON(t, githubTestRelease())), nil
		})
		selected, err := source.Latest(ctx, "linux", "arm64")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		reader, err := source.Open(ctx, *selected)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		want := ErrGitHubUnavailable
		if canceled {
			cancel()
			want = context.Canceled
		}
		_, err = io.ReadAll(reader)
		reader.Close()
		cancel()
		if !errors.Is(err, want) || strings.Contains(err.Error(), "secret") || !body.closed {
			t.Fatalf("unsafe or unclassified body error: %v, closed=%v", err, body.closed)
		}
	}
}

func TestGitHubRedirectLoopAndMetadataRedirect(t *testing.T) {
	requests := 0
	source := githubTestSource(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "release-assets.githubusercontent.com" || strings.Contains(request.URL.Path, "/assets/") {
			requests++
			if requests > MAX_GITHUB_REDIRECTS {
				t.Fatal("redirect loop exceeded the limit")
			}
			response := githubTestResponse(request, http.StatusFound, "")
			response.Header.Set("Location", "https://release-assets.githubusercontent.com/loop")
			return response, nil
		}
		return githubTestResponse(request, http.StatusOK, githubTestJSON(t, githubTestRelease())), nil
	})
	selected, err := source.Latest(t.Context(), "linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(t.Context(), *selected); !errors.Is(err, ErrGitHubUnavailable) {
		t.Fatalf("redirect loop accepted: %v", err)
	}
	source = githubTestSource(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.github.com" {
			t.Fatal("metadata request escaped the API host")
		}
		response := githubTestResponse(request, http.StatusFound, "")
		response.Header.Set("Location", "https://release-assets.githubusercontent.com/metadata.json")
		return response, nil
	})
	if _, err := source.Latest(t.Context(), "linux", "arm64"); !errors.Is(err, ErrGitHubUnavailable) {
		t.Fatalf("metadata redirect accepted: %v", err)
	}
}

func TestGitHubRemovedReleaseClearsSelection(t *testing.T) {
	removed := false
	source := githubTestSource(func(request *http.Request) (*http.Response, error) {
		if removed {
			return githubTestResponse(request, http.StatusNotFound, ""), nil
		}
		return githubTestResponse(request, http.StatusOK, githubTestJSON(t, githubTestRelease())), nil
	})
	selected, err := source.Latest(t.Context(), "linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	removed = true
	if _, err := source.Open(t.Context(), *selected); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("removed tag accepted: %v", err)
	}
	if release, err := source.Latest(t.Context(), "linux", "arm64"); release != nil || err != nil {
		t.Fatalf("expected no published release: %+v, %v", release, err)
	}
	if _, err := source.Open(t.Context(), *selected); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("old selection accepted: %v", err)
	}
}

func TestGitHubOnlyChecksLinuxARM64(t *testing.T) {
	source := githubTestSource(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported platform caused a GitHub request")
		return nil, nil
	})
	for _, platform := range [][2]string{{"linux", "amd64"}, {"linux", "arm"}, {"darwin", "arm64"}, {"windows", "arm64"}} {
		if release, err := source.Latest(t.Context(), platform[0], platform[1]); release != nil || !errors.Is(err, ErrNoCompatiblePackage) {
			t.Fatalf("accepted unsupported platform %v: %+v %v", platform, release, err)
		}
	}
}
