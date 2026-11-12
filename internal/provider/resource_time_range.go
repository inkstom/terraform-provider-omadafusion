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
		Description: "A time-range profile (Profiles > Time Range). Used by WLAN schedules and ACLs. " +
			"Changes are applied in place, so a profile can be edited while an SSID schedule uses it. " +
			"The controller's day mode is derived from `days`: all seven days is Every Day, mon-fri is Weekday, " +
			"sat+sun is Weekend, and anything else is Customized with the same periods on each selected day.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("Time range ID."),
			"site": siteAttribute(),
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Profile name. Must be unique in the site.",
			},
			"days": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Days the periods apply to: any of `mon`, `tue`, `wed`, `thu`, `fri`, `sat`, `sun`.",
			},
			"periods": schema.ListNestedAttribute{
				Required:    true,
				Description: "On-intervals, in the site's time zone.",
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
		Name:   m.Name.ValueString(),
		DayMon: set["mon"], DayTue: set["tue"], DayWed: set["wed"], DayThu: set["thu"],
		DayFri: set["fri"], DaySat: set["sat"], DaySun: set["sun"],
	}
	weekday := set["mon"] && set["tue"] && set["wed"] && set["thu"] && set["fri"]
	weekend := set["sat"] && set["sun"]
	// dayType 0 means "every selected day"; in Customized mode each entry
	// names one day (1 = Monday ... 7 = Sunday). Weekday mode silently drops
	// weekend flags and vice versa, so only use them for exact matches.
	switch {
	case len(set) == 7:
		tr.DayMode = client.DayModeEveryday
	case len(set) == 5 && weekday:
		tr.DayMode = client.DayModeWeekday
	case len(set) == 2 && weekend:
		tr.DayMode = client.DayModeWeekend
	default:
		tr.DayMode = client.DayModeCustom
	}
	period := func(dayType int, p periodModel) client.TimeRangePeriod {
		s, e := minutes(p.Start.ValueString()), minutes(p.End.ValueString())
		return client.TimeRangePeriod{DayType: dayType, StartTimeH: s / 60, StartTimeM: s % 60, EndTimeH: e / 60, EndTimeM: e % 60}
	}
	if tr.DayMode != client.DayModeCustom {
		for _, p := range ps {
			tr.TimeList = append(tr.TimeList, period(0, p))
		}
		return tr
	}
	for i, d := range dayOrder {
		if !set[d] {
			continue
		}
		for _, p := range ps {
			tr.TimeList = append(tr.TimeList, period(i+1, p))
		}
	}
	return tr
}

func (m *timeRangeModel) fromAPI(ctx context.Context, tr *client.TimeRange, diags *diag.Diagnostics) {
	m.ID = types.StringValue(tr.ID)
	m.Name = types.StringValue(tr.Name)
	flags := []bool{tr.DayMon, tr.DayTue, tr.DayWed, tr.DayThu, tr.DayFri, tr.DaySat, tr.DaySun}
	switch tr.DayMode {
	case client.DayModeEveryday:
		flags = []bool{true, true, true, true, true, true, true}
	case client.DayModeWeekday:
		flags = []bool{true, true, true, true, true, false, false}
	case client.DayModeWeekend:
		flags = []bool{false, false, false, false, false, true, true}
	}
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
	// In Customized mode the same periods are repeated per day; report the
	// distinct periods in first-seen order.
	periods := make([]periodModel, 0, len(tr.TimeList))
	seen := map[string]bool{}
	for _, t := range tr.TimeList {
		s, e := fmt.Sprintf("%02d:%02d", t.StartTimeH, t.StartTimeM), fmt.Sprintf("%02d:%02d", t.EndTimeH, t.EndTimeM)
		if seen[s+"-"+e] {
			continue
		}
		seen[s+"-"+e] = true
		periods = append(periods, periodModel{Start: types.StringValue(s), End: types.StringValue(e)})
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
	plan.fromAPI(ctx, created, &resp.Diagnostics)
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

func (r *timeRangeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data == nil {
		notConfigured(&resp.Diagnostics)
		return
	}
	var plan, state timeRangeModel
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
	tr := plan.toAPI(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.data.client.UpdateTimeRange(ctx, siteID, state.ID.ValueString(), tr)
	if err != nil {
		apiError(&resp.Diagnostics, "updating time range", err)
		return
	}
	plan.fromAPI(ctx, updated, &resp.Diagnostics)
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
