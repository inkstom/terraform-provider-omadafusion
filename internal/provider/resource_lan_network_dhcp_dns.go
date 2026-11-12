package provider

import (
	"context"
	"net"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---------------------------------------------------------------------------
// omadafusion_lan_network_dhcp_dns
// ---------------------------------------------------------------------------

var (
	_ resource.ResourceWithImportState    = &lanDNSResource{}
	_ resource.ResourceWithValidateConfig = &lanDNSResource{}
)

func newLANNetworkDHCPDNSResource() resource.Resource { return &lanDNSResource{} }

type lanDNSResource struct{ data *providerData }

type lanDNSModel struct {
	ID             types.String `tfsdk:"id"`
	Site           types.String `tfsdk:"site"`
	NetworkID      types.String `tfsdk:"network_id"`
	PrimaryDNS     types.String `tfsdk:"primary_dns"`
	SecondaryDNS   types.String `tfsdk:"secondary_dns"`
	ResetOnDestroy types.Bool   `tfsdk:"reset_on_destroy"`
}

func (r *lanDNSResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lan_network_dhcp_dns"
}

func (r *lanDNSResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages only the DNS servers that an existing LAN network's DHCP server hands out. " +
			"The network itself is not created or deleted. Every other network field is read and sent back unchanged.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("Same as `network_id`."),
			"site": siteAttribute(),
			"network_id": schema.StringAttribute{
				Required: true, Description: "LAN network ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"primary_dns": schema.StringAttribute{
				Optional:    true,
				Description: "Primary DNS server handed out by DHCP. Leave both DNS attributes unset to hand out the gateway (`auto`).",
			},
			"secondary_dns": schema.StringAttribute{Optional: true, Description: "Secondary DNS server."},
			"reset_on_destroy": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "On destroy, set the network back to handing out the gateway (`auto`). Defaults to `true`.",
			},
		},
	}
}

func (r *lanDNSResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *lanDNSResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m lanDNSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	for _, a := range []struct {
		name string
		v    types.String
	}{{"primary_dns", m.PrimaryDNS}, {"secondary_dns", m.SecondaryDNS}} {
		if a.v.IsNull() || a.v.IsUnknown() {
			continue
		}
		if ip := net.ParseIP(a.v.ValueString()); ip == nil || ip.To4() == nil {
			resp.Diagnostics.AddAttributeError(path.Root(a.name), "Invalid address", "must be an IPv4 address")
		}
	}
	if m.PrimaryDNS.IsNull() && !m.SecondaryDNS.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("secondary_dns"), "Missing primary", "set `primary_dns` when `secondary_dns` is set")
	}
}

func (r *lanDNSResource) apply(ctx context.Context, m lanDNSModel) error {
	siteID, err := r.data.siteID(ctx, m.Site)
	if err != nil {
		return err
	}
	mode := "auto"
	if !m.PrimaryDNS.IsNull() && m.PrimaryDNS.ValueString() != "" {
		mode = "manual"
	}
	return r.data.client.SetLANNetworkDHCPDNS(ctx, siteID, m.NetworkID.ValueString(), mode, m.PrimaryDNS.ValueString(), m.SecondaryDNS.ValueString())
}

func (r *lanDNSResource) read(ctx context.Context, m *lanDNSModel) error {
	siteID, err := r.data.siteID(ctx, m.Site)
	if err != nil {
		return err
	}
	n, err := r.data.client.LANNetwork(ctx, siteID, m.NetworkID.ValueString())
	if err != nil {
		return err
	}
	m.ID = types.StringValue(n.ID)
	m.NetworkID = types.StringValue(n.ID)
	if n.DHCPSettings.DNSMode == "manual" {
		m.PrimaryDNS = types.StringValue(n.DHCPSettings.PrimaryDNS)
		if n.DHCPSettings.SecondaryDN != "" {
			m.SecondaryDNS = types.StringValue(n.DHCPSettings.SecondaryDN)
		} else {
			m.SecondaryDNS = types.StringNull()
		}
	} else {
		m.PrimaryDNS = types.StringNull()
		m.SecondaryDNS = types.StringNull()
	}
	return nil
}

func (r *lanDNSResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan lanDNSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, plan); err != nil {
		apiError(&resp.Diagnostics, "updating LAN network DHCP DNS", err)
		return
	}
	if err := r.read(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "reading back LAN network", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lanDNSResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state lanDNSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.NetworkID.IsNull() {
		state.NetworkID = state.ID
	}
	if handleReadErr(ctx, r.read(ctx, &state), resp, "LAN network") {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *lanDNSResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan lanDNSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, plan); err != nil {
		apiError(&resp.Diagnostics, "updating LAN network DHCP DNS", err)
		return
	}
	if err := r.read(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "reading back LAN network", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lanDNSResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state lanDNSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if !state.ResetOnDestroy.ValueBool() {
		return
	}
	state.PrimaryDNS, state.SecondaryDNS = types.StringNull(), types.StringNull()
	if err := r.apply(ctx, state); err != nil && !isNotFound(err) {
		apiError(&resp.Diagnostics, "resetting LAN network DHCP DNS", err)
	}
}

func (r *lanDNSResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "network_id")
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("reset_on_destroy"), true)...)
	}
}
