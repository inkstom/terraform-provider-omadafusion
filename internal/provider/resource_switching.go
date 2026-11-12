package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

// ======================= port profile =======================

var _ resource.ResourceWithImportState = &portProfileResource{}

func newPortProfileResource() resource.Resource { return &portProfileResource{} }

type portProfileResource struct{ data *providerData }

type portProfileModel struct {
	ID               types.String `tfsdk:"id"`
	Site             types.String `tfsdk:"site"`
	Name             types.String `tfsdk:"name"`
	NativeNetworkID  types.String `tfsdk:"native_network_id"`
	TaggedNetworkIDs types.Set    `tfsdk:"tagged_network_ids"`
	SpanningTree     types.Bool   `tfsdk:"spanning_tree"`
	LoopbackDetect   types.Bool   `tfsdk:"loopback_detect"`
	PortIsolation    types.Bool   `tfsdk:"port_isolation"`
	LLDPMED          types.Bool   `tfsdk:"lldp_med"`
	EEE              types.Bool   `tfsdk:"eee"`
	FlowControl      types.Bool   `tfsdk:"flow_control"`
}

func (r *portProfileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_profile"
}

func boolAttr(def bool, desc string) schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(def), Description: desc}
}

func (r *portProfileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A switch port profile (Network Config > Switch Port Profiles): the native network, tagged networks and " +
			"port protections applied to every switch port that uses it. Assign profiles with `omadafusion_switch_port`.",
		Attributes: map[string]schema.Attribute{
			"id":                idAttribute("Port profile ID."),
			"site":              siteAttribute(),
			"name":              schema.StringAttribute{Required: true, Description: "Profile name."},
			"native_network_id": schema.StringAttribute{Optional: true, Description: "LAN network sent untagged. Omit for a tagged-only port."},
			"tagged_network_ids": schema.SetAttribute{
				Optional: true, Computed: true, ElementType: types.StringType,
				Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, nil)),
				Description: "LAN networks sent tagged.",
			},
			"spanning_tree":   boolAttr(true, "Participate in spanning tree."),
			"loopback_detect": boolAttr(false, "Enable port-based loopback detection."),
			"port_isolation":  boolAttr(false, "Isolate the port from other isolated ports on the switch."),
			"lldp_med":        boolAttr(true, "Enable LLDP-MED."),
			"eee":             boolAttr(false, "Energy Efficient Ethernet."),
			"flow_control":    boolAttr(false, "802.3x flow control."),
		},
	}
}

