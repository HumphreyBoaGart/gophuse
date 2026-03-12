package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// gophuse is a small CLI that exposes a Gopher menu tree as a read-only FUSE
// filesystem. The design favors low runtime state:
// - live Gopher content is fetched lazily on demand
// - active mount metadata is stored under ~/.local/share/gophuse/mounts
// - on-disk cache space is reserved under ~/.local/share/gophuse/cache
//
// The command surface is intentionally minimal:
// - mount: mount a menu URL as a filesystem
// - unmount: detach a mount and prune directories that gophuse created
// - list: show active gophuse mounts
// - cat: print a live menu or file without mounting
const (
	defaultPort    = "70"
	defaultTimeout = 15 * time.Second
	menuFileName   = ".menu.txt"
	dirPerm        = 0o555
	filePerm       = 0o444
	maxNameLength  = 128
)

// target is the canonical internal representation of a Gopher resource.
// ItemType is the leading selector type byte from a gopher:// URL path.
type target struct {
	Host     string
	Port     string
	ItemType byte
	Selector string
	Query    string
}

// menuItem is a single parsed line from a Gopher menu response.
type menuItem struct {
	Type     byte
	Display  string
	Selector string
	Host     string
	Port     string
}

type gopherClient struct {
	timeout time.Duration
}

// dirNode represents a lazily-loaded Gopher menu directory in the mounted tree.
// Children are created only once, on first lookup/readdir.
type dirNode struct {
	fs.Inode
	client   *gopherClient
	target   target
	loadOnce sync.Once
	loadErr  syscall.Errno
}

// fileNode represents either:
// - an actual remote Gopher item fetched lazily on first read, or
// - a synthetic static file such as .menu.txt or an informational item.
type fileNode struct {
	fs.Inode
	client   *gopherClient
	target   target
	loadOnce sync.Once
	loadErr  syscall.Errno
	data     []byte
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gophuse:", err)
		os.Exit(1)
	}
}

// run is the top-level command dispatcher.
func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "mount":
		return runMount(args[1:])
	case "unmount":
		return runUnmount(args[1:])
	case "list":
		return runList(args[1:])
	case "cat":
		return runCat(args[1:])
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New("usage: gophuse mount [--foreground] <gopher-target> [mountpoint] | gophuse unmount <mountpoint-or-target> | gophuse list | gophuse cat <gopher-target>")
}

// runMount handles both direct foreground mounts and the default background
// mount mode. The background mode passes the exact set of created directories to
// the child process so unmount cleanup knows which paths are safe to remove.
func runMount(args []string) error {
	foreground := false
	createdPaths := make([]string, 0)
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--foreground" {
			foreground = true
			continue
		}
		if arg == "--created-path" {
			if i+1 >= len(args) {
				return errors.New("usage: gophuse mount [--foreground] <gopher-url> [mountpoint]")
			}
			i++
			createdPaths = append(createdPaths, args[i])
			continue
		}
		rest = append(rest, arg)
	}
	if len(rest) < 1 || len(rest) > 2 {
		return errors.New("usage: gophuse mount [--foreground] <gopher-url> [mountpoint]")
	}

	gurl, err := parseTarget(rest[0])
	if err != nil {
		return err
	}
	if gurl.ItemType != '1' {
		return fmt.Errorf("mount requires a menu URL, got item type %q", string(gurl.ItemType))
	}

	var mountpoint string
	if len(rest) == 2 {
		mountpoint, err = filepath.Abs(rest[1])
		if err != nil {
			return err
		}
	} else {
		mountpoint, err = defaultMountpoint(gurl)
		if err != nil {
			return err
		}
	}
	if len(createdPaths) == 0 {
		createdPaths, err = ensureMountPath(mountpoint)
		if err != nil {
			return err
		}
	}

	if foreground {
		return serveMount(gurl, mountpoint, createdPaths)
	}

	return backgroundMount(gurl, mountpoint, createdPaths)
}

