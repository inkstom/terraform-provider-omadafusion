package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

// ======================= wireless network (SSID) =======================

var (
	_ resource.ResourceWithImportState    = &wirelessNetworkResource{}
	_ resource.ResourceWithValidateConfig = &wirelessNetworkResource{}
)

func newWirelessNetworkResource() resource.Resource { return &wirelessNetworkResource{} }

type wirelessNetworkResource struct{ data *providerData }

type wirelessNetworkModel struct {
	ID           types.String `tfsdk:"id"`
	Site         types.String `tfsdk:"site"`
	WLANID       types.String `tfsdk:"wlan_id"`
	Name         types.String `tfsdk:"name"`
	Passphrase   types.String `tfsdk:"passphrase"`
	Bands        types.Set    `tfsdk:"bands"`
	Broadcast    types.Bool   `tfsdk:"broadcast"`
	NetworkID    types.String `tfsdk:"network_id"`
	VLANID       types.Int64  `tfsdk:"vlan_id"`
	GuestNetwork types.Bool   `tfsdk:"guest_network"`
	FastRoaming  types.Bool   `tfsdk:"fast_roaming"`
}

var bandBits = map[string]int{"2.4ghz": 1, "5ghz": 2, "6ghz": 4}

func (r *wirelessNetworkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wireless_network"
}

func (r *wirelessNetworkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A WPA-Personal SSID (Wireless Networks). Creating or changing an SSID re-provisions every AP in the WLAN group, " +
			"which briefly interrupts all SSIDs. The passphrase is stored in state; protect the state file. " +
			"WLAN schedules are left untouched; manage them with `omadafusion_ssid_schedule`.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("SSID ID."),
			"site": siteAttribute(),
			"wlan_id": schema.StringAttribute{
				Optional: true, Computed: true,
				Description:   "WLAN group ID. Defaults to the site's primary WLAN group.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()},
			},
			"name":       schema.StringAttribute{Required: true, Description: "SSID name."},
			"passphrase": schema.StringAttribute{Required: true, Sensitive: true, Description: "WPA2 passphrase, 8-63 characters."},
			"bands": schema.SetAttribute{
				Optional: true, Computed: true, ElementType: types.StringType,
				Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{types.StringValue("2.4ghz"), types.StringValue("5ghz")})),
				Description: "Radios to broadcast on: `2.4ghz`, `5ghz`, `6ghz`. Defaults to 2.4 and 5 GHz.",
			},
			"broadcast": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Broadcast the SSID name."},
			"network_id": schema.StringAttribute{
				Optional:    true,
				Description: "LAN network to bridge clients into. Omit to use the AP's native (management) VLAN.",
			},
			"vlan_id":       schema.Int64Attribute{Computed: true, Description: "VLAN the SSID is bridged to, or 0 when untagged."},
			"guest_network": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Controller guest-network isolation for this SSID."},
			"fast_roaming":  schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "802.11r fast roaming."},
		},
	}
}

func (r *wirelessNetworkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *wirelessNetworkResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m wirelessNetworkModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Passphrase.IsNull() && !m.Passphrase.IsUnknown() {
		if n := len(m.Passphrase.ValueString()); n < 8 || n > 63 {
			resp.Diagnostics.AddAttributeError(path.Root("passphrase"), "Invalid passphrase", "must be 8-63 characters")
		}
	}
	if !m.Name.IsNull() && !m.Name.IsUnknown() {
		if n := len(m.Name.ValueString()); n < 1 || n > 32 {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid SSID", "must be 1-32 bytes")
		}
	}
	if !m.Bands.IsNull() && !m.Bands.IsUnknown() {
		bs, allKnown := knownElements(m.Bands.Elements())
		if allKnown && len(bs) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("bands"), "No bands", "set at least one band")
		}
		for _, b := range bs {
			if _, ok := bandBits[b]; !ok {
				resp.Diagnostics.AddAttributeError(path.Root("bands"), "Invalid band", fmt.Sprintf("%q is not one of 2.4ghz, 5ghz, 6ghz", b))
			}
		}
	}
}

