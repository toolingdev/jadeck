package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Release is the subset of the GitHub release JSON we use.
type Release struct {
	Tag    string  `json:"tag_name"`
	Name   string  `json:"name"`
	Body   string  `json:"body"`
	Draft  bool    `json:"draft"`
	Pre    bool    `json:"prerelease"`
	Assets []Asset `json:"assets"`
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"` // API URL (works for private repos with a token)
	Size int64  `json:"size"`
}

// Source fetches releases. API defaults to https://api.github.com.
type Source struct {
	Repo  string // owner/name
	Token string // optional: private repos
	API   string
	Beta  bool // also offer pre-releases
	HTTP  *http.Client
}

func (s *Source) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (s *Source) get(ctx context.Context, url, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "ptz-controller-updater")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub answered HTTP %d for %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download larger than %d bytes", limit)
	}
	return b, nil
}

// Latest returns the newest published release: non-prerelease, or
// on the beta channel the newest of either kind.
// Releases lists recent releases (newest first, as GitHub returns them).
func (s *Source) Releases(ctx context.Context) ([]Release, error) {
	api := s.API
	if api == "" {
		api = "https://api.github.com"
	}
	b, err := s.get(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=50", strings.TrimRight(api, "/"), s.Repo),
		"application/vnd.github+json", 32<<20)
	if err != nil {
		return nil, err
	}
	var list []Release
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("unreadable release list: %w", err)
	}
	return list, nil
}

// IsAddonRelease: add-ons are released from the same repository with
// tags like addon-proclaim-v1.1.0; app updates skip them.
func IsAddonRelease(tag string) bool { return strings.HasPrefix(tag, "addon-") }

// Latest is the newest app release (pre-releases only on the beta
// channel; add-on releases never).
func (s *Source) Latest(ctx context.Context) (*Release, error) {
	list, err := s.Releases(ctx)
	if err != nil {
		return nil, err
	}
	var best *Release
	for i := range list {
		r := &list[i]
		if r.Draft || IsAddonRelease(r.Tag) || (r.Pre && !s.Beta) {
			continue
		}
		if best == nil || Newer(r.Tag, best.Tag) {
			best = r
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no releases in %s", s.Repo)
	}
	return best, nil
}

// Download fetches one asset's bytes.
func (s *Source) Download(ctx context.Context, a Asset, limit int64) ([]byte, error) {
	return s.get(ctx, a.URL, "application/octet-stream", limit)
}
