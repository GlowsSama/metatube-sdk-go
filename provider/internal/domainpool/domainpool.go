package domainpool

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
)

const MaxDomains = 5

// Pool stores bounded fallback domains for a provider. The provider's primary
// domain is always supplied by the caller and remains first in the candidates.
type Pool struct {
	mu      sync.RWMutex
	backups []*url.URL
}

func New() *Pool {
	return &Pool{}
}

func (p *Pool) SetBackups(values []string) error {
	backups := make([]*url.URL, 0, len(values))
	seen := make(map[string]struct{})

	for _, value := range values {
		for _, raw := range strings.Split(value, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			u, err := normalize(raw)
			if err != nil {
				return err
			}
			key := hostKey(u)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			backups = append(backups, u)
		}
	}

	if len(backups)+1 > MaxDomains {
		return fmt.Errorf("too many domains: at most %d total domains are allowed", MaxDomains)
	}

	p.mu.Lock()
	p.backups = backups
	p.mu.Unlock()
	return nil
}

func (p *Pool) Candidates(primary *url.URL) []*url.URL {
	if primary == nil {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	base := clone(primary)
	base.Host = strings.ToLower(base.Host)
	base.Scheme = strings.ToLower(base.Scheme)
	candidates := make([]*url.URL, 0, len(p.backups)+1)
	seen := map[string]struct{}{hostKey(base): {}}
	candidates = append(candidates, base)

	for _, backup := range p.backups {
		key := hostKey(backup)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, clone(backup))
	}
	return candidates
}

func (p *Pool) Domains(primary *url.URL) []string {
	candidates := p.Candidates(primary)
	domains := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		domains = append(domains, candidate.String())
	}
	return domains
}

func (p *Pool) Matches(primary, candidate *url.URL) bool {
	if candidate == nil {
		return false
	}
	key := hostKey(candidate)
	for _, domain := range p.Candidates(primary) {
		if hostKey(domain) == key {
			return true
		}
	}
	return false
}

func normalize(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid domain %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid domain %q: only HTTP and HTTPS are supported", raw)
	}
	if u.User != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid domain %q: an absolute HTTP URL with a host is required", raw)
	}
	if strings.ContainsAny(u.Hostname(), " \t\r\n/") {
		return nil, fmt.Errorf("invalid domain %q: invalid host name", raw)
	}

	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	return &url.URL{
		Scheme: strings.ToLower(u.Scheme),
		Host:   host,
		Path:   "/",
	}, nil
}

func hostKey(u *url.URL) string {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	host = strings.TrimPrefix(host, "www.")
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	return host
}

func clone(u *url.URL) *url.URL {
	cloned := *u
	return &cloned
}
