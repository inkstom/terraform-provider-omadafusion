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

func extConfig(srv *fake.Server, step int) string {
	return providerBlock(srv) + fmt.Sprintf(`
locals { step = %d }
data "omadafusion_lan_network" "staff" { name = "Staff" }
data "omadafusion_lan_network" "guest" { name = "Guest" }
data "omadafusion_port_profile" "all" { name = "All" }
data "omadafusion_wan_ports" "w" {}

resource "omadafusion_lan_network" "t" {
  name             = local.step == 1 ? "Cameras" : "Cameras2"
  vlan             = 60
  gateway_subnet   = "192.168.60.1/24"
  dhcp_start       = local.step == 1 ? null : "192.168.60.100"
  dhcp_end         = local.step == 1 ? null : "192.168.60.150"
  dhcp_dns_servers = local.step == 1 ? null : ["1.1.1.1"]
  isolation        = local.step != 1
}

resource "omadafusion_port_profile" "p" {
  name               = "Cameras access"
  native_network_id  = omadafusion_lan_network.t.id
  tagged_network_ids = local.step == 1 ? [] : [data.omadafusion_lan_network.guest.id]
  loopback_detect    = local.step != 1
}

resource "omadafusion_switch_port" "sp" {
  switch_mac = "aa:bb:cc:00:00:03"
  port       = 5
  name       = local.step == 1 ? "jack-12" : "jack-12b"
  profile_id = local.step == 1 ? omadafusion_port_profile.p.id : data.omadafusion_port_profile.all.id
}

resource "omadafusion_port_forwarding" "pf" {
  name          = "wireguard"
  wan_port_ids  = [one([for p in data.omadafusion_wan_ports.w.ports : p.id if p.name == "WAN1"])]
  source_cidrs  = local.step == 1 ? null : ["198.51.100.0/24"]
  external_port = "51820"
  forward_ip    = "192.168.60.10"
  forward_port  = "51820"
  protocol      = local.step == 1 ? "udp" : "all"
}

resource "omadafusion_static_route" "r" {
  name         = "lab"
  destinations = local.step == 1 ? ["10.99.0.0/16"] : ["10.99.0.0/16", "10.98.0.0/16"]
  next_hop     = "192.168.10.254"
  interface_id = data.omadafusion_lan_network.staff.id
  metric       = local.step == 1 ? 0 : 3
}

resource "omadafusion_wireless_network" "w" {
  name       = local.step == 1 ? "Cameras WiFi" : "Cameras WiFi 2"
  passphrase = "correct-horse-battery"
  network_id = local.step == 1 ? omadafusion_lan_network.t.id : null
  bands      = local.step == 1 ? ["2.4ghz", "5ghz"] : ["5ghz"]
  broadcast  = local.step == 1
}

resource "omadafusion_gateway_acl" "a" {
  count           = 2
  name            = "rule ${["one", "two"][count.index]}"
  policy          = "deny"
  source_ids      = [omadafusion_lan_network.t.id]
  destination_ids = [data.omadafusion_lan_network.staff.id]
}

resource "omadafusion_gateway_acl_order" "o" {
  acl_ids = local.step == 1 ? [omadafusion_gateway_acl.a[1].id, omadafusion_gateway_acl.a[0].id] : [omadafusion_gateway_acl.a[0].id, omadafusion_gateway_acl.a[1].id]
}
`, step)
}

