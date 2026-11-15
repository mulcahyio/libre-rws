package gateway

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// allowRule is one allowed_aes entry: an AE title, optionally limited to an
// address or network.
type allowRule struct {
	ae     string
	prefix netip.Prefix // zero value: any address
}

func parseAllowRule(s string) (allowRule, error) {
	s = strings.TrimSpace(s)
	title, where, hasAddr := strings.Cut(s, "@")
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 16 {
		return allowRule{}, fmt.Errorf("allowed AE %q: title must be 1-16 characters", s)
	}
	r := allowRule{ae: title}
	if !hasAddr {
		return r, nil
	}
	where = strings.TrimSpace(where)
	if p, err := netip.ParsePrefix(where); err == nil {
		r.prefix = p.Masked()
		return r, nil
	}
	a, err := netip.ParseAddr(where)
	if err != nil {
		return allowRule{}, fmt.Errorf("allowed AE %q: %q is not an IP address or CIDR", s, where)
	}
	r.prefix = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
	return r, nil
}

// SetAllowedAEs replaces the calling-AE allow-list at runtime. On error the
// current list is kept.
func (g *Gateway) SetAllowedAEs(entries []string) error {
	var rules []allowRule
	for _, e := range entries {
		if strings.TrimSpace(e) == "" {
			continue
		}
		r, err := parseAllowRule(e)
		if err != nil {
			return err
		}
		rules = append(rules, r)
	}
	g.mu.Lock()
	g.allowed = rules
	g.mu.Unlock()
	return nil
}

// callingAEAllowed is the association-time check (ae.ServerConfig.AcceptCallingAE).
func (g *Gateway) callingAEAllowed(callingAE string, remote net.Addr) bool {
	g.mu.RLock()
	rules := g.allowed
	g.mu.RUnlock()
	if len(rules) == 0 {
		return true
	}
	var addr netip.Addr
	if ap, err := netip.ParseAddrPort(remote.String()); err == nil {
		addr = ap.Addr().Unmap()
	}
	for _, r := range rules {
		if r.ae != callingAE {
			continue
		}
		if !r.prefix.IsValid() || (addr.IsValid() && r.prefix.Contains(addr)) {
			return true
		}
	}
	g.m.rejected.Inc()
	g.audit(context.Background(), auditAssocRejected, "minor-failure", "calling_ae", callingAE, "remote", remote.String())
	return false
}

// String gives the rule in configuration form ("AE", "AE@IP" or "AE@CIDR").
func (r allowRule) String() string {
	if !r.prefix.IsValid() {
		return r.ae
	}
	if r.prefix.IsSingleIP() {
		return r.ae + "@" + r.prefix.Addr().String()
	}
	return r.ae + "@" + r.prefix.String()
}

// AllowedAEs returns the calling-AE allow-list that is in force now, in
// configuration form. enforced is false when the list is empty (any calling
// AE may associate). When the configured list was invalid at start the node
// refuses every association: rules is then empty and enforced is true.
func (g *Gateway) AllowedAEs() (rules []string, enforced bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, r := range g.allowed {
		if r.ae != "" {
			rules = append(rules, r.String())
		}
	}
	return rules, len(g.allowed) > 0
}