func (r *portProfileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (m *portProfileModel) apply(ctx context.Context, body map[string]any, diags *diag.Diagnostics) {
	body["name"] = m.Name.ValueString()
	if m.NativeNetworkID.IsNull() || m.NativeNetworkID.ValueString() == "" {
		delete(body, "nativeNetworkId")
	} else {
		body["nativeNetworkId"] = m.NativeNetworkID.ValueString()
	}
	tagged := setToStrings(ctx, m.TaggedNetworkIDs, diags)
	body["tagNetworkIds"] = tagged
	body["spanningTreeEnable"] = m.SpanningTree.ValueBool()
	body["loopbackDetectEnable"] = m.LoopbackDetect.ValueBool()
	body["portIsolationEnable"] = m.PortIsolation.ValueBool()
	body["lldpMedEnable"] = m.LLDPMED.ValueBool()
	body["eeeEnable"] = m.EEE.ValueBool()
	body["flowControlEnable"] = m.FlowControl.ValueBool()
}

func (m *portProfileModel) fromRaw(ctx context.Context, raw map[string]any, diags *diag.Diagnostics) {
	m.ID = types.StringValue(fmt.Sprint(raw["id"]))
	m.Name = types.StringValue(fmt.Sprint(raw["name"]))
	if v, ok := raw["nativeNetworkId"].(string); ok && v != "" {
		m.NativeNetworkID = types.StringValue(v)
	} else {
		m.NativeNetworkID = types.StringNull()
	}
	m.TaggedNetworkIDs = stringsToSet(ctx, stringsFromAny(raw["tagNetworkIds"]), diags)
	m.SpanningTree = types.BoolValue(raw["spanningTreeEnable"] == true)
	m.LoopbackDetect = types.BoolValue(raw["loopbackDetectEnable"] == true)
	m.PortIsolation = types.BoolValue(raw["portIsolationEnable"] == true)
	m.LLDPMED = types.BoolValue(raw["lldpMedEnable"] == true)
	m.EEE = types.BoolValue(raw["eeeEnable"] == true)
	m.FlowControl = types.BoolValue(raw["flowControlEnable"] == true)
}

func (r *portProfileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan portProfileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	body := client.DefaultPortProfileTemplate()
	plan.apply(ctx, body, &resp.Diagnostics)
	id, err := r.data.client.CreatePortProfile(ctx, siteID, body)
	if err != nil {
		apiError(&resp.Diagnostics, "creating port profile", err)
		return
	}
	raw, err := r.data.client.PortProfileRaw(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back port profile", err)
		return
	}
	plan.fromRaw(ctx, raw, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *portProfileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state portProfileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	raw, err := r.data.client.PortProfileRaw(ctx, siteID, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "reading port profile", err)
		return
	}
	state.fromRaw(ctx, raw, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *portProfileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state portProfileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	raw, err := r.data.client.PortProfileRaw(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading port profile", err)
		return
	}
	body := client.PortProfileWriteBody(raw)
	plan.apply(ctx, body, &resp.Diagnostics)
	if err := r.data.client.UpdatePortProfile(ctx, siteID, id, body); err != nil {
		apiError(&resp.Diagnostics, "updating port profile", err)
		return
	}
	raw, err = r.data.client.PortProfileRaw(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back port profile", err)
		return
	}
	plan.fromRaw(ctx, raw, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *portProfileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state portProfileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok {
		return
	}
	if err := r.data.client.DeletePortProfile(ctx, siteID, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting port profile", err)
	}
}

func (r *portProfileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}

// ======================= switch port =======================

var _ resource.ResourceWithImportState = &switchPortResource{}

func newSwitchPortResource() resource.Resource { return &switchPortResource{} }

type switchPortResource struct{ data *providerData }

type switchPortModel struct {
	ID             types.String `tfsdk:"id"`
	Site           types.String `tfsdk:"site"`
	SwitchMAC      macValue     `tfsdk:"switch_mac"`
	Port           types.Int64  `tfsdk:"port"`
	Name           types.String `tfsdk:"name"`
	ProfileID      types.String `tfsdk:"profile_id"`
	ProfileName    types.String `tfsdk:"profile_name"`
	ResetOnDestroy types.Bool   `tfsdk:"reset_on_destroy"`
}

func (r *switchPortResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_switch_port"
}

func (r *switchPortResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Name and port profile of one port on an adopted switch. The port always exists, so creating this resource " +
			"takes over the port and destroying it optionally resets it to `Port<N>` with the `All` profile. " +
			"Per-port overrides are not managed; put VLAN and protection settings in an `omadafusion_port_profile`.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("`<switch_mac>/<port>`."),
			"site": siteAttribute(),
			"switch_mac": schema.StringAttribute{
				Required: true, CustomType: macType{}, Description: "Switch MAC, any common format.",
				PlanModifiers: []planmodifier.String{macKeepState{}, stringplanmodifier.RequiresReplace()},
			},
			"port": schema.Int64Attribute{
				Required: true, Description: "Port number.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name":         schema.StringAttribute{Optional: true, Computed: true, Description: "Port name. Defaults to `Port<N>`."},
			"profile_id":   schema.StringAttribute{Required: true, Description: "Port profile ID."},
			"profile_name": schema.StringAttribute{Computed: true, Description: "Name of the assigned profile."},
			"reset_on_destroy": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "On destroy, rename the port to `Port<N>` and assign the `All` profile.",
			},
		},
	}
}

func (r *switchPortResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *switchPortResource) apply(ctx context.Context, m *switchPortModel, diags *diag.Diagnostics) {
	siteID, ok := r.data.mustSite(ctx, m.Site, diags)
	if !ok {
		return
	}
	mac := client.NormalizeMAC(m.SwitchMAC.ValueString())
	port := int(m.Port.ValueInt64())
	name := m.Name.ValueString()
	if m.Name.IsNull() || m.Name.IsUnknown() || name == "" {
		name = fmt.Sprintf("Port%d", port)
	}
	if err := r.data.client.SetSwitchPort(ctx, siteID, mac, port, name, m.ProfileID.ValueString()); err != nil {
		apiError(diags, "configuring switch port", err)
		return
	}
	r.read(ctx, siteID, m, diags)
}

func (r *switchPortResource) read(ctx context.Context, siteID string, m *switchPortModel, diags *diag.Diagnostics) bool {
	mac := client.NormalizeMAC(m.SwitchMAC.ValueString())
	p, err := r.data.client.SwitchPort(ctx, siteID, mac, int(m.Port.ValueInt64()))
	if err != nil {
		if client.IsNotFound(err) {
			return false
		}
		apiError(diags, "reading switch port", err)
		return true
	}
	m.ID = types.StringValue(fmt.Sprintf("%s/%d", mac, p.Port))
	m.Name = types.StringValue(p.Name)
	m.ProfileID = types.StringValue(p.ProfileID)
	m.ProfileName = types.StringValue(p.ProfileName)
	if p.Override {
		diags.AddWarning("Port profile override is enabled",
			fmt.Sprintf("Port %d on %s has per-port overrides set in the controller; this provider does not manage them.", p.Port, mac))
	}
	return true
}

func (r *switchPortResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan switchPortModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *switchPortResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state switchPortModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	if !r.read(ctx, siteID, &state, &resp.Diagnostics) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *switchPortResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan switchPortModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *switchPortResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state switchPortModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if !state.ResetOnDestroy.ValueBool() {
		return
	}
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok {
		return
	}
	all, err := r.data.client.PortProfileByName(ctx, siteID, "All")
	if err != nil {
		apiError(&resp.Diagnostics, "finding the All port profile", err)
		return
	}
	port := int(state.Port.ValueInt64())
	if err := r.data.client.SetSwitchPort(ctx, siteID, state.SwitchMAC.ValueString(), port, fmt.Sprintf("Port%d", port), fmt.Sprint(all["id"])); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "resetting switch port", err)
	}
}

func (r *switchPortResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// <switch_mac>/<port> or <site>/<switch_mac>/<port>
	parts := strings.Split(req.ID, "/")
	if len(parts) < 2 || len(parts) > 3 {
		resp.Diagnostics.AddError("Invalid import ID", `expected "<switch_mac>/<port>" or "<site>/<switch_mac>/<port>"`)
		return
	}
	n := len(parts)
	port, err := strconv.Atoi(parts[n-1])
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "port must be a number")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("switch_mac"), client.NormalizeMAC(parts[n-2]))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("port"), int64(port))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("reset_on_destroy"), true)...)
	if n == 3 {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("site"), parts[0])...)
	}
}
