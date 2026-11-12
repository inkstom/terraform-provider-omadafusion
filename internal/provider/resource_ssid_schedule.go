package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---------------------------------------------------------------------------
// omadafusion_ssid_schedule
// ---------------------------------------------------------------------------

var (
	_ resource.ResourceWithImportState    = &ssidScheduleResource{}
	_ resource.ResourceWithValidateConfig = &ssidScheduleResource{}
)

func newSSIDScheduleResource() resource.Resource { return &ssidScheduleResource{} }

type ssidScheduleResource struct{ data *providerData }

type ssidScheduleModel struct {
	ID          types.String `tfsdk:"id"`
	Site        types.String `tfsdk:"site"`
	WLANID      types.String `tfsdk:"wlan_id"`
	SSIDID      types.String `tfsdk:"ssid_id"`
	TimeRangeID types.String `tfsdk:"time_range_id"`
	Action      types.String `tfsdk:"action"`
}

func (r *ssidScheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssid_schedule"
}

func (r *ssidScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Attaches a WLAN schedule to an existing SSID, e.g. to switch guest Wi-Fi off after hours. " +
			"Only the schedule fields change; the SSID and its other settings are read and sent back unchanged. " +
			"Destroying this resource disables the schedule, leaving the SSID broadcasting normally.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("Same as `ssid_id`."),
			"site": siteAttribute(),
			"wlan_id": schema.StringAttribute{
				Optional: true, Computed: true,
				Description:   "WLAN group ID. Looked up from the SSID when omitted.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()},
			},
			"ssid_id": schema.StringAttribute{
				Required: true, Description: "SSID ID, e.g. from `data.omadafusion_ssid`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"time_range_id": schema.StringAttribute{Required: true, Description: "ID of an `omadafusion_time_range`."},
			"action": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("radio_on"),
				Description: "`radio_on` (default): broadcast only inside the time range. `radio_off`: stop broadcasting inside the time range.",
			},
		},
	}
}

func (r *ssidScheduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *ssidScheduleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m ssidScheduleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if !m.Action.IsNull() && !m.Action.IsUnknown() {
		if a := m.Action.ValueString(); a != "radio_on" && a != "radio_off" {
			resp.Diagnostics.AddAttributeError(path.Root("action"), "Invalid action", "must be `radio_on` or `radio_off`")
		}
	}
}

func actionNum(s string) int {
	if s == "radio_off" {
		return 0
	}
	return 1
}

func (r *ssidScheduleResource) set(ctx context.Context, m ssidScheduleModel, enable bool) error {
	siteID, err := r.data.siteID(ctx, m.Site)
	if err != nil {
		return err
	}
	wlan := ""
	if !m.WLANID.IsNull() && !m.WLANID.IsUnknown() {
		wlan = m.WLANID.ValueString()
	}
	return r.data.client.SetSSIDSchedule(ctx, siteID, wlan, m.SSIDID.ValueString(), enable, actionNum(m.Action.ValueString()), m.TimeRangeID.ValueString())
}

// read returns false when the SSID exists but has no schedule (drift that
// should recreate the resource).
func (r *ssidScheduleResource) read(ctx context.Context, m *ssidScheduleModel) (bool, error) {
	siteID, err := r.data.siteID(ctx, m.Site)
	if err != nil {
		return false, err
	}
	wlan := ""
	if !m.WLANID.IsNull() && !m.WLANID.IsUnknown() {
		wlan = m.WLANID.ValueString()
	}
	s, err := r.data.client.FindSSID(ctx, siteID, wlan, m.SSIDID.ValueString())
	if err != nil {
		return false, err
	}
	m.ID = types.StringValue(s.ID)
	m.SSIDID = types.StringValue(s.ID)
	m.WLANID = types.StringValue(s.WLANID)
	if !s.ScheduleEnable {
		return false, nil
	}
	m.TimeRangeID = types.StringValue(s.ScheduleID)
	if s.ScheduleAction != nil && *s.ScheduleAction == 0 {
		m.Action = types.StringValue("radio_off")
	} else {
		m.Action = types.StringValue("radio_on")
	}
	return true, nil
}

func (r *ssidScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan ssidScheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.set(ctx, plan, true); err != nil {
		apiError(&resp.Diagnostics, "enabling SSID schedule", err)
		return
	}
	if _, err := r.read(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "reading back SSID", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ssidScheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state ssidScheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	enabled, err := r.read(ctx, &state)
	if handleReadErr(ctx, err, resp, "SSID") {
		return
	}
	if !enabled {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ssidScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan ssidScheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.set(ctx, plan, true); err != nil {
		apiError(&resp.Diagnostics, "updating SSID schedule", err)
		return
	}
	if _, err := r.read(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "reading back SSID", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ssidScheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state ssidScheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.set(ctx, state, false); err != nil && !isNotFound(err) {
		apiError(&resp.Diagnostics, "disabling SSID schedule", err)
	}
}

func (r *ssidScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "ssid_id")
}