func TestExtendedResourcesLifecycle(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	empty := []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: extConfig(srv, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omadafusion_lan_network.t", "dhcp_start", "192.168.60.50"),
					resource.TestCheckResourceAttr("omadafusion_lan_network.t", "dhcp_end", "192.168.60.199"),
					resource.TestCheckNoResourceAttr("omadafusion_lan_network.t", "dhcp_dns_servers"),
					resource.TestCheckResourceAttr("omadafusion_lan_network.t", "interface_ids.#", "2"),
					resource.TestCheckResourceAttr("omadafusion_switch_port.sp", "profile_name", "Cameras access"),
					resource.TestCheckResourceAttr("omadafusion_switch_port.sp", "id", "AA-BB-CC-00-00-03/5"),
					resource.TestCheckResourceAttr("omadafusion_wireless_network.w", "vlan_id", "60"),
					resource.TestCheckResourceAttr("omadafusion_port_forwarding.pf", "protocol", "udp"),
					func(*terraform.State) error {
						if got := srv.ACLs[0]["name"]; got != "rule two" {
							return fmt.Errorf("first ACL is %v, want rule two", got)
						}
						return nil
					},
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: empty},
			},
			{
				Config: extConfig(srv, 2),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omadafusion_lan_network.t", "dhcp_dns_servers.0", "1.1.1.1"),
					resource.TestCheckResourceAttr("omadafusion_lan_network.t", "isolation", "true"),
					resource.TestCheckResourceAttr("omadafusion_port_profile.p", "tagged_network_ids.#", "1"),
					resource.TestCheckResourceAttr("omadafusion_switch_port.sp", "profile_name", "All"),
					resource.TestCheckResourceAttr("omadafusion_wireless_network.w", "vlan_id", "0"),
					resource.TestCheckResourceAttr("omadafusion_wireless_network.w", "bands.#", "1"),
					resource.TestCheckResourceAttr("omadafusion_static_route.r", "metric", "3"),
					func(*terraform.State) error {
						if got := srv.ACLs[0]["name"]; got != "rule one" {
							return fmt.Errorf("first ACL is %v, want rule one", got)
						}
						return nil
					},
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: empty},
			},
			{ResourceName: "omadafusion_lan_network.t", ImportState: true, ImportStateVerify: true},
			{ResourceName: "omadafusion_port_profile.p", ImportState: true, ImportStateVerify: true},
			{ResourceName: "omadafusion_port_forwarding.pf", ImportState: true, ImportStateVerify: true},
			{ResourceName: "omadafusion_wireless_network.w", ImportState: true, ImportStateVerify: true},
			{
				ResourceName: "omadafusion_static_route.r", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"interface_id"}, // not returned by the controller
			},
			{
				ResourceName: "omadafusion_switch_port.sp", ImportState: true, ImportStateVerify: true,
				ImportStateId:           "aa:bb:cc:00:00:03/5",
				ImportStateVerifyIgnore: []string{"switch_mac"}, // normalised on import; semantically equal
			},
			// A differently formatted MAC in config must not plan a replacement.
			{Config: extConfig(srv, 2), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: empty}},
		},
	})
	if len(srv.Forwards)+len(srv.Routes) != 0 {
		t.Fatalf("objects left behind: %d forwards, %d routes", len(srv.Forwards), len(srv.Routes))
	}
	if p := srv.Ports[fake.SwitchMAC+"/5"]; p["name"] != "Port5" || p["profileId"] != fake.AllProfileID {
		t.Fatalf("switch port not reset on destroy: %v", p)
	}
}

func TestExtendedValidation(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	cases := map[string]string{
		`resource "omadafusion_lan_network" "x" {
  name = "x"
  vlan = 5000
  gateway_subnet = "10.0.0.1/24"
}`: "Invalid VLAN",
		`resource "omadafusion_port_forwarding" "x" {
  name = "x"
  wan_port_ids = ["a"]
  external_port = "70000-1"
  forward_ip = "10.0.0.5"
  forward_port = "1"
}`: "Invalid port",
		`resource "omadafusion_wireless_network" "x" {
  name = "x"
  passphrase = "short"
}`: "Invalid passphrase",
		`resource "omadafusion_static_route" "x" {
  name = "x"
  destinations = ["10.0.0.0/33"]
  next_hop = "10.0.0.1"
  interface_id = "a"
}`: "Invalid CIDR",
	}
	for cfg, want := range cases {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories,
			Steps:                    []resource.TestStep{{Config: providerBlock(srv) + cfg, ExpectError: regexp.MustCompile(want)}},
		})
	}
}

// count.index and for_each keys are unknown during validate; validators
// must not treat them as empty strings.
func TestValidateWithUnknownForEach(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: providerBlock(srv) + `
data "omadafusion_lan_network" "staff" { name = "Staff" }
data "omadafusion_lan_network" "guest" { name = "Guest" }
resource "omadafusion_gateway_acl" "d" {
  count           = 2
  name            = "deny ${["tcp", "udp"][count.index]}"
  policy          = "deny"
  protocols       = [["tcp", "udp"][count.index]]
  source_ids      = [data.omadafusion_lan_network.guest.id]
  destination_ids = [data.omadafusion_lan_network.staff.id]
}`,
			Check: resource.TestCheckResourceAttr("omadafusion_gateway_acl.d.1", "protocols.0", "udp"),
		}},
	})
}
