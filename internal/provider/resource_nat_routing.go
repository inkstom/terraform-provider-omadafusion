package provider

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

var portRange = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)

// validPortRange accepts "443" or "8000-8010" within 1-65535, low to high.
func validPortRange(s string) bool {
	m := portRange.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	lo, _ := strconv.Atoi(m[1])
	hi := lo
	if m[2] != "" {
		hi, _ = strconv.Atoi(m[2])
	}
	return lo >= 1 && hi <= 65535 && lo <= hi
}

var nameToProto = map[string]int{"all": 0, "tcp": 1, "udp": 2}

// ======================= port forwarding =======================

var (
	_ resource.ResourceWithImportState    = &portForwardingResource{}
	_ resource.ResourceWithValidateConfig = &portForwardingResource{}
)

func newPortForwardingResource() resource.Resource { return &portForwardingResource{} }

type portForwardingResource struct{ data *providerData }

type portForwardingModel struct {
	ID           types.String `tfsdk:"id"`
	Site         types.String `tfsdk:"site"`
	Name         types.String `tfsdk:"name"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	WANPortIDs   types.List   `tfsdk:"wan_port_ids"`
	SourceCIDRs  types.List   `tfsdk:"source_cidrs"`
	ExternalPort types.String `tfsdk:"external_port"`
	ForwardIP    types.String `tfsdk:"forward_ip"`
	ForwardPort  types.String `tfsdk:"forward_port"`
	Protocol     types.String `tfsdk:"protocol"`
}

func (r *portForwardingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_forwarding"
}

func (r *portForwardingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A NAT port-forwarding (virtual server) rule on the gateway. Names must be unique. " +
			"Prefer `source_cidrs` to limit who can reach a forwarded port.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttribute("Rule ID."),
			"site":    siteAttribute(),
			"name":    schema.StringAttribute{Required: true, Description: "Rule name. Must be unique."},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Whether the rule is active."},
			"wan_port_ids": schema.ListAttribute{
				Required: true, ElementType: types.StringType,
				Description: "WAN port UUIDs, e.g. from `data.omadafusion_wan_ports`.",
			},
			"source_cidrs": schema.ListAttribute{
				Optional: true, ElementType: types.StringType,
				Description: "Only allow these source networks (`from = limited`). Omit to allow any source.",
			},
			"external_port": schema.StringAttribute{Required: true, Description: "Public port or range, e.g. `51820` or `8000-8010`."},
			"forward_ip":    schema.StringAttribute{Required: true, Description: "LAN address to forward to."},
			"forward_port":  schema.StringAttribute{Required: true, Description: "LAN port or range."},
			"protocol": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("all"),
				Description: "`all`, `tcp` or `udp`.",
			},
		},
	}
}

func (r *portForwardingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *portForwardingResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m portForwardingModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, a := range []struct {
		n string
		v types.String
	}{{"external_port", m.ExternalPort}, {"forward_port", m.ForwardPort}} {
		if !a.v.IsNull() && !a.v.IsUnknown() && !validPortRange(a.v.ValueString()) {
			resp.Diagnostics.AddAttributeError(path.Root(a.n), "Invalid port", "use a port (`443`) or range (`8000-8010`)")
		}
	}
	if !m.ForwardIP.IsNull() && !m.ForwardIP.IsUnknown() && net.ParseIP(m.ForwardIP.ValueString()).To4() == nil {
		resp.Diagnostics.AddAttributeError(path.Root("forward_ip"), "Invalid address", "must be an IPv4 address")
	}
	if !m.Protocol.IsNull() && !m.Protocol.IsUnknown() {
		if _, ok := nameToProto[m.Protocol.ValueString()]; !ok {
			resp.Diagnostics.AddAttributeError(path.Root("protocol"), "Invalid protocol", "must be all, tcp or udp")
		}
	}
	if !m.SourceCIDRs.IsNull() && !m.SourceCIDRs.IsUnknown() {
		cidrs, _ := knownElements(m.SourceCIDRs.Elements())
		for _, c := range cidrs {
			if _, _, err := net.ParseCIDR(c); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("source_cidrs"), "Invalid CIDR", fmt.Sprintf("%q is not a CIDR", c))
			}
		}
	}
}

func (m *portForwardingModel) toAPI(ctx context.Context, diags *diag.Diagnostics) client.PortForwarding {
	pf := client.PortForwarding{
		Name: m.Name.ValueString(), Status: m.Enabled.ValueBool(),
		WANPortIDs:   listToStrings(ctx, m.WANPortIDs, diags),
		ExternalPort: m.ExternalPort.ValueString(), ForwardIP: m.ForwardIP.ValueString(), ForwardPort: m.ForwardPort.ValueString(),
		Protocol: nameToProto[m.Protocol.ValueString()],
	}
	if src := listToStrings(ctx, m.SourceCIDRs, diags); len(src) > 0 {
		pf.From, pf.LimitedAddresses = 1, src
	}
	return pf
}

func (m *portForwardingModel) fromAPI(ctx context.Context, pf *client.PortForwarding, diags *diag.Diagnostics) {
	m.ID = types.StringValue(pf.ID)
	m.Name = types.StringValue(pf.Name)
	m.Enabled = types.BoolValue(pf.Status)
	m.WANPortIDs = stringsToList(ctx, pf.WANPortIDs, diags)
	if pf.From == 1 {
		m.SourceCIDRs = stringsToList(ctx, pf.LimitedAddresses, diags)
	} else {
		m.SourceCIDRs = types.ListNull(types.StringType)
	}
	m.ExternalPort = types.StringValue(pf.ExternalPort)
	m.ForwardIP = types.StringValue(pf.ForwardIP)
	m.ForwardPort = types.StringValue(pf.ForwardPort)
	for k, v := range nameToProto {
		if v == pf.Protocol {
			m.Protocol = types.StringValue(k)
		}
	}
}

func (r *portForwardingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan portForwardingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.client.CreatePortForwarding(ctx, siteID, plan.toAPI(ctx, &resp.Diagnostics))
	if err != nil {
		apiError(&resp.Diagnostics, "creating port forwarding rule", err)
		return
	}
	pf, err := r.data.client.PortForwarding(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back port forwarding rule", err)
		return
	}
	plan.fromAPI(ctx, pf, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *portForwardingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state portForwardingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	pf, err := r.data.client.PortForwarding(ctx, siteID, state.ID.ValueString())
	if handleReadErr(ctx, err, resp, "port forwarding rule") {
		return
	}
	state.fromAPI(ctx, pf, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *portForwardingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state portForwardingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.data.client.UpdatePortForwarding(ctx, siteID, id, plan.toAPI(ctx, &resp.Diagnostics)); err != nil {
		apiError(&resp.Diagnostics, "updating port forwarding rule", err)
		return
	}
	pf, err := r.data.client.PortForwarding(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back port forwarding rule", err)
		return
	}
	plan.fromAPI(ctx, pf, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *portForwardingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state portForwardingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok {
		return
	}
	if err := r.data.client.DeletePortForwarding(ctx, siteID, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting port forwarding rule", err)
	}
}

func (r *portForwardingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}

// ======================= static route =======================

var (
	_ resource.ResourceWithImportState    = &staticRouteResource{}
	_ resource.ResourceWithValidateConfig = &staticRouteResource{}
)

func newStaticRouteResource() resource.Resource { return &staticRouteResource{} }

type staticRouteResource struct{ data *providerData }

type staticRouteModel struct {
	ID           types.String `tfsdk:"id"`
	Site         types.String `tfsdk:"site"`
	Name         types.String `tfsdk:"name"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	Destinations types.List   `tfsdk:"destinations"`
	NextHop      types.String `tfsdk:"next_hop"`
	InterfaceID  types.String `tfsdk:"interface_id"`
	Metric       types.Int64  `tfsdk:"metric"`
}

