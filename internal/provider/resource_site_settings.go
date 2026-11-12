package provider

import (
	"context"
	"fmt"
	"net"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ======================= site settings =======================

// siteSettingsResource manages selected site-wide settings. Every attribute
// is optional: a null attribute is left alone, so the resource can adopt an
// existing site without taking over settings it does not mention. Deleting
// the resource only removes it from state.

var (
	_ resource.ResourceWithImportState    = &siteSettingsResource{}
	_ resource.ResourceWithValidateConfig = &siteSettingsResource{}
)

func newSiteSettingsResource() resource.Resource { return &siteSettingsResource{} }

type siteSettingsResource struct{ data *providerData }

type siteSettingsModel struct {
	ID                    types.String `tfsdk:"id"`
	Site                  types.String `tfsdk:"site"`
	TimeZone              types.String `tfsdk:"timezone"`
	NTPServers            types.List   `tfsdk:"ntp_servers"`
	LEDEnabled            types.Bool   `tfsdk:"led_enabled"`
	AutoUpgradeEnabled    types.Bool   `tfsdk:"auto_upgrade_enabled"`
	LLDPEnabled           types.Bool   `tfsdk:"lldp_enabled"`
	MeshEnabled           types.Bool   `tfsdk:"mesh_enabled"`
	BandSteeringEnabled   types.Bool   `tfsdk:"band_steering_enabled"`
	FastRoamingEnabled    types.Bool   `tfsdk:"fast_roaming_enabled"`
	AlertsEnabled         types.Bool   `tfsdk:"alerts_enabled"`
	SyslogEnabled         types.Bool   `tfsdk:"syslog_enabled"`
	SyslogHost            types.String `tfsdk:"syslog_host"`
	SyslogPort            types.Int64  `tfsdk:"syslog_port"`
	DeviceAccountUsername types.String `tfsdk:"device_account_username"`
	DeviceAccountPassword types.String `tfsdk:"device_account_password"`
	DeviceSSHEnabled      types.Bool   `tfsdk:"device_ssh_enabled"`
	DeviceSSHPort         types.Int64  `tfsdk:"device_ssh_port"`
	UPnPEnabled           types.Bool   `tfsdk:"upnp_enabled"`
	MDNSEnabled           types.Bool   `tfsdk:"mdns_enabled"`
}

func (r *siteSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_settings"
}

func optBool(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Description: desc + " Unset leaves it unmanaged."}
}

func (r *siteSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Site-wide settings (Site Settings and Services). Only attributes you set are managed; unset " +
			"attributes keep whatever the controller has. Destroying this resource leaves the settings as they are. " +
			"Use one instance per site.",
		Attributes: map[string]schema.Attribute{
			"id":       idAttribute("Site ID."),
			"site":     siteAttribute(),
			"timezone": schema.StringAttribute{Optional: true, Description: "IANA time zone, e.g. `America/New_York`."},
			"ntp_servers": schema.ListAttribute{
				Optional: true, ElementType: types.StringType,
				Description: "NTP servers pushed to devices. An empty list turns custom NTP off.",
			},
			"led_enabled":           optBool("Device LEDs on by default."),
			"auto_upgrade_enabled":  optBool("Automatic firmware upgrades for devices."),
			"lldp_enabled":          optBool("LLDP on devices."),
			"mesh_enabled":          optBool("AP mesh."),
			"band_steering_enabled": optBool("Band steering on APs."),
			"fast_roaming_enabled":  optBool("802.11r fast roaming."),
			"alerts_enabled":        optBool("Alert emails."),
			"syslog_enabled":        optBool("Remote syslog."),
			"syslog_host":           schema.StringAttribute{Optional: true, Description: "Remote syslog server. Used with `syslog_enabled`."},
			"syslog_port":           schema.Int64Attribute{Optional: true, Description: "Remote syslog port. Used with `syslog_enabled`."},
			"device_account_username": schema.StringAttribute{
				Optional: true, Description: "Username of the local account the controller sets on every device (SSH and standalone web UI).",
			},
			"device_account_password": schema.StringAttribute{
				Optional: true, Sensitive: true, Description: "Password of the device account. Set together with the username.",
			},
			"device_ssh_enabled": optBool("SSH access to devices (Services > SSH)."),
			"device_ssh_port":    schema.Int64Attribute{Optional: true, Description: "Device SSH port."},
			"upnp_enabled":       optBool("UPnP on the gateway."),
			"mdns_enabled":       optBool("mDNS reflector."),
		},
	}
}

