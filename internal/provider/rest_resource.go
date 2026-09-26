package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// A small declarative engine for resources whose API is plain REST: a create call, a
// read (by id, or by finding the item in a list), an optional update and a delete.
// Most of the platform looks like that, and writing each one out by hand is how the
// provider fell behind the API. A resource here is a restSpec: its paths and fields.

type fieldKind int

const (
	fString fieldKind = iota
	fInt
	fFloat
	fBool
	fStringList
	fStringMap
	// fJSON holds nested configuration as a JSON document (write it with jsonencode()).
	// Compared semantically, so key order and whitespace never show as a diff.
	fJSON
)

type restField struct {
	Attr, JSON  string
	Kind        fieldKind
	Required    bool
	Optional    bool
	Computed    bool
	ForceNew    bool
	Sensitive   bool
	WriteOnly   bool // sent, never read back (passwords, secret values)
	NoUpdate    bool // not sent on update
	PathOnly    bool // used in the URL, not the body
	Default     any
	Description string
}

type restSpec struct {
	Description  string
	CreateMethod string // default POST
	CreatePath   string
	// Read either by id...
	ReadPath string
	// ...or by finding the item in a list, matching ListMatch (JSON key) to the id,
	// or to the attribute named by ListMatchAttr when the id is composite.
	ListPath      string
	ListField     string // list under this key of the answer ("rules"), when not the answer itself
	ListMatch     string // default "id"
	ListMatchAttr string
	UpdateMethod  string // "" = every field forces a new resource
	UpdatePath    string
	DeleteMethod  string // default DELETE
	DeletePath    string
	IDKey         string // JSON key of the new id in the create answer (default "id")
	// IDFormat composes the id from attributes, e.g. "{account_id}/{name}". Import
	// parses an id in the same shape back into those attributes.
	IDFormat string
	// UpdateIDKey sends the id in the update body under this key (for upserts).
	UpdateIDKey string
	// DefaultRegion fills the "region" attribute, when the configuration leaves it
	// out, with the provider's first region (or the API's first available one).
	DefaultRegion bool
	Fields        []restField
}

var placeholder = regexp.MustCompile(`\{([a-z0-9_]+)\}`)

func (s *restSpec) resource() *schema.Resource {
	sch := map[string]*schema.Schema{}
	for _, f := range s.Fields {
		sc := &schema.Schema{
			Required: f.Required, Optional: f.Optional, Computed: f.Computed,
			ForceNew:  f.ForceNew || (s.UpdateMethod == "" && !f.Computed) || (f.NoUpdate && !f.Computed),
			Sensitive: f.Sensitive, Description: f.Description, Default: f.Default,
		}
		if f.Computed && !f.Optional && !f.Required {
			sc.ForceNew = false
		}
		switch f.Kind {
		case fString:
			sc.Type = schema.TypeString
		case fInt:
			sc.Type = schema.TypeInt
		case fFloat:
			sc.Type = schema.TypeFloat
		case fBool:
			sc.Type = schema.TypeBool
		case fStringList:
			sc.Type = schema.TypeList
			sc.Elem = &schema.Schema{Type: schema.TypeString}
			// Always read back (as [] when absent), so safe to accept the API's value.
			sc.Computed = sc.Computed || (f.Optional && f.Default == nil)
		case fStringMap:
			sc.Type = schema.TypeMap
			sc.Elem = &schema.Schema{Type: schema.TypeString}
			sc.Computed = sc.Computed || (f.Optional && f.Default == nil)
		case fJSON:
			sc.Type = schema.TypeString
			if f.Optional || f.Required {
				sc.DiffSuppressFunc = jsonEquivalent
				sc.ValidateFunc = validJSON
			}
		}
		sch[f.Attr] = sc
	}
	r := &schema.Resource{
		Description:   s.Description,
		Schema:        sch,
		CreateContext: s.create,
		ReadContext:   s.read,
		DeleteContext: s.delete,
		Importer:      &schema.ResourceImporter{StateContext: s.importState},
	}
	if s.UpdateMethod != "" {
		r.UpdateContext = s.update
	}
	return r
}

