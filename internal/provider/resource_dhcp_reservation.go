package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

var _ resource.ResourceWithImportState = &dhcpReservationResource{}

func newDHCPReservationResource() resource.Resource { return &dhcpReservationResource{} }

type dhcpReservationResource struct{ data *providerData }

type dhcpReservationModel struct {
	ID              types.String `tfsdk:"id"`
	Site            types.String `tfsdk:"site"`
	MAC             macValue     `tfsdk:"mac"`
	IP              types.String `tfsdk:"ip"`
	NetworkID       types.String `tfsdk:"network_id"`
	Description     types.String `tfsdk:"description"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	ExportToBinding types.Bool   `tfsdk:"export_to_ip_mac_binding"`
}

func (r *dhcpReservationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_reservation"
}

func (r *dhcpReservationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A DHCP reservation on a gateway-served LAN network (Services > DHCP Reservation). " +
			"Reservations are keyed by MAC address. A client moves to its new address only after its lease renews.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("The reserved MAC address, in `AA-BB-CC-DD-EE-FF` form."),
			"site": siteAttribute(),
			"mac": schema.StringAttribute{
				Required:      true,
				CustomType:    macType{},
				Description:   "Client MAC address. `aa:bb:cc:dd:ee:ff` and `AA-BB-CC-DD-EE-FF` are both accepted and treated as equal.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ip":         schema.StringAttribute{Required: true, Description: "Reserved IPv4 address. Must be inside the network's subnet."},
			"network_id": schema.StringAttribute{Required: true, Description: "LAN network ID, e.g. from `data.omadafusion_lan_network`."},
			"description": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				Description: "Free-text description shown in the controller.",
			},
			"enabled": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "Whether the reservation is active. Defaults to `true`.",
			},
			"export_to_ip_mac_binding": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "Also export the pair to the gateway's IP-MAC binding table. Defaults to `true`.",
			},
		},
	}
}

func (r *dhcpReservationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (m dhcpReservationModel) toAPI() client.DHCPReservation {
	return client.DHCPReservation{
		MAC: m.MAC.ValueString(), IP: m.IP.ValueString(), NetID: m.NetworkID.ValueString(),
		Description: m.Description.ValueString(), Status: m.Enabled.ValueBool(),
		ExportToIPMACBinding: m.ExportToBinding.ValueBool(),
	}
}

func (m *dhcpReservationModel) fromAPI(x *client.DHCPReservation) {
	norm := client.NormalizeMAC(x.MAC)
	m.ID = types.StringValue(norm)
	if m.MAC.IsNull() || client.NormalizeMAC(m.MAC.ValueString()) != norm {
		m.MAC = newMACValue(norm)
	}
	m.IP = types.StringValue(x.IP)
	m.NetworkID = types.StringValue(x.NetID)
	m.Description = types.StringValue(x.Description)
	m.Enabled = types.BoolValue(x.Status)
	m.ExportToBinding = types.BoolValue(x.ExportToIPMACBinding)
}

func (r *dhcpReservationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan dhcpReservationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, plan.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	if err := r.data.client.CreateDHCPReservation(ctx, siteID, plan.toAPI()); err != nil {
		apiError(&resp.Diagnostics, "creating DHCP reservation", err)
		return
	}
	got, err := r.data.client.DHCPReservation(ctx, siteID, plan.MAC.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "reading back DHCP reservation", err)
		return
	}
	plan.fromAPI(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dhcpReservationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state dhcpReservationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	got, err := r.data.client.DHCPReservation(ctx, siteID, state.ID.ValueString())
	if handleReadErr(ctx, err, resp, "DHCP reservation") {
		return
	}
	state.fromAPI(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *dhcpReservationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan dhcpReservationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, plan.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	if err := r.data.client.UpdateDHCPReservation(ctx, siteID, plan.toAPI()); err != nil {
		apiError(&resp.Diagnostics, "updating DHCP reservation", err)
		return
	}
	got, err := r.data.client.DHCPReservation(ctx, siteID, plan.MAC.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "reading back DHCP reservation", err)
		return
	}
	plan.fromAPI(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dhcpReservationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state dhcpReservationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	if err := r.data.client.DeleteDHCPReservation(ctx, siteID, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting DHCP reservation", err)
	}
}

func (r *dhcpReservationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
	if resp.Diagnostics.HasError() {
		return
	}
	// mac is required; seed it from the imported ID so Read can complete.
	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, pathRoot("id"), &id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("mac"), client.NormalizeMAC(id.ValueString()))...)
}
