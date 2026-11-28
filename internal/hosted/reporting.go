package hosted

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var reportingDatabasePath = regexp.MustCompile(`^/api/v1/connections/([A-Za-z0-9_-]{1,64}|@datasets)/databases$`)
var reportingBearer = regexp.MustCompile(`^Bearer (mlc_[0-9a-f]{32}\.[A-Za-z0-9_-]{43})$`)

// Looker clients authenticate separately from browser sessions. Forward only a
// reporting credential and one of the gateway's five read-only operations.
func (p *proxy) reporting(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	method := http.MethodGet
	switch {
	case path == "/api/v1/connections", reportingDatabasePath.MatchString(path), path == "/api/v1/collections":
	case path == "/api/v1/schema", path == "/api/v1/query":
		method = http.MethodPost
	default:
		return false
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		w.WriteHeader(405)
		return true
	}
	auth := r.Header.Get("Authorization")
	if key := r.Header.Get("X-MLC-Token"); key != "" {
		if auth != "" {
			http.Error(w, "Supply one reporting token.", 400)
			return true
		}
		auth = "Bearer " + key
	}
	token := reportingBearer.FindStringSubmatch(auth)
	if len(token) == 0 {
		http.Error(w, "A scoped reporting token is required.", 401)
		return true
	}
	if path == "/api/v1/collections" {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(q) != 2 || len(q["connectionId"]) != 1 || len(q["database"]) != 1 || len(r.URL.RawQuery) > 2048 {
			http.Error(w, "Invalid collection selection.", 400)
			return true
		}
		path += "?" + q.Encode()
	} else if r.URL.RawQuery != "" {
		http.Error(w, "Unexpected query parameters.", 400)
		return true
	}
	var body io.Reader
	if method == http.MethodPost {
		media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if media != "application/json" {
			http.Error(w, "Use application/json.", 415)
			return true
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			http.Error(w, "Request too large.", 413)
			return true
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, p.gateway+path, body)
	if err != nil {
		http.Error(w, "Invalid reporting request.", 400)
		return true
	}
	req.Header.Set("X-MLC-Token", token[1])
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, "The reporting gateway is unavailable.", 502)
		return true
	}
	defer resp.Body.Close()
	// Refuse successful responses from older or reporting-only installations
	// which might have relied solely on private network access.
	if (resp.StatusCode < 300 && resp.Header.Get("X-MLC-Reporting-Authorized") != "true") || (resp.StatusCode >= 300 && resp.StatusCode < 400) || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "Unexpected reporting gateway response.", 502)
		return true
	}
	if cache := resp.Header.Get("X-MLC-Cache"); cache == "HIT" || cache == "MISS" {
		w.Header().Set("X-MLC-Cache", cache)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true
}
