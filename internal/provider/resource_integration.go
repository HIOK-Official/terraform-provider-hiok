package provider

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// resourceIntegration is a Slack channel, Jira project or webhook that hears about
// the account's resources: alerts firing and recovering, Container Jobs failing.
//
// The API keeps one secret per integration — a Slack incoming-webhook URL, a Jira
// API token or a webhook signing secret — and never returns it. Each kind's secret
// is its own attribute here, so a configuration says what it holds; it is sent on
// create and whenever it changes, and otherwise left alone.
func resourceIntegration() *schema.Resource {
	jiraKey := regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,9}$`)
	return &schema.Resource{
		Description: "A Slack, Jira or webhook integration: where alerts that fire and recover, " +
			"and Container Jobs that fail, are delivered.",
		CreateContext: integrationCreate,
		ReadContext:   integrationRead,
		UpdateContext: integrationUpdate,
		DeleteContext: integrationDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"kind": {
				Type: schema.TypeString, Required: true, ForceNew: true,
				ValidateFunc: validation.StringInSlice([]string{"slack", "jira", "webhook"}, false),
				Description:  "slack, jira or webhook. Changing it replaces the integration.",
			},
			"name": {Type: schema.TypeString, Required: true, ValidateFunc: validation.StringLenBetween(1, 128),
				Description: "Display name."},
			"events": {
				Type: schema.TypeList, Optional: true, Computed: true,
				Elem: &schema.Schema{Type: schema.TypeString,
					ValidateFunc: validation.StringInSlice([]string{"alert.fired", "alert.resolved", "job.failed"}, false)},
				Description: "Events delivered: alert.fired, alert.resolved, job.failed. Defaults to all three.",
			},
			"enabled": {Type: schema.TypeBool, Optional: true, Default: true,
				Description: "Deliver events. A disabled integration keeps its settings."},

			// Slack
			"slack_webhook_url": {Type: schema.TypeString, Optional: true, Sensitive: true,
				Description: "Slack incoming-webhook URL (https://hooks.slack.com/…). Required for kind = slack."},
			"slack_channel": {Type: schema.TypeString, Optional: true,
				Description: "Channel name, for display only; the webhook decides where messages go."},

			// Jira
			"jira_site": {Type: schema.TypeString, Optional: true,
				StateFunc:   func(v any) string { return strings.TrimRight(v.(string), "/") },
				Description: "Jira Cloud site, e.g. https://your-site.atlassian.net. Required for kind = jira."},
			"jira_email": {Type: schema.TypeString, Optional: true,
				Description: "The Atlassian account the API token belongs to. Required for kind = jira."},
			"jira_project_key": {Type: schema.TypeString, Optional: true,
				ValidateFunc: validation.StringMatch(jiraKey, "a Jira project key in capitals, e.g. OPS"),
				Description:  "Project issues are opened in. Required for kind = jira."},
			"jira_issue_type": {Type: schema.TypeString, Optional: true, Default: "Task",
				Description: "Issue type opened for a firing alert."},
			"jira_api_token": {Type: schema.TypeString, Optional: true, Sensitive: true,
				Description: "Atlassian API token (id.atlassian.com → Security → API tokens). Required for kind = jira."},

			// Webhook
			"webhook_url": {Type: schema.TypeString, Optional: true,
				Description: "HTTPS endpoint on a public address. Required for kind = webhook."},
			"webhook_secret": {Type: schema.TypeString, Optional: true, Sensitive: true,
				Description: "Signs each delivery: HMAC-SHA256 of the body, hex, in X-Hiok-Signature."},

			"has_secret":       {Type: schema.TypeBool, Computed: true, Description: "Whether the API holds a secret for it."},
			"last_status":      {Type: schema.TypeString, Computed: true, Description: "Outcome of the last delivery."},
			"last_delivery_at": {Type: schema.TypeString, Computed: true, Description: "When the last delivery was attempted."},
			"created_at":       {Type: schema.TypeString, Computed: true, Description: "When the integration was created."},
		},
		CustomizeDiff: func(_ context.Context, d *schema.ResourceDiff, _ any) error {
			// Say what a kind needs at plan time, not after the API refuses it. A
			// value only known after apply (another resource's output) is let through.
			kind := d.Get("kind").(string)
			need := map[string][]string{
				"slack":   {"slack_webhook_url"},
				"jira":    {"jira_site", "jira_email", "jira_project_key", "jira_api_token"},
				"webhook": {"webhook_url"},
			}[kind]
			for _, attr := range need {
				// An imported integration keeps the secret the API already holds.
				if d.Id() != "" && attr == integrationSecretAttr(kind) {
					continue
				}
				// Read from the configuration itself: optional fields accept the
				// server's value (acceptServerDefaults), so the planned value of one
				// left out is "unknown", not empty.
				if v := d.GetRawConfig().GetAttr(attr); v.IsKnown() && (v.IsNull() || v.AsString() == "") {
					return fmt.Errorf("%s is required for kind = %q", attr, kind)
				}
			}
			return nil
		},
	}
}

type integration struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Name           string         `json:"name"`
	Enabled        bool           `json:"enabled"`
	Events         []string       `json:"events"`
	Config         map[string]any `json:"config"`
	HasSecret      bool           `json:"hasSecret"`
	LastStatus     string         `json:"lastStatus"`
	LastDeliveryAt string         `json:"lastDeliveryAt"`
	CreatedAt      string         `json:"createdAt"`
}

// integrationSecretAttr names the attribute that holds a kind's secret.
func integrationSecretAttr(kind string) string {
	switch kind {
	case "slack":
		return "slack_webhook_url"
	case "jira":
		return "jira_api_token"
	default:
		return "webhook_secret"
	}
}

func integrationPayload(d *schema.ResourceData, creating bool) map[string]any {
	kind := d.Get("kind").(string)
	body := map[string]any{
		"kind":    kind,
		"name":    d.Get("name").(string),
		"enabled": d.Get("enabled").(bool),
	}
	if events := stringList(d, "events"); len(events) > 0 {
		body["events"] = events
	}
	switch kind {
	case "slack":
		body["config"] = map[string]any{"channel": d.Get("slack_channel").(string)}
	case "jira":
		body["config"] = map[string]any{
			"site":       strings.TrimRight(d.Get("jira_site").(string), "/"),
			"email":      d.Get("jira_email").(string),
			"projectKey": d.Get("jira_project_key").(string),
			"issueType":  d.Get("jira_issue_type").(string),
		}
	case "webhook":
		body["config"] = map[string]any{"url": d.Get("webhook_url").(string)}
	}
	secret := integrationSecretAttr(kind)
	if v := d.Get(secret).(string); v != "" && (creating || d.HasChange(secret)) {
		body["secret"] = v
	}
	return body
}

func integrationApply(d *schema.ResourceData, i *integration) {
	str := func(key string) string {
		if v, ok := i.Config[key]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	_ = d.Set("kind", i.Kind)
	_ = d.Set("name", i.Name)
	_ = d.Set("enabled", i.Enabled)
	_ = d.Set("events", i.Events)
	_ = d.Set("has_secret", i.HasSecret)
	_ = d.Set("last_status", i.LastStatus)
	_ = d.Set("last_delivery_at", i.LastDeliveryAt)
	_ = d.Set("created_at", i.CreatedAt)
	switch i.Kind {
	case "slack":
		_ = d.Set("slack_channel", str("channel"))
	case "jira":
		_ = d.Set("jira_site", str("site"))
		_ = d.Set("jira_email", str("email"))
		_ = d.Set("jira_project_key", str("projectKey"))
		_ = d.Set("jira_issue_type", str("issueType"))
	case "webhook":
		_ = d.Set("webhook_url", str("url"))
	}
}

func integrationCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var resp struct {
		Data integration `json:"data"`
	}
	if err := c.Do(ctx, "POST", "/api/integrations", integrationPayload(d, true), &resp); err != nil {
		return diag.FromErr(err)
	}
	if resp.Data.ID == "" {
		return diag.Errorf("the API accepted the request but returned no integration")
	}
	d.SetId(resp.Data.ID)
	return integrationRead(ctx, d, meta)
}

func integrationRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var resp struct {
		Data integration `json:"data"`
	}
	if err := c.Do(ctx, "GET", "/api/integrations/"+url.PathEscape(d.Id()), nil, &resp); err != nil {
		if client.IsNotFound(err) {
			d.SetId("")
			return nil
		}
		return diag.FromErr(err)
	}
	integrationApply(d, &resp.Data)
	return nil
}

func integrationUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(ctx, "PUT", "/api/integrations/"+url.PathEscape(d.Id()), integrationPayload(d, false), nil); err != nil {
		return diag.FromErr(err)
	}
	return integrationRead(ctx, d, meta)
}

func integrationDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(ctx, "DELETE", "/api/integrations/"+url.PathEscape(d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	return nil
}
