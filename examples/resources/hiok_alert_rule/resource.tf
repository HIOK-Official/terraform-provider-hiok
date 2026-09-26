# Email when the VM is down for 2 minutes.
resource "hiok_alert_rule" "vm_down" {
  resource_type  = "virtualmachine"
  resource_id    = hiok_virtual_machine.web.id
  name           = "web-down"
  metric         = "availability"
  comparison     = "lt"
  threshold      = 100
  window_minutes = 2
  severity       = "critical"
}
