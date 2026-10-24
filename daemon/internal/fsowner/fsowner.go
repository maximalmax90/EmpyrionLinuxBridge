package fsowner

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"elb/daemon/internal/config"
)

func FixTree(cfg config.Config, root string, skipDirNames ...string) error {
	uid, gid, err := ReferenceOwner(cfg)
	if err != nil {
		return err
	}
	skip := make(map[string]bool, len(skipDirNames)+1)
	skip["ElbMod"] = true
	for _, n := range skipDirNames {
		skip[n] = true
	}
	var errs []error
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if d.IsDir() && skip[d.Name()] {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (int(st.Uid) == uid && int(st.Gid) == gid) {
			return nil
		}
		if err := os.Chown(path, uid, gid); err != nil {
			errs = append(errs, err)
		}
		return nil
	})
	return errors.Join(errs...)
}

func ReferenceOwner(cfg config.Config) (uid, gid int, err error) {
	st, statErr := os.Stat(cfg.DataRoot)
	if statErr != nil {
		return 0, 0, statErr
	}
	sysStat, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("syscall.Stat_t unavailable on this platform")
	}
	return int(sysStat.Uid), int(sysStat.Gid), nil
}
