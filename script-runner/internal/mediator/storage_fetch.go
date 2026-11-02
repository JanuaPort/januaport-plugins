package mediator

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

const storagePollInterval = time.Minute

// storageCourier transports opaque bytes. It has no public/deploy key and
// never writes state.json. Routing validation is not an approval decision.
type storageCourier struct {
	base, keyPath, root      string
	now                      func() time.Time
	log                      *slog.Logger
	client                   *http.Client
	mu                       sync.Mutex
	next                     time.Time
	packageNext              map[string]time.Time
	pruneMu                  sync.Mutex
	approvedPackages         map[string]bool
	packageLimit, totalLimit int64
}

func newStorageCourier(base, keyPath, storeDir string, now func() time.Time, log *slog.Logger) *storageCourier {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return &storageCourier{base: strings.TrimRight(base, "/"), keyPath: keyPath, root: filepath.Join(storeDir, "speicher"), now: now, log: log,
		client:      &http.Client{Timeout: 35 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		packageNext: map[string]time.Time{}, packageLimit: storagepins.MaxPackageBytes, totalLimit: storagepins.MaxTotalBytes}
}

func (c *storageCourier) Run(ctx context.Context) {
	defer c.client.CloseIdleConnections()
	tick := time.NewTicker(storagePollInterval)
	defer tick.Stop()
	for {
		c.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (c *storageCourier) Once(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if now.Before(c.next) {
		return
	}
	c.next = now.Add(storagePollInterval)
	for _, dir := range []string{c.root, filepath.Join(c.root, "inbox"), filepath.Join(c.root, "inbox/pkg")} {
		if storagepins.EnsureDirectory(dir) != nil {
			c.log.Warn("storage_transport_failed")
			return
		}
	}
	raw, delay, ok := c.get(ctx, "/api/plugin/script-runner/storage/pins", storagepins.MaxListBytes)
	c.next = c.now().Add(delay)
	if !ok {
		return
	}
	if storagepins.WriteFile(filepath.Join(c.root, "inbox/list.json"), raw, storagepins.MaxListBytes) != nil {
		c.log.Warn("storage_transport_failed")
		return
	}
	// Only extract safe routing tuples; preserve and publish the original body
	// even if it is unsigned or malformed. The fetcher decides validity.
	var envelope storagepins.Signed
	var list storagepins.Manifest
	if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope.Manifest, &list) != nil || list.Pins == nil {
		return
	}
	wanted := map[string]bool{}
	for _, p := range list.Pins {
		if !gitref.ValidPinName(p.Name) || len(p.Commit) != 64 || !gitref.ValidCommit(p.Commit) {
			return
		}
		wanted[p.Name+"-"+p.Commit+".pkg"] = true
	}
	pkgDir := filepath.Join(c.root, "inbox/pkg")
	if names, e := storagepins.DirectoryNames(pkgDir); e == nil {
		for _, name := range names {
			if !wanted[name] {
				_ = storagepins.RemoveFile(filepath.Join(pkgDir, name))
				delete(c.packageNext, name)
			}
		}
	}
	// Reserve all existing bytes first: a missing package sorted before an
	// existing one must not let the directory exceed the total limit.
	budget := c.totalLimit
	existing := map[string]bool{}
	for _, p := range list.Pins {
		name := p.Name + "-" + p.Commit + ".pkg"
		if existing[name] {
			continue
		}
		path := filepath.Join(pkgDir, name)
		if b, e := storagepins.ReadFile(path, c.packageLimit); e == nil {
			if int64(len(b)) > budget {
				_ = storagepins.RemoveFile(path)
				continue
			}
			budget -= int64(len(b))
			existing[name] = true
		}
	}
	for _, p := range list.Pins {
		name := p.Name + "-" + p.Commit + ".pkg"
		if existing[name] {
			continue
		}
		path := filepath.Join(pkgDir, name)
		limit := min(c.packageLimit, budget)
		if limit <= 0 {
			return
		}
		if c.now().Before(c.packageNext[name]) {
			continue
		}
		route := "/api/plugin/script-runner/storage/packages/" + p.Name + "/" + p.Commit + "?" + url.Values{"path": []string{p.Path}}.Encode()
		b, wait, ok := c.get(ctx, route, limit)
		c.packageNext[name] = c.now().Add(wait)
		if !ok {
			budget -= limit
			continue
		}
		budget -= int64(len(b))
		if storagepins.WriteFile(path, b, c.packageLimit) != nil {
			c.log.Warn("storage_transport_failed")
		} else {
			existing[name] = true
		}
	}
}

func (c *storageCourier) get(ctx context.Context, route string, limit int64) ([]byte, time.Duration, bool) {
	fail := func(wait time.Duration) ([]byte, time.Duration, bool) {
		c.log.Warn("storage_transport_failed")
		return nil, wait, false
	}
	key := readKey(c.keyPath)
	if key == "" {
		return fail(storagePollInterval)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.base+route, nil)
	if e != nil {
		return fail(storagePollInterval)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, e := c.client.Do(req)
	if e != nil {
		return fail(storagePollInterval)
	}
	defer func() { _ = resp.Body.Close() }()
	wait := storageRetryAfter(resp, c.now())
	if resp.StatusCode != http.StatusOK {
		return fail(wait)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil || int64(len(b)) > limit {
		return fail(wait)
	}
	return b, wait, true
}

func storageRetryAfter(resp *http.Response, now time.Time) time.Duration {
	wait := storagePollInterval
	if resp.StatusCode == http.StatusNotFound {
		wait = 5 * time.Minute
	}
	if seconds, e := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); e == nil && seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
		wait = max(wait, time.Duration(seconds)*time.Second)
	} else if at, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil {
		wait = max(wait, at.Sub(now))
	}
	return wait
}
