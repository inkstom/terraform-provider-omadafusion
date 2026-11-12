package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

var (
	_ resource.ResourceWithImportState    = &timeRangeResource{}
	_ resource.ResourceWithValidateConfig = &timeRangeResource{}
)

func newTimeRangeResource() resource.Resource { return &timeRangeResource{} }

type timeRangeResource struct{ data *providerData }

type timeRangeModel struct {
	ID      types.String `tfsdk:"id"`
	Site    types.String `tfsdk:"site"`
	Name    types.String `tfsdk:"name"`
	Days    types.Set    `tfsdk:"days"`
	Periods types.List   `tfsdk:"periods"`
}

type periodModel struct {
	Start types.String `tfsdk:"start"`
	End   types.String `tfsdk:"end"`
}

var periodAttrTypes = map[string]attr.Type{"start": types.StringType, "end": types.StringType}

var dayOrder = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$|^24:00$`)

func (r *timeRangeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_time_range"
}

func (r *timeRangeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A time-range profile (Profiles > Time Range). Used by WLAN schedules. " +
			"The controller has no verified in-place edit for this object, so any change replaces it; " +
			"a profile referenced by an SSID schedule must be detached before it can be replaced.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("Time range ID."),
			"site": siteAttribute(),
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Profile name. Must be unique in the site.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"days": schema.SetAttribute{
				Required:      true,
				ElementType:   types.StringType,
				Description:   "Days the periods apply to: any of `mon`, `tue`, `wed`, `thu`, `fri`, `sat`, `sun`.",
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
			},
			"periods": schema.ListNestedAttribute{
				Required:      true,
				Description:   "On-intervals, in the site's time zone.",
				PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"start": schema.StringAttribute{Required: true, Description: "Start time, `HH:MM`."},
						"end":   schema.StringAttribute{Required: true, Description: "End time, `HH:MM` (`24:00` allowed)."},
					},
				},
			},
		},
	}
}

func (r *timeRangeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *timeRangeResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m timeRangeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Days.IsUnknown() && !m.Days.IsNull() {
		var days []string
		resp.Diagnostics.Append(m.Days.ElementsAs(ctx, &days, false)...)
		valid := map[string]bool{}
		for _, d := range dayOrder {
			valid[d] = true
		}
		for _, d := range days {
			if !valid[d] {
				resp.Diagnostics.AddAttributeError(path.Root("days"), "Invalid day", fmt.Sprintf("%q is not one of mon, tue, wed, thu, fri, sat, sun", d))
			}
		}
		if len(days) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("days"), "No days", "at least one day is required")
		}
	}
	if m.Periods.IsUnknown() || m.Periods.IsNull() {
		return
	}
	var ps []periodModel
	resp.Diagnostics.Append(m.Periods.ElementsAs(ctx, &ps, false)...)
	for i, p := range ps {
		if p.Start.IsUnknown() || p.End.IsUnknown() {
			continue
		}
		s, e := p.Start.ValueString(), p.End.ValueString()
		if !hhmm.MatchString(s) || !hhmm.MatchString(e) {
			resp.Diagnostics.AddAttributeError(path.Root("periods").AtListIndex(i), "Invalid time", "use HH:MM, 24-hour")
			continue
		}
		if minutes(s) >= minutes(e) {
			resp.Diagnostics.AddAttributeError(path.Root("periods").AtListIndex(i), "Invalid period", "start must be before end")
		}
	}
}

func minutes(s string) int {
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m
}

func (m *timeRangeModel) toAPI(ctx context.Context, diags *diag.Diagnostics) client.TimeRange {
	var days []string
	diags.Append(m.Days.ElementsAs(ctx, &days, false)...)
	set := map[string]bool{}
	for _, d := range days {
		set[d] = true
	}
	var ps []periodModel
	diags.Append(m.Periods.ElementsAs(ctx, &ps, false)...)
	tr := client.TimeRange{
		Name: m.Name.ValueString(), DayMode: 1,
		DayMon: set["mon"], DayTue: set["tue"], DayWed: set["wed"], DayThu: set["thu"],
		DayFri: set["fri"], DaySat: set["sat"], DaySun: set["sun"],
	}
	for _, p := range ps {
		s, e := minutes(p.Start.ValueString()), minutes(p.End.ValueString())
		tr.TimeList = append(tr.TimeList, client.TimeRangePeriod{
			DayType: 0, StartTimeH: s / 60, StartTimeM: s % 60, EndTimeH: e / 60, EndTimeM: e % 60,
		})
	}
	return tr
}

func (m *timeRangeModel) fromAPI(ctx context.Context, tr *client.TimeRange, diags *diag.Diagnostics) {
	m.ID = types.StringValue(tr.ID)
	m.Name = types.StringValue(tr.Name)
	flags := []bool{tr.DayMon, tr.DayTue, tr.DayWed, tr.DayThu, tr.DayFri, tr.DaySat, tr.DaySun}
	days := []string{}
	for i, on := range flags {
		if on {
			days = append(days, dayOrder[i])
		}
	}
	sort.Strings(days)
	ds, d := types.SetValueFrom(ctx, types.StringType, days)
	diags.Append(d...)
	m.Days = ds
	periods := make([]periodModel, 0, len(tr.TimeList))
	for _, t := range tr.TimeList {
		periods = append(periods, periodModel{
			Start: types.StringValue(fmt.Sprintf("%02d:%02d", t.StartTimeH, t.StartTimeM)),
			End:   types.StringValue(fmt.Sprintf("%02d:%02d", t.EndTimeH, t.EndTimeM)),
		})
	}
	pl, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: periodAttrTypes}, periods)
	diags.Append(d...)
	m.Periods = pl
}

func (r *timeRangeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan timeRangeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, plan.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	tr := plan.toAPI(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.data.client.CreateTimeRange(ctx, siteID, tr)
	if err != nil {
		apiError(&resp.Diagnostics, "creating time range", err)
		return
	}
	plan.ID = types.StringValue(created.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *timeRangeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state timeRangeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	tr, err := r.data.client.TimeRange(ctx, siteID, state.ID.ValueString())
	if handleReadErr(ctx, err, resp, "time range") {
		return
	}
	state.fromAPI(ctx, tr, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never called with changes because every attribute forces
// replacement; it only persists the planned state.
func (r *timeRangeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan timeRangeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *timeRangeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var state timeRangeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, err := r.data.siteID(ctx, state.Site)
	if err != nil {
		apiError(&resp.Diagnostics, "resolving site", err)
		return
	}
	if err := r.data.client.DeleteTimeRange(ctx, siteID, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting time range", err)
	}
}

func (r *timeRangeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "id")
}
