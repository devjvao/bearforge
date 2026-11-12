package bitbucket

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"bearforge/internal/domain"
)

var (
	errUnsafeRedirect  = errors.New("unsafe Bitbucket redirect")
	errLimitedRedirect = errors.New("rate-limited Bitbucket redirect")
)

func apiHost(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func safeEndpoint(origin, target *url.URL, path string) bool {
	return target.Scheme == "https" && target.User == nil && target.Fragment == "" && apiHost(origin) == apiHost(target) && target.Path == path && safePath(target) && !strings.ContainsFunc(target.String(), unicode.IsControl)
}

func (c *Client) get(ctx context.Context, origin, endpoint *url.URL, path, token string, host *hostState) ([]byte, *url.URL, error) {
	if !safeEndpoint(origin, endpoint, path) {
		return nil, nil, domain.E("provider_failed", "Bitbucket returned unsafe pagination")
	}
	client := *c.httpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.Response != nil {
			c.observeRateLimit(req.Response, host)
		}
		if len(via) >= 10 || req.Method != http.MethodGet || !safeEndpoint(origin, req.URL, path) {
			return errUnsafeRedirect
		}
		if c.now().Before(host.cooldown) {
			return errLimitedRedirect
		}
		return nil
	}
	for attempt := range 3 {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if c.now().Before(host.cooldown) {
			return nil, nil, coolingDown()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, nil, domain.E("invalid", "invalid Bitbucket request coordinates")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "bearforge")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if errors.Is(err, errLimitedRedirect) {
				return nil, nil, coolingDown()
			}
			if errors.Is(err, errUnsafeRedirect) {
				return nil, nil, domain.E("provider_failed", "Bitbucket redirected away from the configured repository or API origin; update remotes or provider metadata and retry")
			}
			if attempt < 2 && transientNetwork(err) {
				if err := retryWait(ctx, attempt); err != nil {
					return nil, nil, err
				}
				continue
			}
			return nil, nil, domain.E("offline", "cannot reach Bitbucket API; check network connectivity")
		}
		status := response.StatusCode
		limited := c.observeRateLimit(response, host)
		if status != http.StatusOK {
			_ = response.Body.Close()
			if limited {
				return nil, nil, coolingDown()
			}
			if attempt < 2 && (status == 502 || status == 503 || status == 504) {
				if err := retryWait(ctx, attempt); err != nil {
					return nil, nil, err
				}
				continue
			}
			return nil, nil, statusError(status)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
		_ = response.Body.Close()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if err != nil {
			if !limited && attempt < 2 && transientNetwork(err) {
				if err := retryWait(ctx, attempt); err != nil {
					return nil, nil, err
				}
				continue
			}
			return nil, nil, domain.E("offline", "Bitbucket response was interrupted; check network connectivity")
		}
		if len(body) > 8*1024*1024 {
			return nil, nil, domain.E("provider_failed", "Bitbucket response exceeded 8 MiB")
		}
		return body, response.Request.URL, nil
	}
	return nil, nil, domain.E("provider_failed", "Bitbucket API request failed")
}

func (c *Client) observeRateLimit(response *http.Response, host *hostState) bool {
	limited := response.StatusCode == 429 || response.Header.Get("Retry-After") != "" || response.Header.Get("X-RateLimit-Remaining") == "0"
	if limited {
		host.cooldown = cooldownUntil(response.Header, c.now())
	}
	return limited
}

func coolingDown() error {
	return domain.E("rate_limited", "Bitbucket API is cooling down; retry later")
}

func cooldownUntil(header http.Header, now time.Time) time.Time {
	var until time.Time
	if raw := header.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 32); err == nil && seconds >= 0 {
			until = now.Add(time.Duration(seconds) * time.Second)
		} else if date, err := http.ParseTime(raw); err == nil {
			until = date
		}
	}
	if header.Get("X-RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			reset := time.Unix(seconds, 0)
			if reset.After(until) {
				until = reset
			}
		}
	}
	if !until.After(now) {
		until = now.Add(time.Minute)
	}
	return until
}

func statusError(status int) error {
	switch status {
	case 401:
		return domain.E("unauthenticated", "Bitbucket authentication failed; check token environment variables")
	case 403:
		return domain.E("unauthorized", "Bitbucket access denied; check repository and token permissions")
	case 400, 422:
		return domain.E("invalid", "Bitbucket rejected the pull-request query")
	default:
		return domain.E("provider_failed", "Bitbucket API returned HTTP %d; check repository access or retry later", status)
	}
}

func transientNetwork(err error) bool {
	var network net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		(errors.As(err, &network) && network.Timeout())
}

func retryWait(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
