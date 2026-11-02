package fetcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

const storageInterval = 5 * time.Second

func (f *Fetcher) runStorage(ctx context.Context) {
	tick := time.NewTicker(storageInterval)
	defer tick.Stop()
	for {
		f.StorageOnce()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// StorageOnce is independent of Once: no Git call, deploy-key copy or network
// client can be reached from the hostile inbox. The key is reloaded each time.
func (f *Fetcher) StorageOnce() { f.storageOnce(storagepins.MaxTotalBytes) }

func (f *Fetcher) storageOnce(budget int64) {
	f.storageMu.Lock()
	defer f.storageMu.Unlock()
	root := filepath.Join(f.cfg.StoreDir, "speicher")
	if storagepins.EnsureDirectory(root) != nil {
		f.cfg.Logger.Warn("storage_io_failed")
		return
	}
	public, e := storagepins.LoadKey(filepath.Join(filepath.Dir(f.cfg.KeyPath), "runner-pins.pub"))
	if e != nil {
		public = nil
	}
	if f.storage.SetKey(public) != nil {
		f.cfg.Logger.Warn("storage_io_failed")
		return
	}
	if len(public) == 0 {
		return
	}
	raw, e := storagepins.ReadFile(filepath.Join(root, "inbox", "list.json"), storagepins.MaxListBytes)
	if e != nil {
		f.storageFailed()
		return
	}
	signed, list, e := f.storage.Check(raw)
	if e != nil {
		f.storageFailed()
		return
	}
	approved := []storagepins.Approval{}
	failed := []storagepins.Failure{}
	for i, p := range list.Pins {
		if budget <= 0 {
			failed = append(failed, storagepins.Failure{Index: i, State: "invalid", Reason: "fetch_failed"})
			continue
		}
		limit := min(storagepins.MaxPackageBytes, budget)
		b, e := storagepins.ReadFile(filepath.Join(root, "inbox", "pkg", p.Name+"-"+p.Commit+".pkg"), limit)
		if e != nil {
			// A rejected read may have consumed the whole allowance. Charge it
			// conservatively so many oversized files cannot bypass the total cap.
			budget -= limit
			failed = append(failed, storagepins.Failure{Index: i, State: "invalid", Reason: "fetch_failed"})
			continue
		}
		budget -= int64(len(b))
		if gitobjects.Verify(b, p.Commit, p.Path) != nil {
			failed = append(failed, storagepins.Failure{Index: i, State: "invalid", Reason: "hash_mismatch"})
			continue
		}
		h := sha256.Sum256(b)
		approved = append(approved, storagepins.Approval{Name: p.Name, Commit: p.Commit, Path: p.Path, PackageSHA256: hex.EncodeToString(h[:])})
	}
	// Check freshness again after all IO. Publish writes only canonical verified
	// metadata and locally computed hashes, never raw bodies or error messages.
	if f.storage.Publish(signed, list, approved, failed) != nil {
		f.storageFailed()
	}
}

func (f *Fetcher) storageFailed() {
	f.cfg.Logger.Warn("storage_fetch_failed")
	if f.storage.Fail() != nil {
		f.cfg.Logger.Warn("storage_io_failed")
	}
}
