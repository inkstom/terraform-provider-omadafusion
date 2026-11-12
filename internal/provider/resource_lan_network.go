package provider

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

var (
	_ resource.ResourceWithImportState    = &lanNetworkResource{}
	_ resource.ResourceWithValidateConfig = &lanNetworkResource{}
)

func newLANNetworkResource() resource.Resource { return &lanNetworkResource{} }

type lanNetworkResource struct{ data *providerData }

type lanNetworkModel struct {
	ID               types.String `tfsdk:"id"`
	Site             types.String `tfsdk:"site"`
	Name             types.String `tfsdk:"name"`
	VLAN             types.Int64  `tfsdk:"vlan"`
	GatewaySubnet    types.String `tfsdk:"gateway_subnet"`
	Isolation        types.Bool   `tfsdk:"isolation"`
	IGMPSnooping     types.Bool   `tfsdk:"igmp_snooping"`
	DHCPEnabled      types.Bool   `tfsdk:"dhcp_enabled"`
	DHCPStart        types.String `tfsdk:"dhcp_start"`
	DHCPEnd          types.String `tfsdk:"dhcp_end"`
	DHCPLeaseMinutes types.Int64  `tfsdk:"dhcp_lease_minutes"`
	DHCPDNSServers   types.List   `tfsdk:"dhcp_dns_servers"`
	InterfaceIDs     types.List   `tfsdk:"interface_ids"`
}

func (r *lanNetworkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lan_network"
}

func (r *lanNetworkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A gateway-served VLAN network (Network Config > LAN). Creating a network also makes the controller tag it " +
			"on the built-in `All` port profile and create an access port profile with the same name. " +
			"Do not manage the DHCP DNS of a network with both this resource and `omadafusion_lan_network_dhcp_dns`. " +
			"The default (native) network cannot be created or deleted; import it to manage its settings.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("LAN network ID."),
			"site": siteAttribute(),
			"name": schema.StringAttribute{Required: true, Description: "Network name."},
			"vlan": schema.Int64Attribute{Required: true, Description: "VLAN ID, 2-4094 (the default network uses 1)."},
			"gateway_subnet": schema.StringAttribute{
				Required:    true,
				Description: "Gateway address and prefix, e.g. `192.168.30.1/24`.",
			},
			"isolation":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Guest-style isolation: the network can reach the Internet only."},
			"igmp_snooping": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Enable IGMP snooping."},
			"dhcp_enabled":  schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Serve DHCP from the gateway."},
			"dhcp_start":    schema.StringAttribute{Optional: true, Computed: true, Description: "First address of the DHCP pool. Defaults to `.50` of the subnet when DHCP is enabled."},
			"dhcp_end":      schema.StringAttribute{Optional: true, Computed: true, Description: "Last address of the DHCP pool. Defaults to `.199` of the subnet when DHCP is enabled."},
			"dhcp_lease_minutes": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(120),
				Description: "DHCP lease time in minutes.",
			},
			"dhcp_dns_servers": schema.ListAttribute{
				Optional: true, ElementType: types.StringType,
				Description: "Up to two DNS servers handed out by DHCP. Omit to hand out the gateway (controller `auto` mode).",
			},
			"interface_ids": schema.ListAttribute{
				Optional: true, Computed: true, ElementType: types.StringType,
				Description:   "Gateway LAN interfaces that carry the network. Defaults to the interfaces of the default network.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *lanNetworkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *lanNetworkResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m lanNetworkModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.VLAN.IsNull() && !m.VLAN.IsUnknown() && (m.VLAN.ValueInt64() < 1 || m.VLAN.ValueInt64() > 4094) {
		resp.Diagnostics.AddAttributeError(path.Root("vlan"), "Invalid VLAN", "must be 1-4094")
	}
	if !m.GatewaySubnet.IsNull() && !m.GatewaySubnet.IsUnknown() {
		if _, _, err := net.ParseCIDR(m.GatewaySubnet.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("gateway_subnet"), "Invalid gateway_subnet", "use gateway-address/prefix, e.g. 192.168.30.1/24")
		}
	}
	for _, a := range []struct {
		n string
		v types.String
	}{{"dhcp_start", m.DHCPStart}, {"dhcp_end", m.DHCPEnd}} {
		if !a.v.IsNull() && !a.v.IsUnknown() && net.ParseIP(a.v.ValueString()).To4() == nil {
			resp.Diagnostics.AddAttributeError(path.Root(a.n), "Invalid address", "must be an IPv4 address")
		}
	}
	if !m.DHCPDNSServers.IsNull() && !m.DHCPDNSServers.IsUnknown() {
		dns, allKnown := knownElements(m.DHCPDNSServers.Elements())
		if allKnown && len(dns) == 0 || len(dns) > 2 {
			resp.Diagnostics.AddAttributeError(path.Root("dhcp_dns_servers"), "Invalid DNS servers", "set one or two servers, or omit the attribute")
		}
		for _, d := range dns {
			if net.ParseIP(d).To4() == nil {
				resp.Diagnostics.AddAttributeError(path.Root("dhcp_dns_servers"), "Invalid DNS server", fmt.Sprintf("%q is not an IPv4 address", d))
			}
		}
	}
}

