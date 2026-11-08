package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// capture runs fn with stdout and stderr redirected, and returns what it wrote.
func capture(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	read := func(f **os.File) (func() string, func()) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		old := *f
		*f = w
		done := make(chan string)
		go func() { b, _ := io.ReadAll(r); done <- string(b) }()
		return func() string { return <-done }, func() { w.Close(); *f = old }
	}
	outText, outRestore := read(&os.Stdout)
	errText, errRestore := read(&os.Stderr)
	fn()
	outRestore()
	errRestore()
	return outText(), errText()
}

// cli runs the woolies command line in-process, with a safe temp home and a browser path
// that does not exist (so nothing can ever launch a real browser).
func cli(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"woolies"}, args...)
	defer func() { os.Args = old }()
	stdout, stderr = capture(t, func() { code = run() })
	return code, stdout, stderr
}

func safeHome(t *testing.T, configJSON string) string {
	t.Helper()
	home := t.TempDir()
	resetConfig(t, home, configJSON)
	t.Setenv("WOOLIES_BROWSER_PATH", filepath.Join(home, "no-such-browser"))
	return home
}

func jsonOut(t *testing.T, s string) obj {
	t.Helper()
	var v obj
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, s)
	}
	return v
}

func TestRunHelpAndUsage(t *testing.T) {
	safeHome(t, "")
	code, out, _ := cli(t)
	if code != 1 || !strings.Contains(out, "woolies - Woolies CLI") {
		t.Errorf("no args: code %d, out %q", code, out)
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"cart", "--help"}} {
		if code, out, _ := cli(t, args...); code != 0 || !strings.Contains(out, "woolies config") {
			t.Errorf("%v: code %d", args, code)
		}
	}
	code, _, errOut := cli(t, "fly")
	if code != 2 || jsonOut(t, errOut)["success"] != false || !strings.Contains(errOut, "unknown command") {
		t.Errorf("unknown command: code %d, stderr %s", code, errOut)
	}
	code, _, errOut = cli(t, "search", "milk", "--page")
	if code != 2 || !strings.Contains(errOut, "--page needs a value") {
		t.Errorf("missing flag value: code %d, stderr %s", code, errOut)
	}
}

