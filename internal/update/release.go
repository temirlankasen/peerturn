package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	// Repository is the GitHub project that publishes signed PeerTurn releases.
	Repository = "temirlankasen/peerturn"
	// APIBase is the GitHub REST API root.
	APIBase = "https://api.github.com"
	// DownloadBase is the root under which release assets live as <tag>/<asset>.
	DownloadBase = "https://github.com/" + Repository + "/releases/download"

	apiAccept      = "application/vnd.github+json"
	maxAPIResponse = 4 << 20
	listPageSize   = 15
)

// Release names one published version.
type Release struct {
	Tag     string // v0.2.0-preview
	Version string // 0.2.0-preview
}

type releaseRecord struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
}

// Latest returns the newest published release.
//
// GitHub's "latest" endpoint skips prereleases and every PeerTurn preview is
// one, so a 404 there falls back to the newest tag in the release list.
func Latest(ctx context.Context, client *http.Client, apiBase string) (Release, error) {
	base := strings.TrimSuffix(apiBase, "/")
	body, err := get(ctx, client, base+"/repos/"+Repository+"/releases/latest", maxAPIResponse, apiAccept)
	if status, ok := errors.AsType[*statusError](err); ok && status.status == http.StatusNotFound {
		return latestFromList(ctx, client, base)
	}
	if err != nil {
		return Release{}, err
	}
	var record releaseRecord
	if err = json.Unmarshal(body, &record); err != nil {
		return Release{}, errors.New("the latest release answer is not valid JSON")
	}
	return releaseFromTag(record.TagName)
}

func latestFromList(ctx context.Context, client *http.Client, base string) (Release, error) {
	body, err := get(ctx, client, fmt.Sprintf("%s/repos/%s/releases?per_page=%d", base, Repository, listPageSize), maxAPIResponse, apiAccept)
	if err != nil {
		return Release{}, fmt.Errorf("could not list releases: %w", err)
	}
	var records []releaseRecord
	if err = json.Unmarshal(body, &records); err != nil {
		return Release{}, errors.New("the release list answer is not valid JSON")
	}
	var best Release
	for _, record := range records {
		release, tagErr := releaseFromTag(record.TagName)
		if record.Draft || tagErr != nil {
			continue
		}
		if best.Tag == "" || Newer(best.Version, release.Version) {
			best = release
		}
	}
	if best.Tag == "" {
		return Release{}, errors.New("no published release was found")
	}
	return best, nil
}

// releaseFromTag accepts only v<version>: the tag later becomes part of a URL
// and of the signing identity the signature is checked against.
func releaseFromTag(tag string) (Release, error) {
	version, found := strings.CutPrefix(tag, "v")
	if !found || !ValidVersion(version) {
		return Release{}, fmt.Errorf("the release tag %q is not a version", shorten(tag))
	}
	return Release{Tag: tag, Version: version}, nil
}
