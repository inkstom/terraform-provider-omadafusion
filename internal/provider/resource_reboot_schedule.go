package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

// ======================= reboot schedule =======================

var (
	_ resource.ResourceWithImportState    = &rebootScheduleResource{}
	_ resource.ResourceWithValidateConfig = &rebootScheduleResource{}
)

func newRebootScheduleResource() resource.Resource { return &rebootScheduleResource{} }

type rebootScheduleResource struct{ data *providerData }

type rebootScheduleModel struct {
	ID         types.String `tfsdk:"id"`
	Site       types.String `tfsdk:"site"`
	Name       types.String `tfsdk:"name"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	DeviceMACs types.Set    `tfsdk:"device_macs"`
	Schedule   types.String `tfsdk:"schedule"`
	Hour       types.Int64  `tfsdk:"hour"`
	Minute     types.Int64  `tfsdk:"minute"`
	DayOfWeek  types.Int64  `tfsdk:"day_of_week"`
	DayOfMonth types.Int64  `tfsdk:"day_of_month"`
}

func (r *rebootScheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_reboot_schedule"
}

func (r *rebootScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Scheduled device reboots (Services > Reboot Schedule). Names must be unique.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttribute("Schedule ID."),
			"site":    siteAttribute(),
			"name":    schema.StringAttribute{Required: true, Description: "Schedule name. Must be unique."},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Whether the schedule runs."},
			"device_macs": schema.SetAttribute{
				Required: true, ElementType: types.StringType,
				Description: "MACs of adopted devices to reboot, in `AA-BB-CC-DD-EE-FF` form (as `omadafusion_devices` reports them).",
			},
			"schedule":     schema.StringAttribute{Required: true, Description: "`daily`, `weekly` or `monthly`."},
			"hour":         schema.Int64Attribute{Required: true, Description: "Hour, 0-23, site time zone."},
			"minute":       schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "Minute, 0-59."},
			"day_of_week":  schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "For `weekly`: 0 Sunday to 6 Saturday."},
			"day_of_month": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1), Description: "For `monthly`: 1-31."},
		},
	}
}

func (r *rebootScheduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *rebootScheduleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m rebootScheduleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if set(m.Schedule) {
		if _, ok := backupSchedules[m.Schedule.ValueString()]; !ok {
			resp.Diagnostics.AddAttributeError(path.Root("schedule"), "Invalid schedule", "must be daily, weekly or monthly")
		}
	}
	for name, x := range map[string]struct {
		v      types.Int64
		lo, hi int64
	}{"hour": {m.Hour, 0, 23}, "minute": {m.Minute, 0, 59}, "day_of_week": {m.DayOfWeek, 0, 6}, "day_of_month": {m.DayOfMonth, 1, 31}} {
		if set(x.v) && (x.v.ValueInt64() < x.lo || x.v.ValueInt64() > x.hi) {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Out of range", fmt.Sprintf("must be %d-%d", x.lo, x.hi))
		}
	}
	if set(m.DeviceMACs) {
		macs, allKnown := knownElements(m.DeviceMACs.Elements())
		if allKnown && len(macs) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("device_macs"), "No devices", "list at least one device")
		}
		for _, mac := range macs {
			if mac != client.NormalizeMAC(mac) {
				resp.Diagnostics.AddAttributeError(path.Root("device_macs"), "Use the controller's MAC form",
					fmt.Sprintf("write %q as %q", mac, client.NormalizeMAC(mac)))
			}
		}
	}
}

func (m *rebootScheduleModel) toAPI(ctx context.Context, diags *diag.Diagnostics) client.RebootSchedule {
	occ := map[string]any{"timingType": backupSchedules[m.Schedule.ValueString()], "hour": m.Hour.ValueInt64(), "minute": m.Minute.ValueInt64()}
	switch m.Schedule.ValueString() {
	case "weekly":
		occ["dayOfWeek"] = m.DayOfWeek.ValueInt64()
	case "monthly":
		occ["dayOfMonth"] = m.DayOfMonth.ValueInt64()
	}
	macs := setToStrings(ctx, m.DeviceMACs, diags)
	sort.Strings(macs)
	return client.RebootSchedule{Name: m.Name.ValueString(), Status: m.Enabled.ValueBool(), DeviceMACs: macs, Occurrence: occ}
}

func (m *rebootScheduleModel) fromAPI(ctx context.Context, rs *client.RebootSchedule, diags *diag.Diagnostics) {
	m.ID = types.StringValue(rs.ID)
	m.Name = types.StringValue(rs.Name)
	m.Enabled = types.BoolValue(rs.Status)
	m.DeviceMACs = stringsToSet(ctx, rs.DeviceMACs, diags)
	tt := int64Field(rs.Occurrence, "timingType").ValueInt64()
	for k, v := range backupSchedules {
		if int64(v) == tt {
			m.Schedule = types.StringValue(k)
		}
	}
	m.Hour = int64Field(rs.Occurrence, "hour")
	m.Minute = int64Field(rs.Occurrence, "minute")
	if v := int64Field(rs.Occurrence, "dayOfWeek"); !v.IsNull() {
		m.DayOfWeek = v
	} else if m.DayOfWeek.IsNull() {
		m.DayOfWeek = types.Int64Value(0)
	}
	if v := int64Field(rs.Occurrence, "dayOfMonth"); !v.IsNull() {
		m.DayOfMonth = v
	} else if m.DayOfMonth.IsNull() {
		m.DayOfMonth = types.Int64Value(1)
	}
}

func (r *rebootScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan rebootScheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.client.CreateRebootSchedule(ctx, siteID, plan.toAPI(ctx, &resp.Diagnostics))
	if err != nil {
		apiError(&resp.Diagnostics, "creating reboot schedule", err)
		return
	}
	rs, err := r.data.client.RebootSchedule(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back reboot schedule", err)
		return
	}
	plan.fromAPI(ctx, rs, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rebootScheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state rebootScheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	rs, err := r.data.client.RebootSchedule(ctx, siteID, state.ID.ValueString())
	if handleReadErr(ctx, err, resp, "reboot schedule") {
		return
	}
	state.fromAPI(ctx, rs, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *rebootScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state rebootScheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.data.client.UpdateRebootSchedule(ctx, siteID, id, plan.toAPI(ctx, &resp.Diagnostics)); err != nil {
		apiError(&resp.Diagnostics, "updating reboot schedule", err)
		return
	}
	rs, err := r.data.client.RebootSchedule(ctx, siteID, id)
	if err != nil {
		apiError(&resp.Diagnostics, "reading back reboot schedule", err)
		return
	}
	plan.fromAPI(ctx, rs, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rebootScheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state rebootScheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok {
		return
	}
	if err := r.data.client.DeleteRebootSchedule(ctx, siteID, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting reboot schedule", err)
	}
}

func (r *rebootScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}