// ── paths and ids ─────────────────────────────────────────────────────────────

func (s *restSpec) render(tmpl string, d *schema.ResourceData) string {
	return placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		key := m[1 : len(m)-1]
		if key == "id" {
			return url.PathEscape(d.Id())
		}
		return url.PathEscape(fmt.Sprint(d.Get(key)))
	})
}

func (s *restSpec) importState(_ context.Context, d *schema.ResourceData, _ any) ([]*schema.ResourceData, error) {
	if s.IDFormat == "" {
		return []*schema.ResourceData{d}, nil
	}
	names := placeholder.FindAllStringSubmatch(s.IDFormat, -1)
	parts := strings.SplitN(d.Id(), "/", len(names))
	if len(parts) != len(names) {
		return nil, fmt.Errorf("expected an id like %q, got %q", s.IDFormat, d.Id())
	}
	for i, n := range names {
		if n[1] != "id" {
			if err := d.Set(n[1], parts[i]); err != nil {
				return nil, err
			}
		}
	}
	return []*schema.ResourceData{d}, nil
}

// ── payloads ──────────────────────────────────────────────────────────────────

func (s *restSpec) payload(d *schema.ResourceData, forUpdate bool) (map[string]any, error) {
	body := map[string]any{}
	for _, f := range s.Fields {
		if f.PathOnly || f.JSON == "" || (f.Computed && !f.Optional && !f.Required) || (forUpdate && f.NoUpdate) {
			continue
		}
		v, set := d.GetOk(f.Attr)
		if !set && f.Default == nil && f.Kind != fBool {
			continue
		}
		if !set {
			v = d.Get(f.Attr)
		}
		switch f.Kind {
		case fStringList:
			body[f.JSON] = stringList(d, f.Attr)
		case fStringMap:
			m := map[string]string{}
			for k, val := range v.(map[string]any) {
				m[k] = fmt.Sprint(val)
			}
			body[f.JSON] = m
		case fJSON:
			var parsed any
			if err := json.Unmarshal([]byte(v.(string)), &parsed); err != nil {
				return nil, fmt.Errorf("%s: %w", f.Attr, err)
			}
			body[f.JSON] = parsed
		default:
			body[f.JSON] = v
		}
	}
	return body, nil
}

// unwrap takes the {"data": ...} envelope off an answer, when there is one.
func unwrap(raw any) any {
	if m, ok := raw.(map[string]any); ok {
		if inner, ok := m["data"]; ok && inner != nil {
			return inner
		}
	}
	return raw
}

