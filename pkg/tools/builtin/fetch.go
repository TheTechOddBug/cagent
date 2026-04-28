package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/k3a/html2text"
	"github.com/temoto/robotstxt"

	"github.com/docker/docker-agent/pkg/remote"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/useragent"
)

const (
	ToolNameFetch = "fetch"
)

type FetchTool struct {
	handler *fetchHandler
}

// Verify interface compliance
var (
	_ tools.ToolSet      = (*FetchTool)(nil)
	_ tools.Instructable = (*FetchTool)(nil)
)

type fetchHandler struct {
	timeout        time.Duration
	allowedDomains []string
	blockedDomains []string
}

type FetchToolArgs struct {
	URLs    []string `json:"urls"`
	Timeout int      `json:"timeout,omitempty"`
	Format  string   `json:"format,omitempty"`
}

func (h *fetchHandler) CallTool(ctx context.Context, params FetchToolArgs) (*tools.ToolCallResult, error) {
	if len(params.URLs) == 0 {
		return nil, errors.New("at least one URL is required")
	}

	// Set timeout if specified
	client := &http.Client{
		Timeout:   h.timeout,
		Transport: remote.NewTransport(ctx),
	}
	if params.Timeout > 0 {
		client.Timeout = time.Duration(params.Timeout) * time.Second
	}

	var results []FetchResult

	// Cache parsed robots.txt per host
	robotsCache := make(map[string]*robotstxt.RobotsData)

	for _, urlStr := range params.URLs {
		result := h.fetchURL(ctx, client, urlStr, params.Format, robotsCache)
		results = append(results, result)
	}

	// If only one URL, return simpler format
	if len(params.URLs) == 1 {
		result := results[0]
		if result.Error != "" {
			return tools.ResultError(fmt.Sprintf("Error fetching %s: %s", result.URL, result.Error)), nil
		}
		return tools.ResultSuccess(fmt.Sprintf("Successfully fetched %s (Status: %d, Length: %d bytes):\n\n%s",
			result.URL, result.StatusCode, result.ContentLength, result.Body)), nil
	}

	// Multiple URLs - return structured results
	return tools.ResultJSON(results), nil
}

