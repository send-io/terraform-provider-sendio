package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI is an in-memory send.io API with the response shapes of the real
// backend (resources.go, marketing.go, suppressions.go, agents.go), enough
// for the provider's CRUD paths. Unit tests run real Terraform against it.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	seq int
	// collections: name -> id -> object
	db map[string]map[string]map[string]any
	// requests records "METHOD /path body" for assertions.
	requests []string
	// verifyAfter: POST /domains/{id}/verify marks a domain verified on
	// this call number (0 = never).
	verifyAfter int
	verifyCalls int
	timeoutAt   string // "verify" or "read": keep that verification request in flight until cancellation
}

const fakeKey = "sio_test_key"

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, db: map[string]map[string]map[string]any{}, verifyAfter: 1}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) providerConfig() string {
	return fmt.Sprintf("provider \"sendio\" {\n  api_key  = %q\n  base_url = %q\n}\n", fakeKey, f.srv.URL)
}

func (f *fakeAPI) newID() string {
	f.seq++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", f.seq)
}

func (f *fakeAPI) coll(name string) map[string]map[string]any {
	if f.db[name] == nil {
		f.db[name] = map[string]map[string]any{}
	}
	return f.db[name]
}

// get returns a copy of an object (for test assertions).
func (f *fakeAPI) get(coll, id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.coll(coll)[id]
	if o == nil {
		return nil
	}
	cp := map[string]any{}
	for k, v := range o {
		cp[k] = v
	}
	return cp
}

// mutate changes an object out of band (drift).
func (f *fakeAPI) mutate(coll, id string, fn func(map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o := f.coll(coll)[id]; o != nil {
		fn(o)
	}
}

func (f *fakeAPI) remove(coll, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.coll(coll), id)
}

// only returns the single id in a collection.
func (f *fakeAPI) only(coll string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.coll(coll) {
		return id
	}
	return ""
}

func (f *fakeAPI) count(coll string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.coll(coll))
}

func (f *fakeAPI) requestsMatching(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			out = append(out, r)
		}
	}
	return out
}