// defaultPool returns .50-.199 of the gateway subnet, clamped to the subnet.
func defaultPool(cidr string) (string, string) {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return "", ""
	}
	base := n.IP.To4()
	ones, _ := n.Mask.Size()
	size := 1 << (32 - ones)
	at := func(off int) string {
		v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
		v += uint32(off)
		return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).String()
	}
	if size >= 256 {
		return at(50), at(199)
	}
	return at(2), at(size - 2)
}

func (m *lanNetworkModel) apply(ctx context.Context, body map[string]any, diags *diag.Diagnostics) {
	body["name"] = m.Name.ValueString()
	if body["primary"] != true {
		body["origName"] = m.Name.ValueString()
	}
	body["vlan"] = m.VLAN.ValueInt64()
	body["gatewaySubnet"] = m.GatewaySubnet.ValueString()
	body["isolation"] = m.Isolation.ValueBool()
	body["igmpSnoopEnable"] = m.IGMPSnooping.ValueBool()
	if ids := listToStrings(ctx, m.InterfaceIDs, diags); len(ids) > 0 {
		body["interfaceIds"] = ids
	}
	dhcp, _ := body["dhcpSettings"].(map[string]any)
	if dhcp == nil {
		dhcp = map[string]any{"options": []any{}}
		body["dhcpSettings"] = dhcp
	}
	dhcp["enable"] = m.DHCPEnabled.ValueBool()
	start, end := m.DHCPStart.ValueString(), m.DHCPEnd.ValueString()
	if m.DHCPStart.IsUnknown() || m.DHCPStart.IsNull() || m.DHCPEnd.IsUnknown() || m.DHCPEnd.IsNull() {
		ds, de := defaultPool(m.GatewaySubnet.ValueString())
		if m.DHCPStart.IsUnknown() || m.DHCPStart.IsNull() {
			start = ds
		}
		if m.DHCPEnd.IsUnknown() || m.DHCPEnd.IsNull() {
			end = de
		}
	}
	dhcp["ipaddrStart"], dhcp["ipaddrEnd"] = start, end
	// The pool list must match start/end or the old range is kept.
	dhcp["ipRangePool"] = []any{map[string]any{"ipaddrStart": start, "ipaddrEnd": end}}
	dhcp["leasetime"] = m.DHCPLeaseMinutes.ValueInt64()
	delete(dhcp, "ipRangeStart")
	delete(dhcp, "ipRangeEnd")
	dns := listToStrings(ctx, m.DHCPDNSServers, diags)
	delete(dhcp, "priDns")
	delete(dhcp, "sndDns")
	if len(dns) == 0 {
		dhcp["dhcpns"] = "auto"
	} else {
		dhcp["dhcpns"] = "manual"
		dhcp["priDns"] = dns[0]
		if len(dns) > 1 {
			dhcp["sndDns"] = dns[1]
		}
	}
}