func (r *siteSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *siteSettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m siteSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for name, v := range map[string]types.Int64{"syslog_port": m.SyslogPort, "device_ssh_port": m.DeviceSSHPort} {
		if !v.IsNull() && !v.IsUnknown() && (v.ValueInt64() < 1 || v.ValueInt64() > 65535) {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid port", "must be 1-65535")
		}
	}
	if v := m.SyslogHost; !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" && net.ParseIP(v.ValueString()) == nil {
		resp.Diagnostics.AddAttributeError(path.Root("syslog_host"), "Invalid host", "must be an IP address")
	}
	if m.DeviceAccountUsername.IsNull() != m.DeviceAccountPassword.IsNull() {
		resp.Diagnostics.AddError("Incomplete device account", "set both device_account_username and device_account_password, or neither")
	}
}

func set(v interface {
	IsNull() bool
	IsUnknown() bool
}) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func section(all map[string]any, key string) map[string]any {
	if s, ok := all[key].(map[string]any); ok {
		return deepCopyMap(s)
	}
	return map[string]any{}
}

// siteSectionKeys are the fields of the "site" section the controller accepts
// back on PATCH (the section must be sent complete).
var siteSectionKeys = []string{"name", "region", "timeZone", "scenario", "longitude", "latitude", "address", "dst", "ntpEnable", "ntpServers"}

func (r *siteSettingsResource) apply(ctx context.Context, siteID string, m *siteSettingsModel, diags *diag.Diagnostics) {
	all, err := r.data.client.SiteSettings(ctx, siteID)
	if err != nil {
		apiError(diags, "reading site settings", err)
		return
	}
	patch := map[string]any{}
	if set(m.TimeZone) || set(m.NTPServers) {
		cur := section(all, "site")
		s := map[string]any{}
		for _, k := range siteSectionKeys {
			if v, ok := cur[k]; ok {
				s[k] = v
			}
		}
		if set(m.TimeZone) {
			s["timeZone"] = m.TimeZone.ValueString()
		}
		if set(m.NTPServers) {
			srv := listToStrings(ctx, m.NTPServers, diags)
			addrs := make([]map[string]any, 0, len(srv))
			for _, a := range srv {
				addrs = append(addrs, map[string]any{"address": a})
			}
			s["ntpEnable"] = len(srv) > 0
			s["ntpServers"] = addrs
		}
		patch["site"] = s
	}
	setFlag := func(v types.Bool, sec, field string) {
		if set(v) {
			s, ok := patch[sec].(map[string]any)
			if !ok {
				s = section(all, sec)
			}
			s[field] = v.ValueBool()
			patch[sec] = s
		}
	}
	setFlag(m.LEDEnabled, "led", "enable")
	setFlag(m.AutoUpgradeEnabled, "autoUpgrade", "enable")
	setFlag(m.LLDPEnabled, "lldp", "enable")
	setFlag(m.MeshEnabled, "mesh", "meshEnable")
	setFlag(m.BandSteeringEnabled, "bandSteering", "enable")
	setFlag(m.FastRoamingEnabled, "roaming", "fastRoamingEnable")
	setFlag(m.AlertsEnabled, "alert", "enable")
	setFlag(m.SyslogEnabled, "remoteLog", "enable")
	if set(m.SyslogHost) || set(m.SyslogPort) {
		s, ok := patch["remoteLog"].(map[string]any)
		if !ok {
			s = section(all, "remoteLog")
		}
		if set(m.SyslogHost) {
			s["host"] = m.SyslogHost.ValueString()
		}
		if set(m.SyslogPort) {
			s["port"] = m.SyslogPort.ValueInt64()
		}
		patch["remoteLog"] = s
	}
	if set(m.DeviceAccountUsername) && set(m.DeviceAccountPassword) {
		patch["deviceAccount"] = map[string]any{"username": m.DeviceAccountUsername.ValueString(), "password": m.DeviceAccountPassword.ValueString()}
	}
	if len(patch) > 0 {
		if err := r.data.client.PatchSiteSettings(ctx, siteID, patch); err != nil {
			apiError(diags, "updating site settings", err)
			return
		}
	}
	r.putService(ctx, siteID, "ssh", diags, func(s map[string]any) bool {
		ch := false
		if set(m.DeviceSSHEnabled) {
			s["sshEnable"], ch = m.DeviceSSHEnabled.ValueBool(), true
		}
		if set(m.DeviceSSHPort) {
			s["sshServerPort"], ch = m.DeviceSSHPort.ValueInt64(), true
		}
		return ch
	})
	r.putService(ctx, siteID, "upnp", diags, func(s map[string]any) bool {
		if set(m.UPnPEnabled) {
			s["enable"] = m.UPnPEnabled.ValueBool()
			return true
		}
		return false
	})
	r.putService(ctx, siteID, "mdns", diags, func(s map[string]any) bool {
		if set(m.MDNSEnabled) {
			s["enable"] = m.MDNSEnabled.ValueBool()
			return true
		}
		return false
	})
}

