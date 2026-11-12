package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

// ======================= adopted device settings =======================

var (
	_ resource.ResourceWithImportState    = &deviceResource{}
	_ resource.ResourceWithValidateConfig = &deviceResource{}
)

var ledModes = map[string]int{"off": 0, "on": 1, "site": 2}

func newDeviceResource() resource.Resource { return &deviceResource{} }

type deviceResource struct{ data *providerData }

type radioAttrs struct {
	Enabled      types.Bool   `tfsdk:"enabled"`
	Channel      types.String `tfsdk:"channel"`
	ChannelWidth types.String `tfsdk:"channel_width"`
	TxPowerLevel types.Int64  `tfsdk:"tx_power_level"`
}

type deviceModel struct {
	ID       types.String `tfsdk:"id"`
	Site     types.String `tfsdk:"site"`
	MAC      macValue     `tfsdk:"mac"`
	Type     types.String `tfsdk:"type"`
	Model    types.String `tfsdk:"model"`
	Name     types.String `tfsdk:"name"`
	LED      types.String `tfsdk:"led"`
	Radio2G  *radioAttrs  `tfsdk:"radio_2g"`
	Radio5G  *radioAttrs  `tfsdk:"radio_5g"`
	Firmware types.String `tfsdk:"firmware_version"`
	IP       types.String `tfsdk:"ip"`
}

func (r *deviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device"
}

func radioSchema(band string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: band + " radio of an access point. Only attributes you set are managed.",
		Attributes: map[string]schema.Attribute{
			"enabled":        schema.BoolAttribute{Optional: true, Description: "Radio on or off."},
			"channel":        schema.StringAttribute{Optional: true, Description: "Channel number, or `0` for auto."},
			"channel_width":  schema.StringAttribute{Optional: true, Description: "Channel width code as the controller stores it (for example `2` 20 MHz, `3` 40 MHz, `5` 80 MHz, `4`/`6` 20/40 or 20/40/80 auto)."},
			"tx_power_level": schema.Int64Attribute{Optional: true, Description: "Transmit power level: 0 low, 1 medium, 2 high, 3 custom, 4 auto."},
		},
	}
}

func (r *deviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Settings of an adopted device (gateway, switch or AP): name, LED and AP radios. The device must already be " +
			"adopted. Only attributes you set are managed. Destroying the resource leaves the device as it is; it does not forget it.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("Device MAC."),
			"site": siteAttribute(),
			"mac": schema.StringAttribute{
				CustomType: macType{}, Required: true, Description: "Device MAC address, any common format.",
				PlanModifiers: []planmodifier.String{macKeepState{}, stringplanmodifier.RequiresReplace()},
			},
			"type":             schema.StringAttribute{Computed: true, Description: "`gateway`, `switch` or `ap`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"model":            schema.StringAttribute{Computed: true, Description: "Model.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"firmware_version": schema.StringAttribute{Computed: true, Description: "Firmware version at the last refresh.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"ip":               schema.StringAttribute{Computed: true, Description: "Management IP at the last refresh.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":             schema.StringAttribute{Optional: true, Description: "Device name."},
			"led":              schema.StringAttribute{Optional: true, Description: "`on`, `off` or `site` (follow the site setting)."},
			"radio_2g":         radioSchema("2.4 GHz"),
			"radio_5g":         radioSchema("5 GHz"),
		},
	}
}

func (r *deviceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *deviceResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m deviceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if set(m.LED) {
		if _, ok := ledModes[m.LED.ValueString()]; !ok {
			resp.Diagnostics.AddAttributeError(path.Root("led"), "Invalid LED mode", "must be on, off or site")
		}
	}
	for name, ra := range map[string]*radioAttrs{"radio_2g": m.Radio2G, "radio_5g": m.Radio5G} {
		if ra == nil {
			continue
		}
		if set(ra.TxPowerLevel) && (ra.TxPowerLevel.ValueInt64() < 0 || ra.TxPowerLevel.ValueInt64() > 4) {
			resp.Diagnostics.AddAttributeError(path.Root(name).AtName("tx_power_level"), "Invalid power level", "must be 0-4")
		}
		if set(ra.Channel) {
			if _, err := strconv.Atoi(ra.Channel.ValueString()); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root(name).AtName("channel"), "Invalid channel", "must be a number, 0 for auto")
			}
		}
	}
}

func (r *deviceResource) lookup(ctx context.Context, siteID, mac string) (*client.Device, error) {
	devs, err := r.data.client.Devices(ctx, siteID)
	if err != nil {
		return nil, err
	}
	want := client.NormalizeMAC(mac)
	for i := range devs {
		if client.NormalizeMAC(devs[i].MAC) == want {
			return &devs[i], nil
		}
	}
	return nil, &client.NotFoundError{What: fmt.Sprintf("adopted device %s", want)}
}

