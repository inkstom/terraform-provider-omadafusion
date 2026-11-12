package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/inkstom/terraform-provider-omadafusion/internal/fake"
)

func opsConfig(srv *fake.Server, step int, mac string) string {
	return providerBlock(srv) + fmt.Sprintf(`
locals { s2 = %d >= 2 }
data "omadafusion_wan_ports" "w" {}

resource "omadafusion_wireguard" "wg" {
  name        = local.s2 ? "wg-renamed" : "wg"
  enabled     = false
  listen_port = 51820
  mtu         = local.s2 ? 1400 : 1420
  local_ip    = "10.250.0.1"
}

resource "omadafusion_wireguard_peer" "p" {
  name              = "laptop"
  interface_id      = omadafusion_wireguard.wg.id
  public_key        = "cGVlci1wdWJsaWMta2V5LWZvci10ZXN0cy0wMDAwMDA="
  allowed_addresses = local.s2 ? ["10.250.0.2/32", "192.0.2.0/24"] : ["10.250.0.2/32"]
  keepalive         = local.s2 ? 0 : 25
}

resource "omadafusion_ddns" "d" {
  service         = "noip"
  wan_port_id     = data.omadafusion_wan_ports.w.ports[0].id
  username        = "user"
  password        = "not-a-real-password"
  domain          = local.s2 ? "b.example.net" : "a.example.net"
  update_interval = local.s2 ? 30 : 60
}

resource "omadafusion_admin_user" "u" {
  name     = "helpdesk"
  password = local.s2 ? "Second-pass-2!" : "First-pass-1!"
  role     = local.s2 ? "admin" : "viewer"
  sites    = local.s2 ? [%q] : null
}

resource "omadafusion_auto_backup" "b" {
  schedule  = local.s2 ? "monthly" : "weekly"
  hour      = 3
  max_files = local.s2 ? 10 : 7
}

resource "omadafusion_site_settings" "s" {
  timezone           = "America/Chicago"
  ntp_servers        = local.s2 ? [] : ["time.cloudflare.com"]
  led_enabled        = local.s2
  syslog_enabled     = !local.s2
  syslog_host        = "192.0.2.50"
  device_ssh_enabled = true
  upnp_enabled       = false
}

resource "omadafusion_reboot_schedule" "r" {
  name         = "monthly-ap-reboot"
  enabled      = false
  device_macs  = ["AA-BB-CC-00-00-02"]
  schedule     = local.s2 ? "weekly" : "monthly"
  hour         = 4
  day_of_week  = 6
}

resource "omadafusion_device" "ap" {
  mac  = %q
  name = local.s2 ? "AP-Lobby" : "AP-01"
  led  = local.s2 ? "site" : "off"
  radio_5g = {
    tx_power_level = local.s2 ? 4 : 2
    channel        = local.s2 ? "0" : "36"
  }
}
`, step, fake.SiteName, mac)
}

