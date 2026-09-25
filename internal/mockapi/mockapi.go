// Package mockapi is an in-memory stand-in for the HIOK REST API, shaped
// after the requests the provider sends. It backs the provider's tests and
// `make demo`; it is not a specification of the real API.
package mockapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Options inject the failure modes the provider has to cope with.
type Options struct {
	// Owner prefixes stored names ("<owner>#<name>"), like the real API.
	Owner string
	// HiddenReads makes a newly created resource invisible to the next N list
	// calls, and a deleted one linger for N list calls (async provisioning).
	HiddenReads int
	// FailCreate makes create calls answer 200 {"success":false,"message":...}.
	FailCreate string
	// ExpireTokenAfter answers 401 once after this many authenticated calls.
	ExpireTokenAfter int
	// Transient503 answers 503 to the first N GET list calls.
	Transient503 int
}

type item struct {
	Name     string
	Fields   map[string]any
	hidden   int // list calls left before a new item shows up
	deleting int // list calls left before a deleted item disappears (-1: live)
}

// Server is the mock. Use Handler() with httptest or ListenAndServe.
type Server struct {
	mu    sync.Mutex
	opt   Options
	store map[string]map[string]*item // kind -> name -> item
	calls int
	log   []string
	token int
}

// New returns a mock with the given options.
func New(opt Options) *Server {
	if opt.Owner == "" {
		opt.Owner = "owner-42"
	}
	return &Server{opt: opt, store: map[string]map[string]*item{"vm": {}, "vnet": {}, "ct": {}, "sa": {}}}
}

// Log returns "METHOD /path?query" for every request received.
func (s *Server) Log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

// Names lists live resource names of a kind: vm, vnet, ct, sa.
func (s *Server) Names(kind string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for n, it := range s.store[kind] {
		if it.deleting < 0 {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Put creates a resource out of band (as if made in the console).
func (s *Server) Put(kind, name string, fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fields == nil {
		fields = map[string]any{}
	}
	s.store[kind][name] = &item{Name: name, Fields: fields, deleting: -1}
}

// Remove deletes a resource out of band.
func (s *Server) Remove(kind, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store[kind], name)
}

// SetOptions swaps the failure modes at runtime.
func (s *Server) SetOptions(opt Options) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if opt.Owner == "" {
		opt.Owner = s.opt.Owner
	}
	s.opt = opt
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter) { writeJSON(w, 200, map[string]any{"success": true}) }

// Handler serves the API.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.log = append(s.log, r.Method+" "+r.URL.RequestURI())

		if r.URL.Path == "/api/OAuth/token" {
			var p struct{ Email, Password string }
			_ = json.Unmarshal(raw, &p)
			if p.Email == "" || p.Password == "" {
				writeJSON(w, 200, map[string]any{"data": map[string]any{"success": false, "message": "Invalid email or password"}})
				return
			}
			s.token++
			writeJSON(w, 200, map[string]any{"data": map[string]any{"success": true, "token": "tok-" + strconv.Itoa(s.token)}})
			return
		}

		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			writeJSON(w, 401, map[string]any{"message": "Unauthorized"})
			return
		}
		s.calls++
		if s.opt.ExpireTokenAfter > 0 && s.calls == s.opt.ExpireTokenAfter {
			writeJSON(w, 401, map[string]any{"message": "Token expired"})
			return
		}
		if s.opt.Transient503 > 0 && r.Method == http.MethodGet {
			s.opt.Transient503--
			writeJSON(w, 503, map[string]any{"message": "Service temporarily unavailable"})
			return
		}
		s.route(w, r, raw)
	})
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, raw []byte) {
	p := r.URL.Path
	switch {
	case p == "/api/VirtualMachine/create-vm" && r.Method == http.MethodPost:
		var in struct {
			VMName         string   `json:"vmName"`
			Regions        []string `json:"regions"`
			SourceFilePath string   `json:"sourceFilePath"`
		}
		_ = json.Unmarshal(raw, &in)
		s.create(w, "vm", in.VMName, map[string]any{"regionId": first(in.Regions), "status": "running", "privateIp": "10.20.1.5"})
	case p == "/api/VirtualMachine/list-vms-info" && r.Method == http.MethodGet:
		s.list(w, "vm", nil, func(it *item) map[string]any {
			return merge(it.Fields, map[string]any{"vmName": s.opt.Owner + "#" + it.Name})
		})
	case p == "/api/VirtualMachine/destroy-vm" && r.Method == http.MethodDelete:
		s.remove(w, "vm", r.URL.Query().Get("vmName"))
	case p == "/api/VirtualMachine/list-local-vm-images" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"data": []map[string]string{
			{"documentId": "img-ubuntu-2404", "imageName": "ubuntu-24.04", "description": "Ubuntu 24.04 LTS"},
			{"documentId": "img-debian-12", "imageName": "debian-12", "description": "Debian 12"},
		}})

	case p == "/api/VirtualNetwork/create-vnet" && r.Method == http.MethodPost:
		var in struct {
			Name         string `json:"name"`
			AddressSpace string `json:"addressSpace"`
		}
		_ = json.Unmarshal(raw, &in)
		s.create(w, "vnet", in.Name, map[string]any{"status": "active", "addressSpace": in.AddressSpace})
	case p == "/api/VirtualNetwork/list-vnets" && r.Method == http.MethodGet:
		s.list(w, "vnet", nil, func(it *item) map[string]any { return merge(it.Fields, map[string]any{"name": it.Name}) })
	case p == "/api/VirtualNetwork/delete-vnet" && r.Method == http.MethodDelete:
		var name string
		if json.Unmarshal(raw, &name) != nil || name == "" {
			name = r.URL.Query().Get("name")
		}
		s.remove(w, "vnet", name)

	case p == "/api/Containers/createcontainer" && r.Method == http.MethodPost:
		var in struct {
			Name  string `json:"name"`
			Image string `json:"image"`
		}
		_ = json.Unmarshal(raw, &in)
		s.create(w, "ct", in.Name, map[string]any{"status": "running", "image": in.Image})
	case p == "/api/Containers/listallcontainers" && r.Method == http.MethodPost:
		var in struct{ PageNumber, PageSize int }
		_ = json.Unmarshal(raw, &in)
		s.list(w, "ct", &in, func(it *item) map[string]any { return merge(it.Fields, map[string]any{"name": "/" + it.Name}) })
	case p == "/api/Containers/deletecontainer" && r.Method == http.MethodPost:
		var in struct{ ContainerName string }
		_ = json.Unmarshal(raw, &in)
		s.remove(w, "ct", in.ContainerName)

	case p == "/api/StorageAccount" && r.Method == http.MethodPost:
		var in struct {
			Name          string `json:"name"`
			PrimaryRegion string `json:"primaryRegion"`
		}
		_ = json.Unmarshal(raw, &in)
		s.create(w, "sa", in.Name, map[string]any{"status": "available", "primaryRegion": in.PrimaryRegion})
	case p == "/api/StorageAccount" && r.Method == http.MethodGet:
		s.list(w, "sa", nil, func(it *item) map[string]any { return merge(it.Fields, map[string]any{"name": it.Name}) })
	case strings.HasPrefix(p, "/api/StorageAccount/") && r.Method == http.MethodDelete && p != "/api/StorageAccount/regions":
		name, _ := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/api/StorageAccount/"))
		s.remove(w, "sa", name)
	case strings.EqualFold(p, "/api/storageaccount/regions") && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"data": []map[string]string{
			{"id": "south-india", "displayName": "South India"},
			{"id": "west-india", "displayName": "West India"},
		}})
	default:
		writeJSON(w, 404, map[string]any{"message": fmt.Sprintf("no route for %s %s", r.Method, p)})
	}
}

