package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---------------- omadafusion_controller ----------------

func newControllerDataSource() datasource.DataSource { return &controllerDS{} }

type controllerDS struct{ data *providerData }

type controllerModel struct {
	ID                types.String `tfsdk:"id"`
	ControllerVersion types.String `tfsdk:"controller_version"`
	APIVersion        types.String `tfsdk:"api_version"`
	Configured        types.Bool   `tfsdk:"configured"`
	Type              types.Int64  `tfsdk:"type"`
	Category          types.String `tfsdk:"category"`
}

func (d *controllerDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_controller"
}

func (d *controllerDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Controller identity from the unauthenticated `/api/info` endpoint.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, Description: "Controller ID (`omadacId`)."},
			"controller_version": schema.StringAttribute{Computed: true, Description: "Controller software version, e.g. `6.3.14.2`."},
			"api_version":        schema.StringAttribute{Computed: true, Description: "Web API version."},
			"configured":         schema.BoolAttribute{Computed: true, Description: "Whether initial setup is complete."},
			"type":               schema.Int64Attribute{Computed: true, Description: "Controller type code reported by the controller."},
			"category":           schema.StringAttribute{Computed: true, Description: "Controller category, e.g. `advanced`."},
		},
	}
}

func (d *controllerDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *controllerDS) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	info, err := d.data.client.Info(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "reading controller info", err)
		return
	}
	m := controllerModel{
		ID: types.StringValue(info.OmadacID), ControllerVersion: types.StringValue(info.ControllerVersion),
		APIVersion: types.StringValue(info.APIVersion), Configured: types.BoolValue(info.Configured),
		Type: types.Int64Value(int64(info.Type)), Category: types.StringValue(info.Category),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------- omadafusion_site ----------------

func newSiteDataSource() datasource.DataSource { return &siteDS{} }

type siteDS struct{ data *providerData }

type siteModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (d *siteDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

func (d *siteDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a site by name. Defaults to the provider `site`.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Optional: true, Computed: true, Description: "Site name."},
			"id":   schema.StringAttribute{Computed: true, Description: "Site ID."},
		},
	}
}

