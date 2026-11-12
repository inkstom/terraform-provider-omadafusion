package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
)

// macType is a string type whose values compare equal when they are the same
// MAC address in different formats (aa:bb:cc:dd:ee:ff vs AA-BB-CC-DD-EE-FF).
// With semantic equality the framework keeps the prior value instead of
// planning a change, so imports and formatting edits do not force
// replacement.
type macType struct{ basetypes.StringType }

var (
	_ basetypes.StringTypable                    = macType{}
	_ basetypes.StringValuableWithSemanticEquals = macValue{}
)

func (t macType) Equal(o attr.Type) bool {
	_, ok := o.(macType)
	return ok
}

func (t macType) String() string { return "macType" }

func (t macType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return macValue{StringValue: in}, nil
}

func (t macType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	v, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	s, ok := v.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", v)
	}
	return macValue{StringValue: s}, nil
}

func (t macType) ValueType(context.Context) attr.Value { return macValue{} }

type macValue struct{ basetypes.StringValue }

func (v macValue) Equal(o attr.Value) bool {
	other, ok := o.(macValue)
	return ok && v.StringValue.Equal(other.StringValue)
}

func (v macValue) Type(context.Context) attr.Type { return macType{} }

func (v macValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	nv, ok := newValuable.(macValue)
	if !ok {
		return false, nil
	}
	return client.NormalizeMAC(v.ValueString()) == client.NormalizeMAC(nv.ValueString()), nil
}

func newMACValue(s string) macValue { return macValue{StringValue: basetypes.NewStringValue(s)} }