func TestParseArgs(t *testing.T) {
	pos, o, err := parseArgs([]string{"a", "--special", "--page-size", "4", "--sort=Name", "b", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pos, ",") != "a,b" || o["special"] != "true" || o["page-size"] != "4" || o["sort"] != "Name" || o["dry-run"] != "true" {
		t.Errorf("pos %v, opts %v", pos, o)
	}
	if _, _, err := parseArgs([]string{"--limit"}); err == nil {
		t.Error("want an error for a flag with no value")
	}
}

func TestRunFileOnlyCommands(t *testing.T) {
	home := safeHome(t, `{"postcode": "2000"}`)
	code, out, _ := cli(t, "config")
	res := jsonOut(t, out)
	if code != 0 || res["configExists"] != true || dig(res, "settings", "postcode", "value") != "2000" ||
		dig(res, "settings", "postcode", "source") != "config" || dig(res, "settings", "browser_path", "source") != "env" {
		t.Errorf("config: code %d, %v", code, res)
	}
	if code, _, _ := cli(t, "config", "extra"); code != 2 {
		t.Errorf("config with an argument: code %d", code)
	}

	dir := filepath.Join(home, "recipes")
	os.Mkdir(dir, 0o755)
	if code, out, _ := cli(t, "recipe", "list", "--dir", dir); code != 0 || jsonOut(t, out)["count"] != 0.0 {
		t.Errorf("recipe list: code %d, %s", code, out)
	}
	if code, _, _ := cli(t, "recipe", "show", "nothing", "--dir", dir); code == 0 {
		t.Error("recipe show of a missing recipe should fail")
	}

	file := filepath.Join(home, "cupboard.csv")
	if code, _, _ := cli(t, "cupboard", "add", "Rice", "--file", file); code != 0 {
		t.Errorf("cupboard add: code %d", code)
	}
	if code, out, _ := cli(t, "cupboard", "list", "--file", file); code != 0 || !strings.Contains(out, "Rice") {
		t.Errorf("cupboard list: code %d, %s", code, out)
	}
	if code, _, _ := cli(t, "cupboard", "use", "--file", file); code != 2 {
		t.Errorf("cupboard use with no item: code %d, want 2", code)
	}
	if code, _, _ := cli(t, "cupboard", "use", "Pasta", "--file", file); code != 1 {
		t.Errorf("cupboard use of a missing item: code %d, want 1", code)
	}
}

func TestRunBadConfig(t *testing.T) {
	safeHome(t, "")
	p := filepath.Join(t.TempDir(), "custom.json")
	os.WriteFile(p, []byte(`{"postcod": "1"}`), 0o600)
	t.Setenv("WOOLIES_CONFIG", p)
	code, _, errOut := cli(t, "config")
	if code != 1 || !strings.Contains(errOut, "custom.json") {
		t.Errorf("bad config: code %d, stderr %s", code, errOut)
	}
	if got := configPath(); got != p {
		t.Errorf("configPath = %q, want %q", got, p)
	}
}

func TestRunBrowserCommands(t *testing.T) {
	home := safeHome(t, "")
	f := useFakeSite(t, map[string]any{"GET /apis/ui/Trolley": trolleyJSON})
	code, out, _ := cli(t, "cart")
	if code != 0 || len(dig(jsonOut(t, out), "cart", "items").([]any)) != 2 {
		t.Errorf("cart: code %d, %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, "lock")); !os.IsNotExist(err) {
		t.Error("the lock file was left behind")
	}
	if code, _, _ := cli(t, "add", "abc"); code != 2 {
		t.Errorf("add abc: code %d, want 2", code)
	}
	f.routes["POST /apis/ui/Search/products"] = `{"Products": []}`
	if code, out, _ := cli(t, "quick-add", "nothing"); code != 1 || jsonOut(t, out)["success"] != false {
		t.Errorf("quick-add with no result: code %d, %s", code, out)
	}
	request = browserRequest // the real one, which can't start the (missing) browser
	code, _, errOut := cli(t, "cart")
	if code != 1 || !strings.Contains(errOut, "browser not found") {
		t.Errorf("missing browser: code %d, stderr %s", code, errOut)
	}
	code, _, errOut = cli(t, "login")
	if code != 1 || !strings.Contains(errOut, "browser not found") {
		t.Errorf("login with a missing browser: code %d, stderr %s", code, errOut)
	}

	// The lock can't be taken when the data folder can't be created.
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, nil, 0o600)
	t.Setenv("WOOLIES_HOME", filepath.Join(blocker, "sub"))
	if code, _, _ := cli(t, "cart"); code != 1 {
		t.Errorf("unwritable home: code %d, want 1", code)
	}
}

func TestLock(t *testing.T) {
	home := safeHome(t, "")
	if err := acquireLock(time.Second); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(lockPath())
	if string(b) != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock holds %q", b)
	}
	// Held by a live process (this one): times out.
	lockHeld = false
	if err := acquireLock(0); err == nil || !strings.Contains(err.Error(), "another woolies command") {
		t.Errorf("want a timeout, got %v", err)
	}
	lockHeld = true
	releaseLock()
	releaseLock() // a second release is a no-op
	if _, err := os.Stat(filepath.Join(home, "lock")); !os.IsNotExist(err) {
		t.Error("release left the lock file")
	}

	// A lock left by a process that has exited is taken over.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip("cannot start a helper process:", err)
	}
	os.WriteFile(lockPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	if pidAlive(cmd.Process.Pid) {
		t.Skip("helper pid was reused")
	}
	if err := acquireLock(time.Second); err != nil {
		t.Errorf("stale lock: %v", err)
	}
	releaseLock()
}

func TestSmallHelpers(t *testing.T) {
	home := safeHome(t, "")
	t.Setenv("WOOLIES_DEBUG", "1")
	_, errOut := capture(t, func() { dbg("hello") })
	if !strings.Contains(errOut, "hello") {
		t.Errorf("dbg wrote %q", errOut)
	}

	sessions := filepath.Join(home, "profile", "Default", "Sessions")
	os.MkdirAll(sessions, 0o755)
	os.WriteFile(filepath.Join(sessions, "Tabs_1"), nil, 0o600)
	clearSavedTabs()
	if entries, _ := os.ReadDir(sessions); len(entries) != 0 {
		t.Errorf("saved tabs left: %v", entries)
	}
	closeBrowser() // no session: a no-op
	var s *session
	s.close() // nil session: a no-op

	for _, c := range []struct {
		v    any
		want bool
	}{{true, true}, {nil, false}, {0.0, false}, {2.0, true}, {"", false}, {"x", true}, {[]any{}, true}} {
		if truthy(c.v) != c.want {
			t.Errorf("truthy(%v) = %v", c.v, !c.want)
		}
	}
	if str(true) != "true" || str(12.0) != "12" || str(nil) != "" {
		t.Error("str conversions")
	}
	if hasKeyDeep(obj{"a": obj{"b": 1.0}}, "b", 0) || !hasKeyDeep([]any{obj{"B": 1.0}}, "b", 2) || hasKeyDeep("x", "b", 3) {
		t.Error("hasKeyDeep")
	}
	shape := shapeOf(obj{"s": "x", "n": 1.0, "z": nil, "deep": obj{"a": 1.0}, "e": []any{}}, 1)
	want := obj{"s": "string", "n": "number", "z": "null", "deep": "{…}", "e": []any{}}
	if marshalStr(shape) != marshalStr(want) {
		t.Errorf("shapeOf = %v", shape)
	}
}
