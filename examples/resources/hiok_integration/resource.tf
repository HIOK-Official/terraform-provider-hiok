# Firing and recovered alerts, and failed Container Jobs, posted to Slack.
resource "hiok_integration" "ops_slack" {
  kind              = "slack"
  name              = "Slack #ops"
  slack_webhook_url = var.slack_webhook_url
  slack_channel     = "#ops"
}

# An issue per firing alert in Jira project OPS, commented when it recovers.
resource "hiok_integration" "ops_jira" {
  kind             = "jira"
  name             = "Jira OPS"
  jira_site        = "https://your-site.atlassian.net"
  jira_email       = "ops@example.com"
  jira_project_key = "OPS"
  jira_api_token   = var.jira_api_token
  events           = ["alert.fired", "alert.resolved"]
}

# Any HTTPS endpoint; each delivery is signed with HMAC-SHA256 in X-Hiok-Signature.
resource "hiok_integration" "pager" {
  kind           = "webhook"
  name           = "Pager"
  webhook_url    = "https://hooks.example.com/hiok"
  webhook_secret = var.webhook_secret
}
