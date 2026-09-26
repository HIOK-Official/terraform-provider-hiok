// Package mockapi is an in-memory stand-in for the HIOK REST API. Its request
// and response shapes, status codes and quirks were recorded from the live
// test deployment (test.hiokcloud.com) and its OpenAPI document. It backs the
// provider's tests and `make demo`; it is not a specification of the API.
package mockapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Options inject the failure modes the provider has to cope with.
type Options struct {
	// HiddenReads makes a newly created resource invisible to the next N list
	// calls, and a deleted one linger for N list calls (async provisioning).
	HiddenReads int
	// FailCreate makes create calls fail the way the API does:
	// 400 {"message":..., "data":{"isSuccess":false,"data":<FailCreate>}}.
	FailCreate string
	// ExpireTokenAfter answers 401 once after this many authenticated calls.
	ExpireTokenAfter int
	// Transient503 answers 503 to the first N GET calls.
	Transient503 int
	// OutageAfterStop answers 502 to the next N requests (sign-in included)
	// after a stop-vm, as the live platform did.
	OutageAfterStop int
}

type item struct {
	ID       string
	Name     string
	Fields   map[string]any
	hidden   int // list calls left before a new item shows up
	deleting int // list calls left before a deleted item disappears (-1: live)
}

// Server is the mock. Use Handler() with httptest or ListenAndServe.
type Server struct {
	mu      sync.Mutex
	opt     Options
	store   map[string]map[string]*item // kind -> name -> item
	subnets map[string][]map[string]any // vnet id -> subnets
	calls   int
	outage  int
	seq     int
	log     []string
	token   int
}

// Regions mirrors the live deployment: only canada accepts new resources.
var Regions = []map[string]any{
	{"id": "canada", "name": "canada", "displayName": "Canada", "location": "Beauharnois, Quebec", "isAvailable": true},
	{"id": "central-india", "name": "central-india", "displayName": "Central India", "location": "Mumbai", "isAvailable": false},
	{"id": "germany", "name": "germany", "displayName": "Germany", "location": "Frankfurt", "isAvailable": false},
}

// Images mirrors list-local-vm-images.
var Images = []map[string]any{
	{"documentId": "ubuntu-24.04-amd64", "imageName": "Ubuntu 24.04 LTS", "description": "Ubuntu 24.04 LTS (Noble) Server Cloud Image", "architecture": "amd64"},
	{"documentId": "ubuntu-22.04-amd64", "imageName": "Ubuntu 22.04 LTS", "description": "Ubuntu 22.04 LTS (Jammy) Server Cloud Image", "architecture": "amd64"},
	{"documentId": "debian-12-amd64", "imageName": "Debian 12", "description": "Debian 12 (Bookworm) Cloud Image", "architecture": "amd64"},
	{"documentId": "ubuntu-24.04-arm64", "imageName": "Ubuntu 24.04 LTS (ARM64)", "description": "Ubuntu 24.04 LTS (Noble, ARM64) Server Cloud Image", "architecture": "arm64"},
}

// New returns a mock with the given options.
func New(opt Options) *Server {
	return &Server{opt: opt, store: map[string]map[string]*item{"vm": {}, "vnet": {}, "ct": {}, "sa": {}}, subnets: map[string][]map[string]any{}}
}

// Log returns "METHOD /path?query body" for every request received.
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

// Subnets returns the subnets of a network by name.
func (s *Server) Subnets(vnetName string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it := s.store["vnet"][vnetName]; it != nil {
		return s.subnets[it.ID]
	}
	return nil
}

// Put creates a resource out of band (as if made in the console).
func (s *Server) Put(kind, name string, fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fields == nil {
		fields = map[string]any{}
	}
	it := &item{ID: s.newID(), Name: name, Fields: fields, deleting: -1}
	s.store[kind][name] = it
	if kind == "vnet" {
		if cidr, _ := fields["addressSpace"].(string); cidr != "" {
			s.subnets[it.ID] = []map[string]any{s.defaultSubnet(it, cidr)}
		}
	}
}

// Remove deletes a resource out of band.
func (s *Server) Remove(kind, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store[kind], name)
}