func (r *staticRouteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_static_route"
}

func (r *staticRouteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A gateway static route (Network Config > Routing > Static Route), next-hop type. Names must be unique. " +
			"The controller does not report `interface_id` back, so changes to it made outside Terraform are not detected.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttribute("Route ID."),
			"site":    siteAttribute(),
			"name":    schema.StringAttribute{Required: true, Description: "Route name. Must be unique."},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Whether the route is active."},
			"destinations": schema.ListAttribute{
				Required: true, ElementType: types.StringType, Description: "Destination networks in CIDR form.",
			},
			"next_hop":     schema.StringAttribute{Required: true, Description: "Next-hop IPv4 address."},
			"interface_id": schema.StringAttribute{Required: true, Description: "Outgoing interface: a WAN port UUID or LAN network ID."},
			"metric": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "Metric, 0-15.",
			},
		},
	}
}

func (r *staticRouteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *staticRouteResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m staticRouteModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.NextHop.IsNull() && !m.NextHop.IsUnknown() && net.ParseIP(m.NextHop.ValueString()).To4() == nil {
		resp.Diagnostics.AddAttributeError(path.Root("next_hop"), "Invalid next hop", "must be an IPv4 address")
	}
	if !m.Metric.IsNull() && !m.Metric.IsUnknown() && (m.Metric.ValueInt64() < 0 || m.Metric.ValueInt64() > 15) {
		resp.Diagnostics.AddAttributeError(path.Root("metric"), "Invalid metric", "must be 0-15")
	}
	if !m.Destinations.IsNull() && !m.Destinations.IsUnknown() {
		ds, allKnown := knownElements(m.Destinations.Elements())
		if allKnown && len(ds) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("destinations"), "No destinations", "set at least one destination")
		}
		for _, d := range ds {
			if _, _, err := net.ParseCIDR(d); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("destinations"), "Invalid CIDR", fmt.Sprintf("%q is not a CIDR", d))
			}
		}
	}
}