func (m *lanNetworkModel) fromRaw(ctx context.Context, raw map[string]any, diags *diag.Diagnostics) {
	var n client.LANNetwork
	if err := remarshalAny(raw, &n); err != nil {
		diags.AddError("Decoding LAN network", err.Error())
		return
	}
	m.ID = types.StringValue(n.ID)
	m.Name = types.StringValue(n.DisplayName())
	m.VLAN = types.Int64Value(int64(n.VLAN))
	m.GatewaySubnet = types.StringValue(n.GatewaySubnet)
	m.Isolation = types.BoolValue(n.Isolation)
	m.IGMPSnooping = types.BoolValue(raw["igmpSnoopEnable"] == true)
	m.DHCPEnabled = types.BoolValue(n.DHCPSettings.Enable)
	m.DHCPStart = types.StringValue(n.DHCPSettings.IPStart)
	m.DHCPEnd = types.StringValue(n.DHCPSettings.IPEnd)
	m.DHCPLeaseMinutes = types.Int64Value(int64(n.DHCPSettings.LeaseTime))
	if n.DHCPSettings.DNSMode == "manual" {
		dns := []string{n.DHCPSettings.PrimaryDNS}
		if n.DHCPSettings.SecondaryDN != "" {
			dns = append(dns, n.DHCPSettings.SecondaryDN)
		}
		m.DHCPDNSServers = stringsToList(ctx, dns, diags)
	} else {
		m.DHCPDNSServers = types.ListNull(types.StringType)
	}
	var ids []string
	if v, ok := raw["interfaceIds"].([]any); ok {
		for _, x := range v {
			ids = append(ids, fmt.Sprint(x))
		}
	}
	m.InterfaceIDs = stringsToList(ctx, ids, diags)
}

func (r *lanNetworkResource) read(ctx context.Context, siteID, id string, m *lanNetworkModel, diags *diag.Diagnostics) {
	raw, err := r.data.client.LANNetworkRaw(ctx, siteID, id)
	if err != nil {
		apiError(diags, "reading LAN network", err)
		return
	}
	m.fromRaw(ctx, raw, diags)
}

func (r *lanNetworkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan lanNetworkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, plan.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	body := client.DefaultLANNetworkTemplate()
	if plan.InterfaceIDs.IsUnknown() || plan.InterfaceIDs.IsNull() {
		nets, err := r.data.client.LANNetworks(ctx, siteID)
		if err != nil {
			apiError(&resp.Diagnostics, "listing LAN networks", err)
			return
		}
		for _, n := range nets {
			if n.Raw["primary"] == true {
				body["interfaceIds"] = n.Raw["interfaceIds"]
			}
		}
	}
	plan.apply(ctx, body, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.client.CreateLANNetwork(ctx, siteID, body)
	if err != nil {
		apiError(&resp.Diagnostics, "creating LAN network", err)
		return
	}
	dns := plan.DHCPDNSServers
	r.read(ctx, siteID, id, &plan, &resp.Diagnostics)
	if dns.IsNull() && plan.DHCPDNSServers.IsNull() {
		plan.DHCPDNSServers = dns
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lanNetworkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state lanNetworkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	raw, err := r.data.client.LANNetworkRaw(ctx, siteID, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "reading LAN network", err)
		return
	}
	state.fromRaw(ctx, raw, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *lanNetworkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan, state lanNetworkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, plan.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	id := state.ID.ValueString()
	raw, err := r.data.client.LANNetworkRaw(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading LAN network", err)
		return
	}
	body := deepCopyMap(raw)
	plan.apply(ctx, body, &resp.Diagnostics)
	// The default network is listed as "Name(Default)"; keep that form.
	if n, ok := raw["name"].(string); ok && body["primary"] == true && strings.HasSuffix(n, "(Default)") {
		body["name"] = n
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.UpdateLANNetwork(ctx, siteID, id, body); err != nil {
		apiError(&resp.Diagnostics, "updating LAN network", err)
		return
	}
	dns := plan.DHCPDNSServers
	r.read(ctx, siteID, id, &plan, &resp.Diagnostics)
	if dns.IsNull() && plan.DHCPDNSServers.IsNull() {
		plan.DHCPDNSServers = dns
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lanNetworkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state lanNetworkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	if err := r.data.client.DeleteLANNetwork(ctx, siteID, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting LAN network", err)
	}
}

func (r *lanNetworkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}
