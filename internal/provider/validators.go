package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var httpsURLRE = regexp.MustCompile(`^https://[^\s/?#]+[^\s]*$`)

// headersValidator checks custom webhook headers the way the API does:
// at most 50, no reserved or svix- names, no line breaks, 8 KB in total.
type headersValidator struct{}

func (headersValidator) Description(context.Context) string {
	return "at most 50 headers; reserved names (Content-Type, Host, User-Agent, svix-*, ...) cannot be set"
}
func (v headersValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (headersValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	elems := req.ConfigValue.Elements()
	if len(elems) > 50 {
		resp.Diagnostics.AddAttributeError(req.Path, "Too many headers", "A webhook can have at most 50 custom headers.")
		return
	}
	size := 0
	for k, v := range elems {
		lk := strings.ToLower(k)
		if k == "" || len(k) > 256 || strings.HasPrefix(lk, "svix-") || reservedHeaders[lk] || strings.ContainsAny(k, " :\r\n\t") {
			resp.Diagnostics.AddAttributeError(req.Path.AtMapKey(k), "Header cannot be set", fmt.Sprintf("The header %q cannot be set on a webhook.", k))
			continue
		}
		s, ok := v.(types.String)
		if !ok || s.IsUnknown() {
			continue
		}
		if strings.ContainsAny(s.ValueString(), "\r\n") {
			resp.Diagnostics.AddAttributeError(req.Path.AtMapKey(k), "Invalid header value", "Header values cannot contain line breaks.")
		}
		size += len(k) + len(s.ValueString())
	}
	if size > 8*1024 {
		resp.Diagnostics.AddAttributeError(req.Path, "Headers too large", "Custom headers must be at most 8 KB in total.")
	}
}

// encodeFallback turns a fallback_value string into the JSON value the API
// stores for a variable or property of type typ: a number for number, true
// or false for boolean, parsed JSON for object and array, else the string.
func encodeFallback(typ, s string) (any, error) {
	switch typ {
	case "number":
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, fmt.Errorf("fallback_value %q is not a number", s)
		}
		return f, nil
	case "boolean":
		b, err := strconv.ParseBool(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("fallback_value %q is not true or false", s)
		}
		return b, nil
	case "object", "array":
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, fmt.Errorf("fallback_value must be JSON for type %s: %v", typ, err)
		}
		return v, nil
	}
	return s, nil
}

// decodeFallback is the reverse of encodeFallback: the stored JSON value as
// the string Terraform keeps. prior is returned when it encodes the same value
// (so "1.50" in the configuration is not drift against 1.5).
func decodeFallback(typ string, raw json.RawMessage, prior types.String) types.String {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull()
	}
	if !prior.IsNull() && !prior.IsUnknown() {
		if want, err := encodeFallback(typ, prior.ValueString()); err == nil {
			var got any
			if json.Unmarshal(raw, &got) == nil && jsonEqual(want, got) {
				return prior
			}
		}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return types.StringValue(s)
	}
	return types.StringValue(string(raw))
}

// notPadded refuses leading or trailing spaces (the API trims them, which
// would read back as drift).
type notPadded struct{}

func (notPadded) Description(context.Context) string {
	return "must not start or end with spaces"
}
func (v notPadded) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (v notPadded) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if s := req.ConfigValue.ValueString(); strings.TrimSpace(s) != s {
		resp.Diagnostics.AddAttributeError(req.Path, "Leading or trailing spaces", "The value "+v.Description(ctx)+".")
	}
}

// emailValidator accepts one email address.
type emailValidator struct{}

func (emailValidator) Description(context.Context) string { return "must be an email address" }
func (v emailValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (emailValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	a, err := mail.ParseAddress(s)
	if err != nil || !strings.Contains(a.Address, "@") || strings.ContainsAny(s, "\r\n") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid email address", fmt.Sprintf("%q is not an email address.", s))
	}
}

// rfc3339Validator accepts an RFC 3339 timestamp.
type rfc3339Validator struct{}

func (rfc3339Validator) Description(context.Context) string {
	return "must be an RFC 3339 timestamp such as 2030-01-02T15:04:05Z"
}
func (v rfc3339Validator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v rfc3339Validator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp", v.Description(ctx)+".")
	}
}

func jsonEqual(a, b any) bool {
	ja, ea := json.Marshal(a)
	jb, eb := json.Marshal(b)
	return ea == nil && eb == nil && string(ja) == string(jb)
}