func (d *siteDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *siteDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var m siteModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	name := d.data.defaultSite
	if !m.Name.IsNull() && m.Name.ValueString() != "" {
		name = m.Name.ValueString()
	}
	s, err := d.data.client.SiteByName(ctx, name)
	if err != nil {
		apiError(&resp.Diagnostics, "reading site", err)
		return
	}
	m.ID, m.Name = types.StringValue(s.ID), types.StringValue(s.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------- omadafusion_lan_network ----------------

func newLANNetworkDataSource() datasource.DataSource { return &lanNetDS{} }

type lanNetDS struct{ data *providerData }

type lanNetModel struct {
	Site          types.String `tfsdk:"site"`
	Name          types.String `tfsdk:"name"`
	ID            types.String `tfsdk:"id"`
	VLAN          types.Int64  `tfsdk:"vlan"`
	GatewaySubnet types.String `tfsdk:"gateway_subnet"`
	DHCPEnabled   types.Bool   `tfsdk:"dhcp_enabled"`
	DHCPStart     types.String `tfsdk:"dhcp_start"`
	DHCPEnd       types.String `tfsdk:"dhcp_end"`
	DNSMode       types.String `tfsdk:"dhcp_dns_mode"`
	Isolation     types.Bool   `tfsdk:"isolation"`
}

func (d *lanNetDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lan_network"
}

func (d *lanNetDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a LAN network by name. The default network matches both its plain name and the `Name(Default)` form.",
		Attributes: map[string]schema.Attribute{
			"site":           schema.StringAttribute{Optional: true, Description: "Site name or ID. Defaults to the provider `site`."},
			"name":           schema.StringAttribute{Required: true, Description: "Network name."},
			"id":             schema.StringAttribute{Computed: true, Description: "Network ID."},
			"vlan":           schema.Int64Attribute{Computed: true, Description: "VLAN ID."},
			"gateway_subnet": schema.StringAttribute{Computed: true, Description: "Gateway address with prefix, e.g. `192.168.10.1/24`."},
			"dhcp_enabled":   schema.BoolAttribute{Computed: true, Description: "Whether the gateway serves DHCP on this network."},
			"dhcp_start":     schema.StringAttribute{Computed: true, Description: "First address in the DHCP pool."},
			"dhcp_end":       schema.StringAttribute{Computed: true, Description: "Last address in the DHCP pool."},
			"dhcp_dns_mode":  schema.StringAttribute{Computed: true, Description: "`auto` (gateway) or `manual`."},
			"isolation":      schema.BoolAttribute{Computed: true, Description: "Whether client isolation is on."},
		},
	}
}

func (d *lanNetDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *lanNetDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var m lanNetModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	siteID, err := d.data.siteID(ctx, m.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	n, err := d.data.client.LANNetwork(ctx, siteID, m.Name.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "reading LAN network", err)
		return
	}
	m.ID = types.StringValue(n.ID)
	m.VLAN = types.Int64Value(int64(n.VLAN))
	m.GatewaySubnet = types.StringValue(n.GatewaySubnet)
	m.DHCPEnabled = types.BoolValue(n.DHCPSettings.Enable)
	m.DHCPStart = types.StringValue(n.DHCPSettings.IPStart)
	m.DHCPEnd = types.StringValue(n.DHCPSettings.IPEnd)
	m.DNSMode = types.StringValue(n.DHCPSettings.DNSMode)
	m.Isolation = types.BoolValue(n.Isolation)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------- omadafusion_ssid ----------------

func newSSIDDataSource() datasource.DataSource { return &ssidDS{} }

type ssidDS struct{ data *providerData }

type ssidModel struct {
	Site      types.String `tfsdk:"site"`
	Name      types.String `tfsdk:"name"`
	ID        types.String `tfsdk:"id"`
	WLANID    types.String `tfsdk:"wlan_id"`
	Broadcast types.Bool   `tfsdk:"broadcast"`
	Guest     types.Bool   `tfsdk:"guest_network"`
	Scheduled types.Bool   `tfsdk:"schedule_enabled"`
}

func (d *ssidDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssid"
}

func (d *ssidDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up an SSID by name across the site's WLAN groups. Security keys are never read into state.",
		Attributes: map[string]schema.Attribute{
			"site":             schema.StringAttribute{Optional: true, Description: "Site name or ID. Defaults to the provider `site`."},
			"name":             schema.StringAttribute{Required: true, Description: "SSID name."},
			"id":               schema.StringAttribute{Computed: true, Description: "SSID ID."},
			"wlan_id":          schema.StringAttribute{Computed: true, Description: "WLAN group ID."},
			"broadcast":        schema.BoolAttribute{Computed: true, Description: "Whether the SSID is broadcast."},
			"guest_network":    schema.BoolAttribute{Computed: true, Description: "Whether guest network isolation is on."},
			"schedule_enabled": schema.BoolAttribute{Computed: true, Description: "Whether a WLAN schedule is attached."},
		},
	}
}

func (d *ssidDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *ssidDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var m ssidModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	siteID, err := d.data.siteID(ctx, m.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	s, err := d.data.client.FindSSID(ctx, siteID, "", m.Name.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "reading SSID", err)
		return
	}
	m.ID, m.WLANID = types.StringValue(s.ID), types.StringValue(s.WLANID)
	m.Broadcast, m.Guest = types.BoolValue(s.Broadcast), types.BoolValue(s.GuestNetEnable)
	m.Scheduled = types.BoolValue(s.ScheduleEnable)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------- omadafusion_devices ----------------

func newDevicesDataSource() datasource.DataSource { return &devicesDS{} }

type devicesDS struct{ data *providerData }

type devicesModel struct {
	Site    types.String `tfsdk:"site"`
	Devices types.List   `tfsdk:"devices"`
}

var deviceAttrTypes = map[string]attr.Type{
	"mac": types.StringType, "name": types.StringType, "type": types.StringType, "model": types.StringType,
	"ip": types.StringType, "firmware_version": types.StringType, "connected": types.BoolType,
}

func (d *devicesDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_devices"
}

func (d *devicesDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the site's adopted gateways, switches and access points.",
		Attributes: map[string]schema.Attribute{
			"site": schema.StringAttribute{Optional: true, Description: "Site name or ID. Defaults to the provider `site`."},
			"devices": schema.ListNestedAttribute{
				Computed: true, Description: "Adopted devices.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"mac":              schema.StringAttribute{Computed: true, Description: "MAC address."},
					"name":             schema.StringAttribute{Computed: true, Description: "Device name."},
					"type":             schema.StringAttribute{Computed: true, Description: "`gateway`, `switch` or `ap`."},
					"model":            schema.StringAttribute{Computed: true, Description: "Model."},
					"ip":               schema.StringAttribute{Computed: true, Description: "Management IP."},
					"firmware_version": schema.StringAttribute{Computed: true, Description: "Firmware version."},
					"connected":        schema.BoolAttribute{Computed: true, Description: "Whether the controller reports the device as connected."},
				}},
			},
		},
	}
}

func (d *devicesDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *devicesDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var m devicesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	siteID, err := d.data.siteID(ctx, m.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	devs, err := d.data.client.Devices(ctx, siteID)
	if err != nil {
		apiError(&resp.Diagnostics, "listing devices", err)
		return
	}
	vals := make([]attr.Value, 0, len(devs))
	for _, x := range devs {
		o, diags := types.ObjectValue(deviceAttrTypes, map[string]attr.Value{
			"mac": types.StringValue(x.MAC), "name": types.StringValue(x.Name), "type": types.StringValue(x.Type),
			"model": types.StringValue(x.Model), "ip": types.StringValue(x.IP),
			"firmware_version": types.StringValue(x.FirmwareVersion),
			// Status 14 is "Connected" on 6.x controllers.
			"connected": types.BoolValue(x.Status == 14),
		})
		resp.Diagnostics.Append(diags...)
		vals = append(vals, o)
	}
	l, diags := types.ListValue(types.ObjectType{AttrTypes: deviceAttrTypes}, vals)
	resp.Diagnostics.Append(diags...)
	m.Devices = l
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
