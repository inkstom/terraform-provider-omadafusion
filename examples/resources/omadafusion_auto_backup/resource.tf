# Weekly controller backup, Sunday 03:00, keep 8.
resource "omadafusion_auto_backup" "this" {
  schedule    = "weekly"
  day_of_week = 0
  hour        = 3
  max_files   = 8
}
