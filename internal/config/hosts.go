package config

import (
	"errors"
	"net/url"
	"strings"
)

// HostsURL accepts public GitHub file URLs; downloads go only to GitHub's raw host.
func HostsURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
		return "", errors.New("hosts_url must be an HTTPS GitHub file URL without credentials or query")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if u.Host == "github.com" && len(parts) >= 5 && parts[2] == "blob" {
		parts = append(parts[:2], parts[3:]...)
		u.Host = "raw.githubusercontent.com"
		u.Path = "/" + strings.Join(parts, "/")
		u.RawPath = ""
	}
	if u.Host != "raw.githubusercontent.com" || len(parts) < 4 {
		return "", errors.New("hosts_url must point to a GitHub blob or raw.githubusercontent.com file")
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", errors.New("invalid hosts_url path")
		}
	}
	return u.String(), nil
}