func (m *wirelessNetworkModel) apply(ctx context.Context, siteID string, c *client.Client, body map[string]any, diags *diag.Diagnostics) {
	body["name"] = m.Name.ValueString()
	psk, _ := body["pskSetting"].(map[string]any)
	if psk == nil {
		psk = map[string]any{"versionPsk": 2, "encryptionPsk": 3, "gikRekeyPskEnable": false}
		body["pskSetting"] = psk
	}
	psk["securityKey"] = m.Passphrase.ValueString()
	body["security"] = 3
	band := 0
	for _, b := range setToStrings(ctx, m.Bands, diags) {
		band |= bandBits[b]
	}
	body["band"] = band
	body["broadcast"] = m.Broadcast.ValueBool()
	body["guestNetEnable"] = m.GuestNetwork.ValueBool()
	body["enable11r"] = m.FastRoaming.ValueBool()
	if m.NetworkID.IsNull() || m.NetworkID.ValueString() == "" {
		body["vlanEnable"] = false
		delete(body, "vlanId")
		body["vlanSetting"] = map[string]any{"mode": 0}
		return
	}
	n, err := c.LANNetwork(ctx, siteID, m.NetworkID.ValueString())
	if err != nil {
		diags.AddAttributeError(path.Root("network_id"), "Unknown LAN network", err.Error())
		return
	}
	body["vlanEnable"] = true
	body["vlanId"] = n.VLAN
	body["vlanSetting"] = map[string]any{"mode": 1, "customConfig": map[string]any{
		"customMode": 0, "lanNetworkId": n.ID,
		"lanNetworkVlanIds": map[string]any{n.ID: []int{n.VLAN}}, "bridgeVlan": n.VLAN,
	}}
}

func (m *wirelessNetworkModel) fromRaw(ctx context.Context, raw map[string]any, diags *diag.Diagnostics) {
	m.ID = types.StringValue(fmt.Sprint(raw["id"]))
	if w, ok := raw["wlanId"].(string); ok && w != "" {
		m.WLANID = types.StringValue(w)
	}
	m.Name = types.StringValue(fmt.Sprint(raw["name"]))
	if psk, ok := raw["pskSetting"].(map[string]any); ok {
		if k, ok := psk["securityKey"].(string); ok {
			m.Passphrase = types.StringValue(k)
		}
	}
	band := 0
	if f, ok := raw["band"].(float64); ok {
		band = int(f)
	}
	var bands []string
	for name, bit := range bandBits {
		if band&bit != 0 {
			bands = append(bands, name)
		}
	}
	sort.Strings(bands)
	m.Bands = stringsToSet(ctx, bands, diags)
	m.Broadcast = types.BoolValue(raw["broadcast"] == true)
	m.GuestNetwork = types.BoolValue(raw["guestNetEnable"] == true)
	m.FastRoaming = types.BoolValue(raw["enable11r"] == true)
	m.NetworkID = types.StringNull()
	m.VLANID = types.Int64Value(0)
	if vs, ok := raw["vlanSetting"].(map[string]any); ok {
		if cc, ok := vs["customConfig"].(map[string]any); ok {
			if id, ok := cc["lanNetworkId"].(string); ok && id != "" && raw["vlanEnable"] == true {
				m.NetworkID = types.StringValue(id)
			}
		}
	}
	if raw["vlanEnable"] == true {
		if v, ok := raw["vlanId"].(float64); ok {
			m.VLANID = types.Int64Value(int64(v))
		}
	}
}

func (r *wirelessNetworkResource) wlan(ctx context.Context, siteID string, m *wirelessNetworkModel, diags *diag.Diagnostics) string {
	if !m.WLANID.IsNull() && !m.WLANID.IsUnknown() && m.WLANID.ValueString() != "" {
		return m.WLANID.ValueString()
	}
	gs, err := r.data.client.WLANGroups(ctx, siteID)
	if err != nil {
		apiError(diags, "listing WLAN groups", err)
		return ""
	}
	for _, g := range gs {
		if g.Primary {
			return g.ID
		}
	}
	if len(gs) > 0 {
		return gs[0].ID
	}
	diags.AddError("No WLAN group", "the site has no WLAN group")
	return ""
}

