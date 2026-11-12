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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

// ======================= controller admin account =======================

var (
	_ resource.ResourceWithImportState    = &adminUserResource{}
	_ resource.ResourceWithValidateConfig = &adminUserResource{}
)

var adminRoles = map[string]string{"super_admin": "super_admin_id", "admin": "admin_id", "viewer": "viewer_id"}

func newAdminUserResource() resource.Resource { return &adminUserResource{} }

type adminUserResource struct{ data *providerData }

type adminUserModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Password types.String `tfsdk:"password"`
	Role     types.String `tfsdk:"role"`
	Sites    types.Set    `tfsdk:"sites"`
	Alerts   types.Bool   `tfsdk:"alerts"`
}

func (r *adminUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_admin_user"
}

func (r *adminUserResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A local controller account (Account). Cloud (TP-Link ID) accounts are invited by email and are not managed here. " +
			"The password cannot be read back, so changes made outside Terraform are not detected; it is stored in state.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("User ID."),
			"name": schema.StringAttribute{
				Required: true, Description: "Login name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"password": schema.StringAttribute{Required: true, Sensitive: true, Description: "Login password. The controller rejects simple passwords (errorCode -30105); mix letters, digits and a symbol."},
			"role":     schema.StringAttribute{Required: true, Description: "`super_admin`, `admin` or `viewer`."},
			"sites": schema.SetAttribute{
				Optional: true, ElementType: types.StringType,
				Description: "Site names the account may manage. Omit for all sites (including future ones).",
			},
			"alerts": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Send alert emails to this account."},
		},
	}
}

func (r *adminUserResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *adminUserResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m adminUserModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if set(m.Role) {
		if _, ok := adminRoles[m.Role.ValueString()]; !ok {
			resp.Diagnostics.AddAttributeError(path.Root("role"), "Invalid role", "must be super_admin, admin or viewer")
		}
	}
	if set(m.Password) && len(m.Password.ValueString()) < 8 {
		resp.Diagnostics.AddAttributeError(path.Root("password"), "Password too short", "use at least 8 characters")
	}
}

func (r *adminUserResource) body(ctx context.Context, m *adminUserModel, withPassword bool, diags *diag.Diagnostics) map[string]any {
	var siteIDs []string
	if !m.Sites.IsNull() {
		for _, name := range setToStrings(ctx, m.Sites, diags) {
			id, err := r.data.siteID(ctx, types.StringValue(name))
			if err != nil {
				apiError(diags, "resolving site "+name, err)
				return nil
			}
			siteIDs = append(siteIDs, id)
		}
	}
	pw := ""
	if withPassword {
		pw = m.Password.ValueString()
	}
	return client.AdminUserBody(m.Name.ValueString(), adminRoles[m.Role.ValueString()], pw, siteIDs, m.Alerts.ValueBool())
}

func (r *adminUserResource) read(ctx context.Context, m *adminUserModel, diags *diag.Diagnostics) error {
	u, err := r.data.client.AdminUser(ctx, m.ID.ValueString())
	if err != nil {
		return err
	}
	m.Name = types.StringValue(u.Name)
	m.Alerts = types.BoolValue(u.Alert)
	m.Role = types.StringValue(u.RoleID)
	for k, v := range adminRoles {
		if v == u.RoleID {
			m.Role = types.StringValue(k)
		}
	}
	priv, _ := u.Raw["privilege"].(map[string]any)
	if all, _ := priv["all"].(bool); all {
		m.Sites = types.SetNull(types.StringType)
	} else {
		var names []string
		for _, s := range anySlice(priv["sites"]) {
			if sm, ok := s.(map[string]any); ok {
				names = append(names, fmt.Sprint(sm["name"]))
			}
		}
		sort.Strings(names)
		m.Sites = stringsToSet(ctx, names, diags)
	}
	return nil
}

func (r *adminUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan adminUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := r.body(ctx, &plan, true, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.client.CreateAdminUser(ctx, body)
	if err != nil {
		apiError(&resp.Diagnostics, "creating admin user", err)
		return
	}
	plan.ID = types.StringValue(id)
	if err := r.read(ctx, &plan, &resp.Diagnostics); err != nil {
		apiError(&resp.Diagnostics, "reading back admin user", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *adminUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state adminUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if handleReadErr(ctx, r.read(ctx, &state, &resp.Diagnostics), resp, "admin user") {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *adminUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state adminUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	body := r.body(ctx, &plan, !plan.Password.Equal(state.Password), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.UpdateAdminUser(ctx, plan.ID.ValueString(), body); err != nil {
		apiError(&resp.Diagnostics, "updating admin user", err)
		return
	}
	if err := r.read(ctx, &plan, &resp.Diagnostics); err != nil {
		apiError(&resp.Diagnostics, "reading back admin user", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *adminUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state adminUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.data.client.DeleteAdminUser(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "deleting admin user", err)
	}
}

// ImportState takes the user ID. The password is not readable, so set it in
// configuration; the first apply after import sends it.
func (r *adminUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
