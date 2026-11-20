package service

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Preview Controller routes only project-INTENT hosts below its configured
// domain. Accepting this form is opt-in and preserves the exact-origin checks
// for local and other private listeners.
func validatePreviewOrigin(project, domain string) error {
	if project == "" && domain == "" {
		return nil
	}
	if !validDNSLabel(project) || len(domain) > 253 || !strings.Contains(domain, ".") {
		return fmt.Errorf("preview project and domain must be lowercase DNS names")
	}
	for _, label := range strings.Split(domain, ".") {
		if !validDNSLabel(label) {
			return fmt.Errorf("preview project and domain must be lowercase DNS names")
		}
	}
	return nil
}

func validDNSLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, c := range label {
		if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func previewBrowserHost(host, project, domain string) bool {
	if validatePreviewOrigin(project, domain) != nil || project == "" {
		return false
	}
	prefix, suffix := project+"-", "."+domain
	if !strings.HasPrefix(host, prefix) || !strings.HasSuffix(host, suffix) {
		return false
	}
	number := strings.TrimSuffix(strings.TrimPrefix(host, prefix), suffix)
	if number == "" || number[0] == '0' {
		return false
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return false
		}
	}
	intent, err := strconv.ParseInt(number, 10, 64)
	return err == nil && intent > 0
}

func browserOriginsForListener(listen, extra, previewProject, previewDomain string) ([]string, error) {
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
	if len(origins) == 0 && (previewProject == "" || previewDomain == "") {
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
func allowedBrowserRequest(c fiber.Ctx, allowed []string, previewProject, previewDomain string) bool {
	origin := c.Get("Origin")
	site := c.Get("Sec-Fetch-Site")
	browser := origin != "" || site != "" || c.Get("Sec-Fetch-Mode") != "" || c.Get("Sec-Fetch-Dest") != ""
	// In-process API tests construct Handler without listener configuration.
	// Serve always supplies at least one configured origin.
	if len(allowed) == 0 && previewProject == "" && !browser {
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
	if previewBrowserHost(host, previewProject, previewDomain) {
		allowedHost = true
		allowedSource = allowedSource || strings.ToLower(origin) == "https://"+host
	}
	if !allowedHost {
		return false
	}
	if !browser {
		return true
	}
	return allowedSource || origin == "" && site == "same-origin"
}
