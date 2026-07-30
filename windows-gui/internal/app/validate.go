package app

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var applePathPattern = regexp.MustCompile(`^/[a-zA-Z]{2}/(album|song|artist)/.+/\d+/?$`)

type LinkInfo struct {
	URL        string
	Kind       string
	SingleSong bool
}

func ValidateAppleMusicLink(raw string) (LinkInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return LinkInfo{}, errors.New("请输入 Apple Music 链接")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return LinkInfo{}, errors.New("请输入有效的 HTTPS Apple Music 链接")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "music.apple.com" && host != "beta.music.apple.com" && host != "classical.music.apple.com" {
		return LinkInfo{}, errors.New("链接必须来自 music.apple.com")
	}
	matches := applePathPattern.FindStringSubmatch(parsed.EscapedPath())
	if len(matches) != 2 {
		return LinkInfo{}, errors.New("目前支持专辑、单曲和艺人链接")
	}
	return LinkInfo{URL: parsed.String(), Kind: matches[1], SingleSong: matches[1] == "song" || parsed.Query().Get("i") != ""}, nil
}