// defaultMountpoint chooses the implicit mount path for "gophuse mount
// gopher://host" as ~/mnt/<sanitized-host>.
func defaultMountpoint(gurl target) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := sanitizeName(gurl.Host, 0, 0)
	return filepath.Join(home, "mnt", name), nil
}

// runUnmount unmounts the FUSE filesystem, removes persisted state, and prunes
// only directories that gophuse created for this mount. State is loaded before
// unmounting because the foreground mount process also removes its own state on
// exit; loading first avoids a race where the metadata disappears too early.
func runUnmount(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: gophuse unmount <mountpoint-or-target>")
	}
	mountpoint, err := resolveMountReference(args[0])
	if err != nil {
		return err
	}

	helper, err := exec.LookPath("fusermount3")
	if err != nil {
		if helper, err = exec.LookPath("fusermount"); err != nil {
			return errors.New("fusermount3 or fusermount is required")
		}
	}
	state, err := loadMountState(mountpoint)
	if err != nil {
		return err
	}

	if err := runUnmountCommand(helper, mountpoint, false); err != nil {
		if lazyErr := runUnmountCommand(helper, mountpoint, true); lazyErr != nil {
			return err
		}
	}
	if err := removeMountState(mountpoint); err != nil {
		return err
	}
	return pruneCreatedPaths(state.CreatedPaths, mountpoint)
}

// resolveMountReference accepts either an explicit filesystem path or the same
// bare host / host:port / gopher://target shorthand accepted by mount.
func resolveMountReference(raw string) (string, error) {
	if strings.Contains(raw, "://") {
		gurl, err := parseTarget(raw)
		if err != nil {
			return "", err
		}
		return defaultMountpoint(gurl)
	}
	if looksLikePath(raw) {
		return filepath.Abs(raw)
	}
	gurl, err := parseTarget(raw)
	if err != nil {
		return filepath.Abs(raw)
	}
	return defaultMountpoint(gurl)
}

// looksLikePath preserves explicit path handling for values such as ./mnt/x,
// ../mnt/x, ~/mnt/x, or any argument containing a slash.
func looksLikePath(raw string) bool {
	return strings.Contains(raw, "/") ||
		strings.HasPrefix(raw, ".") ||
		strings.HasPrefix(raw, "~")
}

// runUnmountCommand wraps fusermount so the caller can attempt a normal detach
// first and then fall back to lazy unmount when the kernel still marks the
// mount busy.
func runUnmountCommand(helper, mountpoint string, lazy bool) error {
	args := []string{"-u"}
	if lazy {
		args = append(args, "-z")
	}
	args = append(args, mountpoint)
	cmd := exec.Command(helper, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runList lists active gophuse mounts from the kernel mount table, using the
// persisted state directory only as a supplement and cleanup source.
func runList(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: gophuse list")
	}
	mounts, err := listMounts()
	if err != nil {
		return err
	}
	for _, mount := range mounts {
		fmt.Printf("%s\t%s\n", mount.Mountpoint, mount.Source)
	}
	return nil
}

// runCat fetches a live Gopher resource without mounting it. Menu URLs are
// re-rendered as "type<TAB>display<TAB>url" lines so the output is stable and
// script-friendly.
func runCat(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: gophuse cat <gopher-url>")
	}
	gurl, err := parseTarget(args[0])
	if err != nil {
		return err
	}
	client := &gopherClient{timeout: defaultTimeout}

	if gurl.ItemType == '1' {
		menu, err := client.fetchMenu(gurl)
		if err != nil {
			return err
		}
		for _, item := range menu {
			fmt.Printf("%c\t%s\t%s\n", item.Type, item.Display, buildURL(item.target()))
		}
		return nil
	}

	data, err := client.fetchFile(gurl)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// mountEntry is the normalized runtime view of a mounted filesystem.
type mountEntry struct {
	Mountpoint string
	Source     string
	FSType     string
}

// mountState is the persisted metadata for one active mount. CreatedPaths are
// the exact directories made by gophuse for this mount and are the only paths
// eligible for automatic cleanup during unmount.
type mountState struct {
	Mountpoint   string   `json:"mountpoint"`
	Source       string   `json:"source"`
	FSType       string   `json:"fs_type"`
	PID          int      `json:"pid"`
	MountedAt    string   `json:"mounted_at"`
	CreatedPaths []string `json:"created_paths,omitempty"`
}

// backgroundMount starts a detached foreground server process and waits until
// the kernel reports the mountpoint as active. The detached child is the actual
// long-lived FUSE server.
func backgroundMount(gurl target, mountpoint string, createdPaths []string) error {
	cmdArgs := []string{"mount", "--foreground"}
	for _, path := range createdPaths {
		cmdArgs = append(cmdArgs, "--created-path", path)
	}
	cmdArgs = append(cmdArgs, buildURL(gurl), mountpoint)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}

	for i := 0; i < 100; i++ {
		time.Sleep(100 * time.Millisecond)
		if isMounted(mountpoint) {
			fmt.Println(mountpoint)
			return nil
		}
	}
	return fmt.Errorf("mount did not become ready at %s", mountpoint)
}