func (s *restSpec) apply(d *schema.ResourceData, obj map[string]any) {
	for _, f := range s.Fields {
		if f.WriteOnly || f.PathOnly || f.JSON == "" {
			continue
		}
		v, ok := obj[f.JSON]
		if !ok {
			// The API spells some keys with different case.
			for k, val := range obj {
				if strings.EqualFold(k, f.JSON) {
					v, ok = val, true
					break
				}
			}
		}
		if !ok || v == nil {
			// An absent list or map is an empty one; left unset it stays "known after
			// apply" and every plan shows a change nobody made.
			switch f.Kind {
			case fStringList:
				_ = d.Set(f.Attr, []string{})
			case fStringMap:
				_ = d.Set(f.Attr, map[string]string{})
			}
			continue
		}
		switch f.Kind {
		case fString:
			_ = d.Set(f.Attr, fmt.Sprint(v))
		case fInt:
			if n, ok := v.(float64); ok {
				_ = d.Set(f.Attr, int(n))
			}
		case fFloat:
			if n, ok := v.(float64); ok {
				_ = d.Set(f.Attr, n)
			}
		case fBool:
			if b, ok := v.(bool); ok {
				_ = d.Set(f.Attr, b)
			}
		case fStringList:
			if list, ok := v.([]any); ok {
				out := make([]string, 0, len(list))
				for _, item := range list {
					out = append(out, fmt.Sprint(item))
				}
				_ = d.Set(f.Attr, out)
			}
		case fStringMap:
			if m, ok := v.(map[string]any); ok {
				out := map[string]string{}
				for k, val := range m {
					out[k] = fmt.Sprint(val)
				}
				_ = d.Set(f.Attr, out)
			}
		case fJSON:
			if raw, err := json.Marshal(v); err == nil {
				_ = d.Set(f.Attr, string(raw))
			}
		}
	}
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

func (s *restSpec) create(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	body, err := s.payload(d, false)
	if err != nil {
		return diag.FromErr(err)
	}
	if s.DefaultRegion {
		region, err := resolveRegion(ctx, d, c)
		if err != nil {
			return diag.FromErr(err)
		}
		for _, f := range s.Fields {
			if f.Attr == "region" {
				body[f.JSON] = region
			}
		}
		_ = d.Set("region", region)
	}
	method := s.CreateMethod
	if method == "" {
		method = "POST"
	}
	var raw any
	if err := c.Do(context.Background(), method, s.render(s.CreatePath, d), body, &raw); err != nil {
		return diag.FromErr(err)
	}

	if s.IDFormat != "" {
		d.SetId(placeholder.ReplaceAllStringFunc(s.IDFormat, func(m string) string {
			return fmt.Sprint(d.Get(m[1 : len(m)-1]))
		}))
	} else {
		key := s.IDKey
		if key == "" {
			key = "id"
		}
		obj, _ := unwrap(raw).(map[string]any)
		id := ""
		if obj != nil {
			if v, ok := obj[key]; ok && v != nil {
				id = fmt.Sprint(v)
			}
		}
		if id == "" {
			return diag.Errorf("the API accepted the request but returned no %q", key)
		}
		d.SetId(id)
	}
	// Some values exist only in the create answer — a secret or an agent key shown
	// once — so take everything it carries before reading back.
	if obj, ok := unwrap(raw).(map[string]any); ok {
		s.apply(d, obj)
	}
	return s.read(ctx, d, meta)
}

func (s *restSpec) fetch(d *schema.ResourceData, c *client.Client) (map[string]any, error) {
	if s.ReadPath != "" {
		var raw any
		if err := c.Do(context.Background(), "GET", s.render(s.ReadPath, d), nil, &raw); err != nil {
			return nil, err
		}
		obj, _ := unwrap(raw).(map[string]any)
		if obj == nil {
			return nil, &client.APIError{Method: "GET", Path: s.ReadPath, StatusCode: 404, Message: "empty answer"}
		}
		return obj, nil
	}

	var raw any
	if err := c.Do(context.Background(), "GET", s.render(s.ListPath, d), nil, &raw); err != nil {
		return nil, err
	}
	inner := unwrap(raw)
	if s.ListField != "" {
		if m, ok := inner.(map[string]any); ok {
			inner = m[s.ListField]
		}
	}
	list, _ := inner.([]any)
	match := s.ListMatch
	if match == "" {
		match = "id"
	}
	want := d.Id()
	if s.ListMatchAttr != "" {
		want = fmt.Sprint(d.Get(s.ListMatchAttr))
	}
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range obj {
			if strings.EqualFold(k, match) && strings.EqualFold(fmt.Sprint(v), want) {
				return obj, nil
			}
		}
	}
	return nil, &client.APIError{Method: "GET", Path: s.ListPath, StatusCode: 404, Message: "not in the list"}
}

func (s *restSpec) read(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	obj, err := s.fetch(d, c)
	if err != nil {
		if client.IsNotFound(err) {
			d.SetId("")
			return nil
		}
		return diag.FromErr(err)
	}
	// Some reads by id still answer for a soft-deleted item (storage accounts do).
	if strings.EqualFold(fmt.Sprint(obj["status"]), "deleted") {
		d.SetId("")
		return nil
	}
	s.apply(d, obj)
	return nil
}

