// Package hosted serves the browser-facing console. This service's identity can
// invoke the private gateway. Only the gateway authorizes the browser session.
package hosted

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/console"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

const cookieName = "__Host-mlc-session"
const maxBody = 256 << 10

var key = regexp.MustCompile("^[a-zA-Z0-9_-]{43}$")
var privatePath = regexp.MustCompile("^/api/v1/admin/(connections|datasets|tokens)(/[a-zA-Z0-9_-]{1,64})?$|^/api/v1/admin/preview$")

//go:embed login.html
var landing string

//go:embed login.js
var script string

//go:embed login.css
var style string

type proxy struct {
	gateway string
	client  *http.Client
	assets  http.Handler
}

func Open(ctx context.Context, upstream string) (http.Handler, error) {
	u, err := url.Parse(upstream)
	if err != nil || u.Scheme != "https" || u.Host != u.Hostname() || !strings.HasSuffix(u.Hostname(), ".run.app") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("MLC_HOSTED_GATEWAY must be a Cloud Run HTTPS origin")
	}
	source, err := idtoken.NewTokenSource(ctx, upstream)
	if err != nil {
		return nil, errors.New("could not initialize hosted service identity")
	}
	client := &http.Client{
		Transport: &oauth2.Transport{Source: source}, Timeout: 25 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &proxy{upstream, client, http.StripPrefix("/admin", console.Handler())}, nil
}

func (p *proxy) upstream(r *http.Request, method, path, authKey, secret string, body io.Reader) (*http.Response, error) {
	target, err := http.NewRequestWithContext(r.Context(), method, p.gateway+path, body)
	if err != nil {
		return nil, err
	}
	target.Header.Set(authKey, secret)
	target.Header.Set("X-MLC-Request", "console")
	if body != nil {
		target.Header.Set("Content-Type", "application/json")
	}
	return p.client.Do(target)
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && (r.Header.Get("Origin") != "https://"+r.Host || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin")) {
		http.Error(w, "Use the hosted console to send this request.", 403)
		return
	}
	cookie, err := r.Cookie(cookieName)
	signed := err == nil && key.MatchString(cookie.Value)
	switch {
	case r.Method == "GET" && r.URL.Path == "/":
		h.Set("Content-Type", "text/html; charset=utf-8")
		page := landing
		if signed {
			page = strings.Replace(page, `id="session" hidden`, `id="session"`, 1)
		}
		io.WriteString(w, page)
	case r.Method == "GET" && r.URL.Path == "/login.js":
		h.Set("Content-Type", "text/javascript")
		io.WriteString(w, script)
	case r.Method == "GET" && r.URL.Path == "/login.css":
		h.Set("Content-Type", "text/css")
		io.WriteString(w, style)
	case r.Method == "POST" && r.URL.Path == "/session/redeem":
		p.redeem(w, r)
	case !signed:
		if r.Method == "GET" && r.URL.Path == "/admin/" {
			http.Redirect(w, r, "/", 303)
			return
		}
		http.Error(w, "Sign in to the hosted console.", 401)
	case r.Method == "POST" && r.URL.Path == "/session/logout":
		resp, err := p.upstream(r, "POST", "/api/v1/admin/end-session", "X-MLC-Hosted-Session", cookie.Value, nil)
		if err != nil {
			http.Error(w, "The gateway is unavailable.", 502)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 204 && resp.StatusCode != 401 {
			http.Error(w, "Sign out failed. Try again.", 502)
			return
		}
		http.SetCookie(w, sessionCookie("", -1))
		http.Redirect(w, r, "/", 303)
	case r.Method == "GET" && (r.URL.Path == "/admin/" || r.URL.Path == "/admin/console.js" || r.URL.Path == "/admin/console.css" || r.URL.Path == "/admin/icons.svg"):
		p.assets.ServeHTTP(w, r)
	case privatePath.MatchString(r.URL.Path):
		p.api(w, r, cookie.Value)
	default:
		http.NotFound(w, r)
	}
}

func sessionCookie(value string, seconds int) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: seconds}
}

func (p *proxy) redeem(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64))
	if err != nil || !key.Match(raw) {
		http.Error(w, "Invalid login link.", 400)
		return
	}
	resp, err := p.upstream(r, "POST", "/api/v1/hosted/exchange", "X-MLC-Login-Ticket", string(raw), nil)
	if err != nil {
		http.Error(w, "The gateway is unavailable.", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 401 {
			http.Error(w, "The login link was used or expired.", 401)
		} else {
			http.Error(w, "The gateway could not complete sign-in.", 502)
		}
		return
	}
	var result struct {
		Session   string
		ExpiresAt time.Time
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&result) != nil || !key.MatchString(result.Session) {
		http.Error(w, "Invalid gateway response.", 502)
		return
	}
	remaining := int(time.Until(result.ExpiresAt).Seconds())
	if remaining < 1 {
		http.Error(w, "Session expired.", 401)
		return
	}
	http.SetCookie(w, sessionCookie(result.Session, min(3600, remaining)))
	w.WriteHeader(204)
}

func (p *proxy) api(w http.ResponseWriter, r *http.Request, session string) {
	var body io.Reader
	switch r.Method {
	case "GET":
	case "POST", "PUT", "DELETE":
		if r.Header.Get("X-MLC-Request") != "console" || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "Use the console to send this request.", 403)
			return
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			http.Error(w, "Request too large.", 413)
			return
		}
		body = strings.NewReader(string(b))
	default:
		w.WriteHeader(405)
		return
	}
	resp, err := p.upstream(r, r.Method, r.URL.Path, "X-MLC-Hosted-Session", session, body)
	if err != nil {
		http.Error(w, "The gateway is unavailable.", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		http.Error(w, "Unexpected gateway redirect.", 502)
		return
	}
	if resp.StatusCode == 401 {
		http.SetCookie(w, sessionCookie("", -1))
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