// serveMount performs the actual FUSE mount and blocks until the server exits.
// It persists mount metadata once the mount is live so later commands can list
// and clean it up deterministically.
func serveMount(gurl target, mountpoint string, createdPaths []string) error {
	if err := ensureDataDirs(); err != nil {
		return err
	}
	root := &dirNode{
		client: &gopherClient{timeout: defaultTimeout},
		target: gurl,
	}

	server, err := fs.Mount(mountpoint, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			Name:   "gophuse",
			FsName: buildURL(gurl),
			Debug:  false,
		},
	})
	if err != nil {
		return err
	}
	if err := server.WaitMount(); err != nil {
		return err
	}
	if err := writeMountState(mountState{
		Mountpoint:   mountpoint,
		Source:       buildURL(gurl),
		FSType:       "fuse.gophuse",
		PID:          os.Getpid(),
		MountedAt:    time.Now().UTC().Format(time.RFC3339),
		CreatedPaths: createdPaths,
	}); err != nil {
		_ = server.Unmount()
		return err
	}
	defer removeMountState(mountpoint)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		_ = server.Unmount()
	}()

	server.Wait()
	return nil
}

// isMounted checks /proc/mounts for an exact mountpoint match. It is used only
// as a readiness probe for the background mount path.
func isMounted(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == path {
			return true
		}
	}
	return false
}

// listMounts reads the live kernel mount table and merges any matching
// persisted state. Stale state files are removed opportunistically whenever the
// corresponding mountpoint is no longer active.
func listMounts() ([]mountEntry, error) {
	states, err := readMountStates()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}
	mountsByPoint := make(map[string]mountEntry, len(states)+8)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if !strings.HasPrefix(fields[2], "fuse.") {
			continue
		}
		if fields[0] == "gophuse" || strings.HasPrefix(fields[0], "gopher://") {
			entry := mountEntry{
				Mountpoint: unescapeMountField(fields[1]),
				Source:     unescapeMountField(fields[0]),
				FSType:     fields[2],
			}
			mountsByPoint[entry.Mountpoint] = entry
		}
	}
	for _, state := range states {
		if entry, ok := mountsByPoint[state.Mountpoint]; ok {
			if entry.Source == "" {
				entry.Source = state.Source
			}
			mountsByPoint[state.Mountpoint] = entry
			continue
		}
		_ = removeMountState(state.Mountpoint)
	}
	mounts := make([]mountEntry, 0, len(mountsByPoint))
	for _, mount := range mountsByPoint {
		mounts = append(mounts, mount)
	}
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].Mountpoint < mounts[j].Mountpoint })
	return mounts, nil
}