func (r *wirelessNetworkResource) readInto(ctx context.Context, siteID, wlanID, id string, m *wirelessNetworkModel, diags *diag.Diagnostics) bool {
	s, err := r.data.client.FindSSID(ctx, siteID, wlanID, id)
	if err != nil {
		if client.IsNotFound(err) {
			return false
		}
		apiError(diags, "reading SSID", err)
		return true
	}
	m.fromRaw(ctx, s.Raw, diags)
	m.WLANID = types.StringValue(s.WLANID)
	return true
}

func (r *wirelessNetworkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wirelessNetworkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	wlanID := r.wlan(ctx, siteID, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	body := client.DefaultSSIDTemplate()
	plan.apply(ctx, siteID, r.data.client, body, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.client.CreateSSID(ctx, siteID, wlanID, body)
	if err != nil {
		apiError(&resp.Diagnostics, "creating SSID", err)
		return
	}
	r.readInto(ctx, siteID, wlanID, id, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *wirelessNetworkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wirelessNetworkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	if !r.readInto(ctx, siteID, state.WLANID.ValueString(), state.ID.ValueString(), &state, &resp.Diagnostics) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *wirelessNetworkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state wirelessNetworkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	cur, err := r.data.client.FindSSID(ctx, siteID, state.WLANID.ValueString(), state.ID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "reading SSID", err)
		return
	}
	body := client.SSIDWriteBody(cur.Raw)
	plan.apply(ctx, siteID, r.data.client, body, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.UpdateSSID(ctx, siteID, cur.WLANID, cur.ID, body); err != nil {
		apiError(&resp.Diagnostics, "updating SSID", err)
		return
	}
	r.readInto(ctx, siteID, cur.WLANID, cur.ID, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *wirelessNetworkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state wirelessNetworkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok {
		return
	}
	if err := r.data.client.DeleteSSID(ctx, siteID, state.WLANID.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting SSID", err)
	}
}

func (r *wirelessNetworkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}

// ======================= gateway ACL order =======================

var _ resource.Resource = &gatewayACLOrderResource{}

func newGatewayACLOrderResource() resource.Resource { return &gatewayACLOrderResource{} }

type gatewayACLOrderResource struct{ data *providerData }

type gatewayACLOrderModel struct {
	ID     types.String `tfsdk:"id"`
	Site   types.String `tfsdk:"site"`
	ACLIDs types.List   `tfsdk:"acl_ids"`
}

func (r *gatewayACLOrderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_acl_order"
}

func (r *gatewayACLOrderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Enforces the evaluation order of gateway ACL rules. The listed rules are moved to the top of the table in " +
			"the given order; unlisted rules keep their relative order below them. Use one of these per site. " +
			"Destroying it leaves the order as it is.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttribute("Site ID."),
			"site":    siteAttribute(),
			"acl_ids": schema.ListAttribute{Required: true, ElementType: types.StringType, Description: "Gateway ACL IDs, first match first."},
		},
	}
}

func (r *gatewayACLOrderResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *gatewayACLOrderResource) set(ctx context.Context, m *gatewayACLOrderModel, diags *diag.Diagnostics) {
	siteID, ok := r.data.mustSite(ctx, m.Site, diags)
	if !ok {
		return
	}
	if err := r.data.client.ReorderGatewayACLs(ctx, siteID, listToStrings(ctx, m.ACLIDs, diags)); err != nil {
		apiError(diags, "reordering gateway ACLs", err)
		return
	}
	m.ID = types.StringValue(siteID)
	r.read(ctx, siteID, m, diags)
}

// read reports the IDs of the rules that currently occupy the top
// len(acl_ids) positions, so reordering or insertions made outside Terraform
// show as a diff against the configured order.
func (r *gatewayACLOrderResource) read(ctx context.Context, siteID string, m *gatewayACLOrderModel, diags *diag.Diagnostics) {
	all, err := r.data.client.GatewayACLs(ctx, siteID)
	if err != nil {
		apiError(diags, "listing gateway ACLs", err)
		return
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Index < all[j].Index })
	n := len(listToStrings(ctx, m.ACLIDs, diags))
	if n > len(all) {
		n = len(all)
	}
	top := make([]string, 0, n)
	for _, a := range all[:n] {
		top = append(top, a.ID)
	}
	m.ACLIDs = stringsToList(ctx, top, diags)
}

func (r *gatewayACLOrderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayACLOrderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gatewayACLOrderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayACLOrderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	r.read(ctx, siteID, &state, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayACLOrderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayACLOrderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gatewayACLOrderResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ======================= data sources =======================

// --- omadafusion_port_profile ---

func newPortProfileDataSource() datasource.DataSource { return &portProfileDataSource{} }

type portProfileDataSource struct{ data *providerData }

type portProfileDSModel struct {
	Site             types.String `tfsdk:"site"`
	Name             types.String `tfsdk:"name"`
	ID               types.String `tfsdk:"id"`
	NativeNetworkID  types.String `tfsdk:"native_network_id"`
	TaggedNetworkIDs types.List   `tfsdk:"tagged_network_ids"`
}

func (d *portProfileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_profile"
}

func (d *portProfileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Look up a switch port profile by name, including the built-in `All` and `Disable` profiles.",
		Attributes: map[string]dschema.Attribute{
			"site":               dschema.StringAttribute{Optional: true, Description: "Site name or ID."},
			"name":               dschema.StringAttribute{Required: true, Description: "Profile name."},
			"id":                 dschema.StringAttribute{Computed: true, Description: "Profile ID."},
			"native_network_id":  dschema.StringAttribute{Computed: true, Description: "Untagged network ID."},
			"tagged_network_ids": dschema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Tagged network IDs."},
		},
	}
}

func (d *portProfileDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *portProfileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m portProfileDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	siteID, ok := d.data.mustSite(ctx, m.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	p, err := d.data.client.PortProfileByName(ctx, siteID, m.Name.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "reading port profile", err)
		return
	}
	m.ID = types.StringValue(fmt.Sprint(p["id"]))
	if v, ok := p["nativeNetworkId"].(string); ok {
		m.NativeNetworkID = types.StringValue(v)
	} else {
		m.NativeNetworkID = types.StringNull()
	}
	m.TaggedNetworkIDs = stringsToList(ctx, stringsFromAny(p["tagNetworkIds"]), &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// --- omadafusion_wan_ports ---

func newWANPortsDataSource() datasource.DataSource { return &wanPortsDataSource{} }

type wanPortsDataSource struct{ data *providerData }

type wanPortsDSModel struct {
	Site  types.String `tfsdk:"site"`
	Ports types.List   `tfsdk:"ports"`
}

var wanPortAttrTypes = map[string]attr.Type{
	"port": types.Int64Type, "id": types.StringType, "name": types.StringType, "is_wan": types.BoolType,
	"online": types.BoolType, "ipv4_address": types.StringType,
}

func (d *wanPortsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wan_ports"
}

func (d *wanPortsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "The gateway's ports as listed under Internet settings, with the UUIDs that port forwarding and static routes reference.",
		Attributes: map[string]dschema.Attribute{
			"site": dschema.StringAttribute{Optional: true, Description: "Site name or ID."},
			"ports": dschema.ListNestedAttribute{
				Computed: true, Description: "Gateway ports.",
				NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
					"port":         dschema.Int64Attribute{Computed: true, Description: "Physical port number."},
					"id":           dschema.StringAttribute{Computed: true, Description: "Port UUID."},
					"name":         dschema.StringAttribute{Computed: true, Description: "Port name, e.g. `WAN1`."},
					"is_wan":       dschema.BoolAttribute{Computed: true, Description: "Whether the port is currently a WAN."},
					"online":       dschema.BoolAttribute{Computed: true, Description: "Whether the WAN is online."},
					"ipv4_address": dschema.StringAttribute{Computed: true, Description: "Current IPv4 address, if a WAN."},
				}},
			},
		},
	}
}

func (d *wanPortsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *wanPortsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m wanPortsDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	siteID, ok := d.data.mustSite(ctx, m.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	ports, err := d.data.client.WANPorts(ctx, siteID)
	if err != nil {
		apiError(&resp.Diagnostics, "reading WAN ports", err)
		return
	}
	vals := make([]attr.Value, 0, len(ports))
	for _, p := range ports {
		v, dd := types.ObjectValue(wanPortAttrTypes, map[string]attr.Value{
			"port": types.Int64Value(int64(p.Port)), "id": types.StringValue(p.UUID), "name": types.StringValue(p.Name),
			"is_wan": types.BoolValue(p.Type == 3), "online": types.BoolValue(p.Online == 1), "ipv4_address": types.StringValue(p.IPv4),
		})
		resp.Diagnostics.Append(dd...)
		vals = append(vals, v)
	}
	l, dd := types.ListValue(types.ObjectType{AttrTypes: wanPortAttrTypes}, vals)
	resp.Diagnostics.Append(dd...)
	m.Ports = l
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// --- omadafusion_gateway_acls ---

func newGatewayACLsDataSource() datasource.DataSource { return &gatewayACLsDataSource{} }

type gatewayACLsDataSource struct{ data *providerData }

type gatewayACLsDSModel struct {
	Site types.String `tfsdk:"site"`
	ACLs types.List   `tfsdk:"acls"`
}

var aclSummaryAttrTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "index": types.Int64Type, "enabled": types.BoolType, "policy": types.StringType,
}

func (d *gatewayACLsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_acls"
}

func (d *gatewayACLsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "All gateway ACL rules in evaluation order. Useful for auditing and for importing existing rules.",
		Attributes: map[string]dschema.Attribute{
			"site": dschema.StringAttribute{Optional: true, Description: "Site name or ID."},
			"acls": dschema.ListNestedAttribute{
				Computed: true, Description: "Rules, first match first.",
				NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
					"id":      dschema.StringAttribute{Computed: true, Description: "Rule ID."},
					"name":    dschema.StringAttribute{Computed: true, Description: "Rule name."},
					"index":   dschema.Int64Attribute{Computed: true, Description: "1-based position."},
					"enabled": dschema.BoolAttribute{Computed: true, Description: "Whether the rule is enforced."},
					"policy":  dschema.StringAttribute{Computed: true, Description: "`deny` or `permit`."},
				}},
			},
		},
	}
}

func (d *gatewayACLsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *gatewayACLsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m gatewayACLsDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	siteID, ok := d.data.mustSite(ctx, m.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	all, err := d.data.client.GatewayACLs(ctx, siteID)
	if err != nil {
		apiError(&resp.Diagnostics, "listing gateway ACLs", err)
		return
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Index < all[j].Index })
	vals := make([]attr.Value, 0, len(all))
	for _, a := range all {
		pol := "deny"
		if a.Policy == 1 {
			pol = "permit"
		}
		v, dd := types.ObjectValue(aclSummaryAttrTypes, map[string]attr.Value{
			"id": types.StringValue(a.ID), "name": types.StringValue(a.Name), "index": types.Int64Value(int64(a.Index)),
			"enabled": types.BoolValue(a.Status), "policy": types.StringValue(pol),
		})
		resp.Diagnostics.Append(dd...)
		vals = append(vals, v)
	}
	l, dd := types.ListValue(types.ObjectType{AttrTypes: aclSummaryAttrTypes}, vals)
	resp.Diagnostics.Append(dd...)
	m.ACLs = l
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
