package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// dialDaemon connects to the daemon's unix socket. Every CLI command goes
// through here, so a failure always comes back as an actionable message
// (explainDialError) instead of a bare "connect: permission denied".
func dialDaemon(socketPath string) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return nil, explainDialError(socketPath, err)
	}
	return conn, nil
}

// CheckDaemon dials and immediately closes the socket — a pre-flight for
// `lqg watch`'s TUI, which otherwise retries forever behind a generic
// "disconnected" banner even when the cause (no permission, daemon not
// running) won't fix itself.
func CheckDaemon(socketPath string) error {
	conn, err := dialDaemon(socketPath)
	if err != nil {
		return err
	}
	return conn.Close()
}

// explainDialError turns the two common dial failures into a message that
// says what to do. Anything else is wrapped unchanged.
func explainDialError(socketPath string, err error) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("no permission to connect to %s: %s", socketPath, permissionHint(socketPath))
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("cannot connect to %s: the lqg daemon does not seem to be running (check: systemctl status lqg)", socketPath)
	default:
		return fmt.Errorf("connecting to %s: %w", socketPath, err)
	}
}

// permissionHint explains a permission failure in terms of the socket's
// group (normally "lqg", taken from the socket directory's group so a
// custom setup gets the right name): not a member yet → how to add
// yourself; already a member in /etc/group but not in this login session
// → log in again / newgrp.
func permissionHint(socketPath string) string {
	group := "lqg"
	gid := ""
	if info, err := os.Stat(filepath.Dir(socketPath)); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			gid = strconv.FormatUint(uint64(st.Gid), 10)
			if g, err := user.LookupGroupId(gid); err == nil {
				group = g.Name
			}
		}
	}

	if gid != "" && isGroupMember(gid) && !sessionHasGroup(gid) {
		return fmt.Sprintf("you are in the %q group, but this login session started before you were added — log out and back in (or run: newgrp %s), or use sudo", group, group)
	}
	return fmt.Sprintf("your user is not in the %q group — run: sudo usermod -aG %s $USER, then log out and back in (or run: newgrp %s), or use sudo", group, group, group)
}

// isGroupMember reports whether the current user is listed in gid
// according to the group database (/etc/group) — i.e. after the next login.
func isGroupMember(gid string) bool {
	u, err := user.Current()
	if err != nil {
		return false
	}
	ids, err := u.GroupIds()
	if err != nil {
		return false
	}
	for _, id := range ids {
		if id == gid {
			return true
		}
	}
	return false
}

// sessionHasGroup reports whether this process actually holds gid right
// now (its supplementary groups are fixed at login).
func sessionHasGroup(gid string) bool {
	want, err := strconv.Atoi(gid)
	if err != nil {
		return false
	}
	if os.Getegid() == want {
		return true
	}
	groups, err := os.Getgroups()
	if err != nil {
		return false
	}
	for _, g := range groups {
		if g == want {
			return true
		}
	}
	return false
}
