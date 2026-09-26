package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// ── Service Bus namespace ───────────────────────────────────────────────────

func resourceServiceBusNamespace() *schema.Resource {
	return &schema.Resource{
		Description:   "A Hiok Service Bus namespace: the isolation boundary for queues and topics.",
		CreateContext: sbNamespaceCreate,
		ReadContext:   sbNamespaceRead,
		DeleteContext: sbNamespaceDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":        {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Namespace name."},
			"region":      {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Primary region."},
			"sku":         {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "standard", Description: "standard or premium."},
			"broker_kind": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "pulsar", Description: "pulsar for the managed broker, byo to bring your own."},
			"status":      {Type: schema.TypeString, Computed: true, Description: "Provisioning state."},
			"service_url": {Type: schema.TypeString, Computed: true, Description: "Broker endpoint clients connect to."},
		},
	}
}

func sbNamespaceCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"name":       d.Get("name").(string),
		"product":    "servicebus",
		"sku":        d.Get("sku").(string),
		"brokerKind": d.Get("broker_kind").(string),
	}
	if r := regionOrDefault(d, c); r != "" {
		payload["primaryRegion"] = r
	}

	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := mergeExtras(d, payload, "hiok_servicebus_namespace"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/ServiceBus/namespaces", payload, &out); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(out.Data.ID)
	return sbNamespaceRead(ctx, d, meta)
}

func sbNamespaceRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var out struct {
		Data struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			ServiceURL string `json:"serviceUrl"`
			Region     string `json:"primaryRegion"`
		} `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", "/api/ServiceBus/namespaces/"+d.Id(), nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		// A namespace removed outside Terraform must drop out of state, not fail the plan.
		d.SetId("")
		return nil
	}
	_ = d.Set("name", out.Data.Name)
	_ = d.Set("status", out.Data.Status)
	_ = d.Set("service_url", out.Data.ServiceURL)
	_ = d.Set("region", out.Data.Region)
	return nil
}

func sbNamespaceDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/ServiceBus/namespaces/"+d.Id(), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── Service Bus queue ───────────────────────────────────────────────────────

func resourceServiceBusQueue() *schema.Resource {
	return &schema.Resource{
		Description:   "A queue with competing consumers, dead-lettering and optional sessions.",
		CreateContext: sbQueueCreate,
		ReadContext:   sbQueueRead,
		DeleteContext: sbQueueDelete,

		Schema: map[string]*schema.Schema{
			"namespace_id":           {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Owning namespace."},
			"name":                   {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Queue name."},
			"max_delivery_count":     {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 10, Description: "Attempts before a message is dead-lettered."},
			"lock_duration_seconds":  {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 30, Description: "How long a received message stays locked."},
			"message_ttl_seconds":    {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 1209600, Description: "Message lifetime. 0 disables expiry."},
			"requires_session":       {Type: schema.TypeBool, Optional: true, ForceNew: true, Default: false, Description: "Keep related messages in order on one consumer."},
			"dead_lettering_enabled": {Type: schema.TypeBool, Optional: true, ForceNew: true, Default: true, Description: "Park poison messages instead of losing them."},
			"active_message_count":   {Type: schema.TypeInt, Computed: true, Description: "Messages waiting."},
			"dead_letter_count":      {Type: schema.TypeInt, Computed: true, Description: "Messages dead-lettered."},
		},
	}
}

func sbQueueCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	ns := d.Get("namespace_id").(string)
	payload := map[string]any{
		"name":                 d.Get("name").(string),
		"maxDeliveryCount":     d.Get("max_delivery_count").(int),
		"lockDurationSeconds":  d.Get("lock_duration_seconds").(int),
		"messageTtlSeconds":    d.Get("message_ttl_seconds").(int),
		"requiresSession":      d.Get("requires_session").(bool),
		"deadLetteringEnabled": d.Get("dead_lettering_enabled").(bool),
	}
	if err := c.Do(context.Background(), "POST", fmt.Sprintf("/api/ServiceBus/namespaces/%s/queues", ns), payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(ns + "/" + d.Get("name").(string))
	return sbQueueRead(ctx, d, meta)
}

func sbQueueRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	ns := d.Get("namespace_id").(string)
	var out struct {
		Data struct {
			ActiveMessageCount     int `json:"activeMessageCount"`
			DeadLetterMessageCount int `json:"deadLetterMessageCount"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/api/ServiceBus/namespaces/%s/queues/%s", ns, d.Get("name").(string))
	if err := c.Do(context.Background(), "GET", path, nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	_ = d.Set("active_message_count", out.Data.ActiveMessageCount)
	_ = d.Set("dead_letter_count", out.Data.DeadLetterMessageCount)
	return nil
}

func sbQueueDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	path := fmt.Sprintf("/api/ServiceBus/namespaces/%s/queues/%s",
		d.Get("namespace_id").(string), d.Get("name").(string))
	if err := c.Do(context.Background(), "DELETE", path, nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── Event Mesh stream ───────────────────────────────────────────────────────

func resourceEventStream() *schema.Resource {
	return &schema.Resource{
		Description:   "An Event Mesh stream: partitioned event ingest that stays replayable.",
		CreateContext: streamCreate,
		ReadContext:   streamRead,
		DeleteContext: streamDelete,

		Schema: map[string]*schema.Schema{
			"namespace_id":    {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Owning namespace (product must be pulse)."},
			"name":            {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Stream name."},
			"partition_count": {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 4, Description: "Partitions; determines read parallelism."},
			"retention_hours": {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 168, Description: "How long events stay replayable."},
			"events_in":       {Type: schema.TypeInt, Computed: true, Description: "Events published so far."},
		},
	}
}

func streamCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	ns := d.Get("namespace_id").(string)
	payload := map[string]any{
		"name":           d.Get("name").(string),
		"partitionCount": d.Get("partition_count").(int),
		"retentionHours": d.Get("retention_hours").(int),
	}
	if err := c.Do(context.Background(), "POST", fmt.Sprintf("/api/Pulse/namespaces/%s/streams", ns), payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(ns + "/" + d.Get("name").(string))
	return streamRead(ctx, d, meta)
}

func streamRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var out struct {
		Data []struct {
			Name     string `json:"name"`
			EventsIn int    `json:"eventsIn"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/api/Pulse/namespaces/%s/streams", d.Get("namespace_id").(string))
	if err := c.Do(context.Background(), "GET", path, nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	name := d.Get("name").(string)
	for _, s := range out.Data {
		if s.Name == name {
			_ = d.Set("events_in", s.EventsIn)
			return nil
		}
	}
	// Gone on the server: clear it so the next plan recreates it.
	d.SetId("")
	return nil
}

func streamDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	path := fmt.Sprintf("/api/Pulse/namespaces/%s/streams/%s",
		d.Get("namespace_id").(string), d.Get("name").(string))
	if err := c.Do(context.Background(), "DELETE", path, nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── Event Mesh subscription ─────────────────────────────────────────────────

func resourceEventSubscription() *schema.Resource {
	return &schema.Resource{
		Description:   "Filters a stream and delivers matching events to a handler.",
		CreateContext: eventSubCreate,
		ReadContext:   eventSubRead,
		DeleteContext: eventSubDelete,

		Schema: map[string]*schema.Schema{
			"namespace_id": {Type: schema.TypeString, Required: true, ForceNew: true},
			"stream":       {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Stream to subscribe to."},
			"name":         {Type: schema.TypeString, Required: true, ForceNew: true},
			"handler_type": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "webhook", Description: "webhook, servicebus_queue, pulse_stream."},
			"webhook_url":  {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Target URL when handler_type is webhook."},
			"target_topic": {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Target topic for forwarding handlers."},
			"included_event_types": {
				Type: schema.TypeList, Optional: true, ForceNew: true,
				Elem: &schema.Schema{Type: schema.TypeString}, Description: "Only these event types are delivered.",
			},
			"subject_begins_with":   {Type: schema.TypeString, Optional: true, ForceNew: true},
			"event_format":          {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "cloudevents"},
			"max_delivery_attempts": {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 30},
			"delivered":             {Type: schema.TypeInt, Computed: true, Description: "Events delivered."},
			"dead_lettered":         {Type: schema.TypeInt, Computed: true, Description: "Events dead-lettered."},
		},
	}
}

func eventSubCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	ns, stream := d.Get("namespace_id").(string), d.Get("stream").(string)

	config := map[string]string{}
	if u, ok := d.GetOk("webhook_url"); ok {
		config["url"] = u.(string)
	}
	if t, ok := d.GetOk("target_topic"); ok {
		config["topic"] = t.(string)
	}

	types := []string{}
	for _, v := range d.Get("included_event_types").([]any) {
		types = append(types, v.(string))
	}

	payload := map[string]any{
		"name":                d.Get("name").(string),
		"handlerType":         d.Get("handler_type").(string),
		"handlerConfig":       config,
		"eventFormat":         d.Get("event_format").(string),
		"maxDeliveryAttempts": d.Get("max_delivery_attempts").(int),
	}
	if len(types) > 0 {
		payload["includedEventTypes"] = types
	}
	if s, ok := d.GetOk("subject_begins_with"); ok {
		payload["subjectBeginsWith"] = s.(string)
	}

	path := fmt.Sprintf("/api/Pulse/namespaces/%s/streams/%s/subscriptions", ns, stream)
	if err := c.Do(context.Background(), "POST", path, payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(ns + "/" + stream + "/" + d.Get("name").(string))
	return eventSubRead(ctx, d, meta)
}

func eventSubRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var out struct {
		Data []struct {
			Name         string `json:"name"`
			Delivered    int    `json:"delivered"`
			DeadLettered int    `json:"deadLettered"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/api/Pulse/namespaces/%s/streams/%s/subscriptions",
		d.Get("namespace_id").(string), d.Get("stream").(string))
	if err := c.Do(context.Background(), "GET", path, nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	name := d.Get("name").(string)
	for _, s := range out.Data {
		if s.Name == name {
			_ = d.Set("delivered", s.Delivered)
			_ = d.Set("dead_lettered", s.DeadLettered)
			return nil
		}
	}
	d.SetId("")
	return nil
}

func eventSubDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	path := fmt.Sprintf("/api/Pulse/namespaces/%s/streams/%s/subscriptions/%s",
		d.Get("namespace_id").(string), d.Get("stream").(string), d.Get("name").(string))
	if err := c.Do(context.Background(), "DELETE", path, nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// regionOrDefault falls back to the provider's configured regions when a resource
// does not name one, so a single provider block can drive every resource.
func regionOrDefault(d *schema.ResourceData, c *client.Client) string {
	if r, ok := d.GetOk("region"); ok {
		return r.(string)
	}
	if len(c.Regions) > 0 {
		return c.Regions[0]
	}
	return ""
}
