package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// Data sources read by one GET: the answer's fields become attributes.
type dataSpec struct {
	Description string
	Path        string            // may use {attr} from the arguments
	Args        []string          // required string arguments
	Fields      map[string]string // attribute -> JSON key of the answer
	Sensitive   map[string]bool
	ListAttr    string // when the answer is a list: stored as JSON in this attribute
}

func (s dataSpec) resource() *schema.Resource {
	sch := map[string]*schema.Schema{}
	for _, a := range s.Args {
		sch[a] = &schema.Schema{Type: schema.TypeString, Required: true}
	}
	for a := range s.Fields {
		sch[a] = &schema.Schema{Type: schema.TypeString, Computed: true, Sensitive: s.Sensitive[a]}
	}
	if s.ListAttr != "" {
		sch[s.ListAttr] = &schema.Schema{Type: schema.TypeString, Computed: true,
			Description: "The list as JSON: use jsondecode()."}
	}
	return &schema.Resource{
		Description: s.Description,
		Schema:      sch,
		ReadContext: func(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			c := meta.(*client.Client)
			path := placeholder.ReplaceAllStringFunc(s.Path, func(m string) string {
				return url.PathEscape(fmt.Sprint(d.Get(m[1 : len(m)-1])))
			})
			var raw any
			if err := c.Do(context.Background(), "GET", path, nil, &raw); err != nil {
				return diag.FromErr(err)
			}
			inner := unwrap(raw)
			if s.ListAttr != "" {
				b, _ := json.Marshal(inner)
				_ = d.Set(s.ListAttr, string(b))
			}
			if obj, ok := inner.(map[string]any); ok {
				for attr, key := range s.Fields {
					if v, ok := obj[key]; ok && v != nil {
						_ = d.Set(attr, fmt.Sprint(v))
					}
				}
			}
			d.SetId(path)
			return nil
		},
	}
}

func catalogDataSources() map[string]*schema.Resource {
	specs := map[string]dataSpec{
		"hiok_storage_account_keys": {
			Description: "A storage account's two access keys: the password for its file shares (WebDAV, SFTP) and " +
				"the storage key for the SDKs.",
			Path: "/api/StorageAccount/{account_id}/keys", Args: []string{"account_id"},
			Fields:    map[string]string{"key1": "key1", "key2": "key2", "key1_scope": "key1Scope", "key2_scope": "key2Scope"},
			Sensitive: map[string]bool{"key1": true, "key2": true},
		},
		"hiok_key_vault_secret": {
			Description: "Reads the current value of a key vault item.",
			Path:        "/api/KeyVault/{vault_id}/items/{name}", Args: []string{"vault_id", "name"},
			Fields:    map[string]string{"value": "value", "version": "version", "content_type": "contentType"},
			Sensitive: map[string]bool{"value": true},
		},
		"hiok_container_registry_credentials": {
			Description: "Login server and admin credentials of a container registry (for docker login and CI).",
			Path:        "/api/container-registry/{registry_id}/credentials", Args: []string{"registry_id"},
			Fields:    map[string]string{"login_server": "loginServer", "username": "username", "password": "password"},
			Sensitive: map[string]bool{"password": true},
		},
		"hiok_roles": {
			Description: "Roles that can be assigned with hiok_role_assignment.",
			Path:        "/api/access-control/roles", ListAttr: "roles_json",
		},
		"hiok_resource_groups": {
			Description: "Your resource groups.",
			Path:        "/api/resourcegroups", ListAttr: "resource_groups_json",
		},
	}
	out := map[string]*schema.Resource{}
	for name, spec := range specs {
		out[name] = spec.resource()
	}
	return out
}