func (s *Server) newID() string {
	s.seq++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.seq)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Handler serves the API.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.log = append(s.log, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(raw)))
		if s.outage > 0 {
			s.outage--
			w.WriteHeader(502)
			_, _ = io.WriteString(w, "Error 502: Bad gateway")
			return
		}

		if r.URL.Path == "/api/OAuth/token" {
			var p struct{ Email, Password string }
			_ = json.Unmarshal(raw, &p)
			if p.Email == "" || p.Password == "" {
				writeJSON(w, 200, map[string]any{"message": "Login failed", "data": map[string]any{"success": false, "message": "Invalid email or password"}})
				return
			}
			s.token++
			writeJSON(w, 200, map[string]any{"message": "Token Generated Successfully",
				"data": map[string]any{"success": true, "message": "Login Successful.", "token": "tok-" + strconv.Itoa(s.token)}})
			return
		}

		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(401)
			return
		}
		s.calls++
		if s.opt.ExpireTokenAfter > 0 && s.calls == s.opt.ExpireTokenAfter {
			w.WriteHeader(401)
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

func regionState(id string) (known, available bool) {
	for _, r := range Regions {
		if r["id"] == id {
			return true, r["isAvailable"].(bool)
		}
	}
	return false, false
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, raw []byte) {
	p := r.URL.Path
	switch {
	case strings.EqualFold(p, "/api/storageaccount/regions") && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"message": "Regions retrieved successfully", "data": Regions})

	// ---- virtual machines ----
	case p == "/api/VirtualMachine/list-local-vm-images" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"data": Images})
	case p == "/api/VirtualMachine/create-vm" && r.Method == http.MethodPost:
		var in struct {
			VMName         string   `json:"vmName"`
			Regions        []string `json:"regions"`
			SourceFilePath string   `json:"sourceFilePath"`
			VCPUCount      int      `json:"vcpuCount"`
			RAMSize        float64  `json:"ramSize"`
			Username       string   `json:"username"`
			SSHPublicKey   string   `json:"sshPublicKey"`
		}
		_ = json.Unmarshal(raw, &in)
		region := first(in.Regions)
		if known, _ := regionState(region); !known {
			// The live API hangs until the edge proxy gives up.
			w.WriteHeader(524)
			_, _ = io.WriteString(w, "error code: 524")
			return
		}
		fail := s.opt.FailCreate
		if fail == "" && !knownImage(in.SourceFilePath) {
			fail = "Failed to download VM image"
		}
		if fail != "" {
			writeJSON(w, 400, map[string]any{"message": "Failed Deploying Virtual Machine.",
				"data": map[string]any{"isSuccess": false, "data": fail, "hostname": nil}})
			return
		}
		if _, exists := s.store["vm"][in.VMName]; exists {
			writeJSON(w, 400, map[string]any{"message": "Failed Deploying Virtual Machine.",
				"data": map[string]any{"isSuccess": false, "data": "Virtual machine already exists"}})
			return
		}
		it := &item{ID: s.newID(), Name: in.VMName, hidden: s.opt.HiddenReads, deleting: -1,
			Fields: map[string]any{"state": "running", "vCpu": in.VCPUCount, "memory": int(in.RAMSize * 1792), "regionId": region,
				"username": in.Username, "sshPublicKey": in.SSHPublicKey}}
		s.store["vm"][in.VMName] = it
		hostname := fmt.Sprintf("%s-%s-%06x.hiokcloud.com", in.VMName, region, s.seq)
		it.Fields["hostname"] = hostname
		writeJSON(w, 202, map[string]any{"message": "Virtual Machine Deployment Started.",
			"data": map[string]any{"isSuccess": true, "data": "Virtual Machine created successfully", "hostname": hostname, "sshPrivateKey": "", "sshPublicKey": ""}})
	case p == "/api/VirtualMachine/list-vms-info" && r.Method == http.MethodGet:
		s.list(w, "vm", "Virtual Machine Listed Successfully.", func(it *item) map[string]any {
			return map[string]any{"name": it.Name, "id": it.ID, "state": it.Fields["state"], "vCpu": it.Fields["vCpu"],
				"memory": it.Fields["memory"], "regionId": it.Fields["regionId"]}
		})
	case (p == "/api/VirtualMachine/start-vm" || p == "/api/VirtualMachine/stop-vm") && r.Method == http.MethodPost:
		var in struct {
			VMName string `json:"vmName"`
		}
		_ = json.Unmarshal(raw, &in)
		it := s.store["vm"][in.VMName]
		if it == nil {
			writeJSON(w, 400, map[string]any{"message": "Failed Starting Virtual Machine."})
			return
		}
		if strings.HasSuffix(p, "stop-vm") {
			it.Fields["state"] = "shutoff"
			s.outage = s.opt.OutageAfterStop
			writeJSON(w, 200, map[string]any{"message": "Virtual Machine Stopped Successfully."})
		} else {
			it.Fields["state"] = "running"
			writeJSON(w, 200, map[string]any{"message": "Virtual Machine Started Successfully."})
		}
	case strings.HasPrefix(p, "/api/VirtualMachine/") && strings.HasSuffix(p, "/connect") && r.Method == http.MethodGet:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "/api/VirtualMachine/"), "/connect")
		it := s.store["vm"][name]
		if it == nil || it.hidden > 0 {
			writeJSON(w, 404, map[string]any{"message": "Virtual machine not found"})
			return
		}
		ip := fmt.Sprintf("10.20.0.%d", 2+len(it.ID)%200)
		writeJSON(w, 200, map[string]any{"message": "Connection info", "data": map[string]any{
			"vmName": name, "status": it.Fields["state"], "region": it.Fields["regionId"], "username": firstNonEmpty(str(it.Fields["username"]), "hiokuser"),
			"authenticationType": "ssh", "privateIp": ip, "publicIps": []any{}, "hostname": str(it.Fields["hostname"]),
			"sshCommand": "ssh " + firstNonEmpty(str(it.Fields["username"]), "hiokuser") + "@" + ip}})
	case p == "/api/VirtualMachine/destroy-vm" && r.Method == http.MethodDelete:
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, 415, map[string]any{"title": "Unsupported Media Type", "status": 415})
			return
		}
		var in struct {
			VMName string `json:"vmName"`
		}
		_ = json.Unmarshal(raw, &in)
		s.markDeleted("vm", in.VMName)
		// Answers 200 whether or not the VM exists.
		writeJSON(w, 200, map[string]any{"message": "Virtual Machine Deleted Successfully."})

	// ---- virtual networks ----
	case p == "/api/VirtualNetwork/create-vnet" && r.Method == http.MethodPost:
		var in struct {
			Name         string   `json:"name"`
			AddressSpace string   `json:"addressSpace"`
			Region       string   `json:"region"`
			Regions      []string `json:"regions"`
		}
		_ = json.Unmarshal(raw, &in)
		if s.opt.FailCreate != "" {
			writeJSON(w, 400, map[string]any{"message": "Failed creating virtual network", "data": map[string]any{"isSuccess": false, "data": s.opt.FailCreate}})
			return
		}
		if _, exists := s.store["vnet"][in.Name]; exists {
			writeJSON(w, 400, map[string]any{"message": "Failed creating virtual network", "data": map[string]any{"isSuccess": false, "data": "Virtual network already exists"}})
			return
		}
		it := &item{ID: s.newID(), Name: in.Name, hidden: s.opt.HiddenReads, deleting: -1,
			Fields: map[string]any{"status": "Available", "regionId": firstNonEmpty(in.Region, first(in.Regions)), "cidr": in.AddressSpace}}
		s.store["vnet"][in.Name] = it
		// create-vnet ignores subnet fields and always adds "default".
		s.subnets[it.ID] = []map[string]any{s.defaultSubnet(it, in.AddressSpace)}
		writeJSON(w, 200, map[string]any{"message": "Virtual network created successfully", "data": map[string]any{"isSuccess": true, "data": "Virtual Network created successfully"}})
	case p == "/api/VirtualNetwork/list-vnets" && r.Method == http.MethodGet:
		s.list(w, "vnet", "", func(it *item) map[string]any {
			cidr, _ := it.Fields["cidr"].(string)
			if cidr == "" {
				cidr, _ = it.Fields["addressSpace"].(string)
			}
			ip, mask, _ := strings.Cut(cidr, "/")
			return map[string]any{"id": it.ID, "name": it.Name, "vnetName": it.Name, "status": firstNonEmpty(str(it.Fields["status"]), "Available"),
				"regionId": str(it.Fields["regionId"]), "vnets": []map[string]any{{"ipAddress": ip, "subnetMask": mask, "subnets": s.subnets[it.ID]}}}
		})
	case p == "/api/VirtualNetwork/delete-vnet" && r.Method == http.MethodDelete:
		var name string
		_ = json.Unmarshal(raw, &name)
		if it := s.store["vnet"][name]; it == nil || it.deleting >= 0 {
			writeJSON(w, 400, map[string]any{"message": "Failed Deleting Virtual Network."})
			return
		}
		s.markDeleted("vnet", name)
		writeJSON(w, 200, map[string]any{"message": "Virtual Network Deleted Successfully."})
	case strings.HasPrefix(p, "/api/VirtualNetwork/") && strings.HasSuffix(p, "/subnets"):
		vnetID := strings.TrimSuffix(strings.TrimPrefix(p, "/api/VirtualNetwork/"), "/subnets")
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"message": "Subnets retrieved successfully", "data": s.subnets[vnetID]})
			return
		}
		var in map[string]any
		_ = json.Unmarshal(raw, &in)
		subnet := map[string]any{"id": firstNonEmpty(str(in["id"]), s.newID()), "name": in["name"], "ipRange": in["ipRange"], "size": in["size"], "addressSpace": in["addressSpace"], "vnetId": vnetID}
		list := s.subnets[vnetID]
		replaced := false
		for i := range list {
			if list[i]["id"] == subnet["id"] {
				list[i], replaced = subnet, true
			}
		}
		if !replaced {
			list = append(list, subnet)
		}
		s.subnets[vnetID] = list
		writeJSON(w, 200, map[string]any{"message": fmt.Sprintf("Subnet '%v' was updated.", in["name"]), "data": subnet})

	// ---- containers ----
	case p == "/api/Containers/createcontainer" && r.Method == http.MethodPost:
		var in struct {
			Name  string `json:"name"`
			Image string `json:"image"`
		}
		_ = json.Unmarshal(raw, &in)
		if s.opt.FailCreate != "" {
			writeJSON(w, 400, map[string]any{"message": s.opt.FailCreate})
			return
		}
		s.store["ct"][in.Name] = &item{ID: fmt.Sprintf("%064x", s.seq+1), Name: in.Name, hidden: s.opt.HiddenReads, deleting: -1,
			Fields: map[string]any{"status": "running", "image": in.Image, "dnsHostname": in.Name + ".hiokcloud.com"}}
		s.seq++
		writeJSON(w, 200, map[string]any{"data": "", "message": "Container Created Successfully."})
	case p == "/api/Containers/listallcontainers" && r.Method == http.MethodPost:
		// Paging fields are ignored: every container is returned.
		s.list(w, "ct", "Container Listed Successfully.", func(it *item) map[string]any {
			return merge(it.Fields, map[string]any{"name": it.Name, "id": it.ID})
		})
	case p == "/api/Containers/deletecontainer" && r.Method == http.MethodPost:
		var in struct{ ContainerName string }
		_ = json.Unmarshal(raw, &in)
		s.markDeleted("ct", in.ContainerName)
		writeJSON(w, 200, map[string]any{"data": in.ContainerName, "message": "Container Deleted Successfully."})

	// ---- storage accounts ----
	case p == "/api/StorageAccount" && r.Method == http.MethodPost:
		var in struct {
			Name          string `json:"name"`
			DisplayName   string `json:"displayName"`
			PrimaryRegion string `json:"primaryRegion"`
			StorageTier   string `json:"storageTier"`
			Redundancy    string `json:"redundancy"`
			Consistency   string `json:"consistencyMode"`
			WriteAck      string `json:"writeAcknowledgement"`
		}
		_ = json.Unmarshal(raw, &in)
		fail := func(msg string) {
			writeJSON(w, 500, map[string]any{"message": "Failed to create storage account", "error": msg})
		}
		switch {
		case s.opt.FailCreate != "":
			fail(s.opt.FailCreate)
			return
		case !oneOf(in.StorageTier, "hot", "cool", "cold", "archive"):
			fail("Invalid storage tier: " + in.StorageTier + ". Valid: hot, cool, cold, archive")
			return
		case !oneOf(in.Redundancy, "LRS", "ZRS", "GRS", "RA-GRS"):
			fail("Invalid redundancy type: " + in.Redundancy + ". Valid: LRS, ZRS, GRS, RA-GRS")
			return
		}
		if known, _ := regionState(in.PrimaryRegion); !known {
			fail("Invalid primary region: " + in.PrimaryRegion)
			return
		}
		// Storage accounts are rows in the database: the list is never stale.
		it := &item{ID: s.newID(), Name: in.Name, deleting: -1,
			Fields: map[string]any{"displayName": in.DisplayName, "status": "active", "primaryRegion": in.PrimaryRegion,
				"storageTier": in.StorageTier, "redundancy": in.Redundancy, "quotaBytes": 10737418240,
				"consistencyMode": in.Consistency, "writeAcknowledgement": in.WriteAck}}
		it.Fields["primaryEndpoint"] = "https://test.hiokcloud.com/api/StorageAccount/" + it.ID
		s.store["sa"][in.Name] = it
		writeJSON(w, 200, map[string]any{"message": "Storage account created successfully", "data": merge(it.Fields, map[string]any{"id": it.ID, "name": it.Name})})
	case p == "/api/StorageAccount" && r.Method == http.MethodGet:
		s.list(w, "sa", "Storage accounts retrieved successfully", func(it *item) map[string]any {
			return merge(it.Fields, map[string]any{"name": it.Name, "id": it.ID})
		})
	case strings.HasPrefix(p, "/api/StorageAccount/") && r.Method == http.MethodGet:
		// Like the real API, a deleted account is still answered, marked deleted.
		id := strings.TrimPrefix(p, "/api/StorageAccount/")
		for _, it := range s.store["sa"] {
			if it.ID == id {
				out := merge(it.Fields, map[string]any{"id": it.ID, "name": it.Name})
				if it.deleting >= 0 {
					out["status"] = "deleted"
				}
				writeJSON(w, 200, map[string]any{"message": "Storage account retrieved successfully", "data": out})
				return
			}
		}
		writeJSON(w, 404, map[string]any{"message": "Storage account not found"})
	case strings.HasPrefix(p, "/api/StorageAccount/") && r.Method == http.MethodPut:
		id := strings.TrimPrefix(p, "/api/StorageAccount/")
		var in struct {
			DisplayName string `json:"displayName"`
			StorageTier string `json:"storageTier"`
			Redundancy  string `json:"redundancy"`
		}
		_ = json.Unmarshal(raw, &in)
		for _, it := range s.store["sa"] {
			if it.ID == id {
				it.Fields["displayName"], it.Fields["storageTier"], it.Fields["redundancy"] = in.DisplayName, in.StorageTier, in.Redundancy
				writeJSON(w, 200, map[string]any{"message": "Storage account updated successfully", "data": merge(it.Fields, map[string]any{"id": it.ID, "name": it.Name})})
				return
			}
		}
		writeJSON(w, 404, map[string]any{"message": "Storage account not found"})
	case strings.HasPrefix(p, "/api/StorageAccount/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(p, "/api/StorageAccount/")
		if !isUUID(id) {
			writeJSON(w, 400, map[string]any{"title": "One or more validation errors occurred.", "status": 400,
				"errors": map[string][]string{"id": {fmt.Sprintf("The value '%s' is not valid.", id)}}})
			return
		}
		for name, it := range s.store["sa"] {
			if it.ID == id && it.deleting < 0 {
				s.markDeleted("sa", name)
				writeJSON(w, 200, map[string]any{"message": "Storage account deleted successfully"})
				return
			}
		}
		writeJSON(w, 404, map[string]any{"message": "Storage account not found"})
	default:
		writeJSON(w, 404, map[string]any{"message": fmt.Sprintf("no route for %s %s", r.Method, p)})
	}
}

