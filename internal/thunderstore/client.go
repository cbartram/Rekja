package thunderstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Client reads Thunderstore's public Valheim package index and package zips.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Thunderstore client for a community host such as
// https://valheim.thunderstore.io.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

// FetchIndex downloads the full public package index.
func (c *Client) FetchIndex(ctx context.Context) ([]Package, error) {
	endpoint := c.baseURL + "/api/v1/package/"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("thunderstore index request failed: %s", response.Status)
	}

	var packages []Package
	if err := json.NewDecoder(response.Body).Decode(&packages); err != nil {
		return nil, err
	}
	return packages, nil
}

// OpenDownload returns an HTTP response body for a version zip. The caller owns
// closing the response body.
func (c *Client) OpenDownload(ctx context.Context, downloadURL string) (*http.Response, error) {
	if _, err := url.ParseRequestURI(downloadURL); err != nil {
		return nil, fmt.Errorf("invalid download url %q: %w", downloadURL, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		response.Body.Close()
		return nil, fmt.Errorf("download failed: %s", response.Status)
	}
	return response, nil
}

// LatestActive returns the latest active version by Thunderstore semantic
// version ordering.
func LatestActive(pkg Package) (Version, bool, error) {
	var latest Version
	found := false
	for _, version := range pkg.Versions {
		if !version.IsActive {
			continue
		}
		if !found {
			latest = version
			found = true
			continue
		}
		compare, err := CompareVersions(version.VersionNumber, latest.VersionNumber)
		if err != nil {
			return Version{}, false, err
		}
		if compare > 0 {
			latest = version
		}
	}
	return latest, found, nil
}

// FindVersion returns the requested version. "latest" and empty resolve to the
// latest active version.
func FindVersion(pkg Package, desired string) (Version, bool, error) {
	if desired == "" || desired == "latest" {
		return LatestActive(pkg)
	}
	for _, version := range pkg.Versions {
		if version.VersionNumber == desired && version.IsActive {
			return version, true, nil
		}
	}
	return Version{}, false, nil
}

// IndexByFullName maps packages by Namespace-Name.
func IndexByFullName(packages []Package) map[string]Package {
	index := make(map[string]Package, len(packages))
	for _, pkg := range packages {
		index[pkg.FullName] = pkg
	}
	return index
}
