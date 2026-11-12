package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

var (
	_ resource.ResourceWithImportState    = &gatewayACLResource{}
	_ resource.ResourceWithValidateConfig = &gatewayACLResource{}
)

func newGatewayACLResource() resource.Resource { return &gatewayACLResource{} }

type gatewayACLResource struct{ data *providerData }

type gatewayACLModel struct {
	ID              types.String `tfsdk:"id"`
	Site            types.String `tfsdk:"site"`
	Name            types.String `tfsdk:"name"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	Policy          types.String `tfsdk:"policy"`
	Protocols       types.List   `tfsdk:"protocols"`
	SourceType      types.Int64  `tfsdk:"source_type"`
	SourceIDs       types.List   `tfsdk:"source_ids"`
	DestinationType types.Int64  `tfsdk:"destination_type"`
	DestinationIDs  types.List   `tfsdk:"destination_ids"`
	LANToLAN        types.Bool   `tfsdk:"lan_to_lan"`
	LANToWAN        types.Bool   `tfsdk:"lan_to_wan"`
	MatchStates     types.Set    `tfsdk:"match_states"`
	Syslog          types.Bool   `tfsdk:"syslog"`
	Index           types.Int64  `tfsdk:"index"`
}

var protoByName = map[string]int{"all": 256, "tcp": 6, "udp": 17, "icmp": 1, "igmp": 2, "gre": 47, "esp": 50, "ah": 51}

func protoName(n int) string {
	for k, v := range protoByName {
		if v == n {
			return k
		}
	}
	return strconv.Itoa(n)
}

func protoNumber(s string) (int, error) {
	if n, ok := protoByName[strings.ToLower(s)]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 256 {
		return 0, fmt.Errorf("%q is not a protocol name (all, tcp, udp, icmp, igmp, gre, esp, ah) or IP protocol number", s)
	}
	return n, nil
}

var validStates = []string{"established", "invalid", "new", "related"}

func (r *gatewayACLResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_acl"
}

func (r *gatewayACLResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A rule in the site's gateway ACL table (Network Security > ACL > Gateway ACL), using the controller's " +
			"site-level ACL mode. New rules are appended to the end of the table; order is reported in `index`. " +
			"Rule names must be unique so a new rule can be identified after creation.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("ACL rule ID."),
			"site": siteAttribute(),
			"name": schema.StringAttribute{Required: true, Description: "Rule name. Must be unique among gateway ACLs."},
			"enabled": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "Whether the rule is enforced. Rules saved disabled are listed but do nothing.",
			},
			"policy": schema.StringAttribute{Required: true, Description: "`deny` or `permit`."},
			"protocols": schema.ListAttribute{
				Optional: true, Computed: true, ElementType: types.StringType,
				Default:     listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{types.StringValue("all")})),
				Description: "Protocols to match: `all`, `tcp`, `udp`, `icmp`, `igmp`, `gre`, `esp`, `ah`, or IP protocol numbers. Defaults to `[\"all\"]`.",
			},
			"source_type": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(0),
				Description: "Source kind as the controller encodes it. `0` = LAN network (the only value verified on hardware).",
			},
			"source_ids": schema.ListAttribute{Required: true, ElementType: types.StringType, Description: "Source object IDs, e.g. LAN network IDs."},
			"destination_type": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(0),
				Description: "Destination kind. `0` = LAN network, `5` = gateway management (matches **all** traffic addressed to the gateway, including DNS and DHCP; pair it with `protocols = [\"tcp\"]` unless you mean that).",
			},
			"destination_ids": schema.ListAttribute{
				Optional: true, Computed: true, ElementType: types.StringType,
				Default:     listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})),
				Description: "Destination object IDs. Leave empty for destination types that take none.",
			},
			"lan_to_lan": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Apply to LAN-to-LAN traffic. Defaults to `true`."},
			"lan_to_wan": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Apply to LAN-to-WAN traffic. Defaults to `false`."},
			"match_states": schema.SetAttribute{
				Optional: true, Computed: true, ElementType: types.StringType,
				Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
				Description: "Connection states to match: `new`, `established`, `related`, `invalid`. Empty matches all packets. " +
					"`[\"new\"]` on a LAN-to-LAN deny makes it one-way: the source cannot open connections, but replies to connections opened by the other side still pass.",
			},
			"syslog": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Log matches to syslog."},
			"index": schema.Int64Attribute{
				Computed: true, Description: "1-based position in the gateway ACL table.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *gatewayACLResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *gatewayACLResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m gatewayACLModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Policy.IsUnknown() && !m.Policy.IsNull() {
		if p := m.Policy.ValueString(); p != "deny" && p != "permit" {
			resp.Diagnostics.AddAttributeError(path.Root("policy"), "Invalid policy", "must be `deny` or `permit`")
		}
	}
	if !m.Protocols.IsUnknown() && !m.Protocols.IsNull() {
		ps, allKnown := knownElements(m.Protocols.Elements())
		for _, p := range ps {
			if _, err := protoNumber(p); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("protocols"), "Invalid protocol", err.Error())
			}
		}
		if allKnown && len(ps) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("protocols"), "No protocols", "at least one protocol is required")
		}
	}
	if !m.MatchStates.IsUnknown() && !m.MatchStates.IsNull() {
		ss, _ := knownElements(m.MatchStates.Elements())
		for _, s := range ss {
			if i := sort.SearchStrings(validStates, s); i == len(validStates) || validStates[i] != s {
				resp.Diagnostics.AddAttributeError(path.Root("match_states"), "Invalid state", fmt.Sprintf("%q is not one of new, established, related, invalid", s))
			}
		}
	}
}