func (s *restSpec) update(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	body, err := s.payload(d, true)
	if err != nil {
		return diag.FromErr(err)
	}
	if s.UpdateIDKey != "" {
		body[s.UpdateIDKey] = d.Id()
	}
	if err := c.Do(context.Background(), s.UpdateMethod, s.render(s.UpdatePath, d), body, nil); err != nil {
		return diag.FromErr(err)
	}
	return s.read(ctx, d, meta)
}

func (s *restSpec) delete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	method := s.DeleteMethod
	if method == "" {
		method = "DELETE"
	}
	if err := c.Do(context.Background(), method, s.render(s.DeletePath, d), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── JSON attributes ───────────────────────────────────────────────────────────

func jsonEquivalent(_, old, new string, _ *schema.ResourceData) bool {
	if old == new {
		return true
	}
	var stored, config any
	if json.Unmarshal([]byte(old), &stored) != nil || json.Unmarshal([]byte(new), &config) != nil {
		return false
	}
	return covers(normaliseJSON(stored), normaliseJSON(config))
}

// covers reports whether what the API stored says everything the configuration
// does. The API adds keys of its own (an id, a resolved name, a default); those are
// not a change the configuration asked for, so they are not a diff.
func covers(stored, config any) bool {
	switch c := config.(type) {
	case map[string]any:
		s, ok := stored.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range c {
			if !covers(s[k], v) {
				return false
			}
		}
		return true
	case []any:
		s, ok := stored.([]any)
		if !ok || len(s) != len(c) {
			return false
		}
		for i := range c {
			if !covers(s[i], c[i]) {
				return false
			}
		}
		return true
	case float64:
		n, ok := stored.(float64)
		return ok && n == c
	}
	return reflect.DeepEqual(stored, config)
}

// normaliseJSON drops nulls, which the API fills in for fields a configuration leaves out.
func normaliseJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			if val != nil {
				out[strings.ToLower(k)] = normaliseJSON(val)
			}
		}
		return out
	case []any:
		for i := range t {
			t[i] = normaliseJSON(t[i])
		}
		return t
	}
	return v
}

func validJSON(v any, key string) ([]string, []error) {
	var parsed any
	if err := json.Unmarshal([]byte(v.(string)), &parsed); err != nil {
		return nil, []error{fmt.Errorf("%s must be JSON (use jsonencode()): %v", key, err)}
	}
	return nil, nil
}

// Helpers for field declarations.
func str(attr, jsonKey, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: fString, Optional: true, Description: desc}
}
func reqStr(attr, jsonKey, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: fString, Required: true, Description: desc}
}
func num(attr, jsonKey, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: fInt, Optional: true, Description: desc}
}
func flt(attr, jsonKey, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: fFloat, Optional: true, Description: desc}
}
func boolean(attr, jsonKey string, def bool, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: fBool, Optional: true, Default: def, Description: desc}
}
func strList(attr, jsonKey, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: fStringList, Optional: true, Description: desc}
}
func strMap(attr, jsonKey, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: fStringMap, Optional: true, Description: desc}
}
func jsonField(attr, jsonKey, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: fJSON, Optional: true, Description: desc}
}
func computed(attr, jsonKey string, kind fieldKind, desc string) restField {
	return restField{Attr: attr, JSON: jsonKey, Kind: kind, Computed: true, Description: desc}
}
func parent(attr, desc string) restField {
	return restField{Attr: attr, Kind: fString, Required: true, ForceNew: true, PathOnly: true, Description: desc}
}

func (f restField) forceNew() restField  { f.ForceNew = true; return f }
func (f restField) required() restField  { f.Required, f.Optional = true, false; return f }
func (f restField) sensitive() restField { f.Sensitive, f.WriteOnly = true, true; return f }
func (f restField) noUpdate() restField  { f.NoUpdate = true; return f }
func (f restField) withDefault(v any) restField {
	f.Default = v
	return f
}

var _ = strconv.Itoa
