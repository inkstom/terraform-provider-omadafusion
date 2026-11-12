package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

var _ provider.Provider = &omadaProvider{}

type omadaProvider struct {
	version string
}

// New returns a provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &omadaProvider{version: version} }
}

type providerModel struct {
	URL      types.String `tfsdk:"url"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	Insecure types.Bool   `tfsdk:"insecure"`
	Site     types.String `tfsdk:"site"`
	Timeout  types.Int64  `tfsdk:"timeout_seconds"`
}

// providerData is passed to every resource and data source.
type providerData struct {
	client      *client.Client
	defaultSite string

	mu    sync.Mutex
	sites map[string]string
}

// siteID resolves a site name (or ID) to an ID, defaulting to the provider site.
func (p *providerData) siteID(ctx context.Context, site types.String) (string, error) {
	name := p.defaultSite
	if !site.IsNull() && !site.IsUnknown() && site.ValueString() != "" {
		name = site.ValueString()
	}
	if name == "" {
		return "", fmt.Errorf("no site configured: set `site` on the provider or the resource")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.sites[name]; ok {
		return id, nil
	}
	s, err := p.client.SiteByName(ctx, name)
	if err != nil {
		return "", err
	}
	p.sites[name] = s.ID
	return s.ID, nil
}

func (p *omadaProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "omadafusion"
	resp.Version = p.version
}

func (p *omadaProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a TP-Link Omada controller through its web API (v2), including the controller built into Omada Fusion gateways. " +
			"Authenticates with a local controller account rather than an Open API client.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				Optional:    true,
				Description: "Controller base URL, e.g. `https://192.168.0.1`. Env: `OMADA_URL`.",
			},
			"username": schema.StringAttribute{
				Optional:    true,
				Description: "Local controller account. Use a dedicated, site-limited admin. Env: `OMADA_USERNAME`.",
			},
			"password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Password for `username`. Env: `OMADA_PASSWORD`.",
			},
			"insecure": schema.BoolAttribute{
				Optional:    true,
				Description: "Skip TLS certificate verification. Integrated controllers use a self-signed certificate by default. Env: `OMADA_INSECURE`.",
			},
			"site": schema.StringAttribute{
				Optional:    true,
				Description: "Default site name or ID for resources that do not set `site`. Env: `OMADA_SITE`.",
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional:    true,
				Description: "HTTP timeout per request. Defaults to 60.",
			},
		},
	}
}

func envOr(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func (p *omadaProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	insecure := false
	if !cfg.Insecure.IsNull() && !cfg.Insecure.IsUnknown() {
		insecure = cfg.Insecure.ValueBool()
	} else if v := os.Getenv("OMADA_INSECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("insecure"), "Invalid OMADA_INSECURE", err.Error())
			return
		}
		insecure = b
	}
	timeout := 60 * time.Second
	if !cfg.Timeout.IsNull() && !cfg.Timeout.IsUnknown() && cfg.Timeout.ValueInt64() > 0 {
		timeout = time.Duration(cfg.Timeout.ValueInt64()) * time.Second
	}
	c, err := client.New(client.Config{
		URL:       envOr(cfg.URL, "OMADA_URL"),
		Username:  envOr(cfg.Username, "OMADA_USERNAME"),
		Password:  envOr(cfg.Password, "OMADA_PASSWORD"),
		Insecure:  insecure,
		Timeout:   timeout,
		UserAgent: "terraform-provider-omadafusion/" + p.version,
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}
	data := &providerData{client: c, defaultSite: envOr(cfg.Site, "OMADA_SITE"), sites: map[string]string{}}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *omadaProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newTimeRangeResource,
		newDHCPReservationResource,
		newGatewayACLResource,
		newGroupResource,
		newLANNetworkDHCPDNSResource,
		newSSIDScheduleResource,
		newLANNetworkResource,
		newPortProfileResource,
		newSwitchPortResource,
		newPortForwardingResource,
		newStaticRouteResource,
		newWirelessNetworkResource,
		newGatewayACLOrderResource,
		newWireGuardResource,
		newWireGuardPeerResource,
		newDDNSResource,
		newSiteSettingsResource,
		newAutoBackupResource,
		newAdminUserResource,
		newDeviceResource,
		newRebootScheduleResource,
	}
}

func (p *omadaProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newControllerDataSource,
		newSiteDataSource,
		newLANNetworkDataSource,
		newSSIDDataSource,
		newDevicesDataSource,
		newPortProfileDataSource,
		newWANPortsDataSource,
		newGatewayACLsDataSource,
	}
}

// configureData extracts providerData in Configure methods.
func configureData(raw any, diags *diag.Diagnostics) *providerData {
	if raw == nil {
		return nil
	}
	d, ok := raw.(*providerData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("got %T", raw))
		return nil
	}
	return d
}
