package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// resourceIdGroupMember puts a person (by email) or a service principal (by client
// id) in a HIOK ID group. The membership has no id of its own, so the resource's id
// is "<group_id>/<kind>/<ref>", which is also what import takes.
func resourceIdGroupMember() *schema.Resource {
	return &schema.Resource{
		Description:   "A person or service principal in a HIOK ID group (and so holding the group's role).",
		CreateContext: groupMemberCreate,
		ReadContext:   groupMemberRead,
		DeleteContext: groupMemberDelete,
		Importer: &schema.ResourceImporter{StateContext: func(_ context.Context, d *schema.ResourceData, _ any) ([]*schema.ResourceData, error) {
			parts := strings.SplitN(d.Id(), "/", 3)
			if len(parts) != 3 {
				return nil, fmt.Errorf("import as <group_id>/<user|serviceprincipal>/<email-or-client-id>")
			}
			_ = d.Set("group_id", parts[0])
			_ = d.Set("kind", parts[1])
			_ = d.Set("member", parts[2])
			return []*schema.ResourceData{d}, nil
		}},
		Schema: map[string]*schema.Schema{
			"group_id": {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The hiok_id_group's id."},
			"kind": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "user",
				ValidateFunc: validation.StringInSlice([]string{"user", "serviceprincipal"}, false),
				Description:  "user (member is an email) or serviceprincipal (member is a client id)."},
			"member": {Type: schema.TypeString, Required: true, ForceNew: true,
				Description: "An email already invited with hiok_id_member, or a service principal's client_id."},
		},
	}
}

func groupMemberCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	g, k := d.Get("group_id").(string), d.Get("kind").(string)
	ref := d.Get("member").(string)
	if k == "user" {
		ref = strings.ToLower(ref)
	}
	if err := c.Do(ctx, "POST", "/api/hiok-id/groups/"+url.PathEscape(g)+"/members", map[string]any{"kind": k, "ref": ref}, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(g + "/" + k + "/" + ref)
	return groupMemberRead(ctx, d, meta)
}

func groupMemberRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var resp struct {
		Data []struct {
			ID      string `json:"id"`
			Members []struct {
				Kind string `json:"kind"`
				Ref  string `json:"ref"`
			} `json:"members"`
		} `json:"data"`
	}
	if err := c.Do(ctx, "GET", "/api/hiok-id/groups", nil, &resp); err != nil {
		return diag.FromErr(err)
	}
	parts := strings.SplitN(d.Id(), "/", 3)
	for _, g := range resp.Data {
		if g.ID != parts[0] {
			continue
		}
		for _, m := range g.Members {
			if m.Kind == parts[1] && strings.EqualFold(m.Ref, parts[2]) {
				return nil
			}
		}
	}
	d.SetId("")
	return nil
}

func groupMemberDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	parts := strings.SplitN(d.Id(), "/", 3)
	err := c.Do(ctx, "DELETE", "/api/hiok-id/groups/"+url.PathEscape(parts[0])+"/members/"+parts[1]+"/"+url.PathEscape(parts[2]), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	return nil
}