func TestOpsResourcesLifecycle(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	empty := []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: opsConfig(srv, 1, "AA-BB-CC-00-00-02"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("omadafusion_wireguard.wg", "public_key"),
					resource.TestCheckResourceAttrSet("omadafusion_wireguard.wg", "private_key"),
					resource.TestCheckResourceAttr("omadafusion_ddns.d", "service", "noip"),
					resource.TestCheckResourceAttr("omadafusion_admin_user.u", "role", "viewer"),
					resource.TestCheckNoResourceAttr("omadafusion_admin_user.u", "sites"),
					resource.TestCheckResourceAttr("omadafusion_auto_backup.b", "schedule", "weekly"),
					resource.TestCheckResourceAttr("omadafusion_site_settings.s", "ntp_servers.0", "time.cloudflare.com"),
					resource.TestCheckResourceAttr("omadafusion_device.ap", "type", "ap"),
					resource.TestCheckResourceAttr("omadafusion_device.ap", "radio_5g.channel", "36"),
					func(*terraform.State) error {
						if srv.Ops.AutoBackup["enable"] != true {
							return fmt.Errorf("auto backup not enabled")
						}
						if srv.Ops.Services["ssh"]["sshEnable"] != true {
							return fmt.Errorf("ssh not enabled: %v", srv.Ops.Services["ssh"])
						}
						if led := srv.Ops.Settings["led"].(map[string]any)["enable"]; led != false {
							return fmt.Errorf("led = %v", led)
						}
						return nil
					},
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: empty},
			},
			{
				Config: opsConfig(srv, 2, "AA-BB-CC-00-00-02"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omadafusion_wireguard.wg", "mtu", "1400"),
					resource.TestCheckResourceAttr("omadafusion_wireguard_peer.p", "allowed_addresses.#", "2"),
					resource.TestCheckResourceAttr("omadafusion_ddns.d", "update_interval", "30"),
					resource.TestCheckResourceAttr("omadafusion_admin_user.u", "sites.0", fake.SiteName),
					resource.TestCheckResourceAttr("omadafusion_auto_backup.b", "schedule", "monthly"),
					resource.TestCheckResourceAttr("omadafusion_site_settings.s", "ntp_servers.#", "0"),
					resource.TestCheckResourceAttr("omadafusion_device.ap", "name", "AP-Lobby"),
					resource.TestCheckResourceAttr("omadafusion_reboot_schedule.r", "schedule", "weekly"),
					func(*terraform.State) error {
						if got := srv.UserPassword("helpdesk"); got != "Second-pass-2!" {
							return fmt.Errorf("password not changed: %q", got)
						}
						return nil
					},
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: empty},
			},
			{ResourceName: "omadafusion_wireguard.wg", ImportState: true, ImportStateVerify: true},
			{ResourceName: "omadafusion_wireguard_peer.p", ImportState: true, ImportStateVerify: true},
			{ResourceName: "omadafusion_ddns.d", ImportState: true, ImportStateVerify: true},
			{
				ResourceName: "omadafusion_admin_user.u", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"password"}, // not readable
			},
			{ResourceName: "omadafusion_auto_backup.b", ImportState: true, ImportStateVerify: true},
			{ResourceName: "omadafusion_reboot_schedule.r", ImportState: true, ImportStateVerify: true},
			// A differently formatted MAC must not plan a replacement or an update.
			{Config: opsConfig(srv, 2, "aa:bb:cc:00:00:02"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: empty}},
		},
	})
	o := srv.Ops
	if len(o.WireGuard)+len(o.Peers)+len(o.DDNS)+len(o.Users)+len(o.Reboots) != 0 {
		t.Fatalf("objects left behind: wg=%d peers=%d ddns=%d users=%d", len(o.WireGuard), len(o.Peers), len(o.DDNS), len(o.Users))
	}
	if o.AutoBackup["enable"] != false {
		t.Fatalf("auto backup still enabled after destroy")
	}
}

func TestOpsValidation(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	cases := map[string]string{
		`resource "omadafusion_ddns" "x" {
  service = "cloudflare"
  wan_port_id = "w"
  username = "u"
  password = "p"
  domain = "d"
}`: `Invalid service`,
		`resource "omadafusion_wireguard" "x" {
  name = "x"
  listen_port = 70000
  local_ip = "10.0.0.1"
}`: `Invalid port`,
		`resource "omadafusion_wireguard_peer" "x" {
  name = "x"
  interface_id = "i"
  public_key = "k"
  allowed_addresses = ["10.0.0.0"]
}`: `Invalid CIDR`,
		`resource "omadafusion_admin_user" "x" {
  name = "x"
  password = "Long-enough-1!"
  role = "owner"
}`: `Invalid role`,
		`resource "omadafusion_auto_backup" "x" {
  schedule = "hourly"
}`: `Invalid schedule`,
		`resource "omadafusion_site_settings" "x" {
  device_account_username = "admin"
}`: `Incomplete device account`,
		`resource "omadafusion_device" "x" {
  mac = "aa:bb:cc:00:00:02"
  led = "blink"
}`: `Invalid LED mode`,
	}
	for cfg, want := range cases {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories,
			Steps:                    []resource.TestStep{{Config: providerBlock(srv) + cfg, ExpectError: regexp.MustCompile(want), PlanOnly: true}},
		})
	}
}

func TestDeviceRadioNeedsAP(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: providerBlock(srv) + `
resource "omadafusion_device" "gw" {
  mac      = "AA-BB-CC-00-00-01"
  radio_2g = { enabled = false }
}`,
			ExpectError: regexp.MustCompile(`Radio settings need an access point`),
		}},
	})
}