// unescapeMountField decodes the octal escaping used in /proc/mounts fields.
func unescapeMountField(value string) string {
	value = strings.ReplaceAll(value, "\\040", " ")
	value = strings.ReplaceAll(value, "\\011", "\t")
	value = strings.ReplaceAll(value, "\\012", "\n")
	value = strings.ReplaceAll(value, "\\134", "\\")
	return value
}

// dataRoot returns the single writable runtime location used by gophuse for
// mount metadata and future on-disk cache data.
func dataRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "gophuse"), nil
}

// ensureDataDirs creates the runtime directory layout if it does not already
// exist. The cache directory is created now even though caching is still light
// so future maintenance does not have to reshape the runtime tree.
func ensureDataDirs() error {
	root, err := dataRoot()
	if err != nil {
		return err
	}
	for _, dir := range []string{
		root,
		filepath.Join(root, "mounts"),
		filepath.Join(root, "cache"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// mountStatePath maps a mountpoint to a stable state filename. The hash avoids
// filesystem escaping problems while still keeping one file per mount.
func mountStatePath(mountpoint string) (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(mountpoint))
	return filepath.Join(root, "mounts", hex.EncodeToString(sum[:])+".json"), nil
}

// writeMountState persists the active mount metadata used by list/unmount.
func writeMountState(state mountState) error {
	if err := ensureDataDirs(); err != nil {
		return err
	}
	path, err := mountStatePath(state.Mountpoint)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// removeMountState removes persisted mount metadata. Missing files are treated
// as success so shutdown paths stay idempotent.
func removeMountState(mountpoint string) error {
	path, err := mountStatePath(mountpoint)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// loadMountState reads one state file. A missing file is not an error because
// the unmount path can still fall back to pruning only the leaf mountpoint.
func loadMountState(mountpoint string) (mountState, error) {
	path, err := mountStatePath(mountpoint)
	if err != nil {
		return mountState{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mountState{Mountpoint: mountpoint}, nil
		}
		return mountState{}, err
	}
	var state mountState
	if err := json.Unmarshal(data, &state); err != nil {
		return mountState{}, err
	}
	return state, nil
}

// readMountStates loads every persisted mount record under the state directory.
func readMountStates() ([]mountState, error) {
	if err := ensureDataDirs(); err != nil {
		return nil, err
	}
	root, err := dataRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "mounts"))
	if err != nil {
		return nil, err
	}
	states := make([]mountState, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "mounts", entry.Name()))
		if err != nil {
			return nil, err
		}
		var state mountState
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

// ensureMountPath creates the mountpoint and any missing parents, returning the
// exact directories created in creation order. That ordered list drives safe
// cleanup during unmount.
func ensureMountPath(path string) ([]string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absPath = filepath.Clean(absPath)
	parts := make([]string, 0, 8)
	current := absPath
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%s exists and is not a directory", current)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		parts = append(parts, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	created := make([]string, 0, len(parts))
	for i := len(parts) - 1; i >= 0; i-- {
		if err := os.Mkdir(parts[i], 0o755); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, err
		}
		created = append(created, parts[i])
	}
	return created, nil
}

// pruneCreatedPaths removes only directories that gophuse created for the
// mount. It makes multiple deepest-first passes because FUSE teardown can lag
// briefly after fusermount returns, leaving the leaf path busy for a moment.
func pruneCreatedPaths(paths []string, fallback string) error {
	if len(paths) == 0 {
		paths = []string{fallback}
	}
	for pass := 0; pass < 20; pass++ {
		progress := false
		for i := len(paths) - 1; i >= 0; i-- {
			current := filepath.Clean(paths[i])
			err := os.Remove(current)
			if err == nil {
				progress = true
				continue
			}
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EBUSY) {
				continue
			}
			return err
		}
		if !progress {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return nil
}

// parseTarget converts either a full gopher:// URL or a bare host[/...]
// reference into the internal target representation. Empty paths are treated as
// top-level menu requests, and a missing port defaults to TCP 70.
func parseTarget(raw string) (target, error) {
	u, err := url.Parse(normalizeRawTarget(raw))
	if err != nil {
		return target{}, err
	}
	if u.Scheme != "gopher" {
		return target{}, errors.New("target must be a gopher URL, hostname, or IP address")
	}
	if u.Hostname() == "" {
		return target{}, errors.New("URL must include a host")
	}
	host, err := sanitizeHost(u.Hostname())
	if err != nil {
		return target{}, err
	}

	port := u.Port()
	if port == "" {
		port = defaultPort
	}
	if _, err := sanitizePort(port); err != nil {
		return target{}, err
	}

	path := strings.TrimPrefix(u.EscapedPath(), "/")
	if path == "" {
		return target{Host: host, Port: port, ItemType: '1'}, nil
	}

	decoded, err := url.PathUnescape(path)
	if err != nil {
		return target{}, err
	}
	if decoded == "" {
		return target{Host: host, Port: port, ItemType: '1'}, nil
	}

	return target{
		Host:     host,
		Port:     port,
		ItemType: decoded[0],
		Selector: sanitizeRequestField(decoded[1:]),
		Query:    sanitizeRequestField(u.RawQuery),
	}, nil
}

// normalizeRawTarget makes bare hostnames, IPv4/IPv6 addresses, and host:port
// forms acceptable to the main URL parser by supplying the gopher:// scheme
// when the caller omitted it.
func normalizeRawTarget(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	return "gopher://" + raw
}

// buildURL reconstructs a canonical gopher:// URL from a target.
func buildURL(t target) string {
	base := fmt.Sprintf("gopher://%s", t.Host)
	if t.Port != "" && t.Port != defaultPort {
		base += ":" + t.Port
	}
	path := "/" + string(t.ItemType)
	if t.Selector != "" {
		path += escapeSelector(t.Selector)
	}
	if t.Query != "" {
		return base + path + "?" + t.Query
	}
	return base + path
}

// escapeSelector preserves path segments while URL-escaping selector bytes.
func escapeSelector(selector string) string {
	parts := strings.Split(selector, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// address returns the TCP dial target for a Gopher resource.
func (t target) address() string {
	return net.JoinHostPort(t.Host, t.Port)
}

// requestSelector renders the selector line sent to the Gopher server.
func (t target) requestSelector() string {
	if t.Query == "" {
		return sanitizeRequestField(t.Selector)
	}
	return sanitizeRequestField(t.Selector) + "\t" + sanitizeRequestField(t.Query)
}

// target reconstructs a normalized target from a parsed menu item.
func (m menuItem) target() target {
	port := m.Port
	if port == "" {
		port = defaultPort
	}
	if sanitized, err := sanitizePort(port); err == nil {
		port = sanitized
	} else {
		port = defaultPort
	}
	host := "localhost"
	if sanitized, err := sanitizeHost(m.Host); err == nil {
		host = sanitized
	}
	return target{
		Host:     host,
		Port:     port,
		ItemType: m.Type,
		Selector: sanitizeRequestField(m.Selector),
	}
}

// fetchMenu retrieves and parses a menu response.
func (c *gopherClient) fetchMenu(t target) ([]menuItem, error) {
	data, err := c.fetchSelector(t)
	if err != nil {
		return nil, err
	}
	return parseMenu(data)
}

// fetchFile retrieves a non-menu item verbatim.
func (c *gopherClient) fetchFile(t target) ([]byte, error) {
	return c.fetchSelector(t)
}

// fetchSelector performs the raw Gopher TCP request/response exchange.
func (c *gopherClient) fetchSelector(t target) ([]byte, error) {
	conn, err := net.DialTimeout("tcp", t.address(), c.timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(c.timeout))
	if _, err := io.WriteString(conn, t.requestSelector()+"\r\n"); err != nil {
		return nil, err
	}
	return io.ReadAll(conn)
}

// parseMenu parses a Gopher menu body into structured entries. Invalid or
// truncated lines are tolerated as far as possible because real-world Gopher
// menus are often inconsistent.
func parseMenu(data []byte) ([]menuItem, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	menu := make([]menuItem, 0, 64)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "." {
			break
		}
		if line == "" {
			continue
		}

		itemType := line[0]
		fields := strings.Split(line[1:], "\t")
		item := menuItem{
			Type: itemType,
			Port: defaultPort,
		}
		if len(fields) > 0 {
			item.Display = sanitizeMenuText(fields[0])
		}
		if len(fields) > 1 {
			item.Selector = sanitizeRequestField(fields[1])
		}
		if len(fields) > 2 {
			item.Host = sanitizeMenuText(fields[2])
		}
		if len(fields) > 3 && fields[3] != "" {
			if sanitized, err := sanitizePort(fields[3]); err == nil {
				item.Port = sanitized
			}
		}
		if item.Display == "" {
			item.Display = fmt.Sprintf("item-%d", len(menu)+1)
		}
		if item.Host == "" {
			item.Host = "localhost"
		}
		menu = append(menu, item)
	}
	return menu, scanner.Err()
}

// Getattr reports a fixed read-only directory mode for every dirNode.
func (d *dirNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | dirPerm
	out.Nlink = 2
	return 0
}

// Lookup forces lazy population before resolving a child by name.
func (d *dirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if errno := d.ensureLoaded(ctx); errno != 0 {
		return nil, errno
	}
	child := d.GetChild(name)
	if child == nil {
		return nil, syscall.ENOENT
	}
	return child, 0
}

// Readdir forces lazy population and returns directory entries in stable name
// order so CLI and file manager views are deterministic.
func (d *dirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	if errno := d.ensureLoaded(ctx); errno != 0 {
		return nil, errno
	}

	children := d.Children()
	list := make([]fuse.DirEntry, 0, len(children))
	for name, child := range children {
		list = append(list, fuse.DirEntry{
			Name: name,
			Mode: child.StableAttr().Mode,
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return fs.NewListDirStream(list), 0
}

// ensureLoaded populates a menu directory on first access by fetching the live
// menu, synthesizing .menu.txt, and creating child nodes for every item.
func (d *dirNode) ensureLoaded(ctx context.Context) syscall.Errno {
	d.loadOnce.Do(func() {
		menu, err := d.client.fetchMenu(d.target)
		if err != nil {
			d.loadErr = syscall.EIO
			return
		}

		menuDump := bytes.NewBuffer(nil)
		seen := make(map[string]int, len(menu))
		for i, item := range menu {
			fmt.Fprintf(menuDump, "%c\t%s\t%s\n", item.Type, item.Display, buildURL(item.target()))

			name := sanitizeName(item.Display, item.Type, i)
			if count := seen[name]; count > 0 {
				name = fmt.Sprintf("%s.%d", name, count+1)
			}
			seen[name]++

			child, mode := d.makeChild(item)
			d.AddChild(name, d.NewPersistentInode(ctx, child, fs.StableAttr{Mode: mode}), true)
		}

		menuNode := &fileNode{data: menuDump.Bytes()}
		d.AddChild(menuFileName, d.NewPersistentInode(ctx, menuNode, fs.StableAttr{Mode: syscall.S_IFREG | filePerm}), true)
	})
	return d.loadErr
}

// makeChild maps a Gopher item type onto either a child directory or a
// read-only file node. Unsupported interactive item types are exposed as files
// so the tree remains browsable even when semantics are reduced.
func (d *dirNode) makeChild(item menuItem) (fs.InodeEmbedder, uint32) {
	switch item.Type {
	case '1':
		return &dirNode{
			client: d.client,
			target: item.target(),
		}, syscall.S_IFDIR | dirPerm
	case 'i':
		return &fileNode{data: []byte(item.Display + "\n")}, syscall.S_IFREG | filePerm
	default:
		return &fileNode{
			client: d.client,
			target: item.target(),
		}, syscall.S_IFREG | filePerm
	}
}

// Getattr ensures the file content is loaded before reporting size.
func (f *fileNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if errno := f.ensureLoaded(); errno != 0 {
		return errno
	}
	out.Mode = syscall.S_IFREG | filePerm
	out.Size = uint64(len(f.data))
	out.Nlink = 1
	return 0
}

// Open enforces a read-only filesystem contract and allows the kernel to cache
// file data after a successful load.
func (f *fileNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if flags&syscall.O_ACCMODE != syscall.O_RDONLY {
		return nil, 0, syscall.EPERM
	}
	if errno := f.ensureLoaded(); errno != 0 {
		return nil, 0, errno
	}
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

// Read serves already-loaded content directly from memory.
func (f *fileNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if errno := f.ensureLoaded(); errno != 0 {
		return nil, errno
	}
	if off >= int64(len(f.data)) {
		return fuse.ReadResultData(nil), 0
	}
	end := off + int64(len(dest))
	if end > int64(len(f.data)) {
		end = int64(len(f.data))
	}
	return fuse.ReadResultData(f.data[off:end]), 0
}

// ensureLoaded fetches remote file content only once. Synthetic files have no
// client and therefore count as already loaded.
func (f *fileNode) ensureLoaded() syscall.Errno {
	if f.client == nil {
		return 0
	}
	f.loadOnce.Do(func() {
		data, err := f.client.fetchFile(f.target)
		if err != nil {
			f.loadErr = syscall.EIO
			return
		}
		f.data = data
	})
	return f.loadErr
}

// sanitizeName turns menu display text into a deterministic filesystem-safe
// name. Directory items receive a .dir suffix so they are easier to identify in
// file managers and shells.
func sanitizeName(display string, itemType byte, index int) string {
	name := sanitizeMenuText(display)
	if name == "" {
		name = fmt.Sprintf("item-%03d", index+1)
	}

	var b strings.Builder
	lastSep := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastSep = false
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
			lastSep = false
		default:
			if !lastSep {
				b.WriteByte('_')
				lastSep = true
			}
		}
	}

	name = strings.Trim(b.String(), "._")
	if len(name) > maxNameLength {
		name = strings.Trim(name[:maxNameLength], "._")
	}
	if name == "" {
		name = fmt.Sprintf("item-%03d", index+1)
	}
	if name == menuFileName || name == "." || name == ".." {
		name = fmt.Sprintf("item-%03d", index+1)
	}
	if itemType == '1' {
		name += ".dir"
	}
	return name
}

// sanitizeMenuText removes control characters and collapses whitespace so remote
// menu text cannot smuggle terminal controls or misleading invisible names into
// the local filesystem view.
func sanitizeMenuText(value string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range value {
		switch {
		case r == '\r' || r == '\n' || r == '\t' || r == 0:
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		case unicode.IsControl(r):
			continue
		case unicode.IsSpace(r):
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// sanitizeRequestField strips line-breaking and NUL characters so remote menu
// data cannot inject extra request lines when reused as a selector or query.
func sanitizeRequestField(value string) string {
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', 0:
			return -1
		default:
			if unicode.IsControl(r) && r != '\t' {
				return -1
			}
			return r
		}
	}, value)
	return strings.TrimSpace(value)
}

// sanitizeHost validates the host component before it is used in URLs or TCP
// dial targets.
func sanitizeHost(host string) (string, error) {
	host = sanitizeMenuText(host)
	if host == "" {
		return "", errors.New("host must not be empty")
	}
	if strings.ContainsAny(host, "/\\?#@") {
		return "", fmt.Errorf("invalid host %q", host)
	}
	return host, nil
}

// sanitizePort accepts only numeric TCP ports in range.
func sanitizePort(port string) (string, error) {
	port = sanitizeMenuText(port)
	if port == "" {
		return "", errors.New("port must not be empty")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return "", fmt.Errorf("invalid port %q", port)
	}
	return strconv.Itoa(value), nil
}