// putService reads a /setting/<name> object, lets edit change it, and PUTs it
// back only when edit reports a change.
func (r *siteSettingsResource) putService(ctx context.Context, siteID, name string, diags *diag.Diagnostics, edit func(map[string]any) bool) {
	if diags.HasError() {
		return
	}
	cur, err := r.data.client.SiteServiceSetting(ctx, siteID, name)
	if err != nil {
		apiError(diags, "reading "+name+" setting", err)
		return
	}
	if cur == nil {
		cur = map[string]any{}
	}
	if !edit(cur) {
		return
	}
	if err := r.data.client.PutSiteServiceSetting(ctx, siteID, name, cur); err != nil {
		apiError(diags, "updating "+name+" setting", err)
	}
}

func boolField(sec map[string]any, k string) types.Bool {
	if b, ok := sec[k].(bool); ok {
		return types.BoolValue(b)
	}
	return types.BoolNull()
}

func int64Field(sec map[string]any, k string) types.Int64 {
	if f, ok := sec[k].(float64); ok {
		return types.Int64Value(int64(f))
	}
	return types.Int64Null()
}

// read refreshes only the attributes that are managed (non-null in m).
func (r *siteSettingsResource) read(ctx context.Context, siteID string, m *siteSettingsModel, diags *diag.Diagnostics) {
	all, err := r.data.client.SiteSettings(ctx, siteID)
	if err != nil {
		apiError(diags, "reading site settings", err)
		return
	}
	m.ID = types.StringValue(siteID)
	site := section(all, "site")
	if !m.TimeZone.IsNull() {
		m.TimeZone = types.StringValue(fmt.Sprint(site["timeZone"]))
	}
	if !m.NTPServers.IsNull() {
		var srv []string
		if on, _ := site["ntpEnable"].(bool); on {
			for _, e := range anySlice(site["ntpServers"]) {
				switch v := e.(type) {
				case map[string]any:
					srv = append(srv, fmt.Sprint(v["address"]))
				case string:
					srv = append(srv, v)
				}
			}
		}
		if srv == nil {
			srv = []string{}
		}
		m.NTPServers = stringsToList(ctx, srv, diags)
	}
	refresh := func(v *types.Bool, sec, field string) {
		if !v.IsNull() {
			*v = boolField(section(all, sec), field)
		}
	}
	refresh(&m.LEDEnabled, "led", "enable")
	refresh(&m.AutoUpgradeEnabled, "autoUpgrade", "enable")
	refresh(&m.LLDPEnabled, "lldp", "enable")
	refresh(&m.MeshEnabled, "mesh", "meshEnable")
	refresh(&m.BandSteeringEnabled, "bandSteering", "enable")
	refresh(&m.FastRoamingEnabled, "roaming", "fastRoamingEnable")
	refresh(&m.AlertsEnabled, "alert", "enable")
	refresh(&m.SyslogEnabled, "remoteLog", "enable")
	rl := section(all, "remoteLog")
	if !m.SyslogHost.IsNull() {
		h, _ := rl["host"].(string)
		m.SyslogHost = types.StringValue(h)
	}
	if !m.SyslogPort.IsNull() {
		m.SyslogPort = int64Field(rl, "port")
	}
	da := section(all, "deviceAccount")
	if !m.DeviceAccountUsername.IsNull() {
		u, _ := da["username"].(string)
		m.DeviceAccountUsername = types.StringValue(u)
	}
	if !m.DeviceAccountPassword.IsNull() {
		if p, ok := da["password"].(string); ok {
			m.DeviceAccountPassword = types.StringValue(p)
		}
	}
	if !m.DeviceSSHEnabled.IsNull() || !m.DeviceSSHPort.IsNull() {
		s, err := r.data.client.SiteServiceSetting(ctx, siteID, "ssh")
		if err != nil {
			apiError(diags, "reading ssh setting", err)
			return
		}
		if !m.DeviceSSHEnabled.IsNull() {
			m.DeviceSSHEnabled = boolField(s, "sshEnable")
		}
		if !m.DeviceSSHPort.IsNull() {
			m.DeviceSSHPort = int64Field(s, "sshServerPort")
		}
	}
	for _, x := range []struct {
		v    *types.Bool
		name string
	}{{&m.UPnPEnabled, "upnp"}, {&m.MDNSEnabled, "mdns"}} {
		if x.v.IsNull() {
			continue
		}
		s, err := r.data.client.SiteServiceSetting(ctx, siteID, x.name)
		if err != nil {
			apiError(diags, "reading "+x.name+" setting", err)
			return
		}
		*x.v = boolField(s, "enable")
	}
}

func anySlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func (r *siteSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan siteSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, siteID, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.read(ctx, siteID, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *siteSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state siteSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	site := state.Site
	if site.IsNull() && !state.ID.IsNull() {
		site = state.ID // imported: the import ID names the site
	}
	siteID, ok := r.data.mustSite(ctx, site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	r.read(ctx, siteID, &state, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *siteSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan siteSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, siteID, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.read(ctx, siteID, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *siteSettingsResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// ImportState takes a site name or ID. Imported state manages nothing until
// attributes are set in configuration; the next apply sets them. The import
// ID is not copied into `site`, so a configuration that relies on the
// provider's default site plans no replacement.
func (r *siteSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ======================= controller auto backup =======================

var (
	_ resource.ResourceWithImportState    = &autoBackupResource{}
	_ resource.ResourceWithValidateConfig = &autoBackupResource{}
)

var backupSchedules = map[string]int{"daily": 1, "weekly": 2, "monthly": 3}

func newAutoBackupResource() resource.Resource { return &autoBackupResource{} }

type autoBackupResource struct{ data *providerData }

type autoBackupModel struct {
	ID         types.String `tfsdk:"id"`
	Schedule   types.String `tfsdk:"schedule"`
	Hour       types.Int64  `tfsdk:"hour"`
	Minute     types.Int64  `tfsdk:"minute"`
	DayOfWeek  types.Int64  `tfsdk:"day_of_week"`
	DayOfMonth types.Int64  `tfsdk:"day_of_month"`
	MaxFiles   types.Int64  `tfsdk:"max_files"`
}

func (r *autoBackupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_auto_backup"
}

func (r *autoBackupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Scheduled controller backups (Maintenance > Backup > Auto Backup), saved on the controller. " +
			"Controller-wide: use one instance. Creating it turns auto backup on; destroying it turns auto backup off.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, Description: "Always `auto_backup`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"schedule":     schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("weekly"), Description: "`daily`, `weekly` or `monthly`."},
			"hour":         schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(3), Description: "Hour, 0-23, controller time zone."},
			"minute":       schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "Minute, 0-59."},
			"day_of_week":  schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "For `weekly`: 0 Sunday to 6 Saturday."},
			"day_of_month": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1), Description: "For `monthly`: 1-31."},
			"max_files":    schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(7), Description: "Backup files to keep."},
		},
	}
}

