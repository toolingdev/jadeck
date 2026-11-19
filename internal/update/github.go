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
func (s *Source) Latest(ctx context.Context) (*Release, error) {
	api := s.API
	if api == "" {
		api = "https://api.github.com"
	}
	if s.Beta {
		b, err := s.get(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=20", strings.TrimRight(api, "/"), s.Repo),
			"application/vnd.github+json", 16<<20)
		if err != nil {
			return nil, err
		}
		var list []Release
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, fmt.Errorf("unreadable release list: %w", err)
		}
		var best *Release
		for i := range list {
			if list[i].Draft {
				continue
			}
			if best == nil || Newer(list[i].Tag, best.Tag) {
				best = &list[i]
			}
		}
		if best == nil {
			return nil, fmt.Errorf("no releases in %s", s.Repo)
		}
		return best, nil
	}
	b, err := s.get(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(api, "/"), s.Repo),
		"application/vnd.github+json", 4<<20)
	if err != nil {
		return nil, err
	}
	var r Release
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("unreadable release JSON: %w", err)
	}
	return &r, nil
}

// Download fetches one asset's bytes.
func (s *Source) Download(ctx context.Context, a Asset, limit int64) ([]byte, error) {
	return s.get(ctx, a.URL, "application/octet-stream", limit)
}