func (s *Server) create(w http.ResponseWriter, kind, name string, fields map[string]any) {
	if s.opt.FailCreate != "" {
		writeJSON(w, 200, map[string]any{"success": false, "message": s.opt.FailCreate})
		return
	}
	if name == "" {
		writeJSON(w, 400, map[string]any{"message": "name is required"})
		return
	}
	if _, exists := s.store[kind][name]; exists {
		writeJSON(w, 409, map[string]any{"message": fmt.Sprintf("%q already exists", name)})
		return
	}
	s.store[kind][name] = &item{Name: name, Fields: fields, hidden: s.opt.HiddenReads, deleting: -1}
	ok(w)
}

func (s *Server) remove(w http.ResponseWriter, kind, name string) {
	it, exists := s.store[kind][name]
	if !exists || it.deleting >= 0 {
		writeJSON(w, 404, map[string]any{"message": fmt.Sprintf("%q not found", name)})
		return
	}
	it.deleting = s.opt.HiddenReads
	if it.deleting == 0 {
		delete(s.store[kind], name)
	}
	ok(w)
}

func (s *Server) list(w http.ResponseWriter, kind string, page *struct{ PageNumber, PageSize int }, render func(*item) map[string]any) {
	names := make([]string, 0, len(s.store[kind]))
	for n := range s.store[kind] {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []map[string]any{}
	for _, n := range names {
		it := s.store[kind][n]
		if it.hidden > 0 {
			it.hidden--
			continue
		}
		if it.deleting > 0 {
			it.deleting--
			if it.deleting == 0 {
				delete(s.store[kind], n)
			}
		}
		out = append(out, render(it))
	}
	if page != nil && page.PageSize > 0 {
		start := (max(page.PageNumber, 1) - 1) * page.PageSize
		if start > len(out) {
			start = len(out)
		}
		out = out[start:min(start+page.PageSize, len(out))]
	}
	writeJSON(w, 200, map[string]any{"data": out})
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