func (r *autoBackupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *autoBackupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m autoBackupModel
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
	}{"hour": {m.Hour, 0, 23}, "minute": {m.Minute, 0, 59}, "day_of_week": {m.DayOfWeek, 0, 6}, "day_of_month": {m.DayOfMonth, 1, 31}, "max_files": {m.MaxFiles, 1, 100}} {
		if set(x.v) && (x.v.ValueInt64() < x.lo || x.v.ValueInt64() > x.hi) {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Out of range", fmt.Sprintf("must be %d-%d", x.lo, x.hi))
		}
	}
}

func (r *autoBackupResource) put(ctx context.Context, m *autoBackupModel, enable bool, diags *diag.Diagnostics) {
	cur, err := r.data.client.AutoBackup(ctx)
	if err != nil {
		apiError(diags, "reading auto backup", err)
		return
	}
	body := deepCopyMap(cur)
	// Read-only fields: the controller rejects a PUT that echoes them
	// ("Invalid request parameters"). storageType only appears once auto
	// backup is enabled.
	delete(body, "nowStatus")
	delete(body, "storageType")
	body["enable"] = enable
	if m != nil {
		occ := map[string]any{"timingType": backupSchedules[m.Schedule.ValueString()], "hour": m.Hour.ValueInt64(), "minute": m.Minute.ValueInt64()}
		switch m.Schedule.ValueString() {
		case "weekly":
			occ["dayOfWeek"] = m.DayOfWeek.ValueInt64()
		case "monthly":
			occ["dayOfMonth"] = m.DayOfMonth.ValueInt64()
		}
		body["occurrence"] = occ
		body["maxNumberOfFile"] = m.MaxFiles.ValueInt64()
	}
	if err := r.data.client.PutAutoBackup(ctx, body); err != nil {
		apiError(diags, "updating auto backup", err)
	}
}

// read returns false when auto backup is off (the resource is gone).
func (r *autoBackupResource) read(ctx context.Context, m *autoBackupModel, diags *diag.Diagnostics) bool {
	cur, err := r.data.client.AutoBackup(ctx)
	if err != nil {
		apiError(diags, "reading auto backup", err)
		return true
	}
	m.ID = types.StringValue("auto_backup")
	if on, _ := cur["enable"].(bool); !on {
		return false
	}
	occ := section(cur, "occurrence")
	tt := int64Field(occ, "timingType").ValueInt64()
	for k, v := range backupSchedules {
		if int64(v) == tt {
			m.Schedule = types.StringValue(k)
		}
	}
	m.Hour = int64Field(occ, "hour")
	m.Minute = int64Field(occ, "minute")
	// The controller only stores the day field of the active schedule; keep
	// the other one as configured (or its default after import).
	if v := int64Field(occ, "dayOfWeek"); !v.IsNull() {
		m.DayOfWeek = v
	} else if m.DayOfWeek.IsNull() {
		m.DayOfWeek = types.Int64Value(0)
	}
	if v := int64Field(occ, "dayOfMonth"); !v.IsNull() {
		m.DayOfMonth = v
	} else if m.DayOfMonth.IsNull() {
		m.DayOfMonth = types.Int64Value(1)
	}
	m.MaxFiles = int64Field(cur, "maxNumberOfFile")
	return true
}

func (r *autoBackupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan autoBackupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &plan, true, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.read(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *autoBackupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state autoBackupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.read(ctx, &state, &resp.Diagnostics) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *autoBackupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan autoBackupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &plan, true, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.read(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *autoBackupResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	r.put(ctx, nil, false, &resp.Diagnostics)
}

func (r *autoBackupResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), "auto_backup")...)
}
