package testflight

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Version struct {
	Name string `json:"trainVersion"`
}

type build struct {
	ID         int64  `json:"id"`
	BundleID   string `json:"bundleId"`
	Version    string `json:"cfBundleShortVersion"`
	UploadDate string `json:"uploadDate"`
	Latest     bool   `json:"latestAvailableBuild"`
}

func (c *Client) ListVersions(ctx context.Context, appID int64) ([]Version, error) {
	if appID <= 0 {
		return nil, errors.New("TestFlight app ID must be positive")
	}

	path := c.accountPath() + "/apps/" + strconv.FormatInt(appID, 10) + "/platforms/ios/trains"

	var response struct {
		Data []Version `json:"data"`
	}
	if err := c.send(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}

	versions := make([]Version, 0, len(response.Data))

	seen := make(map[string]bool, len(response.Data))
	for _, version := range response.Data {
		version.Name = strings.TrimSpace(version.Name)
		if version.Name == "" || seen[version.Name] {
			continue
		}

		seen[version.Name] = true
		versions = append(versions, version)
	}

	return versions, nil
}

func (c *Client) listBuilds(ctx context.Context, appID int64, version string) ([]build, error) {
	if appID <= 0 || strings.TrimSpace(version) == "" {
		return nil, errors.New("TestFlight app ID and version are required")
	}

	path := c.accountPath() + "/apps/" + strconv.FormatInt(appID, 10) +
		"/platforms/ios/trains/" + url.PathEscape(version) + "/builds"

	var response struct {
		Data []build `json:"data"`
	}
	if err := c.send(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}

	builds := make([]build, 0, len(response.Data))
	for _, build := range response.Data {
		if build.ID <= 0 || (build.Version != "" && build.Version != version) ||
			(build.BundleID != "" && !identityToken.MatchString(build.BundleID)) {
			continue
		}

		builds = append(builds, build)
	}

	sortBuilds(builds)

	return builds, nil
}

func sortBuilds(builds []build) {
	sort.SliceStable(builds, func(i, j int) bool {
		if builds[i].Latest != builds[j].Latest {
			return builds[i].Latest
		}

		iTime, iErr := time.Parse(time.RFC3339, builds[i].UploadDate)

		jTime, jErr := time.Parse(time.RFC3339, builds[j].UploadDate)
		if iErr == nil && jErr == nil && !iTime.Equal(jTime) {
			return iTime.After(jTime)
		}

		return builds[i].ID > builds[j].ID
	})
}

func selectVersion(versions []Version, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if len(versions) == 0 {
			return "", errors.New("no TestFlight versions are available")
		}

		return versions[0].Name, nil
	}

	for _, version := range versions {
		if version.Name == requested {
			return requested, nil
		}
	}

	return "", fmt.Errorf("TestFlight version %q is not available", requested)
}

func (c *Client) accountPath() string {
	return "/v2/accounts/" + url.PathEscape(c.accountID)
}
