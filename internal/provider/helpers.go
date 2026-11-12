package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

// siteAttribute is the optional per-resource site override.
func siteAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Optional:      true,
		Description:   "Site name or ID. Defaults to the provider `site`. Changing it forces a new resource.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func idAttribute(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		Computed:      true,
		Description:   desc,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// importSiteAndID accepts "<id>" or "<site>/<id>" and writes site and the
// given ID attribute into state.
func importSiteAndID(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, idAttr string) {
	site, id := "", req.ID
	if i := strings.LastIndex(req.ID, "/"); i >= 0 {
		site, id = req.ID[:i], req.ID[i+1:]
	}
	if id == "" {
		resp.Diagnostics.AddError("Invalid import ID", `expected "<id>" or "<site>/<id>"`)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(idAttr), id)...)
	if site != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("site"), site)...)
	}
}

func notConfigured(diags *diag.Diagnostics) {
	diags.AddError("Provider not configured", "The provider has not been configured; this is a bug in the provider or Terraform.")
}

func apiError(diags *diag.Diagnostics, action string, err error) {
	diags.AddError(fmt.Sprintf("Omada API error: %s", action), err.Error())
}

// handleReadErr removes the resource from state when it no longer exists.
func handleReadErr(ctx context.Context, err error, resp *resource.ReadResponse, what string) bool {
	if err == nil {
		return false
	}
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return true
	}
	apiError(&resp.Diagnostics, "reading "+what, err)
	return true
}

func stringsToList(ctx context.Context, in []string, diags *diag.Diagnostics) types.List {
	if in == nil {
		in = []string{}
	}
	l, d := types.ListValueFrom(ctx, types.StringType, in)
	diags.Append(d...)
	return l
}

func listToStrings(ctx context.Context, l types.List, diags *diag.Diagnostics) []string {
	out := []string{}
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	diags.Append(l.ElementsAs(ctx, &out, false)...)
	return out
}

func pathRoot(name string) path.Path { return path.Root(name) }

func isNotFound(err error) bool { return client.IsNotFound(err) }