func (s *Server) defaultSubnet(it *item, cidr string) map[string]any {
	ip, mask, _ := strings.Cut(cidr, "/")
	start, end := ip, ip
	if _, n, err := net.ParseCIDR(cidr); err == nil && n.IP.To4() != nil {
		b := n.IP.To4()
		v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		m := n.Mask
		bc := v | ^(uint32(m[0])<<24 | uint32(m[1])<<16 | uint32(m[2])<<8 | uint32(m[3]))
		start, end = net.IPv4(byte((v+2)>>24), byte((v+2)>>16), byte((v+2)>>8), byte(v+2)).String(),
			net.IPv4(byte((bc-1)>>24), byte((bc-1)>>16), byte((bc-1)>>8), byte(bc-1)).String()
	}
	return map[string]any{"id": s.newID(), "name": "default", "ipRange": start + "-" + end, "size": mask, "addressSpace": cidr, "vnetId": it.ID}
}

func (s *Server) markDeleted(kind, name string) {
	it, exists := s.store[kind][name]
	if !exists || it.deleting >= 0 {
		return
	}
	it.deleting = s.opt.HiddenReads
	if it.deleting == 0 {
		delete(s.store[kind], name)
	}
}

func (s *Server) list(w http.ResponseWriter, kind, message string, render func(*item) map[string]any) {
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
	resp := map[string]any{"data": out}
	if message != "" {
		resp["message"] = message
	}
	writeJSON(w, 200, resp)
}

func knownImage(id string) bool {
	for _, i := range Images {
		if i["documentId"] == id {
			return true
		}
	}
	return false
}

func isUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}

func oneOf(v string, options ...string) bool {
	for _, o := range options {
		if v == o {
			return true
		}
	}
	return false
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
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