type apiError struct {
	status int
	name   string
	msg    string
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	raw := []byte{}
	if r.Body != nil && json.NewDecoder(r.Body).Decode(&body) == nil {
		raw, _ = json.Marshal(body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(raw)))
	hang := (f.timeoutAt == "verify" && r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/verify")) ||
		(f.timeoutAt == "read" && f.verifyCalls > 0 && r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/domains/"))
	f.mu.Unlock()

	if r.Header.Get("Authorization") != "Bearer "+fakeKey {
		writeJSON(w, 401, map[string]any{"statusCode": 401, "name": "missing_api_key", "message": "Missing API key in the authorization header."})
		return
	}
	if hang {
		<-r.Context().Done()
		return
	}
	defer func() {
		if v := recover(); v != nil {
			if e, ok := v.(apiError); ok {
				writeJSON(w, e.status, map[string]any{"statusCode": e.status, "name": e.name, "message": e.msg})
				return
			}
			panic(v)
		}
	}()
	out, status := f.locked(r, body)
	writeJSON(w, status, out)
}

func (f *fakeAPI) locked(r *http.Request, body map[string]any) (any, int) {
	f.mu.Lock()
	defer f.mu.Unlock() // route reports API errors by panicking
	return f.route(r, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound() { panic(apiError{404, "not_found", "The requested resource does not exist."}) }
func invalid(msg string) {
	panic(apiError{422, "validation_error", msg})
}

func now() string { return time.Now().UTC().Format("2006-01-02 15:04:05.000000+00") }

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (f *fakeAPI) route(r *http.Request, b map[string]any) (any, int) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	m := r.Method
	p := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	n := len(parts)
	switch {
	// ---- domains
	case p(0) == "domains" && n == 1 && m == "POST":
		return f.createDomain(b), 200
	case p(0) == "domains" && n == 1 && m == "GET":
		return f.list("domains", r, false), 200
	case p(0) == "domains" && n == 2 && m == "GET":
		return f.domainOut(f.must("domains", p(1))), 200
	case p(0) == "domains" && n == 2 && m == "PATCH":
		d := f.must("domains", p(1))
		for _, k := range []string{"open_tracking", "click_tracking"} {
			if v, ok := b[k]; ok {
				d[k] = v
			}
		}
		if v, ok := b["tracking_subdomain"]; ok {
			if v == nil || v == "" {
				if d["tracking_subdomain"] != nil {
					invalid("The tracking subdomain can be changed but not removed.")
				}
			} else {
				d["tracking_subdomain"] = v
				d["tracking_status"] = "pending"
			}
		}
		return map[string]any{"object": "domain", "id": d["id"]}, 200
	case p(0) == "domains" && n == 3 && p(2) == "verify" && m == "POST":
		d := f.must("domains", p(1))
		f.verifyCalls++
		if f.verifyAfter > 0 && f.verifyCalls >= f.verifyAfter {
			d["status"] = "verified"
		} else {
			d["status"] = "pending"
		}
		return map[string]any{"object": "domain", "id": d["id"]}, 200
	case p(0) == "domains" && n == 2 && m == "DELETE":
		d := f.must("domains", p(1))
		delete(f.coll("domains"), p(1))
		return map[string]any{"object": "domain", "id": d["id"], "deleted": true}, 200

	// ---- webhooks
	case p(0) == "webhooks" && n == 1 && m == "POST":
		id := f.newID()
		headers := b["headers"]
		if headers == nil {
			headers = map[string]any{}
		}
		format := str(b, "format")
		if format == "" {
			format = "sendio"
		}
		o := map[string]any{"id": id, "created_at": now(), "status": "enabled", "endpoint": b["endpoint"], "events": b["events"],
			"format": format, "options": map[string]any{}, "headers": headers, "streams": []any{}, "include_subaccounts": false,
			"signing_secret": "whsec_" + id[len(id)-6:]}
		f.coll("webhooks")[id] = o
		out := copyMap(o)
		out["object"] = "webhook"
		return out, 200
	case p(0) == "webhooks" && n == 2 && m == "GET":
		out := copyMap(f.must("webhooks", p(1)))
		out["object"] = "webhook"
		return out, 200
	case p(0) == "webhooks" && n == 2 && m == "PATCH":
		o := f.must("webhooks", p(1))
		for _, k := range []string{"endpoint", "events", "format", "status"} {
			if v, ok := b[k]; ok {
				o[k] = v
			}
		}
		if v, ok := b["headers"]; ok {
			if v == nil {
				v = map[string]any{}
			}
			o["headers"] = v
		}
		return map[string]any{"object": "webhook", "id": o["id"]}, 200
	case p(0) == "webhooks" && n == 2 && m == "DELETE":
		f.must("webhooks", p(1))
		delete(f.coll("webhooks"), p(1))
		return map[string]any{"object": "webhook", "id": p(1), "deleted": true}, 200

	// ---- api keys
	case p(0) == "api-keys" && n == 1 && m == "POST":
		id := f.newID()
		perm := str(b, "permission")
		if perm == "" {
			perm = "full_access"
		}
		token := "sio_" + strings.ReplaceAll(id, "-", "")
		f.coll("api-keys")[id] = map[string]any{"id": id, "name": b["name"], "created_at": now(), "last_used_at": nil,
			"token_prefix": token[:12], "permission": perm, "domain_id": nullable(str(b, "domain_id")), "domain": nil}
		return map[string]any{"id": id, "object": "api_key", "token": token}, 200
	case p(0) == "api-keys" && n == 1 && m == "GET":
		return f.list("api-keys", r, false), 200
	case p(0) == "api-keys" && n == 2 && m == "GET":
		out := copyMap(f.must("api-keys", p(1)))
		out["object"] = "api_key"
		return out, 200
	case p(0) == "api-keys" && n == 2 && m == "PATCH":
		o := f.must("api-keys", p(1))
		o["name"] = b["name"]
		return map[string]any{"object": "api_key", "id": p(1)}, 200
	case p(0) == "api-keys" && n == 2 && m == "DELETE":
		f.must("api-keys", p(1))
		delete(f.coll("api-keys"), p(1))
		return map[string]any{"object": "api_key", "id": p(1), "deleted": true}, 200

	// ---- suppressions
	case p(0) == "suppressions" && n == 1 && m == "POST":
		email := str(b, "email")
		var o map[string]any
		for _, x := range f.coll("suppressions") {
			if strings.EqualFold(str(x, "email"), email) {
				o = x
			}
		}
		if o == nil {
			id := f.newID()
			o = map[string]any{"id": id, "email": email, "origin": "manual", "stream": nil, "source_id": nil, "created_at": now(),
				"comment": nil, "expires_at": nil, "expired": false}
			f.coll("suppressions")[id] = o
		}
		if v, ok := b["origin"]; ok && v != nil {
			o["origin"] = v
		}
		if v, ok := b["comment"]; ok && v != nil {
			o["comment"] = nullable(v.(string))
		}
		if v, ok := b["expires_at"]; ok && v != nil {
			t, err := time.Parse(time.RFC3339, v.(string))
			if err != nil {
				invalid("expires_at must be an RFC 3339 timestamp.")
			}
			o["expires_at"] = t.UTC().Format("2006-01-02 15:04:05+00")
		}
		out := copyMap(o)
		out["object"] = "suppression"
		return out, 200
	case p(0) == "suppressions" && n == 2 && m == "GET":
		out := copyMap(f.suppressionRef(p(1)))
		out["object"] = "suppression"
		return out, 200
	case p(0) == "suppressions" && n == 2 && m == "DELETE":
		o := f.suppressionRef(p(1))
		delete(f.coll("suppressions"), str(o, "id"))
		return map[string]any{"object": "suppression", "id": o["id"], "deleted": true}, 200

	// ---- templates
	case p(0) == "templates" && n == 1 && m == "POST":
		if str(b, "name") == "" || str(b, "html") == "" {
			invalid("Missing `name` or `html` field.")
		}
		id := f.newID()
		t := map[string]any{"id": id, "status": "draft", "created_at": now(), "updated_at": now(), "published_at": nil,
			"has_unpublished_versions": true, "version_number": float64(0)}
		f.coll("templates")[id] = t
		f.templateVersion(t, b)
		return map[string]any{"object": "template", "id": id}, 200
	case p(0) == "templates" && n == 1 && m == "GET":
		return f.list("templates", r, true), 200
	case p(0) == "templates" && n == 2 && m == "GET":
		out := copyMap(f.templateRef(p(1)))
		out["object"] = "template"
		return out, 200
	case p(0) == "templates" && n == 2 && m == "PATCH":
		t := f.templateRef(p(1))
		merged := copyMap(t)
		for k, v := range b {
			merged[k] = v
		}
		f.templateVersion(t, merged)
		return map[string]any{"object": "template", "id": t["id"]}, 200
	case p(0) == "templates" && n == 3 && p(2) == "publish" && m == "POST":
		t := f.templateRef(p(1))
		if str(t, "subject") == "" {
			invalid("A template needs a subject before it can be published.")
		}
		t["status"], t["published_at"], t["has_unpublished_versions"] = "published", now(), false
		return map[string]any{"object": "template", "id": t["id"]}, 200
	case p(0) == "templates" && n == 2 && m == "DELETE":
		t := f.templateRef(p(1))
		delete(f.coll("templates"), str(t, "id"))
		return map[string]any{"object": "template", "id": t["id"], "deleted": true}, 200

	// ---- segments, topics, contact properties
	case (p(0) == "segments" || p(0) == "topics" || p(0) == "contact-properties") && n == 1 && m == "POST":
		return f.createSimple(p(0), b), 200
	case (p(0) == "segments" || p(0) == "topics" || p(0) == "contact-properties") && n == 2 && m == "GET":
		out := copyMap(f.must(p(0), p(1)))
		return out, 200
	case p(0) == "segments" && n == 2 && m == "PATCH":
		o := f.must("segments", p(1))
		if strings.TrimSpace(str(b, "name")) == "" {
			invalid("Missing `name` field.")
		}
		o["name"] = strings.TrimSpace(str(b, "name"))
		return map[string]any{"object": "segment", "id": p(1)}, 200
	case p(0) == "topics" && n == 2 && m == "PATCH":
		o := f.must("topics", p(1))
		if b["default_subscription"] != nil {
			invalid("default_subscription cannot be changed.")
		}
		for _, k := range []string{"name", "description", "visibility"} {
			if _, ok := b[k]; ok {
				o[k] = nullable(str(b, k))
			}
		}
		return map[string]any{"object": "topic", "id": p(1)}, 200
	case p(0) == "contact-properties" && n == 2 && m == "PATCH":
		o := f.must("contact-properties", p(1))
		if b["key"] != nil || b["type"] != nil {
			invalid("The key and type of a contact property cannot be changed.")
		}
		o["fallback_value"] = b["fallback_value"]
		return map[string]any{"object": "contact_property", "id": p(1)}, 200
	case (p(0) == "segments" || p(0) == "topics" || p(0) == "contact-properties") && n == 2 && m == "DELETE":
		f.must(p(0), p(1))
		delete(f.coll(p(0)), p(1))
		return map[string]any{"id": p(1), "deleted": true}, 200

	// ---- inboxes
	case p(0) == "inboxes" && n == 1 && m == "POST":
		if c := str(b, "client_id"); c != "" {
			for _, x := range f.coll("inboxes") {
				if str(x, "client_id") == c {
					return x, 200
				}
			}
		}
		user, domain := str(b, "username"), str(b, "domain")
		if user == "" {
			user = "agent-" + strconv.Itoa(f.seq+1)
		}
		if domain == "" {
			domain = "agents.example.test"
		}
		id := f.newID()
		o := map[string]any{"id": id, "object": "inbox", "address": user + "@" + domain, "display_name": strings.TrimSpace(str(b, "display_name")),
			"client_id": nullable(str(b, "client_id")), "domain": domain, "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
		f.coll("inboxes")[id] = o
		return o, 200
	case p(0) == "inboxes" && n == 2 && m == "GET":
		return f.inboxRef(p(1)), 200
	case p(0) == "inboxes" && n == 2 && m == "PATCH":
		o := f.inboxRef(p(1))
		if v, ok := b["display_name"].(string); ok {
			o["display_name"] = strings.TrimSpace(v)
		}
		return o, 200
	case p(0) == "inboxes" && n == 2 && m == "DELETE":
		o := f.inboxRef(p(1))
		delete(f.coll("inboxes"), str(o, "id"))
		return map[string]any{"object": "inbox", "id": o["id"], "deleted": true}, 200
	}
	return map[string]any{"statusCode": 404, "name": "not_found", "message": "Route not found."}, 404
}

func copyMap(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (f *fakeAPI) must(coll, id string) map[string]any {
	o := f.coll(coll)[id]
	if o == nil {
		notFound()
	}
	return o
}

// list pages a collection newest first; paged lists default to 20 items.
func (f *fakeAPI) list(coll string, r *http.Request, paged bool) map[string]any {
	items := []map[string]any{}
	for _, o := range f.coll(coll) {
		items = append(items, o)
	}
	sort.Slice(items, func(i, j int) bool { return str(items[i], "id") > str(items[j], "id") })
	lim := 0
	if paged {
		lim = 20
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		lim, _ = strconv.Atoi(v)
	}
	if after := r.URL.Query().Get("after"); after != "" {
		for i, o := range items {
			if str(o, "id") == after {
				items = items[i+1:]
				break
			}
		}
	}
	more := false
	if lim > 0 && len(items) > lim {
		items, more = items[:lim], true
	}
	data := []map[string]any{}
	for _, o := range items {
		data = append(data, copyMap(o))
	}
	return map[string]any{"object": "list", "has_more": more, "data": data}
}

func (f *fakeAPI) createDomain(b map[string]any) map[string]any {
	name := str(b, "name")
	for _, d := range f.coll("domains") {
		if d["name"] == name {
			panic(apiError{409, "validation_error", "Domain already exists."})
		}
	}
	region := str(b, "region")
	if region == "" {
		region = "us-east-1"
	}
	ret := str(b, "custom_return_path")
	if ret == "" {
		ret = "send"
	}
	id := f.newID()
	d := map[string]any{"id": id, "name": name, "status": "not_started", "created_at": now(), "region": region,
		"open_tracking": b["open_tracking"] == true, "click_tracking": b["click_tracking"] == true,
		"tracking_subdomain": nil, "tracking_status": "off", "custom_return_path": ret, "delivery_route": "ses", "tls": "opportunistic",
		"capabilities": map[string]any{"sending": "enabled", "receiving": "disabled"}}
	if s := str(b, "tracking_subdomain"); s != "" {
		d["tracking_subdomain"], d["tracking_status"] = s, "pending"
	}
	f.coll("domains")[id] = d
	return f.domainOut(d)
}

func (f *fakeAPI) domainOut(d map[string]any) map[string]any {
	out := copyMap(d)
	out["object"] = "domain"
	name, ret := str(d, "name"), str(d, "custom_return_path")
	recs := []map[string]any{
		{"record": "DKIM", "type": "CNAME", "name": "abc._domainkey", "value": "abc.dkim.amazonses.com", "ttl": "Auto", "status": "not_started"},
		{"record": "SPF", "type": "MX", "name": ret, "value": "feedback-smtp." + str(d, "region") + ".amazonses.com", "ttl": "Auto", "priority": 10, "status": "not_started"},
		{"record": "SPF", "type": "TXT", "name": ret, "value": "v=spf1 include:amazonses.com ~all", "ttl": "Auto", "status": "not_started"},
	}
	if s := str(d, "tracking_subdomain"); s != "" {
		recs = append(recs, map[string]any{"record": "Tracking", "type": "CNAME", "name": s, "value": "track." + name + ".example", "ttl": "Auto", "status": "pending"})
	}
	out["records"] = recs
	out["events"] = []any{}
	return out
}

func (f *fakeAPI) suppressionRef(ref string) map[string]any {
	for _, o := range f.coll("suppressions") {
		if str(o, "id") == ref || strings.EqualFold(str(o, "email"), ref) {
			return o
		}
	}
	notFound()
	return nil
}

func (f *fakeAPI) templateRef(ref string) map[string]any {
	for _, o := range f.coll("templates") {
		if str(o, "id") == ref || (o["alias"] != nil && str(o, "alias") == ref) {
			return o
		}
	}
	notFound()
	return nil
}

func (f *fakeAPI) inboxRef(ref string) map[string]any {
	for _, o := range f.coll("inboxes") {
		if str(o, "id") == ref || strings.EqualFold(str(o, "address"), ref) {
			return o
		}
	}
	notFound()
	return nil
}

// templateVersion saves b as a new draft version of t (fields as the
// backend's templateVersion: "" stores null).
func (f *fakeAPI) templateVersion(t map[string]any, b map[string]any) {
	t["name"] = b["name"]
	t["alias"] = nullable(str(b, "alias"))
	for _, k := range []string{"subject", "from", "text", "preview_text"} {
		t[k] = nullable(str(b, k))
	}
	t["html"] = str(b, "html")
	engine := str(b, "template_engine")
	if engine == "" {
		engine = "simple"
	}
	t["template_engine"] = engine
	vars := []any{}
	if vs, ok := b["variables"].([]any); ok {
		for _, v := range vs {
			vm := v.(map[string]any)
			vars = append(vars, map[string]any{"id": f.newID(), "key": vm["key"], "type": vm["type"], "fallback_value": vm["fallback_value"]})
		}
	}
	t["variables"] = vars
	t["version_number"] = t["version_number"].(float64) + 1
	t["current_version_id"] = f.newID()
	t["has_unpublished_versions"] = true
	t["updated_at"] = now()
}

func (f *fakeAPI) createSimple(coll string, b map[string]any) map[string]any {
	id := f.newID()
	var o map[string]any
	switch coll {
	case "segments":
		o = map[string]any{"object": "segment", "id": id, "name": strings.TrimSpace(str(b, "name")), "created_at": now()}
	case "topics":
		vis := str(b, "visibility")
		if vis == "" {
			vis = "private"
		}
		o = map[string]any{"object": "topic", "id": id, "name": b["name"], "description": nullable(str(b, "description")),
			"default_subscription": b["default_subscription"], "visibility": vis, "created_at": now()}
	case "contact-properties":
		for _, x := range f.coll(coll) {
			if x["key"] == b["key"] {
				panic(apiError{409, "validation_error", "A contact property with this key already exists."})
			}
		}
		o = map[string]any{"object": "contact_property", "id": id, "key": b["key"], "type": b["type"], "fallback_value": b["fallback_value"], "created_at": now()}
	}
	f.coll(coll)[id] = o
	return map[string]any{"object": o["object"], "id": id}
}
