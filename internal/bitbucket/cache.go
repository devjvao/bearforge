package bitbucket

import (
	"time"

	"bearforge/internal/domain"
)

type cacheKey struct {
	query      domain.PullRequestQuery
	credential [32]byte
}

func copyObservation(obs domain.PullRequestObservation) domain.PullRequestObservation {
	obs.PullRequests = append([]domain.PullRequest{}, obs.PullRequests...)
	return obs
}

func (c *Client) cached(key cacheKey) (domain.PullRequestObservation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	obs, ok := c.cache[key]
	if !ok {
		return domain.PullRequestObservation{}, false
	}
	if !c.now().Before(obs.ObservedAt.Add(time.Minute)) {
		delete(c.cache, key)
		return domain.PullRequestObservation{}, false
	}
	obs = copyObservation(obs)
	obs.Cached = true
	return obs, true
}

func (c *Client) remember(key cacheKey, obs domain.PullRequestObservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for key, value := range c.cache {
		if !now.Before(value.ObservedAt.Add(time.Minute)) {
			delete(c.cache, key)
		}
	}
	if _, exists := c.cache[key]; !exists && len(c.cache) >= 128 {
		var oldest cacheKey
		var oldestTime time.Time
		for key, value := range c.cache {
			if oldestTime.IsZero() || value.ObservedAt.Before(oldestTime) {
				oldest, oldestTime = key, value.ObservedAt
			}
		}
		delete(c.cache, oldest)
	}
	c.cache[key] = copyObservation(obs)
}
