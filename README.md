# terraform-provider-omadafusion

A Terraform/OpenTofu provider for TP-Link Omada controllers, built for the controller that runs **inside an Omada Fusion gateway**.

It talks to the same web API (`/{omadacId}/api/v2`) that the controller's own web UI uses and signs in with a **local controller account**. You don't need an Open API client, cloud binding or separate software controller. That matters on integrated gateways, where the UI session is often the only API path, and where several settings can only be changed through the UI API.

> **Status: early (v0.x), validated on hardware.** Every resource and data source has been run end to end with the Terraform CLI against a real Omada Fusion 2.5G gateway (integrated controller 6.3.14.2, API 3) with adopted SG2428LP/SG2210P switches and EAP650/EAP723 APs: create, update, refresh, drift detection, import and destroy, followed by a check that the controller returned to its prior state. See [Hardware validation](#hardware-validation). Other models and controller versions are untested. Take a controller backup before your first apply, and please open an issue with your model and controller version if something doesn't match.
>
> Not affiliated with or endorsed by TP-Link. "Omada" is a TP-Link trademark.

## What it manages

| Type | Name | Notes |
|---|---|---|
| Resource | `omadafusion_lan_network` | Gateway-served VLAN networks: subnet, DHCP pool, lease, DNS, isolation, IGMP snooping. Import the default network to manage it. |
| Resource | `omadafusion_wireless_network` | WPA-Personal SSIDs: passphrase, bands, VLAN/network, broadcast, guest isolation, 802.11r. |
| Resource | `omadafusion_port_profile` | Switch port profiles: native and tagged networks, STP, loopback detection, isolation, LLDP-MED, EEE, flow control. |
| Resource | `omadafusion_switch_port` | Name and profile of one switch port, for building a jack map. Destroy resets to `Port<N>` / `All`. |
| Resource | `omadafusion_gateway_acl` | Gateway ACL rules (site ACL mode). One-way LAN-to-LAN denies with `match_states = ["new"]`. |
| Resource | `omadafusion_gateway_acl_order` | Enforces rule evaluation order; drift shows when rules are reordered or inserted above. |
| Resource | `omadafusion_group` | IP groups and IP-port groups, edited in place. |
| Resource | `omadafusion_dhcp_reservation` | DHCP reservations, keyed by MAC (any format). |
| Resource | `omadafusion_port_forwarding` | NAT virtual servers, optionally limited to source networks. |
| Resource | `omadafusion_static_route` | Next-hop static routes on the gateway. |
| Resource | `omadafusion_time_range` | Time-range profiles (every day, weekday, weekend or custom days), edited in place even while a schedule uses them. |
| Resource | `omadafusion_ssid_schedule` | Attaches a schedule to an SSID, e.g. guest Wi-Fi off after hours. Destroy turns the schedule off. |
| Resource | `omadafusion_lan_network_dhcp_dns` | DNS servers handed out by an existing network you don't otherwise manage. Destroy resets to the gateway. |
| Data source | `omadafusion_controller` | Version and controller ID (`/api/info`, no login needed). |
| Data source | `omadafusion_site` | Site lookup by name. |
| Data source | `omadafusion_lan_network` | Network ID, VLAN, subnet and DHCP pool by name. |
| Data source | `omadafusion_ssid` | SSID and WLAN group IDs by name. Keys are never read into state. |
| Data source | `omadafusion_port_profile` | Port profile by name, including the built-in `All` and `Disable`. |
| Data source | `omadafusion_wan_ports` | Gateway port UUIDs for port forwarding and routes. |
| Data source | `omadafusion_gateway_acls` | All gateway ACL rules in evaluation order. |
| Data source | `omadafusion_devices` | Adopted gateways, switches and APs with connection status. |

Settings resources (`omadafusion_ssid_schedule`, `omadafusion_lan_network_dhcp_dns`) change a few fields on an object the provider doesn't own. They read the full object, change only their fields, and send everything else back unchanged, because the controller rejects partial updates.

Not covered yet: WPA-Enterprise and open/PPSK SSIDs, captive portals, VPN servers, dynamic DNS, per-port overrides, controller settings, switch/AP ACLs, and the newer per-device gateway ACL mode (`osg-acls`, which needs an Open API token).

### Things the controller does that you should know

- **Creating or changing an SSID re-provisions every AP** in the WLAN group, which briefly drops all SSIDs. If you run Terraform over Wi-Fi from the managed network, expect a few seconds of loss; the provider retries reads and logins on connection errors but does not retry writes.
- **Creating a LAN network** also tags it on the built-in `All` port profile and creates an access profile with the same name. Deleting the network removes both.
- **"Gateway management" as an ACL destination** (`destination_type = 5`) matches all traffic to the gateway, including DNS and DHCP. Use `protocols = ["tcp"]` to block only the admin pages.
- **Static routes** do not return `interface_id` on read, so changes to it made in the UI are not detected.
- **Wireless passphrases** are stored in Terraform state. Protect the state file.

## Quick start

```hcl
terraform {
  required_providers {
    omadafusion = {
      source = "inkstom/omadafusion"
    }
  }
}

provider "omadafusion" {
  site     = "Default"
  insecure = true # integrated controllers ship a self-signed certificate
}

data "omadafusion_ssid" "guest" {
  name = "Guest WiFi"
}

resource "omadafusion_time_range" "business_hours" {
  name    = "Business hours"
  days    = ["mon", "tue", "wed", "thu", "fri"]
  periods = [{ start = "07:30", end = "18:00" }]
}

resource "omadafusion_ssid_schedule" "guest" {
  ssid_id       = data.omadafusion_ssid.guest.id
  time_range_id = omadafusion_time_range.business_hours.id
}
```

```sh
export OMADA_URL=https://192.168.0.1     # the gateway's management address
export OMADA_USERNAME=terraform
export OMADA_PASSWORD='...'
terraform init && terraform plan
```

A fuller example (segmentation, DNS, reservations and a guest schedule) is in [`examples/complete`](examples/complete/main.tf). Per-resource docs are in [`docs/`](docs/).

### Installing before a registry release

Until the provider is on the Terraform Registry, build it and point Terraform at the binary:

```sh
go install github.com/inkstom/terraform-provider-omadafusion@latest
cat >> ~/.terraformrc <<'EOF'
provider_installation {
  dev_overrides {
    "inkstom/omadafusion" = "/Users/YOU/go/bin"
  }
  direct {}
}
EOF
```

## Provider configuration

| Argument | Env var | Description |
|---|---|---|
| `url` | `OMADA_URL` | Controller base URL, e.g. `https://192.168.0.1`. |
| `username` | `OMADA_USERNAME` | Local controller account. |
| `password` | `OMADA_PASSWORD` | Its password. Prefer the env var to keep it out of config. |
| `insecure` | `OMADA_INSECURE` | Skip TLS verification. Off by default. |
| `site` | `OMADA_SITE` | Default site name or ID. Every resource also accepts `site`. |
| `timeout_seconds` | | Per-request timeout, default 60. |

**Use a dedicated account.** Create a separate local admin for Terraform, limited to the site it manages, so you can revoke it without touching anyone else's login. A session that expires is renewed automatically.

## Things the controller does that you should know

These were found on real hardware (Fusion 2.5G, controller 6.3.14.2). The provider handles the API side; the network side is up to you.

- **`destination_type = 5` (gateway management) matches all traffic to the gateway**, not just its admin ports. With `protocols = ["all"]` it blocks DNS, DHCP renewals and ping to the gateway. Use `["tcp"]`. That still blocks DNS-over-TCP to the gateway, so hand those networks public resolvers with `omadafusion_lan_network_dhcp_dns`.
- **IP and IP-port groups do not match traffic addressed to the gateway itself.** A deny for the gateway's own IPs on ports 22/80/443 does nothing, and a permit placed above a type-5 deny does not override it.
- **Rules can be saved disabled.** A rule in the table with `enabled = false` is listed but not enforced. The provider defaults `enabled` to `true`.
- **ACL writes use a different shape than reads.** GET returns `sourceDirection`/`destinationDirection`; PUT with that shape fails with `-1001`. The provider always writes the accepted shape.
- **New ACLs are appended to the end of the table.** Use `depends_on` between rules when order matters. Reordering is not managed yet.
- **LAN network PATCH needs an IPv6 `proto`** even when IPv6 is off, or it fails with `proto should not be null`. The provider adds it.
- **DHCP reservations apply when the lease renews.** Rebooting an edge switch to force it also power-cycles PoE APs behind it.
- **A time range in use by an SSID schedule can't be deleted.** If you change a time range that a schedule uses, remove or repoint the schedule in the same apply, or apply twice.
- **Some settings restart the gateway**, not just the controller. Changing the portal hostname did on the test unit. Those settings are deliberately not in this provider yet.
- **"Incompatible" device warnings** in the controller UI (802.1X, 6 GHz beacon control, emergency numbers, MLO) come from the site-wide template and are cosmetic when the feature is off. Nothing here can clear them.

## Testing

```sh
go test ./...
```

The tests start an in-memory fake controller (`internal/fake`) that reproduces the API shapes and quirks above: the login/CSRF flow, session expiry, the `-1001` rejections, MAC-keyed reservations, append-only ACL ordering and in-use time ranges. The provider tests then drive the real Terraform CLI through create, update, import, drift detection and destroy against it. They need `terraform` on your `PATH` but no hardware.

The fake is only as good as what was observed. It can't prove compatibility with other models, firmware or controller versions.

## Development

```sh
go build ./...
go vet ./...
go test ./...
go generate ./...   # regenerate docs/ with tfplugindocs
```

## License

[MPL-2.0](LICENSE)

## Hardware validation

Run on 2026-11-11 against a bench-staged Fusion 2.5G v2.0 (controller 6.3.14.2, API 3, gateway firmware 1.0.10) with SG2428LP and SG2210P switches, three EAP650 v3 and two EAP723 v2 APs. A controller backup was taken first, and the controller's ACLs, reservations, networks, groups, time ranges, SSIDs, port profiles and port forwards were compared against a snapshot afterwards (no differences).

| Check | Result |
|---|---|
| All data sources against the live site | Pass |
| Import an existing production config (15 objects: ACLs, reservations, DNS, time range, SSID schedule) | Pass, zero-diff plan |
| Create, update, refresh, out-of-band drift, repair and destroy for every resource | Pass |
| Import of every new resource, then a zero-diff plan | Pass (static route `interface_id` must be re-set, see above) |
| Editing a time range in place while an SSID schedule uses it | Pass |

Bugs found and fixed during validation: custom-day time ranges were stored with no days (the controller silently drops days that do not match its day mode); an empty secondary DNS was rejected; groups and time ranges were needlessly replaced instead of edited; ACL `index` showed as unknown on every update; validators rejected `for_each`/`count` values that are unknown during `validate`; a MAC written in another format forced replacement after import.
