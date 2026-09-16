package wcl

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ErrNotCharacterLink is a link that does not name a Warcraft Logs
// character. Its message shows what one looks like (FR-034).
var ErrNotCharacterLink = errors.New("that is not a Warcraft Logs character link; one looks like https://www.warcraftlogs.com/character/us/area-52/name")

var regions = map[string]bool{"us": true, "eu": true, "kr": true, "tw": true, "cn": true}

// ParseCharacterLink reads a character page link:
//
//	https://www.warcraftlogs.com/character/us/area-52/nekromoo
//	https://www.warcraftlogs.com/character/id/1234567
//
// with or without the scheme or "www.", a trailing slash, or a query.
func ParseCharacterLink(s string) (CharacterRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return CharacterRef{}, ErrNotCharacterLink
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return CharacterRef{}, ErrNotCharacterLink
	}
	host := strings.ToLower(u.Hostname())
	if host != "warcraftlogs.com" && !strings.HasSuffix(host, ".warcraftlogs.com") {
		return CharacterRef{}, ErrNotCharacterLink
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "character" {
		return CharacterRef{}, ErrNotCharacterLink
	}
	if parts[1] == "id" {
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || id <= 0 {
			return CharacterRef{}, ErrNotCharacterLink
		}
		return CharacterRef{ID: id}, nil
	}
	if len(parts) < 4 {
		return CharacterRef{}, ErrNotCharacterLink
	}
	region, slug, name := strings.ToLower(parts[1]), strings.ToLower(parts[2]), parts[3]
	if !regions[region] || slug == "" || name == "" {
		return CharacterRef{}, ErrNotCharacterLink
	}
	return CharacterRef{Region: region, Slug: slug, Name: name}, nil
}
