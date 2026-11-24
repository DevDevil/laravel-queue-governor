package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// daemonLogName mirrors logsink.DaemonLogName (config can't import
// logsink's writers' package just for a constant without pulling it into
// every config user; the two are pinned together by a test).
const daemonLogName = "lqg.log"

// validateLogging checks global.log_dir and every apps[].log_file
// (PLAN.md §7/§8.1): directories must exist and be writable, every app's
// resulting log path must be a usable file name, and no two logs may
// resolve to the same file.
func validateLogging(cfg *Config) (errs, warnings []string) {
	g := &cfg.Global
	owner := map[string]string{} // resolved path -> who uses it

	if g.LogDir != "" {
		if !filepath.IsAbs(g.LogDir) {
			errs = append(errs, fmt.Sprintf("global.log_dir %q must be an absolute path", g.LogDir))
		} else {
			e, w := checkWritableDir("global.log_dir", g.LogDir)
			errs, warnings = append(errs, e...), append(warnings, w...)
			owner[filepath.Join(filepath.Clean(g.LogDir), daemonLogName)] = "the daemon log"
		}
	}

	for i := range cfg.Apps {
		app := &cfg.Apps[i]
		label := fmt.Sprintf("apps[%d] (%s)", i, app.Name)
		var path string
		switch {
		case app.LogFile != "":
			if !filepath.IsAbs(app.LogFile) {
				errs = append(errs, fmt.Sprintf("%s: log_file %q must be an absolute path", label, app.LogFile))
				continue
			}
			path = filepath.Clean(app.LogFile)
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				errs = append(errs, fmt.Sprintf("%s: log_file %q is a directory", label, app.LogFile))
				continue
			}
			e, w := checkWritableDir(label+": log_file directory", filepath.Dir(path))
			errs, warnings = append(errs, e...), append(warnings, w...)
		case g.LogDir != "" && filepath.IsAbs(g.LogDir):
			if app.Name == "" {
				continue // already reported by validateApp
			}
			if !safeLogName(app.Name) {
				errs = append(errs, fmt.Sprintf("%s: name %q can't be used as a log file name under global.log_dir (letters, digits, '.', '_', '-' only) — set log_file for this app", label, app.Name))
				continue
			}
			path = filepath.Join(filepath.Clean(g.LogDir), app.Name+".log")
		default:
			continue
		}
		if other, taken := owner[path]; taken {
			errs = append(errs, fmt.Sprintf("%s: log file %s is already used by %s", label, path, other))
			continue
		}
		owner[path] = label
	}
	return errs, warnings
}

// safeLogName reports whether name works as "<name>.log" inside log_dir
// without escaping it or hiding the file.
func safeLogName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// checkWritableDir errors if dir doesn't exist or isn't a directory. Not
// being writable is an error when running as root (the daemon will run
// as root and hit the same wall), but only a warning otherwise: a
// non-root `lqg validate` can't tell whether root could write there.
func checkWritableDir(label, dir string) (errs, warnings []string) {
	info, err := os.Stat(dir)
	if err != nil {
		return []string{fmt.Sprintf("%s %q does not exist", label, dir)}, nil
	}
	if !info.IsDir() {
		return []string{fmt.Sprintf("%s %q is not a directory", label, dir)}, nil
	}
	if err := syscall.Access(dir, 2 /* W_OK */); err != nil {
		if os.Geteuid() == 0 {
			return []string{fmt.Sprintf("%s %q is not writable: %v", label, dir, err)}, nil
		}
		return nil, []string{fmt.Sprintf("%s %q is not writable by this user (the daemon runs as root — run lqg validate with sudo to check for real)", label, dir)}
	}
	return nil, nil
}