func (m *staticRouteModel) toAPI(ctx context.Context, diags *diag.Diagnostics) client.StaticRoute {
	return client.StaticRoute{
		Name: m.Name.ValueString(), Status: m.Enabled.ValueBool(), Destinations: listToStrings(ctx, m.Destinations, diags),
		RouteType: 0, InterfaceID: m.InterfaceID.ValueString(), NextHopIP: m.NextHop.ValueString(), Metric: int(m.Metric.ValueInt64()),
	}
}

func (m *staticRouteModel) fromAPI(ctx context.Context, sr *client.StaticRoute, diags *diag.Diagnostics) {
	m.ID = types.StringValue(sr.ID)
	m.Name = types.StringValue(sr.Name)
	m.Enabled = types.BoolValue(sr.Status)
	m.Destinations = stringsToList(ctx, sr.Destinations, diags)
	m.NextHop = types.StringValue(sr.NextHopIP)
	if sr.InterfaceID != "" {
		m.InterfaceID = types.StringValue(sr.InterfaceID)
	}
	m.Metric = types.Int64Value(int64(sr.Metric))
}

func (r *staticRouteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan staticRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.client.CreateStaticRoute(ctx, siteID, plan.toAPI(ctx, &resp.Diagnostics))
	if err != nil {
		apiError(&resp.Diagnostics, "creating static route", err)
		return
	}
	sr, err := r.data.client.StaticRoute(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back static route", err)
		return
	}
	plan.fromAPI(ctx, sr, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *staticRouteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state staticRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	sr, err := r.data.client.StaticRoute(ctx, siteID, state.ID.ValueString())
	if handleReadErr(ctx, err, resp, "static route") {
		return
	}
	state.fromAPI(ctx, sr, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *staticRouteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state staticRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.data.client.UpdateStaticRoute(ctx, siteID, id, plan.toAPI(ctx, &resp.Diagnostics)); err != nil {
		apiError(&resp.Diagnostics, "updating static route", err)
		return
	}
	sr, err := r.data.client.StaticRoute(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back static route", err)
		return
	}
	plan.fromAPI(ctx, sr, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *staticRouteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state staticRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok {
		return
	}
	if err := r.data.client.DeleteStaticRoute(ctx, siteID, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting static route", err)
	}
}

func (r *staticRouteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}
