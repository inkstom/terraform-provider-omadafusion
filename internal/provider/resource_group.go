package provider

import (
	"context"
	"net"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

var (
	_ resource.ResourceWithImportState    = &groupResource{}
	_ resource.ResourceWithValidateConfig = &groupResource{}
)

func newGroupResource() resource.Resource { return &groupResource{} }

type groupResource struct{ data *providerData }

type groupModel struct {
	ID      types.String `tfsdk:"id"`
	Site    types.String `tfsdk:"site"`
	Name    types.String `tfsdk:"name"`
	Type    types.String `tfsdk:"type"`
	Subnets types.List   `tfsdk:"subnets"`
	Ports   types.List   `tfsdk:"ports"`
}

type subnetModel struct {
	CIDR        types.String `tfsdk:"cidr"`
	Description types.String `tfsdk:"description"`
}

var subnetAttrTypes = map[string]attr.Type{"cidr": types.StringType, "description": types.StringType}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An IP group or IP-port group (Profiles > Groups) for use in ACLs. " +
			"The controller does not support editing groups in place, so every change replaces the group. " +
			"Note: on Fusion 6.3.x, groups that list the gateway's own addresses do not match traffic addressed to the gateway; use `destination_type = 5` on the ACL instead.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("Group ID."),
			"site": siteAttribute(),
			"name": schema.StringAttribute{
				Required: true, Description: "Group name.",
			},
			"type": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("ip"),
				Description:   "`ip` (IP group) or `ip_port` (IP-port group). Defaults to `ip`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"subnets": schema.ListNestedAttribute{
				Required: true, Description: "Members in CIDR form, e.g. `192.168.10.0/24` or `192.168.10.5/32`.",

				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"cidr":        schema.StringAttribute{Required: true, Description: "IPv4 address with prefix length."},
					"description": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Member description."},
				}},
			},
			"ports": schema.ListAttribute{
				Optional: true, ElementType: types.StringType,
				Description: "Ports or ranges (`\"22\"`, `\"8000-8100\"`). Required for `ip_port`, not allowed for `ip`.",
			},
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *groupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t := m.Type.ValueString()
	if !m.Type.IsUnknown() && !m.Type.IsNull() && t != "ip" && t != "ip_port" {
		resp.Diagnostics.AddAttributeError(path.Root("type"), "Invalid type", "must be `ip` or `ip_port`")
	}
	hasPorts := !m.Ports.IsNull() && !m.Ports.IsUnknown() && len(m.Ports.Elements()) > 0
	if t == "ip_port" && !m.Ports.IsUnknown() && !hasPorts {
		resp.Diagnostics.AddAttributeError(path.Root("ports"), "Ports required", "`ip_port` groups need at least one port")
	}
	if (t == "ip" || m.Type.IsNull()) && hasPorts {
		resp.Diagnostics.AddAttributeError(path.Root("ports"), "Ports not allowed", "set `type = \"ip_port\"` to use ports")
	}
	if m.Subnets.IsUnknown() || m.Subnets.IsNull() {
		return
	}
	var subs []subnetModel
	resp.Diagnostics.Append(m.Subnets.ElementsAs(ctx, &subs, false)...)
	for i, s := range subs {
		if s.CIDR.IsUnknown() {
			continue
		}
		if ip, _, err := net.ParseCIDR(s.CIDR.ValueString()); err != nil || ip.To4() == nil {
			resp.Diagnostics.AddAttributeError(path.Root("subnets").AtListIndex(i).AtName("cidr"), "Invalid CIDR", "use IPv4 CIDR form, e.g. 192.168.10.0/24")
		}
	}
}

func groupTypeNum(s string) int {
	if s == "ip_port" {
		return 1
	}
	return 0
}

func (m *groupModel) toAPI(ctx context.Context, diags *diag.Diagnostics) client.Group {
	g := client.Group{Name: m.Name.ValueString(), Type: groupTypeNum(m.Type.ValueString())}
	var subs []subnetModel
	diags.Append(m.Subnets.ElementsAs(ctx, &subs, false)...)
	for _, s := range subs {
		ip, n, err := net.ParseCIDR(s.CIDR.ValueString())
		if err != nil {
			diags.AddError("Invalid CIDR", err.Error())
			continue
		}
		ones, _ := n.Mask.Size()
		g.IPList = append(g.IPList, client.GroupIP{IP: ip.String(), Mask: ones, Description: s.Description.ValueString()})
	}
	if g.Type == 1 {
		g.PortList = listToStrings(ctx, m.Ports, diags)
	}
	return g
}

func (m *groupModel) fromAPI(ctx context.Context, g *client.Group, diags *diag.Diagnostics) {
	m.ID = types.StringValue(g.ID)
	m.Name = types.StringValue(g.Name)
	if g.Type == 1 {
		m.Type = types.StringValue("ip_port")
		m.Ports = stringsToList(ctx, g.PortList, diags)
	} else {
		m.Type = types.StringValue("ip")
		m.Ports = types.ListNull(types.StringType)
	}
	subs := make([]subnetModel, 0, len(g.IPList))
	for _, ip := range g.IPList {
		subs = append(subs, subnetModel{
			CIDR:        types.StringValue(ip.IP + "/" + strconv.Itoa(ip.Mask)),
			Description: types.StringValue(ip.Description),
		})
	}
	l, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: subnetAttrTypes}, subs)
	diags.Append(d...)
	m.Subnets = l
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, plan.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	g := plan.toAPI(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.data.client.CreateGroup(ctx, siteID, g)
	if err != nil {
		apiError(&resp.Diagnostics, "creating group", err)
		return
	}
	plan.ID = types.StringValue(created.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	g, err := r.data.client.Group(ctx, siteID, state.ID.ValueString())
	if handleReadErr(ctx, err, resp, "group") {
		return
	}
	state.fromAPI(ctx, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan, state groupModel
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
	g := plan.toAPI(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.data.client.UpdateGroup(ctx, siteID, state.ID.ValueString(), g)
	if err != nil {
		apiError(&resp.Diagnostics, "updating group", err)
		return
	}
	plan.fromAPI(ctx, updated, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	err = r.data.client.DeleteGroup(ctx, siteID, groupTypeNum(state.Type.ValueString()), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting group", err)
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}
