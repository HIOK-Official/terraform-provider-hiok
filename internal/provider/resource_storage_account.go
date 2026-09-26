package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

func resourceStorageAccount() *schema.Resource {
	spec := &restSpec{
		Description: "A storage account: blob containers, file shares, filesystems, queues and tables, " +
			"replicated to the regions you choose. Its id is the account's GUID, which child resources " +
			"(hiok_storage_container, hiok_storage_queue, …) take as account_id.",
		CreatePath: "/api/StorageAccount", ReadPath: "/api/StorageAccount/{id}",
		UpdateMethod: "PUT", UpdatePath: "/api/StorageAccount/{id}", DeletePath: "/api/StorageAccount/{id}",
		DefaultRegion: true,
		Fields: append([]restField{
			reqStr("name", "name", "3-24 lowercase letters and digits.").forceNew(),
			str("display_name", "displayName", ""),
			str("description", "description", ""),
			restField{Attr: "region", JSON: "primaryRegion", Kind: fString, Optional: true, Computed: true, ForceNew: true,
				Description: "Primary region (default: the provider's first region)."},
			strList("replica_regions", "replicaRegions", "Regions holding a replica."),
			str("preferred_read_region", "preferredReadRegion", "auto (default) or a region id."),
			str("consistency_mode", "consistencyMode", "strong, bounded, session (default) or eventual.").withDefault("session"),
			num("max_staleness_ms", "maxStalenessMs", "For bounded consistency."),
			str("write_acknowledgement", "writeAcknowledgement", "primary, quorum (default) or all.").withDefault("quorum"),
			str("tier", "storageTier", "hot (default), cool or archive.").withDefault("hot"),
			str("redundancy", "redundancy", "LRS (default), ZRS or GRS.").withDefault("LRS"),
			num("quota_bytes", "quotaBytes", "Capacity limit in bytes (default 10 GiB)."),
			computed("account_id", "id", fString, "The account's GUID (same as id; kept for configurations written for 0.1)."),
			computed("status", "status", fString, "Provisioning state."),
			computed("used_bytes", "usedBytes", fInt, ""),
			computed("primary_endpoint", "primaryEndpoint", fString, "The account's data-plane URL."),
		}, placement()...),
	}
	r := spec.resource()
	r.Schema["tier"].ValidateFunc = validation.StringInSlice(storageTiers, false)
	r.Schema["redundancy"].ValidateFunc = validation.StringInSlice(storageRedundancy, true)
	r.Schema["redundancy"].DiffSuppressFunc = func(_, o, n string, _ *schema.ResourceData) bool { return strings.EqualFold(o, n) }

	// Import takes the GUID or, more conveniently, the account's name.
	r.Importer = &schema.ResourceImporter{StateContext: func(ctx context.Context, d *schema.ResourceData, meta any) ([]*schema.ResourceData, error) {
		if !uuidRegexp.MatchString(d.Id()) {
			s, err := findStorage(ctx, meta.(*client.Client), d.Id())
			if err != nil {
				return nil, err
			}
			if s == nil {
				return nil, fmt.Errorf("no storage account named %q", d.Id())
			}
			d.SetId(s.ID)
		}
		return []*schema.ResourceData{d}, nil
	}}

	// Version 0 used the account's name as its id, which the API does not accept
	// anywhere — deleting it failed and no child resource could refer to it.
	r.SchemaVersion = 1
	r.StateUpgraders = []schema.StateUpgrader{{
		Version: 0,
		Type:    r.CoreConfigSchema().ImpliedType(),
		Upgrade: func(_ context.Context, state map[string]any, meta any) (map[string]any, error) {
			c := meta.(*client.Client)
			var resp struct {
				Data []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"data"`
			}
			if err := c.Do(context.Background(), "GET", "/api/StorageAccount", nil, &resp); err != nil {
				return nil, err
			}
			name := fmt.Sprint(state["id"])
			for _, a := range resp.Data {
				if a.Name == name {
					state["id"] = a.ID
					return state, nil
				}
			}
			return state, nil
		},
	}}
	return r
}

var uuidRegexp = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
