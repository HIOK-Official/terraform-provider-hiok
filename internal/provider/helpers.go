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

// usableRange converts an IPv4 CIDR to the API's subnet form: the usable
// range (network+2 .. broadcast-1, as the HIOK console computes it) and the
// prefix length, e.g. 10.0.1.0/24 -> "10.0.1.2-10.0.1.254", "24".
func usableRange(cidr string) (ipRange, size string, err error) {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	ip := n.IP.To4()
	if ip == nil {
		return "", "", fmt.Errorf("subnet_cidr %q must be IPv4", cidr)
	}
	ones, bits := n.Mask.Size()
	if bits-ones < 2 {
		return "", "", fmt.Errorf("subnet_cidr %q is too small; use /30 or larger", cidr)
	}
	start := ipToUint(ip) + 2
	end := ipToUint(ip) | ^ipToUint(net.IP(n.Mask).To4())
	end--
	if start > end {
		start = end
	}
	return uintToIP(start) + "-" + uintToIP(end), fmt.Sprint(ones), nil
}

func ipToUint(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uintToIP(v uint32) string {
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
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
		Pending:      []string{pending, "unavailable"},
		Target:       []string{target},
		Timeout:      timeout,
		PollInterval: pollInterval,
		MinTimeout:   pollInterval,
		// Require two consecutive matching reads so an eventually consistent
		// list cannot flip back straight after we return.
		ContinuousTargetOccurence: 2,
		Refresh: func() (any, string, error) {
			found, err := find(ctx, c, name)
			if client.IsTransient(err) {
				// The API is briefly unavailable (seen as minutes of 502s on
				// the live platform); keep waiting until the timeout.
				return name, "unavailable", nil
			}
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
			return fmt.Errorf("%q was not ready within %s: %w", name, timeout, err)
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
		for _, k := range keys {
			if !d.HasChange(k) {
				continue
			}
			old, _ := d.GetChange(k)
			if imported && isZero(old) {
				continue // recorded in place by the update
			}
			if err := d.ForceNew(k); err != nil {
				return err
			}
		}
		// Clear the flag on the first apply after import even when there is
		// nothing to record; otherwise a later real change to a still-empty
		// attribute would be recorded silently instead of replacing.
		if imported {
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

// resolveRegion picks the resource's region (its own `region`, else the
// provider's first region, else the first available one from the API) and
// checks it exists, so a typo fails in seconds instead of hanging the API.
func resolveRegion(ctx context.Context, d *schema.ResourceData, c *client.Client) (string, error) {
	region := ""
	if r, ok := d.GetOk("region"); ok {
		region = r.(string)
	} else if len(c.Regions) > 0 {
		region = c.Regions[0]
	} else {
		var err error
		if region, err = c.DefaultRegion(ctx); err != nil {
			return "", err
		}
	}
	if err := c.CheckRegion(ctx, region); err != nil {
		return "", err
	}
	return region, nil
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
