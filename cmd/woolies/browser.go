package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const (
	homeURL   = "https://www.woolworths.com.au"
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

var t0 = time.Now()

// dbg prints a timing line to stderr when WOOLIES_DEBUG is set.
func dbg(msg string) {
	if os.Getenv("WOOLIES_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[%6.2fs] %s\n", time.Since(t0).Seconds(), msg)
	}
}

// ---- Cross-process lock: one browser per profile, so queue concurrent invocations ----

var lockHeld bool

func lockPath() string { return filepath.Join(dataDir(), "lock") }

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func acquireLock(timeout time.Duration) error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	start := time.Now()
	for {
		f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d", os.Getpid())
			f.Close()
			lockHeld = true
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		owner := 0
		if b, rerr := os.ReadFile(lockPath()); rerr == nil {
			owner, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if owner > 0 && !pidAlive(owner) {
			os.Remove(lockPath())
			continue
		}
		if time.Since(start) > timeout {
			return fmt.Errorf("another woolies command (pid %d) has held the browser profile for over %s", owner, timeout)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func releaseLock() {
	if lockHeld {
		lockHeld = false
		os.Remove(lockPath())
	}
}

// ---- Browser session ----

type session struct {
	ctx    context.Context
	cancel context.CancelFunc // cancels the tab context
	alloc  context.CancelFunc // cancels the allocator (closes the browser)
}

var sess *session

func startBrowser(headless bool) (*session, error) {
	if err := os.MkdirAll(profileDir(), 0o755); err != nil {
		return nil, err
	}
	clearSavedTabs()
	if _, err := os.Stat(browserPath()); err != nil {
		return nil, fmt.Errorf("browser not found at %s (set browser_path in the config file or WOOLIES_BROWSER_PATH)", browserPath())
	}
	// chromedp's defaults switch off background work (sync, component updates, extensions...)
	// and include the mock keychain, as Puppeteer's did, so the profile's saved login is readable.
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.ExecPath(browserPath()),
		chromedp.UserDataDir(profileDir()),
		chromedp.Flag("headless", headless),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("hide-crash-restore-bubble", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent(userAgent),
		chromedp.WindowSize(1280, 800),
		chromedp.WSURLReadTimeout(30*time.Second),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctxOpts := []chromedp.ContextOption{}
	if os.Getenv("WOOLIES_DEBUG") != "" {
		ctxOpts = append(ctxOpts, chromedp.WithDebugf(func(f string, a ...any) {
			dbg(fmt.Sprintf(f, a...))
		}))
	}
	ctx, cancel := chromedp.NewContext(allocCtx, ctxOpts...)
	s := &session{ctx: ctx, cancel: cancel, alloc: allocCancel}
	// First Run starts the browser.
	dbg("launching browser")
	if err := chromedp.Run(ctx); err != nil {
		allocCancel()
		msg := err.Error()
		if strings.Contains(msg, "websocket url timeout") {
			return nil, fmt.Errorf("could not start the browser: the profile %s is probably open in another browser window (a left-open `woolies login`?). Close it, or delete %s/SingletonLock if nothing is running", profileDir(), profileDir())
		}
		return nil, fmt.Errorf("could not start the browser: %w", err)
	}
	dbg("browser up; loading home page")
	if err := navigate(ctx, homeURL, 60*time.Second); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// close shuts the browser down gracefully so cookies are flushed to the profile.
func (s *session) close() {
	if s == nil {
		return
	}
	dbg("closing browser")
	_ = chromedp.Cancel(s.ctx)
	s.alloc()
	dbg("browser closed")
}

func closeBrowser() {
	if sess != nil {
		sess.close()
		sess = nil
	}
}

// navigate loads a URL and returns once the document is past "loading" (like DOMContentLoaded).
func navigate(ctx context.Context, url string, timeout time.Duration) error {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := chromedp.Run(tctx, chromedp.ActionFunc(func(c context.Context) error {
		_, _, errText, _, err := page.Navigate(url).Do(c)
		if err != nil {
			return err
		}
		if errText != "" {
			return fmt.Errorf("navigate %s: %s", url, errText)
		}
		return nil
	}))
	if err != nil {
		return err
	}
	for {
		var state string
		if err := chromedp.Run(tctx, chromedp.Evaluate(`document.readyState`, &state)); err == nil && state != "loading" {
			return nil
		}
		select {
		case <-tctx.Done():
			return fmt.Errorf("timed out loading %s", url)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// clearSavedTabs deletes the profile's saved tab-session files. Otherwise every launch reopens
// all the tabs earlier runs left behind, which grows with each run and made startup take 20s+.
// The login (cookies) lives elsewhere in the profile and is untouched.
func clearSavedTabs() {
	dir := filepath.Join(profileDir(), "Default", "Sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		os.Remove(filepath.Join(dir, e.Name()))
	}
}

func getSession() (*session, error) {
	if sess == nil {
		s, err := startBrowser(true)
		if err != nil {
			return nil, err
		}
		sess = s
	}
	return sess, nil
}

// request performs an API call. Tests swap it for a fake; the real one is browserRequest.
var request = browserRequest

// browserRequest performs the HTTP call from inside the page so the real session cookies,
// headers and TLS fingerprint are used. Cookies never leave the browser.
func browserRequest(url, method, body string) (any, error) {
	s, err := getSession()
	if err != nil {
		return nil, err
	}
	if method == "" {
		method = "GET"
	}
	argURL, _ := json.Marshal(url)
	argMethod, _ := json.Marshal(method)
	argBody := []byte("null")
	if body != "" {
		argBody, _ = json.Marshal(body)
	}
	js := fmt.Sprintf(`(async () => {
  const body = %s;
  const res = await fetch(%s, {
    method: %s,
    credentials: "include",
    headers: Object.assign({Accept: "application/json, text/plain, */*"}, body ? {"Content-Type": "application/json"} : {}),
    body: body,
  });
  const text = await res.text();
  return {ok: res.ok, status: res.status, statusText: res.statusText, text: text};
})()`, argBody, argURL, argMethod)

	dbg("request " + url)
	var out struct {
		OK         bool   `json:"ok"`
		Status     int    `json:"status"`
		StatusText string `json:"statusText"`
		Text       string `json:"text"`
	}
	var lastErr error
	for range 3 {
		tctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
		lastErr = chromedp.Run(tctx,
			chromedp.ActionFunc(func(c context.Context) error {
				var cur string
				if err := chromedp.Location(&cur).Do(c); err != nil {
					return err
				}
				if !strings.HasPrefix(cur, homeURL) {
					return navigate(c, homeURL, 60*time.Second)
				}
				return nil
			}),
			chromedp.Evaluate(js, &out, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
				return p.WithAwaitPromise(true)
			}),
		)
		cancel()
		if lastErr == nil {
			break
		}
		if !strings.Contains(strings.ToLower(lastErr.Error()), "context was destroyed") {
			return nil, lastErr
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if !out.OK {
		t := out.Text
		if len(t) > 300 {
			t = t[:300]
		}
		return nil, fmt.Errorf("API request failed: %d %s. %s", out.Status, out.StatusText, t)
	}
	var parsed any
	if err := json.Unmarshal([]byte(out.Text), &parsed); err != nil {
		t := out.Text
		if len(t) > 200 {
			t = t[:200]
		}
		return nil, fmt.Errorf("non-JSON response from %s: %s", url, t)
	}
	return parsed, nil
}
