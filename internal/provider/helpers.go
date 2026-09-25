package provider

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// pollInterval is how often create/delete waits re-check the API. Tests shrink it.
var pollInterval = pollIntervalLive

const pollIntervalLive = 5 * time.Second

// nameRegexp keeps names safe to put in URLs, query strings and the
// "<owner>#<name>" scoping the API uses.
var nameRegexp = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

var validateName = validation.StringMatch(nameRegexp,
	"must start with a letter or digit and contain only letters, digits, '.', '_' or '-' (max 63 characters)")

var envRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// cidrWithin checks that inner is fully contained in outer.
func cidrWithin(inner, outer string) error {
	_, in, err := net.ParseCIDR(inner)
	if err != nil {
		return fmt.Errorf("invalid CIDR %q: %w", inner, err)
	}
	_, out, err := net.ParseCIDR(outer)
	if err != nil {
		return fmt.Errorf("invalid CIDR %q: %w", outer, err)
	}
	inOnes, _ := in.Mask.Size()
	outOnes, _ := out.Mask.Size()
	if !out.Contains(in.IP) || inOnes < outOnes {
		return fmt.Errorf("subnet_cidr %s is not inside address_space %s", inner, outer)
	}
	return nil
}

// finder looks a resource up by its visible name; found=false means absent.
type finder func(ctx context.Context, c *client.Client, name string) (found bool, err error)

// waitForPresence blocks until find reports the resource as present (want=true)
// or absent (want=false). HIOK provisions asynchronously, so a create call can
// return before the resource shows up in list calls, and a delete call before
// it is gone.
func waitForPresence(ctx context.Context, c *client.Client, name string, find finder, want bool, timeout time.Duration) error {
	target, pending := "present", "absent"
	if !want {
		target, pending = "absent", "present"
	}
	conf := &retry.StateChangeConf{
		Pending:      []string{pending},
		Target:       []string{target},
		Timeout:      timeout,
		PollInterval: pollInterval,
		MinTimeout:   pollInterval,
		// Require two consecutive matching reads so an eventually consistent
		// list cannot flip back straight after we return.
		ContinuousTargetOccurence: 2,
		Refresh: func() (any, string, error) {
			found, err := find(ctx, c, name)
			if err != nil {
				return nil, "", err
			}
			if found {
				return name, "present", nil
			}
			return name, "absent", nil
		},
	}
	if _, err := conf.WaitForStateContext(ctx); err != nil {
		if want {
			return fmt.Errorf("%q was accepted by the API but did not appear within %s: %w", name, timeout, err)
		}
		return fmt.Errorf("%q was not removed within %s: %w", name, timeout, err)
	}
	return nil
}

// importedSchema marks resources adopted with `terraform import`.
func importedSchema() *schema.Schema {
	return &schema.Schema{
		Type:     schema.TypeBool,
		Computed: true,
		Description: "True when the resource was adopted with `terraform import` and its create-time settings " +
			"have not been recorded yet. The next apply records them without calling the API.",
	}
}

// importState is the importer for every resource: the import ID is the name.
func importState(ctx context.Context, d *schema.ResourceData, _ any) ([]*schema.ResourceData, error) {
	if !nameRegexp.MatchString(d.Id()) {
		return nil, fmt.Errorf("invalid import ID %q: expected the resource name", d.Id())
	}
	_ = d.Set("imported", true)
	return []*schema.ResourceData{d}, nil
}

// createTimeOnly makes the given attributes behave like ForceNew, except
// right after an import. The API does not report these settings back, so an
// imported resource has nothing stored for them; forcing replacement would
// destroy the resource that was just imported. Instead the first apply
// records the configured values in state (no API call); from then on any
// change forces replacement as usual.
func createTimeOnly(keys ...string) schema.CustomizeDiffFunc {
	return func(_ context.Context, d *schema.ResourceDiff, _ any) error {
		if d.Id() == "" {
			return nil
		}
		imported := d.Get("imported").(bool)
		recording := false
		for _, k := range keys {
			if !d.HasChange(k) {
				continue
			}
			old, _ := d.GetChange(k)
			if imported && isZero(old) {
				recording = true
				continue
			}
			if err := d.ForceNew(k); err != nil {
				return err
			}
		}
		if recording {
			return d.SetNew("imported", false)
		}
		return nil
	}
}

func isZero(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case int:
		return t == 0
	case float64:
		return t == 0
	case bool:
		return !t
	case []any:
		return len(t) == 0
	}
	return false
}

// recordOnly is the Update for resources whose only updatable attributes are
// the create-time settings recorded after import: the values come from the
// plan, so there is nothing to send to the API.
func recordOnly(read schema.ReadContextFunc) schema.UpdateContextFunc {
	return func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
		_ = d.Set("imported", false)
		return read(ctx, d, meta)
	}
}

func regionsFor(d *schema.ResourceData, c *client.Client) []string {
	if r, ok := d.GetOk("region"); ok && r.(string) != "" {
		return []string{r.(string)}
	}
	return c.Regions
}

func firstRegion(d *schema.ResourceData, c *client.Client) string {
	if r := regionsFor(d, c); len(r) > 0 {
		return r[0]
	}
	return ""
}

func stringList(d *schema.ResourceData, key string) []string {
	raw := d.Get(key).([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
