package provider

import (
	"context"
	"encoding/json"

	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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

func remarshalAny(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func deepCopyMap(m map[string]any) map[string]any {
	var out map[string]any
	_ = remarshalAny(m, &out)
	return out
}

func stringsFromAny(v any) []string {
	out := []string{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

func setToStrings(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	out := []string{}
	if s.IsNull() || s.IsUnknown() {
		return out
	}
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	return out
}

func stringsToSet(ctx context.Context, in []string, diags *diag.Diagnostics) types.Set {
	if in == nil {
		in = []string{}
	}
	s, d := types.SetValueFrom(ctx, types.StringType, in)
	diags.Append(d...)
	return s
}

// siteFromState resolves the site for CRUD methods and reports errors.
func (p *providerData) mustSite(ctx context.Context, site types.String, diags *diag.Diagnostics) (string, bool) {
	id, err := p.siteID(ctx, site)
	if err != nil {
		apiError(diags, "resolving site", err)
		return "", false
	}
	return id, true
}

// knownElements returns the known string elements of a list or set and
// whether every element was known. Validators must skip unknown elements:
// during validate, values such as for_each keys are still unknown and
// ElementsAs would report them as "".
func knownElements(elems []attr.Value) ([]string, bool) {
	out := make([]string, 0, len(elems))
	all := true
	for _, e := range elems {
		s, ok := e.(types.String)
		if !ok || s.IsUnknown() || s.IsNull() {
			all = false
			continue
		}
		out = append(out, s.ValueString())
	}
	return out, all
}
