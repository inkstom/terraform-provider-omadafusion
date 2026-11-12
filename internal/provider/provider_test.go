package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/inkstom/terraform-provider-omadafusion/internal/fake"
)

// These tests run the real Terraform CLI (plan/apply/refresh/import/destroy)
// against the in-memory fake controller, so they need `terraform` on PATH but
// no hardware. They do not prove compatibility with a real controller.

var factories = map[string]func() (tfprotov6.ProviderServer, error){
	"omadafusion": providerserver.NewProtocol6WithError(New("test")()),
}

func providerBlock(srv *fake.Server) string {
	return fmt.Sprintf(`
provider "omadafusion" {
  url      = %q
  username = %q
  password = %q
  insecure = true
  site     = %q
}
`, srv.URL, fake.Username, fake.Password, fake.SiteName)
}

func TestDataSources(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: providerBlock(srv) + `
data "omadafusion_controller" "this" {}
data "omadafusion_site" "this" {}
data "omadafusion_lan_network" "mgmt" { name = "Management" }
data "omadafusion_ssid" "guest" { name = "Guest WiFi" }
data "omadafusion_devices" "all" {}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.omadafusion_controller.this", "controller_version", "6.3.14.2"),
				resource.TestCheckResourceAttr("data.omadafusion_controller.this", "id", fake.ControllerID),
				resource.TestCheckResourceAttr("data.omadafusion_site.this", "id", fake.SiteID),
				resource.TestCheckResourceAttr("data.omadafusion_lan_network.mgmt", "vlan", "1"),
				resource.TestCheckResourceAttr("data.omadafusion_lan_network.mgmt", "dhcp_dns_mode", "auto"),
				resource.TestCheckResourceAttr("data.omadafusion_ssid.guest", "wlan_id", fake.WLANID),
				resource.TestCheckResourceAttr("data.omadafusion_ssid.guest", "guest_network", "true"),
				resource.TestCheckResourceAttr("data.omadafusion_devices.all", "devices.#", "2"),
				resource.TestCheckResourceAttr("data.omadafusion_devices.all", "devices.0.connected", "true"),
			),
		}},
	})
}

const fullConfig = `
data "omadafusion_lan_network" "mgmt"  { name = "Management" }
data "omadafusion_lan_network" "staff" { name = "Staff" }
data "omadafusion_ssid" "guest" { name = "Guest WiFi" }

resource "omadafusion_time_range" "business_hours" {
  name    = "Business hours"
  days    = ["mon", "tue", "wed", "thu", "fri"]
  periods = [{ start = "07:30", end = "18:00" }]
}

resource "omadafusion_ssid_schedule" "guest" {
  ssid_id       = data.omadafusion_ssid.guest.id
  time_range_id = omadafusion_time_range.business_hours.id
}

resource "omadafusion_dhcp_reservation" "printer" {
  mac         = "aa:bb:cc:11:22:33"
  ip          = "%s"
  network_id  = data.omadafusion_lan_network.mgmt.id
  description = "Front desk printer"
}

resource "omadafusion_gateway_acl" "staff_to_mgmt" {
  name            = "Deny Staff to Management"
  policy          = "%s"
  protocols       = %s
  source_ids      = [data.omadafusion_lan_network.staff.id]
  destination_ids = [data.omadafusion_lan_network.mgmt.id]
  match_states    = ["new"]
}

resource "omadafusion_group" "admin_ports" {
  name    = "Admin ports"
  type    = "ip_port"
  subnets = [{ cidr = "192.168.10.0/24", description = "management" }]
  ports   = ["22", "443"]
}

resource "omadafusion_lan_network_dhcp_dns" "staff" {
  network_id    = data.omadafusion_lan_network.staff.id
  primary_dns   = "1.1.1.1"
  secondary_dns = %s
}
`

func TestResourcesLifecycle(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	staffID := srv.NetworkID("Staff")
	guestSSID := srv.SSIDID("Guest WiFi")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(srv.ACLs) != 0 || len(srv.DHCP) != 0 || len(srv.Groups) != 0 || len(srv.TimeRanges) != 0 {
				return fmt.Errorf("objects left behind: acls=%d dhcp=%d groups=%d timeranges=%d",
					len(srv.ACLs), len(srv.DHCP), len(srv.Groups), len(srv.TimeRanges))
			}
			if srv.SSIDs[guestSSID]["wlanScheduleEnable"] != false {
				return fmt.Errorf("guest SSID schedule still enabled")
			}
			dhcp := srv.Networks[staffID]["dhcpSettings"].(map[string]any)
			if dhcp["dhcpns"] != "auto" {
				return fmt.Errorf("staff DNS not reset: %v", dhcp["dhcpns"])
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerBlock(srv) + fmt.Sprintf(fullConfig, "192.168.10.20", "deny", `["all"]`, "null"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("omadafusion_time_range.business_hours", "id"),
					resource.TestCheckResourceAttr("omadafusion_ssid_schedule.guest", "action", "radio_on"),
					resource.TestCheckResourceAttr("omadafusion_ssid_schedule.guest", "wlan_id", fake.WLANID),
					resource.TestCheckResourceAttr("omadafusion_dhcp_reservation.printer", "id", "AA-BB-CC-11-22-33"),
					resource.TestCheckResourceAttr("omadafusion_dhcp_reservation.printer", "enabled", "true"),
					resource.TestCheckResourceAttr("omadafusion_gateway_acl.staff_to_mgmt", "index", "1"),
					resource.TestCheckResourceAttr("omadafusion_gateway_acl.staff_to_mgmt", "enabled", "true"),
					resource.TestCheckResourceAttr("omadafusion_group.admin_ports", "ports.#", "2"),
					resource.TestCheckResourceAttr("omadafusion_lan_network_dhcp_dns.staff", "primary_dns", "1.1.1.1"),
					func(_ *terraform.State) error {
						a := srv.ACLs[0]
						st := a["states"].(map[string]any)
						if a["stateMode"] != float64(1) || st["new"] != true || st["established"] != false {
							return fmt.Errorf("ACL state matching not sent: %v %v", a["stateMode"], st)
						}
						if _, has := a["sourceDirection"]; has {
							return fmt.Errorf("ACL write used the GET shape")
						}
						if srv.SSIDs[guestSSID]["pskSetting"] == nil {
							return fmt.Errorf("SSID PATCH dropped unrelated fields")
						}
						return nil
					},
				),
			},
			{
				// In-place updates.
				Config: providerBlock(srv) + fmt.Sprintf(fullConfig, "192.168.10.21", "permit", `["tcp", "udp"]`, `"1.0.0.1"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omadafusion_dhcp_reservation.printer", "ip", "192.168.10.21"),
					resource.TestCheckResourceAttr("omadafusion_gateway_acl.staff_to_mgmt", "policy", "permit"),
					resource.TestCheckResourceAttr("omadafusion_gateway_acl.staff_to_mgmt", "protocols.1", "udp"),
					resource.TestCheckResourceAttr("omadafusion_lan_network_dhcp_dns.staff", "secondary_dns", "1.0.0.1"),
				),
			},
			{
				// Imports round-trip without diffs.
				ResourceName:      "omadafusion_gateway_acl.staff_to_mgmt",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:            "omadafusion_dhcp_reservation.printer",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"mac"},
			},
			{
				ResourceName:      "omadafusion_time_range.business_hours",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestDriftIsDetected(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	cfg := providerBlock(srv) + `
data "omadafusion_lan_network" "mgmt" { name = "Management" }
resource "omadafusion_dhcp_reservation" "ap" {
  mac        = "AA-BB-CC-00-00-02"
  ip         = "192.168.10.4"
  network_id = data.omadafusion_lan_network.mgmt.id
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				// Someone deletes the reservation in the UI: Terraform recreates it.
				PreConfig: func() { delete(srv.DHCP, "AA-BB-CC-00-00-02") },
				Config:    cfg,
				Check: func(_ *terraform.State) error {
					if _, ok := srv.DHCP["AA-BB-CC-00-00-02"]; !ok {
						return fmt.Errorf("reservation was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestValidation(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	cases := []struct{ name, cfg, want string }{
		{"bad day", `resource "omadafusion_time_range" "x" {
  name    = "x"
  days    = ["monday"]
  periods = [{ start = "07:00", end = "08:00" }]
}`, "Invalid day"},
		{"bad period", `resource "omadafusion_time_range" "x" {
  name    = "x"
  days    = ["mon"]
  periods = [{ start = "09:00", end = "08:00" }]
}`, "start must be before end"},
		{"bad policy", `resource "omadafusion_gateway_acl" "x" {
  name       = "x"
  policy     = "drop"
  source_ids = ["a"]
}`, "Invalid policy"},
		{"bad protocol", `resource "omadafusion_gateway_acl" "x" {
  name       = "x"
  policy     = "deny"
  protocols  = ["sctp-ish"]
  source_ids = ["a"]
}`, "Invalid protocol"},
		{"ports on ip group", `resource "omadafusion_group" "x" {
  name    = "x"
  subnets = [{ cidr = "10.0.0.0/8" }]
  ports   = ["22"]
}`, "Ports not allowed"},
		{"bad dns", `resource "omadafusion_lan_network_dhcp_dns" "x" {
  network_id  = "n"
  primary_dns = "not-an-ip"
}`, "Invalid address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps: []resource.TestStep{{
					Config:      providerBlock(srv) + tc.cfg,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}
