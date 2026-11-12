resource "omadafusion_time_range" "business_hours" {
  name    = "Business hours"
  days    = ["mon", "tue", "wed", "thu", "fri"]
  periods = [{ start = "07:30", end = "18:00" }]
}