func (m *gatewayACLModel) toAPI(ctx context.Context, diags *diag.Diagnostics) client.GatewayACL {
	a := client.GatewayACL{
		Name:            m.Name.ValueString(),
		Status:          m.Enabled.ValueBool(),
		SourceType:      int(m.SourceType.ValueInt64()),
		SourceIDs:       listToStrings(ctx, m.SourceIDs, diags),
		DestinationType: int(m.DestinationType.ValueInt64()),
		DestinationIDs:  listToStrings(ctx, m.DestinationIDs, diags),
		Direction:       &client.ACLDirection{LANToLAN: m.LANToLAN.ValueBool(), LANToWAN: m.LANToWAN.ValueBool()},
		Syslog:          m.Syslog.ValueBool(),
	}
	if m.Policy.ValueString() == "permit" {
		a.Policy = 1
	}
	for _, p := range listToStrings(ctx, m.Protocols, diags) {
		n, err := protoNumber(p)
		if err != nil {
			diags.AddError("Invalid protocol", err.Error())
			continue
		}
		a.Protocols = append(a.Protocols, n)
	}
	var states []string
	if !m.MatchStates.IsNull() && !m.MatchStates.IsUnknown() {
		diags.Append(m.MatchStates.ElementsAs(ctx, &states, false)...)
	}
	st := &client.ACLStates{}
	for _, s := range states {
		switch s {
		case "new":
			st.New = true
		case "established":
			st.Established = true
		case "related":
			st.Related = true
		case "invalid":
			st.Invalid = true
		}
	}
	if len(states) > 0 {
		a.StateMode = 1
	}
	a.States = st
	return a
}

func (m *gatewayACLModel) fromAPI(ctx context.Context, a *client.GatewayACL, diags *diag.Diagnostics) {
	m.ID = types.StringValue(a.ID)
	m.Name = types.StringValue(a.Name)
	m.Enabled = types.BoolValue(a.Status)
	if a.Policy == 1 {
		m.Policy = types.StringValue("permit")
	} else {
		m.Policy = types.StringValue("deny")
	}
	protos := make([]string, 0, len(a.Protocols))
	for _, p := range a.Protocols {
		protos = append(protos, protoName(p))
	}
	// Keep the user's spelling (e.g. "6" vs "tcp") when it means the same thing.
	if prior := listToStrings(ctx, m.Protocols, diags); len(prior) == len(protos) {
		same := true
		for i := range prior {
			if n, err := protoNumber(prior[i]); err != nil || n != a.Protocols[i] {
				same = false
			}
		}
		if same {
			protos = prior
		}
	}
	m.Protocols = stringsToList(ctx, protos, diags)
	m.SourceType = types.Int64Value(int64(a.SourceType))
	m.SourceIDs = stringsToList(ctx, a.SourceIDs, diags)
	m.DestinationType = types.Int64Value(int64(a.DestinationType))
	m.DestinationIDs = stringsToList(ctx, a.DestinationIDs, diags)
	if a.Direction != nil {
		m.LANToLAN = types.BoolValue(a.Direction.LANToLAN)
		m.LANToWAN = types.BoolValue(a.Direction.LANToWAN)
	}
	states := []string{}
	if a.StateMode == 1 && a.States != nil {
		if a.States.New {
			states = append(states, "new")
		}
		if a.States.Established {
			states = append(states, "established")
		}
		if a.States.Related {
			states = append(states, "related")
		}
		if a.States.Invalid {
			states = append(states, "invalid")
		}
	}
	sv, d := types.SetValueFrom(ctx, types.StringType, states)
	diags.Append(d...)
	m.MatchStates = sv
	m.Syslog = types.BoolValue(a.Syslog)
	m.Index = types.Int64Value(int64(a.Index))
}

func (r *gatewayACLResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan gatewayACLModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, plan.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	body := plan.toAPI(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.data.client.CreateGatewayACL(ctx, siteID, body)
	if err != nil {
		apiError(&resp.Diagnostics, "creating gateway ACL", err)
		return
	}
	plan.fromAPI(ctx, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gatewayACLResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state gatewayACLModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	a, err := r.data.client.GatewayACL(ctx, siteID, state.ID.ValueString())
	if handleReadErr(ctx, err, resp, "gateway ACL") {
		return
	}
	state.fromAPI(ctx, a, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayACLResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan, state gatewayACLModel
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
	body := plan.toAPI(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.data.client.UpdateGatewayACL(ctx, siteID, id, body); err != nil {
		apiError(&resp.Diagnostics, "updating gateway ACL", err)
		return
	}
	a, err := r.data.client.GatewayACL(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back gateway ACL", err)
		return
	}
	plan.fromAPI(ctx, a, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gatewayACLResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state gatewayACLModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	if err := r.data.client.DeleteGatewayACL(ctx, siteID, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting gateway ACL", err)
	}
}

func (r *gatewayACLResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}
