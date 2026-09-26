# Snapshot the VM every night at 02:30 IST, keeping the newest 7.
resource "hiok_scheduled_task" "nightly_backup" {
  resource_type = "virtualmachine"
  resource_id   = hiok_virtual_machine.web.id
  name          = "nightly-backup"
  action        = "snapshot"
  schedule_time = "02:30"
  time_zone     = "Asia/Kolkata"
  keep_count    = 7
}
