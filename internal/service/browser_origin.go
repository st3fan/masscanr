package service

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func browserOriginsForListener(listen, extra string) ([]string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("invalid listen address: %w", err)
	}
	var origins []string
	if host != "" && host != "0.0.0.0" && host != "::" {
		origins = append(origins, "http://"+net.JoinHostPort(host, port))
	}
	if extra != "" {
		origin, _, err := parseBrowserOrigin(extra)
		if err != nil {
			return nil, fmt.Errorf("invalid browser origin: %w", err)
		}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("browser origin is required for a wildcard listener")
	}
	return origins, nil
}

// parseBrowserOrigin accepts one serialized HTTP origin, without credentials or a path.
func parseBrowserOrigin(value string) (string, string, error) {
	u, err := url.Parse(value)
	if err != nil || u == nil || u.User != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.String() != value {
		return "", "", fmt.Errorf("browser origin must be an exact http(s) origin")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsUnspecified() {
		return "", "", fmt.Errorf("browser origin must name a concrete host")
	}
	return strings.ToLower(value), strings.ToLower(u.Host), nil
}

// Requests to the private API must use a configured Host. In particular, an
// attacker-controlled DNS name cannot become an accepted Host simply because
// it resolves to the private listener. Browser requests must additionally come
// from a configured origin. Headerless API clients need no browser headers;
// browsers cannot send JSON:API writes from another origin without a CORS
// preflight, which this server does not allow.
func allowedBrowserRequest(c fiber.Ctx, allowed []string) bool {
	origin := c.Get("Origin")
	site := c.Get("Sec-Fetch-Site")
	browser := origin != "" || site != "" || c.Get("Sec-Fetch-Mode") != "" || c.Get("Sec-Fetch-Dest") != ""
	// In-process API tests construct Handler without listener configuration.
	// Serve always supplies at least one configured origin.
	if len(allowed) == 0 && !browser {
		return true
	}
	if site != "" && site != "same-origin" {
		return false
	}
	host := strings.ToLower(string(c.Request().Host()))
	allowedHost, allowedSource := false, false
	for _, configured := range allowed {
		allowedOrigin, configuredHost, err := parseBrowserOrigin(configured)
		if err != nil {
			continue
		}
		allowedHost = allowedHost || host == configuredHost
		allowedSource = allowedSource || strings.ToLower(origin) == allowedOrigin
	}
	if !allowedHost {
		return false
	}
	if !browser {
		return true
	}
	return allowedSource || origin == "" && site == "same-origin"
}