func mergeRadio(cur map[string]any, ra *radioAttrs) (map[string]any, bool) {
	if ra == nil {
		return nil, false
	}
	out := deepCopyMap(cur)
	ch := false
	if set(ra.Enabled) {
		out["radioEnable"], ch = ra.Enabled.ValueBool(), true
	}
	if set(ra.Channel) {
		out["channel"], ch = ra.Channel.ValueString(), true
	}
	if set(ra.ChannelWidth) {
		out["channelWidth"], ch = ra.ChannelWidth.ValueString(), true
	}
	if set(ra.TxPowerLevel) {
		out["txPowerLevel"], ch = ra.TxPowerLevel.ValueInt64(), true
	}
	return out, ch
}

func (r *deviceResource) apply(ctx context.Context, siteID string, m *deviceModel, diags *diag.Diagnostics) {
	dev, err := r.lookup(ctx, siteID, m.MAC.ValueString())
	if err != nil {
		apiError(diags, "finding device", err)
		return
	}
	if (m.Radio2G != nil || m.Radio5G != nil) && dev.Type != "ap" {
		diags.AddError("Radio settings need an access point", fmt.Sprintf("device %s is a %s", dev.MAC, dev.Type))
		return
	}
	detail, err := r.data.client.DeviceDetail(ctx, siteID, dev.Type, dev.MAC)
	if err != nil {
		apiError(diags, "reading device", err)
		return
	}
	body := map[string]any{}
	if set(m.Name) && m.Name.ValueString() != fmt.Sprint(detail["name"]) {
		body["name"] = m.Name.ValueString()
	}
	if set(m.LED) {
		body["ledSetting"] = ledModes[m.LED.ValueString()]
	}
	if rs, ok := mergeRadio(section(detail, "radioSetting2g"), m.Radio2G); ok {
		body["radioSetting2g"] = rs
	}
	if rs, ok := mergeRadio(section(detail, "radioSetting5g"), m.Radio5G); ok {
		body["radioSetting5g"] = rs
	}
	if len(body) == 0 {
		return
	}
	if err := r.data.client.PatchDevice(ctx, siteID, dev.Type, dev.MAC, body); err != nil {
		apiError(diags, "updating device", err)
	}
}

func readRadio(sec map[string]any, ra *radioAttrs) {
	if ra == nil {
		return
	}
	if !ra.Enabled.IsNull() {
		ra.Enabled = boolField(sec, "radioEnable")
	}
	if !ra.Channel.IsNull() {
		ra.Channel = types.StringValue(fmt.Sprint(sec["channel"]))
	}
	if !ra.ChannelWidth.IsNull() {
		ra.ChannelWidth = types.StringValue(fmt.Sprint(sec["channelWidth"]))
	}
	if !ra.TxPowerLevel.IsNull() {
		ra.TxPowerLevel = int64Field(sec, "txPowerLevel")
	}
}

func (r *deviceResource) read(ctx context.Context, siteID string, m *deviceModel) error {
	dev, err := r.lookup(ctx, siteID, m.MAC.ValueString())
	if err != nil {
		return err
	}
	detail, err := r.data.client.DeviceDetail(ctx, siteID, dev.Type, dev.MAC)
	if err != nil {
		return err
	}
	m.ID = types.StringValue(dev.MAC)
	m.Type = types.StringValue(dev.Type)
	m.Model = types.StringValue(dev.Model)
	m.Firmware = types.StringValue(dev.FirmwareVersion)
	m.IP = types.StringValue(dev.IP)
	if !m.Name.IsNull() {
		m.Name = types.StringValue(fmt.Sprint(detail["name"]))
	}
	if !m.LED.IsNull() {
		led := int64Field(detail, "ledSetting").ValueInt64()
		for k, v := range ledModes {
			if int64(v) == led {
				m.LED = types.StringValue(k)
			}
		}
	}
	readRadio(section(detail, "radioSetting2g"), m.Radio2G)
	readRadio(section(detail, "radioSetting5g"), m.Radio5G)
	return nil
}

func (r *deviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, siteID, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.read(ctx, siteID, &plan); err != nil {
		apiError(&resp.Diagnostics, "reading back device", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	siteID, ok := r.data.mustSite(ctx, state.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	if handleReadErr(ctx, r.read(ctx, siteID, &state), resp, "device") {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *deviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan deviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	siteID, ok := r.data.mustSite(ctx, plan.Site, &resp.Diagnostics)
	if !ok || resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, siteID, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.read(ctx, siteID, &plan); err != nil {
		apiError(&resp.Diagnostics, "reading back device", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deviceResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

// ImportState takes "<mac>" or "<site>/<mac>".
func (r *deviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSiteAndID(ctx, req, resp, "mac")
}