type FetchResult struct {
	URL           string `json:"url"`
	StatusCode    int    `json:"statusCode"`
	Status        string `json:"status"`
	ContentType   string `json:"contentType,omitempty"`
	ContentLength int    `json:"contentLength"`
	Body          string `json:"body,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (h *fetchHandler) fetchURL(ctx context.Context, client *http.Client, urlStr, format string, robotsCache map[string]*robotstxt.RobotsData) FetchResult {
	result := FetchResult{URL: urlStr}

	// Validate URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		result.Error = fmt.Sprintf("invalid URL: %v", err)
		return result
	}

	// Check for valid URL structure
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		result.Error = "invalid URL: missing scheme or host"
		return result
	}

	// Only allow HTTP and HTTPS
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		result.Error = "only HTTP and HTTPS URLs are supported"
		return result
	}

	// Enforce domain allow/deny lists configured on the toolset.
	if err := h.checkDomainAllowed(parsedURL); err != nil {
		result.Error = err.Error()
		return result
	}

	// Check robots.txt (with caching per host)
	host := parsedURL.Host
	robots, cached := robotsCache[host]
	if !cached {
		var err error
		robots, err = h.fetchRobots(ctx, client, parsedURL, useragent.Header)
		if err != nil {
			result.Error = fmt.Sprintf("robots.txt check failed: %v", err)
			return result
		}
		robotsCache[host] = robots
	}

	if robots != nil && !robots.TestAgent(parsedURL.Path, useragent.Header) {
		result.Error = "URL blocked by robots.txt"
		return result
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, http.NoBody)
	if err != nil {
		result.Error = fmt.Sprintf("failed to create request: %v", err)
		return result
	}

	req.Header.Set("User-Agent", useragent.Header)

	switch format {
	case "markdown":
		req.Header.Set("Accept", "text/markdown;q=1.0, text/plain;q=0.9, text/html;q=0.7, */*;q=0.1")
	case "html":
		req.Header.Set("Accept", "text/html;q=1.0, text/plain;q=0.8, */*;q=0.1")
	case "text":
		req.Header.Set("Accept", "text/plain;q=1.0,  text/markdown;q=0.9, text/html;q=0.8, */*;q=0.1")
	default:
		req.Header.Set("Accept", "text/plain;q=1.0, */*;q=0.1")
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("request failed: %v", err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Status = resp.Status
	result.ContentType = resp.Header.Get("Content-Type")

	// Read response body
	maxSize := int64(1 << 20) // 1MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		result.Error = fmt.Sprintf("failed to read response body: %v", err)
		return result
	}

	contentType := resp.Header.Get("Content-Type")

	switch format {
	case "markdown":
		if strings.Contains(contentType, "text/html") {
			result.Body = htmlToMarkdown(string(body))
		} else {
			result.Body = string(body)
		}
	case "html":
		result.Body = string(body)
	case "text":
		if strings.Contains(contentType, "text/html") {
			result.Body = htmlToText(string(body))
		} else {
			result.Body = string(body)
		}
	default:
		result.Body = string(body)
	}

	result.ContentLength = len(result.Body)

	return result
}

// fetchRobots fetches and parses robots.txt for the given URL's host.
// Returns nil (allow all) if robots.txt is missing or unreachable.
// Returns an error if the server returns a non-OK status or the content cannot be read/parsed.
func (h *fetchHandler) fetchRobots(ctx context.Context, client *http.Client, targetURL *url.URL, userAgent string) (*robotstxt.RobotsData, error) {
	// Build robots.txt URL
	robotsURL := &url.URL{
		Scheme: targetURL.Scheme,
		Host:   targetURL.Host,
		Path:   "/robots.txt",
	}

	// Create request for robots.txt
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL.String(), http.NoBody)
	if err != nil {
		// If we can't create request, allow the fetch
		return nil, nil
	}

	req.Header.Set("User-Agent", userAgent)

	// Create robots client with same timeout and transport as main client
	robotsClient := &http.Client{
		Timeout:   client.Timeout,   // Same timeout as main client
		Transport: client.Transport, // Inherit proxy/transport settings
	}

	resp, err := robotsClient.Do(req)
	if err != nil {
		// If robots.txt is unreachable, allow the fetch
		return nil, nil
	}
	defer resp.Body.Close()

	// If robots.txt doesn't exist (404), allow the fetch
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	// For other non-200 status codes, fail the fetch
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	// Read robots.txt content (limit to 64KB)
	robotsBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read robots.txt: %w", err)
	}

	// Parse robots.txt
	robots, err := robotstxt.FromBytes(robotsBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse robots.txt: %w", err)
	}

	return robots, nil
}

// checkDomainAllowed returns nil if u's host is permitted by the configured
// allow- and block-lists, or a descriptive error otherwise. When neither list
// is configured, every URL is allowed.
func (h *fetchHandler) checkDomainAllowed(u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return errors.New("URL has no host")
	}
	if len(h.blockedDomains) > 0 && matchesAnyDomain(host, h.blockedDomains) {
		return fmt.Errorf("URL host %q is blocked by blocked_domains", host)
	}
	if len(h.allowedDomains) > 0 && !matchesAnyDomain(host, h.allowedDomains) {
		return fmt.Errorf("URL host %q is not in allowed_domains", host)
	}
	return nil
}

// matchesAnyDomain reports whether host matches any of the supplied patterns.
// See matchesDomain for the matching rules.
func matchesAnyDomain(host string, patterns []string) bool {
	for _, p := range patterns {
		if matchesDomain(host, p) {
			return true
		}
	}
	return false
}

// matchesDomain reports whether host matches pattern.
//
// Matching rules (case-insensitive):
//   - An empty pattern matches nothing.
//   - A pattern with a leading dot (".example.com") matches strict subdomains
//     of example.com but NOT example.com itself.
//   - Any other pattern ("example.com") matches the host exactly and any of
//     its subdomains (e.g. "docs.example.com"). It does NOT match unrelated
//     hosts that share a suffix (e.g. "badexample.com").
func matchesDomain(host, pattern string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if host == "" || pattern == "" {
		return false
	}
	// Strip IPv6 brackets if any (url.URL.Hostname already does this, but be safe).
	host = strings.Trim(host, "[]")

	subdomainOnly := strings.HasPrefix(pattern, ".")
	if subdomainOnly {
		pattern = strings.TrimPrefix(pattern, ".")
		if pattern == "" {
			return false
		}
		return strings.HasSuffix(host, "."+pattern)
	}
	if host == pattern {
		return true
	}
	return strings.HasSuffix(host, "."+pattern)
}

func htmlToMarkdown(html string) string {
	markdown, err := htmltomarkdown.ConvertString(html)
	if err != nil {
		return html
	}
	return markdown
}

func htmlToText(html string) string {
	return html2text.HTML2Text(html)
}

func NewFetchTool(options ...FetchToolOption) *FetchTool {
	tool := &FetchTool{
		handler: &fetchHandler{
			timeout: 30 * time.Second,
		},
	}

	for _, opt := range options {
		opt(tool)
	}

	return tool
}

type FetchToolOption func(*FetchTool)

func WithTimeout(timeout time.Duration) FetchToolOption {
	return func(t *FetchTool) {
		t.handler.timeout = timeout
	}
}

// WithAllowedDomains restricts the fetch tool to URLs whose host matches one
// of the supplied domain patterns. See matchesDomain for matching rules.
// An empty or nil slice disables the allow-list (every host is allowed).
func WithAllowedDomains(domains []string) FetchToolOption {
	return func(t *FetchTool) {
		t.handler.allowedDomains = domains
	}
}

// WithBlockedDomains forbids the fetch tool from fetching URLs whose host
// matches one of the supplied domain patterns. See matchesDomain for matching
// rules. An empty or nil slice disables the deny-list.
func WithBlockedDomains(domains []string) FetchToolOption {
	return func(t *FetchTool) {
		t.handler.blockedDomains = domains
	}
}

func (t *FetchTool) Instructions() string {
	var b strings.Builder
	b.WriteString("## Fetch Tool\n\n")
	b.WriteString("Fetch content from HTTP/HTTPS URLs. Supports multiple URLs per call, output format selection (text, markdown, html), and respects robots.txt.")
	if len(t.handler.allowedDomains) > 0 {
		b.WriteString("\n\nThis tool is restricted to the following domains (and their subdomains): ")
		b.WriteString(strings.Join(t.handler.allowedDomains, ", "))
		b.WriteString(". Requests to any other host will fail without making a network call.")
	}
	if len(t.handler.blockedDomains) > 0 {
		b.WriteString("\n\nThis tool is forbidden from fetching the following domains (and their subdomains): ")
		b.WriteString(strings.Join(t.handler.blockedDomains, ", "))
		b.WriteString(". Requests to those hosts will fail without making a network call.")
	}
	return b.String()
}

func (t *FetchTool) Tools(context.Context) ([]tools.Tool, error) {
	return []tools.Tool{
		{
			Name:        ToolNameFetch,
			Category:    "fetch",
			Description: "Fetch content from one or more HTTP/HTTPS URLs. Returns the response body and metadata.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"urls": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "Array of URLs to fetch",
						"minItems":    1,
					},
					"format": map[string]any{
						"type":        "string",
						"description": "The format to return the content in (text, markdown, or html)",
						"enum":        []string{"text", "markdown", "html"},
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Request timeout in seconds (default: 30)",
						"minimum":     1,
						"maximum":     300,
					},
				},
				"required": []string{"urls", "format"},
			},
			OutputSchema: tools.MustSchemaFor[string](),
			Handler:      tools.NewHandler(t.handler.CallTool),
			Annotations: tools.ToolAnnotations{
				Title: "Fetch URLs",
			},
		},
	}, nil
}
